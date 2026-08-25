package jobs

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	billusecase "paysplit-backend/internal/modules/bill/usecase"
)

// BulkFinalizeWorker xử lý từng item của batch chốt toàn bộ trên River Queue.
// Mỗi job nhận trách nhiệm đúng một bill captured; worker không tự retry lỗi
// trạng thái ổn định (chỉ trả nil), còn lỗi tạm thời được trả về để River retry
// với backoff (Spec 0008 Batch item transaction, Migration plan risk 2).
type BulkFinalizeWorker struct {
	river.WorkerDefaults[BulkFinalizeItemArgs]
	service *billusecase.Service
}

// NewBulkFinalizeWorker khởi tạo worker chưa gắn service. Service phải được gắn
// qua SetService trước khi River client start, vì bundle worker bắt buộc đăng ký
// hoàn chỉnh TRƯỚC khi tạo client trong bootstrap.
func NewBulkFinalizeWorker() *BulkFinalizeWorker {
	return &BulkFinalizeWorker{}
}

// SetService gắn bill usecase service mà worker gọi cho từng job item.
func (w *BulkFinalizeWorker) SetService(service *billusecase.Service) {
	if service == nil {
		panic("bulk finalize worker service must not be nil")
	}
	w.service = service
}

// Work chạy một item: phân loại, review (khi cần) và finalize bill trong một
// transaction duy nhất do usecase sở hữu.
func (w *BulkFinalizeWorker) Work(ctx context.Context, job *river.Job[BulkFinalizeItemArgs]) error {
	if w.service == nil {
		return fmt.Errorf("bulk finalize worker service is not set")
	}
	args := job.Args

	batchID, err := uuid.Parse(args.BatchID)
	if err != nil {
		return fmt.Errorf("parse batch id: %w", err)
	}
	groupID, err := uuid.Parse(args.GroupID)
	if err != nil {
		return fmt.Errorf("parse group id: %w", err)
	}
	billID, err := uuid.Parse(args.BillID)
	if err != nil {
		return fmt.Errorf("parse bill id: %w", err)
	}

	outcome, err := w.service.ProcessBulkFinalizeItem(ctx, batchID, groupID, billID)
	if err != nil {
		log.Printf("event=bulk_finalize_item_error batch_id=%s bill_id=%s attempt=%d err=%v", args.BatchID, args.BillID, job.Attempt, err)
		return err
	}

	switch outcome.MetricOutcome {
	case "finalized":
		log.Printf("event=bulk_finalize_item_finalized batch_id=%s bill_id=%s", args.BatchID, args.BillID)
	case "skipped":
		// Item đã xử lý hoặc batch đã hoàn tất: giao lại trùng lặp là no-op an toàn.
	default:
		log.Printf("event=bulk_finalize_item_failed batch_id=%s bill_id=%s error_code=%s", args.BatchID, args.BillID, outcome.ErrorCode)
	}
	return nil
}
