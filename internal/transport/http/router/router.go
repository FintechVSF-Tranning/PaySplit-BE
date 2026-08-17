package router

import (
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"

	"paysplit-backend/internal/config"
	helpers "paysplit-backend/internal/transport/http/helpers"
	middleware "paysplit-backend/internal/transport/http/middleware"
)

// New tạo router gốc của ứng dụng, cài đặt middleware dùng chung và trả về
// chi.Router để bootstrap có thể đăng ký route của từng module trước khi chạy server.
func New(appConfig config.AppConfig) chi.Router {
	router := chi.NewRouter()

	// RequestID thêm mã định danh vào context để theo dõi request xuyên suốt hệ thống.
	// ClientIPFromRemoteAddr chỉ tin địa chỉ TCP trực tiếp, không tin các forwarding
	// header có thể bị giả mạo. Dùng middleware.GetClientIP khi tạo khóa rate limit.
	router.Use(
		chiMiddleware.RequestID,
		chiMiddleware.ClientIPFromRemoteAddr,
		chiMiddleware.Logger,
		chiMiddleware.Recoverer,
		middleware.CORS(appConfig.CORSAllowedOrigins...),
		middleware.RateLimit(appConfig.RateLimitRequestsPerMinute, time.Minute),
		middleware.Timeout(appConfig.RequestTimeout),
	)

	router.Get("/", root)
	router.Get("/health", health)

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

func writeJSON(w http.ResponseWriter, status int, data any) {
	if err := helpers.WriteJSON(w, status, data); err != nil {
		log.Printf("failed to write router response: %v", err)
	}
}
