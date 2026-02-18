package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrPairCodeInvalid = errors.New("pair code invalid or expired")
	ErrSessionInvalid  = errors.New("session invalid or expired")
	ErrDeviceNotFound  = errors.New("device not found")
	ErrDeviceRevoked   = errors.New("device revoked")
)

type PairCodeRecord struct {
	Code        string
	Challenge   string
	Permissions []string
	CreatedBy   string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	Used        bool
	UsedAt      time.Time
}

type DeviceRecord struct {
	Address      string
	PublicKey    string
	Name         string
	Permissions  []string
	CreatedAt    time.Time
	LastSeenAt   time.Time
	Revoked      bool
	RevokedAt    time.Time
	RevokeReason string
}

type SessionRecord struct {
	SessionID        string
	AccessHash       string
	RefreshHash      string
	Address          string
	Scopes           []string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
	Revoked          bool
	RevokedAt        time.Time
}

func (s *Store) CreatePairCode(ctx context.Context, rec PairCodeRecord) error {
	permJSON, _ := json.Marshal(rec.Permissions)
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO pair_codes(code, challenge, permissions_json, created_by, created_at, expires_at, used, used_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.Code,
		rec.Challenge,
		string(permJSON),
		rec.CreatedBy,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		rec.ExpiresAt.UTC().Format(time.RFC3339Nano),
		boolToInt(rec.Used),
		formatTime(rec.UsedAt),
	)
	return err
}

func (s *Store) ConsumePairCode(ctx context.Context, code string, now time.Time) (PairCodeRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PairCodeRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()

	rec, err := readPairCodeTx(ctx, tx, code)
	if err != nil {
		return PairCodeRecord{}, err
	}
	if rec.Used || now.After(rec.ExpiresAt) {
		return PairCodeRecord{}, ErrPairCodeInvalid
	}

	res, err := tx.ExecContext(
		ctx,
		`UPDATE pair_codes SET used=1, used_at=? WHERE code=? AND used=0`,
		now.UTC().Format(time.RFC3339Nano), code,
	)
	if err != nil {
		return PairCodeRecord{}, err
	}
	affected, _ := res.RowsAffected()
	if affected != 1 {
		return PairCodeRecord{}, ErrPairCodeInvalid
	}

	if err := tx.Commit(); err != nil {
		return PairCodeRecord{}, err
	}
	return rec, nil
}

func (s *Store) UpsertDevice(ctx context.Context, rec DeviceRecord) (DeviceRecord, error) {
	now := rec.LastSeenAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	if rec.Permissions == nil {
		rec.Permissions = []string{}
	}
	permJSON, _ := json.Marshal(rec.Permissions)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeviceRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := readDeviceTx(ctx, tx, rec.Address)
	if err != nil && !errors.Is(err, ErrDeviceNotFound) {
		return DeviceRecord{}, err
	}
	if errors.Is(err, ErrDeviceNotFound) {
		_, err := tx.ExecContext(
			ctx,
			`INSERT INTO devices(address, public_key, name, permissions_json, created_at, last_seen_at, revoked, revoked_at, revoke_reason)
			 VALUES (?, ?, ?, ?, ?, ?, 0, '', '')`,
			rec.Address,
			rec.PublicKey,
			rec.Name,
			string(permJSON),
			rec.CreatedAt.UTC().Format(time.RFC3339Nano),
			now.UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			return DeviceRecord{}, err
		}
	} else {
		if existing.Revoked {
			return DeviceRecord{}, ErrDeviceRevoked
		}
		if existing.PublicKey != rec.PublicKey {
			return DeviceRecord{}, fmt.Errorf("public key mismatch for address %s", rec.Address)
		}
		name := existing.Name
		if rec.Name != "" {
			name = rec.Name
		}
		perms := existing.Permissions
		if len(rec.Permissions) > 0 {
			perms = rec.Permissions
		}
		permsJSON, _ := json.Marshal(perms)
		_, err := tx.ExecContext(
			ctx,
			`UPDATE devices SET name=?, permissions_json=?, last_seen_at=? WHERE address=?`,
			name,
			string(permsJSON),
			now.UTC().Format(time.RFC3339Nano),
			rec.Address,
		)
		if err != nil {
			return DeviceRecord{}, err
		}
	}

	out, err := readDeviceTx(ctx, tx, rec.Address)
	if err != nil {
		return DeviceRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return DeviceRecord{}, err
	}
	return out, nil
}

func (s *Store) TouchDevice(ctx context.Context, address string, ts time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE devices SET last_seen_at=? WHERE address=?`,
		ts.UTC().Format(time.RFC3339Nano),
		address,
	)
	return err
}

func (s *Store) ListDevices(ctx context.Context) ([]DeviceRecord, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT address, public_key, name, permissions_json, created_at, last_seen_at, revoked, revoked_at, revoke_reason
		 FROM devices ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []DeviceRecord{}
	for rows.Next() {
		rec, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) GetDevice(ctx context.Context, address string) (DeviceRecord, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT address, public_key, name, permissions_json, created_at, last_seen_at, revoked, revoked_at, revoke_reason
		 FROM devices WHERE address=?`,
		address,
	)
	rec, err := scanDeviceRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DeviceRecord{}, ErrDeviceNotFound
		}
		return DeviceRecord{}, err
	}
	return rec, nil
}

func (s *Store) RenameDevice(ctx context.Context, address, name string) error {
	res, err := s.db.ExecContext(
		ctx,
		`UPDATE devices SET name=? WHERE address=? AND revoked=0`,
		name,
		address,
	)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

func (s *Store) RevokeDevice(ctx context.Context, address, reason string, now time.Time) error {
	res, err := s.db.ExecContext(
		ctx,
		`UPDATE devices SET revoked=1, revoked_at=?, revoke_reason=? WHERE address=? AND revoked=0`,
		now.UTC().Format(time.RFC3339Nano),
		reason,
		address,
	)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, rec SessionRecord) error {
	scopeJSON, _ := json.Marshal(rec.Scopes)
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO sessions(session_id, access_hash, refresh_hash, address, scopes_json, created_at, expires_at, refresh_expires_at, revoked, revoked_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.SessionID,
		rec.AccessHash,
		rec.RefreshHash,
		rec.Address,
		string(scopeJSON),
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		rec.ExpiresAt.UTC().Format(time.RFC3339Nano),
		rec.RefreshExpiresAt.UTC().Format(time.RFC3339Nano),
		boolToInt(rec.Revoked),
		formatTime(rec.RevokedAt),
	)
	return err
}

func (s *Store) GetSessionByAccessHash(ctx context.Context, accessHash string, now time.Time) (SessionRecord, DeviceRecord, error) {
	sess, dev, err := s.readSessionAndDevice(ctx, `access_hash=?`, accessHash)
	if err != nil {
		return SessionRecord{}, DeviceRecord{}, err
	}
	if sess.Revoked || now.After(sess.ExpiresAt) || now.After(sess.RefreshExpiresAt) || dev.Revoked {
		return SessionRecord{}, DeviceRecord{}, ErrSessionInvalid
	}
	return sess, dev, nil
}

func (s *Store) GetSessionByRefreshHash(ctx context.Context, refreshHash string, now time.Time) (SessionRecord, DeviceRecord, error) {
	sess, dev, err := s.readSessionAndDevice(ctx, `refresh_hash=?`, refreshHash)
	if err != nil {
		return SessionRecord{}, DeviceRecord{}, err
	}
	if sess.Revoked || now.After(sess.RefreshExpiresAt) || dev.Revoked {
		return SessionRecord{}, DeviceRecord{}, ErrSessionInvalid
	}
	return sess, dev, nil
}

func (s *Store) RotateSession(ctx context.Context, sessionID, accessHash, refreshHash string, expiresAt, refreshExpiresAt time.Time) error {
	res, err := s.db.ExecContext(
		ctx,
		`UPDATE sessions
		   SET access_hash=?, refresh_hash=?, expires_at=?, refresh_expires_at=?, revoked=0, revoked_at=''
		 WHERE session_id=?`,
		accessHash,
		refreshHash,
		expiresAt.UTC().Format(time.RFC3339Nano),
		refreshExpiresAt.UTC().Format(time.RFC3339Nano),
		sessionID,
	)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrSessionInvalid
	}
	return nil
}

func (s *Store) RevokeSessionsByAddress(ctx context.Context, address string, now time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE sessions SET revoked=1, revoked_at=? WHERE address=? AND revoked=0`,
		now.UTC().Format(time.RFC3339Nano),
		address,
	)
	return err
}

func (s *Store) readSessionAndDevice(ctx context.Context, whereClause string, value any) (SessionRecord, DeviceRecord, error) {
	row := s.db.QueryRowContext(
		ctx,
		fmt.Sprintf(
			`SELECT s.session_id, s.access_hash, s.refresh_hash, s.address, s.scopes_json, s.created_at, s.expires_at, s.refresh_expires_at, s.revoked, s.revoked_at,
			        d.address, d.public_key, d.name, d.permissions_json, d.created_at, d.last_seen_at, d.revoked, d.revoked_at, d.revoke_reason
			   FROM sessions s
			   JOIN devices d ON d.address = s.address
			  WHERE %s`,
			whereClause,
		),
		value,
	)

	var sess SessionRecord
	var scopesJSON string
	var sessCreated, sessExpires, sessRefreshExpires, sessRevokedAt string

	var dev DeviceRecord
	var permsJSON string
	var devCreated, devLastSeen, devRevokedAt string

	var sessRevokedInt int
	var devRevokedInt int
	if err := row.Scan(
		&sess.SessionID, &sess.AccessHash, &sess.RefreshHash, &sess.Address, &scopesJSON, &sessCreated, &sessExpires, &sessRefreshExpires, &sessRevokedInt, &sessRevokedAt,
		&dev.Address, &dev.PublicKey, &dev.Name, &permsJSON, &devCreated, &devLastSeen, &devRevokedInt, &devRevokedAt, &dev.RevokeReason,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionRecord{}, DeviceRecord{}, ErrSessionInvalid
		}
		return SessionRecord{}, DeviceRecord{}, err
	}
	sess.Scopes = decodeStringArray(scopesJSON)
	sess.CreatedAt, _ = time.Parse(time.RFC3339Nano, sessCreated)
	sess.ExpiresAt, _ = time.Parse(time.RFC3339Nano, sessExpires)
	sess.RefreshExpiresAt, _ = time.Parse(time.RFC3339Nano, sessRefreshExpires)
	sess.Revoked = sessRevokedInt == 1
	sess.RevokedAt = parseTime(sessRevokedAt)

	dev.Permissions = decodeStringArray(permsJSON)
	dev.CreatedAt, _ = time.Parse(time.RFC3339Nano, devCreated)
	dev.LastSeenAt = parseTime(devLastSeen)
	dev.Revoked = devRevokedInt == 1
	dev.RevokedAt = parseTime(devRevokedAt)

	return sess, dev, nil
}

func readPairCodeTx(ctx context.Context, tx *sql.Tx, code string) (PairCodeRecord, error) {
	row := tx.QueryRowContext(
		ctx,
		`SELECT code, challenge, permissions_json, created_by, created_at, expires_at, used, used_at
		 FROM pair_codes WHERE code=?`,
		code,
	)
	var rec PairCodeRecord
	var permsJSON string
	var createdAt, expiresAt, usedAt string
	var usedInt int
	if err := row.Scan(&rec.Code, &rec.Challenge, &permsJSON, &rec.CreatedBy, &createdAt, &expiresAt, &usedInt, &usedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PairCodeRecord{}, ErrPairCodeInvalid
		}
		return PairCodeRecord{}, err
	}
	rec.Permissions = decodeStringArray(permsJSON)
	rec.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	rec.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAt)
	rec.Used = usedInt == 1
	rec.UsedAt = parseTime(usedAt)
	return rec, nil
}

func readDeviceTx(ctx context.Context, tx *sql.Tx, address string) (DeviceRecord, error) {
	row := tx.QueryRowContext(
		ctx,
		`SELECT address, public_key, name, permissions_json, created_at, last_seen_at, revoked, revoked_at, revoke_reason
		 FROM devices WHERE address=?`,
		address,
	)
	rec, err := scanDeviceRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DeviceRecord{}, ErrDeviceNotFound
		}
		return DeviceRecord{}, err
	}
	return rec, nil
}

func scanDevice(rows *sql.Rows) (DeviceRecord, error) {
	var rec DeviceRecord
	var permsJSON string
	var createdAt, lastSeenAt, revokedAt string
	var revokedInt int
	if err := rows.Scan(
		&rec.Address, &rec.PublicKey, &rec.Name, &permsJSON, &createdAt, &lastSeenAt, &revokedInt, &revokedAt, &rec.RevokeReason,
	); err != nil {
		return DeviceRecord{}, err
	}
	rec.Permissions = decodeStringArray(permsJSON)
	rec.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	rec.LastSeenAt = parseTime(lastSeenAt)
	rec.Revoked = revokedInt == 1
	rec.RevokedAt = parseTime(revokedAt)
	return rec, nil
}

func scanDeviceRow(row *sql.Row) (DeviceRecord, error) {
	var rec DeviceRecord
	var permsJSON string
	var createdAt, lastSeenAt, revokedAt string
	var revokedInt int
	if err := row.Scan(
		&rec.Address, &rec.PublicKey, &rec.Name, &permsJSON, &createdAt, &lastSeenAt, &revokedInt, &revokedAt, &rec.RevokeReason,
	); err != nil {
		return DeviceRecord{}, err
	}
	rec.Permissions = decodeStringArray(permsJSON)
	rec.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	rec.LastSeenAt = parseTime(lastSeenAt)
	rec.Revoked = revokedInt == 1
	rec.RevokedAt = parseTime(revokedAt)
	return rec, nil
}

func decodeStringArray(v string) []string {
	if v == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(v), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

func parseTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}
	}
	return t
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
