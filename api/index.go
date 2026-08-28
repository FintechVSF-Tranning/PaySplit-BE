package api

import (
	"context"
	"log"
	"net/http"
	"sync"

	"paysplit-backend/internal/bootstrap"
	"paysplit-backend/internal/config"
)

var (
	handler http.Handler
	once    sync.Once
	initErr error
)

func initialize() {
	cfg, err := config.Load()
	if err != nil {
		initErr = err
		log.Printf("[Vercel] Error loading config: %v", err)
		return
	}

	app, err := bootstrap.NewServerless(context.Background(), cfg)
	if err != nil {
		initErr = err
		log.Printf("[Vercel] Error bootstrapping serverless app: %v", err)
		return
	}

	handler = app.Handler()
}

// Handler là entrypoint chính cho Vercel Serverless Functions trong region sin1 (Spec 0010 AC-1, AC-2)
func Handler(w http.ResponseWriter, r *http.Request) {
	once.Do(initialize)
	if initErr != nil {
		http.Error(w, "Serverless initialization failed: "+initErr.Error(), http.StatusInternalServerError)
		return
	}
	handler.ServeHTTP(w, r)
}
