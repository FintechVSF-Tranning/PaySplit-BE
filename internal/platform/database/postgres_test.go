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
