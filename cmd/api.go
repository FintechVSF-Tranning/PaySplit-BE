package main

import (
	"context"
	"errors"
	repo "go-project/internal/adapters/postgresql/sqlc"
	"go-project/internal/products"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

// mount
func (app *application) mount() http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)              // important for rate limiting
	r.Use(middleware.ClientIPFromRemoteAddr) // rate limiting and analytics, tracing
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer) // recover form crashes

	// Set a timeout on the req contect (ctx), that will signal
	// through ctx.Done() that the req has timeout and further processing should be stopped
	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("root. Oke!"))
	})

	// Product routers
	productService := products.NewService(repo.New(app.db))
	productHandler := products.NewHandler(productService)
	r.Get("/products", productHandler.ListProducts)
	r.Get("/products/{id}", productHandler.FindProductByID)

	return r
}

// run
func (app *application) run(h http.Handler) error {
	srv := &http.Server{
		Addr:         app.config.addr,
		Handler:      h,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful Shutdown ======================================
	shutdownErr := make(chan error) // Tạo channel nhận kq shutdown server
	go func() {                     // Chạy luồng (Goroutine) chạy ngầm để lắn nghe tín hiệu từ os
		quit := make(chan os.Signal, 1)
		// Lắng nghe SIGINT (Ctrl+C) và SIGTERM (Docker stop)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

		<-quit // Luồng này bị block tại đây cho đến khi có tín hiệu tắt

		log.Println("Shutting down server...")

		// Cho server max = 5s để done các req còn lại
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Yêu cầu server tắt an toàn rồi gửi kq vào channel
		shutdownErr <- srv.Shutdown(ctx)
	}()

	log.Printf("Starting server on %s", app.config.addr)

	// Start server. Block here
	err := srv.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	// Đợt kết quả từ Graceful Shutdown
	err = <-shutdownErr
	if err != nil {
		return err
	}

	log.Println("Server gracefully stopped!!!")
	return nil
}

type application struct {
	config config
	db     *pgxpool.Pool
}
type config struct {
	addr string
	port int
	db   dpConfig
}
type dpConfig struct {
	dsn string // Data Source Name
}
