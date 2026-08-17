package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config chứa các thiết lập runtime đã được kiểm tra dùng để khởi tạo API.
type Config struct {
	App        AppConfig
	Database   DatabaseConfig
	Auth       AuthConfig
	SMTP       SMTPConfig
	Cloudinary CloudinaryConfig
	Avatar     AvatarConfig
	Cleanup    CleanupConfig
	Group      GroupConfig
}

// AppConfig chứa cấu hình HTTP server và middleware ở cấp tiến trình.
type AppConfig struct {
	Environment                string
	Address                    string
	RequestTimeout             time.Duration
	CORSAllowedOrigins         []string
	RateLimitRequestsPerMinute int
}

// DatabaseConfig chứa cấu hình kết nối và pool PostgreSQL; giá trị này không
// đại diện cho một kết nối database đã được mở.
type DatabaseConfig struct {
	URL               string
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
}

// AuthConfig chứa các thiết lập cần thiết để phát hành access token.
type AuthConfig struct {
	JWTSecret            string
	JWTIssuer            string
	AccessTokenTTL       time.Duration
	RefreshTokenTTL      time.Duration
	EmailVerificationTTL time.Duration
	PasswordResetTTL     time.Duration
	EmailVerificationURL string
	PasswordResetURL     string
}

type SMTPConfig struct {
	Host        string
	Port        int
	Username    string
	AppPassword string
	FromName    string
	Timeout     time.Duration
}
type CloudinaryConfig struct {
	CloudName string
	APIKey    string
	APISecret string
}
type AvatarConfig struct {
	UploadTimeout            time.Duration
	ProcessingTimeout        time.Duration
	MaxConcurrentConversions int
}
type CleanupConfig struct {
	Interval            time.Duration
	Retention           time.Duration
	MediaWorkerInterval time.Duration
	MediaMaxAttempts    int
}

// GroupConfig chứa cấu hình dùng riêng cho module group management.
type GroupConfig struct {
	InviteBaseURL string
}

// Load đọc cấu hình runtime từ biến môi trường, áp dụng giá trị mặc định và
// kiểm tra kết quả trước khi bootstrap khởi tạo các tài nguyên bên ngoài.
func Load() (*Config, error) {
	// File .env hỗ trợ cấu hình thuận tiện khi phát triển cục bộ. Biến môi trường
	// hiện có vẫn được ưu tiên để nền tảng triển khai có thể cung cấp giá trị riêng.
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load .env: %w", err)
	}

	maxConns, err := int32Env("DB_MAX_CONNS", 10)
	if err != nil {
		return nil, err
	}
	minConns, err := int32Env("DB_MIN_CONNS", 2)
	if err != nil {
		return nil, err
	}
	maxConnLifetime, err := durationEnv("DB_MAX_CONN_LIFETIME_MINUTES", 60, time.Minute)
	if err != nil {
		return nil, err
	}
	maxConnIdle, err := durationEnv("DB_MAX_CONN_IDLE_MINUTES", 15, time.Minute)
	if err != nil {
		return nil, err
	}
	healthCheckPeriod, err := durationEnv("DB_HEALTH_CHECK_SECONDS", 30, time.Second)
	if err != nil {
		return nil, err
	}
	accessTokenTTL, err := durationEnv("JWT_ACCESS_TOKEN_TTL_MINUTES", 15, time.Minute)
	if err != nil {
		return nil, err
	}
	rateLimit, err := intEnv("HTTP_RATE_LIMIT_REQUESTS_PER_MINUTE", 30)
	if err != nil {
		return nil, err
	}
	requestTimeout, err := durationEnv("HTTP_REQUEST_TIMEOUT_SECONDS", 15, time.Second)
	if err != nil {
		return nil, err
	}

	refreshTTL, err := durationEnv("AUTH_REFRESH_TOKEN_TTL_HOURS", 168, time.Hour)
	if err != nil {
		return nil, err
	}
	verificationTTL, err := durationEnv("AUTH_EMAIL_VERIFICATION_TTL_MINUTES", 10, time.Minute)
	if err != nil {
		return nil, err
	}
	resetTTL, err := durationEnv("AUTH_PASSWORD_RESET_TTL_MINUTES", 10, time.Minute)
	if err != nil {
		return nil, err
	}
	smtpPort, err := intEnv("SMTP_PORT", 587)
	if err != nil {
		return nil, err
	}
	smtpTimeout, err := durationEnv("SMTP_TIMEOUT_SECONDS", 5, time.Second)
	if err != nil {
		return nil, err
	}
	avatarUploadTimeout, err := durationEnv("AVATAR_UPLOAD_TIMEOUT_SECONDS", 15, time.Second)
	if err != nil {
		return nil, err
	}
	avatarProcessingTimeout, err := durationEnv("AVATAR_PROCESSING_TIMEOUT_SECONDS", 10, time.Second)
	if err != nil {
		return nil, err
	}
	avatarConcurrency, err := intEnv("AVATAR_MAX_CONCURRENT_CONVERSIONS", 2)
	if err != nil {
		return nil, err
	}
	cleanupInterval, err := durationEnv("AUTH_CLEANUP_INTERVAL_HOURS", 24, time.Hour)
	if err != nil {
		return nil, err
	}
	retention, err := durationEnv("AUTH_RECORD_RETENTION_DAYS", 30, 24*time.Hour)
	if err != nil {
		return nil, err
	}
	mediaInterval, err := durationEnv("MEDIA_CLEANUP_WORKER_INTERVAL_SECONDS", 60, time.Second)
	if err != nil {
		return nil, err
	}
	mediaAttempts, err := intEnv("MEDIA_CLEANUP_MAX_ATTEMPTS", 10)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		App: AppConfig{
			Environment:                stringEnv("APP_ENV", "development"),
			Address:                    stringEnv("HTTP_ADDRESS", ":8080"),
			RequestTimeout:             requestTimeout,
			CORSAllowedOrigins:         csvEnv("HTTP_CORS_ALLOWED_ORIGINS"),
			RateLimitRequestsPerMinute: rateLimit,
		},
		Database: DatabaseConfig{
			URL:               os.Getenv("DATABASE_URL"),
			MaxConns:          maxConns,
			MinConns:          minConns,
			MaxConnLifetime:   maxConnLifetime,
			MaxConnIdleTime:   maxConnIdle,
			HealthCheckPeriod: healthCheckPeriod,
		},
		Auth: AuthConfig{
			JWTSecret:            os.Getenv("JWT_SECRET_KEY"),
			JWTIssuer:            stringEnv("JWT_ISSUER", "paysplit-backend"),
			AccessTokenTTL:       accessTokenTTL,
			RefreshTokenTTL:      refreshTTL,
			EmailVerificationTTL: verificationTTL,
			PasswordResetTTL:     resetTTL,
			EmailVerificationURL: os.Getenv("AUTH_EMAIL_VERIFICATION_URL"),
			PasswordResetURL:     os.Getenv("AUTH_PASSWORD_RESET_URL"),
		},
		SMTP:       SMTPConfig{Host: stringEnv("SMTP_HOST", "smtp.gmail.com"), Port: smtpPort, Username: os.Getenv("SMTP_USERNAME"), AppPassword: os.Getenv("SMTP_APP_PASSWORD"), FromName: stringEnv("SMTP_FROM_NAME", "PaySplit"), Timeout: smtpTimeout},
		Cloudinary: CloudinaryConfig{CloudName: os.Getenv("CLOUDINARY_CLOUD_NAME"), APIKey: os.Getenv("CLOUDINARY_API_KEY"), APISecret: os.Getenv("CLOUDINARY_API_SECRET")},
		Avatar:     AvatarConfig{UploadTimeout: avatarUploadTimeout, ProcessingTimeout: avatarProcessingTimeout, MaxConcurrentConversions: avatarConcurrency},
		Cleanup:    CleanupConfig{Interval: cleanupInterval, Retention: retention, MediaWorkerInterval: mediaInterval, MediaMaxAttempts: mediaAttempts},
		Group:      GroupConfig{InviteBaseURL: os.Getenv("APP_INVITE_BASE_URL")},
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate từ chối cấu hình thiếu hoặc không nhất quán để startup thất bại
// trước khi mở cổng mạng hoặc kết nối database.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config must not be nil")
	}
	if strings.TrimSpace(c.App.Address) == "" {
		return errors.New("HTTP_ADDRESS must not be empty")
	}
	if c.App.RequestTimeout <= 0 {
		return errors.New("HTTP_REQUEST_TIMEOUT_SECONDS must be positive")
	}
	if len(c.App.CORSAllowedOrigins) == 0 {
		return errors.New("HTTP_CORS_ALLOWED_ORIGINS must contain at least one origin")
	}
	if c.App.RateLimitRequestsPerMinute <= 0 {
		return errors.New("HTTP_RATE_LIMIT_REQUESTS_PER_MINUTE must be positive")
	}
	if strings.TrimSpace(c.Database.URL) == "" {
		return errors.New("DATABASE_URL must not be empty")
	}
	if c.Database.MinConns < 0 || c.Database.MaxConns <= 0 || c.Database.MinConns > c.Database.MaxConns {
		return errors.New("DB_MIN_CONNS must be non-negative and no greater than DB_MAX_CONNS")
	}
	if c.Database.MaxConnLifetime <= 0 || c.Database.MaxConnIdleTime <= 0 || c.Database.HealthCheckPeriod <= 0 {
		return errors.New("database duration settings must be positive")
	}
	if strings.TrimSpace(c.Auth.JWTSecret) == "" {
		return errors.New("JWT_SECRET_KEY must not be empty")
	}
	if strings.TrimSpace(c.Auth.JWTIssuer) == "" {
		return errors.New("JWT_ISSUER must not be empty")
	}
	if c.Auth.AccessTokenTTL != 240*time.Minute {
		return errors.New("JWT_ACCESS_TOKEN_TTL_MINUTES must be 15 for auth v1")
	}
	if c.Auth.RefreshTokenTTL != 7*24*time.Hour || c.Auth.EmailVerificationTTL != 10*time.Minute || c.Auth.PasswordResetTTL != 10*time.Minute {
		return errors.New("auth TTL settings must match the v1 contract")
	}
	if strings.TrimSpace(c.Auth.EmailVerificationURL) == "" || strings.TrimSpace(c.Auth.PasswordResetURL) == "" {
		return errors.New("auth callback URLs must not be empty")
	}
	if strings.TrimSpace(c.SMTP.Host) == "" || c.SMTP.Port <= 0 || strings.TrimSpace(c.SMTP.Username) == "" || strings.TrimSpace(c.SMTP.AppPassword) == "" || strings.TrimSpace(c.SMTP.FromName) == "" || c.SMTP.Timeout <= 0 {
		return errors.New("Gmail SMTP configuration is required")
	}
	if strings.TrimSpace(c.Cloudinary.CloudName) == "" || strings.TrimSpace(c.Cloudinary.APIKey) == "" || strings.TrimSpace(c.Cloudinary.APISecret) == "" {
		return errors.New("Cloudinary configuration is required")
	}
	if c.Avatar.UploadTimeout <= 0 || c.Avatar.ProcessingTimeout <= 0 || c.Avatar.MaxConcurrentConversions <= 0 {
		return errors.New("avatar settings must be positive")
	}
	if c.Cleanup.Interval <= 0 || c.Cleanup.Retention <= 0 || c.Cleanup.MediaWorkerInterval <= 0 || c.Cleanup.MediaMaxAttempts != 10 {
		return errors.New("cleanup settings are invalid")
	}
	if _, err := url.Parse(c.Group.InviteBaseURL); strings.TrimSpace(c.Group.InviteBaseURL) == "" || err != nil {
		return errors.New("APP_INVITE_BASE_URL must be a valid HTTPS URL or deep link base")
	}
	return nil
}

func stringEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func csvEnv(name string) []string {
	values := strings.Split(os.Getenv(name), ",")
	origins := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			origins = append(origins, value)
		}
	}
	return origins
}

func intEnv(name string, fallback int) (int, error) {
	value := stringEnv(name, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func int32Env(name string, fallback int32) (int32, error) {
	value, err := intEnv(name, int(fallback))
	if err != nil {
		return 0, err
	}
	if value < -1<<31 || value > 1<<31-1 {
		return 0, fmt.Errorf("parse %s: value is outside int32 range", name)
	}
	return int32(value), nil
}

func durationEnv(name string, fallback int, unit time.Duration) (time.Duration, error) {
	value, err := intEnv(name, fallback)
	if err != nil {
		return 0, err
	}
	return time.Duration(value) * unit, nil
}
