package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	platformmetrics "paysplit-backend/internal/platform/metrics"
)

const (
	listenerBackoffBase  = 500 * time.Millisecond
	listenerBackoffMax   = 30 * time.Second
	listenerHealthyReset = 30 * time.Second
	listenerCleanupLimit = 3 * time.Second
)

// NotificationHandler giải mã và phát một payload của channel tương ứng.
// Handler phải hoàn tất nhanh vì listener dispatch đồng bộ để giữ thứ tự nhận.
type NotificationHandler func(context.Context, string) error

type notificationSession interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	WaitForNotification(context.Context) (*pgconn.Notification, error)
	Release()
	Destroy(context.Context) error
}

type poolNotificationSession struct {
	conn *pgxpool.Conn
}

func (s *poolNotificationSession) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return s.conn.Exec(ctx, sql, args...)
}

func (s *poolNotificationSession) WaitForNotification(ctx context.Context) (*pgconn.Notification, error) {
	return s.conn.Conn().WaitForNotification(ctx)
}

func (s *poolNotificationSession) Release() {
	s.conn.Release()
}

func (s *poolNotificationSession) Destroy(ctx context.Context) error {
	return s.conn.Hijack().Close(ctx)
}

// PostgresNotificationListener sở hữu đúng một PostgreSQL session và đăng ký
// tất cả channel trước khi chuyển sang trạng thái connected.
type PostgresNotificationListener struct {
	pool         *pgxpool.Pool
	handlers     map[string]NotificationHandler
	channels     []string
	onDisconnect func()
	acquire      func(context.Context) (notificationSession, error)

	connected atomic.Bool
	ready     chan struct{}
	readyOnce sync.Once
}

func NewPostgresNotificationListener(
	pool *pgxpool.Pool,
	handlers map[string]NotificationHandler,
	onDisconnect func(),
) (*PostgresNotificationListener, error) {
	if pool == nil {
		return nil, errors.New("notification listener pool must not be nil")
	}
	if len(handlers) == 0 {
		return nil, errors.New("notification listener handlers must not be empty")
	}

	listener := &PostgresNotificationListener{
		pool:         pool,
		handlers:     make(map[string]NotificationHandler, len(handlers)),
		channels:     make([]string, 0, len(handlers)),
		onDisconnect: onDisconnect,
		ready:        make(chan struct{}),
	}
	for channel, handler := range handlers {
		if channel == "" || handler == nil {
			return nil, errors.New("notification listener channel and handler must not be empty")
		}
		listener.handlers[channel] = handler
		listener.channels = append(listener.channels, channel)
	}
	sort.Strings(listener.channels)
	listener.acquire = func(ctx context.Context) (notificationSession, error) {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return nil, err
		}
		return &poolNotificationSession{conn: conn}, nil
	}
	return listener, nil
}

// Ready đóng sau lần đầu listener đăng ký thành công mọi channel.
func (l *PostgresNotificationListener) Ready() <-chan struct{} {
	return l.ready
}

// Connected báo listener đang giữ session LISTEN healthy, dùng cho cửa vào SSE
// trước khi ghi header. Không thay Ping của readiness probe.
func (l *PostgresNotificationListener) Connected() bool {
	return l != nil && l.connected.Load()
}

// ComposeHandlers chạy lần lượt mọi handler của một channel. Lỗi đầu tiên được
// trả về sau khi tất cả handler đã chạy, để một notify nuôi cả hub cũ và hub mới.
func ComposeHandlers(handlers ...NotificationHandler) NotificationHandler {
	return func(ctx context.Context, payload string) error {
		var first error
		for _, handler := range handlers {
			if handler == nil {
				continue
			}
			if err := handler(ctx, payload); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
}

// Ping kiểm tra cả trạng thái listener và khả năng truy cập database để dùng
// trực tiếp cho readiness probe.
func (l *PostgresNotificationListener) Ping(ctx context.Context) error {
	if l == nil || !l.connected.Load() {
		return errors.New("PostgreSQL notification listener is disconnected")
	}
	return l.pool.Ping(ctx)
}

// Run duy trì listener cho đến khi context bị hủy. Mỗi lần reconnect dùng một
// session mới và đăng ký lại toàn bộ channel.
func (l *PostgresNotificationListener) Run(ctx context.Context) error {
	attempt := 0
	for {
		connectedAt, reason, channel, err := l.runSession(ctx)
		if ctx.Err() != nil {
			l.setConnected(false)
			return nil
		}
		if err == nil {
			return nil
		}

		l.setConnected(false)
		if !connectedAt.IsZero() {
			if l.onDisconnect != nil {
				l.onDisconnect()
			}
			if time.Since(connectedAt) >= listenerHealthyReset {
				attempt = 0
			}
		}

		platformmetrics.RecordDBListenerReconnect(reason)
		backoff := listenerBackoff(attempt)
		log.Printf("event=postgres_listener_reconnect channel=%s reason=%s attempt=%d backoff=%s", channel, reason, attempt, backoff)
		attempt++

		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil
		}
	}
}

func (l *PostgresNotificationListener) runSession(ctx context.Context) (time.Time, string, string, error) {
	session, err := l.acquire(ctx)
	if err != nil {
		return time.Time{}, "acquire", "all", fmt.Errorf("acquire notification listener connection: %w", err)
	}
	cleanRelease := false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), listenerCleanupLimit)
		defer cancel()
		if cleanRelease {
			if _, cleanupErr := session.Exec(cleanupCtx, "UNLISTEN *"); cleanupErr == nil {
				session.Release()
				return
			}
		}
		_ = session.Destroy(cleanupCtx)
	}()

	for _, channel := range l.channels {
		if _, err = session.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
			return time.Time{}, "listen", channel, fmt.Errorf("listen %s: %w", channel, err)
		}
	}

	connectedAt := time.Now()
	l.setConnected(true)
	l.readyOnce.Do(func() { close(l.ready) })

	for {
		notification, waitErr := session.WaitForNotification(ctx)
		if waitErr != nil {
			l.setConnected(false)
			if ctx.Err() != nil {
				cleanRelease = true
				return connectedAt, "", "all", ctx.Err()
			}
			return connectedAt, "wait", "all", fmt.Errorf("wait for PostgreSQL notification: %w", waitErr)
		}

		handler, ok := l.handlers[notification.Channel]
		if !ok {
			continue
		}
		if handlerErr := handler(ctx, notification.Payload); handlerErr != nil {
			platformmetrics.RecordDBListenerInvalidPayload(notification.Channel)
			log.Printf("event=postgres_listener_invalid_payload channel=%s reason=%q", notification.Channel, handlerErr.Error())
		}
	}
}

func (l *PostgresNotificationListener) setConnected(connected bool) {
	l.connected.Store(connected)
	platformmetrics.SetDBListenerConnected(connected)
}

func listenerBackoff(attempt int) time.Duration {
	base := listenerBackoffBase
	for i := 0; i < attempt && base < listenerBackoffMax; i++ {
		if base > listenerBackoffMax/2 {
			base = listenerBackoffMax
			break
		}
		base *= 2
	}
	factor := 0.8 + rand.Float64()*0.4
	delay := time.Duration(float64(base) * factor)
	if delay > listenerBackoffMax {
		return listenerBackoffMax
	}
	return delay
}
