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

// RateLimitByAccountAndIP applies one shared fixed-window budget to route
// groups that are already behind authentication. Account and direct peer IP
// are independent dimensions and both are incremented for every attempt.
func RateLimitByAccountAndIP(limit int, window time.Duration) func(http.Handler) http.Handler {
	return rateLimitByAccountAndIP(limit, window, time.Now)
}

func rateLimitByAccountAndIP(limit int, window time.Duration, nowFunc func() time.Time) func(http.Handler) http.Handler {
	if limit <= 0 {
		panic("middleware: rate limit must be positive")
	}
	if window <= 0 {
		panic("middleware: rate limit window must be positive")
	}
	if nowFunc == nil {
		panic("middleware: rate limit clock must not be nil")
	}

	var mu sync.Mutex
	entries := make(map[string]rateLimitEntry)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := UserID(r.Context())
			if !ok || userID == "" {
				writeAuthError(w)
				return
			}
			clientIP := chiMiddleware.GetClientIP(r.Context())
			if clientIP == "" {
				clientIP = r.RemoteAddr
			}

			now := nowFunc().UTC()
			windowStart := now.Truncate(window)
			keys := [...]string{"account:" + userID, "ip:" + clientIP}

			mu.Lock()
			for key, entry := range entries {
				if entry.windowStart.Before(windowStart) {
					delete(entries, key)
				}
			}
			allowed := true
			for _, key := range keys {
				entry := entries[key]
				if !entry.windowStart.Equal(windowStart) {
					entry = rateLimitEntry{windowStart: windowStart}
				}
				entry.count++
				entries[key] = entry
				if entry.count > limit {
					allowed = false
				}
			}
			retryDuration := windowStart.Add(window).Sub(now)
			mu.Unlock()

			if !allowed {
				retryAfter := int((retryDuration + time.Second - 1) / time.Second)
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
