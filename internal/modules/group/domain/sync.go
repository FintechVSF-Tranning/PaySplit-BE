package domain

import (
	"encoding/json"
	"time"
)

// Các loại sự kiện đồng bộ của nhóm. Giá trị được ghi thẳng vào group_events và
// gửi nguyên văn ra SSE nên là một phần của hợp đồng API: chỉ thêm mới, không đổi tên.
const (
	EventMemberJoined      = "member_joined"
	EventMemberReactivated = "member_reactivated"
	EventMemberLeft        = "member_left"
	EventMemberRemoved     = "member_removed"
	EventCaptainTransfer   = "captain_transferred"
	EventGroupRenamed      = "group_renamed"
	EventGroupArchived     = "group_archived"
)

// Chế độ trả về của một lần đồng bộ.
const (
	// SyncModeDelta: client còn đủ gần, chỉ cần áp các sự kiện trả kèm.
	SyncModeDelta = "delta"
	// SyncModeSnapshot: client tụt quá xa (hoặc chưa có version), phải thay
	// toàn bộ state bằng snapshot.
	SyncModeSnapshot = "snapshot"
)

// SyncEvent là một dòng nhật ký của nhóm. Payload đi qua nguyên văn: nó được
// ghi một lần trong transaction của mutation và không bao giờ dựng lại lúc đọc,
// đúng nguyên tắc đang áp dụng cho Activity.
type SyncEvent struct {
	Version   int64
	Type      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// SyncPage là kết quả một lần catch-up. Đúng một trong Events/Snapshot có nghĩa,
// tùy theo Mode.
type SyncPage struct {
	// Version là roster_version của nhóm tại thời điểm đọc. Client lưu lại làm
	// điểm xuất phát cho lần đồng bộ kế tiếp.
	Version  int64
	Mode     string
	Events   []SyncEvent
	Snapshot *GroupDetail
}

// SyncCursor là cặp mốc quyết định delta hay snapshot: Current là version mới
// nhất, Oldest là version nhỏ nhất còn giữ trong nhật ký (0 khi nhật ký rỗng).
type SyncCursor struct {
	Current int64
	Oldest  int64
}

// CanServeDelta cho biết một client đang ở since có thể được phục vụ bằng delta
// hay không.
//
// since <= 0 luôn là snapshot, kể cả với nhóm chưa có sự kiện nào (version 0):
// client gửi 0 là client chưa có gì trong tay, phục vụ nó một delta rỗng sẽ để
// nó ở lại với danh sách thành viên trống.
func (c SyncCursor) CanServeDelta(since int64) bool {
	if since <= 0 || since > c.Current {
		return false
	}
	if since == c.Current {
		return true
	}
	// Còn thiếu ít nhất một sự kiện: nhật ký phải giữ được từ since+1 trở đi.
	return c.Oldest > 0 && c.Oldest <= since+1
}
