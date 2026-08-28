package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	billusecase "paysplit-backend/internal/modules/bill/usecase"
	"paysplit-backend/internal/platform/queue/appjob"
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
	return w.processItem(ctx, job.Args.BatchID, job.Args.GroupID, job.Args.BillID)
}

// Execute triển khai appjob.JobHandler cho BulkFinalizeWorker (Spec 0010 AC-11)
func (w *BulkFinalizeWorker) Execute(ctx context.Context, job appjob.Job) error {
	var args BulkFinalizeItemArgs
	if err := json.Unmarshal(job.Args, &args); err != nil {
		return fmt.Errorf("unmarshal bulk finalize job args: %w", err)
	}
	return w.processItem(ctx, args.BatchID, args.GroupID, args.BillID)
}

func (w *BulkFinalizeWorker) processItem(ctx context.Context, batchIDStr, groupIDStr, billIDStr string) error {
	if w.service == nil {
		return fmt.Errorf("bulk finalize worker service is not set")
	}

	batchID, err := uuid.Parse(batchIDStr)
	if err != nil {
		return fmt.Errorf("parse batch id: %w", err)
	}
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		return fmt.Errorf("parse group id: %w", err)
	}
	billID, err := uuid.Parse(billIDStr)
	if err != nil {
		return fmt.Errorf("parse bill id: %w", err)
	}

	outcome, err := w.service.ProcessBulkFinalizeItem(ctx, batchID, groupID, billID)
	if err != nil {
		log.Printf("event=bulk_finalize_item_error batch_id=%s bill_id=%s err=%v", batchIDStr, billIDStr, err)
		return err
	}

	switch outcome.MetricOutcome {
	case "finalized":
		log.Printf("event=bulk_finalize_item_finalized batch_id=%s bill_id=%s", batchIDStr, billIDStr)
	case "skipped":
		// Item đã xử lý hoặc batch đã hoàn tất: giao lại trùng lặp là no-op an toàn.
	default:
		log.Printf("event=bulk_finalize_item_failed batch_id=%s bill_id=%s error_code=%s", batchIDStr, billIDStr, outcome.ErrorCode)
	}
	return nil
}


