package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"echohelix/internal/ledger"
)

func newAuthService(t *testing.T) *Service {
	t.Helper()
	store, err := ledger.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	return New(store, Config{
		AccessTokenTTL:  2 * time.Minute,
		RefreshTokenTTL: 10 * time.Minute,
		PairCodeTTL:     2 * time.Minute,
	})
}

func TestPairCompleteAuthenticateRefresh(t *testing.T) {
	svc := newAuthService(t)
	start, err := svc.StartPair(context.Background(), "admin", []string{ScopeRunsSubmit, ScopeRunsRead}, 0)
	if err != nil {
		t.Fatalf("start pair: %v", err)
	}
	if start.PairCode == "" || start.Challenge == "" {
		t.Fatalf("unexpected empty start result: %#v", start)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sig := ed25519.Sign(priv, []byte(start.Challenge))
	complete, err := svc.CompletePair(context.Background(), CompletePairRequest{
		PairCode:   start.PairCode,
		PublicKey:  base64.RawURLEncoding.EncodeToString(pub),
		Signature:  base64.RawURLEncoding.EncodeToString(sig),
		DeviceName: "macbook",
	})
	if err != nil {
		t.Fatalf("complete pair: %v", err)
	}
	if complete.Address == "" || complete.AccessToken == "" || complete.RefreshToken == "" {
		t.Fatalf("unexpected complete result: %#v", complete)
	}
	if len(complete.Scopes) == 0 {
		t.Fatalf("expected scopes assigned")
	}

	principal, err := svc.AuthenticateToken(context.Background(), complete.AccessToken)
	if err != nil {
		t.Fatalf("authenticate access token: %v", err)
	}
	if principal.Address != complete.Address {
		t.Fatalf("principal address mismatch: got %s want %s", principal.Address, complete.Address)
	}
	if !principal.HasScope(ScopeRunsSubmit) {
		t.Fatalf("missing expected scope")
	}

	refreshed, err := svc.RefreshSession(context.Background(), complete.RefreshToken)
	if err != nil {
		t.Fatalf("refresh session: %v", err)
	}
	if refreshed.AccessToken == complete.AccessToken || refreshed.RefreshToken == complete.RefreshToken {
		t.Fatalf("expected token rotation")
	}
	if _, err := svc.AuthenticateToken(context.Background(), complete.AccessToken); err == nil {
		t.Fatalf("expected old access token to be invalid after refresh")
	}
	if _, err := svc.AuthenticateToken(context.Background(), refreshed.AccessToken); err != nil {
		t.Fatalf("new access token should be valid: %v", err)
	}
}

func TestDeviceListRenameRevoke(t *testing.T) {
	svc := newAuthService(t)
	start, err := svc.StartPair(context.Background(), "admin", nil, 0)
	if err != nil {
		t.Fatalf("start pair: %v", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sig := ed25519.Sign(priv, []byte(start.Challenge))
	complete, err := svc.CompletePair(context.Background(), CompletePairRequest{
		PairCode:   start.PairCode,
		PublicKey:  base64.RawURLEncoding.EncodeToString(pub),
		Signature:  base64.RawURLEncoding.EncodeToString(sig),
		DeviceName: "desktop",
	})
	if err != nil {
		t.Fatalf("complete pair: %v", err)
	}

	devices, err := svc.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}

	if err := svc.RenameDevice(context.Background(), complete.Address, "desktop-renamed"); err != nil {
		t.Fatalf("rename device: %v", err)
	}
	devices, err = svc.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices after rename: %v", err)
	}
	if devices[0].Name != "desktop-renamed" {
		t.Fatalf("expected renamed device, got %s", devices[0].Name)
	}

	if err := svc.RevokeDevice(context.Background(), complete.Address, "security reset"); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	if _, err := svc.AuthenticateToken(context.Background(), complete.AccessToken); err == nil {
		t.Fatalf("expected access token invalid after revoke")
	}
}
