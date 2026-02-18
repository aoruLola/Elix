package ledger

import (
	"context"
)

func (s *Store) initAuthSchema(ctx context.Context) error {
	schema := `
CREATE TABLE IF NOT EXISTS pair_codes (
  code TEXT PRIMARY KEY,
  challenge TEXT NOT NULL,
  permissions_json TEXT NOT NULL DEFAULT '[]',
  created_by TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used INTEGER NOT NULL DEFAULT 0,
  used_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_pair_codes_expires ON pair_codes(expires_at);

CREATE TABLE IF NOT EXISTS devices (
  address TEXT PRIMARY KEY,
  public_key TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  permissions_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  revoked INTEGER NOT NULL DEFAULT 0,
  revoked_at TEXT NOT NULL DEFAULT '',
  revoke_reason TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_devices_public_key ON devices(public_key);

CREATE TABLE IF NOT EXISTS sessions (
  session_id TEXT PRIMARY KEY,
  access_hash TEXT NOT NULL UNIQUE,
  refresh_hash TEXT NOT NULL UNIQUE,
  address TEXT NOT NULL,
  scopes_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  refresh_expires_at TEXT NOT NULL,
  revoked INTEGER NOT NULL DEFAULT 0,
  revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sessions_access_hash ON sessions(access_hash);
CREATE INDEX IF NOT EXISTS idx_sessions_refresh_hash ON sessions(refresh_hash);
CREATE INDEX IF NOT EXISTS idx_sessions_address ON sessions(address);`

	_, err := s.db.ExecContext(ctx, schema)
	return err
}
