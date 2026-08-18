package jobs

import (
	"context"
	"errors"
	"testing"

	"paysplit-backend/internal/modules/notification/domain"
	"paysplit-backend/internal/platform/notification/fcm"

	"github.com/riverqueue/river"
)

type mockWorkerRepo struct {
	notif          domain.Notification
	notifErr       error
	activeToken    string
	activeTokenErr error
	clearedUserID  string
	clearedToken   string
}

func (m *mockWorkerRepo) GetNotificationByID(ctx context.Context, notificationID string) (domain.Notification, error) {
	if m.notifErr != nil {
		return domain.Notification{}, m.notifErr
	}
	return m.notif, nil
}

func (m *mockWorkerRepo) GetActiveFCMTokenByUserID(ctx context.Context, userID string) (string, error) {
	if m.activeTokenErr != nil {
		return "", m.activeTokenErr
	}
	return m.activeToken, nil
}

func (m *mockWorkerRepo) ClearFCMToken(ctx context.Context, userID, fcmToken string) error {
	m.clearedUserID = userID
	m.clearedToken = fcmToken
	return nil
}

type mockWorkerNotifier struct {
	sentToken string
	sentMsg   domain.PushMessage
	errToSend error
}

func (m *mockWorkerNotifier) SendToDevice(ctx context.Context, fcmToken string, msg domain.PushMessage) error {
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
	msg := fcm.NewPaymentReminderMessage("Lâm", 50000, "g-1", "b-1")
	notif := domain.Notification{ID: "notif-1", UserID: "user-123", Title: msg.Title, Body: msg.Body}
	repo := &mockWorkerRepo{activeToken: "device-tok-1", notif: notif}
	notifier := &mockWorkerNotifier{}
	worker := NewNotificationWorker(repo, notifier)

	job := &river.Job[NotificationJobArgs]{Args: NotificationJobArgs{NotificationID: "notif-1"}}

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
	notif := domain.Notification{ID: "notif-1", UserID: "user-123", Title: "t", Body: "b"}
	repo := &mockWorkerRepo{activeToken: "dead-token", notif: notif}
	notifier := &mockWorkerNotifier{errToSend: fcm.ErrInvalidToken}
	worker := NewNotificationWorker(repo, notifier)

	job := &river.Job[NotificationJobArgs]{Args: NotificationJobArgs{NotificationID: "notif-1"}}

	err := worker.Work(context.Background(), job)
	if err != nil {
		t.Errorf("expected nil error when invalid token is cleared, got %v", err)
	}
	if repo.clearedUserID != "user-123" || repo.clearedToken != "dead-token" {
		t.Errorf("expected dead-token cleared for user-123, got user=%s token=%s", repo.clearedUserID, repo.clearedToken)
	}
}

// TestNotificationWorker_Work_InvalidMessageDoesNotClearToken pins the fix for the blocker where
// FCM's INVALID_ARGUMENT (a bad message/payload, not a dead token) was previously conflated with
// IsInvalidTokenError and made the worker wipe a perfectly valid device token.
func TestNotificationWorker_Work_InvalidMessageDoesNotClearToken(t *testing.T) {
	notif := domain.Notification{ID: "notif-1", UserID: "user-123", Title: "t", Body: "b"}
	repo := &mockWorkerRepo{activeToken: "still-valid-token", notif: notif}
	notifier := &mockWorkerNotifier{errToSend: fcm.ErrInvalidMessage}
	worker := NewNotificationWorker(repo, notifier)

	job := &river.Job[NotificationJobArgs]{Args: NotificationJobArgs{NotificationID: "notif-1"}}

	err := worker.Work(context.Background(), job)
	if err != nil {
		t.Errorf("expected nil error (job completes, no retry) for an invalid message, got %v", err)
	}
	if repo.clearedToken != "" {
		t.Errorf("expected token left untouched on an invalid-message error, got cleared token %q", repo.clearedToken)
	}
}

func TestNotificationWorker_Work_NotificationNotFoundCompletesJob(t *testing.T) {
	repo := &mockWorkerRepo{notifErr: domain.ErrNotificationNotFound}
	notifier := &mockWorkerNotifier{}
	worker := NewNotificationWorker(repo, notifier)

	job := &river.Job[NotificationJobArgs]{Args: NotificationJobArgs{NotificationID: "notif-gone"}}

	err := worker.Work(context.Background(), job)
	if err != nil {
		t.Fatalf("expected nil error when notification no longer exists, got %v", err)
	}
	if notifier.sentToken != "" {
		t.Errorf("expected nothing sent, got token %s", notifier.sentToken)
	}
}

func TestNotificationWorker_Work_DBErrorPropagates(t *testing.T) {
	notif := domain.Notification{ID: "notif-1", UserID: "user-123", Title: "t", Body: "b"}
	repo := &mockWorkerRepo{activeTokenErr: errors.New("db timeout"), notif: notif}
	notifier := &mockWorkerNotifier{}
	worker := NewNotificationWorker(repo, notifier)

	job := &river.Job[NotificationJobArgs]{Args: NotificationJobArgs{NotificationID: "notif-1"}}

	err := worker.Work(context.Background(), job)
	if err == nil {
		t.Fatalf("expected error when DB fails")
	}
}

func TestNotificationWorker_Work_NoActiveToken(t *testing.T) {
	notif := domain.Notification{ID: "notif-1", UserID: "user-123", Title: "t", Body: "b"}
	repo := &mockWorkerRepo{activeToken: "", notif: notif}
	notifier := &mockWorkerNotifier{}
	worker := NewNotificationWorker(repo, notifier)

	job := &river.Job[NotificationJobArgs]{Args: NotificationJobArgs{NotificationID: "notif-1"}}

	err := worker.Work(context.Background(), job)
	if err != nil {
		t.Fatalf("expected nil error when no active token, got %v", err)
	}
	if notifier.sentToken != "" {
		t.Errorf("expected nothing sent, got token %s", notifier.sentToken)
	}
}
