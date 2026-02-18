package runtime

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"echohelix/internal/events"
	adapterrpc "echohelix/internal/rpc/adapter"
)

const maxScanTokenSize = 4 * 1024 * 1024

type NormalizedEvent struct {
	Type    string
	Channel string
	Format  string
	Role    string
	Payload map[string]any
}

type Mapper func(line string, source string) (NormalizedEvent, bool)

type RunOptionsApplier func(args []string, req *adapterrpc.StartRunRequest) []string

type PromptArgApplier func(args []string, mode string, prompt string) []string

type EventDowngrader func(ne NormalizedEvent, schemaVersion string) NormalizedEvent

type Config struct {
	Backend        string
	Mapper         Mapper
	ApplyRunOption RunOptionsApplier
	ApplyPromptArg PromptArgApplier
	Downgrade      EventDowngrader

	CLIBinEnv      string
	CLIBinDefault  string
	CLIArgsEnv     string
	CLIArgsDefault string
	CLIModeEnv     string
	CLIModeDefault string

	EventTypes             []string
	SupportsCancel         bool
	SupportsPTY            bool
	SchemaVersions         []string
	PreferredSchemaVersion string
	CompatFields           []string
}

type Server struct {
	cfg Config

	mu   sync.RWMutex
	runs map[string]*runState
}

type runState struct {
	runID         string
	schemaVersion string
	backend       string
	downgrade     EventDowngrader

	mu      sync.RWMutex
	seq     int64
	history []*adapterrpc.AgentEvent
	subs    map[chan *adapterrpc.AgentEvent]struct{}
	closed  bool

	cancel context.CancelFunc
	cmd    *exec.Cmd
}

func NewServer(cfg Config) *Server {
	if cfg.CLIBinDefault == "" {
		cfg.CLIBinDefault = cfg.Backend
	}
	if cfg.CLIModeDefault == "" {
		cfg.CLIModeDefault = "args"
	}
	if cfg.CLIArgsDefault == "" {
		cfg.CLIArgsDefault = ""
	}
	if cfg.SchemaVersions == nil {
		cfg.SchemaVersions = []string{"v1", "v2"}
	}
	if cfg.PreferredSchemaVersion == "" {
		cfg.PreferredSchemaVersion = "v2"
	}
	if cfg.CompatFields == nil {
		cfg.CompatFields = []string{"text", "status", "is_error"}
	}
	if cfg.EventTypes == nil {
		cfg.EventTypes = []string{"token", "tool_call", "tool_result", "patch", "status", "done", "error"}
	}

	return &Server{
		cfg:  cfg,
		runs: map[string]*runState{},
	}
}

func (s *Server) StartRun(ctx context.Context, req *adapterrpc.StartRunRequest) (*adapterrpc.StartRunResponse, error) {
	if req.RunID == "" || req.WorkspacePath == "" || req.Prompt == "" {
		return &adapterrpc.StartRunResponse{Accepted: false, Error: "run_id/workspace_path/prompt are required"}, nil
	}
	schemaVersion := req.SchemaVersion
	if schemaVersion == "" {
		schemaVersion = events.SchemaVersionV2
	}
	if schemaVersion != events.SchemaVersionV1 && schemaVersion != events.SchemaVersionV2 {
		return &adapterrpc.StartRunResponse{Accepted: false, Error: "unsupported schema_version"}, nil
	}

	s.mu.Lock()
	if _, exists := s.runs[req.RunID]; exists {
		s.mu.Unlock()
		return &adapterrpc.StartRunResponse{Accepted: false, Error: "run already exists"}, nil
	}

	runCtx := context.Background()
	var cancel context.CancelFunc
	if req.TimeoutSec > 0 {
		runCtx, cancel = context.WithTimeout(runCtx, time.Duration(req.TimeoutSec)*time.Second)
	} else {
		runCtx, cancel = context.WithCancel(runCtx)
	}

	rs := &runState{
		runID:         req.RunID,
		schemaVersion: schemaVersion,
		backend:       s.cfg.Backend,
		downgrade:     s.cfg.Downgrade,
		subs:          map[chan *adapterrpc.AgentEvent]struct{}{},
		history:       make([]*adapterrpc.AgentEvent, 0, 128),
		cancel:        cancel,
	}
	s.runs[req.RunID] = rs
	s.mu.Unlock()

	go s.execute(runCtx, rs, req)
	return &adapterrpc.StartRunResponse{Accepted: true}, nil
}

func (s *Server) StreamEvents(req *adapterrpc.StreamEventsRequest, stream adapterrpc.AdapterStreamEventsServer) error {
	rs, err := s.getRun(req.RunID)
	if err != nil {
		return err
	}

	history, ch, unsub := rs.subscribe()
	defer unsub()

	for _, ev := range history {
		if err := stream.Send(ev); err != nil {
			return err
		}
	}
	for ev := range ch {
		if err := stream.Send(ev); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) CancelRun(ctx context.Context, req *adapterrpc.CancelRunRequest) (*adapterrpc.CancelRunResponse, error) {
	rs, err := s.getRun(req.RunID)
	if err != nil {
		return &adapterrpc.CancelRunResponse{Cancelled: false, Error: err.Error()}, nil
	}
	rs.cancel()
	return &adapterrpc.CancelRunResponse{Cancelled: true}, nil
}

func (s *Server) Health(context.Context, *adapterrpc.HealthRequest) (*adapterrpc.HealthResponse, error) {
	return &adapterrpc.HealthResponse{OK: true, Message: "ok"}, nil
}

func (s *Server) Capabilities(context.Context, *adapterrpc.CapabilitiesRequest) (*adapterrpc.CapabilitiesResponse, error) {
	return &adapterrpc.CapabilitiesResponse{
		Backend:                s.cfg.Backend,
		EventTypes:             s.cfg.EventTypes,
		SupportsCancel:         s.cfg.SupportsCancel,
		SupportsPTY:            s.cfg.SupportsPTY,
		SchemaVersions:         s.cfg.SchemaVersions,
		PreferredSchemaVersion: s.cfg.PreferredSchemaVersion,
		CompatFields:           s.cfg.CompatFields,
	}, nil
}

func (s *Server) getRun(runID string) (*runState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rs, ok := s.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run %s not found", runID)
	}
	return rs, nil
}

func (s *Server) execute(ctx context.Context, rs *runState, req *adapterrpc.StartRunRequest) {
	defer func() {
		// Keep short-lived history for reconnect/replay, then cleanup.
		time.AfterFunc(10*time.Minute, func() {
			s.mu.Lock()
			delete(s.runs, req.RunID)
			s.mu.Unlock()
		})
	}()

	rs.publish(NormalizedEvent{
		Type:    "status",
		Channel: "system",
		Format:  "json",
		Role:    "system",
		Payload: map[string]any{"status": "running"},
	}, "adapter")

	bin := env(s.cfg.CLIBinEnv, s.cfg.CLIBinDefault)
	args := append([]string{}, strings.Fields(env(s.cfg.CLIArgsEnv, s.cfg.CLIArgsDefault))...)
	mode := env(s.cfg.CLIModeEnv, s.cfg.CLIModeDefault)
	if s.cfg.ApplyRunOption != nil {
		args = s.cfg.ApplyRunOption(args, req)
	}
	if s.cfg.ApplyPromptArg != nil {
		args = s.cfg.ApplyPromptArg(args, mode, req.Prompt)
	} else if mode != "stdin" {
		args = append(args, req.Prompt)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = req.WorkspacePath

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		rs.publish(NormalizedEvent{
			Type:    "error",
			Channel: "system",
			Format:  "plain",
			Role:    "system",
			Payload: map[string]any{"message": err.Error()},
		}, "adapter")
		rs.finish()
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		rs.publish(NormalizedEvent{
			Type:    "error",
			Channel: "system",
			Format:  "plain",
			Role:    "system",
			Payload: map[string]any{"message": err.Error()},
		}, "adapter")
		rs.finish()
		return
	}

	var stdin io.WriteCloser
	if mode == "stdin" {
		in, err := cmd.StdinPipe()
		if err != nil {
			rs.publish(NormalizedEvent{
				Type:    "error",
				Channel: "system",
				Format:  "plain",
				Role:    "system",
				Payload: map[string]any{"message": err.Error()},
			}, "adapter")
			rs.finish()
			return
		}
		stdin = in
	} else {
		// Avoid blocking when CLI reads stdin unexpectedly.
		cmd.Stdin = strings.NewReader("")
	}

	if err := cmd.Start(); err != nil {
		rs.publish(NormalizedEvent{
			Type:    "error",
			Channel: "system",
			Format:  "plain",
			Role:    "system",
			Payload: map[string]any{"message": err.Error()},
		}, "adapter")
		rs.finish()
		return
	}
	rs.setCmd(cmd)

	if stdin != nil {
		_, _ = stdin.Write([]byte(req.Prompt))
		_, _ = stdin.Write([]byte("\n"))
		_ = stdin.Close()
	}

	var wg sync.WaitGroup
	var sawDone atomic.Bool
	mdAssembler := &markdownAssembler{}
	wg.Add(2)
	go scanPipe(stdout, func(line string) {
		ev, ok := s.cfg.Mapper(line, "stdout")
		if !ok {
			return
		}
		if ev.Type == "token" && ev.Channel == "final" && ev.Format == "markdown" {
			text, _ := ev.Payload["text"].(string)
			merged, ready := mdAssembler.Push(text)
			if !ready {
				return
			}
			ev.Payload["text"] = merged
		}
		if ev.Type == "done" {
			sawDone.Store(true)
		}
		rs.publish(ev, "stdout")
	}, &wg)
	go scanPipe(stderr, func(line string) {
		ev, ok := s.cfg.Mapper(line, "stderr")
		if !ok {
			return
		}
		if ev.Type == "done" {
			sawDone.Store(true)
		}
		rs.publish(ev, "stderr")
	}, &wg)

	waitErr := cmd.Wait()
	wg.Wait()
	if merged, ok := mdAssembler.Flush(); ok {
		rs.publish(NormalizedEvent{
			Type:    "token",
			Channel: "final",
			Format:  "markdown",
			Role:    "assistant",
			Payload: map[string]any{"text": merged, "flushed": true},
		}, "stdout")
	}

	if waitErr != nil {
		rs.publish(NormalizedEvent{
			Type:    "error",
			Channel: "system",
			Format:  "plain",
			Role:    "system",
			Payload: map[string]any{"message": waitErr.Error()},
		}, "adapter")
	}
	if !sawDone.Load() {
		rs.publish(NormalizedEvent{
			Type:    "done",
			Channel: "system",
			Format:  "json",
			Role:    "system",
			Payload: map[string]any{"status": "completed"},
		}, "adapter")
	}
	rs.finish()
}

func (r *runState) setCmd(cmd *exec.Cmd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmd = cmd
}

func (r *runState) subscribe() ([]*adapterrpc.AgentEvent, <-chan *adapterrpc.AgentEvent, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	history := make([]*adapterrpc.AgentEvent, len(r.history))
	copy(history, r.history)

	ch := make(chan *adapterrpc.AgentEvent, 128)
	if r.closed {
		close(ch)
		return history, ch, func() {}
	}
	r.subs[ch] = struct{}{}
	unsub := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if _, ok := r.subs[ch]; ok {
			delete(r.subs, ch)
			close(ch)
		}
	}
	return history, ch, unsub
}

func (r *runState) publish(ne NormalizedEvent, source string) {
	if r.downgrade != nil {
		ne = r.downgrade(ne, r.schemaVersion)
	}

	bridgeEv := events.Event{
		RunID:         r.runID,
		SchemaVersion: r.schemaVersion,
		Type:          ne.Type,
		Channel:       ne.Channel,
		Format:        ne.Format,
		Role:          ne.Role,
		Payload:       ne.Payload,
		Backend:       r.backend,
		Source:        source,
	}
	events.NormalizeEvent(&bridgeEv)

	var compatText string
	var compatStatus string
	var compatIsError bool
	if bridgeEv.Compat != nil {
		compatText = bridgeEv.Compat.Text
		compatStatus = bridgeEv.Compat.Status
		compatIsError = bridgeEv.Compat.IsError
	}

	ev := &adapterrpc.AgentEvent{
		RunID:         r.runID,
		Seq:           atomic.AddInt64(&r.seq, 1),
		TsUnix:        time.Now().Unix(),
		SchemaVersion: bridgeEv.SchemaVersion,
		Type:          bridgeEv.Type,
		Channel:       bridgeEv.Channel,
		Format:        bridgeEv.Format,
		Role:          bridgeEv.Role,
		CompatText:    compatText,
		CompatStatus:  compatStatus,
		CompatIsError: compatIsError,
		Payload:       bridgeEv.Payload,
		Source:        source,
	}

	r.mu.Lock()
	if !r.closed {
		r.history = append(r.history, ev)
		if len(r.history) > 2048 {
			r.history = r.history[len(r.history)-2048:]
		}
		for sub := range r.subs {
			select {
			case sub <- ev:
			default:
			}
		}
	}
	r.mu.Unlock()
}

func (r *runState) finish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	for sub := range r.subs {
		close(sub)
		delete(r.subs, sub)
	}
}

func scanPipe(reader io.Reader, onLine func(string), wg *sync.WaitGroup) {
	defer wg.Done()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScanTokenSize)
	for scanner.Scan() {
		onLine(scanner.Text())
	}
}

func env(k, def string) string {
	if k == "" {
		return def
	}
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type markdownAssembler struct {
	buf         strings.Builder
	fenceParity int
}

func (m *markdownAssembler) Push(text string) (string, bool) {
	if text == "" {
		return "", false
	}
	if m.buf.Len() > 0 {
		m.buf.WriteString("\n")
	}
	m.buf.WriteString(text)
	if strings.Count(text, "```")%2 == 1 {
		m.fenceParity ^= 1
	}
	if m.fenceParity == 0 {
		out := m.buf.String()
		m.buf.Reset()
		return out, true
	}
	return "", false
}

func (m *markdownAssembler) Flush() (string, bool) {
	if m.buf.Len() == 0 {
		return "", false
	}
	out := m.buf.String()
	m.buf.Reset()
	m.fenceParity = 0
	return out, true
}

func DowngradeLegacyTooling(ne NormalizedEvent, schemaVersion string) NormalizedEvent {
	if schemaVersion != events.SchemaVersionV1 {
		return ne
	}

	switch ne.Type {
	case events.TypeToolCall, events.TypeToolResult, events.TypePatch:
		return NormalizedEvent{
			Type:    events.TypeToken,
			Channel: events.ChannelWorking,
			Format:  events.FormatPlain,
			Role:    events.RoleAssistant,
			Payload: map[string]any{
				"text":        fmt.Sprintf("[%s]", ne.Type),
				"legacy_type": ne.Type,
				"raw":         ne.Payload,
			},
		}
	default:
		return ne
	}
}
