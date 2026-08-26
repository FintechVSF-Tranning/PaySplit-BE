package jobs

import (
	"context"
	"log"
	"time"

	"paysplit-backend/internal/modules/group/repository"
)

// EventPruner là phần repository mà job dọn nhật ký cần.
type EventPruner interface {
	DeleteEventsBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

var _ EventPruner = (repository.Repository)(nil)

// RunEventRetention dọn định kỳ nhật ký group_events cũ hơn retention.
//
// Nhật ký chỉ tồn tại để phục vụ catch-up; client offline lâu hơn retention vẫn
// đồng bộ đúng, chỉ là qua đường snapshot đắt hơn. Vì vậy job này an toàn khi
// chạy đồng thời trên nhiều instance và bỏ qua lỗi tạm thời.
func RunEventRetention(ctx context.Context, pruner EventPruner, retention, interval time.Duration) {
	if pruner == nil || retention <= 0 {
		return
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-retention)
			deleted, err := pruner.DeleteEventsBefore(ctx, cutoff)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("event=group_event_retention_failed error=%v", err)
				}
				continue
			}
			if deleted > 0 {
				log.Printf("event=group_event_retention_pruned deleted=%d cutoff=%s", deleted, cutoff.Format(time.RFC3339))
			}
		}
	}
}
