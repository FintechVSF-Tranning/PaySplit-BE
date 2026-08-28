package realtimejwt

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"paysplit-backend/internal/config"
)

// RealtimeClaims chứa các claims cần thiết cho Supabase Realtime Broadcast authorization (Spec 0010 AC-5)
type RealtimeClaims struct {
	Role      string `json:"role"`
	SessionID string `json:"sid"`
	jwtv5.RegisteredClaims
}

// Manager tạo và ký short lived JWT ES256 cho Supabase Realtime
type Manager struct {
	privateKey *ecdsa.PrivateKey
	kid        string
	issuer     string
	audience   string
	ttl        time.Duration
}

// NewManager khởi tạo Realtime JWT Manager
func NewManager(cfg config.RealtimeConfig) (*Manager, error) {
	ttl := cfg.TokenTTL
	if ttl <= 0 {
		ttl = 300 * time.Second
	}
	issuer := cfg.JWTIssuer
	if issuer == "" {
		issuer = "supabase"
	}
	audience := cfg.JWTAudience
	if audience == "" {
		audience = "authenticated"
	}

	var privKey *ecdsa.PrivateKey
	pemStr := strings.TrimSpace(cfg.JWTPrivateKey)

	if pemStr != "" {
		key, err := jwtv5.ParseECPrivateKeyFromPEM([]byte(pemStr))
		if err != nil {
			return nil, fmt.Errorf("parse SUPABASE_REALTIME_JWT_PRIVATE_KEY: %w", err)
		}
		privKey = key
	} else {
		// Trong môi trường development / test khi chưa cấu hình key thật, sinh ephemeral ECDSA key
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate ephemeral EC key: %w", err)
		}
		privKey = key
	}

	kid := strings.TrimSpace(cfg.JWTKID)
	if kid == "" {
		kid = "dev-key-1"
	}

	return &Manager{
		privateKey: privKey,
		kid:        kid,
		issuer:     issuer,
		audience:   audience,
		ttl:        ttl,
	}, nil
}

// Sign phát hành Realtime JWT có thời hạn 5 phút mang role authenticated và kid header
func (m *Manager) Sign(userID uuid.UUID, sessionID uuid.UUID) (string, time.Time, error) {
	if userID == uuid.Nil {
		return "", time.Time{}, errors.New("user ID must not be nil")
	}
	if sessionID == uuid.Nil {
		return "", time.Time{}, errors.New("session ID must not be nil")
	}

	now := time.Now().UTC()
	expiresAt := now.Add(m.ttl)

	claims := RealtimeClaims{
		Role:      "authenticated",
		SessionID: sessionID.String(),
		RegisteredClaims: jwtv5.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   userID.String(),
			Audience:  jwtv5.ClaimStrings{m.audience},
			IssuedAt:  jwtv5.NewNumericDate(now),
			ExpiresAt: jwtv5.NewNumericDate(expiresAt),
		},
	}

	token := jwtv5.NewWithClaims(jwtv5.SigningMethodES256, claims)
	token.Header["kid"] = m.kid

	signed, err := token.SignedString(m.privateKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign realtime token ES256: %w", err)
	}

	return signed, expiresAt, nil
}

// PublicKey trả về public key ECDSA để phục vụ verify trong test
func (m *Manager) PublicKey() *ecdsa.PublicKey {
	return &m.privateKey.PublicKey
}

// KID trả về Key ID hiện tại
func (m *Manager) KID() string {
	return m.kid
}
