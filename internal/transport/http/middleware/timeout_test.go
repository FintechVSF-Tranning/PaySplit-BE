package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTimeout_AcceptHeaderAlone_DoesNotBypassTimeout(t *testing.T) {
	// covers: a client must not be able to escape the request timeout on an arbitrary route
	// (e.g. bill create/finalize) merely by sending Accept: text/event-stream.
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	})

	handler := Timeout(20 * time.Millisecond)(slow)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/bills", nil)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected the timeout to still apply (503) despite the Accept header, got %d", rec.Code)
	}
}

func TestTimeout_EventsRoute_BypassesTimeout(t *testing.T) {
	// covers: the legitimate SSE exemption (keyed on the route, not a client-controlled header) still works.
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	handler := Timeout(10 * time.Millisecond)(slow)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/bills/abc/events", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected the /events route to bypass the timeout and reach the handler, got %d", rec.Code)
	}
}
