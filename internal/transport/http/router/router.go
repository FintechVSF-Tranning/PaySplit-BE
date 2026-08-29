package router

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"

	"paysplit-backend/internal/config"
	platformmetrics "paysplit-backend/internal/platform/metrics"
	helpers "paysplit-backend/internal/transport/http/helpers"
	middleware "paysplit-backend/internal/transport/http/middleware"
)

// DBPingChecker kiểm tra kết nối DB cho readiness probe.
type DBPingChecker interface {
	Ping(ctx context.Context) error
}

// New tạo router gốc của ứng dụng, cài đặt middleware dùng chung và trả về
// chi.Router để bootstrap có thể đăng ký route của từng module trước khi chạy server.
func New(appConfig config.AppConfig, metricsConfig config.MetricsConfig, dbChecker DBPingChecker) chi.Router {
	router := chi.NewRouter()

	// RequestID thêm mã định danh vào context để theo dõi request xuyên suốt hệ thống.
	// ClientIPFromRemoteAddr chỉ tin địa chỉ TCP trực tiếp, không tin các forwarding
	// header có thể bị giả mạo. Dùng middleware.GetClientIP khi tạo khóa rate limit.
	router.Use(
		chiMiddleware.RequestID,
		chiMiddleware.ClientIPFromRemoteAddr,
		platformmetrics.HTTPMetricsMiddleware,
		middleware.RequestLogger,
		chiMiddleware.Recoverer,
		middleware.CORS(appConfig.CORSAllowedOrigins...),
		middleware.RateLimit(appConfig.RateLimitRequestsPerMinute, time.Minute),
		middleware.Timeout(appConfig.RequestTimeout),
	)

	router.Get("/", root)
	router.Get("/health", health)
	router.Get("/health/live", healthLive)
	router.Get("/health/ready", healthReady(dbChecker))

	router.Method(http.MethodGet, "/metrics", platformmetrics.MetricsHandler(metricsConfig.Enabled, metricsConfig.BearerToken))

	// Phục vụ giao diện web Admin Portal từ thư mục web/admin nếu tồn tại
	adminWebDir := "./web/admin"
	if _, err := os.Stat(adminWebDir); err == nil {
		fs := http.FileServer(http.Dir(adminWebDir))
		router.Get("/admin-portal", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin-portal/", http.StatusPermanentRedirect)
		})
		router.Handle("/admin-portal/*", http.StripPrefix("/admin-portal", fs))
	}

	router.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		if err := helpers.WriteAPIError(w, http.StatusNotFound, "ROUTE_NOT_FOUND", "route not found", nil); err != nil {
			log.Printf("event=response_write_failed")
		}
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		if err := helpers.WriteAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil); err != nil {
			log.Printf("event=response_write_failed")
		}
	})

	return router
}

func root(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":    "PaySplit API",
		"status":  "running",
		"version": "0.1.0",
	})
}

func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func healthLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func healthReady(dbChecker DBPingChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dbChecker != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()

			if err := dbChecker.Ping(ctx); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{
					"status":   "degraded",
					"database": "down",
				})
				return
			}
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"status":   "ready",
			"database": "ok",
		})
	}
}

// writeJSON ghi response thô, không bọc envelope success/data/message: các
// endpoint hạ tầng (root, health probes) được miễn trừ vì mục đích của chúng
// là bị đọc bởi công cụ probe/monitoring bên ngoài, không phải client API.
func writeJSON(w http.ResponseWriter, status int, data any) {
	if err := helpers.WriteRawJSON(w, status, data); err != nil {
		log.Printf("failed to write router response: %v", err)
	}
}
