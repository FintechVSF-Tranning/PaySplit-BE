package http

import (
	"encoding/json"
	"time"

	"paysplit-backend/internal/modules/notification/domain"
)

type notificationResponse struct {
	ID        string          `json:"id"`
	UserID    string          `json:"user_id"`
	Type      string          `json:"type"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	ReadAt    *time.Time      `json:"read_at,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type unreadCountResponse struct {
	UnreadCount int64 `json:"unread_count"`
}

type messageResponse struct {
	Message string `json:"message"`
}

func toNotificationResponse(n domain.Notification) notificationResponse {
	return notificationResponse{
		ID:        n.ID,
		UserID:    n.UserID,
		Type:      n.Type,
		Title:     n.Title,
		Body:      n.Body,
		Payload:   n.Payload,
		ReadAt:    n.ReadAt,
		CreatedAt: n.CreatedAt,
	}
}
