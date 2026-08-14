package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/config"
	authhttp "paysplit-backend/internal/modules/auth/delivery/http"
	authpostgres "paysplit-backend/internal/modules/auth/repository/postgres"
	"paysplit-backend/internal/modules/auth/usecase"
	"paysplit-backend/internal/platform/auth/jwt"
	"paysplit-backend/internal/platform/database"
	"paysplit-backend/internal/platform/security/password"
	"paysplit-backend/internal/transport/http/router"
)

type App struct {
	server *http.Server
	db     *pgxpool.Pool
}

// New loads application configuration and creates the shared infrastructure.
// Authentication routes are mounted later, when the auth service is available.
func New(ctx context.Context) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	db, err := database.NewPostgresPool(ctx, cfg.Database)
	if err != nil {
		return nil, err
	}

	tokens, err := jwt.NewIssuer(cfg.Auth.JWTSecret, cfg.Auth.JWTIssuer, cfg.Auth.AccessTokenTTL)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create JWT issuer: %w", err)
	}
	authService := usecase.NewService(authpostgres.New(db), password.New(), tokens)
	authHandler := authhttp.NewHandler(authService)

	return &App{
		db: db,
		server: &http.Server{
			Addr:    cfg.App.Address,
			Handler: router.New(authHandler, cfg.App),
		},
	}, nil
}

func (a *App) Address() string {
	return a.server.Addr
}

func (a *App) Start() error {
	if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
	if err := a.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	a.db.Close()
	return nil
}
