package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"paysplit-backend/internal/modules/notification/domain"
	"paysplit-backend/internal/modules/notification/repository"
	"paysplit-backend/internal/platform/notification/fcm"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// Repository định nghĩa các thao tác lưu trữ mà NotificationWorker cần
type Repository interface {
	GetNotificationByID(ctx context.Context, notificationID string) (domain.Notification, error)
	GetActiveFCMTokenByUserID(ctx context.Context, userID string) (string, error)
	ClearFCMToken(ctx context.Context, userID, fcmToken string) error
}

// PushNotifier định nghĩa cổng gửi push notification
type PushNotifier interface {
	SendToDevice(ctx context.Context, fcmToken string, msg fcm.PushMessage) error
}

// NotificationJobArgs định nghĩa payload công việc gửi thông báo được lưu vào River Queue.
// Job chỉ mang NotificationID; worker nạp lại title/body/payload từ bản ghi đã lưu, tránh
// lưu trùng nội dung và cho phép dùng NotificationID làm handle idempotency/dedupe.
type NotificationJobArgs struct {
	NotificationID string `json:"notification_id"`
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

// Work được River Queue gọi tự động mỗi khi bốc được một NotificationJobArgs từ hàng đợi.
// Job nạp lại nội dung từ bản ghi notifications thay vì mang sẵn title/body/payload, vì River
// đảm bảo giao at-least-once và cần NotificationID làm handle để chống trùng lặp/nạp lại nội dung.
func (w *NotificationWorker) Work(ctx context.Context, job *river.Job[NotificationJobArgs]) error {
	if w.pushNotifier == nil || job.Args.NotificationID == "" {
		return nil
	}

	notif, err := w.repo.GetNotificationByID(ctx, job.Args.NotificationID)
	if err != nil {
		if errors.Is(err, domain.ErrNotificationNotFound) {
			// Bản ghi thông báo không còn tồn tại (vd: user đã bị xóa) -> hoàn tất job, không retry.
			return nil
		}
		return fmt.Errorf("get notification: %w", err)
	}

	token, err := w.repo.GetActiveFCMTokenByUserID(ctx, notif.UserID)
	if err != nil {
		return fmt.Errorf("get active FCM token: %w", err)
	}
	if token == "" {
		return nil
	}

	msg := fcm.PushMessage{Title: notif.Title, Body: notif.Body, Data: payloadToData(notif.Payload)}

	if sendErr := w.pushNotifier.SendToDevice(ctx, token, msg); sendErr != nil {
		// Nếu token không còn hợp lệ (user gỡ app) -> Xóa khỏi DB và kết thúc job
		if fcm.IsInvalidTokenError(sendErr) {
			_ = w.repo.ClearFCMToken(ctx, notif.UserID, token)
			return nil
		}
		// Lỗi do nội dung message (không phải token) -> ghi log để phát hiện bug ở message
		// builder, hoàn tất job mà KHÔNG xóa token vì bản thân token vẫn hợp lệ.
		if fcm.IsInvalidMessageError(sendErr) {
			log.Printf("event=fcm_invalid_message notification_id=%s user_id=%s err=%v", notif.ID, notif.UserID, sendErr)
			return nil
		}
		// Trả về lỗi để River Queue tự động retry với exponential backoff
		return sendErr
	}

	return nil
}

func payloadToData(payload json.RawMessage) map[string]string {
	if len(payload) == 0 {
		return nil
	}
	data := make(map[string]string)
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil
	}
	return data
}

// Enqueuer hỗ trợ đẩy công việc gửi thông báo vào River Queue
type Enqueuer struct {
	client *river.Client[pgx.Tx]
}

func NewEnqueuer(client *river.Client[pgx.Tx]) *Enqueuer {
	return &Enqueuer{client: client}
}

// EnqueueNotificationTx đẩy job gửi push cho notificationID vào River Queue, tham gia cùng
// transaction ex với việc lưu bản ghi notifications để đảm bảo cả hai thao tác nguyên tử.
func (e *Enqueuer) EnqueueNotificationTx(ctx context.Context, ex repository.Executor, notificationID string) error {
	if e == nil || e.client == nil {
		return nil
	}

	tx, ok := ex.(pgx.Tx)
	if !ok {
		return fmt.Errorf("enqueue notification: invalid transaction executor")
	}

	_, err := e.client.InsertTx(ctx, tx, NotificationJobArgs{NotificationID: notificationID}, nil)
	return err
}
