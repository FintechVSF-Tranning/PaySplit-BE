package repository

import (
	"context"

	"paysplit-backend/internal/modules/notification/domain"

	"github.com/brpaz/lib-go/pagination"
)

// Executor là một handle không định kiểu (opaque) đại diện cho một transaction database
// đang mở, được truyền qua ranh giới usecase mà không cần usecase import pgx trực tiếp.
// Adapter (repository/postgres) chịu trách nhiệm ép kiểu handle này về loại cụ thể của nó.
type Executor interface{}

type Repository interface {
	// CreateNotification lưu một thông báo mới vào bảng notifications
	CreateNotification(ctx context.Context, notif *domain.Notification) error

	// CreateNotificationTx giống CreateNotification nhưng tham gia vào transaction ex,
	// cho phép caller ghi nhận record cùng lúc với việc enqueue job trong 1 transaction duy nhất.
	CreateNotificationTx(ctx context.Context, ex Executor, notif *domain.Notification) error

	// WithTx mở một database transaction, chạy fn với Executor tương ứng, rồi commit nếu fn
	// thành công hoặc rollback nếu fn trả về lỗi.
	WithTx(ctx context.Context, fn func(ctx context.Context, ex Executor) error) error

	// GetNotificationByID lấy một thông báo theo ID, dùng bởi worker để nạp lại nội dung gửi push.
	GetNotificationByID(ctx context.Context, notificationID string) (domain.Notification, error)

	// ListByUserID lấy danh sách thông báo của user có phân trang
	ListByUserID(ctx context.Context, userID string, pager pagination.OffsetPager) (pagination.Page[domain.Notification], error)

	// CountUnread đếm số lượng thông báo chưa đọc của user
	CountUnread(ctx context.Context, userID string) (int64, error)

	// MarkAsRead đánh dấu một thông báo cụ thể là đã đọc (idempotent với thông báo đã đọc trước đó)
	MarkAsRead(ctx context.Context, userID, notificationID string) error

	// MarkAllAsRead đánh dấu tất cả thông báo của user là đã đọc
	MarkAllAsRead(ctx context.Context, userID string) error

	// GetActiveFCMTokenByUserID lấy FCM token của phiên đang hoạt động của user
	GetActiveFCMTokenByUserID(ctx context.Context, userID string) (string, error)

	// ClearFCMToken xóa FCM token chết khỏi database khi nhận diện token không còn hợp lệ,
	// giới hạn theo userID để không xóa nhầm token của user khác đang giữ cùng giá trị token.
	ClearFCMToken(ctx context.Context, userID, fcmToken string) error
}
