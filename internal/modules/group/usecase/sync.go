package usecase

import (
	"context"
	"strings"

	"paysplit-backend/internal/modules/group/domain"
)

// defaultSyncEventLimit là số sự kiện tối đa trả về trong một lần catch-up.
// Client cần nhiều hơn thế thì nhận snapshot rẻ hơn là phát lại từng bước.
const defaultSyncEventLimit = 200

type SyncInput struct {
	GroupID      string
	CallerUserID string
	// Since là version client đang giữ. 0 nghĩa là chưa có gì và luôn dẫn tới
	// snapshot; giá trị âm bị coi là không hợp lệ.
	Since int64
	Limit int
}

// Sync là điểm catch-up của giao thức: nó quyết định client được phục vụ bằng
// delta hay phải thay toàn bộ state bằng snapshot.
//
// Ba trường hợp dẫn tới snapshot:
//   - Since <= 0: client chưa có điểm xuất phát.
//   - Since > version hiện tại: client giữ version của một nhóm khác, hoặc dữ
//     liệu cục bộ đã hỏng — không được im lặng bỏ qua.
//   - Nhật ký đã bị dọn qua mốc Since: không còn đủ sự kiện để phát lại.
func (s *Service) Sync(ctx context.Context, in SyncInput) (*domain.SyncPage, error) {
	groupID := strings.TrimSpace(in.GroupID)
	callerUserID := strings.TrimSpace(in.CallerUserID)
	if groupID == "" || callerUserID == "" {
		return nil, domain.ErrGroupNotFound
	}

	cursor, err := s.repo.GetSyncCursor(ctx, groupID, callerUserID)
	if err != nil {
		return nil, err
	}

	if !cursor.CanServeDelta(in.Since) {
		return s.snapshot(ctx, groupID, callerUserID)
	}

	limit := in.Limit
	if limit <= 0 || limit > defaultSyncEventLimit {
		limit = defaultSyncEventLimit
	}
	events, err := s.repo.ListEventsSince(ctx, groupID, in.Since, limit)
	if err != nil {
		return nil, err
	}
	// Nhật ký bị cắt ngang vì chạm limit: gửi delta một phần sẽ khiến client
	// tưởng nó đã bắt kịp. Trả snapshot dứt điểm thay vì bắt nó lặp nhiều vòng.
	if len(events) == limit && cursor.Current > in.Since+int64(limit) {
		return s.snapshot(ctx, groupID, callerUserID)
	}

	version := cursor.Current
	if len(events) > 0 {
		version = events[len(events)-1].Version
	}
	return &domain.SyncPage{Version: version, Mode: domain.SyncModeDelta, Events: events}, nil
}

func (s *Service) snapshot(ctx context.Context, groupID, callerUserID string) (*domain.SyncPage, error) {
	detail, err := s.repo.GetGroupDetail(ctx, groupID, callerUserID)
	if err != nil {
		return nil, err
	}
	return &domain.SyncPage{Version: detail.Version, Mode: domain.SyncModeSnapshot, Snapshot: detail}, nil
}
