package middleware

import (
	"net/http"
	"time"
)

type timeoutResponseWriter struct {
	http.ResponseWriter
}

// WriteHeader ensures the standard library timeout response uses the same JSON
// content type as other API errors.
func (w timeoutResponseWriter) WriteHeader(status int) {
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}

	w.ResponseWriter.WriteHeader(status)
}

func (w timeoutResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Timeout limits how long a handler may process a request. When the deadline is
// reached, net/http cancels the request context and sends a JSON 503 response.
// Downstream operations must observe r.Context() for cancellation to release
// their resources promptly.
func Timeout(duration time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		const timeoutBody = `{"error":"request timeout"}` + "\n"
		timeoutHandler := http.TimeoutHandler(next, duration, timeoutBody)

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			timeoutHandler.ServeHTTP(timeoutResponseWriter{ResponseWriter: w}, r)
		})
	}
}
