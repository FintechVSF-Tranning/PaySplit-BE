package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"paysplit-backend/internal/bootstrap"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := bootstrap.New(ctx)
	if err != nil {
		log.Fatal(err)
	}

	errors := make(chan error, 1)
	go func() { errors <- app.Start() }()

	select {
	case err := <-errors:
		if err != nil {
			log.Fatal(err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	}
}
