package jobs

import (
	"context"
	"fmt"

	"paysplit-backend/internal/platform/notification/fcm"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// Repository định nghĩa các thao tác lưu trữ mà NotificationWorker cần
type Repository interface {
	GetActiveFCMTokenByUserID(ctx context.Context, userID string) (string, error)
	ClearFCMToken(ctx context.Context, fcmToken string) error
}

// PushNotifier định nghĩa cổng gửi push notification
type PushNotifier interface {
	SendToDevice(ctx context.Context, fcmToken string, msg fcm.PushMessage) error
}

// NotificationJobArgs định nghĩa payload công việc gửi thông báo được lưu vào River Queue
type NotificationJobArgs struct {
	UserID  string          `json:"user_id"`
	Message fcm.PushMessage `json:"message"`
}

// Kind định danh loại job trong River Queue
func (NotificationJobArgs) Kind() string { return "send_notification" }

// NotificationWorker là worker của River Queue chịu trách nhiệm bốc và xử lý job gửi thông báo
type NotificationWorker struct {
	river.WorkerDefaults[NotificationJobArgs]
	repo         Repository
	pushNotifier PushNotifier
}

func NewNotificationWorker(repo Repository, pushNotifier PushNotifier) *NotificationWorker {
	return &NotificationWorker{
		repo:         repo,
		pushNotifier: pushNotifier,
	}
}

// Work được River Queue gọi tự động mỗi khi bốc được một NotificationJobArgs từ hàng đợi
func (w *NotificationWorker) Work(ctx context.Context, job *river.Job[NotificationJobArgs]) error {
	if w.pushNotifier == nil || job.Args.UserID == "" {
		return nil
	}

	token, err := w.repo.GetActiveFCMTokenByUserID(ctx, job.Args.UserID)
	if err != nil {
		return fmt.Errorf("get active FCM token: %w", err)
	}
	if token == "" {
		return nil
	}

	if sendErr := w.pushNotifier.SendToDevice(ctx, token, job.Args.Message); sendErr != nil {
		// Nếu token không còn hợp lệ (user gỡ app) -> Xóa khỏi DB và kết thúc job
		if fcm.IsInvalidTokenError(sendErr) {
			_ = w.repo.ClearFCMToken(ctx, token)
			return nil
		}
		// Trả về lỗi để River Queue tự động retry với exponential backoff
		return sendErr
	}

	return nil
}

// Enqueuer hỗ trợ đẩy công việc gửi thông báo vào River Queue
type Enqueuer struct {
	client *river.Client[pgx.Tx]
}

func NewEnqueuer(client *river.Client[pgx.Tx]) *Enqueuer {
	return &Enqueuer{client: client}
}

func (e *Enqueuer) EnqueueNotification(ctx context.Context, userID string, msg fcm.PushMessage) error {
	if e == nil || e.client == nil {
		return nil
	}

	_, err := e.client.Insert(ctx, NotificationJobArgs{
		UserID:  userID,
		Message: msg,
	}, nil)
	return err
}
