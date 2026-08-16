package repository

import (
	"context"

	"paysplit-backend/internal/modules/auth/domain"
)

// Repository là cổng lưu trữ mà auth usecase sử dụng. Interface này trao đổi
// domain.User để không làm lộ kiểu dữ liệu của PostgreSQL hoặc sqlc ra ngoài adapter.
type Repository interface {
	// Create lưu một người dùng mới.
	Create(ctx context.Context, user *domain.User) error
	// GetByEmail trả về người dùng theo email hoặc domain.ErrUserNotFound.
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
}
