package http_test

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
)

// Các stub cho method mới của Spec 0008 trên mock repository của handler tests.
// Mỗi stub đọc scenario control tương ứng từ mockHandlerRepo khi có, nếu không
// giữ hành vi trung lập để các test cũ không bị ảnh hưởng.

func (m *mockHandlerRepo) GetGroupSubmissionLock(ctx context.Context, groupID uuid.UUID) (*time.Time, error) {
	return nil, nil
}

func (m *mockHandlerRepo) LockSubmissions(ctx context.Context, groupID, callerUserID uuid.UUID) (*repository.LockSubmissionsResult, error) {
	if m.lockErr != nil {
		return nil, m.lockErr
	}
	if m.lockResult != nil {
		return m.lockResult, nil
	}
	return &repository.LockSubmissionsResult{LockedAt: time.Now().UTC(), LockedNow: true}, nil
}

func (m *mockHandlerRepo) UnlockSubmissions(ctx context.Context, groupID, callerUserID uuid.UUID) error {
	if m.lockErr != nil {
		return m.lockErr
	}
	return nil
}

func (m *mockHandlerRepo) StartBulkFinalize(ctx context.Context, p repository.StartBulkFinalizeParams) (*repository.StartBulkFinalizeResult, error) {
	if m.bulkErr != nil {
		return nil, m.bulkErr
	}
	if m.bulkResult != nil {
		if p.BeforeCommit != nil {
			if err := p.BeforeCommit(ctx, nil, &repository.BulkStartEnqueueInfo{
				BatchID: p.BatchID,
				Result:  m.bulkResult,
			}); err != nil {
				return nil, err
			}
		}
		return m.bulkResult, nil
	}
	return nil, domain.ErrInvalidInput
}

func (m *mockHandlerRepo) GetFinalizeBatch(ctx context.Context, batchID, groupID uuid.UUID) (*domain.FinalizeBatch, error) {
	if m.batchErr != nil {
		return nil, m.batchErr
	}
	if m.batch != nil {
		return m.batch, nil
	}
	return nil, domain.ErrBatchNotFound
}

func (m *mockHandlerRepo) ListBatchItemsPage(ctx context.Context, batchID uuid.UUID, cursor *string, limit int32) ([]*domain.BatchItemResult, *string, error) {
	return m.batchItems, m.batchNext, nil
}

func (m *mockHandlerRepo) BeginTx(ctx context.Context) (pgx.Tx, error) { return nil, nil }

func (m *mockHandlerRepo) LockActiveGroupInTx(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) error {
	return nil
}

func (m *mockHandlerRepo) LockBatchForItem(ctx context.Context, tx pgx.Tx, batchID uuid.UUID) (*domain.FinalizeBatch, error) {
	return nil, domain.ErrBatchNotFound
}

func (m *mockHandlerRepo) PromoteBatchToProcessing(ctx context.Context, tx pgx.Tx, batchID uuid.UUID) error {
	return nil
}

func (m *mockHandlerRepo) LockBatchItemForUpdate(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID) (*domain.FinalizeBatchItem, error) {
	return nil, domain.ErrBatchNotFound
}

func (m *mockHandlerRepo) GetBillStateForUpdateInTx(ctx context.Context, tx pgx.Tx, billID, groupID uuid.UUID) (*domain.Bill, error) {
	return nil, domain.ErrBillNotFound
}

func (m *mockHandlerRepo) GetBillByIDInTx(ctx context.Context, tx pgx.Tx, id, groupID uuid.UUID) (*domain.Bill, error) {
	return nil, domain.ErrBillNotFound
}

func (m *mockHandlerRepo) ListActiveGroupMembersInTx(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) ([]*repository.GroupMember, error) {
	return nil, nil
}

func (m *mockHandlerRepo) GetGroupMemberUserInTx(ctx context.Context, tx pgx.Tx, memberID, groupID uuid.UUID) (*repository.GroupMemberWithUser, error) {
	return nil, domain.ErrInvalidInput
}

func (m *mockHandlerRepo) ApplyReviewInTx(ctx context.Context, tx pgx.Tx, billID, groupID uuid.UUID, expectedVersion int32, reviewerMemberID uuid.UUID) error {
	return nil
}

func (m *mockHandlerRepo) FinalizeBillInTx(ctx context.Context, tx pgx.Tx, p repository.FinalizeBillParams) (*domain.Bill, error) {
	return nil, domain.ErrBillConflict
}

func (m *mockHandlerRepo) MarkBatchItemFinalized(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID) error {
	return nil
}

func (m *mockHandlerRepo) MarkBatchItemFailed(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID, errorCode string) error {
	return nil
}

func (m *mockHandlerRepo) RecordBatchItemFailure(ctx context.Context, batchID, groupID, billID uuid.UUID, errorCode string) error {
	return nil
}

func (m *mockHandlerRepo) TryCompleteBatch(ctx context.Context, batchID, groupID uuid.UUID, beforeCommit func(ctx context.Context, tx pgx.Tx, notificationIDs []string) error) (bool, error) {
	return false, nil
}

func (m *mockHandlerRepo) CountActiveBatches(ctx context.Context, groupID uuid.UUID) (int64, error) {
	return 0, nil
}
