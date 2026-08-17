package repository

import (
	"context"

	"paysplit-backend/internal/modules/notification/domain"

	"github.com/brpaz/lib-go/pagination"
)

type Repository interface {
	// CreateNotification lưu một thông báo mới vào bảng notifications
	CreateNotification(ctx context.Context, notif *domain.Notification) error

	// ListByUserID lấy danh sách thông báo của user có phân trang
	ListByUserID(ctx context.Context, userID string, pager pagination.OffsetPager) (pagination.Page[domain.Notification], error)

	// CountUnread đếm số lượng thông báo chưa đọc của user
	CountUnread(ctx context.Context, userID string) (int64, error)

	// MarkAsRead đánh dấu một thông báo cụ thể là đã đọc
	MarkAsRead(ctx context.Context, userID, notificationID string) error

	// MarkAllAsRead đánh dấu tất cả thông báo của user là đã đọc
	MarkAllAsRead(ctx context.Context, userID string) error

	// GetActiveFCMTokenByUserID lấy FCM token của phiên đang hoạt động của user
	GetActiveFCMTokenByUserID(ctx context.Context, userID string) (string, error)

	// UpdateSessionFCMToken cập nhật FCM token cho session hiện tại
	UpdateSessionFCMToken(ctx context.Context, sessionID, fcmToken string) error

	// ClearFCMToken xóa FCM token chết khỏi database khi nhận diện token không còn hợp lệ
	ClearFCMToken(ctx context.Context, fcmToken string) error
}
