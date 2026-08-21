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
		return "Payment proof submitted", "A payment proof is waiting for your confirmation"
	case "payment_confirmed":
		return "Payment confirmed", "Your payment was confirmed"
	case "payment_rejected":
		return "Payment rejected", "Your payment proof was rejected"
	case "debt_reminded":
		return "Payment reminder", "You have an outstanding debt to settle"
	case "payment_stalled_confirmation":
		return "Confirmation reminder", "A submitted payment is still waiting for confirmation"
	default:
		return "PaySplit update", words
	}
}
