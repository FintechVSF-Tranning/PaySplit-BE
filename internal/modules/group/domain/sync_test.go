package domain

import "testing"

// CanServeDelta là hàng rào quyết định client được phục vụ bằng delta hay phải
// nhận snapshot. Sai ở đây nghĩa là client hoặc mất sự kiện vĩnh viễn, hoặc
// nhận snapshot đắt tiền không cần thiết.
func TestSyncCursor_CanServeDelta(t *testing.T) {
	tests := []struct {
		name   string
		cursor SyncCursor
		since  int64
		want   bool
	}{
		{"client chưa có version phải nhận snapshot", SyncCursor{Current: 5, Oldest: 1}, 0, false},
		{"since âm là dữ liệu hỏng, phải nhận snapshot", SyncCursor{Current: 5, Oldest: 1}, -1, false},
		{"client đã bắt kịp thì delta rỗng là đủ", SyncCursor{Current: 5, Oldest: 1}, 5, true},
		{"client giữ version tương lai phải nhận snapshot", SyncCursor{Current: 5, Oldest: 1}, 6, false},
		{"nhật ký còn đủ từ since+1 thì phục vụ delta", SyncCursor{Current: 5, Oldest: 3}, 2, true},
		{"nhật ký đã bị dọn qua mốc since thì phải snapshot", SyncCursor{Current: 5, Oldest: 3}, 1, false},
		{"nhật ký rỗng nhưng nhóm có version thì phải snapshot", SyncCursor{Current: 5, Oldest: 0}, 2, false},
		{"nhóm chưa từng có sự kiện: since 0 vẫn là snapshot", SyncCursor{Current: 0, Oldest: 0}, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cursor.CanServeDelta(tt.since); got != tt.want {
				t.Fatalf("CanServeDelta(%d) = %v, want %v", tt.since, got, tt.want)
			}
		})
	}
}
