package main

import (
	"context"
	"go-project/internal/env"
	"log"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx := context.Background()

	cfg := config{
		addr: ":8080",
		db: dpConfig{
			dsn: env.GetString("GOOSE_DBSTRING", "host=localhost dbname=ecom user=postgres password=postgres sslmode=disable"),
		},
	}

	// Chuyển định dạng logger đẹp hơn
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Database
	pool, err := pgxpool.New(ctx, cfg.db.dsn)
	if err != nil {
		panic(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		panic(err)
	}
	defer pool.Close() // giống finally, sẽ chạy khi hàm main end

	logger.Info("Connected to database", "dsn", cfg.db.dsn)

	// Create Application & Routing
	api := application{
		config: cfg,
		db:     pool,
	}
	handler := api.mount()
	if err := api.run(handler); err != nil {
		log.Printf("Server failed to start: %s", err)
		os.Exit(1)
	}
}
