package usecase

import (
	"context"
	"encoding/json"
	"testing"

	"paysplit-backend/internal/modules/group/domain"
)

func TestSync_ServesDeltaWhenClientIsCloseEnough(t *testing.T) {
	repo := &fakeRepo{
		getSyncCursorFn: func(ctx context.Context, groupID, callerUserID string) (domain.SyncCursor, error) {
			return domain.SyncCursor{Current: 7, Oldest: 3}, nil
		},
		listEventsSinceFn: func(ctx context.Context, groupID string, since int64, limit int) ([]domain.SyncEvent, error) {
			if since != 5 {
				t.Fatalf("ListEventsSince since = %d, want 5", since)
			}
			return []domain.SyncEvent{
				{Version: 6, Type: domain.EventMemberJoined, Payload: json.RawMessage(`{}`)},
				{Version: 7, Type: domain.EventMemberLeft, Payload: json.RawMessage(`{}`)},
			}, nil
		},
	}

	page, err := newTestService(t, repo).Sync(context.Background(), SyncInput{GroupID: "g", CallerUserID: "u", Since: 5})
	if err != nil {
		t.Fatal(err)
	}
	if page.Mode != domain.SyncModeDelta {
		t.Fatalf("mode = %q, want delta", page.Mode)
	}
	if len(page.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(page.Events))
	}
	// Version trả về phải là version của sự kiện cuối cùng đã gửi, không phải
	// version hiện tại của nhóm: client chỉ được tiến tới điểm nó thực sự nhận.
	if page.Version != 7 {
		t.Fatalf("version = %d, want 7", page.Version)
	}
}

func TestSync_ReturnsCurrentVersionWhenAlreadyCaughtUp(t *testing.T) {
	repo := &fakeRepo{
		getSyncCursorFn: func(ctx context.Context, groupID, callerUserID string) (domain.SyncCursor, error) {
			return domain.SyncCursor{Current: 4, Oldest: 1}, nil
		},
		listEventsSinceFn: func(ctx context.Context, groupID string, since int64, limit int) ([]domain.SyncEvent, error) {
			return nil, nil
		},
	}

	page, err := newTestService(t, repo).Sync(context.Background(), SyncInput{GroupID: "g", CallerUserID: "u", Since: 4})
	if err != nil {
		t.Fatal(err)
	}
	if page.Mode != domain.SyncModeDelta || len(page.Events) != 0 || page.Version != 4 {
		t.Fatalf("unexpected page: %+v", page)
	}
}

// Ba đường dẫn tới snapshot. fakeRepo sẽ tự fail nếu ListEventsSince bị gọi,
// nên mỗi case cũng chứng minh service không đọc nhật ký một cách vô ích.
func TestSync_FallsBackToSnapshot(t *testing.T) {
	tests := []struct {
		name   string
		cursor domain.SyncCursor
		since  int64
	}{
		{"client chưa có version", domain.SyncCursor{Current: 9, Oldest: 1}, 0},
		{"client giữ version tương lai", domain.SyncCursor{Current: 9, Oldest: 1}, 12},
		{"nhật ký đã bị dọn qua mốc since", domain.SyncCursor{Current: 9, Oldest: 6}, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{
				getSyncCursorFn: func(ctx context.Context, groupID, callerUserID string) (domain.SyncCursor, error) {
					return tt.cursor, nil
				},
				getGroupDetailFn: func(ctx context.Context, groupID, callerUserID string) (*domain.GroupDetail, error) {
					return &domain.GroupDetail{Version: tt.cursor.Current}, nil
				},
			}

			page, err := newTestService(t, repo).Sync(context.Background(), SyncInput{GroupID: "g", CallerUserID: "u", Since: tt.since})
			if err != nil {
				t.Fatal(err)
			}
			if page.Mode != domain.SyncModeSnapshot || page.Snapshot == nil {
				t.Fatalf("unexpected page: %+v", page)
			}
			if page.Version != tt.cursor.Current {
				t.Fatalf("version = %d, want %d", page.Version, tt.cursor.Current)
			}
		})
	}
}

// Delta bị cắt vì chạm limit sẽ khiến client tưởng nó đã bắt kịp trong khi vẫn
// còn thiếu, nên phải chuyển sang snapshot thay vì gửi một phần.
func TestSync_UpgradesTruncatedDeltaToSnapshot(t *testing.T) {
	const limit = 2
	repo := &fakeRepo{
		getSyncCursorFn: func(ctx context.Context, groupID, callerUserID string) (domain.SyncCursor, error) {
			return domain.SyncCursor{Current: 100, Oldest: 1}, nil
		},
		listEventsSinceFn: func(ctx context.Context, groupID string, since int64, limit int) ([]domain.SyncEvent, error) {
			return []domain.SyncEvent{{Version: 2}, {Version: 3}}, nil
		},
		getGroupDetailFn: func(ctx context.Context, groupID, callerUserID string) (*domain.GroupDetail, error) {
			return &domain.GroupDetail{Version: 100}, nil
		},
	}

	page, err := newTestService(t, repo).Sync(context.Background(), SyncInput{GroupID: "g", CallerUserID: "u", Since: 1, Limit: limit})
	if err != nil {
		t.Fatal(err)
	}
	if page.Mode != domain.SyncModeSnapshot {
		t.Fatalf("mode = %q, want snapshot when the delta was truncated", page.Mode)
	}
}

func TestSync_RejectsBlankIdentifiersWithoutTouchingRepository(t *testing.T) {
	for _, in := range []SyncInput{{GroupID: "  ", CallerUserID: "u"}, {GroupID: "g", CallerUserID: " "}} {
		if _, err := newTestService(t, &fakeRepo{}).Sync(context.Background(), in); err != domain.ErrGroupNotFound {
			t.Fatalf("Sync(%+v) error = %v, want ErrGroupNotFound", in, err)
		}
	}
}
