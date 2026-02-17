package run

import (
	"context"
	"fmt"
	"log"
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
}

type activeRun struct {
	driver        driver.Driver
	cancel        context.CancelFunc
	seq           int64
	status        string
	schemaVersion string
}

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
	return &Service{
		ledger:        ledgerStore,
		registry:      registry,
		hub:           hub,
		policy:        p,
		runTimeout:    runTimeout,
		maxConcurrent: maxConcurrent,
		slots:         make(chan struct{}, maxConcurrent),
		active:        map[string]*activeRun{},
	}
}

func (s *Service) Submit(ctx context.Context, req SubmitRequest) (Run, error) {
	if req.Backend == "" {
		req.Backend = "codex"
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

	now := time.Now().UTC()
	r := Run{
		ID:          uuid.NewString(),
		WorkspaceID: req.WorkspaceID,
		Workspace:   req.WorkspacePath,
		Backend:     req.Backend,
		Prompt:      req.Prompt,
		Context:     req.Context,
		Options:     req.Options,
		Status:      StatusQueued,
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

	runCtx, cancel := context.WithTimeout(context.Background(), s.runTimeout)
	defer cancel()

	s.mu.Lock()
	s.active[r.ID] = &activeRun{
		driver:        drv,
		cancel:        cancel,
		seq:           1,
		status:        StatusQueued,
		schemaVersion: r.Options.SchemaVersion,
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
	for {
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
			if ev.Type == events.TypeDone {
				sawDone = true
				s.setStatus(runCtx, r.ID, StatusCompleted, "")
			}
			_ = s.ledger.AppendEvent(runCtx, ev)
			s.hub.Publish(ev)
		case doneErr, ok := <-stream.Done:
			if !ok {
				stream.Done = nil
				continue
			}
			if doneErr != nil {
				st := s.currentStatus(r.ID)
				if st != StatusCancelled && st != StatusCancelling {
					s.setStatus(runCtx, r.ID, StatusFailed, doneErr.Error())
					s.emit(runCtx, r.ID, r.Backend, "bridge", events.TypeError, map[string]any{"message": doneErr.Error()})
				}
				return
			}
			if !sawDone && s.currentStatus(r.ID) != StatusCancelled {
				s.setStatus(runCtx, r.ID, StatusCompleted, "")
				s.emit(runCtx, r.ID, r.Backend, "bridge", events.TypeDone, map[string]any{"status": StatusCompleted})
			}
			return
		}
	}
}

func (s *Service) Cancel(ctx context.Context, runID string) error {
	rec, err := s.ledger.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	s.setStatus(ctx, runID, StatusCancelling, "")
	s.emit(ctx, runID, rec.Backend, "bridge", events.TypeStatus, map[string]any{"status": StatusCancelling})

	s.mu.Lock()
	ar := s.active[runID]
	s.mu.Unlock()
	if ar != nil {
		if err := ar.driver.Cancel(ctx, runID); err != nil {
			log.Printf("cancel driver run=%s: %v", runID, err)
		}
		ar.cancel()
	}

	s.setStatus(ctx, runID, StatusCancelled, "")
	s.emit(ctx, runID, rec.Backend, "bridge", events.TypeStatus, map[string]any{"status": StatusCancelled})
	return nil
}

func (s *Service) GetRun(ctx context.Context, runID string) (Run, error) {
	rec, err := s.ledger.GetRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	return Run{
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
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	}, nil
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if ar := s.active[runID]; ar != nil {
		ar.status = status
	}
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
