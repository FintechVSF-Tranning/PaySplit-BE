package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"paysplit-backend/internal/config"
)

type mockDBChecker struct {
	pingErr error
}

func (m *mockDBChecker) Ping(ctx context.Context) error {
	return m.pingErr
}

func TestRouter_HealthProbes(t *testing.T) {
	appCfg := config.AppConfig{
		Address:                    ":8080",
		RequestTimeout:             15 * time.Second,
		CORSAllowedOrigins:         []string{"*"},
		RateLimitRequestsPerMinute: 60,
	}
	metricsCfg := config.MetricsConfig{
		Enabled:     true,
		BearerToken: "",
	}

	t.Run("GET /health returns 200 ok", func(t *testing.T) {
		r := New(appCfg, metricsCfg, &mockDBChecker{})
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("GET /health/live returns 200 ok", func(t *testing.T) {
		r := New(appCfg, metricsCfg, &mockDBChecker{})
		req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("GET /health/ready returns 200 ready when DB healthy", func(t *testing.T) {
		r := New(appCfg, metricsCfg, &mockDBChecker{pingErr: nil})
		req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("GET /health/ready returns 503 degraded when DB down", func(t *testing.T) {
		r := New(appCfg, metricsCfg, &mockDBChecker{pingErr: errors.New("db down")})
		req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", rec.Code)
		}
	})
}

func TestRouter_Metrics(t *testing.T) {
	appCfg := config.AppConfig{
		Address:                    ":8080",
		RequestTimeout:             15 * time.Second,
		CORSAllowedOrigins:         []string{"*"},
		RateLimitRequestsPerMinute: 60,
	}

	t.Run("GET /metrics returns 200 when enabled without token", func(t *testing.T) {
		r := New(appCfg, config.MetricsConfig{Enabled: true}, &mockDBChecker{})
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("GET /metrics returns 401 when token required and missing", func(t *testing.T) {
		r := New(appCfg, config.MetricsConfig{Enabled: true, BearerToken: "secret123"}, &mockDBChecker{})
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("GET /metrics returns 200 when token required and matches", func(t *testing.T) {
		r := New(appCfg, config.MetricsConfig{Enabled: true, BearerToken: "secret123"}, &mockDBChecker{})
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Header.Set("Authorization", "Bearer secret123")
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})
}
