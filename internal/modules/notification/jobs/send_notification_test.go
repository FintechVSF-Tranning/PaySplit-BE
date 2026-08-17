package jobs

import (
	"context"
	"testing"

	"paysplit-backend/internal/platform/notification/fcm"

	"github.com/riverqueue/river"
)

type mockProcessor struct {
	calledUserID string
	calledMsg    fcm.PushMessage
	returnErr    error
}

func (m *mockProcessor) ProcessNotificationJob(ctx context.Context, userID string, msg fcm.PushMessage) error {
	m.calledUserID = userID
	m.calledMsg = msg
	return m.returnErr
}

func TestNotificationJobArgs_Kind(t *testing.T) {
	args := NotificationJobArgs{}
	if args.Kind() != "send_notification" {
		t.Errorf("expected send_notification, got %s", args.Kind())
	}
}

func TestNotificationWorker_Work(t *testing.T) {
	proc := &mockProcessor{}
	worker := NewNotificationWorker(proc)

	msg := fcm.NewPaymentReminderMessage("Lâm", 50000, "g-1", "b-1")
	job := &river.Job[NotificationJobArgs]{
		Args: NotificationJobArgs{
			UserID:  "user-123",
			Message: msg,
		},
	}

	err := worker.Work(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if proc.calledUserID != "user-123" {
		t.Errorf("expected user-123, got %s", proc.calledUserID)
	}
	if proc.calledMsg.Title != msg.Title {
		t.Errorf("expected title %s, got %s", msg.Title, proc.calledMsg.Title)
	}
}
