package jwt

import (
	"errors"
	"fmt"
	"strings"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// AccessTokenManager tạo và xác thực access token JWT bằng thuật toán HS256.
// Nó triển khai cổng phát hành token của auth usecase và cổng xác thực token của
// HTTP middleware; quyết định đăng nhập vẫn thuộc về usecase.
type AccessTokenManager struct {
	secret []byte        // Khóa bí mật dùng để ký và xác thực chữ ký.
	issuer string        // Đơn vị phát hành token, được lưu trong claim "iss".
	ttl    time.Duration // Thời gian token có hiệu lực kể từ lúc phát hành.
}

// Các role hợp lệ có thể được ghi vào access token.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// claims định nghĩa dữ liệu mà PaySplit lưu trong payload của JWT.
// RegisteredClaims cung cấp các claim tiêu chuẩn như iss, sub, iat và exp.
type claims struct {
	Role      string `json:"role"`
	SessionID string `json:"sid"`
	jwtv5.RegisteredClaims
}

// NewAccessTokenManager kiểm tra cấu hình và tạo một AccessTokenManager dùng
// để phát hành cũng như xác thực access token HS256.
func NewAccessTokenManager(secret, issuer string, ttl time.Duration) (*AccessTokenManager, error) {
	secret = strings.TrimSpace(secret)
	issuer = strings.TrimSpace(issuer)
	if secret == "" {
		return nil, errors.New("JWT secret must not be empty")
	}
	if issuer == "" {
		return nil, errors.New("JWT issuer must not be empty")
	}
	if ttl <= 0 {
		return nil, errors.New("JWT access token TTL must be positive")
	}

	return &AccessTokenManager{secret: []byte(secret), issuer: issuer, ttl: ttl}, nil
}

// Issue phát hành access token có role mặc định là user và trả về token cùng
// thời gian hiệu lực tính bằng giây.
// Issue phát hành access token chứa ID người dùng, role và ID phiên đăng nhập.
func (i *AccessTokenManager) Issue(userID, role, sessionID string) (string, time.Time, error) {
	if strings.TrimSpace(userID) == "" {
		return "", time.Time{}, errors.New("user ID must not be empty")
	}
	if !validRole(role) {
		return "", time.Time{}, errors.New("invalid user role")
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", time.Time{}, errors.New("session ID must not be empty")
	}

	now := time.Now()
	expiresAt := now.Add(i.ttl)
	token := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims{
		Role:      role,
		SessionID: sessionID,
		RegisteredClaims: jwtv5.RegisteredClaims{
			Issuer:    i.issuer,
			Subject:   userID,
			IssuedAt:  jwtv5.NewNumericDate(now),
			ExpiresAt: jwtv5.NewNumericDate(expiresAt),
		},
	})

	signed, err := token.SignedString(i.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, expiresAt, nil
}

// Verify kiểm tra chữ ký, thuật toán, đơn vị phát hành, hạn sử dụng và dữ liệu
// nghiệp vụ của token; nếu hợp lệ, hàm trả về ID cùng role của người dùng.
func (i *AccessTokenManager) Verify(token string) (string, string, string, error) {
	parsedClaims := &claims{}
	parsed, err := jwtv5.ParseWithClaims(
		token,
		parsedClaims,
		func(token *jwtv5.Token) (any, error) {
			if token.Method.Alg() != jwtv5.SigningMethodHS256.Alg() {
				return nil, errors.New("unexpected signing method")
			}
			return i.secret, nil
		},
		jwtv5.WithValidMethods([]string{jwtv5.SigningMethodHS256.Alg()}),
		jwtv5.WithIssuer(i.issuer),
		jwtv5.WithExpirationRequired(),
	)
	if err != nil || parsed == nil || !parsed.Valid {
		return "", "", "", errors.New("invalid access token")
	}

	userID := strings.TrimSpace(parsedClaims.Subject)
	if userID == "" {
		return "", "", "", errors.New("invalid access token subject")
	}
	if !validRole(parsedClaims.Role) {
		return "", "", "", errors.New("invalid access token role")
	}
	sessionID := strings.TrimSpace(parsedClaims.SessionID)
	if sessionID == "" {
		return "", "", "", errors.New("invalid access token session")
	}
	return userID, parsedClaims.Role, sessionID, nil
}

func validRole(role string) bool {
	return role == RoleAdmin || role == RoleUser
}
