package realtimejwt

import (
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"paysplit-backend/internal/config"
)

func TestRealtimeJWT_SigningAndClaims(t *testing.T) {
	mgr, err := NewManager(config.RealtimeConfig{
		JWTIssuer:   "supabase",
		JWTAudience: "authenticated",
		JWTKID:      "test-kid-2026",
		TokenTTL:    300 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create realtime jwt manager: %v", err)
	}

	userID := uuid.New()
	sessionID := uuid.New()

	tokenStr, expiresAt, err := mgr.Sign(userID, sessionID)
	if err != nil {
		t.Fatalf("failed to sign realtime jwt: %v", err)
	}

	if time.Until(expiresAt) <= 0 || time.Until(expiresAt) > 305*time.Second {
		t.Fatalf("unexpected expiry: %v", expiresAt)
	}

	// Verify token with ECDSA public key
	parsedClaims := &RealtimeClaims{}
	token, err := jwtv5.ParseWithClaims(tokenStr, parsedClaims, func(tok *jwtv5.Token) (any, error) {
		if tok.Method.Alg() != jwtv5.SigningMethodES256.Alg() {
			t.Fatalf("expected alg ES256, got %s", tok.Method.Alg())
		}
		if tok.Header["kid"] != "test-kid-2026" {
			t.Fatalf("expected kid test-kid-2026, got %v", tok.Header["kid"])
		}
		return mgr.PublicKey(), nil
	})
	if err != nil {
		t.Fatalf("failed to parse/verify signed token: %v", err)
	}

	if !token.Valid {
		t.Fatal("token is invalid")
	}

	if parsedClaims.Subject != userID.String() {
		t.Fatalf("subject mismatch: expected %s, got %s", userID.String(), parsedClaims.Subject)
	}
	if parsedClaims.SessionID != sessionID.String() {
		t.Fatalf("session id mismatch: expected %s, got %s", sessionID.String(), parsedClaims.SessionID)
	}
	if parsedClaims.Role != "authenticated" {
		t.Fatalf("role mismatch: expected authenticated, got %s", parsedClaims.Role)
	}
	if parsedClaims.Issuer != "supabase" {
		t.Fatalf("issuer mismatch: expected supabase, got %s", parsedClaims.Issuer)
	}
}
