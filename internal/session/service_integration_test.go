package session

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"echohelix/internal/policy"
)

func TestSessionCreateSupportsGeminiBackend(t *testing.T) {
	testSessionCreateSupportsBackend(t, "gemini")
}

func TestSessionCreateSupportsClaudeBackend(t *testing.T) {
	testSessionCreateSupportsBackend(t, "claude")
}

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

func TestSessionCleanupRemovesExpiredClosedSessions(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	fakeCodex := writeFakeCodex(t, root)

	svc := NewService(Config{
		CodexBin:             fakeCodex,
		StartTimeout:         3 * time.Second,
		RequestTimeout:       3 * time.Second,
		SessionRetention:     50 * time.Millisecond,
		SessionCleanupPeriod: 10 * time.Millisecond,
	}, policy.New([]string{root}))

	sess, err := svc.Create(context.Background(), CreateRequest{WorkspacePath: workspace, Backend: "codex"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := svc.Close(sess.ID); err != nil {
		t.Fatalf("close session: %v", err)
	}

	time.Sleep(80 * time.Millisecond)
	_ = svc.List() // trigger lazy cleanup

	if _, err := svc.Get(sess.ID); err == nil {
		t.Fatalf("expected expired closed session to be cleaned up")
	}
}

func testSessionCreateSupportsBackend(t *testing.T, backend string) {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	fakeCodex := writeFakeCodex(t, root)
	writeBinaryAlias(t, fakeCodex, filepath.Join(root, backendBinaryName(backend)))

	pathSep := string(os.PathListSeparator)
	t.Setenv("PATH", root+pathSep+os.Getenv("PATH"))

	svc := NewService(Config{
		CodexBin:       fakeCodex,
		StartTimeout:   3 * time.Second,
		RequestTimeout: 3 * time.Second,
	}, policy.New([]string{root}))

	sess, err := svc.Create(context.Background(), CreateRequest{WorkspacePath: workspace, Backend: backend})
	if err != nil {
		t.Fatalf("create session with backend %q: %v", backend, err)
	}
	if sess.Status != StatusReady {
		t.Fatalf("expected status ready for backend %q, got %#v", backend, sess)
	}
	if sess.Backend != backend {
		t.Fatalf("expected backend %q, got %#v", backend, sess)
	}
	if err := svc.Close(sess.ID); err != nil {
		t.Fatalf("close session: %v", err)
	}
}

func writeFakeCodex(t *testing.T, dir string) string {
	t.Helper()
	srcPath := filepath.Join(dir, "fake-codex.go")
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
	turn := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		id := extractID(line)
		switch {
		case strings.Contains(line, "\"method\":\"initialize\""):
			writef("{\"id\":\"%s\",\"result\":{\"userAgent\":\"fake\"}}", id)
		case strings.Contains(line, "\"method\":\"thread/start\""):
			writef("{\"id\":\"%s\",\"result\":{\"thread\":{\"id\":\"thr_test\"}}}", id)
			writef("{\"method\":\"thread/started\",\"params\":{\"thread\":{\"id\":\"thr_test\"}}}")
		case strings.Contains(line, "\"method\":\"status\""):
			writef("{\"id\":\"%s\",\"result\":{\"state\":\"ready\",\"model\":\"gpt-5\"}}", id)
		case strings.Contains(line, "\"method\":\"turn/start\""):
			turn++
			tid := fmt.Sprintf("turn_%d", turn)
			rid := fmt.Sprintf("apr_%d", turn)
			itemID := fmt.Sprintf("itm_%d", turn)
			cmdID := fmt.Sprintf("cmd_%d", turn)
			writef("{\"id\":\"%s\",\"result\":{\"turn\":{\"id\":\"%s\",\"status\":\"inProgress\",\"threadId\":\"thr_test\"}}}", id, tid)
			writef("{\"method\":\"turn/started\",\"params\":{\"turn\":{\"id\":\"%s\",\"status\":\"inProgress\"}}}", tid)
			writef("{\"method\":\"item/agentMessage/delta\",\"params\":{\"threadId\":\"thr_test\",\"turnId\":\"%s\",\"itemId\":\"%s\",\"delta\":\"ok\"}}", tid, itemID)
			writef("{\"method\":\"item/commandExecution/requestApproval\",\"id\":\"%s\",\"params\":{\"threadId\":\"thr_test\",\"turnId\":\"%s\",\"itemId\":\"%s\",\"command\":\"echo hi\",\"cwd\":\"/tmp\"}}", rid, tid, cmdID)
		case strings.Contains(line, "\"id\":\"apr_"):
			writef("{\"method\":\"item/completed\",\"params\":{\"item\":{\"type\":\"commandExecution\",\"id\":\"cmd_1\",\"status\":\"completed\"}}}")
			writef("{\"method\":\"turn/completed\",\"params\":{\"turn\":{\"id\":\"turn_1\",\"status\":\"completed\"}}}")
		case strings.Contains(line, "\"method\":\"turn/interrupt\""):
			writef("{\"id\":\"%s\",\"result\":{}}", id)
			writef("{\"method\":\"turn/completed\",\"params\":{\"turn\":{\"id\":\"turn_1\",\"status\":\"interrupted\"}}}")
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
	binPath := filepath.Join(dir, "fake-codex")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binPath, srcPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake codex: %v, output=%s", err, strings.TrimSpace(string(out)))
	}
	return binPath
}

func backendBinaryName(backend string) string {
	name := strings.ToLower(strings.TrimSpace(backend))
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func writeBinaryAlias(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open source binary: %v", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatalf("open destination binary: %v", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("copy binary alias: %v", err)
	}
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
