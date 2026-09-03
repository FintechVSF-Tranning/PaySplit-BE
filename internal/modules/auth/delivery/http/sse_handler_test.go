package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"paysplit-backend/internal/platform/realtime"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

type fakeListener struct{ connected bool }

func (f fakeListener) Connected() bool { return f.connected }

type stubNotify struct{}

func (stubNotify) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("SELECT"), nil
}

type failNotify struct{}

func (failNotify) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, context.DeadlineExceeded
}

func TestStreamUserEventsRejectsUnhealthyListenerBeforeHeaders(t *testing.T) {
	hub := NewHub(nil)
	publisher := &realtime.Publisher{Enabled: true}
	handler := NewSSEHandler(hub, publisher, stubNotify{}, fakeListener{connected: false}, time.Second, time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/events", nil)
	req = req.WithContext(authmw.WithAuthContext(req.Context(), uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String(), "user"))
	rec := httptest.NewRecorder()
	handler.StreamUserEvents(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Content-Type") == "text/event-stream" {
		t.Fatal("wrote SSE headers while listener was unhealthy")
	}
}

func TestStreamUserEventsRejectsMissingAuthBeforeHeaders(t *testing.T) {
	// covers: AC-14
	handler := NewSSEHandler(NewHub(nil), &realtime.Publisher{Enabled: true}, stubNotify{}, fakeListener{connected: true}, time.Second, time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/events", nil)
	rec := httptest.NewRecorder()
	handler.StreamUserEvents(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if rec.Header().Get("Content-Type") == "text/event-stream" {
		t.Fatal("wrote SSE headers without auth")
	}
}

func TestStreamUserEventsKeepsOldStreamWhenReplacePublishFails(t *testing.T) {
	// covers: AC-14
	hub := NewHub(nil)
	userID := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())
	oldID := uuid.Must(uuid.NewV7())
	oldCh, _, _ := hub.RegisterPaused(userID, sid, oldID)
	handler := NewSSEHandler(hub, &realtime.Publisher{Enabled: true}, failNotify{}, fakeListener{connected: true}, time.Second, time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/events", nil)
	req = req.WithContext(authmw.WithAuthContext(req.Context(), userID.String(), sid.String(), "user"))
	rec := httptest.NewRecorder()
	handler.StreamUserEvents(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Content-Type") == "text/event-stream" {
		t.Fatal("wrote SSE headers after replace publish failed")
	}
	select {
	case <-oldCh:
		t.Fatal("old stream was closed after a failed replace publish")
	default:
	}
}

func TestSIDLimiterAllowsTenThenBlocks(t *testing.T) {
	// covers: AC-14
	limiter := newSIDLimiter(10, time.Minute)
	sid := uuid.Must(uuid.NewV7())
	for i := 0; i < 10; i++ {
		if _, limited := limiter.allow(sid); limited {
			t.Fatalf("attempt %d was limited", i+1)
		}
	}
	retryAfter, limited := limiter.allow(sid)
	if !limited || retryAfter < 1 {
		t.Fatalf("11th limited=%t retryAfter=%d", limited, retryAfter)
	}
}

// hubNotify mô phỏng vòng NOTIFY/LISTEN: payload phát ra được đưa thẳng lại cho
// hub, đúng như listener PostgreSQL làm trong tiến trình thật.
type hubNotify struct{ hub *Hub }

func (n hubNotify) Exec(ctx context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	if len(args) == 2 {
		if payload, ok := args[1].(string); ok {
			_ = n.hub.HandleUserNotification(ctx, payload)
		}
	}
	return pgconn.NewCommandTag("SELECT"), nil
}

func TestStreamUserEventsWritesReadyOnlyAfterAdmission(t *testing.T) {
	// covers: AC-14, AC-16
	hub := NewHub(nil)
	handler := NewSSEHandler(hub, &realtime.Publisher{Enabled: true}, hubNotify{hub: hub}, fakeListener{connected: true}, time.Hour, time.Hour)
	ctx, cancel := context.WithCancel(authmw.WithAuthContext(context.Background(), uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String(), "user"))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.StreamUserEvents(rec, req)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for !strings.Contains(rec.Body.String(), "event: ready") {
		select {
		case <-deadline:
			t.Fatalf("ready was never written, body = %q", rec.Body.String())
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after the client went away")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestStreamUserEventsRejectsWhenAdmissionTimesOut(t *testing.T) {
	// covers: AC-14
	// stubNotify nuốt payload nên control không bao giờ quay lại: kết nối phải bị
	// từ chối trước khi ghi header thay vì mở một stream ngoài thứ tự thay thế.
	hub := NewHub(nil)
	handler := NewSSEHandler(hub, &realtime.Publisher{Enabled: true}, stubNotify{}, fakeListener{connected: true}, time.Second, time.Minute)
	handler.admissionTimeout = 20 * time.Millisecond
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/events", nil)
	req = req.WithContext(authmw.WithAuthContext(req.Context(), uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String(), "user"))
	rec := httptest.NewRecorder()

	handler.StreamUserEvents(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Content-Type") == "text/event-stream" {
		t.Fatal("wrote SSE headers without admission")
	}
}

func TestStreamUserEventsRejectsWhenReplacedBeforeReady(t *testing.T) {
	// covers: AC-14
	// Kết nối bị một control mới hơn thay thế trong lúc còn chờ: nó thua và không
	// được phép ghi bất kỳ header SSE nào.
	hub := NewHub(nil)
	userID := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())
	newer := uuid.Must(uuid.NewV7())
	handler := NewSSEHandler(hub, &realtime.Publisher{Enabled: true}, replaceThenSupersede{hub: hub, sid: sid, newer: newer}, fakeListener{connected: true}, time.Second, time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/events", nil)
	req = req.WithContext(authmw.WithAuthContext(req.Context(), userID.String(), sid.String(), "user"))
	rec := httptest.NewRecorder()

	handler.StreamUserEvents(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Content-Type") == "text/event-stream" {
		t.Fatal("a superseded connection wrote SSE headers")
	}
}

// replaceThenSupersede áp control của kết nối hiện tại rồi lập tức áp một control
// mới hơn, mô phỏng một kết nối thứ hai commit ngay sau nó.
type replaceThenSupersede struct {
	hub   *Hub
	sid   uuid.UUID
	newer uuid.UUID
}

func (n replaceThenSupersede) Exec(ctx context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	if len(args) == 2 {
		if payload, ok := args[1].(string); ok {
			_ = n.hub.HandleUserNotification(ctx, payload)
		}
	}
	n.hub.ApplyReplace([]uuid.UUID{n.sid}, n.newer)
	return pgconn.NewCommandTag("SELECT"), nil
}
