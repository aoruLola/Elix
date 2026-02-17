package run

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"echohelix/internal/driver"
	"echohelix/internal/events"
	"echohelix/internal/ledger"
	"echohelix/internal/policy"
)

type fakeDriver struct {
	name            string
	block           bool
	cancelMu        sync.Mutex
	cancelChan      map[string]chan struct{}
	lastStart       driver.StartRequest
	schemaVersions  []string
	preferredSchema string
}

func newFakeDriver(name string, block bool) *fakeDriver {
	return &fakeDriver{
		name:            name,
		block:           block,
		cancelChan:      map[string]chan struct{}{},
		schemaVersions:  []string{events.SchemaVersionV2},
		preferredSchema: events.SchemaVersionV2,
	}
}

func (d *fakeDriver) Name() string { return d.name }

func (d *fakeDriver) StartRun(ctx context.Context, req driver.StartRequest) (*driver.Stream, error) {
	d.cancelMu.Lock()
	d.lastStart = req
	d.cancelMu.Unlock()

	eventsCh := make(chan events.Event, 8)
	doneCh := make(chan error, 1)

	stop := make(chan struct{})
	d.cancelMu.Lock()
	d.cancelChan[req.RunID] = stop
	d.cancelMu.Unlock()

	go func() {
		defer close(eventsCh)
		defer close(doneCh)
		defer func() {
			d.cancelMu.Lock()
			delete(d.cancelChan, req.RunID)
			d.cancelMu.Unlock()
		}()

		if d.block {
			select {
			case <-ctx.Done():
				doneCh <- ctx.Err()
				return
			case <-stop:
				doneCh <- nil
				return
			}
		}

		eventsCh <- events.Event{
			Type:    events.TypeToken,
			Payload: map[string]any{"text": "fake-output"},
			Source:  "fake",
			TS:      time.Now().UTC(),
		}
		eventsCh <- events.Event{
			Type:    events.TypeDone,
			Payload: map[string]any{"status": "completed"},
			Source:  "fake",
			TS:      time.Now().UTC(),
		}
		doneCh <- nil
	}()

	return &driver.Stream{Events: eventsCh, Done: doneCh}, nil
}

func (d *fakeDriver) Cancel(_ context.Context, runID string) error {
	d.cancelMu.Lock()
	defer d.cancelMu.Unlock()
	if ch, ok := d.cancelChan[runID]; ok {
		close(ch)
	}
	return nil
}

func (d *fakeDriver) Health(context.Context) (driver.Health, error) {
	return driver.Health{OK: true, Message: "ok"}, nil
}

func (d *fakeDriver) Capabilities(context.Context) (driver.CapabilitySet, error) {
	return driver.CapabilitySet{
		Backend:                d.name,
		SupportsCancel:         true,
		SchemaVersions:         d.schemaVersions,
		PreferredSchemaVersion: d.preferredSchema,
	}, nil
}

func setupService(t *testing.T, drv driver.Driver) *Service {
	t.Helper()
	return setupServiceWithDrivers(t, drv)
}

func setupServiceWithDrivers(t *testing.T, drivers ...driver.Driver) *Service {
	t.Helper()

	store, err := ledger.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("init ledger: %v", err)
	}

	reg := driver.NewRegistry()
	for _, drv := range drivers {
		reg.Register(drv)
	}
	svc := NewService(
		store,
		reg,
		NewHub(),
		policy.New([]string{t.TempDir(), "/tmp"}),
		10*time.Second,
		8,
	)
	return svc
}

func waitStatus(t *testing.T, svc *Service, runID string, want ...string) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := svc.GetRun(context.Background(), runID)
		if err == nil {
			for _, w := range want {
				if r.Status == w {
					return r
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	r, _ := svc.GetRun(context.Background(), runID)
	t.Fatalf("timeout waiting status=%v, got=%s", want, r.Status)
	return Run{}
}

func TestSubmitStreamDone(t *testing.T) {
	svc := setupService(t, newFakeDriver("codex", false))

	req := SubmitRequest{
		WorkspaceID:   "ws-1",
		WorkspacePath: "/tmp",
		Backend:       "codex",
		Prompt:        "hello",
		Options: RunOptions{
			Model:   "gpt-5",
			Profile: "default",
			Sandbox: "workspace-write",
		},
	}
	r, err := svc.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	final := waitStatus(t, svc, r.ID, StatusCompleted)
	if final.Options.Model != "gpt-5" {
		t.Fatalf("expected options persisted, got %#v", final.Options)
	}
	if final.Options.SchemaVersion != events.SchemaVersionV2 {
		t.Fatalf("expected default schema_version v2, got %s", final.Options.SchemaVersion)
	}

	evs, err := svc.ListEvents(context.Background(), r.ID, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(evs) < 3 {
		t.Fatalf("expected events, got %d", len(evs))
	}
	for _, ev := range evs {
		if err := events.ValidateEvent(ev); err != nil {
			t.Fatalf("invalid event contract: %v", err)
		}
	}
}

func TestCancel(t *testing.T) {
	svc := setupService(t, newFakeDriver("codex", true))
	r, err := svc.Submit(context.Background(), SubmitRequest{
		WorkspaceID:   "ws-1",
		WorkspacePath: "/tmp",
		Backend:       "codex",
		Prompt:        "cancel me",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	waitStatus(t, svc, r.ID, StatusRunning, StatusStreaming)
	if err := svc.Cancel(context.Background(), r.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitStatus(t, svc, r.ID, StatusCancelled)
}

func TestReplayFromSeq(t *testing.T) {
	svc := setupService(t, newFakeDriver("codex", false))
	r, err := svc.Submit(context.Background(), SubmitRequest{
		WorkspaceID:   "ws-1",
		WorkspacePath: "/tmp",
		Backend:       "codex",
		Prompt:        "replay",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitStatus(t, svc, r.ID, StatusCompleted)

	evs, err := svc.ListEvents(context.Background(), r.ID, 3)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(evs) == 0 {
		t.Fatalf("expected replay events from seq")
	}
	for _, ev := range evs {
		if ev.Seq < 3 {
			t.Fatalf("unexpected seq: %d", ev.Seq)
		}
		if err := events.ValidateEvent(ev); err != nil {
			t.Fatalf("invalid replay event contract: %v", err)
		}
	}
}

func TestSubmitRejectsInvalidOptions(t *testing.T) {
	svc := setupService(t, newFakeDriver("codex", false))
	_, err := svc.Submit(context.Background(), SubmitRequest{
		WorkspaceID:   "ws-1",
		WorkspacePath: "/tmp",
		Backend:       "codex",
		Prompt:        "bad opts",
		Options: RunOptions{
			Sandbox: "evil-mode",
		},
	})
	if err == nil {
		t.Fatalf("expected invalid options error")
	}
}

func TestSchemaNegotiationRequestedV1(t *testing.T) {
	drv := newFakeDriver("codex", false)
	drv.schemaVersions = []string{events.SchemaVersionV1, events.SchemaVersionV2}
	drv.preferredSchema = events.SchemaVersionV2

	svc := setupService(t, drv)
	r, err := svc.Submit(context.Background(), SubmitRequest{
		WorkspaceID:   "ws-1",
		WorkspacePath: "/tmp",
		Backend:       "codex",
		Prompt:        "schema v1",
		Options: RunOptions{
			SchemaVersion: events.SchemaVersionV1,
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	final := waitStatus(t, svc, r.ID, StatusCompleted)
	if final.Options.SchemaVersion != events.SchemaVersionV1 {
		t.Fatalf("expected run schema_version v1, got %s", final.Options.SchemaVersion)
	}
	drv.cancelMu.Lock()
	startSchema := drv.lastStart.Options.SchemaVersion
	drv.cancelMu.Unlock()
	if startSchema != events.SchemaVersionV1 {
		t.Fatalf("expected driver schema_version v1, got %s", startSchema)
	}
}

func TestSchemaNegotiationRejectsUnsupported(t *testing.T) {
	drv := newFakeDriver("codex", false)
	drv.schemaVersions = []string{events.SchemaVersionV2}
	drv.preferredSchema = events.SchemaVersionV2

	svc := setupService(t, drv)
	_, err := svc.Submit(context.Background(), SubmitRequest{
		WorkspaceID:   "ws-1",
		WorkspacePath: "/tmp",
		Backend:       "codex",
		Prompt:        "schema unsupported",
		Options: RunOptions{
			SchemaVersion: events.SchemaVersionV1,
		},
	})
	if err == nil {
		t.Fatalf("expected unsupported schema_version error")
	}
}

func TestListBackendsMultipleDrivers(t *testing.T) {
	svc := setupServiceWithDrivers(
		t,
		newFakeDriver("codex", false),
		newFakeDriver("gemini", false),
	)
	backends, err := svc.ListBackends(context.Background())
	if err != nil {
		t.Fatalf("list backends: %v", err)
	}
	if len(backends) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(backends))
	}

	seen := map[string]bool{}
	for _, b := range backends {
		name, _ := b["name"].(string)
		seen[name] = true
		if _, ok := b["health"]; !ok {
			t.Fatalf("backend %s missing health", name)
		}
		if _, ok := b["capabilities"]; !ok {
			t.Fatalf("backend %s missing capabilities", name)
		}
	}
	if !seen["codex"] || !seen["gemini"] {
		t.Fatalf("expected codex+gemini backends, got %#v", seen)
	}
}
