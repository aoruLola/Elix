package ledger

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestInitMigratesLegacyEventsTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer raw.Close()

	legacy := `
CREATE TABLE runs (
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
CREATE TABLE events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  ts TEXT NOT NULL,
  type TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  backend TEXT NOT NULL,
  source TEXT NOT NULL,
  UNIQUE(run_id, seq)
);`
	if _, err := raw.Exec(legacy); err != nil {
		t.Fatalf("prepare legacy schema: %v", err)
	}

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("init/migrate: %v", err)
	}

	rows, err := store.db.Query(`PRAGMA table_info(events)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()

	has := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		has[name] = true
	}
	for _, col := range []string{"channel", "format", "role", "schema_version", "compat_json"} {
		if !has[col] {
			t.Fatalf("expected migrated column %s", col)
		}
	}
}
