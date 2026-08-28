package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	notificationdomain "paysplit-backend/internal/modules/notification/domain"
	notificationrepo "paysplit-backend/internal/modules/notification/repository"
	settlementrepo "paysplit-backend/internal/modules/settlement/repository"
)

type Enqueuer interface {
	EnqueueNotificationTx(context.Context, notificationrepo.Executor, string) error
}
type Notifier struct {
	repo     notificationrepo.Repository
	enqueuer Enqueuer
}

func NewNotifier(repo notificationrepo.Repository, enqueuer Enqueuer) *Notifier {
	if repo == nil {
		panic("settlement notifier repository must not be nil")
	}
	return &Notifier{repo: repo, enqueuer: enqueuer}
}
func (n *Notifier) NotifyTx(ctx context.Context, ex settlementrepo.Executor, userID, kind string, data map[string]string) error {
	title, body := message(kind)
	raw, e := json.Marshal(data)
	if e != nil {
		return e
	}
	notification := &notificationdomain.Notification{UserID: userID, Type: kind, Title: title, Body: body, Payload: raw}
	if e = n.repo.CreateNotificationTx(ctx, ex, notification); e != nil {
		return fmt.Errorf("create settlement notification: %w", e)
	}
	if n.enqueuer != nil {
		if e = n.enqueuer.EnqueueNotificationTx(ctx, ex, notification.ID); e != nil {
			return fmt.Errorf("enqueue settlement notification: %w", e)
		}
	}
	return nil
}
func message(kind string) (string, string) {
	words := strings.ReplaceAll(kind, "_", " ")
	switch kind {
	case "payment_submitted":
		return "Minh chứng thanh toán mới", "Có minh chứng chuyển tiền mới đang chờ bạn xác nhận."
	case "payment_confirmed":
		return "Thanh toán đã xác nhận", "Thanh toán của bạn đã được chủ nợ xác nhận thành công."
	case "payment_rejected":
		return "Thanh toán bị từ chối", "Minh chứng chuyển tiền bị từ chối. Vui lòng kiểm tra và gửi lại."
	case "debt_reminded":
		return "Nhắc nhở thanh toán nợ", "Bạn có khoản nợ chưa thanh toán. Vui lòng kiểm tra và chuyển khoản."
	case "payment_stalled_confirmation":
		return "Nhắc duyệt minh chứng", "Minh chứng thanh toán đã gửi lâu chưa được duyệt. Vui lòng xác nhận."
	case "payment_created":
		return "Yêu cầu thanh toán mới", "Đã tạo mã thanh toán VietQR cho khoản nợ."
	default:
		return "Thông báo từ PaySplit", words
	}
}
