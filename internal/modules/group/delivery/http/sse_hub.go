package http

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// notifyChannel là kênh pg_notify mà tầng repository phát sự kiện nhóm vào,
// ngay bên trong transaction của mutation.
const notifyChannel = "group_events"

// subscriberBuffer là độ sâu hàng đợi cho mỗi kết nối SSE. Client chậm hơn mức
// này sẽ bị bỏ sự kiện — đúng thiết kế: version fencing khiến client tự phát
// hiện lỗ hổng và gọi /sync, không được để một client chậm chặn cả server.
const subscriberBuffer = 32

// Event là một sự kiện nhóm trên đường truyền. Version là thứ tự đơn điệu tăng
// trong phạm vi một nhóm; Data có thể nil khi envelope bị cắt vì vượt giới hạn
// 8000 byte của pg_notify, khi đó client sẽ thấy version nhảy cóc và tự catch-up.
type Event struct {
	GroupID uuid.UUID       `json:"group_id"`
	Version int64           `json:"version"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Broadcaster là cổng mà SSE handler dùng để đăng ký nhận sự kiện.
type Broadcaster interface {
	Subscribe(groupID uuid.UUID) (chan Event, func())
	Publish(event Event)
}

// Hub giữ danh sách kết nối SSE theo group_id trên tiến trình này và nhận sự
// kiện từ mọi tiến trình khác qua PostgreSQL LISTEN/NOTIFY.
//
// Khác với Hub của module bill, Hub này KHÔNG có hàm Broadcast: sự kiện nhóm
// luôn được phát bằng pg_notify ngay trong transaction của mutation, nên không
// tồn tại đường phát nào đi vòng qua nhật ký.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[uuid.UUID]map[chan Event]struct{}
	pool        *pgxpool.Pool
}

func NewHub(pool *pgxpool.Pool) *Hub {
	return &Hub{
		subscribers: make(map[uuid.UUID]map[chan Event]struct{}),
		pool:        pool,
	}
}

// Subscribe đăng ký nhận sự kiện của một nhóm. Trả về channel và hàm hủy đăng ký.
func (h *Hub) Subscribe(groupID uuid.UUID) (chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)

	h.mu.Lock()
	if _, ok := h.subscribers[groupID]; !ok {
		h.subscribers[groupID] = make(map[chan Event]struct{})
	}
	h.subscribers[groupID][ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if subs, ok := h.subscribers[groupID]; ok {
				delete(subs, ch)
				if len(subs) == 0 {
					delete(h.subscribers, groupID)
				}
			}
			close(ch)
		})
	}
}

// Publish đẩy sự kiện tới mọi kết nối của nhóm trên tiến trình này.
func (h *Hub) Publish(event Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for ch := range h.subscribers[event.GroupID] {
		select {
		case ch <- event:
		default:
			// Hàng đợi đầy: bỏ sự kiện thay vì chặn server. Client sẽ thấy
			// version nhảy cóc ở sự kiện kế tiếp và tự gọi /sync.
		}
	}
}

// StartPostgresListener lắng nghe kênh pg_notify và tự kết nối lại khi mạng
// hoặc database gặp sự cố.
func (h *Hub) StartPostgresListener(ctx context.Context) error {
	if h.pool == nil {
		return nil
	}

	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := h.listenLoop(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			select {
			case <-time.After(1 * time.Second):
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (h *Hub) listenLoop(ctx context.Context) error {
	conn, err := h.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn for group event listener: %w", err)
	}
	defer conn.Release()

	if _, err = conn.Exec(ctx, "LISTEN "+notifyChannel); err != nil {
		return fmt.Errorf("listen %s: %w", notifyChannel, err)
	}

	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("wait for group notification: %w", err)
		}

		var event Event
		if err := json.Unmarshal([]byte(notification.Payload), &event); err == nil {
			h.Publish(event)
		}
	}
}
