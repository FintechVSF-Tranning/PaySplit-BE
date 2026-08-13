package config

import "time"

type Config struct {
	App      AppConfig
	Database DatabaseConfig
	Auth     AuthConfig
}

type AppConfig struct {
	Environment                string
	Address                    string
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
	panic("TODO: implement config.Load")
}

func (c *Config) Validate() error {
	panic("TODO: implement Config.Validate")
}
