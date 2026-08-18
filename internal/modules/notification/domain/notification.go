package domain

import (
	"encoding/json"
	"time"
)

// Notification đại diện cho một bản ghi thông báo trong cơ sở dữ liệu (In-App notification)
type Notification struct {
	ID        string          `json:"id"`
	UserID    string          `json:"user_id"`
	Type      string          `json:"type"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	ReadAt    *time.Time      `json:"read_at,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// IsRead kiểm tra xem thông báo đã được đọc hay chưa
func (n Notification) IsRead() bool {
	return n.ReadAt != nil
}
