package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadAuthDefaults(t *testing.T) {
	values := map[string]string{"HTTP_CORS_ALLOWED_ORIGINS": "http://localhost:3000", "DATABASE_URL": "postgres://local/test", "JWT_SECRET_KEY": "long-development-secret", "JWT_ACCESS_TOKEN_TTL_MINUTES": "15", "AUTH_REFRESH_TOKEN_TTL_HOURS": "168", "AUTH_EMAIL_VERIFICATION_TTL_MINUTES": "10", "AUTH_PASSWORD_RESET_TTL_MINUTES": "10", "AUTH_EMAIL_VERIFICATION_URL": "paysplit://verify-email", "AUTH_PASSWORD_RESET_URL": "paysplit://reset-password", "SMTP_USERNAME": "owner@gmail.com", "SMTP_APP_PASSWORD": "app-password", "CLOUDINARY_CLOUD_NAME": "test", "CLOUDINARY_API_KEY": "test", "CLOUDINARY_API_SECRET": "test", "APP_INVITE_BASE_URL": "https://paysplit.app/join"}
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

func TestLoadPrefersPlatformPortOverHTTPPort(t *testing.T) {
	values := map[string]string{"HTTP_CORS_ALLOWED_ORIGINS": "http://localhost:3000", "DATABASE_URL": "postgres://local/test", "JWT_SECRET_KEY": "long-development-secret", "JWT_ACCESS_TOKEN_TTL_MINUTES": "15", "AUTH_REFRESH_TOKEN_TTL_HOURS": "168", "AUTH_EMAIL_VERIFICATION_TTL_MINUTES": "10", "AUTH_PASSWORD_RESET_TTL_MINUTES": "10", "AUTH_EMAIL_VERIFICATION_URL": "paysplit://verify-email", "AUTH_PASSWORD_RESET_URL": "paysplit://reset-password", "SMTP_USERNAME": "owner@gmail.com", "SMTP_APP_PASSWORD": "app-password", "CLOUDINARY_CLOUD_NAME": "test", "CLOUDINARY_API_KEY": "test", "CLOUDINARY_API_SECRET": "test", "APP_INVITE_BASE_URL": "https://paysplit.app/join", "HTTP_HOST": "0.0.0.0", "HTTP_PORT": "8080", "PORT": "36015", "HTTP_ADDRESS": ""}
	for key, value := range values {
		t.Setenv(key, value)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.Port != "36015" || cfg.App.Address != "0.0.0.0:36015" {
		t.Fatalf("server address = %q with port %q, want Vercel platform port 36015", cfg.App.Address, cfg.App.Port)
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

func TestValidateAcceptsHTTPSInviteBaseURL_AC12(t *testing.T) {
	cfg := validConfig()
	cfg.Group.InviteBaseURL = "https://paysplit.app/join"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error for a valid deep link base: %v", err)
	}
}

func TestValidateRejectsUnsafeInviteBaseURLs_AC12(t *testing.T) {
	for _, raw := range []string{
		"http://paysplit.app/join",
		"paysplit://join",
		"https://user:secret@paysplit.app/join",
		"https://paysplit.app/join?source=share",
		"https://paysplit.app/join#fragment",
	} {
		t.Run(raw, func(t *testing.T) {
			cfg := validConfig()
			cfg.Group.InviteBaseURL = raw
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "APP_INVITE_BASE_URL") {
				t.Fatalf("Validate() error = %v, want APP_INVITE_BASE_URL rejection", err)
			}
		})
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

func TestValidateRejectsInvalidGroupSync(t *testing.T) {
	cfg := validConfig()
	cfg.GroupSync.EventRetention = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected an error for a non-positive group event retention")
	}

	cfg = validConfig()
	cfg.GroupSync.HeartbeatInterval = 20 * time.Minute
	cfg.GroupSync.MaxConnectionAge = 15 * time.Minute
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected an error when the group heartbeat outlives the connection")
	}
}

func TestValidateRejectsInvalidBillSSE(t *testing.T) {
	cfg := validConfig()
	cfg.BillSSE.HeartbeatInterval = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected an error for invalid BillSSE HeartbeatInterval")
	}

	cfg = validConfig()
	cfg.BillSSE.HeartbeatInterval = 20 * time.Minute
	cfg.BillSSE.MaxConnectionAge = 15 * time.Minute
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected an error when HeartbeatInterval >= MaxConnectionAge")
	}
}

func TestLoadSettlementDefaults_AC6AndAC10(t *testing.T) {
	values := map[string]string{"HTTP_CORS_ALLOWED_ORIGINS": "http://localhost:3000", "DATABASE_URL": "postgres://local/test", "JWT_SECRET_KEY": "long-development-secret", "JWT_ACCESS_TOKEN_TTL_MINUTES": "15", "AUTH_REFRESH_TOKEN_TTL_HOURS": "168", "AUTH_EMAIL_VERIFICATION_TTL_MINUTES": "10", "AUTH_PASSWORD_RESET_TTL_MINUTES": "10", "AUTH_EMAIL_VERIFICATION_URL": "paysplit://verify-email", "AUTH_PASSWORD_RESET_URL": "paysplit://reset-password", "SMTP_USERNAME": "owner@gmail.com", "SMTP_APP_PASSWORD": "app-password", "CLOUDINARY_CLOUD_NAME": "test", "CLOUDINARY_API_KEY": "test", "CLOUDINARY_API_SECRET": "test", "APP_INVITE_BASE_URL": "https://paysplit.app/join"}
	for key, value := range values {
		t.Setenv(key, value)
	}
	for _, key := range []string{"PAYMENT_PROOF_MAX_BYTES", "PAYMENT_PROOF_SIGNED_URL_TTL", "PAYMENT_REMINDER_STALE_HOURS", "PAYMENT_REMINDER_MAX_COUNT", "STALLED_CONFIRMATION_HOURS"} {
		t.Setenv(key, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Settlement.ProofMaxBytes != 10<<20 || cfg.Settlement.ProofSignedURLTTL != 5*time.Minute || cfg.Settlement.ReminderStaleAge != 72*time.Hour || cfg.Settlement.ReminderMaxCount != 3 || cfg.Settlement.StalledConfirmationAge != 48*time.Hour {
		t.Fatalf("unexpected settlement defaults: %+v", cfg.Settlement)
	}
}

func TestValidateAcceptsConfiguredSettlementReminderMaximum_AC10(t *testing.T) {
	cfg := validConfig()
	cfg.Settlement = SettlementConfig{VietQRServiceBaseURL: "https://img.vietqr.io/image", VietQRTemplate: "compact", ProofMaxBytes: 10 << 20, ProofSignedURLTTL: 5 * time.Minute, ReminderStaleAge: 72 * time.Hour, ReminderMaxCount: 2, StalledConfirmationAge: 48 * time.Hour}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("configured reminder maximum rejected: %v", err)
	}
	for _, maxCount := range []int{0, 4} {
		cfg.Settlement.ReminderMaxCount = maxCount
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected reminder maximum %d to be rejected", maxCount)
		}
	}
}

func TestValidateRejectsEmptySettlementBaseURLByVariableName(t *testing.T) {
	cfg := validConfig()
	cfg.Settlement.VietQRServiceBaseURL = ""
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "VIETQR_SERVICE_BASE_URL") {
		t.Fatalf("Validate() error = %v, want named settlement URL error", err)
	}
}

func TestValidateServerlessRuntimeRole_AC1(t *testing.T) {
	cfg := validConfig()
	cfg.App.Environment = "production"
	cfg.App.RuntimeRole = "api"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error for role api in production: %v", err)
	}

	cfg.App.RuntimeRole = "worker"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "APP_RUNTIME_ROLE=api") {
		t.Fatalf("expected error for non-api role in production, got: %v", err)
	}
}

func TestValidateJobConfig_AC10AndAC11(t *testing.T) {
	cfg := validConfig()
	cfg.Job.BatchSize = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero batch size")
	}

	cfg = validConfig()
	cfg.Job.StopClaimingAfter = 50 * time.Second
	cfg.Job.InvocationTimeout = 45 * time.Second
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when StopClaimingAfter >= InvocationTimeout")
	}
}

func TestValidateSyncConfig_AC6(t *testing.T) {
	cfg := validConfig()
	cfg.Sync.PageLimit = 501
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for page limit > 500")
	}
}

func validConfig() *Config {
	return &Config{
		App:        AppConfig{Environment: "development", RuntimeRole: "api", Address: ":8080", RequestTimeout: 15 * time.Second, CORSAllowedOrigins: []string{"http://localhost"}, RateLimitRequestsPerMinute: 300, InviteAttemptsPerMinute: 30},
		Database:   DatabaseConfig{URL: "postgres://local/test", PoolMode: "transaction", MaxConns: 10, MinConns: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second, AcquireTimeout: time.Second, IdleInTransactionTimeout: 5 * time.Second, ApplicationName: "paysplit-api"},
		Auth:       AuthConfig{JWTSecret: "secret", JWTIssuer: "issuer", AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 7 * 24 * time.Hour, EmailVerificationTTL: 10 * time.Minute, PasswordResetTTL: 10 * time.Minute, EmailVerificationURL: "paysplit://verify", PasswordResetURL: "paysplit://reset"},
		SMTP:       SMTPConfig{Host: "smtp.gmail.com", Port: 587, Username: "owner@gmail.com", AppPassword: "app", FromName: "PaySplit", Timeout: 5 * time.Second},
		Cloudinary: CloudinaryConfig{CloudName: "test", APIKey: "test", APISecret: "test"},
		Avatar:     AvatarConfig{UploadTimeout: 15 * time.Second, ProcessingTimeout: 10 * time.Second, MaxConcurrentConversions: 2},
		Cleanup:    CleanupConfig{Interval: 24 * time.Hour, Retention: 30 * 24 * time.Hour, MediaWorkerInterval: time.Minute, MediaMaxAttempts: 10},
		River:      RiverConfig{WorkerCount: 5, FetchCooldown: 100 * time.Millisecond},
		Group:      GroupConfig{InviteBaseURL: "https://paysplit.app/join"},
		OCR:        OCRConfig{Endpoint: "https://api.cloud.llamaindex.ai", ProviderTimeout: 8 * time.Second, MaxAttempts: 3, RetryBaseDelay: time.Second, ManualLimit: 5, ManualWindowHours: 24 * time.Hour, RawRetentionDays: 30 * 24 * time.Hour},
		BillImage:  BillImageConfig{MaxCount: 5, MaxBytes: 10 * 1024 * 1024, UploadTimeout: 15 * time.Second, ProcessingTimeout: 10 * time.Second, SignedURLTTL: 5 * time.Minute},
		BillSSE:    BillSSEConfig{HeartbeatInterval: 15 * time.Second, MaxConnectionAge: 15 * time.Minute},
		GroupSync:  GroupSyncConfig{HeartbeatInterval: 15 * time.Second, MaxConnectionAge: 15 * time.Minute, EventRetention: 7 * 24 * time.Hour},
		Settlement: SettlementConfig{VietQRServiceBaseURL: "https://img.vietqr.io/image", VietQRTemplate: "compact", ProofMaxBytes: 10 << 20, ProofSignedURLTTL: 5 * time.Minute, ReminderStaleAge: 72 * time.Hour, ReminderMaxCount: 3, StalledConfirmationAge: 48 * time.Hour},
		Job: JobConfig{
			ProcessingEnabled:      true,
			BatchSize:              5,
			DispatcherTimeout:      5 * time.Second,
			DispatcherLease:        15 * time.Second,
			InvocationTimeout:      45 * time.Second,
			StopClaimingAfter:      40 * time.Second,
			ExternalTimeout:        35 * time.Second,
			LeaseDuration:          75 * time.Second,
			DrainSlotLeaseDuration: 75 * time.Second,
			BackendNotification:    "app_jobs",
			BackendOCR:             "app_jobs",
			BackendBulkFinalize:    "app_jobs",
			BackendCleanup:         "app_jobs",
			BackendSettlement:      "app_jobs",
			PGNetDispatchTimeout:   10 * time.Second,
			PGNetDrainTimeout:      55 * time.Second,
		},
		Realtime: RealtimeConfig{
			JWTIssuer:          "supabase",
			JWTAudience:        "authenticated",
			TokenTTL:           300 * time.Second,
			ClockSkew:          60 * time.Second,
			MobileRealtimeMode: "supabase",
			PollInterval:       10 * time.Second,
			PollJitterPercent:  20,
			MaxGroupChannels:   10,
		},
		Sync: SyncConfig{
			PageLimit:        500,
			MaxBytes:         262144,
			MaxPagesPerCycle: 4,
		},
	}
}
