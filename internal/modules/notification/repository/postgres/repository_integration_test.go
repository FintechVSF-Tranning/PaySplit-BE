package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brpaz/lib-go/pagination"
	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/modules/notification/domain"
	"paysplit-backend/internal/modules/notification/repository"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type testCleanup struct {
	pool  *pgxpool.Pool
	users []string
}

func newCleanup(t *testing.T, pool *pgxpool.Pool) *testCleanup {
	c := &testCleanup{pool: pool}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, u := range c.users {
			_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, u)
		}
	})
	return c
}

func (c *testCleanup) trackUser(id string) { c.users = append(c.users, id) }

var testUserSeq atomic.Uint64

func createTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cleanup *testCleanup, name string) string {
	t.Helper()
	seq := testUserSeq.Add(1)
	email := fmt.Sprintf("notif.repo.test.%d.%d@example.invalid", time.Now().UnixNano(), seq)
	phone := fmt.Sprintf("+84%09d", (time.Now().UnixNano()+int64(seq*7919))%1000000000)
	var id string
	err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, display_name, phone_number, role, status, email_verified_at) VALUES ($1,'x',$2,$3,'user','active',now()) RETURNING id`, email, name, phone).Scan(&id)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	cleanup.trackUser(id)
	return id
}

func TestPostgresRepository_CreateAndListNotifications(t *testing.T) {
	pool := testPool(t)
	cleanup := newCleanup(t, pool)
	ctx := context.Background()
	repo := New(pool)

	user1 := createTestUser(t, ctx, pool, cleanup, "User One")
	user2 := createTestUser(t, ctx, pool, cleanup, "User Two")

	payload := json.RawMessage(`{"group_id":"g-1","bill_id":"b-1","amount":"50000"}`)
	notif1 := &domain.Notification{
		UserID:  user1,
		Type:    "payment_reminder",
		Title:   "Nhắc nợ 1",
		Body:    "Vui lòng trả 50.000đ",
		Payload: payload,
	}

	if err := repo.CreateNotification(ctx, notif1); err != nil {
		t.Fatalf("create notif1 failed: %v", err)
	}
	if notif1.ID == "" {
		t.Fatal("expected non-empty notification ID")
	}
	if notif1.CreatedAt.IsZero() {
		t.Fatal("expected non-zero created_at")
	}

	notif2 := &domain.Notification{
		UserID:  user1,
		Type:    "new_bill",
		Title:   "Hóa đơn mới 2",
		Body:    "Hóa đơn ăn trưa",
		Payload: json.RawMessage(`{}`),
	}
	if err := repo.CreateNotification(ctx, notif2); err != nil {
		t.Fatalf("create notif2 failed: %v", err)
	}

	// Notification for user2 to test isolation
	notifUser2 := &domain.Notification{
		UserID: user2,
		Type:   "group_invitation",
		Title:  "Lời mời vào nhóm",
		Body:   "Bạn được mời",
	}
	if err := repo.CreateNotification(ctx, notifUser2); err != nil {
		t.Fatalf("create notifUser2 failed: %v", err)
	}

	// Test ListByUserID for user1
	pager := pagination.NewOffsetPager(1, 10)
	page, err := repo.ListByUserID(ctx, user1, pager)
	if err != nil {
		t.Fatalf("list notifications for user1: %v", err)
	}
	if page.Total != 2 {
		t.Errorf("expected total 2, got %d", page.Total)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(page.Items))
	}
	// Ordered by created_at DESC -> notif2 first, then notif1
	if page.Items[0].ID != notif2.ID {
		t.Errorf("expected most recent notif first (%s), got %s", notif2.ID, page.Items[0].ID)
	}
	if page.Items[1].ID != notif1.ID {
		t.Errorf("expected second notif (%s), got %s", notif1.ID, page.Items[1].ID)
	}

	// Test ListByUserID for user2
	page2, err := repo.ListByUserID(ctx, user2, pager)
	if err != nil {
		t.Fatalf("list notifications for user2: %v", err)
	}
	if page2.Total != 1 || len(page2.Items) != 1 || page2.Items[0].ID != notifUser2.ID {
		t.Errorf("expected 1 notif for user2, got %+v", page2)
	}
}

func TestPostgresRepository_UnreadCountAndMarkAsRead(t *testing.T) {
	pool := testPool(t)
	cleanup := newCleanup(t, pool)
	ctx := context.Background()
	repo := New(pool)

	user1 := createTestUser(t, ctx, pool, cleanup, "User Read Test")
	user2 := createTestUser(t, ctx, pool, cleanup, "User Isolation")

	// Initially unread count is 0
	count, err := repo.CountUnread(ctx, user1)
	if err != nil {
		t.Fatalf("count unread: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 unread, got %d", count)
	}

	notif1 := &domain.Notification{
		UserID: user1,
		Type:   "bill_finalized",
		Title:  "Chốt hóa đơn 1",
		Body:   "Đã chốt",
	}
	notif2 := &domain.Notification{
		UserID: user1,
		Type:   "bill_finalized",
		Title:  "Chốt hóa đơn 2",
		Body:   "Đã chốt",
	}
	notifOther := &domain.Notification{
		UserID: user2,
		Type:   "bill_finalized",
		Title:  "Chốt hóa đơn user khác",
		Body:   "Đã chốt",
	}
	_ = repo.CreateNotification(ctx, notif1)
	_ = repo.CreateNotification(ctx, notif2)
	_ = repo.CreateNotification(ctx, notifOther)

	// Verify unread count is 2 for user1
	count, err = repo.CountUnread(ctx, user1)
	if err != nil || count != 2 {
		t.Errorf("expected 2 unread for user1, got %d, err: %v", count, err)
	}

	// Mark notif1 as read
	if err := repo.MarkAsRead(ctx, user1, notif1.ID); err != nil {
		t.Fatalf("mark notif1 as read: %v", err)
	}

	// Unread count should now be 1
	count, err = repo.CountUnread(ctx, user1)
	if err != nil || count != 1 {
		t.Errorf("expected 1 unread for user1, got %d", count)
	}

	// Marking an already-read notification again must be idempotent (200, not 404): the client
	// may retry a flaky PATCH, or the user may tap it twice.
	if err := repo.MarkAsRead(ctx, user1, notif1.ID); err != nil {
		t.Errorf("expected marking an already-read notification to be idempotent, got: %v", err)
	}

	// User1 cannot mark User2's notification as read
	err = repo.MarkAsRead(ctx, user1, notifOther.ID)
	if err != domain.ErrNotificationNotFound {
		t.Errorf("expected ErrNotificationNotFound for cross-user notif mark, got: %v", err)
	}

	// MarkAllAsRead for user1
	if err := repo.MarkAllAsRead(ctx, user1); err != nil {
		t.Fatalf("mark all as read: %v", err)
	}

	count, err = repo.CountUnread(ctx, user1)
	if err != nil || count != 0 {
		t.Errorf("expected 0 unread for user1 after mark-all, got %d", count)
	}

	// User2's unread notification should still be untouched
	count2, err := repo.CountUnread(ctx, user2)
	if err != nil || count2 != 1 {
		t.Errorf("expected user2 unread count to remain 1, got %d", count2)
	}
}

func TestPostgresRepository_GetAndClearActiveFCMToken(t *testing.T) {
	pool := testPool(t)
	cleanup := newCleanup(t, pool)
	ctx := context.Background()
	repo := New(pool)

	user := createTestUser(t, ctx, pool, cleanup, "FCM Token User")

	// No session yet -> empty token, no error
	token, err := repo.GetActiveFCMTokenByUserID(ctx, user)
	if err != nil {
		t.Fatalf("get active fcm token: %v", err)
	}
	if token != "" {
		t.Errorf("expected empty token for user without session, got %s", token)
	}

	// Insert active session with fcm_token
	fcmTok := "fcm-device-sample-token-12345"
	_, err = pool.Exec(ctx, `
		INSERT INTO sessions (user_id, device_id, fcm_token, issued_at, expires_at)
		VALUES ($1, gen_random_uuid(), $2, now(), now() + interval '7 days')
	`, user, fcmTok)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// Verify token retrieved
	token, err = repo.GetActiveFCMTokenByUserID(ctx, user)
	if err != nil || token != fcmTok {
		t.Fatalf("expected token %s, got %s (err: %v)", fcmTok, token, err)
	}

	// Clear the FCM token (dead token cleanup)
	if err := repo.ClearFCMToken(ctx, user, fcmTok); err != nil {
		t.Fatalf("clear fcm token: %v", err)
	}

	// Verify token is now empty
	token, err = repo.GetActiveFCMTokenByUserID(ctx, user)
	if err != nil {
		t.Fatalf("get token after clear: %v", err)
	}
	if token != "" {
		t.Errorf("expected empty token after clear, got %s", token)
	}
}

// TestPostgresRepository_ClearFCMToken_ScopedToUser pins the fix for a token being cleared for
// every session holding that value, not only the caller's user. Two different app installs can
// legitimately share the same FCM registration token (e.g. sign-out on user A, sign-in as user B
// on the same phone), so clearing must be scoped to the user the worker is currently processing.
func TestPostgresRepository_ClearFCMToken_ScopedToUser(t *testing.T) {
	pool := testPool(t)
	cleanup := newCleanup(t, pool)
	ctx := context.Background()
	repo := New(pool)

	user1 := createTestUser(t, ctx, pool, cleanup, "Shared Token User 1")
	user2 := createTestUser(t, ctx, pool, cleanup, "Shared Token User 2")

	sharedTok := "fcm-shared-device-token"
	for _, uid := range []string{user1, user2} {
		_, err := pool.Exec(ctx, `
			INSERT INTO sessions (user_id, device_id, fcm_token, issued_at, expires_at)
			VALUES ($1, gen_random_uuid(), $2, now(), now() + interval '7 days')
		`, uid, sharedTok)
		if err != nil {
			t.Fatalf("insert session: %v", err)
		}
	}

	if err := repo.ClearFCMToken(ctx, user1, sharedTok); err != nil {
		t.Fatalf("clear fcm token: %v", err)
	}

	token1, err := repo.GetActiveFCMTokenByUserID(ctx, user1)
	if err != nil || token1 != "" {
		t.Errorf("expected user1 token cleared, got %q (err: %v)", token1, err)
	}

	token2, err := repo.GetActiveFCMTokenByUserID(ctx, user2)
	if err != nil || token2 != sharedTok {
		t.Errorf("expected user2 token untouched, got %q (err: %v)", token2, err)
	}
}

// TestPostgresRepository_CreateNotificationTx_RollsBackOnError pins the transactional-enqueue
// fix: CreateNotificationTx participates in a caller-managed transaction, so a rollback after it
// must leave no orphan row.
func TestPostgresRepository_CreateNotificationTx_RollsBackOnError(t *testing.T) {
	pool := testPool(t)
	cleanup := newCleanup(t, pool)
	ctx := context.Background()
	repo := New(pool)

	user := createTestUser(t, ctx, pool, cleanup, "Tx Rollback User")

	notif := &domain.Notification{UserID: user, Type: "bill_finalized", Title: "t", Body: "b"}
	err := repo.WithTx(ctx, func(ctx context.Context, ex repository.Executor) error {
		if err := repo.CreateNotificationTx(ctx, ex, notif); err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	if err == nil {
		t.Fatalf("expected WithTx to propagate the forced error")
	}

	page, err := repo.ListByUserID(ctx, user, pagination.NewOffsetPager(1, 20))
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if page.Total != 0 {
		t.Errorf("expected the notification insert to be rolled back, found %d rows", page.Total)
	}
}

// TestPostgresRepository_CreateNotificationTx_CommitsOnSuccess pins that a successful WithTx
// commits the notification row and GetNotificationByID can read it back afterwards, exactly what
// the River worker relies on to load title/body/payload by NotificationID.
func TestPostgresRepository_CreateNotificationTx_CommitsOnSuccess(t *testing.T) {
	pool := testPool(t)
	cleanup := newCleanup(t, pool)
	ctx := context.Background()
	repo := New(pool)

	user := createTestUser(t, ctx, pool, cleanup, "Tx Commit User")

	notif := &domain.Notification{UserID: user, Type: "bill_finalized", Title: "t", Body: "b"}
	err := repo.WithTx(ctx, func(ctx context.Context, ex repository.Executor) error {
		return repo.CreateNotificationTx(ctx, ex, notif)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notif.ID == "" {
		t.Fatalf("expected notification ID to be populated after commit")
	}

	got, err := repo.GetNotificationByID(ctx, notif.ID)
	if err != nil {
		t.Fatalf("get notification by id: %v", err)
	}
	if got.Title != "t" || got.Body != "b" {
		t.Errorf("unexpected notification content: %+v", got)
	}
}
