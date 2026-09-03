package database

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeNotificationSession struct {
	mu            sync.Mutex
	execCalls     []string
	failExecAt    int
	waitErr       error
	notifications chan *pgconn.Notification
	released      bool
	destroyed     bool
}

func newFakeNotificationSession() *fakeNotificationSession {
	return &fakeNotificationSession{notifications: make(chan *pgconn.Notification, 4)}
}

func (s *fakeNotificationSession) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.execCalls = append(s.execCalls, sql)
	if s.failExecAt > 0 && len(s.execCalls) == s.failExecAt {
		return pgconn.CommandTag{}, errors.New("forced exec failure")
	}
	return pgconn.NewCommandTag("LISTEN"), nil
}

func (s *fakeNotificationSession) WaitForNotification(ctx context.Context) (*pgconn.Notification, error) {
	s.mu.Lock()
	if s.waitErr != nil {
		err := s.waitErr
		s.waitErr = nil
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()
	select {
	case notification := <-s.notifications:
		return notification, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *fakeNotificationSession) Release() {
	s.mu.Lock()
	s.released = true
	s.mu.Unlock()
}

func (s *fakeNotificationSession) Destroy(context.Context) error {
	s.mu.Lock()
	s.destroyed = true
	s.mu.Unlock()
	return nil
}

func testListener(session notificationSession, handlers map[string]NotificationHandler) *PostgresNotificationListener {
	listener := &PostgresNotificationListener{
		handlers: handlers,
		channels: []string{"bill_events", "group_events"},
		ready:    make(chan struct{}),
	}
	listener.acquire = func(context.Context) (notificationSession, error) { return session, nil }
	return listener
}

func TestPostgresNotificationListener_RoutesBothChannelsAndCleansSession(t *testing.T) {
	session := newFakeNotificationSession()
	received := make(chan string, 2)
	listener := testListener(session, map[string]NotificationHandler{
		"bill_events": func(_ context.Context, payload string) error {
			received <- "bill:" + payload
			return nil
		},
		"group_events": func(_ context.Context, payload string) error {
			received <- "group:" + payload
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	select {
	case <-listener.Ready():
	case <-time.After(time.Second):
		t.Fatal("listener did not become ready")
	}

	session.notifications <- &pgconn.Notification{Channel: "bill_events", Payload: "one"}
	session.notifications <- &pgconn.Notification{Channel: "group_events", Payload: "two"}
	for _, want := range []string{"bill:one", "group:two"} {
		select {
		case got := <-received:
			if got != want {
				t.Fatalf("routed payload = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.execCalls) != 3 || session.execCalls[2] != "UNLISTEN *" {
		t.Fatalf("exec calls = %v, want two LISTEN calls then UNLISTEN *", session.execCalls)
	}
	if !session.released || session.destroyed {
		t.Fatalf("clean session released=%t destroyed=%t", session.released, session.destroyed)
	}
}

func TestPostgresNotificationListener_PartialListenDestroysSession(t *testing.T) {
	session := newFakeNotificationSession()
	session.failExecAt = 2
	listener := testListener(session, map[string]NotificationHandler{
		"bill_events":  func(context.Context, string) error { return nil },
		"group_events": func(context.Context, string) error { return nil },
	})

	_, reason, channel, err := listener.runSession(context.Background())
	if err == nil || reason != "listen" || channel != "group_events" {
		t.Fatalf("runSession() = reason %q channel %q err %v", reason, channel, err)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.released || !session.destroyed {
		t.Fatalf("dirty session released=%t destroyed=%t", session.released, session.destroyed)
	}
}

func TestListenerBackoffStaysWithinConfiguredBounds(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		delay := listenerBackoff(attempt)
		if delay < 400*time.Millisecond || delay > listenerBackoffMax {
			t.Fatalf("listenerBackoff(%d) = %s, outside configured bounds", attempt, delay)
		}
	}
}

func TestPostgresNotificationListener_ReconnectsAndClosesSubscribersAfterWaitFailure(t *testing.T) {
	first := newFakeNotificationSession()
	first.waitErr = errors.New("connection reset")
	second := newFakeNotificationSession()
	listener := testListener(first, map[string]NotificationHandler{
		"bill_events":  func(context.Context, string) error { return nil },
		"group_events": func(context.Context, string) error { return nil },
	})
	acquiredSecond := make(chan struct{})
	acquires := 0
	listener.acquire = func(context.Context) (notificationSession, error) {
		acquires++
		if acquires == 1 {
			return first, nil
		}
		select {
		case <-acquiredSecond:
		default:
			close(acquiredSecond)
		}
		return second, nil
	}
	disconnected := make(chan struct{}, 1)
	listener.onDisconnect = func() { disconnected <- struct{}{} }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	select {
	case <-acquiredSecond:
	case <-time.After(2 * time.Second):
		t.Fatal("listener did not reconnect")
	}
	select {
	case <-disconnected:
	default:
		t.Fatal("disconnect callback was not called")
	}

	first.mu.Lock()
	if !first.destroyed || first.released {
		t.Fatalf("failed session released=%t destroyed=%t", first.released, first.destroyed)
	}
	first.mu.Unlock()

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	second.mu.Lock()
	defer second.mu.Unlock()
	if !second.released || second.destroyed {
		t.Fatalf("replacement session released=%t destroyed=%t", second.released, second.destroyed)
	}
}

func TestPostgresNotificationListener_ContextCancelSkipsOnDisconnect(t *testing.T) {
	session := newFakeNotificationSession()
	listener := testListener(session, map[string]NotificationHandler{
		"bill_events":  func(context.Context, string) error { return nil },
		"group_events": func(context.Context, string) error { return nil },
	})
	disconnected := 0
	listener.onDisconnect = func() { disconnected++ }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	select {
	case <-listener.Ready():
	case <-time.After(time.Second):
		t.Fatal("listener did not become ready")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if disconnected != 0 {
		t.Fatalf("onDisconnect called %d times on clean cancel, want 0", disconnected)
	}
}

func TestNewPostgresNotificationListener_RejectsEmptyRegistry(t *testing.T) {
	// covers: AC-1
	if _, err := NewPostgresNotificationListener(nil, map[string]NotificationHandler{
		"bill_events": func(context.Context, string) error { return nil },
	}, nil); err == nil {
		t.Fatal("nil pool accepted")
	}
	if _, err := NewPostgresNotificationListener(&pgxpool.Pool{}, map[string]NotificationHandler{}, nil); err == nil {
		t.Fatal("empty handlers accepted")
	}
	if _, err := NewPostgresNotificationListener(&pgxpool.Pool{}, map[string]NotificationHandler{
		"": func(context.Context, string) error { return nil },
	}, nil); err == nil {
		t.Fatal("blank channel accepted")
	}
}

func TestPostgresNotificationListener_PingReportsDisconnected(t *testing.T) {
	// covers: AC-3
	listener := testListener(newFakeNotificationSession(), map[string]NotificationHandler{
		"bill_events":  func(context.Context, string) error { return nil },
		"group_events": func(context.Context, string) error { return nil },
	})
	if err := listener.Ping(context.Background()); err == nil {
		t.Fatal("disconnected listener reported healthy")
	}
}

func TestPostgresNotificationListener_HandlerErrorKeepsSession(t *testing.T) {
	// covers: AC-4
	session := newFakeNotificationSession()
	received := make(chan string, 1)
	listener := testListener(session, map[string]NotificationHandler{
		"bill_events": func(_ context.Context, payload string) error {
			if payload == "bad" {
				return errors.New("invalid_json")
			}
			received <- payload
			return nil
		},
		"group_events": func(context.Context, string) error { return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	select {
	case <-listener.Ready():
	case <-time.After(time.Second):
		t.Fatal("listener did not become ready")
	}

	session.notifications <- &pgconn.Notification{Channel: "bill_events", Payload: "bad"}
	session.notifications <- &pgconn.Notification{Channel: "bill_events", Payload: "good"}
	select {
	case got := <-received:
		if got != "good" {
			t.Fatalf("payload = %q, want good", got)
		}
	case <-time.After(time.Second):
		t.Fatal("valid payload after a handler error never arrived")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if !session.released || session.destroyed {
		t.Fatalf("session after handler error released=%t destroyed=%t", session.released, session.destroyed)
	}
}

func TestPostgresNotificationListener_UnlistenFailureDestroysSession(t *testing.T) {
	// covers: AC-9
	session := newFakeNotificationSession()
	session.failExecAt = 3
	listener := testListener(session, map[string]NotificationHandler{
		"bill_events":  func(context.Context, string) error { return nil },
		"group_events": func(context.Context, string) error { return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	select {
	case <-listener.Ready():
	case <-time.After(time.Second):
		t.Fatal("listener did not become ready")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.execCalls) < 3 || session.execCalls[2] != "UNLISTEN *" {
		t.Fatalf("exec calls = %v, want UNLISTEN * after two LISTEN calls", session.execCalls)
	}
	if session.released || !session.destroyed {
		t.Fatalf("failed UNLISTEN released=%t destroyed=%t", session.released, session.destroyed)
	}
}
