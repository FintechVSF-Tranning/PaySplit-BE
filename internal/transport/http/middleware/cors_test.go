package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORS_PreflightAllowsIdempotencyKey(t *testing.T) {
	// Trình duyệt chỉ gửi request thật khi header nó định dùng nằm trong
	// Access-Control-Allow-Headers. Thiếu Idempotency-Key thì preflight vẫn trả
	// 204 nhưng DELETE/POST không bao giờ rời khỏi trình duyệt.
	handler := CORS("http://localhost:5173")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/bills/abc", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", http.MethodDelete)
	req.Header.Set("Access-Control-Request-Headers", "idempotency-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
	allowHeaders := strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers"))
	if !strings.Contains(allowHeaders, "idempotency-key") {
		t.Errorf("Allow-Headers = %q, thiếu Idempotency-Key", allowHeaders)
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), http.MethodDelete) {
		t.Errorf("Allow-Methods thiếu DELETE: %q", rec.Header().Get("Access-Control-Allow-Methods"))
	}
}

func TestCORS_PreflightAllowsAppVersionAndLocalhostOrigin(t *testing.T) {
	handler := CORS("http://localhost:5173")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/users/me/events", nil)
	req.Header.Set("Origin", "http://localhost:49650")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "authorization, x-app-version")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:49650" {
		t.Fatalf("Allow-Origin = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	allowHeaders := strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers"))
	if !strings.Contains(allowHeaders, "x-app-version") {
		t.Errorf("Allow-Headers = %q, missing X-App-Version", allowHeaders)
	}
}

func TestCORS_ExposesRetryAfter(t *testing.T) {
	handler := CORS("http://localhost:5173")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/bills", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	exposed := strings.ToLower(rec.Header().Get("Access-Control-Expose-Headers"))
	if !strings.Contains(exposed, "retry-after") {
		t.Errorf("Expose-Headers = %q, client sẽ không đọc được thời gian chờ sau 429", exposed)
	}
}
