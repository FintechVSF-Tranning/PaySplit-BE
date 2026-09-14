package router

import (
	"context"
	"encoding/json"
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
		r := New(appCfg, metricsCfg, Dependency{Name: "database", Checker: &mockDBChecker{}})
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("GET /health/live returns 200 ok", func(t *testing.T) {
		r := New(appCfg, metricsCfg, Dependency{Name: "database", Checker: &mockDBChecker{}})
		req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("GET /health/ready returns 200 ready when DB healthy", func(t *testing.T) {
		r := New(appCfg, metricsCfg, Dependency{Name: "database", Checker: &mockDBChecker{}})
		req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("GET /health/ready returns 503 degraded when DB down", func(t *testing.T) {
		r := New(appCfg, metricsCfg, Dependency{Name: "database", Checker: &mockDBChecker{pingErr: errors.New("db down")}})
		req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", rec.Code)
		}
	})

	// Redis nằm trên đường đi của mọi request có xác thực: một instance mất Redis
	// chỉ trả 401 chứ không báo lỗi, nên nó phải bị rút khỏi load balancer. Probe
	// cũng phải gọi tên thành phần hỏng — "degraded" trống rỗng không nói cho
	// người trực biết nên nhìn Postgres hay Redis.
	t.Run("GET /health/ready names the failing dependency", func(t *testing.T) {
		r := New(appCfg, metricsCfg,
			Dependency{Name: "database", Checker: &mockDBChecker{}},
			Dependency{Name: "redis", Checker: &mockDBChecker{pingErr: errors.New("redis down")}},
		)
		req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 when Redis is down, got %d", rec.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["status"] != "degraded" || body["redis"] != "down" || body["database"] != "ok" {
			t.Fatalf("unexpected readiness body: %v", body)
		}
	})

	// Cả hai cùng chết thì phải thấy cả hai trong một lần gọi, thay vì sửa xong
	// Postgres mới phát hiện Redis cũng hỏng.
	t.Run("GET /health/ready probes every dependency", func(t *testing.T) {
		r := New(appCfg, metricsCfg,
			Dependency{Name: "database", Checker: &mockDBChecker{pingErr: errors.New("db down")}},
			Dependency{Name: "redis", Checker: &mockDBChecker{pingErr: errors.New("redis down")}},
		)
		req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["database"] != "down" || body["redis"] != "down" {
			t.Fatalf("expected both dependencies reported down, got %v", body)
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
		r := New(appCfg, config.MetricsConfig{Enabled: true}, Dependency{Name: "database", Checker: &mockDBChecker{}})
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("GET /metrics returns 401 when token required and missing", func(t *testing.T) {
		r := New(appCfg, config.MetricsConfig{Enabled: true, BearerToken: "secret123"}, Dependency{Name: "database", Checker: &mockDBChecker{}})
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("GET /metrics returns 200 when token required and matches", func(t *testing.T) {
		r := New(appCfg, config.MetricsConfig{Enabled: true, BearerToken: "secret123"}, Dependency{Name: "database", Checker: &mockDBChecker{}})
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Header.Set("Authorization", "Bearer secret123")
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})
}
