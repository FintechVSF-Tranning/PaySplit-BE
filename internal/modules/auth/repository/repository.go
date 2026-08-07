package repository

import (
	"context"

	"paysplit-backend/internal/modules/auth/domain"
)

type Repository interface {
	Create(ctx context.Context, user *domain.User) error
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
}
