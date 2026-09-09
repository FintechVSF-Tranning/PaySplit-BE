package router

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"

	"paysplit-backend/internal/config"
	platformmetrics "paysplit-backend/internal/platform/metrics"
	helpers "paysplit-backend/internal/transport/http/helpers"
	middleware "paysplit-backend/internal/transport/http/middleware"
	"paysplit-backend/web"
)

// DBPingChecker kiểm tra kết nối tới một dependency cho readiness probe.
type DBPingChecker interface {
	Ping(ctx context.Context) error
}

// Dependency gắn tên vào một probe để readiness nói rõ THÀNH PHẦN NÀO hỏng.
// Không có tên thì "degraded" là một tín hiệu vô dụng khi có nhiều dependency:
// người trực không biết nên nhìn Postgres hay Redis.
type Dependency struct {
	Name    string
	Checker DBPingChecker
}

// New tạo router gốc của ứng dụng, cài đặt middleware dùng chung và trả về
// chi.Router để bootstrap có thể đăng ký route của từng module trước khi chạy server.
func New(appConfig config.AppConfig, metricsConfig config.MetricsConfig, deps ...Dependency) chi.Router {
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
	router.Get("/health/ready", healthReady(deps))

	router.Method(http.MethodGet, "/metrics", platformmetrics.MetricsHandler(metricsConfig.Enabled, metricsConfig.BearerToken))

	// Phục vụ giao diện web Admin Portal từ embedded assets
	if adminSubFS, err := fs.Sub(web.AdminFS, "admin"); err == nil {
		fileServer := http.FileServer(http.FS(adminSubFS))
		router.Get("/admin-portal", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin-portal/", http.StatusPermanentRedirect)
		})
		router.Handle("/admin-portal/*", http.StripPrefix("/admin-portal", fileServer))
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

func healthReady(deps []Dependency) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		// Probe hết mọi dependency chứ không dừng ở cái hỏng đầu tiên: khi cả
		// Postgres lẫn Redis cùng chết, người trực cần thấy cả hai trong một lần
		// gọi thay vì sửa xong cái này mới phát hiện cái kia.
		body := map[string]string{"status": "ready"}
		healthy := true
		for _, dep := range deps {
			if dep.Checker == nil || dep.Name == "" {
				continue
			}
			if err := dep.Checker.Ping(ctx); err != nil {
				body[dep.Name] = "down"
				healthy = false
				continue
			}
			body[dep.Name] = "ok"
		}

		if !healthy {
			body["status"] = "degraded"
			writeJSON(w, http.StatusServiceUnavailable, body)
			return
		}
		writeJSON(w, http.StatusOK, body)
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
