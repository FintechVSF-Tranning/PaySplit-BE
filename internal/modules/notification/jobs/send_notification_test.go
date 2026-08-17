package jobs

import (
	"context"
	"errors"
	"testing"

	"paysplit-backend/internal/platform/notification/fcm"

	"github.com/riverqueue/river"
)

type mockWorkerRepo struct {
	activeToken    string
	activeTokenErr error
	clearedToken   string
}

func (m *mockWorkerRepo) GetActiveFCMTokenByUserID(ctx context.Context, userID string) (string, error) {
	if m.activeTokenErr != nil {
		return "", m.activeTokenErr
	}
	return m.activeToken, nil
}

func (m *mockWorkerRepo) ClearFCMToken(ctx context.Context, fcmToken string) error {
	m.clearedToken = fcmToken
	return nil
}

type mockWorkerNotifier struct {
	sentToken string
	sentMsg   fcm.PushMessage
	errToSend error
}

func (m *mockWorkerNotifier) SendToDevice(ctx context.Context, fcmToken string, msg fcm.PushMessage) error {
	m.sentToken = fcmToken
	m.sentMsg = msg
	return m.errToSend
}

func TestNotificationJobArgs_Kind(t *testing.T) {
	args := NotificationJobArgs{}
	if args.Kind() != "send_notification" {
		t.Errorf("expected send_notification, got %s", args.Kind())
	}
}

func TestNotificationWorker_Work_Success(t *testing.T) {
	repo := &mockWorkerRepo{activeToken: "device-tok-1"}
	notifier := &mockWorkerNotifier{}
	worker := NewNotificationWorker(repo, notifier)

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

	if notifier.sentToken != "device-tok-1" {
		t.Errorf("expected sent token device-tok-1, got %s", notifier.sentToken)
	}
	if notifier.sentMsg.Title != msg.Title {
		t.Errorf("expected title %s, got %s", msg.Title, notifier.sentMsg.Title)
	}
}

func TestNotificationWorker_Work_ClearsDeadToken(t *testing.T) {
	repo := &mockWorkerRepo{activeToken: "dead-token"}
	notifier := &mockWorkerNotifier{errToSend: fcm.ErrInvalidToken}
	worker := NewNotificationWorker(repo, notifier)

	msg := fcm.NewPaymentReminderMessage("Lâm", 50000, "g-1", "b-1")
	job := &river.Job[NotificationJobArgs]{
		Args: NotificationJobArgs{
			UserID:  "user-123",
			Message: msg,
		},
	}

	err := worker.Work(context.Background(), job)
	if err != nil {
		t.Errorf("expected nil error when invalid token is cleared, got %v", err)
	}
	if repo.clearedToken != "dead-token" {
		t.Errorf("expected dead-token cleared, got %s", repo.clearedToken)
	}
}

func TestNotificationWorker_Work_DBErrorPropagates(t *testing.T) {
	repo := &mockWorkerRepo{activeTokenErr: errors.New("db timeout")}
	notifier := &mockWorkerNotifier{}
	worker := NewNotificationWorker(repo, notifier)

	job := &river.Job[NotificationJobArgs]{
		Args: NotificationJobArgs{
			UserID: "user-123",
		},
	}

	err := worker.Work(context.Background(), job)
	if err == nil {
		t.Fatalf("expected error when DB fails")
	}
}

func TestNotificationWorker_Work_NoActiveToken(t *testing.T) {
	repo := &mockWorkerRepo{activeToken: ""}
	notifier := &mockWorkerNotifier{}
	worker := NewNotificationWorker(repo, notifier)

	job := &river.Job[NotificationJobArgs]{
		Args: NotificationJobArgs{
			UserID: "user-123",
		},
	}

	err := worker.Work(context.Background(), job)
	if err != nil {
		t.Fatalf("expected nil error when no active token, got %v", err)
	}
	if notifier.sentToken != "" {
		t.Errorf("expected nothing sent, got token %s", notifier.sentToken)
	}
}
