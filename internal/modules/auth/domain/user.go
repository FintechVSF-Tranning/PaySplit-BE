package domain

import "time"

// User biểu diễn người dùng trong miền xác thực, độc lập với cách dữ liệu được
// lưu trong PostgreSQL hoặc trả về qua HTTP.
type User struct {
	ID          string
	Email       string
	DisplayName string
	PasswordHash string		// PasswordHash chỉ dùng nội bộ để xác thực và không được trả về qua API.
	Role         string
	CreatedAt    time.Time
}
