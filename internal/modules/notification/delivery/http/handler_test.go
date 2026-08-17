package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"paysplit-backend/internal/modules/notification/domain"
	"paysplit-backend/internal/modules/notification/usecase"
	transportmw "paysplit-backend/internal/transport/http/middleware"

	"github.com/brpaz/lib-go/pagination"
	"github.com/go-chi/chi/v5"
)

type mockRepo struct {
	unreadCount   int64
	markedReadID  string
	allMarkedRead bool
}

func (m *mockRepo) CreateNotification(ctx context.Context, notif *domain.Notification) error {
	return nil
}
func (m *mockRepo) ListByUserID(ctx context.Context, userID string, pager pagination.OffsetPager) (pagination.Page[domain.Notification], error) {
	items := []domain.Notification{
		{ID: "n-1", UserID: userID, Title: "Test", Body: "Body", CreatedAt: time.Now()},
	}
	return pagination.NewPage(items, 1, pager), nil
}
func (m *mockRepo) CountUnread(ctx context.Context, userID string) (int64, error) {
	return m.unreadCount, nil
}
func (m *mockRepo) MarkAsRead(ctx context.Context, userID, notificationID string) error {
	m.markedReadID = notificationID
	return nil
}
func (m *mockRepo) MarkAllAsRead(ctx context.Context, userID string) error {
	m.allMarkedRead = true
	return nil
}
func (m *mockRepo) GetActiveFCMTokenByUserID(ctx context.Context, userID string) (string, error) {
	return "", nil
}
func (m *mockRepo) ClearFCMToken(ctx context.Context, fcmToken string) error {
	return nil
}

func fakeAuthMiddleware(userID, sessionID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := transportmw.WithAuthContext(r.Context(), userID, sessionID, "user")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func TestListNotificationsEndpoint(t *testing.T) {
	repo := &mockRepo{}
	svc := usecase.NewService(repo, nil)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	r.Route("/api/v1/notifications", func(sub chi.Router) {
		handler.RegisterRoutes(sub, fakeAuthMiddleware("user-1", "session-1"))
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications?page=1&page_size=10", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetUnreadCountEndpoint(t *testing.T) {
	repo := &mockRepo{unreadCount: 7}
	svc := usecase.NewService(repo, nil)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	r.Route("/api/v1/notifications", func(sub chi.Router) {
		handler.RegisterRoutes(sub, fakeAuthMiddleware("user-1", "session-1"))
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/unread-count", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var res map[string]int64
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res["unread_count"] != 7 {
		t.Errorf("expected unread count 7, got %d", res["unread_count"])
	}
}

func TestMarkAsReadEndpoint(t *testing.T) {
	repo := &mockRepo{}
	svc := usecase.NewService(repo, nil)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	r.Route("/api/v1/notifications", func(sub chi.Router) {
		handler.RegisterRoutes(sub, fakeAuthMiddleware("user-1", "session-1"))
	})

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/notifications/notif-99/read", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if repo.markedReadID != "notif-99" {
		t.Errorf("expected notif-99, got %s", repo.markedReadID)
	}
}

func TestMarkAllAsReadEndpoint(t *testing.T) {
	repo := &mockRepo{}
	svc := usecase.NewService(repo, nil)
	handler := NewHandler(svc)

	r := chi.NewRouter()
	r.Route("/api/v1/notifications", func(sub chi.Router) {
		handler.RegisterRoutes(sub, fakeAuthMiddleware("user-1", "session-1"))
	})

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/notifications/read-all", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !repo.allMarkedRead {
		t.Errorf("expected all marked read")
	}
}
