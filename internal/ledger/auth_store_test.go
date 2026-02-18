package ledger

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newAuthStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	return store
}

func TestPairCodeConsumeOnce(t *testing.T) {
	store := newAuthStore(t)
	now := time.Now().UTC()
	code := "AAAA-BBBB"
	if err := store.CreatePairCode(context.Background(), PairCodeRecord{
		Code:        code,
		Challenge:   "challenge",
		Permissions: []string{"runs:submit"},
		CreatedBy:   "admin",
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("create pair code: %v", err)
	}

	rec, err := store.ConsumePairCode(context.Background(), code, now)
	if err != nil {
		t.Fatalf("consume first: %v", err)
	}
	if rec.Challenge != "challenge" || len(rec.Permissions) != 1 {
		t.Fatalf("unexpected pair record: %#v", rec)
	}

	if _, err := store.ConsumePairCode(context.Background(), code, now); err == nil {
		t.Fatalf("expected second consume to fail")
	}
}

func TestSessionLookupAndRotate(t *testing.T) {
	store := newAuthStore(t)
	now := time.Now().UTC()
	_, err := store.UpsertDevice(context.Background(), DeviceRecord{
		Address:     "elix1abc",
		PublicKey:   "pub",
		Name:        "dev",
		Permissions: []string{"runs:submit"},
		CreatedAt:   now,
		LastSeenAt:  now,
	})
	if err != nil {
		t.Fatalf("upsert device: %v", err)
	}

	if err := store.CreateSession(context.Background(), SessionRecord{
		SessionID:        "s1",
		AccessHash:       "a1",
		RefreshHash:      "r1",
		Address:          "elix1abc",
		Scopes:           []string{"runs:submit"},
		CreatedAt:        now,
		ExpiresAt:        now.Add(time.Minute),
		RefreshExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	sess, dev, err := store.GetSessionByAccessHash(context.Background(), "a1", now)
	if err != nil {
		t.Fatalf("get session by access hash: %v", err)
	}
	if sess.SessionID != "s1" || dev.Address != "elix1abc" {
		t.Fatalf("unexpected session/device: %#v %#v", sess, dev)
	}

	if err := store.RotateSession(context.Background(), "s1", "a2", "r2", now.Add(2*time.Minute), now.Add(20*time.Minute)); err != nil {
		t.Fatalf("rotate session: %v", err)
	}
	if _, _, err := store.GetSessionByAccessHash(context.Background(), "a1", now); err == nil {
		t.Fatalf("expected old access hash invalid")
	}
	if _, _, err := store.GetSessionByRefreshHash(context.Background(), "r2", now); err != nil {
		t.Fatalf("expected new refresh hash valid: %v", err)
	}
}

func TestRevokeDeviceRevokesSessions(t *testing.T) {
	store := newAuthStore(t)
	now := time.Now().UTC()
	_, err := store.UpsertDevice(context.Background(), DeviceRecord{
		Address:     "elix1rev",
		PublicKey:   "pub",
		Name:        "dev",
		Permissions: []string{"runs:submit"},
		CreatedAt:   now,
		LastSeenAt:  now,
	})
	if err != nil {
		t.Fatalf("upsert device: %v", err)
	}
	if err := store.CreateSession(context.Background(), SessionRecord{
		SessionID:        "s1",
		AccessHash:       "a1",
		RefreshHash:      "r1",
		Address:          "elix1rev",
		Scopes:           []string{"runs:submit"},
		CreatedAt:        now,
		ExpiresAt:        now.Add(time.Minute),
		RefreshExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.RevokeDevice(context.Background(), "elix1rev", "manual", now); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	if err := store.RevokeSessionsByAddress(context.Background(), "elix1rev", now); err != nil {
		t.Fatalf("revoke sessions: %v", err)
	}
	if _, _, err := store.GetSessionByAccessHash(context.Background(), "a1", now); err == nil {
		t.Fatalf("expected revoked session invalid")
	}
}
