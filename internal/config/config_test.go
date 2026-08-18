package config

import (
	"testing"
	"time"
)

func TestLoadAuthDefaults(t *testing.T) {
	values := map[string]string{"HTTP_CORS_ALLOWED_ORIGINS": "http://localhost:3000", "DATABASE_URL": "postgres://local/test", "JWT_SECRET_KEY": "long-development-secret", "JWT_ACCESS_TOKEN_TTL_MINUTES": "15", "AUTH_REFRESH_TOKEN_TTL_HOURS": "168", "AUTH_EMAIL_VERIFICATION_TTL_MINUTES": "10", "AUTH_PASSWORD_RESET_TTL_MINUTES": "10", "AUTH_EMAIL_VERIFICATION_URL": "paysplit://verify-email", "AUTH_PASSWORD_RESET_URL": "paysplit://reset-password", "SMTP_USERNAME": "owner@gmail.com", "SMTP_APP_PASSWORD": "app-password", "CLOUDINARY_CLOUD_NAME": "test", "CLOUDINARY_API_KEY": "test", "CLOUDINARY_API_SECRET": "test", "APP_INVITE_BASE_URL": "paysplit://join"}
	for key, value := range values {
		t.Setenv(key, value)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.AccessTokenTTL != 15*time.Minute || cfg.Auth.RefreshTokenTTL != 7*24*time.Hour || cfg.Auth.EmailVerificationTTL != 10*time.Minute {
		t.Fatalf("unexpected auth TTLs: %+v", cfg.Auth)
	}
}

func TestValidateRejectsMissingGmail(t *testing.T) {
	cfg := validConfig()
	cfg.SMTP.AppPassword = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing Gmail configuration error")
	}
}
func TestValidateRejectsWrongAccessTTL(t *testing.T) {
	cfg := validConfig()
	cfg.Auth.AccessTokenTTL = time.Hour
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected access TTL validation error")
	}
}

func TestValidateRejectsBlankInviteBaseURL(t *testing.T) {
	cfg := validConfig()
	cfg.Group.InviteBaseURL = "   "
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected an error for a blank APP_INVITE_BASE_URL")
	}
}

func TestValidateAcceptsADeepLinkInviteBaseURL(t *testing.T) {
	cfg := validConfig()
	cfg.Group.InviteBaseURL = "paysplit://join"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error for a valid deep link base: %v", err)
	}
}

func TestValidateRejectsInvalidOCR(t *testing.T) {
	cfg := validConfig()
	cfg.OCR.ProviderTimeout = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected an error for invalid OCR provider timeout")
	}
}

func TestValidateRejectsInvalidBillImage(t *testing.T) {
	cfg := validConfig()
	cfg.BillImage.MaxBytes = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected an error for invalid BillImage MaxBytes")
	}
}

func validConfig() *Config {
	return &Config{
		App:        AppConfig{Address: ":8080", RequestTimeout: 15 * time.Second, CORSAllowedOrigins: []string{"http://localhost"}, RateLimitRequestsPerMinute: 30},
		Database:   DatabaseConfig{URL: "postgres://local/test", MaxConns: 10, MinConns: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second},
		Auth:       AuthConfig{JWTSecret: "secret", JWTIssuer: "issuer", AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 7 * 24 * time.Hour, EmailVerificationTTL: 10 * time.Minute, PasswordResetTTL: 10 * time.Minute, EmailVerificationURL: "paysplit://verify", PasswordResetURL: "paysplit://reset"},
		SMTP:       SMTPConfig{Host: "smtp.gmail.com", Port: 587, Username: "owner@gmail.com", AppPassword: "app", FromName: "PaySplit", Timeout: 5 * time.Second},
		Cloudinary: CloudinaryConfig{CloudName: "test", APIKey: "test", APISecret: "test"},
		Avatar:     AvatarConfig{UploadTimeout: 15 * time.Second, ProcessingTimeout: 10 * time.Second, MaxConcurrentConversions: 2},
		Cleanup:    CleanupConfig{Interval: 24 * time.Hour, Retention: 30 * 24 * time.Hour, MediaWorkerInterval: time.Minute, MediaMaxAttempts: 10},
		River:      RiverConfig{WorkerCount: 5, FetchCooldown: 100 * time.Millisecond},
		Group:      GroupConfig{InviteBaseURL: "paysplit://join"},
		OCR:        OCRConfig{Endpoint: "https://api.cloud.llamaindex.ai", ProviderTimeout: 8 * time.Second, MaxAttempts: 3, RetryBaseDelay: time.Second, ManualLimit: 5, ManualWindowHours: 24 * time.Hour, RawRetentionDays: 30 * 24 * time.Hour},
		BillImage:  BillImageConfig{MaxCount: 5, MaxBytes: 10 * 1024 * 1024, UploadTimeout: 15 * time.Second, ProcessingTimeout: 10 * time.Second, SignedURLTTL: 5 * time.Minute},
	}
}
