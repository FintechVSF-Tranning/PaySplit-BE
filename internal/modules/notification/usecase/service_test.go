package usecase

import (
	"context"
	"testing"
	"time"

	"paysplit-backend/internal/modules/notification/domain"
	"paysplit-backend/internal/platform/notification/fcm"

	"github.com/brpaz/lib-go/pagination"
)

type mockRepo struct {
	createdNotif      *domain.Notification
	activeToken       string
	clearedToken      string
	updatedSessionID  string
	updatedFCMToken   string
	unreadCount       int64
	readNotificationID string
	allMarkedRead     bool
}

func (m *mockRepo) CreateNotification(ctx context.Context, notif *domain.Notification) error {
	m.createdNotif = notif
	notif.ID = "notif-1"
	notif.CreatedAt = time.Now()
	return nil
}

func (m *mockRepo) ListByUserID(ctx context.Context, userID string, pager pagination.OffsetPager) (pagination.Page[domain.Notification], error) {
	return pagination.NewPage([]domain.Notification{}, 0, pager), nil
}

func (m *mockRepo) CountUnread(ctx context.Context, userID string) (int64, error) {
	return m.unreadCount, nil
}

func (m *mockRepo) MarkAsRead(ctx context.Context, userID, notificationID string) error {
	m.readNotificationID = notificationID
	return nil
}

func (m *mockRepo) MarkAllAsRead(ctx context.Context, userID string) error {
	m.allMarkedRead = true
	return nil
}

func (m *mockRepo) GetActiveFCMTokenByUserID(ctx context.Context, userID string) (string, error) {
	return m.activeToken, nil
}

func (m *mockRepo) UpdateSessionFCMToken(ctx context.Context, sessionID, fcmToken string) error {
	m.updatedSessionID = sessionID
	m.updatedFCMToken = fcmToken
	return nil
}

func (m *mockRepo) ClearFCMToken(ctx context.Context, fcmToken string) error {
	m.clearedToken = fcmToken
	return nil
}

type mockPushNotifier struct {
	sentToken string
	sentMsg   fcm.PushMessage
	broadcastMsg fcm.PushMessage
	errToSend error
}

func (m *mockPushNotifier) SendToDevice(ctx context.Context, fcmToken string, msg fcm.PushMessage) error {
	m.sentToken = fcmToken
	m.sentMsg = msg
	return m.errToSend
}

func (m *mockPushNotifier) SendToAllUsers(ctx context.Context, msg fcm.PushMessage) error {
	m.broadcastMsg = msg
	return m.errToSend
}

type mockJobEnqueuer struct {
	enqueuedUserID string
	enqueuedMsg    fcm.PushMessage
}

func (m *mockJobEnqueuer) EnqueueNotification(ctx context.Context, userID string, msg fcm.PushMessage) error {
	m.enqueuedUserID = userID
	m.enqueuedMsg = msg
	return nil
}

func TestSendToUser_WithJobEnqueuer(t *testing.T) {
	repo := &mockRepo{}
	notifier := &mockPushNotifier{}
	service := NewService(repo, notifier)
	enqueuer := &mockJobEnqueuer{}
	service.SetEnqueuer(enqueuer)

	ctx := context.Background()
	msg := fcm.NewPaymentReminderMessage("Lâm", 50000, "group-1", "bill-1")

	err := service.SendToUser(ctx, "user-1", msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.createdNotif == nil {
		t.Fatalf("expected notification to be saved to DB")
	}
	if enqueuer.enqueuedUserID != "user-1" {
		t.Errorf("expected job enqueued for user-1, got %s", enqueuer.enqueuedUserID)
	}
}

func TestSendToUser_FallbackGoroutine(t *testing.T) {
	repo := &mockRepo{activeToken: "device-token-123"}
	notifier := &mockPushNotifier{}
	service := NewService(repo, notifier)

	ctx := context.Background()
	msg := fcm.NewPaymentReminderMessage("Lâm", 50000, "group-1", "bill-1")

	err := service.SendToUser(ctx, "user-1", msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.createdNotif == nil {
		t.Fatalf("expected notification to be saved to DB")
	}

	time.Sleep(50 * time.Millisecond)

	if notifier.sentToken != "device-token-123" {
		t.Errorf("expected device-token-123, got %s", notifier.sentToken)
	}
}

func TestProcessNotificationJob_ClearsInvalidToken(t *testing.T) {
	repo := &mockRepo{activeToken: "dead-token"}
	notifier := &mockPushNotifier{errToSend: fcm.ErrInvalidToken}
	service := NewService(repo, notifier)

	ctx := context.Background()
	msg := fcm.NewPaymentReminderMessage("Lâm", 50000, "group-1", "bill-1")

	err := service.ProcessNotificationJob(ctx, "user-1", msg)
	if err != nil {
		t.Errorf("expected nil error when invalid token is cleared, got %v", err)
	}

	if repo.clearedToken != "dead-token" {
		t.Errorf("expected dead-token to be cleared from DB, got %s", repo.clearedToken)
	}
}

func TestUpdateFCMToken(t *testing.T) {
	repo := &mockRepo{}
	service := NewService(repo, nil)

	ctx := context.Background()
	err := service.UpdateFCMToken(ctx, "session-1", "new-token-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.updatedSessionID != "session-1" || repo.updatedFCMToken != "new-token-123" {
		t.Errorf("token update mismatch")
	}
}

func TestGetUnreadCount(t *testing.T) {
	repo := &mockRepo{unreadCount: 5}
	service := NewService(repo, nil)

	count, err := service.GetUnreadCount(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5, got %d", count)
	}
}

func TestMarkAsRead(t *testing.T) {
	repo := &mockRepo{}
	service := NewService(repo, nil)

	err := service.MarkAsRead(context.Background(), "user-1", "notif-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.readNotificationID != "notif-1" {
		t.Errorf("expected notif-1, got %s", repo.readNotificationID)
	}
}

func TestMarkAllAsRead(t *testing.T) {
	repo := &mockRepo{}
	service := NewService(repo, nil)

	err := service.MarkAllAsRead(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !repo.allMarkedRead {
		t.Errorf("expected all marked as read")
	}
}
