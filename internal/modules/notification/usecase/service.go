package usecase

import (
	"context"
	"encoding/json"
	"fmt"

	"paysplit-backend/internal/modules/notification/domain"
	"paysplit-backend/internal/modules/notification/repository"
	"paysplit-backend/internal/platform/notification/fcm"

	"github.com/brpaz/lib-go/pagination"
)

type PushNotifier interface {
	SendToDevice(ctx context.Context, fcmToken string, msg fcm.PushMessage) error
	SendToAllUsers(ctx context.Context, msg fcm.PushMessage) error
}

// JobEnqueuer là interface đại diện cho hàng đợi bất đồng bộ (River Queue)
type JobEnqueuer interface {
	EnqueueNotification(ctx context.Context, userID string, msg fcm.PushMessage) error
}

type Service struct {
	repo         repository.Repository
	pushNotifier PushNotifier
	enqueuer     JobEnqueuer
}

func NewService(repo repository.Repository, pushNotifier PushNotifier, enqueuer JobEnqueuer) *Service {
	if repo == nil {
		panic("notification repository must not be nil")
	}
	return &Service{
		repo:         repo,
		pushNotifier: pushNotifier,
		enqueuer:     enqueuer,
	}
}

// SendToUser thực hiện 2 việc:
// 1. Lưu thông báo vào Database (In-App notification)
// 2. Đẩy job gửi Push Notification vào River Queue (hoặc gửi trực tiếp nếu không có queue)
func (s *Service) SendToUser(ctx context.Context, userID string, msg fcm.PushMessage) error {
	if userID == "" {
		return domain.ErrInvalidInput
	}

	// 1. Lưu bản ghi vào bảng notifications
	var rawPayload json.RawMessage
	if len(msg.Data) > 0 {
		b, err := json.Marshal(msg.Data)
		if err == nil {
			rawPayload = b
		}
	}

	notifType := msg.Data["type"]
	if notifType == "" {
		notifType = fcm.TypeSystemAnnouncement
	}

	notif := &domain.Notification{
		UserID:  userID,
		Type:    notifType,
		Title:   msg.Title,
		Body:    msg.Body,
		Payload: rawPayload,
	}

	if err := s.repo.CreateNotification(ctx, notif); err != nil {
		return fmt.Errorf("create in-app notification: %w", err)
	}

	// 2. Đẩy job gửi Push Notification vào Queue (được đảm bảo độ tin cậy và retry)
	if s.enqueuer != nil {
		if err := s.enqueuer.EnqueueNotification(ctx, userID, msg); err != nil {
			return fmt.Errorf("enqueue notification: %w", err)
		}
		return nil
	}

	// Fallback gửi trực tiếp nếu enqueuer không được cấu hình (ví dụ trong môi trường test)
	if s.pushNotifier != nil {
		token, err := s.repo.GetActiveFCMTokenByUserID(ctx, userID)
		if err == nil && token != "" {
			_ = s.pushNotifier.SendToDevice(ctx, token, msg)
		}
	}

	return nil
}

// SendToAllUsers gửi thông báo hệ thống tới toàn bộ người dùng đã cài app
func (s *Service) SendToAllUsers(ctx context.Context, msg fcm.PushMessage) error {
	if s.pushNotifier == nil {
		return nil
	}
	return s.pushNotifier.SendToAllUsers(ctx, msg)
}

// ListNotifications lấy danh sách thông báo của user có phân trang
func (s *Service) ListNotifications(ctx context.Context, userID string, pager pagination.OffsetPager) (pagination.Page[domain.Notification], error) {
	if userID == "" {
		return pagination.Page[domain.Notification]{}, domain.ErrInvalidInput
	}
	return s.repo.ListByUserID(ctx, userID, pager)
}

// GetUnreadCount đếm số lượng thông báo chưa đọc của user
func (s *Service) GetUnreadCount(ctx context.Context, userID string) (int64, error) {
	if userID == "" {
		return 0, domain.ErrInvalidInput
	}
	return s.repo.CountUnread(ctx, userID)
}

// MarkAsRead đánh dấu 1 thông báo cụ thể là đã đọc
func (s *Service) MarkAsRead(ctx context.Context, userID, notificationID string) error {
	if userID == "" || notificationID == "" {
		return domain.ErrInvalidInput
	}
	return s.repo.MarkAsRead(ctx, userID, notificationID)
}

// MarkAllAsRead đánh dấu tất cả thông báo của user là đã đọc
func (s *Service) MarkAllAsRead(ctx context.Context, userID string) error {
	if userID == "" {
		return domain.ErrInvalidInput
	}
	return s.repo.MarkAllAsRead(ctx, userID)
}
