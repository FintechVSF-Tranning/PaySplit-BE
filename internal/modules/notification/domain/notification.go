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

// PushMessage đại diện cho nội dung thông báo đẩy gửi tới thiết bị người dùng.
type PushMessage struct {
	Title string            `json:"title"`
	Body  string            `json:"body"`
	Data  map[string]string `json:"data,omitempty"`
}

const (
	TypePaymentReminder    = "payment_reminder"
	TypeNewBill            = "new_bill"
	TypePaymentConfirmed   = "payment_confirmed"
	TypePaymentRejected    = "payment_rejected"
	TypeGroupInvitation    = "group_invitation"
	TypeBillUpdated        = "bill_updated"
	TypeSystemAnnouncement = "system_announcement"
)
