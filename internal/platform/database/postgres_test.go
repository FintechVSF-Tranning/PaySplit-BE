package database

import (
	"testing"
	"time"

	"paysplit-backend/internal/config"
)

func TestParsePoolConfigSetsApplicationName(t *testing.T) {
	// covers: AC-10
	cfg, err := ParsePoolConfig(config.DatabaseConfig{
		URL:               "postgres://postgres:postgres@localhost:5433/paysplit?sslmode=disable",
		ApplicationName:   "paysplit-api-test",
		MaxConns:          6,
		MinConns:          0,
		MaxConnLifetime:   time.Hour,
		MaxConnIdleTime:   15 * time.Minute,
		HealthCheckPeriod: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ConnConfig.RuntimeParams["application_name"]; got != "paysplit-api-test" {
		t.Fatalf("application_name = %q, want paysplit-api-test", got)
	}
}

func TestParseListenerPoolConfigCapsConnections(t *testing.T) {
	// covers: AC-13
	cfg, err := ParseListenerPoolConfig(config.DatabaseConfig{
		URL:               "postgres://postgres:postgres@localhost:5433/paysplit?sslmode=disable",
		ApplicationName:   "paysplit-api-test",
		MaxConns:          8,
		MinConns:          2,
		MaxConnLifetime:   time.Hour,
		MaxConnIdleTime:   15 * time.Minute,
		HealthCheckPeriod: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConns != 2 || cfg.MinConns != 0 {
		t.Fatalf("listener pool max=%d min=%d, want max 2 min 0", cfg.MaxConns, cfg.MinConns)
	}
}
