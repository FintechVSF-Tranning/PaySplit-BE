package middleware

import (
	"net/http"
	"net/url"
	"strings"
)

// CORS permits browser requests only from the supplied origins. Requests with
// no Origin header (for example curl and server-to-server calls) pass through.
func CORS(allowedOrigins ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowed[origin] = struct{}{}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			if _, ok := allowed[origin]; !ok && !isLocalWebOrigin(origin) {
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			// Idempotency-Key phải nằm ở đây, nếu không trình duyệt cho preflight
			// đi qua rồi vẫn chặn request thật: xóa hóa đơn, tạo QR thanh toán,
			// nộp minh chứng và nhắc nợ đều gửi header này.
			// X-App-Version đi cùng user và legacy SSE trên Flutter web.
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID, Idempotency-Key, X-App-Version")
			// Retry-After là cách duy nhất client biết phải chờ bao lâu sau 429;
			// không expose thì JavaScript không đọc được nó.
			w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, Retry-After")
			w.Header().Set("Access-Control-Max-Age", "600")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isLocalWebOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "localhost" || host == "127.0.0.1"
}
