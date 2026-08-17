package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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

func NewService(repo repository.Repository, pushNotifier PushNotifier) *Service {
	return &Service{
		repo:         repo,
		pushNotifier: pushNotifier,
	}
}

// SetEnqueuer thiết lập Queue Enqueuer cho Service
func (s *Service) SetEnqueuer(enqueuer JobEnqueuer) {
	s.enqueuer = enqueuer
}

// SendToUser thực hiện 2 việc:
// 1. Lưu thông báo vào Database (In-App notification)
// 2. Đẩy job gửi Push Notification vào River Queue (hoặc fallback goroutine)
func (s *Service) SendToUser(ctx context.Context, userID string, msg fcm.PushMessage) error {
	if userID == "" {
		return errors.New("user ID must not be empty")
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
		if err := s.enqueuer.EnqueueNotification(ctx, userID, msg); err == nil {
			return nil
		}
	}

	// Fallback: Nếu chưa bật queue thì chạy ngầm qua goroutine như cũ
	if s.pushNotifier != nil {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = s.ProcessNotificationJob(bgCtx, userID, msg)
		}()
	}

	return nil
}

// ProcessNotificationJob xử lý công việc gửi push notification từ River Worker
func (s *Service) ProcessNotificationJob(ctx context.Context, userID string, msg fcm.PushMessage) error {
	if s.pushNotifier == nil || userID == "" {
		return nil
	}

	token, err := s.repo.GetActiveFCMTokenByUserID(ctx, userID)
	if err != nil || token == "" {
		return nil
	}

	if sendErr := s.pushNotifier.SendToDevice(ctx, token, msg); sendErr != nil {
		// Nếu token không còn hợp lệ (user gỡ app) -> Xóa khỏi DB và kết thúc job
		if fcm.IsInvalidTokenError(sendErr) {
			_ = s.repo.ClearFCMToken(ctx, token)
			return nil
		}
		// Trả về lỗi để River Queue tự động retry với exponential backoff
		return sendErr
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

// UpdateFCMToken cập nhật FCM Token của thiết bị cho phiên đăng nhập hiện tại
func (s *Service) UpdateFCMToken(ctx context.Context, sessionID, fcmToken string) error {
	if sessionID == "" {
		return errors.New("session ID must not be empty")
	}
	if fcmToken == "" {
		return errors.New("fcm token must not be empty")
	}

	return s.repo.UpdateSessionFCMToken(ctx, sessionID, fcmToken)
}

// ListNotifications lấy danh sách thông báo của user có phân trang
func (s *Service) ListNotifications(ctx context.Context, userID string, pager pagination.OffsetPager) (pagination.Page[domain.Notification], error) {
	if userID == "" {
		return pagination.Page[domain.Notification]{}, errors.New("user ID must not be empty")
	}
	return s.repo.ListByUserID(ctx, userID, pager)
}

// GetUnreadCount đếm số lượng thông báo chưa đọc của user
func (s *Service) GetUnreadCount(ctx context.Context, userID string) (int64, error) {
	if userID == "" {
		return 0, errors.New("user ID must not be empty")
	}
	return s.repo.CountUnread(ctx, userID)
}

// MarkAsRead đánh dấu 1 thông báo cụ thể là đã đọc
func (s *Service) MarkAsRead(ctx context.Context, userID, notificationID string) error {
	if userID == "" || notificationID == "" {
		return errors.New("user ID and notification ID must not be empty")
	}
	return s.repo.MarkAsRead(ctx, userID, notificationID)
}

// MarkAllAsRead đánh dấu tất cả thông báo của user là đã đọc
func (s *Service) MarkAllAsRead(ctx context.Context, userID string) error {
	if userID == "" {
		return errors.New("user ID must not be empty")
	}
	return s.repo.MarkAllAsRead(ctx, userID)
}
