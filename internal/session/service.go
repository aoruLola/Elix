package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"echohelix/internal/policy"

	"github.com/google/uuid"
)

type Config struct {
	CodexBin             string
	CodexArgs            []string
	GeminiBin            string
	GeminiArgs           []string
	ClaudeBin            string
	ClaudeArgs           []string
	StartTimeout         time.Duration
	RequestTimeout       time.Duration
	SessionRetention     time.Duration
	SessionCleanupPeriod time.Duration
	BlockedMethods       []string
}

type backendLaunch struct {
	bin  string
	args []string
}

type Service struct {
	cfg            Config
	policy         *policy.Policy
	hub            *Hub
	blockedMethods map[string]struct{}
	launchers      map[string]backendLaunch
	lastCleanup    time.Time

	mu       sync.Mutex
	sessions map[string]*sessionState
}

type sessionState struct {
	session Session
	client  *appServerClient

	mu            sync.Mutex
	seq           int64
	history       []Event
	pending       map[string]*pendingRequestState
	activeTurnID  string
	closedLocally bool
}

type pendingRequestState struct {
	obj    PendingRequest
	wireID any
}

func NewService(cfg Config, p *policy.Policy) *Service {
	cfg.CodexBin = strings.TrimSpace(cfg.CodexBin)
	if cfg.CodexBin == "" {
		cfg.CodexBin = "codex"
	}
	cfg.GeminiBin = strings.TrimSpace(cfg.GeminiBin)
	if cfg.GeminiBin == "" {
		cfg.GeminiBin = "gemini"
	}
	cfg.ClaudeBin = strings.TrimSpace(cfg.ClaudeBin)
	if cfg.ClaudeBin == "" {
		cfg.ClaudeBin = "claude"
	}
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = 20 * time.Second
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.SessionRetention <= 0 {
		cfg.SessionRetention = 6 * time.Hour
	}
	if cfg.SessionCleanupPeriod <= 0 {
		cfg.SessionCleanupPeriod = 5 * time.Minute
	}
	blocked := make(map[string]struct{}, len(cfg.BlockedMethods))
	for _, m := range cfg.BlockedMethods {
		if key := normalizeMethod(m); key != "" {
			blocked[key] = struct{}{}
		}
	}
	if len(blocked) == 0 {
		blocked[normalizeMethod("initialize")] = struct{}{}
		blocked[normalizeMethod("initialized")] = struct{}{}
	}
	launchers := map[string]backendLaunch{
		BackendCodex: {
			bin:  cfg.CodexBin,
			args: buildCodexArgs(cfg.CodexArgs),
		},
		BackendGemini: {
			bin:  cfg.GeminiBin,
			args: append([]string(nil), cfg.GeminiArgs...),
		},
		BackendClaude: {
			bin:  cfg.ClaudeBin,
			args: append([]string(nil), cfg.ClaudeArgs...),
		},
	}
	return &Service{
		cfg:            cfg,
		policy:         p,
		hub:            NewHub(),
		blockedMethods: blocked,
		launchers:      launchers,
		sessions:       map[string]*sessionState{},
		lastCleanup:    time.Now().UTC(),
	}
}

func (s *Service) Create(ctx context.Context, req CreateRequest) (Session, error) {
	s.maybeCleanup(time.Now().UTC())
	backend := normalizeBackend(req.Backend)
	if backend == "" {
		backend = BackendCodex
	}
	launcher, ok := s.launchers[backend]
	if !ok {
		return Session{}, fmt.Errorf("unsupported backend %q", req.Backend)
	}
	if err := s.policy.ValidateWorkspace(req.WorkspacePath); err != nil {
		return Session{}, err
	}
	if err := s.policy.ValidateRunOptions(policy.RunOptions{Model: req.Model, Sandbox: req.Sandbox}); err != nil {
		return Session{}, err
	}

	sessionID := uuid.NewString()
	now := time.Now().UTC()
	state := &sessionState{
		session: Session{
			ID:            sessionID,
			Backend:       backend,
			WorkspaceID:   req.WorkspaceID,
			WorkspacePath: req.WorkspacePath,
			Status:        StatusStarting,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		history: make([]Event, 0, 256),
		pending: map[string]*pendingRequestState{},
	}

	s.mu.Lock()
	s.sessions[sessionID] = state
	s.mu.Unlock()

	client, err := newAppServerClient(launcher.bin, launcher.args, req.WorkspacePath)
	if err != nil {
		s.deleteSession(sessionID)
		return Session{}, err
	}
	state.client = client

	client.onNotification = func(method string, params map[string]any) {
		s.handleNotification(state, method, params)
	}
	client.onRequest = func(reqIDKey string, wireID any, method string, params map[string]any) {
		s.handleServerRequest(state, reqIDKey, wireID, method, params)
	}
	client.onStderr = func(line string) {
		s.publish(state, "stderr", "stderr", map[string]any{"line": line})
	}
	client.onClose = func(exitErr error) {
		s.handleClientClosed(state, exitErr)
	}

	startCtx, cancel := requestTimeout(ctx, s.cfg.StartTimeout)
	defer cancel()
	if _, err := client.Call(startCtx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "echohelix_bridge",
			"title":   "EchoHelix Bridge",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}); err != nil {
		_ = client.Close()
		s.deleteSession(sessionID)
		return Session{}, err
	}
	if err := client.Notify("initialized", nil); err != nil {
		_ = client.Close()
		s.deleteSession(sessionID)
		return Session{}, err
	}

	threadMethod := "thread/start"
	threadParams := map[string]any{"cwd": req.WorkspacePath}
	if req.Model != "" {
		threadParams["model"] = req.Model
	}
	if req.Approval != "" {
		threadParams["approvalPolicy"] = req.Approval
	}
	if req.Sandbox != "" {
		threadParams["sandbox"] = toCodexSandbox(req.Sandbox)
	}
	if len(req.Config) > 0 {
		threadParams["config"] = req.Config
	}
	if strings.TrimSpace(req.ThreadID) != "" {
		threadMethod = "thread/resume"
		threadParams = map[string]any{"threadId": strings.TrimSpace(req.ThreadID)}
	}

	result, err := client.Call(startCtx, threadMethod, threadParams)
	if err != nil {
		_ = client.Close()
		s.deleteSession(sessionID)
		return Session{}, err
	}
	threadID := decodeResultField(result, "thread", "id")
	if threadID == "" {
		_ = client.Close()
		s.deleteSession(sessionID)
		return Session{}, fmt.Errorf("%s app-server %s returned empty thread id", backend, threadMethod)
	}

	state.mu.Lock()
	state.session.ThreadID = threadID
	state.session.Status = StatusReady
	state.session.UpdatedAt = time.Now().UTC()
	out := state.session
	state.mu.Unlock()

	s.publish(state, "status", "session/ready", map[string]any{"thread_id": threadID})
	return out, nil
}

func (s *Service) List() []Session {
	s.maybeCleanup(time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, 0, len(s.sessions))
	for _, st := range s.sessions {
		st.mu.Lock()
		out = append(out, st.session)
		st.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (s *Service) Get(sessionID string) (Session, error) {
	st, err := s.state(sessionID)
	if err != nil {
		return Session{}, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.session, nil
}

func (s *Service) Close(sessionID string) error {
	st, err := s.state(sessionID)
	if err != nil {
		return err
	}
	st.mu.Lock()
	st.closedLocally = true
	st.session.Status = StatusClosed
	st.session.UpdatedAt = time.Now().UTC()
	st.mu.Unlock()

	if st.client != nil {
		_ = st.client.Close()
	}
	s.publish(st, "status", "session/closed", nil)
	return nil
}

func (s *Service) Shutdown(ctx context.Context) error {
	_ = ctx
	s.mu.Lock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		_ = s.Close(id)
	}
	return nil
}

func (s *Service) StartTurn(ctx context.Context, sessionID string, req StartTurnRequest) (StartTurnResult, error) {
	st, err := s.state(sessionID)
	if err != nil {
		return StartTurnResult{}, err
	}
	if req.Prompt == "" && len(req.Input) == 0 {
		return StartTurnResult{}, fmt.Errorf("prompt or input is required")
	}
	if err := s.policy.ValidateRunOptions(policy.RunOptions{Model: req.Model, Sandbox: req.Sandbox}); err != nil {
		return StartTurnResult{}, err
	}

	st.mu.Lock()
	if st.session.Status == StatusClosed {
		st.mu.Unlock()
		return StartTurnResult{}, fmt.Errorf("session is closed")
	}
	threadID := st.session.ThreadID
	activeTurnID := st.activeTurnID
	st.mu.Unlock()

	method := "turn/start"
	if req.Steer {
		method = "turn/steer"
	}
	params := map[string]any{"threadId": threadID}
	if req.Steer {
		expectedTurnID := strings.TrimSpace(req.ExpectedTurnID)
		if expectedTurnID == "" {
			expectedTurnID = activeTurnID
		}
		if expectedTurnID == "" {
			return StartTurnResult{}, fmt.Errorf("no active turn for steer")
		}
		params["expectedTurnId"] = expectedTurnID
	}
	input := req.Input
	if len(input) == 0 {
		input = []map[string]any{{"type": "text", "text": req.Prompt}}
	}
	params["input"] = input
	if req.Model != "" {
		params["model"] = req.Model
	}
	if req.Approval != "" {
		params["approvalPolicy"] = req.Approval
	}
	if req.Sandbox != "" {
		params["sandboxPolicy"] = map[string]any{"type": toCodexSandbox(req.Sandbox)}
	}
	if len(req.OutputSchema) > 0 {
		params["outputSchema"] = req.OutputSchema
	}

	callCtx, cancel := requestTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()
	resultRaw, err := st.client.Call(callCtx, method, params)
	if err != nil {
		return StartTurnResult{}, err
	}

	threadResultID := decodeResultField(resultRaw, "turn", "threadId")
	if threadResultID == "" {
		threadResultID = threadID
	}
	turnID := decodeResultField(resultRaw, "turn", "id")
	status := decodeResultField(resultRaw, "turn", "status")

	st.mu.Lock()
	if turnID != "" {
		st.activeTurnID = turnID
	}
	st.session.UpdatedAt = time.Now().UTC()
	st.mu.Unlock()

	return StartTurnResult{
		SessionID: sessionID,
		ThreadID:  threadResultID,
		TurnID:    turnID,
		Status:    status,
	}, nil
}

func (s *Service) InterruptTurn(ctx context.Context, sessionID string, turnID string) error {
	st, err := s.state(sessionID)
	if err != nil {
		return err
	}
	st.mu.Lock()
	threadID := st.session.ThreadID
	if strings.TrimSpace(turnID) == "" {
		turnID = st.activeTurnID
	}
	st.mu.Unlock()
	if turnID == "" {
		return fmt.Errorf("turn_id is required")
	}
	callCtx, cancel := requestTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()
	_, err = st.client.Call(callCtx, "turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	})
	return err
}

func (s *Service) BackendStatus(ctx context.Context, sessionID string) (BackendStatus, error) {
	out, err := s.BackendCall(ctx, sessionID, BackendCallRequest{Method: "status"})
	if err != nil {
		return BackendStatus{}, err
	}
	return BackendStatus{
		SessionID: out.SessionID,
		Backend:   out.Backend,
		ThreadID:  out.ThreadID,
		Result:    out.Result,
		FetchedAt: out.CalledAt,
	}, nil
}

func (s *Service) BackendCall(ctx context.Context, sessionID string, in BackendCallRequest) (BackendCallResult, error) {
	method := strings.TrimSpace(in.Method)
	methodKey := normalizeMethod(method)
	if methodKey == "" {
		return BackendCallResult{}, fmt.Errorf("method is required")
	}
	if _, blocked := s.blockedMethods[methodKey]; blocked {
		return BackendCallResult{}, fmt.Errorf("method %q is managed by bridge", method)
	}
	st, err := s.state(sessionID)
	if err != nil {
		return BackendCallResult{}, err
	}

	st.mu.Lock()
	if st.session.Status == StatusClosed {
		st.mu.Unlock()
		return BackendCallResult{}, fmt.Errorf("session is closed")
	}
	backend := st.session.Backend
	threadID := st.session.ThreadID
	st.mu.Unlock()

	timeout := s.cfg.RequestTimeout
	if in.TimeoutMS > 0 {
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
		if timeout > 10*time.Minute {
			return BackendCallResult{}, fmt.Errorf("timeout_ms exceeds maximum 600000")
		}
	}
	callCtx, cancel := requestTimeout(ctx, timeout)
	defer cancel()
	raw, err := st.client.Call(callCtx, method, in.Params)
	if err != nil {
		return BackendCallResult{}, err
	}

	result := any(map[string]any{})
	if len(raw) > 0 {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err == nil {
			result = decoded
		} else {
			result = map[string]any{"raw": string(raw)}
		}
	}

	return BackendCallResult{
		SessionID: sessionID,
		Backend:   backend,
		ThreadID:  threadID,
		Method:    methodKey,
		Result:    result,
		CalledAt:  time.Now().UTC(),
	}, nil
}

func (s *Service) ListEvents(sessionID string, fromSeq int64) ([]Event, error) {
	st, err := s.state(sessionID)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if fromSeq <= 0 {
		out := make([]Event, len(st.history))
		copy(out, st.history)
		return out, nil
	}
	out := make([]Event, 0, len(st.history))
	for _, ev := range st.history {
		if ev.Seq >= fromSeq {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (s *Service) Subscribe(sessionID string) (<-chan Event, func(), error) {
	if _, err := s.state(sessionID); err != nil {
		return nil, nil, err
	}
	ch, unsub := s.hub.Subscribe(sessionID, 256)
	return ch, unsub, nil
}

func (s *Service) ListPendingRequests(sessionID string) ([]PendingRequest, error) {
	st, err := s.state(sessionID)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]PendingRequest, 0, len(st.pending))
	for _, item := range st.pending {
		if !item.obj.Resolved {
			out = append(out, item.obj)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *Service) ResolvePendingRequest(ctx context.Context, sessionID, requestID string, in ResolveRequestInput) error {
	st, err := s.state(sessionID)
	if err != nil {
		return err
	}
	st.mu.Lock()
	pending, ok := st.pending[requestID]
	if !ok || pending.obj.Resolved {
		st.mu.Unlock()
		return fmt.Errorf("pending request not found")
	}
	pending.obj.Resolved = true
	pending.obj.ResolvedAt = time.Now().UTC()
	st.mu.Unlock()

	if in.Error != nil {
		data := map[string]any(nil)
		if in.Error.Data != nil {
			data = in.Error.Data
		}
		if err := st.client.ReplyError(pending.wireID, in.Error.Code, in.Error.Message, data); err != nil {
			return err
		}
		s.publish(st, "request_resolved", pending.obj.Method, map[string]any{"request_id": requestID, "error": in.Error})
		return nil
	}
	result := map[string]any{}
	if in.Result != nil {
		result = in.Result
	}
	if err := st.client.ReplyResult(pending.wireID, result); err != nil {
		return err
	}
	s.publish(st, "request_resolved", pending.obj.Method, map[string]any{"request_id": requestID, "result": result})
	return nil
}

func (s *Service) ListApprovals(sessionID string) ([]Approval, error) {
	pending, err := s.ListPendingRequests(sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]Approval, 0, len(pending))
	for _, item := range pending {
		if item.Kind != "approval" {
			continue
		}
		ap := Approval{
			RequestID: item.RequestID,
			Method:    item.Method,
			Payload:   item.Params,
			CreatedAt: item.CreatedAt,
			Resolved:  item.Resolved,
		}
		if v, ok := item.Params["threadId"].(string); ok {
			ap.ThreadID = v
		}
		if v, ok := item.Params["turnId"].(string); ok {
			ap.TurnID = v
		}
		if v, ok := item.Params["itemId"].(string); ok {
			ap.ItemID = v
		}
		if v, ok := item.Params["reason"].(string); ok {
			ap.Reason = v
		}
		if v, ok := item.Params["command"].(string); ok {
			ap.Command = v
		}
		if v, ok := item.Params["cwd"].(string); ok {
			ap.Cwd = v
		}
		out = append(out, ap)
	}
	return out, nil
}

func (s *Service) ResolveApproval(ctx context.Context, sessionID, requestID string, decision ApprovalDecision) error {
	d := strings.ToLower(strings.TrimSpace(decision.Decision))
	if d == "" {
		d = "decline"
	}
	if d != "accept" && d != "decline" {
		return fmt.Errorf("decision must be accept or decline")
	}
	result := map[string]any{"decision": d}
	if d == "accept" {
		result["acceptSettings"] = map[string]any{"forSession": decision.ForSession}
	}
	return s.ResolvePendingRequest(ctx, sessionID, requestID, ResolveRequestInput{Result: result})
}

func (s *Service) handleNotification(st *sessionState, method string, params map[string]any) {
	if method == "turn/started" {
		if turn, ok := params["turn"].(map[string]any); ok {
			if turnID, ok := turn["id"].(string); ok {
				st.mu.Lock()
				st.activeTurnID = turnID
				st.mu.Unlock()
			}
		}
	}
	if method == "turn/completed" {
		st.mu.Lock()
		st.activeTurnID = ""
		st.mu.Unlock()
	}
	s.publish(st, "notification", method, params)
}

func (s *Service) handleServerRequest(st *sessionState, reqIDKey string, wireID any, method string, params map[string]any) {
	kind := requestKind(method)
	created := time.Now().UTC()
	obj := PendingRequest{
		RequestID: reqIDKey,
		Method:    method,
		Kind:      kind,
		Params:    params,
		CreatedAt: created,
	}

	st.mu.Lock()
	st.pending[reqIDKey] = &pendingRequestState{obj: obj, wireID: wireID}
	st.mu.Unlock()
	s.publish(st, "request", method, map[string]any{
		"request_id": reqIDKey,
		"method":     method,
		"kind":       kind,
		"params":     params,
	})

	if kind == "unsupported" {
		_ = st.client.ReplyError(wireID, -32601, "unsupported server request method", nil)
		st.mu.Lock()
		if item, ok := st.pending[reqIDKey]; ok {
			item.obj.Resolved = true
			item.obj.ResolvedAt = time.Now().UTC()
		}
		st.mu.Unlock()
	}
}

func (s *Service) handleClientClosed(st *sessionState, exitErr error) {
	st.mu.Lock()
	if st.closedLocally {
		st.session.Status = StatusClosed
	} else if exitErr != nil {
		st.session.Status = StatusFailed
		st.session.Error = exitErr.Error()
	} else {
		st.session.Status = StatusClosed
	}
	st.session.UpdatedAt = time.Now().UTC()
	st.mu.Unlock()
	payload := map[string]any{}
	if exitErr != nil {
		payload["error"] = exitErr.Error()
	}
	s.publish(st, "status", "session/exited", payload)
}

func (s *Service) publish(st *sessionState, typ, method string, payload map[string]any) {
	st.mu.Lock()
	st.seq++
	ev := Event{
		SessionID: st.session.ID,
		Seq:       st.seq,
		TS:        time.Now().UTC(),
		Type:      typ,
		Method:    method,
		Payload:   payload,
	}
	st.history = append(st.history, ev)
	if len(st.history) > 4000 {
		st.history = st.history[len(st.history)-4000:]
	}
	st.session.UpdatedAt = ev.TS
	st.mu.Unlock()
	s.hub.Publish(ev)
}

func (s *Service) state(sessionID string) (*sessionState, error) {
	s.maybeCleanup(time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found")
	}
	return st, nil
}

func (s *Service) deleteSession(sessionID string) {
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()
}

func (s *Service) maybeCleanup(now time.Time) {
	if s.cfg.SessionRetention <= 0 {
		return
	}

	s.mu.Lock()
	if !s.lastCleanup.IsZero() && now.Sub(s.lastCleanup) < s.cfg.SessionCleanupPeriod {
		s.mu.Unlock()
		return
	}
	s.lastCleanup = now
	cutoff := now.Add(-s.cfg.SessionRetention)
	for id, st := range s.sessions {
		st.mu.Lock()
		status := st.session.Status
		updatedAt := st.session.UpdatedAt
		st.mu.Unlock()
		if !isTerminalSessionStatus(status) {
			continue
		}
		if updatedAt.After(cutoff) {
			continue
		}
		delete(s.sessions, id)
	}
	s.mu.Unlock()
}

func isTerminalSessionStatus(status string) bool {
	switch status {
	case StatusClosed, StatusFailed:
		return true
	default:
		return false
	}
}

func buildCodexArgs(extra []string) []string {
	args := []string{"app-server", "--listen", "stdio://"}
	if len(extra) > 0 {
		args = append(args, extra...)
	}
	return args
}

func requestKind(method string) string {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "applyPatchApproval", "execCommandApproval":
		return "approval"
	case "item/tool/requestUserInput":
		return "request_user_input"
	case "item/tool/call":
		return "dynamic_tool"
	default:
		return "unsupported"
	}
}

func toCodexSandbox(v string) string {
	switch strings.TrimSpace(v) {
	case "read-only", "readOnly":
		return "read-only"
	case "workspace-write", "workspaceWrite":
		return "workspace-write"
	case "danger-full-access", "dangerFullAccess":
		return "danger-full-access"
	default:
		return v
	}
}

func normalizeMethod(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func normalizeBackend(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}
