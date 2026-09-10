package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadAuthDefaults(t *testing.T) {
	values := map[string]string{"APP_ENV": "development", "HTTP_CORS_ALLOWED_ORIGINS": "http://localhost:3000", "DATABASE_URL": "postgres://local/test", "REDIS_URL": "redis://localhost:6380/0", "DB_APPLICATION_NAME": "paysplit-api", "AUTH_EMAIL_VERIFICATION_TTL_MINUTES": "10", "AUTH_PASSWORD_RESET_TTL_MINUTES": "10", "AUTH_EMAIL_VERIFICATION_URL": "paysplit://verify-email", "AUTH_PASSWORD_RESET_URL": "paysplit://reset-password", "SMTP_USERNAME": "owner@gmail.com", "SMTP_APP_PASSWORD": "app-password", "CLOUDINARY_CLOUD_NAME": "test", "CLOUDINARY_API_KEY": "test", "CLOUDINARY_API_SECRET": "test", "APP_INVITE_BASE_URL": "https://paysplit.app/join", "RIVER_FETCH_COOLDOWN_MS": "100", "RIVER_FETCH_POLL_INTERVAL_MS": "1000", "RIVER_POLL_ONLY": "false"}
	for key, value := range values {
		t.Setenv(key, value)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.EmailVerificationTTL != 10*time.Minute || cfg.Auth.PasswordResetTTL != 10*time.Minute {
		t.Fatalf("unexpected auth TTLs: %+v", cfg.Auth)
	}
	// Vòng đời phiên giờ do Redis quyết định, không còn TTL access/refresh token.
	if cfg.Redis.IdleTTL != 7*24*time.Hour || cfg.Redis.AbsoluteTTL != 30*24*time.Hour {
		t.Fatalf("unexpected session TTLs: %+v", cfg.Redis)
	}
	if cfg.Database.ApplicationName != "paysplit-api" {
		t.Fatalf("DB application name = %q, want paysplit-api", cfg.Database.ApplicationName)
	}
	if cfg.River.PollOnly || cfg.River.FetchPollInterval != time.Second || cfg.River.FetchCooldown != 100*time.Millisecond {
		t.Fatalf("unexpected River defaults: %+v", cfg.River)
	}
}

func TestLoadPrefersPlatformPortOverHTTPPort(t *testing.T) {
	values := map[string]string{"APP_ENV": "development", "HTTP_CORS_ALLOWED_ORIGINS": "http://localhost:3000", "DATABASE_URL": "postgres://local/test", "REDIS_URL": "redis://localhost:6380/0", "AUTH_EMAIL_VERIFICATION_TTL_MINUTES": "10", "AUTH_PASSWORD_RESET_TTL_MINUTES": "10", "AUTH_EMAIL_VERIFICATION_URL": "paysplit://verify-email", "AUTH_PASSWORD_RESET_URL": "paysplit://reset-password", "SMTP_USERNAME": "owner@gmail.com", "SMTP_APP_PASSWORD": "app-password", "CLOUDINARY_CLOUD_NAME": "test", "CLOUDINARY_API_KEY": "test", "CLOUDINARY_API_SECRET": "test", "APP_INVITE_BASE_URL": "https://paysplit.app/join", "HTTP_HOST": "0.0.0.0", "HTTP_PORT": "8080", "PORT": "36015", "HTTP_ADDRESS": ""}
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
func TestValidateRejectsWrongVerificationTTL(t *testing.T) {
	cfg := validConfig()
	cfg.Auth.EmailVerificationTTL = time.Hour
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected verification TTL validation error")
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

func TestValidateRejectsInvalidGroupMaxActiveMembers(t *testing.T) {
	for _, invalid := range []int{0, -1, -50} {
		cfg := validConfig()
		cfg.Group.MaxActiveMembers = invalid
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "GROUP_MAX_ACTIVE_MEMBERS") {
			t.Fatalf("Validate() with MaxActiveMembers=%d: error = %v, want GROUP_MAX_ACTIVE_MEMBERS error", invalid, err)
		}
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

func TestValidateRejectsInvalidConnectionEfficientEventSettings(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Config)
		wantError string
	}{
		{
			name:      "blank application name",
			mutate:    func(cfg *Config) { cfg.Database.ApplicationName = " " },
			wantError: "DB_APPLICATION_NAME",
		},
		{
			name:      "non-positive fetch poll interval",
			mutate:    func(cfg *Config) { cfg.River.FetchPollInterval = 0 },
			wantError: "RIVER_FETCH_POLL_INTERVAL_MS",
		},
		{
			name: "fetch poll interval below cooldown",
			mutate: func(cfg *Config) {
				cfg.River.FetchCooldown = time.Second
				cfg.River.FetchPollInterval = 100 * time.Millisecond
			},
			wantError: "RIVER_FETCH_POLL_INTERVAL_MS",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Validate() error = %v, want %s", err, tt.wantError)
			}
			if tt.name == "fetch poll interval below cooldown" && !strings.Contains(err.Error(), "RIVER_FETCH_COOLDOWN_MS") {
				t.Fatalf("Validate() error = %v, want RIVER_FETCH_COOLDOWN_MS", err)
			}
		})
	}
}

func TestLoadSettlementDefaults_AC6AndAC10(t *testing.T) {
	values := map[string]string{"APP_ENV": "development", "HTTP_CORS_ALLOWED_ORIGINS": "http://localhost:3000", "DATABASE_URL": "postgres://local/test", "REDIS_URL": "redis://localhost:6380/0", "AUTH_EMAIL_VERIFICATION_TTL_MINUTES": "10", "AUTH_PASSWORD_RESET_TTL_MINUTES": "10", "AUTH_EMAIL_VERIFICATION_URL": "paysplit://verify-email", "AUTH_PASSWORD_RESET_URL": "paysplit://reset-password", "SMTP_USERNAME": "owner@gmail.com", "SMTP_APP_PASSWORD": "app-password", "CLOUDINARY_CLOUD_NAME": "test", "CLOUDINARY_API_KEY": "test", "CLOUDINARY_API_SECRET": "test", "APP_INVITE_BASE_URL": "https://paysplit.app/join"}
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

func TestListenerDSNFallsBackAndDetectsDedicatedPool(t *testing.T) {
	// covers: AC-13
	cfg := DatabaseConfig{URL: "postgres://local/app"}
	if cfg.ListenerDSN() != "postgres://local/app" || cfg.UsesDedicatedListenerPool() {
		t.Fatalf("empty listener URL should reuse DATABASE_URL, got %+v", cfg)
	}
	cfg.ListenerURL = "postgres://local/listen"
	if cfg.ListenerDSN() != "postgres://local/listen" || !cfg.UsesDedicatedListenerPool() {
		t.Fatalf("dedicated listener URL was ignored: %+v", cfg)
	}
}

func TestValidateRejectsBadRealtimeMinAppVersion(t *testing.T) {
	// covers: AC-23
	cfg := validConfig()
	cfg.Realtime.MinUserStreamAppVersion = "1.4.0"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected REALTIME_MIN_USER_STREAM_APP_VERSION to require build")
	}
	cfg.Realtime.MinUserStreamAppVersion = "1.4.0+27"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid min app version: %v", err)
	}
}

// Lỗ hổng thật nằm ở tầng Load, không phải Validate: trước đây APP_ENV chưa đặt
// được stringEnv biến thành "development", nên luật bắt buộc mật khẩu Redis tự
// tắt. Một lần deploy quên đặt biến là quay lại đúng chỗ cũ. Test này chạy qua
// Load để khóa lại đường đi thật, chứ không dựng Config bằng tay.
func TestLoadTreatsUnsetAppEnvAsNotDevelopment(t *testing.T) {
	base := map[string]string{
		"HTTP_CORS_ALLOWED_ORIGINS":           "http://localhost:3000",
		"DATABASE_URL":                        "postgres://local/test",
		"AUTH_EMAIL_VERIFICATION_TTL_MINUTES": "10",
		"AUTH_PASSWORD_RESET_TTL_MINUTES":     "10",
		"AUTH_EMAIL_VERIFICATION_URL":         "paysplit://verify-email",
		"AUTH_PASSWORD_RESET_URL":             "paysplit://reset-password",
		"SMTP_USERNAME":                       "owner@gmail.com",
		"SMTP_APP_PASSWORD":                   "app-password",
		"CLOUDINARY_CLOUD_NAME":               "test",
		"CLOUDINARY_API_KEY":                  "test",
		"CLOUDINARY_API_SECRET":               "test",
		"APP_INVITE_BASE_URL":                 "https://paysplit.app/join",
	}

	tests := []struct {
		name     string
		appEnv   string
		redisURL string
		wantErr  bool
	}{
		{name: "APP_ENV chưa đặt, Redis không mật khẩu", appEnv: "", redisURL: "redis://localhost:6380/0", wantErr: true},
		{name: "APP_ENV chưa đặt, Redis có mật khẩu", appEnv: "", redisURL: "redis://:secret@localhost:6380/0"},
		{name: "development tường minh vẫn được nới", appEnv: "development", redisURL: "redis://localhost:6380/0"},
		{name: "production, Redis không mật khẩu", appEnv: "production", redisURL: "redis://localhost:6380/0", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range base {
				t.Setenv(key, value)
			}
			// t.Setenv với chuỗi rỗng đặt biến thành rỗng, và stringEnv coi rỗng
			// như chưa đặt, nên đây đúng là ca "quên đặt APP_ENV".
			t.Setenv("APP_ENV", tt.appEnv)
			t.Setenv("REDIS_URL", tt.redisURL)

			cfg, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Load() = nil: một Redis không mật khẩu được chấp nhận khi APP_ENV không phải development")
				}
				if !strings.Contains(err.Error(), "REDIS_URL") {
					t.Fatalf("Load() error = %v, muốn nêu tên REDIS_URL", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() lỗi bất ngờ: %v", err)
			}
			if cfg.App.Environment != tt.appEnv {
				t.Fatalf("Environment = %q, want %q: Load không được tự điền mặc định nào cho APP_ENV", cfg.App.Environment, tt.appEnv)
			}
		})
	}
}

// Redis giữ toàn bộ phiên đăng nhập ở dạng dùng được ngay, nên một instance
// không mật khẩu là toàn bộ tài khoản bị phơi ra cho bất kỳ ai nối được tới port.
// Spec 0011 Follow up 4.
func TestValidateRequiresRedisPasswordOutsideDevelopment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		environment string
		redisURL    string
		wantErr     bool
	}{
		{name: "production không mật khẩu bị từ chối", environment: "production", redisURL: "redis://redis.internal:6379/0", wantErr: true},
		{name: "production userinfo chỉ có username bị từ chối", environment: "production", redisURL: "redis://appuser@redis.internal:6379/0", wantErr: true},
		{name: "production mật khẩu rỗng bị từ chối", environment: "production", redisURL: "redis://:@redis.internal:6379/0", wantErr: true},
		{name: "production có mật khẩu được chấp nhận", environment: "production", redisURL: "redis://:secret@redis.internal:6379/0"},
		{name: "staging cũng bị ràng buộc", environment: "staging", redisURL: "redis://redis.internal:6379/0", wantErr: true},
		{name: "environment rỗng fail closed", environment: "", redisURL: "redis://redis.internal:6379/0", wantErr: true},
		{name: "development được nới", environment: "development", redisURL: "redis://localhost:6380/0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := validConfig()
			cfg.App.Environment = tt.environment
			cfg.Redis.URL = tt.redisURL

			err := cfg.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Validate() = nil: REDIS_URL không mật khẩu được chấp nhận ngoài development")
				}
				if !strings.Contains(err.Error(), "REDIS_URL") {
					t.Fatalf("Validate() error = %v, muốn nêu tên REDIS_URL để người vận hành biết sửa ở đâu", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() lỗi bất ngờ: %v", err)
			}
		})
	}
}

func validConfig() *Config {
	return &Config{
		App:        AppConfig{Environment: "development", Address: ":8080", RequestTimeout: 15 * time.Second, CORSAllowedOrigins: []string{"http://localhost"}, RateLimitRequestsPerMinute: 300, InviteAttemptsPerMinute: 30},
		Database:   DatabaseConfig{URL: "postgres://local/test", ApplicationName: "paysplit-api", MaxConns: 10, MinConns: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute, HealthCheckPeriod: time.Second},
		Auth:       AuthConfig{EmailVerificationTTL: 10 * time.Minute, PasswordResetTTL: 10 * time.Minute, EmailVerificationURL: "paysplit://verify", PasswordResetURL: "paysplit://reset"},
		SMTP:       SMTPConfig{Host: "smtp.gmail.com", Port: 587, Username: "owner@gmail.com", AppPassword: "app", FromName: "PaySplit", Timeout: 5 * time.Second},
		Cloudinary: CloudinaryConfig{CloudName: "test", APIKey: "test", APISecret: "test"},
		Avatar:     AvatarConfig{UploadTimeout: 15 * time.Second, ProcessingTimeout: 10 * time.Second, MaxConcurrentConversions: 2},
		Cleanup:    CleanupConfig{Interval: 24 * time.Hour, Retention: 30 * 24 * time.Hour, MediaWorkerInterval: time.Minute, MediaMaxAttempts: 10},
		River:      RiverConfig{WorkerCount: 5, FetchCooldown: 100 * time.Millisecond, FetchPollInterval: time.Second},
		Redis:      RedisConfig{URL: "redis://localhost:6380/0", PoolSize: 20, DialTimeout: 5 * time.Second, ReadTimeout: 2 * time.Second, IdleTTL: 7 * 24 * time.Hour, AbsoluteTTL: 30 * 24 * time.Hour},
		Group:      GroupConfig{InviteBaseURL: "https://paysplit.app/join", MaxActiveMembers: 50},
		OCR:        OCRConfig{Endpoint: "https://api.cloud.llamaindex.ai", ProviderTimeout: 8 * time.Second, MaxAttempts: 3, RetryBaseDelay: time.Second, ManualLimit: 5, ManualWindowHours: 24 * time.Hour, RawRetentionDays: 30 * 24 * time.Hour},
		BillImage:  BillImageConfig{MaxCount: 5, MaxBytes: 10 * 1024 * 1024, UploadTimeout: 15 * time.Second, ProcessingTimeout: 10 * time.Second, SignedURLTTL: 5 * time.Minute},
		BillSSE:    BillSSEConfig{HeartbeatInterval: 15 * time.Second, MaxConnectionAge: 15 * time.Minute},
		GroupSync:  GroupSyncConfig{HeartbeatInterval: 15 * time.Second, MaxConnectionAge: 15 * time.Minute, EventRetention: 7 * 24 * time.Hour},
		Settlement: SettlementConfig{VietQRServiceBaseURL: "https://img.vietqr.io/image", VietQRTemplate: "compact", ProofMaxBytes: 10 << 20, ProofSignedURLTTL: 5 * time.Minute, ReminderStaleAge: 72 * time.Hour, ReminderMaxCount: 3, StalledConfirmationAge: 48 * time.Hour},
	}
}

func TestValidateRejectsMissingRedisURL(t *testing.T) {
	cfg := validConfig()
	cfg.Redis.URL = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected REDIS_URL validation error")
	}
}

func TestValidateRejectsNonRedisScheme(t *testing.T) {
	for _, raw := range []string{"postgres://localhost:5432/db", "http://localhost:6379", "redis://"} {
		cfg := validConfig()
		cfg.Redis.URL = raw
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate() accepted REDIS_URL %q, want rejection", raw)
		}
	}
}

// TTL trượt được gia hạn mỗi request. Nếu nó vượt trần tuyệt đối thì trần mất tác
// dụng và phiên sống vô hạn với người dùng mở app đều đặn — đúng thứ mà trần cứng
// sinh ra để chặn.
func TestValidateRejectsIdleTTLAboveAbsoluteTTL(t *testing.T) {
	cfg := validConfig()
	cfg.Redis.IdleTTL = 31 * 24 * time.Hour
	cfg.Redis.AbsoluteTTL = 30 * 24 * time.Hour
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected idle TTL to be rejected when it exceeds the absolute TTL")
	}
}

// Hàng audit trong `sessions` mang expires_at = now + AbsoluteTTL, còn worker dọn
// rác xoá theo Retention. Retention ngắn hơn (AbsoluteTTL - IdleTTL) sẽ xoá mất bản
// ghi của phiên vẫn đang sống trên Redis, và lỗi đó chỉ lộ ra sau nhiều tuần chạy.
func TestValidateRejectsRetentionShorterThanSessionDrift(t *testing.T) {
	cfg := validConfig()
	cfg.Redis.IdleTTL = 7 * 24 * time.Hour
	cfg.Redis.AbsoluteTTL = 30 * 24 * time.Hour
	cfg.Cleanup.Retention = 22 * 24 * time.Hour // cần >= 23 ngày
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected retention shorter than the absolute-minus-idle drift to be rejected")
	}

	cfg.Cleanup.Retention = 23 * 24 * time.Hour
	if err := cfg.Validate(); err != nil {
		t.Fatalf("retention exactly covering the drift should be accepted, got %v", err)
	}
}
