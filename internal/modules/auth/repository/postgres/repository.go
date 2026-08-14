package postgres

import (
	"context"
	"errors"
	"fmt"

	"paysplit-backend/internal/modules/auth/domain"
	"paysplit-backend/internal/modules/auth/repository"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type postgresRepository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) repository.Repository {
	if pool == nil {
		panic("auth repository pool must not be nil")
	}
	return &postgresRepository{pool: pool}
}

func (r *postgresRepository) Create(ctx context.Context, user *domain.User) error {
	const query = `
		INSERT INTO users (id, email, display_name, password_hash)
		VALUES ($1, $2, $3, $4)`

	_, err := r.pool.Exec(ctx, query, user.ID, user.Email, user.DisplayName, user.PasswordHash)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

func (r *postgresRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	const query = `
		SELECT id, email, display_name, password_hash, role, created_at
		FROM users
		WHERE email = $1
		LIMIT 1`

	user := &domain.User{}
	err := r.pool.QueryRow(ctx, query, email).Scan(
		&user.ID,
		&user.Email,
		&user.DisplayName,
		&user.PasswordHash,
		&user.Role,
		&user.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return user, nil
}
