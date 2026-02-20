package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"echohelix/internal/auth"

	"github.com/gorilla/websocket"
)

func TestSessionEventsWebSocketSupportsAccessTokenQuery(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	codexBin := writeFakeCodexForAPI(t, root)
	ts := newTestServerWithSession(t, root, testSessionConfig(codexBin))

	accessToken := issueAccessTokenForScopes(t, ts, []string{auth.ScopeRunsSubmit, auth.ScopeRunsRead})
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
		t.Fatalf("missing session id in create response")
	}

	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) +
		"/api/v3/sessions/" + url.PathEscape(createResp.SessionID) + "/events?access_token=" + url.QueryEscape(accessToken)
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("websocket dial failed status=%d err=%v", status, err)
	}
	defer conn.Close()

	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read first event: %v", err)
	}
	if msg["session_id"] != createResp.SessionID {
		t.Fatalf("unexpected event session_id: %#v", msg)
	}
}

func TestSessionEventsWebSocketRejectsInvalidAccessTokenQuery(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	codexBin := writeFakeCodexForAPI(t, root)
	ts := newTestServerWithSession(t, root, testSessionConfig(codexBin))

	accessToken := issueAccessTokenForScopes(t, ts, []string{auth.ScopeRunsSubmit, auth.ScopeRunsRead})
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
		t.Fatalf("missing session id in create response")
	}

	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) +
		"/api/v3/sessions/" + url.PathEscape(createResp.SessionID) + "/events?access_token=bad-token"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		conn.Close()
		t.Fatalf("expected websocket dial to fail for invalid token")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("expected 401 unauthorized, got status=%d err=%v", status, err)
	}
}
