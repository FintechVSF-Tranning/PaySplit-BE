package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	App      AppConfig
	Database DatabaseConfig
	Auth     AuthConfig
}

type AppConfig struct {
	Environment                string
	Address                    string
	RequestTimeout             time.Duration
	CORSAllowedOrigins         []string
	RateLimitRequestsPerMinute int
}

type DatabaseConfig struct {
	URL               string
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
}

type AuthConfig struct {
	JWTSecret      string
	JWTIssuer      string
	AccessTokenTTL time.Duration
}

func Load() (*Config, error) {
	// .env is a local-development convenience. Existing environment variables
	// keep precedence, which lets deployment platforms supply their own values.
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
	accessTokenTTL, err := durationEnv("JWT_ACCESS_TOKEN_TTL_MINUTES", 60, time.Minute)
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
			JWTSecret:      os.Getenv("JWT_SECRET_KEY"),
			JWTIssuer:      stringEnv("JWT_ISSUER", "paysplit-backend"),
			AccessTokenTTL: accessTokenTTL,
		},
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

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
	if c.Auth.AccessTokenTTL <= 0 {
		return errors.New("JWT_ACCESS_TOKEN_TTL_MINUTES must be positive")
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
