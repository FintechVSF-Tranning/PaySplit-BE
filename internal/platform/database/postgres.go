package database

import (
	"context"

	"paysplit-backend/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

func ParsePoolConfig(cfg config.DatabaseConfig) (*pgxpool.Config, error) {
	panic("TODO: implement database.ParsePoolConfig")
}

func NewPostgresPool(ctx context.Context, cfg config.DatabaseConfig) (*pgxpool.Pool, error) {
	panic("TODO: implement database.NewPostgresPool")
}
