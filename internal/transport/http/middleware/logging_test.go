package middleware

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

func TestRedactPath_RedactsInviteCodeSegment(t *testing.T) {
	got := redactPath("/api/v1/groups/invites/sRmnxouQ3BHuu6lm_CdypDv9NTyl5JtG5BCFECJpsAI")
	want := "/api/v1/groups/invites/[REDACTED]"
	if got != want {
		t.Fatalf("redactPath() = %q, want %q", got, want)
	}
}

func TestRedactPath_LeavesUnrelatedPathsUntouched(t *testing.T) {
	for _, path := range []string{
		"/health",
		"/api/v1/groups",
		"/api/v1/groups/018f0000-0000-7000-8000-000000000001",
		"/api/v1/groups/018f0000-0000-7000-8000-000000000001/invites",
		"/api/v1/auth/sign-in",
	} {
		if got := redactPath(path); got != path {
			t.Fatalf("redactPath(%q) = %q, want it unchanged", path, got)
		}
	}
}

func TestRedactPath_RedactsEvenWhenTheCodeLooksLikeAnotherSegment(t *testing.T) {
	// A pathological code containing characters that could be mistaken for a
	// path separator must still be fully redacted up to the next real slash.
	got := redactPath("/api/v1/groups/invites/abc-DEF_123")
	if strings.Contains(got, "abc-DEF_123") {
		t.Fatalf("redactPath() = %q, still contains the raw code", got)
	}
	if got != "/api/v1/groups/invites/[REDACTED]" {
		t.Fatalf("redactPath() = %q, want the whole code segment replaced", got)
	}
}

func TestRequestLogger_NeverPrintsTheRawInviteCode(t *testing.T) {
	const code = "sRmnxouQ3BHuu6lm_CdypDv9NTyl5JtG5BCFECJpsAI"

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(nil) })

	handler := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups/invites/"+code, nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.RequestIDKey, "test-req-id"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	logged := buf.String()
	if strings.Contains(logged, code) {
		t.Fatalf("access log contains the raw invite code: %q", logged)
	}
	if !strings.Contains(logged, "[REDACTED]") {
		t.Fatalf("access log does not show the redacted placeholder: %q", logged)
	}
}

func TestRequestLogger_LogsUnrelatedPathsUnchanged(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(nil) })

	handler := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !strings.Contains(buf.String(), "/health") {
		t.Fatalf("access log does not contain the requested path: %q", buf.String())
	}
}
