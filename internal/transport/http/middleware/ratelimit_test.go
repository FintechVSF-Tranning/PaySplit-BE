package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chiMiddleware "github.com/go-chi/chi/v5/middleware"
)

func TestRateLimit_SkipsProbeEndpoints(t *testing.T) {
	// Một agent giám sát poll /health không được phép tiêu hết ngân sách của
	// người dùng thật đứng sau cùng địa chỉ IP.
	limiter := RateLimit(1, time.Minute)
	handler := limiter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	for _, path := range []string{"/health", "/health/live", "/health/ready", "/metrics", "/"} {
		for i := 0; i < 5; i++ {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = "203.0.113.20:1234"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s lần %d = %d, want 200", path, i+1, rec.Code)
			}
		}
	}

	// Ngân sách của route thật vẫn còn nguyên sau chuỗi probe ở trên.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil)
	req.RemoteAddr = "203.0.113.20:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("route thật bị chặn oan: %d", rec.Code)
	}
}

func TestRateLimitByAccountAndIP_SharesBudgetAcrossInviteRoutes_AC12(t *testing.T) {
	limiter := RateLimitByAccountAndIP(2, time.Minute)
	preview := limiter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	join := limiter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	if status := runLimitedRequest(preview, "user-1", "203.0.113.10:1234", ""); status != http.StatusOK {
		t.Fatalf("preview status = %d, want 200", status)
	}
	if status := runLimitedRequest(join, "user-1", "203.0.113.10:1234", ""); status != http.StatusOK {
		t.Fatalf("join status = %d, want 200", status)
	}
	if status := runLimitedRequest(preview, "user-1", "203.0.113.10:1234", ""); status != http.StatusTooManyRequests {
		t.Fatalf("third shared attempt status = %d, want 429", status)
	}
}

func TestRateLimitByAccountAndIP_EnforcesAccountAndIPIndependently_AC12(t *testing.T) {
	accountLimiter := RateLimitByAccountAndIP(1, time.Minute)
	handler := accountLimiter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	if status := runLimitedRequest(handler, "same-account", "203.0.113.1:1000", ""); status != http.StatusOK {
		t.Fatalf("first account attempt status = %d, want 200", status)
	}
	if status := runLimitedRequest(handler, "same-account", "203.0.113.2:1000", ""); status != http.StatusTooManyRequests {
		t.Fatalf("same account from another IP status = %d, want 429", status)
	}

	ipLimiter := RateLimitByAccountAndIP(1, time.Minute)
	handler = ipLimiter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	if status := runLimitedRequest(handler, "account-a", "203.0.113.3:1000", ""); status != http.StatusOK {
		t.Fatalf("first IP attempt status = %d, want 200", status)
	}
	if status := runLimitedRequest(handler, "account-b", "203.0.113.3:1000", ""); status != http.StatusTooManyRequests {
		t.Fatalf("another account from same IP status = %d, want 429", status)
	}
}

func TestRateLimitByAccountAndIP_IgnoresForwardingHeaders_AC12(t *testing.T) {
	limiter := RateLimitByAccountAndIP(1, time.Minute)
	handler := limiter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	if status := runLimitedRequest(handler, "account-a", "203.0.113.9:1000", "198.51.100.1"); status != http.StatusOK {
		t.Fatalf("first direct peer attempt status = %d, want 200", status)
	}
	if status := runLimitedRequest(handler, "account-b", "203.0.113.9:1000", "198.51.100.2"); status != http.StatusTooManyRequests {
		t.Fatalf("spoofed forwarding header bypassed direct IP budget, status = %d", status)
	}
}

func TestRateLimitByAccountAndIP_ResetsAtTheNextUTCEpochMinute_AC12(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 34, 59, 500_000_000, time.UTC)
	limiter := rateLimitByAccountAndIP(1, time.Minute, func() time.Time { return now })
	handler := limiter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	if status := runLimitedRequest(handler, "user-1", "203.0.113.20:1000", ""); status != http.StatusOK {
		t.Fatalf("first attempt status = %d, want 200", status)
	}
	response := runLimitedRecorder(handler, "user-1", "203.0.113.20:1000", "")
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("limited response = %d Retry-After %q, want 429 and 1", response.Code, response.Header().Get("Retry-After"))
	}

	now = now.Add(time.Second)
	if status := runLimitedRequest(handler, "user-1", "203.0.113.20:1000", ""); status != http.StatusOK {
		t.Fatalf("first attempt in next epoch minute status = %d, want 200", status)
	}
}

func runLimitedRequest(handler http.Handler, userID, remoteAddr, forwardedFor string) int {
	return runLimitedRecorder(handler, userID, remoteAddr, forwardedFor).Code
}

func runLimitedRecorder(handler http.Handler, userID, remoteAddr, forwardedFor string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	if forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}
	ctx := WithAuthContext(context.Background(), userID, "session", "user")
	req = req.WithContext(ctx)
	response := httptest.NewRecorder()
	chiMiddleware.ClientIPFromRemoteAddr(handler).ServeHTTP(response, req)
	return response
}
