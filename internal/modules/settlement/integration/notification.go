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
	notification := &notificationdomain.Notification{UserID: userID, Type: storedType(kind), Title: title, Body: body, Payload: raw}
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
	case "payment_confirmed":
		return "Thanh toán đã xác nhận", "Thanh toán của bạn đã được chủ nợ xác nhận thành công."
	case "payment_marked_received":
		return "Thanh toán đã xác nhận", "Người nhận đã xác nhận đã nhận đủ tiền, khoản nợ đã được gạch."
	case "debt_reminded":
		return "Nhắc nhở thanh toán nợ", "Bạn có khoản nợ chưa thanh toán. Vui lòng kiểm tra và chuyển khoản."
	case "payment_created":
		return "Yêu cầu thanh toán mới", "Đã tạo mã thanh toán VietQR cho khoản nợ."
	case "payment_bank_confirmed":
		return "Thanh toán thành công", "Ngân hàng đã ghi nhận chuyển khoản của bạn, khoản nợ đã được gạch."
	case "payment_bank_received":
		return "Bạn đã nhận được tiền", "Một khoản chuyển khoản trong nhóm đã vào tài khoản của bạn và được tự động đối soát."
	default:
		return "Thông báo từ PaySplit", words
	}
}

// storedType gộp các biến thể xác nhận (ngân hàng, người nhận tự xác nhận) về payment_confirmed để
// client hiện có vẫn điều hướng tới màn hình payment như cũ; chỉ câu chữ khác.
func storedType(kind string) string {
	switch kind {
	case "payment_bank_confirmed", "payment_bank_received", "payment_marked_received":
		return "payment_confirmed"
	}
	return kind
}
