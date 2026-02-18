package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"echohelix/internal/auth"
	"echohelix/internal/run"
	"echohelix/internal/session"

	"github.com/gorilla/websocket"
)

type Server struct {
	httpServer *http.Server
	runSvc     *run.Service
	sessionSvc *session.Service
	authToken  string
	authSvc    *auth.Service
	security   SecurityConfig

	pairStartLimiter         *windowLimiter
	refreshFailureCounter    *windowCounter
	authFailureCounter       *windowCounter
	pairCompleteFailureCount *windowCounter
	backendCallReadSet       map[string]struct{}
	backendCallCancelSet     map[string]struct{}
}

type principalContextKey struct{}

func New(addr string, authToken string, runSvc *run.Service, sessionSvc *session.Service, authSvc *auth.Service, securityCfg ...SecurityConfig) *Server {
	cfg := defaultSecurityConfig()
	if len(securityCfg) > 0 {
		cfg = normalizeSecurityConfig(securityCfg[0])
	}
	s := &Server{
		runSvc:                   runSvc,
		sessionSvc:               sessionSvc,
		authToken:                authToken,
		authSvc:                  authSvc,
		security:                 cfg,
		pairStartLimiter:         newWindowLimiter(cfg.PairStartRateLimit, cfg.PairStartRateWindow),
		refreshFailureCounter:    newWindowCounter(cfg.RefreshFailureAlertWindow),
		authFailureCounter:       newWindowCounter(cfg.AuthFailureAlertWindow),
		pairCompleteFailureCount: newWindowCounter(cfg.PairCompleteFailureAlertWindow),
		backendCallReadSet:       makeMethodSet(cfg.BackendCallReadMethods),
		backendCallCancelSet:     makeMethodSet(cfg.BackendCallCancelMethods),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/api/v3/pair/complete", s.handlePairComplete)
	mux.HandleFunc("/api/v3/session/refresh", s.handleSessionRefresh)
	mux.HandleFunc("/api/v3/pair/start", s.withAuth(s.handlePairStart))
	mux.HandleFunc("/api/v3/devices", s.withAuth(s.handleDevices))
	mux.HandleFunc("/api/v3/devices/", s.withAuth(s.handleDeviceByAddress))
	mux.HandleFunc("/api/v3/backends", s.withAuth(s.handleBackends))
	mux.HandleFunc("/api/v3/usage/tokens", s.withAuth(s.handleUsageTokens))
	mux.HandleFunc("/api/v3/usage/quota", s.withAuth(s.handleUsageQuota))
	mux.HandleFunc("/api/v3/emergency/stop", s.withAuth(s.handleEmergencyStop))
	mux.HandleFunc("/api/v3/emergency/resume", s.withAuth(s.handleEmergencyResume))
	mux.HandleFunc("/api/v3/emergency/status", s.withAuth(s.handleEmergencyStatus))
	mux.HandleFunc("/api/v3/files", s.withAuth(s.handleFiles))
	mux.HandleFunc("/api/v3/files/", s.withAuth(s.handleFileByID))
	mux.HandleFunc("/api/v3/sessions", s.withAuth(s.handleSessions))
	mux.HandleFunc("/api/v3/sessions/", s.withAuth(s.handleSessionByID))
	mux.HandleFunc("/api/v3/runs", s.withAuth(s.handleRuns))
	mux.HandleFunc("/api/v3/runs/", s.withAuth(s.handleRunByID))
	if h, err := uiHandler(); err == nil {
		mux.Handle("/ui/", http.StripPrefix("/ui/", h))
		mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ui/", http.StatusTemporaryRedirect)
		})
	}

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

func (s *Server) Start() error {
	log.Printf("bridge listening on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := s.authenticate(r)
		if err != nil {
			s.auditf(r, "auth_failed", "invalid bearer token")
			s.maybeAlertAuthFailure(r)
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]any{
					"code":    "unauthorized",
					"message": err.Error(),
				},
			})
			return
		}
		s.authFailureCounter.Reset(s.clientIP(r))
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) authenticate(r *http.Request) (auth.Principal, error) {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	token := ""
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return auth.Principal{}, fmt.Errorf("missing or invalid bearer token")
		}
		token = strings.TrimSpace(parts[1])
	}

	if s.authToken == "" && s.authSvc == nil {
		return auth.AdminPrincipal(), nil
	}
	if token == "" {
		return auth.Principal{}, fmt.Errorf("missing or invalid bearer token")
	}
	if s.authToken != "" && token == s.authToken {
		return auth.StaticBootstrapPrincipal(), nil
	}
	if s.authSvc == nil {
		return auth.Principal{}, fmt.Errorf("missing or invalid bearer token")
	}
	principal, err := s.authSvc.AuthenticateToken(r.Context(), token)
	if err != nil {
		return auth.Principal{}, fmt.Errorf("missing or invalid bearer token")
	}
	return principal, nil
}

func (s *Server) principalFromContext(ctx context.Context) (auth.Principal, bool) {
	v := ctx.Value(principalContextKey{})
	if v == nil {
		return auth.Principal{}, false
	}
	p, ok := v.(auth.Principal)
	return p, ok
}

func (s *Server) requireScope(w http.ResponseWriter, r *http.Request, scope string) (auth.Principal, bool) {
	principal, ok := s.principalFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return auth.Principal{}, false
	}
	if principal.Admin || principal.HasScope(scope) {
		return principal, true
	}
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error": map[string]any{
			"code":    "forbidden",
			"message": "missing scope: " + scope,
		},
	})
	return auth.Principal{}, false
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if _, ok := s.requireScope(w, r, auth.ScopeRunsSubmit); !ok {
		return
	}

	var req run.SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	obj, err := s.runSvc.Submit(r.Context(), req)
	if err != nil {
		if errors.Is(err, run.ErrEmergencyStopActive) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": map[string]any{
					"code":    "emergency_stop_active",
					"message": err.Error(),
				},
			})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"run_id":     obj.ID,
		"status":     obj.Status,
		"stream_url": "/api/v3/runs/" + obj.ID + "/events",
		"created_at": obj.CreatedAt,
	})
}

func (s *Server) handleRunByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v3/runs/")
	path = strings.Trim(path, "/")
	if path == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "run id missing"})
		return
	}
	parts := strings.Split(path, "/")
	runID := parts[0]

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if _, ok := s.requireScope(w, r, auth.ScopeRunsRead); !ok {
			return
		}
		obj, err := s.runSvc.GetRun(r.Context(), runID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, obj)
		return
	}

	action := parts[1]
	switch action {
	case "cancel":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if _, ok := s.requireScope(w, r, auth.ScopeRunsCancel); !ok {
			return
		}
		if err := s.runSvc.Cancel(r.Context(), runID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"run_id": runID,
			"status": run.StatusCancelled,
		})
	case "events":
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if _, ok := s.requireScope(w, r, auth.ScopeRunsRead); !ok {
			return
		}
		s.handleRunEvents(w, r, runID)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown action"})
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request, runID string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	fromSeq := int64(0)
	if v := r.URL.Query().Get("from_seq"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			fromSeq = n
		}
	}

	history, err := s.runSvc.ListEvents(r.Context(), runID, fromSeq)
	if err == nil {
		for _, ev := range history {
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		}
	}

	sub, unsub := s.runSvc.Subscribe(runID)
	defer unsub()

	for ev := range sub {
		if err := conn.WriteJSON(ev); err != nil {
			return
		}
	}
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if s.sessionSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return
	}
	switch r.Method {
	case http.MethodPost:
		if _, ok := s.requireScope(w, r, auth.ScopeRunsSubmit); !ok {
			return
		}
		var req session.CreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		obj, err := s.sessionSvc.Create(r.Context(), req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, obj)
	case http.MethodGet:
		if _, ok := s.requireScope(w, r, auth.ScopeRunsRead); !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": s.sessionSvc.List()})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	if s.sessionSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v3/sessions/")
	path = strings.Trim(path, "/")
	if path == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "session id missing"})
		return
	}
	parts := strings.Split(path, "/")
	sessionID := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			if _, ok := s.requireScope(w, r, auth.ScopeRunsRead); !ok {
				return
			}
			obj, err := s.sessionSvc.Get(sessionID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, obj)
		case http.MethodDelete:
			if _, ok := s.requireScope(w, r, auth.ScopeRunsCancel); !ok {
				return
			}
			if err := s.sessionSvc.Close(sessionID); err != nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"session_id": sessionID, "closed": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
		return
	}

	action := parts[1]
	switch action {
	case "turns":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if _, ok := s.requireScope(w, r, auth.ScopeRunsSubmit); !ok {
			return
		}
		var req session.StartTurnRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		obj, err := s.sessionSvc.StartTurn(r.Context(), sessionID, req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, obj)
	case "interrupt":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if _, ok := s.requireScope(w, r, auth.ScopeRunsCancel); !ok {
			return
		}
		var req struct {
			TurnID string `json:"turn_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := s.sessionSvc.InterruptTurn(r.Context(), sessionID, req.TurnID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"session_id": sessionID, "interrupted": true})
	case "backend":
		if len(parts) != 3 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown action"})
			return
		}
		switch parts[2] {
		case "status":
			if r.Method != http.MethodGet {
				writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			if _, ok := s.requireScope(w, r, auth.ScopeRunsRead); !ok {
				return
			}
			obj, err := s.sessionSvc.BackendStatus(r.Context(), sessionID)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, obj)
		case "call":
			if r.Method != http.MethodPost {
				writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			var req session.BackendCallRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			if _, ok := s.requireScope(w, r, s.backendCallScope(req.Method)); !ok {
				return
			}
			obj, err := s.sessionSvc.BackendCall(r.Context(), sessionID, req)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, obj)
		default:
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown action"})
		}
	case "events":
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if _, ok := s.requireScope(w, r, auth.ScopeRunsRead); !ok {
			return
		}
		s.handleSessionEvents(w, r, sessionID)
	case "requests":
		if len(parts) == 2 {
			if r.Method != http.MethodGet {
				writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			if _, ok := s.requireScope(w, r, auth.ScopeRunsRead); !ok {
				return
			}
			items, err := s.sessionSvc.ListPendingRequests(sessionID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
			return
		}
		if len(parts) != 3 || r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if _, ok := s.requireScope(w, r, auth.ScopeRunsCancel); !ok {
			return
		}
		var in session.ResolveRequestInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := s.sessionSvc.ResolvePendingRequest(r.Context(), sessionID, parts[2], in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"session_id": sessionID, "request_id": parts[2], "resolved": true})
	case "approvals":
		if len(parts) == 2 {
			if r.Method != http.MethodGet {
				writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			if _, ok := s.requireScope(w, r, auth.ScopeRunsRead); !ok {
				return
			}
			items, err := s.sessionSvc.ListApprovals(sessionID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
			return
		}
		if len(parts) != 3 || r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if _, ok := s.requireScope(w, r, auth.ScopeRunsCancel); !ok {
			return
		}
		var in session.ApprovalDecision
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := s.sessionSvc.ResolveApproval(r.Context(), sessionID, parts[2], in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"session_id": sessionID, "request_id": parts[2], "resolved": true})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown action"})
	}
}

func (s *Server) handleSessionEvents(w http.ResponseWriter, r *http.Request, sessionID string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	fromSeq := int64(0)
	if v := r.URL.Query().Get("from_seq"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			fromSeq = n
		}
	}
	history, err := s.sessionSvc.ListEvents(sessionID, fromSeq)
	if err == nil {
		for _, ev := range history {
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		}
	}
	sub, unsub, err := s.sessionSvc.Subscribe(sessionID)
	if err != nil {
		return
	}
	defer unsub()
	for ev := range sub {
		if err := conn.WriteJSON(ev); err != nil {
			return
		}
	}
}

func (s *Server) backendCallScope(method string) string {
	key := normalizeMethod(method)
	if _, ok := s.backendCallReadSet[key]; ok {
		return auth.ScopeRunsRead
	}
	if _, ok := s.backendCallCancelSet[key]; ok {
		return auth.ScopeRunsCancel
	}
	return auth.ScopeRunsSubmit
}

func makeMethodSet(methods []string) map[string]struct{} {
	out := make(map[string]struct{}, len(methods))
	for _, m := range methods {
		if key := normalizeMethod(m); key != "" {
			out[key] = struct{}{}
		}
	}
	return out
}

func normalizeMethod(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func (s *Server) handleBackends(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if _, ok := s.requireScope(w, r, auth.ScopeBackendsRead); !ok {
		return
	}
	backends, err := s.runSvc.ListBackends(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backends": backends})
}

func (s *Server) handleUsageTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if _, ok := s.requireScope(w, r, auth.ScopeBackendsRead); !ok {
		return
	}

	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now
	if v := strings.TrimSpace(r.URL.Query().Get("window")); v != "" {
		dur, err := time.ParseDuration(v)
		if err != nil || dur <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid window duration"})
			return
		}
		from = now.Add(-dur)
	}
	if v := strings.TrimSpace(r.URL.Query().Get("from")); v != "" {
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid from (expect RFC3339)"})
			return
		}
		from = ts.UTC()
	}
	if v := strings.TrimSpace(r.URL.Query().Get("to")); v != "" {
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid to (expect RFC3339)"})
			return
		}
		to = ts.UTC()
	}
	if !from.Before(to) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "from must be before to"})
		return
	}

	backend := strings.TrimSpace(r.URL.Query().Get("backend"))
	summary, err := s.runSvc.TokenUsage(r.Context(), from, to, backend)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleUsageQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if _, ok := s.requireScope(w, r, auth.ScopeBackendsRead); !ok {
		return
	}
	backend := strings.TrimSpace(r.URL.Query().Get("backend"))
	items, err := s.runSvc.TokenQuota(r.Context(), time.Now().UTC(), backend)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) requireBootstrapOperator(w http.ResponseWriter, r *http.Request) bool {
	principal, ok := s.requireScope(w, r, auth.ScopePairStart)
	if !ok {
		return false
	}
	if principal.Admin || principal.AuthType == "static" {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error": map[string]any{
			"code":    "forbidden",
			"message": "requires bootstrap static token",
		},
	})
	return false
}

func (s *Server) handleEmergencyStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !s.requireBootstrapOperator(w, r) {
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	state, cancelled := s.runSvc.EmergencyStop(r.Context(), req.Reason)
	writeJSON(w, http.StatusOK, map[string]any{
		"active":         state.Active,
		"reason":         state.Reason,
		"activated_at":   state.Activated,
		"cancelled_runs": cancelled,
	})
}

func (s *Server) handleEmergencyResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !s.requireBootstrapOperator(w, r) {
		return
	}
	state := s.runSvc.EmergencyResume()
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleEmergencyStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !s.requireBootstrapOperator(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.runSvc.EmergencyStatus())
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleFileUpload(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (s *Server) handleFileByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if _, ok := s.requireScope(w, r, auth.ScopeRunsRead); !ok {
		return
	}
	fileID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v3/files/"), "/")
	if fileID == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "file id missing"})
		return
	}
	obj, err := s.runSvc.GetUploadedFile(r.Context(), fileID)
	if err != nil {
		if errors.Is(err, run.ErrFileNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "file not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, obj)
}

func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireScope(w, r, auth.ScopeRunsSubmit)
	if !ok {
		return
	}

	limit := s.runSvc.MaxUploadBytes()
	r.Body = http.MaxBytesReader(w, r.Body, limit+1024)
	if err := r.ParseMultipartForm(limit); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid multipart form or file too large"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "multipart field 'file' is required"})
		return
	}
	defer file.Close()

	createdBy := principal.Address
	if createdBy == "" {
		createdBy = "admin"
	}
	contentType := strings.TrimSpace(header.Header.Get("Content-Type"))
	if contentType == "" {
		buf := make([]byte, 512)
		n, _ := io.ReadFull(file, buf)
		contentType = http.DetectContentType(buf[:n])
		if seeker, ok := file.(io.Seeker); ok {
			_, _ = seeker.Seek(0, io.SeekStart)
		}
	}
	obj, err := s.runSvc.UploadFile(r.Context(), run.UploadFileRequest{
		Reader:       file,
		OriginalName: header.Filename,
		MIMEType:     contentType,
		CreatedBy:    createdBy,
	})
	if err != nil {
		if errors.Is(err, run.ErrFileTooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, obj)
}

func (s *Server) handlePairStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	principal, ok := s.requireScope(w, r, auth.ScopePairStart)
	if !ok {
		return
	}
	if !principal.Admin && principal.AuthType != "static" {
		s.auditf(r, "pair_start_denied", "requires bootstrap static token")
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": map[string]any{
				"code":    "forbidden",
				"message": "pair/start requires bootstrap static token",
			},
		})
		return
	}
	ok, attempts, retryAfter := s.pairStartLimiter.Allow(s.clientIP(r), time.Now().UTC())
	if !ok {
		retrySec := int(retryAfter.Seconds())
		if retrySec < 1 {
			retrySec = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retrySec))
		s.auditf(r, "pair_start_rate_limited", fmt.Sprintf("attempts=%d retry_after=%ds", attempts, retrySec))
		log.Printf("security_alert event=pair_start_burst ip=%s attempts=%d window_sec=%d", s.clientIP(r), attempts, int(s.security.PairStartRateWindow.Seconds()))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": map[string]any{
				"code":    "rate_limited",
				"message": "too many pair/start requests",
			},
		})
		return
	}
	if s.authSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "auth service unavailable"})
		return
	}

	var req struct {
		Permissions []string `json:"permissions"`
		TTLSeconds  int      `json:"ttl_seconds"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	ttl := time.Duration(req.TTLSeconds) * time.Second
	createdBy := principal.Address
	if createdBy == "" {
		createdBy = "admin"
	}
	resp, err := s.authSvc.StartPair(r.Context(), createdBy, req.Permissions, ttl)
	if err != nil {
		s.auditf(r, "pair_start_failed", err.Error())
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.auditf(r, "pair_start_ok", "pair code issued")
	writeJSON(w, http.StatusOK, map[string]any{
		"pair_code":    resp.PairCode,
		"challenge":    resp.Challenge,
		"permissions":  resp.Permissions,
		"expires_at":   resp.ExpiresAt,
		"elix_uri":     pairURI(r, resp.PairCode, resp.Challenge),
		"pair_version": "v1",
	})
}

func (s *Server) handlePairComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if s.authSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "auth service unavailable"})
		return
	}
	var req auth.CompletePairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	resp, err := s.authSvc.CompletePair(r.Context(), req)
	if err != nil {
		s.auditf(r, "pair_complete_failed", err.Error())
		s.maybeAlertPairCompleteFailure(r)
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.pairCompleteFailureCount.Reset(s.clientIP(r))
	s.auditf(r, "pair_complete_ok", "device paired")
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSessionRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if s.authSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "auth service unavailable"})
		return
	}
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	resp, err := s.authSvc.RefreshSession(r.Context(), req.RefreshToken)
	if err != nil {
		s.auditf(r, "session_refresh_failed", err.Error())
		s.maybeAlertRefreshFailure(r)
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.refreshFailureCounter.Reset(s.clientIP(r))
	s.auditf(r, "session_refresh_ok", "session token rotated")
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if _, ok := s.requireScope(w, r, auth.ScopeDevicesRead); !ok {
		return
	}
	if s.authSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "auth service unavailable"})
		return
	}
	devices, err := s.authSvc.ListDevices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (s *Server) handleDeviceByAddress(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v3/devices/")
	path = strings.Trim(path, "/")
	if path == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "device address missing"})
		return
	}
	parts := strings.Split(path, "/")
	address := parts[0]
	if len(parts) < 2 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown action"})
		return
	}
	action := parts[1]

	principal, ok := s.requireScope(w, r, auth.ScopeDevicesWrite)
	if !ok {
		return
	}
	if !principal.Admin && principal.Address != address {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "can only manage current device"})
		return
	}
	if s.authSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "auth service unavailable"})
		return
	}

	switch action {
	case "rename":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := s.authSvc.RenameDevice(r.Context(), address, req.Name); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"address": address, "renamed": true})
	case "revoke":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		var req struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := s.authSvc.RevokeDevice(r.Context(), address, req.Reason); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"address": address, "revoked": true})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown action"})
	}
}

func pairURI(r *http.Request, code, challenge string) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "127.0.0.1:8765"
	}
	frag := url.Values{}
	frag.Set("code", code)
	frag.Set("challenge", challenge)
	u := url.URL{
		Scheme:   "elix",
		Host:     host,
		Path:     "/pair",
		Fragment: frag.Encode(),
	}
	return u.String()
}

func (s *Server) clientIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); v != "" {
		parts := strings.Split(v, ",")
		if len(parts) > 0 {
			if ip := strings.TrimSpace(parts[0]); ip != "" {
				return ip
			}
		}
	}
	host := strings.TrimSpace(r.RemoteAddr)
	if host == "" {
		return "unknown"
	}
	if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
		return h
	}
	return host
}

func (s *Server) auditf(r *http.Request, event, detail string) {
	log.Printf(
		"audit event=%s ip=%s method=%s path=%s detail=%q",
		event, s.clientIP(r), r.Method, r.URL.Path, detail,
	)
}

func (s *Server) maybeAlertRefreshFailure(r *http.Request) {
	ip := s.clientIP(r)
	n := s.refreshFailureCounter.Inc(ip, time.Now().UTC())
	if n >= s.security.RefreshFailureAlertLimit {
		log.Printf(
			"security_alert event=refresh_fail_burst ip=%s failures=%d window_sec=%d",
			ip, n, int(s.security.RefreshFailureAlertWindow.Seconds()),
		)
	}
}

func (s *Server) maybeAlertAuthFailure(r *http.Request) {
	ip := s.clientIP(r)
	n := s.authFailureCounter.Inc(ip, time.Now().UTC())
	if n >= s.security.AuthFailureAlertLimit {
		log.Printf(
			"security_alert event=auth_fail_burst ip=%s failures=%d window_sec=%d",
			ip, n, int(s.security.AuthFailureAlertWindow.Seconds()),
		)
	}
}

func (s *Server) maybeAlertPairCompleteFailure(r *http.Request) {
	ip := s.clientIP(r)
	n := s.pairCompleteFailureCount.Inc(ip, time.Now().UTC())
	if n >= s.security.PairCompleteFailureAlertLimit {
		log.Printf(
			"security_alert event=pair_complete_fail_burst ip=%s failures=%d window_sec=%d",
			ip, n, int(s.security.PairCompleteFailureAlertWindow.Seconds()),
		)
	}
}

func writeJSON(w http.ResponseWriter, status int, obj any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(obj)
}
