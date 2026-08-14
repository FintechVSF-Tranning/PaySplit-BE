package router

import (
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"

	"paysplit-backend/internal/config"
	authhttp "paysplit-backend/internal/modules/auth/delivery/http"
	helpers "paysplit-backend/internal/transport/http/helpers"
	middleware "paysplit-backend/internal/transport/http/middleware"
)

// New builds the application HTTP router, installs global middleware, and
// mounts each module under its versioned API prefix.
func New(authHandler *authhttp.Handler, appConfig config.AppConfig) http.Handler {
	router := chi.NewRouter()

	// RequestID makes one correlation ID available through the request context.
	// ClientIPFromRemoteAddr trusts only the TCP peer address and ignores
	// spoofable forwarding headers. Read it with middleware.GetClientIP when
	// building rate-limit or analytics keys.
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
	if authHandler != nil {
		router.Route("/api/v1/auth", authHandler.RegisterRoutes)
	}

	router.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "route not found")
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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

func writeError(w http.ResponseWriter, status int, message string) {
	if err := helpers.WriteError(w, status, message); err != nil {
		log.Printf("failed to write router error response: %v", err)
	}
}
