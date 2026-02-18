package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"echohelix/internal/auth"
	"echohelix/internal/driver"
	"echohelix/internal/events"
	"echohelix/internal/ledger"
	"echohelix/internal/policy"
	"echohelix/internal/run"
	"echohelix/internal/session"
)

type fakeAPIDriver struct{}

func (d *fakeAPIDriver) Name() string { return "codex" }

func (d *fakeAPIDriver) StartRun(ctx context.Context, req driver.StartRequest) (*driver.Stream, error) {
	eventsCh := make(chan events.Event, 2)
	doneCh := make(chan error, 1)
	go func() {
		defer close(eventsCh)
		defer close(doneCh)
		eventsCh <- events.Event{
			Type:    events.TypeToken,
			Payload: map[string]any{"text": "ok"},
			TS:      time.Now().UTC(),
			Source:  "fake",
		}
		eventsCh <- events.Event{
			Type:    events.TypeDone,
			Payload: map[string]any{"status": "completed"},
			TS:      time.Now().UTC(),
			Source:  "fake",
		}
		doneCh <- nil
	}()
	return &driver.Stream{Events: eventsCh, Done: doneCh}, nil
}

func (d *fakeAPIDriver) Cancel(context.Context, string) error { return nil }

func (d *fakeAPIDriver) Health(context.Context) (driver.Health, error) {
	return driver.Health{OK: true, Message: "ok"}, nil
}

func (d *fakeAPIDriver) Capabilities(context.Context) (driver.CapabilitySet, error) {
	return driver.CapabilitySet{
		Backend:                "codex",
		SupportsCancel:         true,
		SchemaVersions:         []string{events.SchemaVersionV1, events.SchemaVersionV2},
		PreferredSchemaVersion: events.SchemaVersionV2,
	}, nil
}

func newTestServer(t *testing.T, securityCfg ...SecurityConfig) *httptest.Server {
	t.Helper()
	store, err := ledger.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("init ledger: %v", err)
	}

	reg := driver.NewRegistry()
	reg.Register(&fakeAPIDriver{})

	runSvc := run.NewService(
		store,
		reg,
		run.NewHub(),
		policy.New([]string{"/tmp"}),
		30*time.Second,
		4,
	)
	runSvc.SetFileStorage(filepath.Join(t.TempDir(), "files"), 2*1024*1024)
	authSvc := auth.New(store, auth.Config{
		AccessTokenTTL:  2 * time.Minute,
		RefreshTokenTTL: 10 * time.Minute,
		PairCodeTTL:     2 * time.Minute,
	})
	s := New("127.0.0.1:0", "admin-token", runSvc, nil, authSvc, securityCfg...)
	ts := httptest.NewServer(s.httpServer.Handler)
	t.Cleanup(ts.Close)
	return ts
}

func newTestServerWithSession(t *testing.T, workspaceRoot string, sessionCfg session.Config, securityCfg ...SecurityConfig) *httptest.Server {
	t.Helper()
	store, err := ledger.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("init ledger: %v", err)
	}

	reg := driver.NewRegistry()
	reg.Register(&fakeAPIDriver{})

	runPolicy := policy.New([]string{workspaceRoot})
	runSvc := run.NewService(
		store,
		reg,
		run.NewHub(),
		runPolicy,
		30*time.Second,
		4,
	)
	runSvc.SetFileStorage(filepath.Join(t.TempDir(), "files"), 2*1024*1024)
	if sessionCfg.StartTimeout <= 0 {
		sessionCfg.StartTimeout = 3 * time.Second
	}
	if sessionCfg.RequestTimeout <= 0 {
		sessionCfg.RequestTimeout = 3 * time.Second
	}
	sessionSvc := session.NewService(sessionCfg, runPolicy)
	t.Cleanup(func() {
		_ = sessionSvc.Shutdown(context.Background())
	})

	authSvc := auth.New(store, auth.Config{
		AccessTokenTTL:  2 * time.Minute,
		RefreshTokenTTL: 10 * time.Minute,
		PairCodeTTL:     2 * time.Minute,
	})
	s := New("127.0.0.1:0", "admin-token", runSvc, sessionSvc, authSvc, securityCfg...)
	ts := httptest.NewServer(s.httpServer.Handler)
	t.Cleanup(ts.Close)
	return ts
}

func testSessionConfig(codexBin string) session.Config {
	return session.Config{
		CodexBin:       codexBin,
		GeminiBin:      codexBin,
		ClaudeBin:      codexBin,
		StartTimeout:   3 * time.Second,
		RequestTimeout: 3 * time.Second,
	}
}

func TestPairAndSessionEndpoints(t *testing.T) {
	ts := newTestServer(t)

	startStatus, startBody := doJSON(t, ts, "POST", "/api/v3/pair/start", "admin-token", map[string]any{
		"permissions": []string{
			auth.ScopeRunsSubmit,
			auth.ScopeRunsRead,
			auth.ScopeRunsCancel,
			auth.ScopeBackendsRead,
			auth.ScopeDevicesRead,
			auth.ScopeDevicesWrite,
		},
	})
	if startStatus != http.StatusOK {
		t.Fatalf("pair start status=%d body=%s", startStatus, string(startBody))
	}

	staticRunStatus, staticRunBody := doJSON(t, ts, "POST", "/api/v3/runs", "admin-token", map[string]any{
		"workspace_id":   "ws-static",
		"workspace_path": "/tmp",
		"backend":        "codex",
		"prompt":         "blocked",
	})
	if staticRunStatus != http.StatusForbidden {
		t.Fatalf("expected static token run submit forbidden, status=%d body=%s", staticRunStatus, string(staticRunBody))
	}
	staticBackendsStatus, _ := doJSON(t, ts, "GET", "/api/v3/backends", "admin-token", nil)
	if staticBackendsStatus != http.StatusOK {
		t.Fatalf("expected static token to access backends, got %d", staticBackendsStatus)
	}
	var startResp struct {
		PairCode  string `json:"pair_code"`
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(startBody, &startResp); err != nil {
		t.Fatalf("decode pair start: %v", err)
	}
	if startResp.PairCode == "" || startResp.Challenge == "" {
		t.Fatalf("invalid pair start response: %#v", startResp)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sig := ed25519.Sign(priv, []byte(startResp.Challenge))
	completeStatus, completeBody := doJSON(t, ts, "POST", "/api/v3/pair/complete", "", map[string]any{
		"pair_code":   startResp.PairCode,
		"public_key":  base64.RawURLEncoding.EncodeToString(pub),
		"signature":   base64.RawURLEncoding.EncodeToString(sig),
		"device_name": "api-test",
	})
	if completeStatus != http.StatusOK {
		t.Fatalf("pair complete status=%d body=%s", completeStatus, string(completeBody))
	}
	var completeResp struct {
		Address      string `json:"address"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(completeBody, &completeResp); err != nil {
		t.Fatalf("decode pair complete: %v", err)
	}
	if completeResp.AccessToken == "" || completeResp.RefreshToken == "" || completeResp.Address == "" {
		t.Fatalf("invalid complete response: %#v", completeResp)
	}

	sessionPairStartStatus, _ := doJSON(t, ts, "POST", "/api/v3/pair/start", completeResp.AccessToken, map[string]any{})
	if sessionPairStartStatus != http.StatusForbidden {
		t.Fatalf("expected session token pair/start forbidden, got %d", sessionPairStartStatus)
	}

	runStatus, runBody := doJSON(t, ts, "POST", "/api/v3/runs", completeResp.AccessToken, map[string]any{
		"workspace_id":   "ws-1",
		"workspace_path": "/tmp",
		"backend":        "codex",
		"prompt":         "hello",
	})
	if runStatus != http.StatusAccepted {
		t.Fatalf("run submit status=%d body=%s", runStatus, string(runBody))
	}

	backendsStatus, _ := doJSON(t, ts, "GET", "/api/v3/backends", completeResp.AccessToken, nil)
	if backendsStatus != http.StatusOK {
		t.Fatalf("backends should be allowed by paired token")
	}

	refreshStatus, refreshBody := doJSON(t, ts, "POST", "/api/v3/session/refresh", "", map[string]any{
		"refresh_token": completeResp.RefreshToken,
	})
	if refreshStatus != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", refreshStatus, string(refreshBody))
	}
	var refreshResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(refreshBody, &refreshResp); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	if refreshResp.AccessToken == "" {
		t.Fatalf("refresh returned empty access token")
	}
	backendsStatusOld, _ := doJSON(t, ts, "GET", "/api/v3/backends", completeResp.AccessToken, nil)
	if backendsStatusOld != http.StatusUnauthorized {
		t.Fatalf("old rotated access token should be unauthorized, got %d", backendsStatusOld)
	}

	revokeStatus, revokeBody := doJSON(t, ts, "POST", "/api/v3/devices/"+completeResp.Address+"/revoke", refreshResp.AccessToken, map[string]any{
		"reason": "security",
	})
	if revokeStatus != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", revokeStatus, string(revokeBody))
	}

	backendsAfterRevoke, _ := doJSON(t, ts, "GET", "/api/v3/backends", refreshResp.AccessToken, nil)
	if backendsAfterRevoke != http.StatusUnauthorized {
		t.Fatalf("revoked token should be unauthorized, got %d", backendsAfterRevoke)
	}
}

func TestPairStartRateLimit(t *testing.T) {
	ts := newTestServer(t, SecurityConfig{
		PairStartRateLimit:  1,
		PairStartRateWindow: 30 * time.Second,
	})

	payload := map[string]any{"permissions": []string{auth.ScopeRunsSubmit}}
	status1, body1 := doJSON(t, ts, "POST", "/api/v3/pair/start", "admin-token", payload)
	if status1 != http.StatusOK {
		t.Fatalf("first pair/start expected 200, got %d body=%s", status1, string(body1))
	}
	status2, body2 := doJSON(t, ts, "POST", "/api/v3/pair/start", "admin-token", payload)
	if status2 != http.StatusTooManyRequests {
		t.Fatalf("second pair/start expected 429, got %d body=%s", status2, string(body2))
	}
	if !strings.Contains(string(body2), "rate_limited") {
		t.Fatalf("expected rate_limited error body, got %s", string(body2))
	}
}

func TestRefreshFailureSecurityAlert(t *testing.T) {
	ts := newTestServer(t, SecurityConfig{
		RefreshFailureAlertLimit:  2,
		RefreshFailureAlertWindow: 30 * time.Second,
	})

	var buf bytes.Buffer
	prevWriter := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prevWriter)

	payload := map[string]any{"refresh_token": "invalid-token"}
	status1, _ := doJSON(t, ts, "POST", "/api/v3/session/refresh", "", payload)
	status2, _ := doJSON(t, ts, "POST", "/api/v3/session/refresh", "", payload)
	if status1 != http.StatusBadRequest || status2 != http.StatusBadRequest {
		t.Fatalf("expected both refresh requests to return 400, got %d and %d", status1, status2)
	}
	if !strings.Contains(buf.String(), "security_alert event=refresh_fail_burst") {
		t.Fatalf("expected refresh security alert log, got logs=%s", buf.String())
	}
}

func TestFileUploadAndRunAttachments(t *testing.T) {
	ts := newTestServer(t)

	startStatus, startBody := doJSON(t, ts, "POST", "/api/v3/pair/start", "admin-token", map[string]any{
		"permissions": []string{auth.ScopeRunsSubmit, auth.ScopeRunsRead},
	})
	if startStatus != http.StatusOK {
		t.Fatalf("pair start status=%d body=%s", startStatus, string(startBody))
	}
	var startResp struct {
		PairCode  string `json:"pair_code"`
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(startBody, &startResp); err != nil {
		t.Fatalf("decode pair start: %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sig := ed25519.Sign(priv, []byte(startResp.Challenge))
	completeStatus, completeBody := doJSON(t, ts, "POST", "/api/v3/pair/complete", "", map[string]any{
		"pair_code":   startResp.PairCode,
		"public_key":  base64.RawURLEncoding.EncodeToString(pub),
		"signature":   base64.RawURLEncoding.EncodeToString(sig),
		"device_name": "attach-test",
	})
	if completeStatus != http.StatusOK {
		t.Fatalf("pair complete status=%d body=%s", completeStatus, string(completeBody))
	}
	var completeResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(completeBody, &completeResp); err != nil {
		t.Fatalf("decode pair complete: %v", err)
	}

	uploadStatus, uploadBody := doMultipart(
		t, ts,
		"/api/v3/files",
		completeResp.AccessToken,
		"file",
		"spec.md",
		[]byte("# spec\nhello"),
	)
	if uploadStatus != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", uploadStatus, string(uploadBody))
	}
	var uploadResp struct {
		FileID string `json:"file_id"`
	}
	if err := json.Unmarshal(uploadBody, &uploadResp); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if uploadResp.FileID == "" {
		t.Fatalf("empty file_id in upload response")
	}

	runStatus, runBody := doJSON(t, ts, "POST", "/api/v3/runs", completeResp.AccessToken, map[string]any{
		"workspace_id":   "ws-attach",
		"workspace_path": "/tmp",
		"backend":        "codex",
		"prompt":         "read @spec.md",
		"context": map[string]any{
			"attachments": []map[string]any{
				{"file_id": uploadResp.FileID, "alias": "spec.md"},
			},
		},
	})
	if runStatus != http.StatusAccepted {
		t.Fatalf("run submit status=%d body=%s", runStatus, string(runBody))
	}
	var runResp struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(runBody, &runResp); err != nil {
		t.Fatalf("decode run submit response: %v", err)
	}
	if runResp.RunID == "" {
		t.Fatalf("missing run id")
	}

	getStatus, getBody := doJSON(t, ts, "GET", "/api/v3/runs/"+runResp.RunID, completeResp.AccessToken, nil)
	if getStatus != http.StatusOK {
		t.Fatalf("get run status=%d body=%s", getStatus, string(getBody))
	}
	var runObj struct {
		Prompt      string `json:"prompt"`
		Attachments []struct {
			FileID string `json:"file_id"`
			Alias  string `json:"alias"`
			Path   string `json:"path"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(getBody, &runObj); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if len(runObj.Attachments) != 1 {
		t.Fatalf("expected one attachment, got %d", len(runObj.Attachments))
	}
	if runObj.Attachments[0].FileID != uploadResp.FileID {
		t.Fatalf("attachment file mismatch: got=%s want=%s", runObj.Attachments[0].FileID, uploadResp.FileID)
	}
	if !strings.Contains(runObj.Prompt, ".elix/attachments/spec.md") {
		t.Fatalf("expected prompt mention rewrite, got prompt=%q", runObj.Prompt)
	}
}

func TestEmergencyStopResumeEndpoints(t *testing.T) {
	ts := newTestServer(t)

	startStatus, startBody := doJSON(t, ts, "POST", "/api/v3/pair/start", "admin-token", map[string]any{
		"permissions": []string{auth.ScopeRunsSubmit, auth.ScopeRunsRead},
	})
	if startStatus != http.StatusOK {
		t.Fatalf("pair start status=%d body=%s", startStatus, string(startBody))
	}
	var startResp struct {
		PairCode  string `json:"pair_code"`
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(startBody, &startResp); err != nil {
		t.Fatalf("decode pair start: %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sig := ed25519.Sign(priv, []byte(startResp.Challenge))
	completeStatus, completeBody := doJSON(t, ts, "POST", "/api/v3/pair/complete", "", map[string]any{
		"pair_code":   startResp.PairCode,
		"public_key":  base64.RawURLEncoding.EncodeToString(pub),
		"signature":   base64.RawURLEncoding.EncodeToString(sig),
		"device_name": "emergency-test",
	})
	if completeStatus != http.StatusOK {
		t.Fatalf("pair complete status=%d body=%s", completeStatus, string(completeBody))
	}
	var completeResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(completeBody, &completeResp); err != nil {
		t.Fatalf("decode pair complete: %v", err)
	}

	sessionStatus, _ := doJSON(t, ts, "GET", "/api/v3/emergency/status", completeResp.AccessToken, nil)
	if sessionStatus != http.StatusForbidden {
		t.Fatalf("expected session token emergency/status forbidden, got %d", sessionStatus)
	}

	stopStatus, stopBody := doJSON(t, ts, "POST", "/api/v3/emergency/stop", "admin-token", map[string]any{
		"reason": "maintenance",
	})
	if stopStatus != http.StatusOK {
		t.Fatalf("stop status=%d body=%s", stopStatus, string(stopBody))
	}
	var stopResp struct {
		Active bool   `json:"active"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(stopBody, &stopResp); err != nil {
		t.Fatalf("decode stop response: %v", err)
	}
	if !stopResp.Active || stopResp.Reason != "maintenance" {
		t.Fatalf("unexpected stop response: %#v", stopResp)
	}

	runStatus, runBody := doJSON(t, ts, "POST", "/api/v3/runs", completeResp.AccessToken, map[string]any{
		"workspace_id":   "ws-blocked",
		"workspace_path": "/tmp",
		"backend":        "codex",
		"prompt":         "blocked by emergency",
	})
	if runStatus != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 while emergency active, got %d body=%s", runStatus, string(runBody))
	}
	if !strings.Contains(string(runBody), "emergency_stop_active") {
		t.Fatalf("expected emergency_stop_active error, body=%s", string(runBody))
	}

	resumeStatus, resumeBody := doJSON(t, ts, "POST", "/api/v3/emergency/resume", "admin-token", map[string]any{})
	if resumeStatus != http.StatusOK {
		t.Fatalf("resume status=%d body=%s", resumeStatus, string(resumeBody))
	}

	runStatusAfterResume, runBodyAfterResume := doJSON(t, ts, "POST", "/api/v3/runs", completeResp.AccessToken, map[string]any{
		"workspace_id":   "ws-resumed",
		"workspace_path": "/tmp",
		"backend":        "codex",
		"prompt":         "works again",
	})
	if runStatusAfterResume != http.StatusAccepted {
		t.Fatalf("expected 202 after resume, got %d body=%s", runStatusAfterResume, string(runBodyAfterResume))
	}
}

func TestSessionsEndpointUnavailableByDefault(t *testing.T) {
	ts := newTestServer(t)
	status, body := doJSON(t, ts, "GET", "/api/v3/sessions", "admin-token", nil)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when session service is disabled, got %d body=%s", status, string(body))
	}
	status2, body2 := doJSON(t, ts, "GET", "/api/v3/sessions/demo/backend/status", "admin-token", nil)
	if status2 != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for backend/status when session service is disabled, got %d body=%s", status2, string(body2))
	}
	status3, body3 := doJSON(t, ts, "POST", "/api/v3/sessions/demo/backend/call", "admin-token", map[string]any{"method": "status"})
	if status3 != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for backend/call when session service is disabled, got %d body=%s", status3, string(body3))
	}
}

func TestBackendCallScopeMapping(t *testing.T) {
	s := &Server{
		backendCallReadSet:   makeMethodSet([]string{"status", "foo/read"}),
		backendCallCancelSet: makeMethodSet([]string{"turn/interrupt", "foo/cancel"}),
	}
	if got := s.backendCallScope("status"); got != auth.ScopeRunsRead {
		t.Fatalf("status scope mismatch: %s", got)
	}
	if got := s.backendCallScope("foo/read"); got != auth.ScopeRunsRead {
		t.Fatalf("foo/read scope mismatch: %s", got)
	}
	if got := s.backendCallScope("turn/interrupt"); got != auth.ScopeRunsCancel {
		t.Fatalf("turn/interrupt scope mismatch: %s", got)
	}
	if got := s.backendCallScope("foo/cancel"); got != auth.ScopeRunsCancel {
		t.Fatalf("foo/cancel scope mismatch: %s", got)
	}
	if got := s.backendCallScope("turn/start"); got != auth.ScopeRunsSubmit {
		t.Fatalf("turn/start scope mismatch: %s", got)
	}
}

func TestSessionCreateSupportsGeminiBackendAPI(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ws-gemini")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	codexBin := writeFakeCodexForAPI(t, root)
	ts := newTestServerWithSession(t, root, testSessionConfig(codexBin))

	accessToken := issueAccessTokenForScopes(t, ts, []string{auth.ScopeRunsSubmit})
	status, body := doJSON(t, ts, "POST", "/api/v3/sessions", accessToken, map[string]any{
		"workspace_id":   "ws-gemini",
		"workspace_path": workspace,
		"backend":        "gemini",
	})
	if status != http.StatusCreated {
		t.Fatalf("create gemini session status=%d body=%s", status, string(body))
	}
	var resp struct {
		SessionID string `json:"session_id"`
		Backend   string `json:"backend"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode create session response: %v", err)
	}
	if resp.SessionID == "" || resp.Backend != "gemini" || resp.Status != session.StatusReady {
		t.Fatalf("unexpected session response: %#v", resp)
	}
}

func TestSessionCreateSupportsClaudeBackendAPI(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ws-claude")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	codexBin := writeFakeCodexForAPI(t, root)
	ts := newTestServerWithSession(t, root, testSessionConfig(codexBin))

	accessToken := issueAccessTokenForScopes(t, ts, []string{auth.ScopeRunsSubmit})
	status, body := doJSON(t, ts, "POST", "/api/v3/sessions", accessToken, map[string]any{
		"workspace_id":   "ws-claude",
		"workspace_path": workspace,
		"backend":        "claude",
	})
	if status != http.StatusCreated {
		t.Fatalf("create claude session status=%d body=%s", status, string(body))
	}
	var resp struct {
		SessionID string `json:"session_id"`
		Backend   string `json:"backend"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode create session response: %v", err)
	}
	if resp.SessionID == "" || resp.Backend != "claude" || resp.Status != session.StatusReady {
		t.Fatalf("unexpected session response: %#v", resp)
	}
}

func TestSessionCreateRejectsUnknownBackendAPI(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ws-unknown")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	codexBin := writeFakeCodexForAPI(t, root)
	ts := newTestServerWithSession(t, root, testSessionConfig(codexBin))

	accessToken := issueAccessTokenForScopes(t, ts, []string{auth.ScopeRunsSubmit})
	status, body := doJSON(t, ts, "POST", "/api/v3/sessions", accessToken, map[string]any{
		"workspace_id":   "ws-unknown",
		"workspace_path": workspace,
		"backend":        "not-supported",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("create unknown backend session status=%d body=%s", status, string(body))
	}
	if !strings.Contains(string(body), "unsupported backend") {
		t.Fatalf("expected unsupported backend error, got %s", string(body))
	}
}

func TestSessionBackendCallPassthrough(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	codexBin := writeFakeCodexForAPI(t, root)
	ts := newTestServerWithSession(t, root, testSessionConfig(codexBin))

	accessToken := issueAccessTokenForScopes(t, ts, []string{auth.ScopeRunsSubmit, auth.ScopeRunsRead, auth.ScopeRunsCancel})
	createStatus, createBody := doJSON(t, ts, "POST", "/api/v3/sessions", accessToken, map[string]any{
		"workspace_id":   "ws-api",
		"workspace_path": workspace,
		"backend":        "codex",
	})
	if createStatus != http.StatusCreated {
		t.Fatalf("create session status=%d body=%s", createStatus, string(createBody))
	}
	var createResp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(createBody, &createResp); err != nil {
		t.Fatalf("decode create session response: %v", err)
	}
	if createResp.SessionID == "" {
		t.Fatalf("missing session id in create response: %s", string(createBody))
	}

	callStatus, callBody := doJSON(t, ts, "POST", "/api/v3/sessions/"+createResp.SessionID+"/backend/call", accessToken, map[string]any{
		"method": "status",
	})
	if callStatus != http.StatusOK {
		t.Fatalf("backend call status=%d body=%s", callStatus, string(callBody))
	}
	var callResp struct {
		Method string         `json:"method"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(callBody, &callResp); err != nil {
		t.Fatalf("decode backend call response: %v", err)
	}
	if callResp.Method != "status" || callResp.Result["state"] != "ready" {
		t.Fatalf("unexpected backend call response: %#v", callResp)
	}

	readOnlyToken := issueAccessTokenForScopes(t, ts, []string{auth.ScopeRunsRead})
	callReadStatus, callReadBody := doJSON(t, ts, "POST", "/api/v3/sessions/"+createResp.SessionID+"/backend/call", readOnlyToken, map[string]any{
		"method": "status",
	})
	if callReadStatus != http.StatusOK {
		t.Fatalf("expected backend/call status allowed for read-only token, got %d body=%s", callReadStatus, string(callReadBody))
	}

	callReadForbidden, callReadForbiddenBody := doJSON(t, ts, "POST", "/api/v3/sessions/"+createResp.SessionID+"/backend/call", readOnlyToken, map[string]any{
		"method": "turn/start",
		"params": map[string]any{"threadId": "thr_api", "input": []map[string]any{{"type": "text", "text": "hi"}}},
	})
	if callReadForbidden != http.StatusForbidden {
		t.Fatalf("expected backend/call turn/start forbidden for read-only token, got %d body=%s", callReadForbidden, string(callReadForbiddenBody))
	}

	submitOnlyToken := issueAccessTokenForScopes(t, ts, []string{auth.ScopeRunsSubmit})
	callSubmitForbidden, callSubmitForbiddenBody := doJSON(t, ts, "POST", "/api/v3/sessions/"+createResp.SessionID+"/backend/call", submitOnlyToken, map[string]any{
		"method": "turn/interrupt",
		"params": map[string]any{"threadId": "thr_api", "turnId": "turn_1"},
	})
	if callSubmitForbidden != http.StatusForbidden {
		t.Fatalf("expected backend/call turn/interrupt forbidden for submit-only token, got %d body=%s", callSubmitForbidden, string(callSubmitForbiddenBody))
	}
}

func issueAccessTokenForScopes(t *testing.T, ts *httptest.Server, scopes []string) string {
	t.Helper()
	startStatus, startBody := doJSON(t, ts, "POST", "/api/v3/pair/start", "admin-token", map[string]any{
		"permissions": scopes,
	})
	if startStatus != http.StatusOK {
		t.Fatalf("pair start status=%d body=%s", startStatus, string(startBody))
	}
	var startResp struct {
		PairCode  string `json:"pair_code"`
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(startBody, &startResp); err != nil {
		t.Fatalf("decode pair start: %v", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sig := ed25519.Sign(priv, []byte(startResp.Challenge))
	completeStatus, completeBody := doJSON(t, ts, "POST", "/api/v3/pair/complete", "", map[string]any{
		"pair_code":  startResp.PairCode,
		"public_key": base64.RawURLEncoding.EncodeToString(pub),
		"signature":  base64.RawURLEncoding.EncodeToString(sig),
	})
	if completeStatus != http.StatusOK {
		t.Fatalf("pair complete status=%d body=%s", completeStatus, string(completeBody))
	}
	var completeResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(completeBody, &completeResp); err != nil {
		t.Fatalf("decode pair complete: %v", err)
	}
	if completeResp.AccessToken == "" {
		t.Fatalf("empty access token in pair complete response: %s", string(completeBody))
	}
	return completeResp.AccessToken
}

func writeFakeCodexForAPI(t *testing.T, dir string) string {
	t.Helper()
	srcPath := filepath.Join(dir, "fake-codex-api.go")
	source := `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		id := extractID(line)
		switch {
		case strings.Contains(line, "\"method\":\"initialize\""):
			writef("{\"id\":\"%s\",\"result\":{\"userAgent\":\"fake-api\"}}", id)
		case strings.Contains(line, "\"method\":\"thread/start\""):
			writef("{\"id\":\"%s\",\"result\":{\"thread\":{\"id\":\"thr_api\"}}}", id)
			writef("{\"method\":\"thread/started\",\"params\":{\"thread\":{\"id\":\"thr_api\"}}}")
		case strings.Contains(line, "\"method\":\"status\""):
			writef("{\"id\":\"%s\",\"result\":{\"state\":\"ready\",\"source\":\"fake-api\"}}", id)
		}
	}
}

func extractID(line string) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return ""
	}
	idRaw, ok := raw["id"]
	if !ok {
		return ""
	}
	var id any
	if err := json.Unmarshal(idRaw, &id); err != nil {
		return ""
	}
	return fmt.Sprintf("%v", id)
}

func writef(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}
`
	if err := os.WriteFile(srcPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write fake codex source: %v", err)
	}
	binPath := filepath.Join(dir, "fake-codex-api")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binPath, srcPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake codex api: %v, output=%s", err, strings.TrimSpace(string(out)))
	}
	return binPath
}

func doJSON(t *testing.T, ts *httptest.Server, method, path, bearer string, payload any) (int, []byte) {
	t.Helper()
	var body []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = b
	}
	req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	respBody := make([]byte, 0, 1024)
	buf := make([]byte, 512)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			respBody = append(respBody, buf[:n]...)
		}
		if readErr != nil {
			break
		}
	}
	return resp.StatusCode, respBody
}

func doMultipart(
	t *testing.T,
	ts *httptest.Server,
	path string,
	bearer string,
	field string,
	filename string,
	content []byte,
) (int, []byte) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+path, &body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody
}
