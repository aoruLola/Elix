package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"echohelix/internal/run"

	"github.com/gorilla/websocket"
)

type Server struct {
	httpServer *http.Server
	runSvc     *run.Service
	authToken  string
}

func New(addr string, authToken string, runSvc *run.Service) *Server {
	s := &Server{
		runSvc:    runSvc,
		authToken: authToken,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/api/v3/backends", s.withAuth(s.handleBackends))
	mux.HandleFunc("/api/v3/runs", s.withAuth(s.handleRuns))
	mux.HandleFunc("/api/v3/runs/", s.withAuth(s.handleRunByID))

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
		if s.authToken != "" {
			auth := strings.TrimSpace(r.Header.Get("Authorization"))
			want := "Bearer " + s.authToken
			if auth != want {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"error": map[string]any{
						"code":    "unauthorized",
						"message": "missing or invalid bearer token",
					},
				})
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var req run.SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	obj, err := s.runSvc.Submit(r.Context(), req)
	if err != nil {
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
	// /api/v3/runs/{id}
	// /api/v3/runs/{id}/cancel
	// /api/v3/runs/{id}/events
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

func (s *Server) handleBackends(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	backends, err := s.runSvc.ListBackends(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backends": backends})
}

func writeJSON(w http.ResponseWriter, status int, obj any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(obj)
}
