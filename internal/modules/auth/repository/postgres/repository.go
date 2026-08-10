package postgres

import (
	"context"

	"paysplit-backend/internal/modules/auth/domain"
	"paysplit-backend/internal/modules/auth/repository"
	"paysplit-backend/internal/modules/auth/repository/postgres/sqlc"

	"github.com/jackc/pgx/v5/pgxpool"
)

type postgresRepository struct {
	queries *sqlc.Queries
}

func New(pool *pgxpool.Pool) repository.Repository {
	panic("TODO: implement postgres.New")
}

func (r *postgresRepository) Create(ctx context.Context, user *domain.User) error {
	panic("TODO: implement postgresRepository.Create")
}

func (r *postgresRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	panic("TODO: implement postgresRepository.GetByEmail")
}
