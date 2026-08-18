package http

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event đại diện cho một sự kiện realtime phát qua Server-Sent Events (SSE).
type Event struct {
	Type   string    `json:"type"`
	BillID uuid.UUID `json:"bill_id"`
	Data   any       `json:"data"`
}

// Broadcaster định nghĩa interface phát và đăng ký nhận sự kiện realtime.
type Broadcaster interface {
	Subscribe(billID uuid.UUID) (chan Event, func())
	Publish(event Event)
	Broadcast(billID uuid.UUID, eventType string, data any)
}

// Hub quản lý danh sách kết nối SSE theo từng bill_id và hỗ trợ đồng bộ qua PostgreSQL LISTEN/NOTIFY.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[uuid.UUID]map[chan Event]struct{}
	pool        *pgxpool.Pool
}

// NewHub khởi tạo một Hub mới.
func NewHub(pool *pgxpool.Pool) *Hub {
	return &Hub{
		subscribers: make(map[uuid.UUID]map[chan Event]struct{}),
		pool:        pool,
	}
}

// Subscribe đăng ký lắng nghe sự kiện của một hóa đơn cụ thể.
// Trả về channel nhận Event và hàm hủy đăng ký (cleanup).
func (h *Hub) Subscribe(billID uuid.UUID) (chan Event, func()) {
	ch := make(chan Event, 16)

	h.mu.Lock()
	if _, ok := h.subscribers[billID]; !ok {
		h.subscribers[billID] = make(map[chan Event]struct{})
	}
	h.subscribers[billID][ch] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if subs, ok := h.subscribers[billID]; ok {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(h.subscribers, billID)
			}
		}
		close(ch)
	}

	return ch, unsubscribe
}

// Publish gửi sự kiện đến toàn bộ client đang kết nối với billID tương ứng trên tiến trình này.
func (h *Hub) Publish(event Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	subs, ok := h.subscribers[event.BillID]
	if !ok || len(subs) == 0 {
		return
	}

	for ch := range subs {
		select {
		case ch <- event:
		default:
			// Nếu buffer channel đầy (client chậm), bỏ qua để không block server
		}
	}
}

// Broadcast phát sự kiện đến các client kết nối qua SSE.
// Khi có PostgreSQL connection pool, sự kiện được phát qua PostgreSQL NOTIFY để tất cả
// các instance (bao gồm cả instance hiện tại thông qua listener) nhận và gửi đến client đúng 1 lần.
func (h *Hub) Broadcast(billID uuid.UUID, eventType string, data any) {
	event := Event{
		Type:   eventType,
		BillID: billID,
		Data:   data,
	}

	if h.pool != nil {
		go func() {
			ctx := context.Background()
			payloadBytes, err := json.Marshal(event)
			if err != nil {
				return
			}
			_, _ = h.pool.Exec(ctx, "SELECT pg_notify('bill_events', $1)", string(payloadBytes))
		}()
	} else {
		// Fallback cho môi trường không có PostgreSQL connection pool (ví dụ unit test)
		h.Publish(event)
	}
}

// StartPostgresListener lắng nghe kênh pg_notify 'bill_events' từ các instance khác hoặc background worker.
func (h *Hub) StartPostgresListener(ctx context.Context) error {
	if h.pool == nil {
		return nil
	}

	conn, err := h.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn for pg_notify listener: %w", err)
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, "LISTEN bill_events")
	if err != nil {
		return fmt.Errorf("listen bill_events: %w", err)
	}

	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // Context cancelled
			}
			return fmt.Errorf("wait for pg notification: %w", err)
		}

		var event Event
		if err := json.Unmarshal([]byte(notification.Payload), &event); err == nil {
			h.Publish(event)
		}
	}
}
