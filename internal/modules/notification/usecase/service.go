package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"paysplit-backend/internal/modules/notification/domain"
	"paysplit-backend/internal/modules/notification/repository"
	"paysplit-backend/internal/platform/notification/fcm"

	"github.com/brpaz/lib-go/pagination"
)

// Độ dài tối đa cho phép của các cột notifications, phải khớp với CHECK constraint trong
// migration 000003 để tránh việc DB từ chối insert bằng một lỗi 500 khó hiểu ở tầng handler.
const (
	maxTypeLength  = 60
	maxTitleLength = 255
	maxBodyLength  = 1000
)

type PushNotifier interface {
	SendToDevice(ctx context.Context, fcmToken string, msg fcm.PushMessage) error
	SendToAllUsers(ctx context.Context, msg fcm.PushMessage) error
}

// JobEnqueuer là interface đại diện cho hàng đợi bất đồng bộ (River Queue). Việc enqueue luôn
// tham gia vào transaction ex cùng với CreateNotificationTx để đảm bảo record in-app và job
// push được ghi nhận nguyên tử (atomic): hoặc cả hai cùng thành công, hoặc cả hai cùng rollback.
type JobEnqueuer interface {
	EnqueueNotificationTx(ctx context.Context, ex repository.Executor, notificationID string) error
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

	title := strings.TrimSpace(msg.Title)
	body := strings.TrimSpace(msg.Body)
	if title == "" || body == "" {
		return domain.ErrInvalidInput
	}

	notifType := strings.TrimSpace(msg.Data["type"])
	if notifType == "" {
		notifType = fcm.TypeSystemAnnouncement
	}

	// Cắt bớt về giới hạn CHECK constraint của DB thay vì để insert thất bại bằng lỗi 500,
	// vì nội dung (vd: rejection_reason do người dùng nhập) có thể vượt giới hạn cho phép.
	notifType = truncate(notifType, maxTypeLength)
	title = truncate(title, maxTitleLength)
	body = truncate(body, maxBodyLength)

	var rawPayload json.RawMessage
	if len(msg.Data) > 0 {
		b, err := json.Marshal(msg.Data)
		if err != nil {
			return fmt.Errorf("marshal notification payload: %w", err)
		}
		rawPayload = b
	}

	notif := &domain.Notification{
		UserID:  userID,
		Type:    notifType,
		Title:   title,
		Body:    body,
		Payload: rawPayload,
	}

	// 1 + 2. Lưu bản ghi in-app và enqueue job push trong cùng 1 transaction, để không bao giờ
	// có bản ghi mồ côi (record được lưu nhưng job enqueue thất bại) hay job trùng lặp khi caller
	// tự retry sau một lỗi giữa chừng.
	if s.enqueuer != nil {
		return s.repo.WithTx(ctx, func(ctx context.Context, ex repository.Executor) error {
			if err := s.repo.CreateNotificationTx(ctx, ex, notif); err != nil {
				return fmt.Errorf("create in-app notification: %w", err)
			}
			if err := s.enqueuer.EnqueueNotificationTx(ctx, ex, notif.ID); err != nil {
				return fmt.Errorf("enqueue notification: %w", err)
			}
			return nil
		})
	}

	if err := s.repo.CreateNotification(ctx, notif); err != nil {
		return fmt.Errorf("create in-app notification: %w", err)
	}

	// Fallback gửi trực tiếp nếu enqueuer không được cấu hình (ví dụ trong môi trường test)
	if s.pushNotifier != nil {
		token, err := s.repo.GetActiveFCMTokenByUserID(ctx, userID)
		if err != nil {
			log.Printf("event=fcm_token_lookup_failed user_id=%s err=%v", userID, err)
		} else if token != "" {
			if sendErr := s.pushNotifier.SendToDevice(ctx, token, msg); sendErr != nil {
				if fcm.IsInvalidTokenError(sendErr) {
					_ = s.repo.ClearFCMToken(ctx, userID, token)
				} else {
					log.Printf("event=fcm_send_failed user_id=%s err=%v", userID, sendErr)
				}
			}
		}
	}

	return nil
}

// truncate cắt s về tối đa max ký tự (rune, không phải byte, để tương thích với char_length
// trong CHECK constraint của Postgres), thêm dấu "..." nếu bị cắt.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	const ellipsis = "..."
	if max <= len(ellipsis) {
		return string(r[:max])
	}
	return string(r[:max-len(ellipsis)]) + ellipsis
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
