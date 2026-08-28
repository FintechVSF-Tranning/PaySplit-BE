package usecase_test

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
)

// Các stub cho method mới của Spec 0008 trên mock repository của handler tests.

func (m *mockServiceRepo) GetGroupSubmissionLock(ctx context.Context, groupID uuid.UUID) (*time.Time, error) {
	if m.lockLookupErr != nil {
		return nil, m.lockLookupErr
	}
	if m.submissionLockedAt != nil {
		t := *m.submissionLockedAt
		return &t, nil
	}
	return nil, nil
}

func (m *mockServiceRepo) LockSubmissions(ctx context.Context, groupID, callerUserID uuid.UUID) (*repository.LockSubmissionsResult, error) {
	return &repository.LockSubmissionsResult{LockedAt: time.Now().UTC(), LockedNow: true}, nil
}

func (m *mockServiceRepo) UnlockSubmissions(ctx context.Context, groupID, callerUserID uuid.UUID) error {
	return nil
}

func (m *mockServiceRepo) StartBulkFinalize(ctx context.Context, p repository.StartBulkFinalizeParams) (*repository.StartBulkFinalizeResult, error) {
	return nil, domain.ErrInvalidInput
}

func (m *mockServiceRepo) GetFinalizeBatch(ctx context.Context, batchID, groupID uuid.UUID) (*domain.FinalizeBatch, error) {
	return nil, domain.ErrBatchNotFound
}

func (m *mockServiceRepo) ListBatchItemsPage(ctx context.Context, batchID uuid.UUID, cursor *string, limit int32) ([]*domain.BatchItemResult, *string, error) {
	return nil, nil, nil
}

func (m *mockServiceRepo) BeginTx(ctx context.Context) (pgx.Tx, error) { return nil, nil }

func (m *mockServiceRepo) LockActiveGroupInTx(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) error {
	return nil
}

func (m *mockServiceRepo) LockBatchForItem(ctx context.Context, tx pgx.Tx, batchID uuid.UUID) (*domain.FinalizeBatch, error) {
	return nil, domain.ErrBatchNotFound
}

func (m *mockServiceRepo) PromoteBatchToProcessing(ctx context.Context, tx pgx.Tx, batchID uuid.UUID) error {
	return nil
}

func (m *mockServiceRepo) LockBatchItemForUpdate(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID) (*domain.FinalizeBatchItem, error) {
	return nil, domain.ErrBatchNotFound
}

func (m *mockServiceRepo) GetBillStateForUpdateInTx(ctx context.Context, tx pgx.Tx, billID, groupID uuid.UUID) (*domain.Bill, error) {
	return nil, domain.ErrBillNotFound
}

func (m *mockServiceRepo) GetBillByIDInTx(ctx context.Context, tx pgx.Tx, id, groupID uuid.UUID) (*domain.Bill, error) {
	return nil, domain.ErrBillNotFound
}

func (m *mockServiceRepo) ListActiveGroupMembersInTx(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) ([]*repository.GroupMember, error) {
	return nil, nil
}

func (m *mockServiceRepo) GetGroupMemberUserInTx(ctx context.Context, tx pgx.Tx, memberID, groupID uuid.UUID) (*repository.GroupMemberWithUser, error) {
	return nil, domain.ErrInvalidInput
}

func (m *mockServiceRepo) ApplyReviewInTx(ctx context.Context, tx pgx.Tx, billID, groupID uuid.UUID, expectedVersion int32, reviewerMemberID uuid.UUID) error {
	return nil
}

func (m *mockServiceRepo) FinalizeBillInTx(ctx context.Context, tx pgx.Tx, p repository.FinalizeBillParams) (*domain.Bill, error) {
	return nil, domain.ErrBillConflict
}

func (m *mockServiceRepo) MarkBatchItemFinalized(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID) error {
	return nil
}

func (m *mockServiceRepo) MarkBatchItemFailed(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID, errorCode string) error {
	return nil
}

func (m *mockServiceRepo) RecordBatchItemFailure(ctx context.Context, batchID, groupID, billID uuid.UUID, errorCode string) error {
	return nil
}

func (m *mockServiceRepo) TryCompleteBatch(ctx context.Context, batchID, groupID uuid.UUID, beforeCommit func(ctx context.Context, tx pgx.Tx, notificationIDs []string) error) (bool, error) {
	return false, nil
}

func (m *mockServiceRepo) CountActiveBatches(ctx context.Context, groupID uuid.UUID) (int64, error) {
	return 0, nil
}
