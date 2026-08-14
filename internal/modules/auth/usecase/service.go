package usecase

import (
	"context"
	"errors"
	"strings"

	"paysplit-backend/internal/modules/auth/domain"
	"paysplit-backend/internal/modules/auth/repository"
)

type PasswordManager interface {
	Hash(plain string) (string, error)
	Compare(hash, plain string) error
}

type TokenIssuer interface {
	IssueWithRole(userID, role string) (token string, expiresIn int64, err error)
}

type Service struct {
	repo      repository.Repository
	passwords PasswordManager
	tokens    TokenIssuer
}

type RegisterInput struct {
	Email       string
	DisplayName string
	Password    string
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
	if repo == nil || passwords == nil || tokens == nil {
		panic("auth service dependencies must not be nil")
	}
	return &Service{repo: repo, passwords: passwords, tokens: tokens}
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (*AuthOutput, error) {
	panic("TODO: implement Service.Register")
}

func (s *Service) Login(ctx context.Context, input LoginInput) (*AuthOutput, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if email == "" || input.Password == "" {
		return nil, domain.ErrInvalidInput
	}

	user, err := s.repo.GetByEmail(ctx, email)
	if errors.Is(err, domain.ErrUserNotFound) {
		return nil, domain.ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if err := s.passwords.Compare(user.PasswordHash, input.Password); err != nil {
		return nil, domain.ErrInvalidCredentials
	}

	token, expiresIn, err := s.tokens.IssueWithRole(user.ID, user.Role)
	if err != nil {
		return nil, err
	}
	return &AuthOutput{
		User:        user,
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   expiresIn,
	}, nil
}
