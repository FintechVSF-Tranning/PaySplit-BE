package usecase

import (
	"context"

	"paysplit-backend/internal/modules/auth/domain"
	"paysplit-backend/internal/modules/auth/repository"
)

type PasswordManager interface {
	Hash(plain string) (string, error)
	Compare(hash, plain string) error
}

type TokenIssuer interface {
	Issue(userID int64) (token string, expiresIn int64, err error)
}

type Service struct {
	repo      repository.Repository
	passwords PasswordManager
	tokens    TokenIssuer
}

type RegisterInput struct {
	Email    string
	Name     string
	Password string
}

type LoginInput struct {
	Email    string
	Password string
}

type AuthOutput struct {
	User        *domain.User
	AccessToken string
	TokenType   string
	ExpiresIn   int64
}

func NewService(repo repository.Repository, passwords PasswordManager, tokens TokenIssuer) *Service {
	panic("TODO: implement usecase.NewService")
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (*AuthOutput, error) {
	panic("TODO: implement Service.Register")
}

func (s *Service) Login(ctx context.Context, input LoginInput) (*AuthOutput, error) {
	panic("TODO: implement Service.Login")
}
