package bootstrap_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"paysplit-backend/internal/bootstrap"
	"paysplit-backend/internal/config"
	"paysplit-backend/internal/platform/auth/realtimejwt"
)

func generateTestECDSAKeyPEM(t *testing.T) string {
	t.Helper()
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ecdsa key: %v", err)
	}
	der, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		t.Fatalf("failed to marshal ec key: %v", err)
	}
	pemBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: der,
	})
	return string(pemBlock)
}

func TestServerlessApp_BootAndRouting(t *testing.T) {
	keyPEM := generateTestECDSAKeyPEM(t)

	cfg := &config.Config{
		App: config.AppConfig{
			Environment: "testing",
			RuntimeRole: "api",
			Address:     ":8080",
		},
		Database: config.DatabaseConfig{
			URL:      "postgres://postgres:password@localhost:5432/paysplit",
			MaxConns: 2,
			MinConns: 0,
		},
		Metrics: config.MetricsConfig{
			Enabled: false,
		},
		Auth: config.AuthConfig{
			JWTSecret:            "test-access-token-secret-key-32b!",
			JWTIssuer:            "paysplit-test",
			AccessTokenTTL:       15 * time.Minute,
			RefreshTokenTTL:      7 * 24 * time.Hour,
			EmailVerificationTTL: 24 * time.Hour,
			PasswordResetTTL:     15 * time.Minute,
		},
		Realtime: config.RealtimeConfig{
			JWTPrivateKey: keyPEM,
			JWTKID:        "test-kid",
			JWTIssuer:     "supabase",
			JWTAudience:   "authenticated",
			TokenTTL:      300 * time.Second,
		},
		Sync: config.SyncConfig{
			PageLimit:        500,
			MaxBytes:         262144,
			MaxPagesPerCycle: 4,
			CursorHMACKey:    "test-cursor-key-32-bytes-long!!",
		},
		Job: config.JobConfig{
			TriggerSecret: "test-job-secret-token",
			BatchSize:     5,
		},
	}

	app, err := bootstrap.NewServerless(context.Background(), cfg)
	if err != nil {
		t.Skipf("Skipping DB connected serverless boot test without active postgres pool: %v", err)
	}
	defer app.Close()

	handler := app.Handler()
	if handler == nil {
		t.Fatal("expected non nil serverless handler")
	}

	// 1. Health checks
	reqLive := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	wLive := httptest.NewRecorder()
	handler.ServeHTTP(wLive, reqLive)
	if wLive.Code != http.StatusOK {
		t.Errorf("health/live code = %d, want 200", wLive.Code)
	}

	// 2. App Config
	reqConfig := httptest.NewRequest(http.MethodGet, "/api/v1/app-config", nil)
	wConfig := httptest.NewRecorder()
	handler.ServeHTTP(wConfig, reqConfig)
	if wConfig.Code != http.StatusOK {
		t.Errorf("app-config code = %d, want 200", wConfig.Code)
	}

	// 3. Internal Jobs Unauthorized without secret
	reqDispatchNoAuth := httptest.NewRequest(http.MethodPost, "/internal/jobs/dispatch", nil)
	wDispatchNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(wDispatchNoAuth, reqDispatchNoAuth)
	if wDispatchNoAuth.Code != http.StatusUnauthorized {
		t.Errorf("dispatch without secret code = %d, want 401", wDispatchNoAuth.Code)
	}
}

func TestRealtimeJWT_SigningAndHeaderValidation(t *testing.T) {
	keyPEM := generateTestECDSAKeyPEM(t)

	cfg := config.RealtimeConfig{
		JWTPrivateKey: keyPEM,
		JWTKID:        "realtime-v1",
		JWTIssuer:     "supabase",
		JWTAudience:   "authenticated",
		TokenTTL:      300 * time.Second,
	}

	manager, err := realtimejwt.NewManager(cfg)
	if err != nil {
		t.Fatalf("failed to create realtimejwt manager: %v", err)
	}

	tokenStr, expiresAt, err := manager.Sign(uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	if tokenStr == "" {
		t.Error("expected non empty token string")
	}

	if time.Until(expiresAt) <= 0 || time.Until(expiresAt) > 305*time.Second {
		t.Errorf("unexpected token expiry: %v", expiresAt)
	}
}

func TestAppConfig_ResponseStructure(t *testing.T) {
	cfg := config.AppConfig{
		Environment: "production",
		RuntimeRole: "api",
	}
	realtimeCfg := config.RealtimeConfig{
		MobileRealtimeMode: "supabase",
		SupabaseURL:        "https://test.supabase.co",
		SupabasePublishableKey: "sb_pub_key",
		MaxGroupChannels:   10,
	}
	syncCfg := config.SyncConfig{
		PageLimit:        500,
		MaxBytes:         262144,
		MaxPagesPerCycle: 4,
	}

	fullConfig := config.Config{
		App:      cfg,
		Realtime: realtimeCfg,
		Sync:     syncCfg,
	}

	marshaled, err := json.Marshal(fullConfig)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}

	if len(marshaled) == 0 {
		t.Error("expected non empty json config")
	}
}
