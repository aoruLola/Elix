package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"echohelix/internal/policy"
)

func TestSessionCreateTurnAndApprovalFlow(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	fakeCodex := writeFakeCodex(t, root)

	svc := NewService(Config{
		CodexBin:       fakeCodex,
		StartTimeout:   3 * time.Second,
		RequestTimeout: 3 * time.Second,
	}, policy.New([]string{root}))

	sess, err := svc.Create(context.Background(), CreateRequest{WorkspacePath: workspace, Backend: "codex"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if sess.ThreadID == "" || sess.Status != StatusReady {
		t.Fatalf("unexpected session: %#v", sess)
	}

	turn, err := svc.StartTurn(context.Background(), sess.ID, StartTurnRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("start turn: %v", err)
	}
	if turn.TurnID == "" {
		t.Fatalf("expected turn id")
	}

	waitFor(t, 2*time.Second, func() bool {
		items, _ := svc.ListApprovals(sess.ID)
		return len(items) == 1
	})

	approvals, err := svc.ListApprovals(sess.ID)
	if err != nil {
		t.Fatalf("list approvals: %v", err)
	}
	if len(approvals) != 1 {
		t.Fatalf("expected one approval, got %d", len(approvals))
	}
	if approvals[0].Command != "echo hi" {
		t.Fatalf("unexpected approval payload: %#v", approvals[0])
	}

	if err := svc.ResolveApproval(context.Background(), sess.ID, approvals[0].RequestID, ApprovalDecision{Decision: "accept"}); err != nil {
		t.Fatalf("resolve approval: %v", err)
	}

	callRes, err := svc.BackendCall(context.Background(), sess.ID, BackendCallRequest{Method: "status"})
	if err != nil {
		t.Fatalf("backend call status: %v", err)
	}
	if callRes.Method != "status" {
		t.Fatalf("unexpected method: %#v", callRes)
	}
	result, ok := callRes.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected backend call result type: %T", callRes.Result)
	}
	if result["state"] != "ready" {
		t.Fatalf("unexpected backend call result: %#v", result)
	}
	if _, err := svc.BackendCall(context.Background(), sess.ID, BackendCallRequest{Method: "initialize"}); err == nil {
		t.Fatalf("expected initialize to be blocked by bridge")
	}
	if _, err := svc.BackendCall(context.Background(), sess.ID, BackendCallRequest{Method: "status", TimeoutMS: 600001}); err == nil {
		t.Fatalf("expected timeout guard to reject oversized timeout_ms")
	}

	backendStatus, err := svc.BackendStatus(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("backend status: %v", err)
	}
	if _, ok := backendStatus.Result.(map[string]any); !ok {
		t.Fatalf("unexpected backend status result type: %T", backendStatus.Result)
	}

	waitFor(t, 2*time.Second, func() bool {
		evs, _ := svc.ListEvents(sess.ID, 0)
		for _, ev := range evs {
			if ev.Method == "turn/completed" {
				return true
			}
		}
		return false
	})

	if err := svc.Close(sess.ID); err != nil {
		t.Fatalf("close session: %v", err)
	}
}

func writeFakeCodex(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-codex.sh")
	script := `#!/usr/bin/env bash
set -euo pipefail
turn=0
while IFS= read -r line; do
  if [[ -z "$line" ]]; then
    continue
  fi

  if [[ "$line" == *'"method":"initialize"'* ]]; then
    id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
    printf '{"id":"%s","result":{"userAgent":"fake"}}\n' "$id"
    continue
  fi

  if [[ "$line" == *'"method":"thread/start"'* ]]; then
    id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
    printf '{"id":"%s","result":{"thread":{"id":"thr_test"}}}\n' "$id"
    printf '{"method":"thread/started","params":{"thread":{"id":"thr_test"}}}\n'
    continue
  fi

  if [[ "$line" == *'"method":"status"'* ]]; then
    id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
    printf '{"id":"%s","result":{"state":"ready","model":"gpt-5"}}\n' "$id"
    continue
  fi

  if [[ "$line" == *'"method":"turn/start"'* ]]; then
    id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
    turn=$((turn+1))
    tid="turn_${turn}"
    rid="apr_${turn}"
    printf '{"id":"%s","result":{"turn":{"id":"%s","status":"inProgress","threadId":"thr_test"}}}\n' "$id" "$tid"
    printf '{"method":"turn/started","params":{"turn":{"id":"%s","status":"inProgress"}}}\n' "$tid"
    printf '{"method":"item/agentMessage/delta","params":{"threadId":"thr_test","turnId":"%s","itemId":"itm_%s","delta":"ok"}}\n' "$tid" "$turn"
    printf '{"method":"item/commandExecution/requestApproval","id":"%s","params":{"threadId":"thr_test","turnId":"%s","itemId":"cmd_%s","command":"echo hi","cwd":"/tmp"}}\n' "$rid" "$tid" "$turn"
    continue
  fi

  if [[ "$line" == *'"id":"apr_'* ]]; then
    printf '{"method":"item/completed","params":{"item":{"type":"commandExecution","id":"cmd_1","status":"completed"}}}\n'
    printf '{"method":"turn/completed","params":{"turn":{"id":"turn_1","status":"completed"}}}\n'
    continue
  fi

  if [[ "$line" == *'"method":"turn/interrupt"'* ]]; then
    id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
    printf '{"id":"%s","result":{}}\n' "$id"
    printf '{"method":"turn/completed","params":{"turn":{"id":"turn_1","status":"interrupted"}}}\n'
    continue
  fi
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex script: %v", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod fake codex script: %v", err)
	}
	return path
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", strings.TrimSpace(timeout.String()))
}
