package jobs

import (
	"context"
	"fmt"

	"paysplit-backend/internal/platform/notification/fcm"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// NotificationProcessor là interface tiếp nhận job thông báo từ Worker để thực thi
type NotificationProcessor interface {
	ProcessNotificationJob(ctx context.Context, userID string, msg fcm.PushMessage) error
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
	processor NotificationProcessor
}

func NewNotificationWorker(processor NotificationProcessor) *NotificationWorker {
	return &NotificationWorker{
		processor: processor,
	}
}

// Work được River Queue gọi tự động mỗi khi bốc được một NotificationJobArgs từ hàng đợi
func (w *NotificationWorker) Work(ctx context.Context, job *river.Job[NotificationJobArgs]) error {
	if w.processor == nil {
		return fmt.Errorf("notification processor is not configured")
	}

	return w.processor.ProcessNotificationJob(ctx, job.Args.UserID, job.Args.Message)
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
