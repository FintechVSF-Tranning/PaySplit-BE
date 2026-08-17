package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	chiMiddleware "github.com/go-chi/chi/v5/middleware"

	"paysplit-backend/internal/transport/http/helpers"
)

type rateLimitEntry struct {
	count       int
	windowStart time.Time
}

// RateLimit limits each client IP to limit requests within each fixed window.
// It is process-local and therefore appropriate for a single API instance.
func RateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	if limit <= 0 {
		panic("middleware: rate limit must be positive")
	}
	if window <= 0 {
		panic("middleware: rate limit window must be positive")
	}

	var mu sync.Mutex
	entries := make(map[string]rateLimitEntry)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := chiMiddleware.GetClientIP(r.Context())
			if clientIP == "" {
				clientIP = r.RemoteAddr
			}

			now := time.Now()
			mu.Lock()
			entry := entries[clientIP]
			if entry.windowStart.IsZero() || now.Sub(entry.windowStart) >= window {
				entry = rateLimitEntry{windowStart: now}
			}
			entry.count++
			entries[clientIP] = entry
			allowed := entry.count <= limit
			retryAfter := int((window - now.Sub(entry.windowStart)).Seconds())
			mu.Unlock()

			if !allowed {
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				_ = helpers.WriteAPIError(w, http.StatusTooManyRequests, "RATE_LIMITED", "rate limit exceeded", nil)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
