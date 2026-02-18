package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"echohelix/internal/events"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type RunRecord struct {
	ID          string
	WorkspaceID string
	Workspace   string
	Backend     string
	Prompt      string
	Context     map[string]any
	Options     RunOptionsRecord
	Status      string
	Error       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type RunOptionsRecord struct {
	Model         string
	Profile       string
	Sandbox       string
	SchemaVersion string
}

type TokenUsageRecord struct {
	RunID        string
	Backend      string
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	RecordedAt   time.Time
}

type TokenUsageAggregate struct {
	Backend      string
	RunCount     int64
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}

type persistedContext struct {
	Context map[string]any   `json:"context,omitempty"`
	Options RunOptionsRecord `json:"options,omitempty"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Init(ctx context.Context) error {
	schema := `
CREATE TABLE IF NOT EXISTS runs (
  run_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  workspace_path TEXT NOT NULL,
  backend TEXT NOT NULL,
  prompt TEXT NOT NULL,
  context_json TEXT NOT NULL,
  status TEXT NOT NULL,
  error_text TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  ts TEXT NOT NULL,
  schema_version TEXT NOT NULL DEFAULT 'v2',
  type TEXT NOT NULL,
  channel TEXT NOT NULL DEFAULT '',
  format TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT '',
  compat_json TEXT NOT NULL DEFAULT '{}',
  payload_json TEXT NOT NULL,
  backend TEXT NOT NULL,
  source TEXT NOT NULL,
  UNIQUE(run_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_events_run_seq ON events(run_id, seq);
CREATE TABLE IF NOT EXISTS run_usage (
  run_id TEXT PRIMARY KEY,
  backend TEXT NOT NULL,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  total_tokens INTEGER NOT NULL DEFAULT 0,
  recorded_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_run_usage_backend_recorded_at ON run_usage(backend, recorded_at);
CREATE TABLE IF NOT EXISTS files (
  file_id TEXT PRIMARY KEY,
  storage_key TEXT NOT NULL UNIQUE,
  original_name TEXT NOT NULL,
  mime_type TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  sha256 TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_files_created_at ON files(created_at);
CREATE TABLE IF NOT EXISTS run_attachments (
  run_id TEXT NOT NULL,
  file_id TEXT NOT NULL,
  alias TEXT NOT NULL,
  materialized_path TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(run_id, alias)
);
CREATE INDEX IF NOT EXISTS idx_run_attachments_run_id ON run_attachments(run_id);`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	if err := s.ensureEventColumn(ctx, "channel", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureEventColumn(ctx, "schema_version", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureEventColumn(ctx, "format", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureEventColumn(ctx, "role", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureEventColumn(ctx, "compat_json", "TEXT"); err != nil {
		return err
	}
	if err := s.initAuthSchema(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) CreateRun(ctx context.Context, r RunRecord) error {
	ctxJSON, _ := json.Marshal(persistedContext{
		Context: r.Context,
		Options: r.Options,
	})
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO runs(run_id, workspace_id, workspace_path, backend, prompt, context_json, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.WorkspaceID, r.Workspace, r.Backend, r.Prompt, string(ctxJSON), r.Status, r.CreatedAt.UTC().Format(time.RFC3339Nano), r.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) UpdateRunStatus(ctx context.Context, runID, status, errText string) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE runs SET status=?, error_text=?, updated_at=? WHERE run_id=?`,
		status, errText, time.Now().UTC().Format(time.RFC3339Nano), runID,
	)
	return err
}

func (s *Store) GetRun(ctx context.Context, runID string) (RunRecord, error) {
	var out RunRecord
	var tsCreated, tsUpdated string
	var ctxJSON string

	row := s.db.QueryRowContext(
		ctx,
		`SELECT run_id, workspace_id, workspace_path, backend, prompt, context_json, status, error_text, created_at, updated_at
		 FROM runs WHERE run_id=?`,
		runID,
	)
	if err := row.Scan(
		&out.ID, &out.WorkspaceID, &out.Workspace, &out.Backend, &out.Prompt, &ctxJSON, &out.Status, &out.Error, &tsCreated, &tsUpdated,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RunRecord{}, fmt.Errorf("run not found")
		}
		return RunRecord{}, err
	}
	if ctxJSON != "" {
		var persisted persistedContext
		if err := json.Unmarshal([]byte(ctxJSON), &persisted); err == nil && (persisted.Context != nil || persisted.Options != (RunOptionsRecord{})) {
			out.Context = persisted.Context
			out.Options = persisted.Options
		} else {
			// backward compatible path for older rows storing context only
			_ = json.Unmarshal([]byte(ctxJSON), &out.Context)
		}
	}
	out.CreatedAt, _ = time.Parse(time.RFC3339Nano, tsCreated)
	out.UpdatedAt, _ = time.Parse(time.RFC3339Nano, tsUpdated)
	return out, nil
}

func (s *Store) AppendEvent(ctx context.Context, ev events.Event) error {
	events.NormalizeEvent(&ev)
	compatJSON, _ := json.Marshal(ev.Compat)
	payloadJSON, _ := json.Marshal(ev.Payload)
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO events(run_id, seq, ts, schema_version, type, channel, format, role, compat_json, payload_json, backend, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.RunID, ev.Seq, ev.TS.UTC().Format(time.RFC3339Nano), ev.SchemaVersion, ev.Type, ev.Channel, ev.Format, ev.Role, string(compatJSON), string(payloadJSON), ev.Backend, ev.Source,
	)
	return err
}

func (s *Store) ListEvents(ctx context.Context, runID string, fromSeq, limit int64) ([]events.Event, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT run_id, seq, ts, schema_version, type, channel, format, role, compat_json, payload_json, backend, source
		 FROM events WHERE run_id=? AND seq>=?
		 ORDER BY seq ASC LIMIT ?`,
		runID, fromSeq, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []events.Event{}
	for rows.Next() {
		var ev events.Event
		var ts string
		var compatJSON string
		var payloadJSON string
		if err := rows.Scan(&ev.RunID, &ev.Seq, &ts, &ev.SchemaVersion, &ev.Type, &ev.Channel, &ev.Format, &ev.Role, &compatJSON, &payloadJSON, &ev.Backend, &ev.Source); err != nil {
			return nil, err
		}
		ev.TS, _ = time.Parse(time.RFC3339Nano, ts)
		if compatJSON != "" && compatJSON != "null" {
			var compat events.CompatFields
			if err := json.Unmarshal([]byte(compatJSON), &compat); err == nil {
				ev.Compat = &compat
			}
		}
		_ = json.Unmarshal([]byte(payloadJSON), &ev.Payload)
		events.NormalizeEvent(&ev)
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *Store) ensureEventColumn(ctx context.Context, name, typ string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(events)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	has := false
	for rows.Next() {
		var cid int
		var colName string
		var colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if colName == name {
			has = true
		}
	}
	if has {
		return nil
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE events ADD COLUMN %s %s NOT NULL DEFAULT ''`, name, typ))
	return err
}

func (s *Store) NextSeq(ctx context.Context, runID string) (int64, error) {
	var maxSeq sql.NullInt64
	row := s.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM events WHERE run_id=?`, runID)
	if err := row.Scan(&maxSeq); err != nil {
		return 0, err
	}
	if !maxSeq.Valid {
		return 1, nil
	}
	return maxSeq.Int64 + 1, nil
}

func (s *Store) UpsertTokenUsage(ctx context.Context, rec TokenUsageRecord) error {
	if rec.RunID == "" {
		return fmt.Errorf("run id is required")
	}
	if rec.RecordedAt.IsZero() {
		rec.RecordedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO run_usage(run_id, backend, input_tokens, output_tokens, total_tokens, recorded_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(run_id) DO UPDATE SET
		   backend=excluded.backend,
		   input_tokens=excluded.input_tokens,
		   output_tokens=excluded.output_tokens,
		   total_tokens=excluded.total_tokens,
		   recorded_at=excluded.recorded_at`,
		rec.RunID,
		rec.Backend,
		rec.InputTokens,
		rec.OutputTokens,
		rec.TotalTokens,
		rec.RecordedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) AggregateTokenUsage(ctx context.Context, from, to time.Time, backend string) ([]TokenUsageAggregate, error) {
	base := `SELECT backend, COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COALESCE(SUM(total_tokens), 0)
	         FROM run_usage
	         WHERE recorded_at >= ? AND recorded_at < ?`
	args := []any{from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano)}
	if strings.TrimSpace(backend) != "" {
		base += ` AND backend = ?`
		args = append(args, strings.TrimSpace(backend))
	}
	base += ` GROUP BY backend ORDER BY backend ASC`

	rows, err := s.db.QueryContext(ctx, base, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TokenUsageAggregate, 0, 8)
	for rows.Next() {
		var agg TokenUsageAggregate
		if err := rows.Scan(&agg.Backend, &agg.RunCount, &agg.InputTokens, &agg.OutputTokens, &agg.TotalTokens); err != nil {
			return nil, err
		}
		out = append(out, agg)
	}
	return out, rows.Err()
}
