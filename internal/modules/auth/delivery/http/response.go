package http

import "time"

// userResponse là phiên bản an toàn của domain.User dành cho API và không chứa
// password hash.
type userResponse struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

// authResponse kết hợp thông tin người dùng an toàn với access token được cấp.
type authResponse struct {
	User        userResponse `json:"user"`
	AccessToken string       `json:"access_token"`
	TokenType   string       `json:"token_type"`
	ExpiresIn   int64        `json:"expires_in"`
}
