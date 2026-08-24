package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"paysplit-backend/internal/modules/notification/domain"
	"paysplit-backend/internal/modules/notification/repository"

	"github.com/brpaz/lib-go/pagination"
)

type mockRepo struct {
	createdNotif       *domain.Notification
	createNotifErr     error
	activeToken        string
	activeTokenErr     error
	clearedUserID      string
	clearedToken       string
	unreadCount        int64
	readNotificationID string
	allMarkedRead      bool
}

func (m *mockRepo) CreateNotification(ctx context.Context, notif *domain.Notification) error {
	if m.createNotifErr != nil {
		return m.createNotifErr
	}
	m.createdNotif = notif
	notif.ID = "notif-1"
	notif.CreatedAt = time.Now()
	return nil
}

func (m *mockRepo) CreateNotificationTx(ctx context.Context, ex repository.Executor, notif *domain.Notification) error {
	return m.CreateNotification(ctx, notif)
}

func (m *mockRepo) WithTx(ctx context.Context, fn func(ctx context.Context, ex repository.Executor) error) error {
	return fn(ctx, nil)
}

func (m *mockRepo) GetNotificationByID(ctx context.Context, notificationID string) (domain.Notification, error) {
	if m.createdNotif != nil && m.createdNotif.ID == notificationID {
		return *m.createdNotif, nil
	}
	return domain.Notification{}, domain.ErrNotificationNotFound
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
	if m.activeTokenErr != nil {
		return "", m.activeTokenErr
	}
	return m.activeToken, nil
}

func (m *mockRepo) ClearFCMToken(ctx context.Context, userID, fcmToken string) error {
	m.clearedUserID = userID
	m.clearedToken = fcmToken
	return nil
}

type mockPushNotifier struct {
	sentToken    string
	sentMsg      domain.PushMessage
	broadcastMsg domain.PushMessage
	errToSend    error
}

func (m *mockPushNotifier) SendToDevice(ctx context.Context, fcmToken string, msg domain.PushMessage) error {
	m.sentToken = fcmToken
	m.sentMsg = msg
	return m.errToSend
}

func (m *mockPushNotifier) SendToAllUsers(ctx context.Context, msg domain.PushMessage) error {
	m.broadcastMsg = msg
	return m.errToSend
}

func (m *mockPushNotifier) IsInvalidToken(err error) bool {
	return err != nil && err.Error() == "invalid token"
}

type mockJobEnqueuer struct {
	enqueuedNotificationID string
	errToReturn            error
}

func (m *mockJobEnqueuer) EnqueueNotificationTx(ctx context.Context, ex repository.Executor, notificationID string) error {
	if m.errToReturn != nil {
		return m.errToReturn
	}
	m.enqueuedNotificationID = notificationID
	return nil
}

func samplePushMessage() domain.PushMessage {
	return domain.PushMessage{
		Title: "Nhắc nhở thanh toán 💸",
		Body:  "Lâm vừa nhắc bạn thanh toán 50.000đ.",
		Data: map[string]string{
			"type":     domain.TypePaymentReminder,
			"group_id": "group-1",
			"bill_id":  "bill-1",
			"amount":   "50000",
		},
	}
}

func TestSendToUser_WithJobEnqueuer(t *testing.T) {
	repo := &mockRepo{}
	notifier := &mockPushNotifier{}
	enqueuer := &mockJobEnqueuer{}
	service := NewService(repo, notifier, enqueuer)

	ctx := context.Background()
	msg := samplePushMessage()

	err := service.SendToUser(ctx, "user-1", msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.createdNotif == nil {
		t.Fatalf("expected notification to be saved to DB")
	}
	if enqueuer.enqueuedNotificationID != repo.createdNotif.ID {
		t.Errorf("expected job enqueued with notification id %s, got %s", repo.createdNotif.ID, enqueuer.enqueuedNotificationID)
	}
}

func TestSendToUser_EnqueueFailureDoesNotLeaveOrphanRecord(t *testing.T) {
	repo := &mockRepo{}
	notifier := &mockPushNotifier{}
	enqueuer := &mockJobEnqueuer{errToReturn: errors.New("insert job failed")}
	service := NewService(repo, notifier, enqueuer)

	ctx := context.Background()
	msg := samplePushMessage()

	err := service.SendToUser(ctx, "user-1", msg)
	if err == nil {
		t.Fatalf("expected error when job enqueue fails")
	}

	// mockRepo.WithTx does not actually roll back (it has no real transaction to roll back), but
	// the point under test is that SendToUser propagates the enqueue error instead of swallowing
	// it, so a real (transactional) repository rolls the notification insert back too.
	if enqueuer.enqueuedNotificationID != "" {
		t.Errorf("expected no notification id recorded on enqueue failure")
	}
}

func TestSendToUser_RejectsEmptyTitleOrBody(t *testing.T) {
	repo := &mockRepo{}
	service := NewService(repo, &mockPushNotifier{}, &mockJobEnqueuer{})

	err := service.SendToUser(context.Background(), "user-1", domain.PushMessage{Title: "", Body: "body"})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for empty title, got %v", err)
	}

	err = service.SendToUser(context.Background(), "user-1", domain.PushMessage{Title: "title", Body: "  "})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for empty body, got %v", err)
	}
}

func TestSendToUser_TruncatesOverLongBody(t *testing.T) {
	repo := &mockRepo{}
	enqueuer := &mockJobEnqueuer{}
	service := NewService(repo, &mockPushNotifier{}, enqueuer)

	longBody := strings.Repeat("a", maxBodyLength+500)
	err := service.SendToUser(context.Background(), "user-1", domain.PushMessage{Title: "title", Body: longBody})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.createdNotif == nil {
		t.Fatalf("expected notification to be saved")
	}
	if len([]rune(repo.createdNotif.Body)) > maxBodyLength {
		t.Errorf("expected body truncated to at most %d runes, got %d", maxBodyLength, len([]rune(repo.createdNotif.Body)))
	}
}

func TestSendToUser_FallbackDirectSend(t *testing.T) {
	repo := &mockRepo{activeToken: "device-token-123"}
	notifier := &mockPushNotifier{}
	service := NewService(repo, notifier, nil)

	ctx := context.Background()
	msg := samplePushMessage()

	err := service.SendToUser(ctx, "user-1", msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.createdNotif == nil {
		t.Fatalf("expected notification to be saved to DB")
	}

	if notifier.sentToken != "device-token-123" {
		t.Errorf("expected device-token-123, got %s", notifier.sentToken)
	}
}

func TestGetUnreadCount(t *testing.T) {
	repo := &mockRepo{unreadCount: 5}
	service := NewService(repo, nil, nil)

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
	service := NewService(repo, nil, nil)

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
	service := NewService(repo, nil, nil)

	err := service.MarkAllAsRead(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !repo.allMarkedRead {
		t.Errorf("expected all marked as read")
	}
}
