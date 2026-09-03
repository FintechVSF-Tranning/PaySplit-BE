package http

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	NotificationChannel = "bill_events"
	subscriberBuffer    = 16
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
	ch := make(chan Event, subscriberBuffer)

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
			if _, exists := subs[ch]; exists {
				delete(subs, ch)
				close(ch)
				if len(subs) == 0 {
					delete(h.subscribers, billID)
				}
			}
		}
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
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			payloadBytes, err := json.Marshal(event)
			if err != nil {
				return
			}
			_, _ = h.pool.Exec(ctx, "SELECT pg_notify($1, $2)", NotificationChannel, string(payloadBytes))
		}()
	} else {
		// Fallback cho môi trường không có PostgreSQL connection pool (ví dụ unit test)
		h.Publish(event)
	}
}

// HandlePostgresNotification giải mã và kiểm tra semantic envelope trước khi
// phát tới subscriber cục bộ. Lỗi trả về không chứa raw payload.
func (h *Hub) HandlePostgresNotification(_ context.Context, payload string) error {
	var event Event
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return errors.New("invalid_json")
	}
	if event.BillID == uuid.Nil {
		return errors.New("missing_bill_id")
	}
	if strings.TrimSpace(event.Type) == "" {
		return errors.New("missing_type")
	}
	h.Publish(event)
	return nil
}

// CloseSubscribers đóng mọi stream cục bộ để client reconnect và lấy snapshot
// sau khi PostgreSQL listener bị gián đoạn.
func (h *Hub) CloseSubscribers() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for billID, subscribers := range h.subscribers {
		for ch := range subscribers {
			close(ch)
		}
		delete(h.subscribers, billID)
	}
}
