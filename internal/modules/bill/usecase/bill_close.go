package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	platformmetrics "paysplit-backend/internal/platform/metrics"
)

// ============================================================================
// GROUP BILL CLOSE V1 (Spec 0008)
// Khóa gửi hóa đơn một chiều, batch chốt toàn bộ và xử lý item bất đồng bộ.
// ============================================================================

// LockResult là kết quả khóa gửi hóa đơn trả về cho Captain (Spec 0008 AC-1).
type LockResult struct {
	BillSubmissionLocked   bool      `json:"bill_submission_locked"`
	BillSubmissionLockedAt time.Time `json:"bill_submission_locked_at"`
}

// StartBulkResult là kết quả mở batch chốt toàn bộ, trả về kèm trạng thái 202
// (Spec 0008 API surface: batch ID, status, counts, lock state).
type StartBulkResult struct {
	Batch                   *domain.FinalizeBatch `json:"batch"`
	BillSubmissionLocked    bool                  `json:"bill_submission_locked"`
	BillSubmissionLockedAt  time.Time             `json:"bill_submission_locked_at"`
	CapturedReviewedCount   int                   `json:"captured_reviewed_count"`
	CapturedUnreviewedCount int                   `json:"captured_unreviewed_count"`
}

// BatchDetailResult là kết quả đọc chi tiết batch kèm trang item (Spec 0008 AC-6).
type BatchDetailResult struct {
	Batch      *domain.FinalizeBatch     `json:"batch"`
	Items      []*domain.BatchItemResult `json:"items"`
	NextCursor *string                   `json:"next_cursor,omitempty"`
}

// LockSubmissions cho phép Captain đang active khóa việc tạo bill mới của nhóm.
// Hành động idempotent: nhóm đã khóa vẫn trả về 200 với mốc thời gian đã lưu mà
// không ghi thêm activity nào. Khóa là một chiều trong V1, không có hành động mở
// (Spec 0008 AC-1). Caller không phải thành viên active nhận ErrGroupNotFound,
// thành viên active nhưng không phải Captain nhận ErrCaptainRequired.
func (s *Service) LockSubmissions(ctx context.Context, callerUserID, groupID uuid.UUID) (*LockResult, error) {
	start := time.Now()
	result, err := s.repo.LockSubmissions(ctx, groupID, callerUserID)

	outcome := "success"
	switch {
	case err == nil && !result.LockedNow:
		outcome = "already_locked"
	case errors.Is(err, domain.ErrCaptainRequired), errors.Is(err, domain.ErrGroupNotFound):
		outcome = "forbidden"
	case err != nil:
		outcome = "failed"
	}
	platformmetrics.RecordGroupBillSubmissionLock(outcome, time.Since(start))

	if err != nil {
		return nil, err
	}
	return &LockResult{BillSubmissionLocked: true, BillSubmissionLockedAt: result.LockedAt}, nil
}

// UnlockSubmissions cho phép Captain đang active mở khóa việc tạo bill mới của nhóm.
// Idempotent: nhóm chưa khóa vẫn thành công mà không sinh activity thừa.
func (s *Service) UnlockSubmissions(ctx context.Context, callerUserID, groupID uuid.UUID) error {
	return s.repo.UnlockSubmissions(ctx, groupID, callerUserID)
}

// StartBulkFinalize cho phép Captain đang active mở một batch chốt toàn bộ: bật
// khóa gửi hóa đơn ngay lập tức, capture mọi bill còn mở kèm version và review
// state, rồi enqueue từng item vào River để xử lý độc lập (Spec 0008 AC-4).
func (s *Service) StartBulkFinalize(ctx context.Context, callerUserID, groupID uuid.UUID) (*StartBulkResult, error) {
	return s.startBulkFinalize(ctx, callerUserID, groupID, "")
}

// StartBulkFinalizeIdempotent mở batch và hoàn tất reservation idempotency
// trong cùng transaction PostgreSQL tạo batch. Vì vậy không có khoảng trống
// mà batch đã commit nhưng key vẫn còn in_progress.
func (s *Service) StartBulkFinalizeIdempotent(ctx context.Context, callerUserID, groupID uuid.UUID, rawIdempotencyKey string) (*StartBulkResult, error) {
	return s.startBulkFinalize(ctx, callerUserID, groupID, rawIdempotencyKey)
}

func (s *Service) startBulkFinalize(ctx context.Context, callerUserID, groupID uuid.UUID, rawIdempotencyKey string) (*StartBulkResult, error) {
	batchID := uuid.Must(uuid.NewV7())

	result, err := s.repo.StartBulkFinalize(ctx, repository.StartBulkFinalizeParams{
		GroupID:      groupID,
		CallerUserID: callerUserID,
		BatchID:      batchID,
		BeforeCommit: func(txCtx context.Context, tx pgx.Tx, info *repository.BulkStartEnqueueInfo) error {
			if s.enqueuer != nil {
				for _, billID := range info.BillIDs {
					if err := s.enqueuer.EnqueueBulkFinalizeItemTx(txCtx, tx, info.BatchID, billID, groupID); err != nil {
						return fmt.Errorf("enqueue bulk finalize item job: %w", err)
					}
				}
				for _, notifID := range info.NotificationIDs {
					if err := s.enqueuer.EnqueueNotificationTx(txCtx, tx, notifID); err != nil {
						return fmt.Errorf("enqueue bulk start notification job: %w", err)
					}
				}
			}
			if strings.TrimSpace(rawIdempotencyKey) != "" {
				response := startBulkResultFromRepository(info.Result)
				responseBody, err := json.Marshal(response)
				if err != nil {
					return fmt.Errorf("marshal bulk finalize idempotency response: %w", err)
				}
				if err := s.repo.CompleteIdempotencyKeyInTx(txCtx, tx, repository.CompleteIdempotencyParams{
					ActorUserID:  callerUserID,
					Operation:    "bulk_finalize_all",
					KeyHash:      HashSHA256([]byte(rawIdempotencyKey)),
					ResponseCode: 202,
					ResponseBody: responseBody,
					ResourceID:   &batchID,
				}); err != nil {
					return fmt.Errorf("complete bulk finalize idempotency in tx: %w", err)
				}
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}

	// Batch rỗng hoàn tất ngay trong transaction mở batch; ghi metric tại đây
	// vì không có worker item nào chạy để làm điều đó.
	if result.Batch.Status == domain.BatchStatusCompleted {
		recordBatchOutcome(result.Batch.TargetCount, result.Batch.FinalizedCount, result.Batch.FailedCount, time.Since(result.Batch.CreatedAt))
	}

	return startBulkResultFromRepository(result), nil
}

func startBulkResultFromRepository(result *repository.StartBulkFinalizeResult) *StartBulkResult {
	if result == nil {
		return nil
	}
	return &StartBulkResult{
		Batch:                   result.Batch,
		BillSubmissionLocked:    true,
		BillSubmissionLockedAt:  result.SubmissionLockedAt,
		CapturedReviewedCount:   result.CapturedReviewedCount,
		CapturedUnreviewedCount: result.CapturedUnreviewedCount,
	}
}

// GetFinalizeBatch đọc tóm tắt batch và một trang kết quả item; chỉ Captain
// active được đọc để thành viên thường không suy ra được ID batch hay kết quả
// thất bại của từng bill (Spec 0008 Security model 5, AC-10).
func (s *Service) GetFinalizeBatch(ctx context.Context, callerUserID, groupID, batchID uuid.UUID, cursor *string, limit int32) (*BatchDetailResult, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member == nil || member.Status != "active" {
		return nil, domain.ErrGroupNotFound
	}
	if member.Role != "captain" {
		return nil, domain.ErrCaptainRequired
	}

	batch, err := s.repo.GetFinalizeBatch(ctx, batchID, groupID)
	if err != nil {
		return nil, err
	}
	items, nextCursor, err := s.repo.ListBatchItemsPage(ctx, batchID, cursor, limit)
	if err != nil {
		return nil, err
	}
	return &BatchDetailResult{Batch: batch, Items: items, NextCursor: nextCursor}, nil
}

// ProcessItemOutcome mô tả kết quả xử lý một item batch cho worker và metrics.
type ProcessItemOutcome struct {
	ItemStatus    string // pending (chưa xử lý hoặc bỏ qua), finalized, failed
	MetricOutcome string // skipped, finalized, version_conflict, not_ready, bank_required, deleted, failed
	ErrorCode     string
}

// ProcessBulkFinalizeItem xử lý MỘT bill captured bên trong đúng một transaction:
// khóa nhóm rồi batch rồi item rồi bill theo đúng thứ tự chung của mọi mutation
// gây xung đột (bất biến 11), phân loại trạng thái, review (khi cần) và finalize
// bằng đúng luật hiện có của Spec 3. Mỗi item có transaction riêng nên một bill
// thất bại không bao giờ cuộn rollback một bill khác (Spec 0008 AC-5).
func (s *Service) ProcessBulkFinalizeItem(ctx context.Context, batchID, groupID, billID uuid.UUID) (*ProcessItemOutcome, error) {
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin batch item tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Bước 1: khóa group active trước mọi tài nguyên con để đồng bộ với Captain
	// transfer và giữ một thứ tự khóa duy nhất cho mutation theo group.
	if err = s.repo.LockActiveGroupInTx(ctx, tx, groupID); errors.Is(err, domain.ErrGroupNotFound) {
		return &ProcessItemOutcome{ItemStatus: domain.BatchItemPending, MetricOutcome: "skipped"}, nil
	} else if err != nil {
		return nil, err
	}

	// Bước 2: khóa dòng batch; batch đã completed thì job là no-op an toàn khi
	// River giao lại (retry safety, AC-7).
	batch, err := s.repo.LockBatchForItem(ctx, tx, batchID)
	if errors.Is(err, domain.ErrBatchNotFound) {
		return &ProcessItemOutcome{ItemStatus: domain.BatchItemPending, MetricOutcome: "skipped"}, nil
	}
	if err != nil {
		return nil, err
	}
	if batch.GroupID != groupID.String() {
		return &ProcessItemOutcome{ItemStatus: domain.BatchItemPending, MetricOutcome: "skipped"}, nil
	}
	if batch.Status == domain.BatchStatusCompleted {
		return &ProcessItemOutcome{ItemStatus: domain.BatchItemPending, MetricOutcome: "skipped"}, nil
	}
	if err = s.repo.PromoteBatchToProcessing(ctx, tx, batchID); err != nil {
		return nil, err
	}

	// Bước 2: khóa item; item khác pending nghĩa là đã xử lý ở lần giao trước.
	item, err := s.repo.LockBatchItemForUpdate(ctx, tx, batchID, billID)
	if errors.Is(err, domain.ErrBatchNotFound) {
		return &ProcessItemOutcome{ItemStatus: domain.BatchItemPending, MetricOutcome: "skipped"}, nil
	}
	if err != nil {
		return nil, err
	}
	if item.Status != domain.BatchItemPending {
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit duplicate item tx: %w", err)
		}
		if err = s.completeBatchIfDone(ctx, batchID, groupID); err != nil {
			return nil, err
		}
		return &ProcessItemOutcome{ItemStatus: item.Status, MetricOutcome: "skipped"}, nil
	}

	// Version captured nằm trên dòng item là nguồn sự thật của lần capture này.
	capturedVersion := item.BillVersion

	// Phân loại bill dưới khóa (bước 3 đến 5).
	state, err := s.repo.GetBillStateForUpdateInTx(ctx, tx, billID, groupID)
	if errors.Is(err, domain.ErrBillNotFound) {
		return s.failItemStable(ctx, tx, batchID, groupID, billID, domain.ItemErrorDeleted, "deleted")
	}
	if err != nil {
		return nil, err
	}

	// Bill đã finalized ngay sau capture từ đúng version captured: đánh dấu item
	// finalized mà không ghi trùng shares, debts, activity hay notification (bước 5).
	if state.Status == domain.BillStatusFinalized && state.Version == capturedVersion+1 {
		return s.finishItemFinalized(ctx, tx, batchID, groupID, billID)
	}

	// Version lệch khỏi bản capture (bill bị sửa hoặc chuyển trạng thái bằng
	// đường khác sau capture) -> thất bại ổn định VERSION_CONFLICT (bước 4).
	if state.Version != capturedVersion {
		return s.failItemStable(ctx, tx, batchID, groupID, billID, domain.ItemErrorVersionConflict, "version_conflict")
	}

	// Bill bị hủy sau capture -> thất bại ổn định.
	if state.Status != domain.BillStatusDraft && state.Status != domain.BillStatusReviewed {
		return s.failItemStable(ctx, tx, batchID, groupID, billID, domain.ItemErrorVoided, "failed")
	}

	// Bước 6: chạy đúng luật review validation + finalize của Spec 3 trong cùng
	// transaction này. Thất bại ổn định sau khi đã ghi cục bộ (ví dụ giữa chừng
	// finalize) sẽ rollback toàn bộ transaction (bao gồm cả thay đổi review) rồi
	// ghi item failed bằng transaction ngắn riêng (bước 8).
	outcome, procErr := s.finalizeCapturedBill(ctx, tx, groupID, billID, state, capturedVersion)
	if procErr != nil {
		errorCode, metricOutcome := stableItemErrorCode(procErr)
		if errorCode == "" {
			// Lỗi tạm thời (database, queue): để transaction rollback qua defer
			// và trả lỗi cho River retry với item vẫn còn pending.
			return nil, procErr
		}
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return nil, fmt.Errorf("rollback batch item before stable failure: %w", rollbackErr)
		}
		if recErr := s.repo.RecordBatchItemFailure(ctx, batchID, groupID, billID, errorCode); recErr != nil {
			return nil, recErr
		}
		platformmetrics.RecordGroupBillBulkItem(metricOutcome)
		if err = s.completeBatchIfDone(ctx, batchID, groupID); err != nil {
			return nil, err
		}
		return &ProcessItemOutcome{ItemStatus: domain.BatchItemFailed, MetricOutcome: metricOutcome, ErrorCode: errorCode}, nil
	}

	if outcome.ErrorCode != "" {
		// Thất bại ổn định phát hiện trước khi ghi gì cả (thiếu ngân hàng, đối
		// soát chặn): item failed ngay trong transaction này rồi commit.
		if err = s.repo.MarkBatchItemFailed(ctx, tx, batchID, billID, outcome.ErrorCode); err != nil {
			return nil, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit failed item tx: %w", err)
		}
		platformmetrics.RecordGroupBillBulkItem(outcome.MetricOutcome)
		outcome.ItemStatus = domain.BatchItemFailed
		if err = s.completeBatchIfDone(ctx, batchID, groupID); err != nil {
			return nil, err
		}
		return outcome, nil
	}

	return s.finishItemFinalized(ctx, tx, batchID, groupID, billID)
}

// finalizeCapturedBill thực hiện phần review + finalize của một bill captured
// bên trong transaction đang mở, tái dùng đúng luật Spec 3. Trả về outcome thành
// công (ErrorCode rỗng), outcome thất bại ổn định chưa ghi gì, hoặc lỗi để caller
// phân loại (ổn định hay tạm thời) rồi rollback.
func (s *Service) finalizeCapturedBill(ctx context.Context, tx pgx.Tx, groupID, billID uuid.UUID, state *domain.Bill, capturedVersion int32) (*ProcessItemOutcome, error) {
	// Captain active HIỆN TẠI giữ quyền authorize finalize; batch đã hợp lệ khi
	// bắt đầu nên tiếp tục chạy kể cả khi Captain đổi giữa chừng (Spec 0008
	// Security model 4). Requester vẫn là audit actor trên batch.
	captain, err := s.getActiveCaptainInTx(ctx, tx, groupID)
	if err != nil {
		return nil, err
	}

	// Kiểm tra tài khoản ngân hàng của Creditor trước khi ghi bất cứ thứ gì
	// (giống bước 4 của finalize đơn lẻ, Spec 3 AC-9).
	creditor, err := s.repo.GetGroupMemberUserInTx(ctx, tx, state.CreditorMemberID, groupID)
	if err != nil || creditor.DefaultBankCode == nil || creditor.DefaultBankAccountNum == nil ||
		isEmptyString(creditor.DefaultBankCode) || isEmptyString(creditor.DefaultBankAccountNum) {
		return &ProcessItemOutcome{ItemStatus: domain.BatchItemPending, MetricOutcome: "bank_required", ErrorCode: domain.ItemErrorBankRequired}, nil
	}

	// Đọc đầy đủ bill dưới khóa để tính lại phân bổ từ dữ liệu thật tại thời điểm xử lý.
	bill, err := s.repo.GetBillByIDInTx(ctx, tx, billID, groupID)
	if err != nil {
		return nil, err
	}

	members, err := s.repo.ListActiveGroupMembersInTx(ctx, tx, groupID)
	if err != nil {
		return nil, fmt.Errorf("list active group members in tx: %w", err)
	}
	activeSet := make(map[uuid.UUID]bool, len(members))
	memberUserMap := make(map[uuid.UUID]uuid.UUID, len(members))
	for _, m := range members {
		activeSet[m.ID] = true
		memberUserMap[m.ID] = m.UserID
	}

	allocations, err := validateAllocationForFinalize(bill, activeSet)
	if err != nil {
		// blockersToError trả về ErrBillNotReady / ErrDiscountNotAllocatable /
		// ErrCreditorRequired: thất bại ổn định, chưa ghi gì nên an toàn.
		metricOutcome := "not_ready"
		errorCode := domain.ItemErrorNotReady
		if errors.Is(err, domain.ErrDiscountNotAllocatable) {
			errorCode = domain.ItemErrorDiscount
		}
		return &ProcessItemOutcome{ItemStatus: domain.BatchItemPending, MetricOutcome: metricOutcome, ErrorCode: errorCode}, nil
	}

	// Draft captured cần review trước trong CÙNG transaction (AC-5); reviewed
	// captured đi thẳng vào finalize ở đúng version captured như đường đơn lẻ.
	expectedVersion := capturedVersion
	if state.Status == domain.BillStatusDraft {
		if err = s.repo.ApplyReviewInTx(ctx, tx, billID, groupID, capturedVersion, captain.ID); err != nil {
			return nil, err
		}
		expectedVersion = capturedVersion + 1
	}

	plan := s.buildFinalizationPlan(bill, groupID, captain.ID, allocations, memberUserMap)
	if _, err = s.repo.FinalizeBillInTx(ctx, tx, repository.FinalizeBillParams{
		BillID:          billID,
		GroupID:         groupID,
		ExpectedVersion: expectedVersion,
		Shares:          plan.shares,
		Debts:           plan.debts,
		ActorMemberID:   captain.ID,
		Notifications:   plan.notifications,
		BeforeCommit:    plan.beforeCommit,
	}); err != nil {
		return nil, err
	}
	return &ProcessItemOutcome{ItemStatus: domain.BatchItemFinalized, MetricOutcome: "finalized"}, nil
}

// finishItemFinalized đánh dấu item finalized, commit, ghi metric và thử hoàn tất batch.
func (s *Service) finishItemFinalized(ctx context.Context, tx pgx.Tx, batchID, groupID, billID uuid.UUID) (*ProcessItemOutcome, error) {
	if err := s.repo.MarkBatchItemFinalized(ctx, tx, batchID, billID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit batch item tx: %w", err)
	}
	platformmetrics.RecordGroupBillBulkItem("finalized")
	if err := s.completeBatchIfDone(ctx, batchID, groupID); err != nil {
		return nil, err
	}
	return &ProcessItemOutcome{ItemStatus: domain.BatchItemFinalized, MetricOutcome: "finalized"}, nil
}

// failItemStable ghi nhận item failed với mã ổn định rồi commit transaction;
// dùng cho các phân loại thất bại phát hiện dưới khóa (xóa, version, voided).
func (s *Service) failItemStable(ctx context.Context, tx pgx.Tx, batchID, groupID, billID uuid.UUID, errorCode, metricOutcome string) (*ProcessItemOutcome, error) {
	if err := s.repo.MarkBatchItemFailed(ctx, tx, batchID, billID, errorCode); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit stable failure tx: %w", err)
	}
	platformmetrics.RecordGroupBillBulkItem(metricOutcome)
	if err := s.completeBatchIfDone(ctx, batchID, groupID); err != nil {
		return nil, err
	}
	return &ProcessItemOutcome{ItemStatus: domain.BatchItemFailed, MetricOutcome: metricOutcome, ErrorCode: errorCode}, nil
}

// getActiveCaptainInTx đọc thành viên Captain active hiện tại của nhóm trong
// transaction; nhóm active luôn có đúng một Captain nhờ unique index.
func (s *Service) getActiveCaptainInTx(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) (*repository.GroupMember, error) {
	members, err := s.repo.ListActiveGroupMembersInTx(ctx, tx, groupID)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		if m.Role == "captain" {
			return m, nil
		}
	}
	// Không thể xảy ra với nhóm active (uq_group_members_active_captain), nhưng
	// fail closed thay vì ghi nợ sai người.
	return nil, domain.ErrForbidden
}

// completeBatchIfDone thử hoàn tất batch sau mỗi item chốt xong; khi batch vừa
// chuyển completed thì đọc lại đếm cuối và ghi metric batch + duration.
func (s *Service) completeBatchIfDone(ctx context.Context, batchID, groupID uuid.UUID) error {
	completed, err := s.repo.TryCompleteBatch(ctx, batchID, groupID, func(txCtx context.Context, tx pgx.Tx, notificationIDs []string) error {
		if s.enqueuer == nil {
			return nil
		}
		for _, notifID := range notificationIDs {
			if err := s.enqueuer.EnqueueNotificationTx(txCtx, tx, notifID); err != nil {
				return fmt.Errorf("enqueue completion notification job: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("complete batch %s: %w", batchID, err)
	}
	if !completed {
		return nil
	}
	batch, err := s.repo.GetFinalizeBatch(ctx, batchID, groupID)
	if err != nil {
		fmt.Printf("event=bulk_batch_metric_read_failed batch_id=%s err=%v\n", batchID, err)
		return nil
	}
	recordBatchOutcome(batch.TargetCount, batch.FinalizedCount, batch.FailedCount, time.Since(batch.CreatedAt))
	return nil
}

// recordBatchOutcome chọn outcome theo đếm cuối và ghi counter + duration.
func recordBatchOutcome(target, finalized, failedCount int, duration time.Duration) {
	outcome := "completed"
	switch {
	case target == 0:
		outcome = "empty"
	case failedCount > 0:
		outcome = "partial"
	}
	platformmetrics.RecordGroupBillBulkBatch(outcome)
	platformmetrics.ObserveGroupBillBulkDuration(outcome, duration)
}

// stableItemErrorCode ánh xạ một lỗi nghiệp vụ thành cặp (mã lỗi item ổn định,
// outcome metric). Trả về mã rỗng khi lỗi là tạm thời và worker nên retry.
func stableItemErrorCode(err error) (string, string) {
	switch {
	case errors.Is(err, domain.ErrBankAccountRequired):
		return domain.ItemErrorBankRequired, "bank_required"
	case errors.Is(err, domain.ErrDiscountNotAllocatable):
		return domain.ItemErrorDiscount, "not_ready"
	case errors.Is(err, domain.ErrBillNotReady), errors.Is(err, domain.ErrReviewRequired), errors.Is(err, domain.ErrCreditorRequired):
		return domain.ItemErrorNotReady, "not_ready"
	default:
		return "", ""
	}
}

func isEmptyString(v *string) bool {
	return v == nil || strings.TrimSpace(*v) == ""
}
