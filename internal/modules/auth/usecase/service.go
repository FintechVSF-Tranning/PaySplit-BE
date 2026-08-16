package usecase

import (
	"context"
	"errors"
	"strings"

	"paysplit-backend/internal/modules/auth/domain"
	"paysplit-backend/internal/modules/auth/repository"
)

// PasswordManager định nghĩa các thao tác mật khẩu mà usecase cần, nhờ đó
// Service không phụ thuộc trực tiếp vào thư viện bcrypt cụ thể.
type PasswordManager interface {
	Hash(plain string) (string, error)
	Compare(hash, plain string) error
}

// TokenIssuer định nghĩa khả năng phát hành access token sau khi danh tính người
// dùng đã được xác thực.
type TokenIssuer interface {
	IssueWithRole(userID, role string) (token string, expiresIn int64, err error)
}

// Service điều phối các ca sử dụng đăng ký và đăng nhập. Nó chỉ phụ thuộc vào
// các interface, không biết chi tiết PostgreSQL, bcrypt hay JWT.
type Service struct {
	repo      repository.Repository
	passwords PasswordManager
	tokens    TokenIssuer
}

// RegisterInput chứa dữ liệu mà ca sử dụng đăng ký cần từ tầng delivery.
type RegisterInput struct {
	Email       string
	DisplayName string
	Password    string
}

// LoginInput chứa thông tin đăng nhập đã được tách khỏi chi tiết HTTP.
type LoginInput struct {
	Email    string
	Password string
}

// AuthOutput chứa kết quả xác thực để tầng delivery chuyển thành response an toàn.
type AuthOutput struct {
	User        *domain.User
	AccessToken string
	TokenType   string
	ExpiresIn   int64
}

// NewService tạo auth service từ các dependency do bootstrap cung cấp.
func NewService(repo repository.Repository, passwords PasswordManager, tokens TokenIssuer) *Service {
	if repo == nil || passwords == nil || tokens == nil {
		panic("auth service dependencies must not be nil")
	}
	return &Service{repo: repo, passwords: passwords, tokens: tokens}
}

// Register tiếp nhận dữ liệu đăng ký và sẽ điều phối việc kiểm tra, băm mật
// khẩu, lưu người dùng và phát hành access token.
func (s *Service) Register(ctx context.Context, input RegisterInput) (*AuthOutput, error) {
	panic("TODO: implement Service.Register")
}

// Login kiểm tra thông tin đăng nhập và phát hành access token cho người dùng hợp lệ.
func (s *Service) Login(ctx context.Context, input LoginInput) (*AuthOutput, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if email == "" || input.Password == "" {
		return nil, domain.ErrInvalidInput
	}

	user, err := s.repo.GetByEmail(ctx, email)
	// Không phân biệt email không tồn tại với mật khẩu sai để tránh làm lộ tài
	// khoản nào đã được đăng ký trong hệ thống.
	if errors.Is(err, domain.ErrUserNotFound) {
		return nil, domain.ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if err := s.passwords.Compare(user.PasswordHash, input.Password); err != nil {
		return nil, domain.ErrInvalidCredentials
	}

	// Chỉ phát hành token sau khi mật khẩu đã được xác thực thành công.
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
