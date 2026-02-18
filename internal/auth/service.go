package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"echohelix/internal/ledger"
	"echohelix/internal/wallet"

	"github.com/google/uuid"
)

type Config struct {
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	PairCodeTTL     time.Duration
}

type Service struct {
	store *ledger.Store
	cfg   Config
}

type Principal struct {
	AuthType  string
	Admin     bool
	Address   string
	SessionID string
	Scopes    []string
}

type PairStartResult struct {
	PairCode    string    `json:"pair_code"`
	Challenge   string    `json:"challenge"`
	Permissions []string  `json:"permissions"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type CompletePairRequest struct {
	PairCode   string `json:"pair_code"`
	PublicKey  string `json:"public_key"`
	Signature  string `json:"signature"`
	DeviceName string `json:"device_name"`
}

type CompletePairResult struct {
	Address          string    `json:"address"`
	PublicKey        string    `json:"public_key"`
	DeviceName       string    `json:"device_name"`
	Scopes           []string  `json:"scopes"`
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	ExpiresAt        time.Time `json:"expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

type RefreshResult struct {
	Address          string    `json:"address"`
	Scopes           []string  `json:"scopes"`
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	ExpiresAt        time.Time `json:"expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

type DeviceView struct {
	Address      string    `json:"address"`
	Name         string    `json:"name"`
	PublicKey    string    `json:"public_key"`
	Permissions  []string  `json:"permissions"`
	CreatedAt    time.Time `json:"created_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	Revoked      bool      `json:"revoked"`
	RevokedAt    time.Time `json:"revoked_at,omitempty"`
	RevokeReason string    `json:"revoke_reason,omitempty"`
}

func New(store *ledger.Store, cfg Config) *Service {
	if cfg.AccessTokenTTL <= 0 {
		cfg.AccessTokenTTL = 15 * time.Minute
	}
	if cfg.RefreshTokenTTL <= 0 {
		cfg.RefreshTokenTTL = 24 * time.Hour
	}
	if cfg.PairCodeTTL <= 0 {
		cfg.PairCodeTTL = 60 * time.Second
	}
	return &Service{
		store: store,
		cfg:   cfg,
	}
}

func AdminPrincipal() Principal {
	return Principal{
		AuthType: "static",
		Admin:    true,
	}
}

func StaticBootstrapPrincipal() Principal {
	return Principal{
		AuthType: "static",
		Admin:    false,
		Scopes:   append([]string{}, staticBootstrapScopes()...),
	}
}

func (p Principal) HasScope(scope string) bool {
	if p.Admin {
		return true
	}
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

func (s *Service) StartPair(ctx context.Context, createdBy string, requestedScopes []string, ttl time.Duration) (PairStartResult, error) {
	if ttl <= 0 || ttl > 10*time.Minute {
		ttl = s.cfg.PairCodeTTL
	}
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)
	code, err := generatePairCode()
	if err != nil {
		return PairStartResult{}, err
	}
	challenge, err := randomToken(32)
	if err != nil {
		return PairStartResult{}, err
	}
	scopes := normalizeScopes(requestedScopes)
	if err := s.store.CreatePairCode(ctx, ledger.PairCodeRecord{
		Code:        code,
		Challenge:   challenge,
		Permissions: scopes,
		CreatedBy:   createdBy,
		CreatedAt:   now,
		ExpiresAt:   expiresAt,
	}); err != nil {
		return PairStartResult{}, err
	}
	return PairStartResult{
		PairCode:    code,
		Challenge:   challenge,
		Permissions: scopes,
		ExpiresAt:   expiresAt,
	}, nil
}

func (s *Service) CompletePair(ctx context.Context, req CompletePairRequest) (CompletePairResult, error) {
	if strings.TrimSpace(req.PairCode) == "" || strings.TrimSpace(req.PublicKey) == "" || strings.TrimSpace(req.Signature) == "" {
		return CompletePairResult{}, errors.New("pair_code/public_key/signature are required")
	}
	now := time.Now().UTC()
	pairRec, err := s.store.ConsumePairCode(ctx, strings.TrimSpace(req.PairCode), now)
	if err != nil {
		return CompletePairResult{}, err
	}

	pubRaw, err := decodeBase64Flexible(req.PublicKey)
	if err != nil {
		return CompletePairResult{}, fmt.Errorf("decode public_key: %w", err)
	}
	if len(pubRaw) != ed25519.PublicKeySize {
		return CompletePairResult{}, fmt.Errorf("public_key size must be %d", ed25519.PublicKeySize)
	}
	sigRaw, err := decodeBase64Flexible(req.Signature)
	if err != nil {
		return CompletePairResult{}, fmt.Errorf("decode signature: %w", err)
	}
	if len(sigRaw) != ed25519.SignatureSize {
		return CompletePairResult{}, fmt.Errorf("signature size must be %d", ed25519.SignatureSize)
	}
	if !ed25519.Verify(ed25519.PublicKey(pubRaw), []byte(pairRec.Challenge), sigRaw) {
		return CompletePairResult{}, errors.New("signature verification failed")
	}

	address := wallet.AddressFromPublicKey(pubRaw)
	deviceName := strings.TrimSpace(req.DeviceName)
	if deviceName == "" {
		deviceName = "device-" + address[len(address)-8:]
	}
	device, err := s.store.UpsertDevice(ctx, ledger.DeviceRecord{
		Address:     address,
		PublicKey:   base64.RawURLEncoding.EncodeToString(pubRaw),
		Name:        deviceName,
		Permissions: normalizeScopes(pairRec.Permissions),
		CreatedAt:   now,
		LastSeenAt:  now,
	})
	if err != nil {
		return CompletePairResult{}, err
	}

	tokens, err := s.issueSession(ctx, device.Address, device.Permissions)
	if err != nil {
		return CompletePairResult{}, err
	}
	return CompletePairResult{
		Address:          device.Address,
		PublicKey:        device.PublicKey,
		DeviceName:       device.Name,
		Scopes:           append([]string{}, device.Permissions...),
		AccessToken:      tokens.AccessToken,
		RefreshToken:     tokens.RefreshToken,
		ExpiresAt:        tokens.ExpiresAt,
		RefreshExpiresAt: tokens.RefreshExpiresAt,
	}, nil
}

func (s *Service) AuthenticateToken(ctx context.Context, accessToken string) (Principal, error) {
	if strings.TrimSpace(accessToken) == "" {
		return Principal{}, errors.New("empty access token")
	}
	now := time.Now().UTC()
	sess, dev, err := s.store.GetSessionByAccessHash(ctx, hashToken(accessToken), now)
	if err != nil {
		return Principal{}, err
	}
	_ = s.store.TouchDevice(ctx, dev.Address, now)
	return Principal{
		AuthType:  "session",
		Address:   dev.Address,
		SessionID: sess.SessionID,
		Scopes:    append([]string{}, sess.Scopes...),
	}, nil
}

func (s *Service) RefreshSession(ctx context.Context, refreshToken string) (RefreshResult, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return RefreshResult{}, errors.New("refresh_token is required")
	}
	now := time.Now().UTC()
	sess, dev, err := s.store.GetSessionByRefreshHash(ctx, hashToken(refreshToken), now)
	if err != nil {
		return RefreshResult{}, err
	}
	accessToken, err := randomToken(48)
	if err != nil {
		return RefreshResult{}, err
	}
	newRefreshToken, err := randomToken(56)
	if err != nil {
		return RefreshResult{}, err
	}
	expiresAt := now.Add(s.cfg.AccessTokenTTL)
	refreshExpiresAt := now.Add(s.cfg.RefreshTokenTTL)
	if err := s.store.RotateSession(ctx, sess.SessionID, hashToken(accessToken), hashToken(newRefreshToken), expiresAt, refreshExpiresAt); err != nil {
		return RefreshResult{}, err
	}
	return RefreshResult{
		Address:          dev.Address,
		Scopes:           append([]string{}, sess.Scopes...),
		AccessToken:      accessToken,
		RefreshToken:     newRefreshToken,
		ExpiresAt:        expiresAt,
		RefreshExpiresAt: refreshExpiresAt,
	}, nil
}

func (s *Service) ListDevices(ctx context.Context) ([]DeviceView, error) {
	recs, err := s.store.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DeviceView, 0, len(recs))
	for _, rec := range recs {
		out = append(out, DeviceView{
			Address:      rec.Address,
			Name:         rec.Name,
			PublicKey:    rec.PublicKey,
			Permissions:  append([]string{}, rec.Permissions...),
			CreatedAt:    rec.CreatedAt,
			LastSeenAt:   rec.LastSeenAt,
			Revoked:      rec.Revoked,
			RevokedAt:    rec.RevokedAt,
			RevokeReason: rec.RevokeReason,
		})
	}
	return out, nil
}

func (s *Service) RenameDevice(ctx context.Context, address, name string) error {
	name = strings.TrimSpace(name)
	if address == "" || name == "" {
		return errors.New("address and name are required")
	}
	return s.store.RenameDevice(ctx, address, name)
}

func (s *Service) RevokeDevice(ctx context.Context, address, reason string) error {
	if strings.TrimSpace(address) == "" {
		return errors.New("address is required")
	}
	now := time.Now().UTC()
	if err := s.store.RevokeDevice(ctx, address, strings.TrimSpace(reason), now); err != nil {
		return err
	}
	return s.store.RevokeSessionsByAddress(ctx, address, now)
}

type issuedSession struct {
	AccessToken      string
	RefreshToken     string
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
}

func (s *Service) issueSession(ctx context.Context, address string, scopes []string) (issuedSession, error) {
	now := time.Now().UTC()
	accessToken, err := randomToken(48)
	if err != nil {
		return issuedSession{}, err
	}
	refreshToken, err := randomToken(56)
	if err != nil {
		return issuedSession{}, err
	}
	expiresAt := now.Add(s.cfg.AccessTokenTTL)
	refreshExpiresAt := now.Add(s.cfg.RefreshTokenTTL)

	err = s.store.CreateSession(ctx, ledger.SessionRecord{
		SessionID:        uuid.NewString(),
		AccessHash:       hashToken(accessToken),
		RefreshHash:      hashToken(refreshToken),
		Address:          address,
		Scopes:           normalizeScopes(scopes),
		CreatedAt:        now,
		ExpiresAt:        expiresAt,
		RefreshExpiresAt: refreshExpiresAt,
	})
	if err != nil {
		return issuedSession{}, err
	}
	return issuedSession{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		ExpiresAt:        expiresAt,
		RefreshExpiresAt: refreshExpiresAt,
	}, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func generatePairCode() (string, error) {
	raw, err := randomToken(6)
	if err != nil {
		return "", err
	}
	normalized := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(raw, "-", "A"), "_", "B"))
	if len(normalized) < 8 {
		normalized += "ABCDEFGH"
	}
	normalized = normalized[:8]
	return normalized[:4] + "-" + normalized[4:], nil
}

func decodeBase64Flexible(v string) ([]byte, error) {
	decoders := []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	}
	for _, enc := range decoders {
		if b, err := enc.DecodeString(v); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("invalid base64 payload")
}
