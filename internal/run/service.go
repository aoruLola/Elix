package run

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"echohelix/internal/driver"
	"echohelix/internal/events"
	"echohelix/internal/ledger"
	"echohelix/internal/policy"

	"github.com/google/uuid"
)

type Service struct {
	ledger        *ledger.Store
	registry      *driver.Registry
	hub           *Hub
	policy        *policy.Policy
	runTimeout    time.Duration
	maxConcurrent int
	slots         chan struct{}

	mu     sync.Mutex
	active map[string]*activeRun

	dailyTokenQuota map[string]int64
	fileStoreDir    string
	maxUploadBytes  int64
	emergency       EmergencyState
}

type activeRun struct {
	driver        driver.Driver
	cancel        context.CancelFunc
	seq           int64
	status        string
	schemaVersion string
	backend       string
}

var ErrEmergencyStopActive = errors.New("bridge emergency stop is active")

func NewService(
	ledgerStore *ledger.Store,
	registry *driver.Registry,
	hub *Hub,
	p *policy.Policy,
	runTimeout time.Duration,
	maxConcurrent int,
) *Service {
	if maxConcurrent <= 0 {
		maxConcurrent = 32
	}
	defaultFileStoreDir := filepath.Join(os.TempDir(), "echohelix-files")
	return &Service{
		ledger:          ledgerStore,
		registry:        registry,
		hub:             hub,
		policy:          p,
		runTimeout:      runTimeout,
		maxConcurrent:   maxConcurrent,
		slots:           make(chan struct{}, maxConcurrent),
		active:          map[string]*activeRun{},
		dailyTokenQuota: map[string]int64{},
		fileStoreDir:    defaultFileStoreDir,
		maxUploadBytes:  20 * 1024 * 1024,
	}
}

func (s *Service) Submit(ctx context.Context, req SubmitRequest) (Run, error) {
	if req.Backend == "" {
		req.Backend = "codex"
	}
	if s.isEmergencyActive() {
		return Run{}, ErrEmergencyStopActive
	}
	if req.Prompt == "" {
		return Run{}, fmt.Errorf("prompt is required")
	}
	if err := s.policy.ValidateWorkspace(req.WorkspacePath); err != nil {
		return Run{}, err
	}
	if err := s.policy.ValidateRunOptions(policy.RunOptions{
		Model:         req.Options.Model,
		Profile:       req.Options.Profile,
		Sandbox:       req.Options.Sandbox,
		SchemaVersion: req.Options.SchemaVersion,
	}); err != nil {
		return Run{}, err
	}
	drv, err := s.registry.Get(req.Backend)
	if err != nil {
		return Run{}, err
	}
	caps, err := drv.Capabilities(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("resolve backend capabilities: %w", err)
	}
	negotiated, err := negotiateSchemaVersion(req.Backend, req.Options.SchemaVersion, caps)
	if err != nil {
		return Run{}, err
	}
	req.Options.SchemaVersion = negotiated
	runID := uuid.NewString()
	rewrittenPrompt, rewrittenContext, attachments, err := s.prepareAttachments(ctx, runID, req.WorkspacePath, req.Prompt, req.Context)
	if err != nil {
		return Run{}, err
	}
	req.Prompt = rewrittenPrompt
	req.Context = rewrittenContext

	now := time.Now().UTC()
	r := Run{
		ID:          runID,
		WorkspaceID: req.WorkspaceID,
		Workspace:   req.WorkspacePath,
		Backend:     req.Backend,
		Prompt:      req.Prompt,
		Context:     req.Context,
		Options:     req.Options,
		Attachments: attachments,
		Status:      StatusQueued,
		Terminal:    deriveTerminalInfo(StatusQueued, ""),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.ledger.CreateRun(ctx, ledger.RunRecord{
		ID:          r.ID,
		WorkspaceID: r.WorkspaceID,
		Workspace:   r.Workspace,
		Backend:     r.Backend,
		Prompt:      r.Prompt,
		Context:     r.Context,
		Options: ledger.RunOptionsRecord{
			Model:         r.Options.Model,
			Profile:       r.Options.Profile,
			Sandbox:       r.Options.Sandbox,
			SchemaVersion: r.Options.SchemaVersion,
		},
		Status:    r.Status,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}); err != nil {
		return Run{}, err
	}

	go s.executeRun(r, drv)
	return r, nil
}

func (s *Service) executeRun(r Run, drv driver.Driver) {
	s.slots <- struct{}{}
	defer func() { <-s.slots }()

	// Run may be cancelled before worker gets a slot.
	if rec, err := s.ledger.GetRun(context.Background(), r.ID); err == nil && isTerminalStatus(rec.Status) {
		return
	}

	runCtx, cancel := context.WithTimeout(context.Background(), s.runTimeout)
	defer cancel()

	s.mu.Lock()
	s.active[r.ID] = &activeRun{
		driver:        drv,
		cancel:        cancel,
		seq:           1,
		status:        StatusQueued,
		schemaVersion: r.Options.SchemaVersion,
		backend:       r.Backend,
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.active, r.ID)
		s.mu.Unlock()
	}()

	s.setStatus(runCtx, r.ID, StatusRunning, "")
	s.emit(runCtx, r.ID, r.Backend, "bridge", events.TypeStatus, map[string]any{"status": StatusRunning})

	stream, err := drv.StartRun(runCtx, driver.StartRequest{
		RunID:         r.ID,
		WorkspaceID:   r.WorkspaceID,
		WorkspacePath: r.Workspace,
		Prompt:        r.Prompt,
		Context:       r.Context,
		Options: driver.RunOptions{
			Model:         r.Options.Model,
			Profile:       r.Options.Profile,
			Sandbox:       r.Options.Sandbox,
			SchemaVersion: r.Options.SchemaVersion,
		},
	})
	if err != nil {
		s.setStatus(runCtx, r.ID, StatusFailed, err.Error())
		s.emit(runCtx, r.ID, r.Backend, "bridge", events.TypeError, map[string]any{"message": err.Error()})
		return
	}

	s.setStatus(runCtx, r.ID, StatusStreaming, "")
	s.emit(runCtx, r.ID, r.Backend, "bridge", events.TypeStatus, map[string]any{"status": StatusStreaming})

	sawDone := false
	sawError := false
	doneReceived := false
	var doneErr error
	for {
		if doneReceived && stream.Events == nil {
			if doneErr != nil {
				st := s.currentStatus(r.ID)
				if st != StatusCancelled && st != StatusCancelling {
					s.setStatus(runCtx, r.ID, StatusFailed, doneErr.Error())
					s.emit(runCtx, r.ID, r.Backend, "bridge", events.TypeError, map[string]any{"message": doneErr.Error()})
				}
				return
			}
			if !sawDone {
				st := s.currentStatus(r.ID)
				if st == StatusCancelled || st == StatusCancelling {
					return
				}
				if s.currentStatus(r.ID) == StatusFailed || sawError {
					if s.currentStatus(r.ID) != StatusFailed {
						s.setStatus(runCtx, r.ID, StatusFailed, "run finished after error event")
					}
					return
				}
				s.setStatus(runCtx, r.ID, StatusCompleted, "")
				s.emit(runCtx, r.ID, r.Backend, "bridge", events.TypeDone, map[string]any{"status": StatusCompleted})
			}
			return
		}

		select {
		case <-runCtx.Done():
			errText := runCtx.Err().Error()
			st := s.currentStatus(r.ID)
			if st != StatusCancelled && st != StatusCancelling {
				s.setStatus(context.Background(), r.ID, StatusFailed, errText)
				s.emit(context.Background(), r.ID, r.Backend, "bridge", events.TypeError, map[string]any{"message": errText})
			}
			return
		case ev, ok := <-stream.Events:
			if !ok {
				stream.Events = nil
				continue
			}
			ev.RunID = r.ID
			ev.Backend = r.Backend
			if ev.TS.IsZero() {
				ev.TS = time.Now().UTC()
			}
			ev.Seq = s.nextSeq(runCtx, r.ID)
			events.NormalizeEvent(&ev)
			if err := events.ValidateEvent(ev); err != nil {
				s.emit(runCtx, r.ID, r.Backend, "bridge", events.TypeError, map[string]any{"message": "invalid event contract", "detail": err.Error()})
				continue
			}

			switch ev.Type {
			case events.TypeError:
				sawError = true
				st := s.currentStatus(r.ID)
				if st != StatusCancelled && st != StatusCancelling {
					s.setStatus(runCtx, r.ID, StatusFailed, eventErrorMessage(ev.Payload))
				}
			case events.TypeDone:
				sawDone = true
				st := s.currentStatus(r.ID)
				if st != StatusCancelled && st != StatusCancelling {
					status, errText := terminalStatusFromDone(ev.Payload, sawError)
					if !(st == StatusFailed && status == StatusFailed) {
						s.setStatus(runCtx, r.ID, status, errText)
					}
				}
				s.recordTokenUsage(runCtx, r.ID, r.Backend, ev.Payload)
			}

			_ = s.ledger.AppendEvent(runCtx, ev)
			s.hub.Publish(ev)
		case dErr, ok := <-stream.Done:
			if !ok {
				doneReceived = true
				stream.Done = nil
				continue
			}
			doneReceived = true
			doneErr = dErr
			stream.Done = nil
		}
	}
}

func (s *Service) Cancel(ctx context.Context, runID string) error {
	storageCtx := context.Background()
	rec, err := s.ledger.GetRun(storageCtx, runID)
	if err != nil {
		return err
	}
	if isTerminalStatus(rec.Status) {
		if rec.Status == StatusCancelled {
			return nil
		}
		return fmt.Errorf("run is already %s", rec.Status)
	}

	s.mu.Lock()
	ar := s.active[runID]
	if ar != nil {
		if isTerminalStatus(ar.status) {
			status := ar.status
			s.mu.Unlock()
			if status == StatusCancelled {
				return nil
			}
			return fmt.Errorf("run is already %s", status)
		}
	}
	s.mu.Unlock()

	if ar == nil {
		updated, err := s.setStatusIfNotTerminal(storageCtx, runID, StatusCancelled, "")
		if err != nil {
			return err
		}
		if !updated {
			return s.cancelTerminalConflict(runID)
		}
		// Run can still be queued (not active yet); mark cancelled directly.
		s.emit(storageCtx, runID, rec.Backend, "bridge", events.TypeStatus, map[string]any{"status": StatusCancelled})
		return nil
	}

	updated, err := s.setStatusIfNotTerminal(storageCtx, runID, StatusCancelling, "")
	if err != nil {
		return err
	}
	if !updated {
		return s.cancelTerminalConflict(runID)
	}
	s.emit(storageCtx, runID, rec.Backend, "bridge", events.TypeStatus, map[string]any{"status": StatusCancelling})
	if err := ar.driver.Cancel(ctx, runID); err != nil {
		log.Printf("cancel driver run=%s: %v", runID, err)
	}
	ar.cancel()

	updated, err = s.setStatusIfNotTerminal(storageCtx, runID, StatusCancelled, "")
	if err != nil {
		return err
	}
	if !updated {
		return s.cancelTerminalConflict(runID)
	}
	s.emit(storageCtx, runID, rec.Backend, "bridge", events.TypeStatus, map[string]any{"status": StatusCancelled})
	return nil
}

func (s *Service) GetRun(ctx context.Context, runID string) (Run, error) {
	rec, err := s.ledger.GetRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	out := Run{
		ID:          rec.ID,
		WorkspaceID: rec.WorkspaceID,
		Workspace:   rec.Workspace,
		Backend:     rec.Backend,
		Prompt:      rec.Prompt,
		Context:     rec.Context,
		Options: RunOptions{
			Model:         rec.Options.Model,
			Profile:       rec.Options.Profile,
			Sandbox:       rec.Options.Sandbox,
			SchemaVersion: rec.Options.SchemaVersion,
		},
		Status:    rec.Status,
		Error:     rec.Error,
		Terminal:  deriveTerminalInfo(rec.Status, rec.Error),
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	}
	atts, err := s.ledger.ListRunAttachments(ctx, runID)
	if err == nil && len(atts) > 0 {
		out.Attachments = make([]RunAttachment, 0, len(atts))
		for _, item := range atts {
			out.Attachments = append(out.Attachments, RunAttachment{
				FileID: item.FileID,
				Alias:  item.Alias,
				Path:   "./" + item.MaterializedPath,
			})
		}
	}
	return out, nil
}

func negotiateSchemaVersion(backend string, requested string, caps driver.CapabilitySet) (string, error) {
	selected := requested
	if selected == "" {
		if caps.PreferredSchemaVersion != "" {
			selected = caps.PreferredSchemaVersion
		} else if len(caps.SchemaVersions) > 0 {
			selected = caps.SchemaVersions[len(caps.SchemaVersions)-1]
		} else {
			selected = events.SchemaVersionV2
		}
	}
	switch selected {
	case events.SchemaVersionV1, events.SchemaVersionV2:
	default:
		return "", fmt.Errorf("invalid schema_version %q", selected)
	}
	if len(caps.SchemaVersions) == 0 {
		return selected, nil
	}
	for _, v := range caps.SchemaVersions {
		if v == selected {
			return selected, nil
		}
	}
	return "", fmt.Errorf("schema_version %q is not supported by backend %q", selected, backend)
}

func (s *Service) ListEvents(ctx context.Context, runID string, fromSeq int64) ([]events.Event, error) {
	return s.ledger.ListEvents(ctx, runID, fromSeq, 2000)
}

func (s *Service) Subscribe(runID string) (<-chan events.Event, func()) {
	return s.hub.Subscribe(runID, 128)
}

func (s *Service) ListBackends(ctx context.Context) ([]map[string]any, error) {
	drivers := s.registry.All()
	out := make([]map[string]any, 0, len(drivers))
	for _, d := range drivers {
		health, hErr := d.Health(ctx)
		caps, cErr := d.Capabilities(ctx)
		entry := map[string]any{
			"name":   d.Name(),
			"health": health,
		}
		if hErr != nil {
			entry["health"] = map[string]any{"ok": false, "message": hErr.Error()}
		}
		if cErr == nil {
			entry["capabilities"] = caps
		} else {
			entry["capabilities_error"] = cErr.Error()
		}
		out = append(out, entry)
	}
	return out, nil
}

func (s *Service) setStatus(ctx context.Context, runID, status, errText string) {
	_ = s.ledger.UpdateRunStatus(ctx, runID, status, errText)
	s.setActiveStatus(runID, status)
}

func (s *Service) setStatusIfNotTerminal(ctx context.Context, runID, status, errText string) (bool, error) {
	updated, err := s.ledger.UpdateRunStatusIfNotTerminal(ctx, runID, status, errText)
	if err != nil {
		return false, err
	}
	if updated {
		s.setActiveStatus(runID, status)
	}
	return updated, nil
}

func (s *Service) setActiveStatus(runID, status string) {
	s.mu.Lock()
	if ar := s.active[runID]; ar != nil {
		ar.status = status
	}
	s.mu.Unlock()
}

func (s *Service) cancelTerminalConflict(runID string) error {
	rec, err := s.ledger.GetRun(context.Background(), runID)
	if err != nil {
		return err
	}
	s.setActiveStatus(runID, rec.Status)
	if rec.Status == StatusCancelled {
		return nil
	}
	if isTerminalStatus(rec.Status) {
		return fmt.Errorf("run is already %s", rec.Status)
	}
	return fmt.Errorf("run status changed to %s while cancelling", rec.Status)
}

func (s *Service) currentStatus(runID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ar := s.active[runID]; ar != nil {
		return ar.status
	}
	return ""
}

func (s *Service) nextSeq(ctx context.Context, runID string) int64 {
	s.mu.Lock()
	ar := s.active[runID]
	if ar == nil {
		s.mu.Unlock()
		seq, err := s.ledger.NextSeq(ctx, runID)
		if err != nil {
			return 1
		}
		return seq
	}
	seq := ar.seq
	ar.seq++
	s.mu.Unlock()
	return seq
}

func (s *Service) emit(ctx context.Context, runID, backend, source, typ string, payload map[string]any) {
	channel := "system"
	format := "json"
	role := "system"
	if typ == events.TypeError {
		format = "plain"
	}
	ev := events.Event{
		RunID:         runID,
		Seq:           s.nextSeq(ctx, runID),
		TS:            time.Now().UTC(),
		SchemaVersion: s.runSchemaVersion(ctx, runID),
		Type:          typ,
		Channel:       channel,
		Format:        format,
		Role:          role,
		Payload:       payload,
		Backend:       backend,
		Source:        source,
	}
	events.NormalizeEvent(&ev)
	if err := events.ValidateEvent(ev); err != nil {
		ev.Type = events.TypeError
		ev.Channel = events.ChannelSystem
		ev.Format = events.FormatPlain
		ev.Role = events.RoleSystem
		ev.Payload = map[string]any{"message": "invalid event contract in bridge emit", "detail": err.Error()}
	}
	_ = s.ledger.AppendEvent(ctx, ev)
	s.hub.Publish(ev)
}

func (s *Service) runSchemaVersion(ctx context.Context, runID string) string {
	s.mu.Lock()
	if ar := s.active[runID]; ar != nil {
		v := ar.schemaVersion
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()

	rec, err := s.ledger.GetRun(ctx, runID)
	if err != nil {
		return ""
	}
	return rec.Options.SchemaVersion
}

func terminalStatusFromDone(payload map[string]any, sawError bool) (string, string) {
	raw := strings.ToLower(strings.TrimSpace(payloadString(payload, "status")))
	switch raw {
	case "":
		if sawError {
			return StatusFailed, "run contained error events"
		}
		return StatusCompleted, ""
	case "completed", "complete", "success", "succeeded", "ok":
		if sawError {
			return StatusFailed, "run contained error events"
		}
		return StatusCompleted, ""
	case "failed", "failure", "error":
		return StatusFailed, eventErrorMessage(payload)
	case "cancelled", "canceled":
		return StatusCancelled, ""
	default:
		if sawError {
			return StatusFailed, "run contained error events"
		}
		return StatusCompleted, ""
	}
}

func isTerminalStatus(status string) bool {
	switch status {
	case StatusCancelled, StatusCompleted, StatusFailed:
		return true
	default:
		return false
	}
}

func eventErrorMessage(payload map[string]any) string {
	msg := strings.TrimSpace(payloadString(payload, "message"))
	if msg == "" {
		msg = strings.TrimSpace(payloadString(payload, "result"))
	}
	if msg == "" {
		msg = strings.TrimSpace(payloadString(payload, "error"))
	}
	if msg == "" {
		msg = "backend reported error event"
	}
	return msg
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
