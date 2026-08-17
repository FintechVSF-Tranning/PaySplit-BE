package middleware

import (
	"log"
	"net/http"
	"regexp"
	"time"

	chiMiddleware "github.com/go-chi/chi/v5/middleware"
)

// sensitivePathPatterns match URL paths that embed a value that must never
// reach a log line, such as a group invite code (spec 0002 invariant 10).
var sensitivePathPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(/api/v1/groups/invites/)[^/?]+`),
}

func redactPath(path string) string {
	for _, re := range sensitivePathPatterns {
		path = re.ReplaceAllString(path, "${1}[REDACTED]")
	}
	return path
}

// RequestLogger mirrors chi's default access logger, except the path is
// redacted first so a route that embeds a sensitive value in its URL never
// prints that value.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chiMiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		defer func() {
			reqID := chiMiddleware.GetReqID(r.Context())
			log.Printf("[%s] \"%s %s %s\" from %s - %d %dB in %s", reqID, r.Method, redactPath(r.URL.Path), r.Proto, r.RemoteAddr, ww.Status(), ww.BytesWritten(), time.Since(start))
		}()
		next.ServeHTTP(ww, r)
	})
}
