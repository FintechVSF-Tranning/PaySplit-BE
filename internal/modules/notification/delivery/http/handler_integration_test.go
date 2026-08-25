package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	authdomain "paysplit-backend/internal/modules/auth/domain"
	notificationhttp "paysplit-backend/internal/modules/notification/delivery/http"
	notificationdomain "paysplit-backend/internal/modules/notification/domain"
	notificationrepository "paysplit-backend/internal/modules/notification/repository"
	notificationpostgres "paysplit-backend/internal/modules/notification/repository/postgres"
	notificationusecase "paysplit-backend/internal/modules/notification/usecase"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

type fakeVerifier struct{}

func (fakeVerifier) Verify(token string) (string, string, string, error) {
	if token == "" {
		return "", "", "", fmt.Errorf("empty token")
	}
	return token, "user", "session-" + token, nil
}

type fakeSessions struct{}

func (fakeSessions) ValidateSession(_ context.Context, userID, sessionID string, _ time.Time) (*authdomain.SessionIdentity, error) {
	return &authdomain.SessionIdentity{UserID: userID, Role: "user", SessionID: sessionID}, nil
}

func testHandler(t *testing.T) (stdhttp.Handler, *pgxpool.Pool, notificationrepository.Repository) {
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

	repo := notificationpostgres.New(pool)
	service := notificationusecase.NewService(repo, nil, nil)
	handler := notificationhttp.NewHandler(service)

	router := chi.NewRouter()
	liveAuth := authmw.Auth(fakeVerifier{}, fakeSessions{})
	router.Route("/api/v1", func(api chi.Router) {
		api.Route("/notifications", func(r chi.Router) { handler.RegisterRoutes(r, liveAuth) })
	})
	return router, pool, repo
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
	email := fmt.Sprintf("notif.http.test.%d.%d@example.invalid", time.Now().UnixNano(), seq)
	phone := fmt.Sprintf("+84%09d", (time.Now().UnixNano()+int64(seq*7919))%1000000000)
	var id string
	err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, display_name, phone_number, role, status, email_verified_at) VALUES ($1,'x',$2,$3,'user','active',now()) RETURNING id`, email, name, phone).Scan(&id)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	cleanup.trackUser(id)
	return id
}

func TestNotificationHTTP_EndToEndFlow(t *testing.T) {
	router, pool, repo := testHandler(t)
	cleanup := newCleanup(t, pool)
	ctx := context.Background()

	user1 := createTestUser(t, ctx, pool, cleanup, "Notif HTTP User")

	// 1. Initial unread count should be 0
	req := httptest.NewRequest(stdhttp.MethodGet, "/api/v1/notifications/unread-count", nil)
	req.Header.Set("Authorization", "Bearer "+user1)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	type unreadCountBody struct {
		UnreadCount int64 `json:"unread_count"`
	}
	var unreadResp struct {
		Data unreadCountBody `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &unreadResp)
	if unreadResp.Data.UnreadCount != 0 {
		t.Errorf("expected 0 unread, got %d", unreadResp.Data.UnreadCount)
	}

	// 2. Create 3 notifications for user1
	notif1 := &notificationdomain.Notification{
		UserID:  user1,
		Type:    "payment_reminder",
		Title:   "Nhắc nợ 1",
		Body:    "Vui lòng thanh toán 100.000đ",
		Payload: json.RawMessage(`{"amount":100000}`),
	}
	notif2 := &notificationdomain.Notification{
		UserID:  user1,
		Type:    "payment_confirmed",
		Title:   "Đã nhận tiền 2",
		Body:    "Thành công",
		Payload: json.RawMessage(`{}`),
	}
	notif3 := &notificationdomain.Notification{
		UserID: user1,
		Type:   "bill_finalized",
		Title:  "Hóa đơn 3",
		Body:   "Đã chốt bill",
	}
	if err := repo.CreateNotification(ctx, notif1); err != nil {
		t.Fatalf("create notif1: %v", err)
	}
	if err := repo.CreateNotification(ctx, notif2); err != nil {
		t.Fatalf("create notif2: %v", err)
	}
	if err := repo.CreateNotification(ctx, notif3); err != nil {
		t.Fatalf("create notif3: %v", err)
	}

	// 3. Check unread count is 3
	req = httptest.NewRequest(stdhttp.MethodGet, "/api/v1/notifications/unread-count", nil)
	req.Header.Set("Authorization", "Bearer "+user1)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &unreadResp)
	if unreadResp.Data.UnreadCount != 3 {
		t.Errorf("expected 3 unread, got %d", unreadResp.Data.UnreadCount)
	}

	// 4. List notifications with pagination
	req = httptest.NewRequest(stdhttp.MethodGet, "/api/v1/notifications?page=1&page_size=2", nil)
	req.Header.Set("Authorization", "Bearer "+user1)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	type notificationsPage struct {
		Items []struct {
			ID     string  `json:"id"`
			Title  string  `json:"title"`
			Type   string  `json:"type"`
			ReadAt *string `json:"read_at"`
		} `json:"items"`
		Meta struct {
			TotalItems int64 `json:"total_items"`
			Page       int   `json:"page"`
			PageSize   int   `json:"page_size"`
			TotalPages int   `json:"total_pages"`
		} `json:"meta"`
	}
	var listResp struct {
		Data notificationsPage `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listResp)
	if listResp.Data.Meta.TotalItems != 3 {
		t.Errorf("expected total_items=3, got %d", listResp.Data.Meta.TotalItems)
	}
	if len(listResp.Data.Items) != 2 {
		t.Fatalf("expected 2 items on page 1, got %d", len(listResp.Data.Items))
	}

	// 5. Mark single notification as read
	req = httptest.NewRequest(stdhttp.MethodPatch, fmt.Sprintf("/api/v1/notifications/%s/read", notif1.ID), nil)
	req.Header.Set("Authorization", "Bearer "+user1)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200 when marking notif1 as read, got %d: %s", rec.Code, rec.Body.String())
	}

	// Unread count should now be 2
	req = httptest.NewRequest(stdhttp.MethodGet, "/api/v1/notifications/unread-count", nil)
	req.Header.Set("Authorization", "Bearer "+user1)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &unreadResp)
	if unreadResp.Data.UnreadCount != 2 {
		t.Errorf("expected 2 unread after single read, got %d", unreadResp.Data.UnreadCount)
	}

	// 6. Mark all as read
	req = httptest.NewRequest(stdhttp.MethodPatch, "/api/v1/notifications/read-all", nil)
	req.Header.Set("Authorization", "Bearer "+user1)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200 when mark all as read, got %d: %s", rec.Code, rec.Body.String())
	}

	// Unread count should now be 0
	req = httptest.NewRequest(stdhttp.MethodGet, "/api/v1/notifications/unread-count", nil)
	req.Header.Set("Authorization", "Bearer "+user1)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &unreadResp)
	if unreadResp.Data.UnreadCount != 0 {
		t.Errorf("expected 0 unread after mark all, got %d", unreadResp.Data.UnreadCount)
	}

	// 7. Test unauthorized without bearer token
	req = httptest.NewRequest(stdhttp.MethodGet, "/api/v1/notifications", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != stdhttp.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized without token, got %d", rec.Code)
	}
}

func readBody(r io.Reader) string {
	b, _ := io.ReadAll(r)
	return string(b)
}
