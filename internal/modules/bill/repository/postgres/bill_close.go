package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	"paysplit-backend/internal/modules/bill/repository/postgres/sqlc"
	"paysplit-backend/internal/platform/database"
)

// ============================================================================
// GROUP BILL CLOSE V1 (Spec 0008)
// Các phương thức khóa gửi hóa đơn một chiều, batch chốt toàn bộ và xử lý item.
// Mọi mutation đều giữ khóa dòng nhóm TRƯỚC mọi khóa bill/batch (bất biến 11).
// ============================================================================

// GetGroupSubmissionLock đọc trạng thái khóa gửi hóa đơn của nhóm đang active.
// Dùng làm pre-check rẻ tiền trước khi upload ảnh trong create gate (Spec 0008
// Bill creation gate); nhóm không tồn tại hoặc đã archive trả về ErrBillNotFound.
func (r *postgresRepository) GetGroupSubmissionLock(ctx context.Context, groupID uuid.UUID) (*time.Time, error) {
	lockedAt, err := sqlc.New(r.pool).GetGroupSubmissionLock(ctx, pgtype.UUID{Bytes: groupID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBillNotFound
		}
		return nil, fmt.Errorf("get group submission lock: %w", err)
	}
	if !lockedAt.Valid {
		return nil, nil
	}
	t := lockedAt.Time
	return &t, nil
}

// LockSubmissions bật khóa một chiều trong đúng một transaction: khóa dòng nhóm,
// kiểm tra Captain, đặt bill_submission_locked_at khi chưa có, ghi activity
// bill_submission_locked chỉ khi khóa thay đổi (Spec 0008 AC-1). Replay tự nhiên
// trả về cùng trạng thái mà không ghi thêm activity nào.
func (r *postgresRepository) LockSubmissions(ctx context.Context, groupID, callerUserID uuid.UUID) (*repository.LockSubmissionsResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin lock submissions tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := sqlc.New(tx)

	// Khóa dòng nhóm trước mọi thao tác khác (bất biến 11). Nhóm không tồn tại
	// hoặc đã archive trả về not found theo API surface của spec 0008.
	if err = database.LockActiveGroup(ctx, tx, groupID); err != nil {
		if errors.Is(err, database.ErrGroupNotActive) {
			return nil, domain.ErrGroupNotFound
		}
		return nil, fmt.Errorf("lock group for submission lock: %w", err)
	}

	member, err := q.GetGroupMember(ctx, sqlc.GetGroupMemberParams{
		GroupID: pgtype.UUID{Bytes: groupID, Valid: true},
		UserID:  pgtype.UUID{Bytes: callerUserID, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get caller membership for lock submissions: %w", err)
	}
	if fmt.Sprintf("%v", member.Role) != "captain" {
		return nil, domain.ErrCaptainRequired
	}

	current, err := q.GetGroupSubmissionLock(ctx, pgtype.UUID{Bytes: groupID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("read submission lock: %w", err)
	}

	result := &repository.LockSubmissionsResult{LockedNow: false}
	if current.Valid {
		result.LockedAt = current.Time
	} else {
		updated, err := q.SetGroupSubmissionLockedAt(ctx, pgtype.UUID{Bytes: groupID, Valid: true})
		if err != nil {
			return nil, fmt.Errorf("set submission lock: %w", err)
		}
		result.LockedAt = updated.Time
		result.LockedNow = true

		meta, _ := json.Marshal(map[string]any{
			"group_id":  groupID.String(),
			"locked_at": result.LockedAt.UTC().Format(time.RFC3339),
		})
		if _, err := q.InsertGroupActivity(ctx, sqlc.InsertGroupActivityParams{
			ID:            pgtype.UUID{Bytes: uuid.Must(uuid.NewV7()), Valid: true},
			GroupID:       pgtype.UUID{Bytes: groupID, Valid: true},
			ActorMemberID: member.ID,
			ActionType:    "bill_submission_locked",
			Description:   "đã khóa gửi hóa đơn mới cho nhóm",
			Metadata:      meta,
		}); err != nil {
			return nil, fmt.Errorf("insert bill_submission_locked activity: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit lock submissions tx: %w", err)
	}
	return result, nil
}

// StartBulkFinalize mở batch chốt toàn bộ trong một transaction theo đúng trình
// tự spec 0008 (Start bulk finalize transaction bước 2 đến 9): khóa nhóm, kiểm
// tra Captain, bật khóa gửi hóa đơn, từ chối khi còn batch queued/processing,
// capture các bill còn mở, tạo batch kèm item, ghi activity và hoàn tất ngay
// batch rỗng. Enqueue River chạy trong hook beforeCommit, không có lời gọi mạng
// nào giữ khóa nhóm (bất biến 12).
func (r *postgresRepository) StartBulkFinalize(ctx context.Context, p repository.StartBulkFinalizeParams) (*repository.StartBulkFinalizeResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin start bulk finalize tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := sqlc.New(tx)

	if err = database.LockActiveGroup(ctx, tx, p.GroupID); err != nil {
		if errors.Is(err, database.ErrGroupNotActive) {
			return nil, domain.ErrGroupNotFound
		}
		return nil, fmt.Errorf("lock group for bulk finalize: %w", err)
	}

	member, err := q.GetGroupMember(ctx, sqlc.GetGroupMemberParams{
		GroupID: pgtype.UUID{Bytes: p.GroupID, Valid: true},
		UserID:  pgtype.UUID{Bytes: p.CallerUserID, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get caller membership for bulk finalize: %w", err)
	}
	if fmt.Sprintf("%v", member.Role) != "captain" {
		return nil, domain.ErrCaptainRequired
	}

	// Bước 4: bật khóa gửi hóa đơn ngay trong transaction này (COALESCE).
	result := &repository.StartBulkFinalizeResult{SubmissionLockedNow: false}
	current, err := q.GetGroupSubmissionLock(ctx, pgtype.UUID{Bytes: p.GroupID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("read submission lock for bulk finalize: %w", err)
	}
	var lockedAt time.Time
	if current.Valid {
		lockedAt = current.Time
	} else {
		updated, err := q.SetGroupSubmissionLockedAt(ctx, pgtype.UUID{Bytes: p.GroupID, Valid: true})
		if err != nil {
			return nil, fmt.Errorf("set submission lock for bulk finalize: %w", err)
		}
		lockedAt = updated.Time
		result.SubmissionLockedNow = true

		meta, _ := json.Marshal(map[string]any{
			"group_id":  p.GroupID.String(),
			"locked_at": lockedAt.UTC().Format(time.RFC3339),
		})
		if _, err := q.InsertGroupActivity(ctx, sqlc.InsertGroupActivityParams{
			ID:            pgtype.UUID{Bytes: uuid.Must(uuid.NewV7()), Valid: true},
			GroupID:       pgtype.UUID{Bytes: p.GroupID, Valid: true},
			ActorMemberID: member.ID,
			ActionType:    "bill_submission_locked",
			Description:   "đã khóa gửi hóa đơn mới cho nhóm",
			Metadata:      meta,
		}); err != nil {
			return nil, fmt.Errorf("insert bill_submission_locked activity for bulk finalize: %w", err)
		}
	}

	// Bước 5: từ chối request khác khi nhóm còn một batch đang queued/processing,
	// trả kèm ID an toàn để Captain tiếp tục với batch đó.
	activeBatches, err := q.GetActiveFinalizeBatch(ctx, pgtype.UUID{Bytes: p.GroupID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("find active finalize batch: %w", err)
	}
	if len(activeBatches) > 0 {
		return nil, &domain.BulkFinalizeInProgressError{ActiveBatchID: uuid.UUID(activeBatches[0].ID.Bytes).String()}
	}

	// Bước 6: capture mọi bill còn mở kèm version và review state, theo thứ tự
	// byte UUID tăng dần (ORDER BY id trên kiểu uuid của PostgreSQL).
	capturedRows, err := q.CaptureOpenBills(ctx, pgtype.UUID{Bytes: p.GroupID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("capture open bills: %w", err)
	}

	reviewedCount, unreviewedCount := 0, 0
	for _, row := range capturedRows {
		if row.CapturedReviewed {
			reviewedCount++
		} else {
			unreviewedCount++
		}
	}

	// Bước 7: tạo batch; batch rỗng hoàn tất ngay với đủ hai mốc thời gian
	// (bước 9), thỏa mãn check constraint của ma trận trạng thái.
	batchStatus := domain.BatchStatusQueued
	var startedAt, completedAt pgtype.Timestamptz
	if len(capturedRows) == 0 {
		batchStatus = domain.BatchStatusCompleted
		startedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
		completedAt = startedAt
	}
	batchRow, err := q.InsertFinalizeBatch(ctx, sqlc.InsertFinalizeBatchParams{
		ID:                  pgtype.UUID{Bytes: p.BatchID, Valid: true},
		GroupID:             pgtype.UUID{Bytes: p.GroupID, Valid: true},
		RequestedByMemberID: member.ID,
		Status:              batchStatus,
		TargetCount:         int32(len(capturedRows)),
		StartedAt:           startedAt,
		CompletedAt:         completedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("insert finalize batch: %w", err)
	}
	batch := mapFinalizeBatch(&batchRow)

	for _, row := range capturedRows {
		if _, err := q.InsertFinalizeItem(ctx, sqlc.InsertFinalizeItemParams{
			BatchID:          batchRow.ID,
			BillID:           row.ID,
			BillVersion:      row.Version,
			CapturedReviewed: row.CapturedReviewed,
		}); err != nil {
			return nil, fmt.Errorf("insert finalize item %s: %w", uuid.UUID(row.ID.Bytes), err)
		}
	}

	// Bước 8: activity khóa (khi thay đổi) rồi activity mở đầu batch.
	startMeta, _ := json.Marshal(map[string]any{
		"batch_id":                  batch.ID,
		"target_count":              batch.TargetCount,
		"captured_reviewed_count":   reviewedCount,
		"captured_unreviewed_count": unreviewedCount,
	})
	if _, err := q.InsertGroupActivity(ctx, sqlc.InsertGroupActivityParams{
		ID:            pgtype.UUID{Bytes: uuid.Must(uuid.NewV7()), Valid: true},
		GroupID:       pgtype.UUID{Bytes: p.GroupID, Valid: true},
		ActorMemberID: member.ID,
		ActionType:    "bill_bulk_finalize_started",
		Description:   fmt.Sprintf("đã bắt đầu chốt toàn bộ %d hóa đơn", len(capturedRows)),
		Metadata:      startMeta,
	}); err != nil {
		return nil, fmt.Errorf("insert bill_bulk_finalize_started activity: %w", err)
	}

	result.Batch = batch
	result.SubmissionLockedAt = lockedAt
	result.CapturedReviewedCount = reviewedCount
	result.CapturedUnreviewedCount = unreviewedCount

	if len(capturedRows) == 0 {
		// Batch rỗng: ghi luôn activity hoàn thành và thông báo cho Captain
		// trong cùng transaction; hook beforeCommit sẽ enqueue job gửi push.
		notificationIDs, err := r.insertBulkCompletionNotificationTx(ctx, q, batchRow, member.UserID)
		if err != nil {
			return nil, err
		}
		if p.BeforeCommit != nil {
			if err := p.BeforeCommit(ctx, tx, &repository.BulkStartEnqueueInfo{
				BatchID:         p.BatchID,
				BillIDs:         nil,
				NotificationIDs: notificationIDs,
				Result:          result,
			}); err != nil {
				return nil, fmt.Errorf("before commit bulk finalize hook: %w", err)
			}
		}
	} else if p.BeforeCommit != nil {
		billIDs := make([]uuid.UUID, 0, len(capturedRows))
		for _, row := range capturedRows {
			billIDs = append(billIDs, uuid.UUID(row.ID.Bytes))
		}
		if err := p.BeforeCommit(ctx, tx, &repository.BulkStartEnqueueInfo{
			BatchID:         p.BatchID,
			BillIDs:         billIDs,
			NotificationIDs: nil,
			Result:          result,
		}); err != nil {
			return nil, fmt.Errorf("before commit bulk finalize hook: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit start bulk finalize tx: %w", err)
	}

	return result, nil
}

// insertBulkCompletionNotificationTx ghi activity hoàn thành và bản ghi thông báo
// cho Captain, dùng chung cho batch rỗng (start) và batch thường (completion).
func (r *postgresRepository) insertBulkCompletionNotificationTx(ctx context.Context, q *sqlc.Queries, batchRow sqlc.GroupBillFinalizeBatch, captainUserID pgtype.UUID) ([]string, error) {
	meta, _ := json.Marshal(map[string]any{
		"batch_id":        uuid.UUID(batchRow.ID.Bytes).String(),
		"target_count":    int(batchRow.TargetCount),
		"finalized_count": int(batchRow.FinalizedCount),
		"failed_count":    int(batchRow.FailedCount),
	})
	if _, err := q.InsertGroupActivity(ctx, sqlc.InsertGroupActivityParams{
		ID:            pgtype.UUID{Bytes: uuid.Must(uuid.NewV7()), Valid: true},
		GroupID:       batchRow.GroupID,
		ActorMemberID: batchRow.RequestedByMemberID,
		ActionType:    "bill_bulk_finalize_completed",
		Description:   fmt.Sprintf("đã hoàn tất chốt toàn bộ (%d/%d hóa đơn)", batchRow.FinalizedCount, batchRow.TargetCount),
		Metadata:      meta,
	}); err != nil {
		return nil, fmt.Errorf("insert bill_bulk_finalize_completed activity: %w", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"batch_id":        uuid.UUID(batchRow.ID.Bytes).String(),
		"group_id":        uuid.UUID(batchRow.GroupID.Bytes).String(),
		"target_count":    int(batchRow.TargetCount),
		"finalized_count": int(batchRow.FinalizedCount),
		"failed_count":    int(batchRow.FailedCount),
	})
	notificationID := uuid.Must(uuid.NewV7())
	if _, err := q.CreateNotification(ctx, sqlc.CreateNotificationParams{
		ID:      pgtype.UUID{Bytes: notificationID, Valid: true},
		UserID:  captainUserID,
		Type:    "bill_bulk_finalize_completed",
		Title:   "Chốt toàn bộ hoàn tất",
		Body:    fmt.Sprintf("Đã chốt xong %d/%d hóa đơn trong nhóm.", batchRow.FinalizedCount, batchRow.TargetCount),
		Payload: payload,
	}); err != nil {
		return nil, fmt.Errorf("create bulk completion notification: %w", err)
	}
	return []string{notificationID.String()}, nil
}

// GetFinalizeBatch đọc tóm tắt batch theo (batchID, groupID).
func (r *postgresRepository) GetFinalizeBatch(ctx context.Context, batchID, groupID uuid.UUID) (*domain.FinalizeBatch, error) {
	row, err := sqlc.New(r.pool).GetFinalizeBatchByID(ctx, sqlc.GetFinalizeBatchByIDParams{
		ID:      pgtype.UUID{Bytes: batchID, Valid: true},
		GroupID: pgtype.UUID{Bytes: groupID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBatchNotFound
		}
		return nil, fmt.Errorf("get finalize batch: %w", err)
	}
	return mapFinalizeBatch(&row), nil
}

// ListBatchItemsPage đọc kết quả item phân trang cursor theo (created_at, bill_id)
// tăng dần, mặc định 20 và tối đa 100 dòng mỗi trang (Spec 0008 API surface).
func (r *postgresRepository) ListBatchItemsPage(ctx context.Context, batchID uuid.UUID, cursor *string, limit int32) ([]*domain.BatchItemResult, *string, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	var cursorCreatedAt pgtype.Timestamptz
	var cursorBillID pgtype.UUID
	if cursor != nil && *cursor != "" {
		createdAt, id, err := decodeCursor(*cursor)
		if err != nil {
			return nil, nil, domain.ErrInvalidCursor
		}
		cursorCreatedAt = pgtype.Timestamptz{Time: createdAt, Valid: true}
		cursorBillID = pgtype.UUID{Bytes: id, Valid: true}
	}

	rows, err := sqlc.New(r.pool).ListBatchItemsPage(ctx, sqlc.ListBatchItemsPageParams{
		BatchID: pgtype.UUID{Bytes: batchID, Valid: true},
		Column2: cursorCreatedAt,
		Column3: cursorBillID,
		Limit:   limit + 1,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list batch items page: %w", err)
	}

	hasMore := len(rows) > int(limit)
	if hasMore {
		rows = rows[:limit]
	}

	items := make([]*domain.BatchItemResult, 0, len(rows))
	for _, row := range rows {
		item := &domain.BatchItemResult{
			FinalizeBatchItem: domain.FinalizeBatchItem{
				BillID:           uuid.UUID(row.BillID.Bytes).String(),
				BillVersion:      row.BillVersion,
				CapturedReviewed: row.CapturedReviewed,
				Status:           fmt.Sprintf("%v", row.Status),
				CreatedAt:        row.CreatedAt.Time,
			},
		}
		if row.ErrorCode.Valid {
			v := row.ErrorCode.String
			item.ErrorCode = &v
		}
		if row.ProcessedAt.Valid {
			v := row.ProcessedAt.Time
			item.ProcessedAt = &v
		}
		if row.BillDisplayName.Valid {
			v := row.BillDisplayName.String
			item.BillDisplayName = &v
		}
		items = append(items, item)
	}

	var nextCursor *string
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		c := encodeCursor(last.CreatedAt, last.BillID)
		nextCursor = &c
	}
	return items, nextCursor, nil
}

// BeginTx mở transaction dùng chung cho worker xử lý item (Spec 0008 Batch item
// transaction): mọi thao tác của một item nằm trong đúng một transaction.
func (r *postgresRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return r.pool.Begin(ctx)
}

// LockActiveGroupInTx giữ khóa group trước batch, item và bill trong worker.
func (r *postgresRepository) LockActiveGroupInTx(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) error {
	if err := database.LockActiveGroup(ctx, tx, groupID); err != nil {
		if errors.Is(err, database.ErrGroupNotActive) {
			return domain.ErrGroupNotFound
		}
		return fmt.Errorf("lock active group for batch item: %w", err)
	}
	return nil
}

// LockBatchForItem khóa dòng batch và trả về trạng thái hiện tại.
func (r *postgresRepository) LockBatchForItem(ctx context.Context, tx pgx.Tx, batchID uuid.UUID) (*domain.FinalizeBatch, error) {
	row, err := sqlc.New(tx).LockBatchRowForUpdate(ctx, pgtype.UUID{Bytes: batchID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBatchNotFound
		}
		return nil, fmt.Errorf("lock batch row: %w", err)
	}
	return mapFinalizeBatch(&row), nil
}

// PromoteBatchToProcessing chuyển batch queued sang processing; batch đã
// processing/completed là no-op an toàn khi job được giao lại (retry safety).
func (r *postgresRepository) PromoteBatchToProcessing(ctx context.Context, tx pgx.Tx, batchID uuid.UUID) error {
	if _, err := sqlc.New(tx).PromoteBatchToProcessing(ctx, pgtype.UUID{Bytes: batchID, Valid: true}); err != nil {
		return fmt.Errorf("promote batch to processing: %w", err)
	}
	return nil
}

// LockBatchItemForUpdate khóa dòng item và trả về dữ liệu capture; item phải
// đang tồn tại trong batch, nếu không trả về ErrBatchNotFound.
func (r *postgresRepository) LockBatchItemForUpdate(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID) (*domain.FinalizeBatchItem, error) {
	row, err := sqlc.New(tx).LockBatchItemForUpdate(ctx, sqlc.LockBatchItemForUpdateParams{
		BatchID: pgtype.UUID{Bytes: batchID, Valid: true},
		BillID:  pgtype.UUID{Bytes: billID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBatchNotFound
		}
		return nil, fmt.Errorf("lock batch item: %w", err)
	}
	return mapFinalizeBatchItem(&row), nil
}

// GetBillStateForUpdateInTx khóa dòng bill và trả về trạng thái cơ bản phục vụ
// phân loại item; bill đã bị hard delete trả về ErrBillNotFound.
func (r *postgresRepository) GetBillStateForUpdateInTx(ctx context.Context, tx pgx.Tx, billID, groupID uuid.UUID) (*domain.Bill, error) {
	row, err := sqlc.New(tx).GetBillStateForBatch(ctx, sqlc.GetBillStateForBatchParams{
		ID:      pgtype.UUID{Bytes: billID, Valid: true},
		GroupID: pgtype.UUID{Bytes: groupID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBillNotFound
		}
		return nil, fmt.Errorf("lock bill state for batch: %w", err)
	}
	statusStr := fmt.Sprintf("%v", row.Status)
	return &domain.Bill{
		ID:               uuid.UUID(row.ID.Bytes),
		GroupID:          uuid.UUID(row.GroupID.Bytes),
		CreditorMemberID: uuid.UUID(row.CreditorMemberID.Bytes),
		Status:           domain.BillStatus(statusStr),
		Version:          row.Version,
	}, nil
}

// GetBillByIDInTx đọc đầy đủ bill bên trong transaction item sau khi bill đã
// được khóa, tái dùng đúng loader của đường đọc thường.
func (r *postgresRepository) GetBillByIDInTx(ctx context.Context, tx pgx.Tx, id, groupID uuid.UUID) (*domain.Bill, error) {
	return loadBillByID(ctx, sqlc.New(tx), id, groupID)
}

func (r *postgresRepository) ListActiveGroupMembersInTx(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) ([]*repository.GroupMember, error) {
	members, err := sqlc.New(tx).ListActiveGroupMembers(ctx, pgtype.UUID{Bytes: groupID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list active group members in tx: %w", err)
	}
	result := make([]*repository.GroupMember, 0, len(members))
	for _, m := range members {
		result = append(result, &repository.GroupMember{
			ID:      uuid.UUID(m.ID.Bytes),
			GroupID: uuid.UUID(m.GroupID.Bytes),
			UserID:  uuid.UUID(m.UserID.Bytes),
			Role:    fmt.Sprintf("%v", m.Role),
			Status:  fmt.Sprintf("%v", m.Status),
		})
	}
	return result, nil
}

func (r *postgresRepository) GetGroupMemberUserInTx(ctx context.Context, tx pgx.Tx, memberID, groupID uuid.UUID) (*repository.GroupMemberWithUser, error) {
	m, err := sqlc.New(tx).GetGroupMemberUser(ctx, sqlc.GetGroupMemberUserParams{
		ID:      pgtype.UUID{Bytes: memberID, Valid: true},
		GroupID: pgtype.UUID{Bytes: groupID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrInvalidInput
		}
		return nil, fmt.Errorf("get group member user in tx: %w", err)
	}

	var bankCode, bankAcc, bankHolder *string
	if m.DefaultBankCode.Valid {
		bankCode = &m.DefaultBankCode.String
	}
	if m.DefaultBankAccountNumber.Valid {
		bankAcc = &m.DefaultBankAccountNumber.String
	}
	if m.DefaultBankAccountHolder.Valid {
		bankHolder = &m.DefaultBankAccountHolder.String
	}
	return &repository.GroupMemberWithUser{
		ID:                    uuid.UUID(m.ID.Bytes),
		GroupID:               uuid.UUID(m.GroupID.Bytes),
		UserID:                uuid.UUID(m.UserID.Bytes),
		Role:                  fmt.Sprintf("%v", m.Role),
		Status:                fmt.Sprintf("%v", m.Status),
		DefaultBankCode:       bankCode,
		DefaultBankAccountNum: bankAcc,
		DefaultBankHolder:     bankHolder,
	}, nil
}

// ApplyReviewInTx ghi nhận review cho version hiện tại của bill draft bên trong
// transaction item, đúng câu UPDATE ReviewBill hiện có (status draft -> reviewed,
// version +1). Version không khớp trả về ErrVersionConflict.
func (r *postgresRepository) ApplyReviewInTx(ctx context.Context, tx pgx.Tx, billID, groupID uuid.UUID, expectedVersion int32, reviewerMemberID uuid.UUID) error {
	_, err := sqlc.New(tx).ReviewBill(ctx, sqlc.ReviewBillParams{
		ID:                 pgtype.UUID{Bytes: billID, Valid: true},
		GroupID:            pgtype.UUID{Bytes: groupID, Valid: true},
		Version:            expectedVersion,
		ReviewedByMemberID: pgtype.UUID{Bytes: reviewerMemberID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrVersionConflict
		}
		return fmt.Errorf("apply review in tx: %w", err)
	}
	return nil
}

// MarkBatchItemFinalized đánh dấu item finalized và tăng finalized_count.
func (r *postgresRepository) MarkBatchItemFinalized(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID) error {
	q := sqlc.New(tx)
	if _, err := q.MarkBatchItemFinalized(ctx, sqlc.MarkBatchItemFinalizedParams{
		BatchID: pgtype.UUID{Bytes: batchID, Valid: true},
		BillID:  pgtype.UUID{Bytes: billID, Valid: true},
	}); err != nil {
		return fmt.Errorf("mark batch item finalized: %w", err)
	}
	if _, err := q.IncrementBatchFinalizedCount(ctx, pgtype.UUID{Bytes: batchID, Valid: true}); err != nil {
		return fmt.Errorf("increment finalized count: %w", err)
	}
	return nil
}

// MarkBatchItemFailed đánh dấu item failed với mã lỗi ổn định và tăng failed_count.
func (r *postgresRepository) MarkBatchItemFailed(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID, errorCode string) error {
	q := sqlc.New(tx)
	if _, err := q.MarkBatchItemFailed(ctx, sqlc.MarkBatchItemFailedParams{
		BatchID:   pgtype.UUID{Bytes: batchID, Valid: true},
		BillID:    pgtype.UUID{Bytes: billID, Valid: true},
		ErrorCode: pgtype.Text{String: errorCode, Valid: true},
	}); err != nil {
		return fmt.Errorf("mark batch item failed: %w", err)
	}
	if _, err := q.IncrementBatchFailedCount(ctx, pgtype.UUID{Bytes: batchID, Valid: true}); err != nil {
		return fmt.Errorf("increment failed count: %w", err)
	}
	return nil
}

// RecordBatchItemFailure ghi nhận item failed trong một transaction ngắn riêng,
// sau khi transaction finalize chính đã rollback toàn bộ (Spec 0008 bước 8).
func (r *postgresRepository) RecordBatchItemFailure(ctx context.Context, batchID, groupID, billID uuid.UUID, errorCode string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin record item failure tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err = r.LockActiveGroupInTx(ctx, tx, groupID); err != nil {
		return err
	}
	batch, err := r.LockBatchForItem(ctx, tx, batchID)
	if err != nil {
		return err
	}
	if batch.GroupID != groupID.String() {
		return domain.ErrBatchNotFound
	}
	item, err := r.LockBatchItemForUpdate(ctx, tx, batchID, billID)
	if err != nil {
		return err
	}
	if item.Status != domain.BatchItemPending {
		return nil
	}
	if err = r.MarkBatchItemFailed(ctx, tx, batchID, billID, errorCode); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit record item failure tx: %w", err)
	}
	return nil
}

// TryCompleteBatch khóa batch; khi không còn item pending thì chuyển completed,
// đối chiếu đếm, ghi activity hoàn thành và tạo thông báo cho Captain active
// hiện tại tại thời điểm hoàn thành (Spec 0008 Value sourcing). Trả về true khi
// batch vừa chuyển completed trong lần gọi này.
func (r *postgresRepository) TryCompleteBatch(ctx context.Context, batchID, groupID uuid.UUID, beforeCommit func(ctx context.Context, tx pgx.Tx, notificationIDs []string) error) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin complete batch tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err = r.LockActiveGroupInTx(ctx, tx, groupID); err != nil {
		return false, err
	}
	q := sqlc.New(tx)
	batchRow, err := q.LockBatchRowForUpdate(ctx, pgtype.UUID{Bytes: batchID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, domain.ErrBatchNotFound
		}
		return false, fmt.Errorf("lock batch for completion: %w", err)
	}
	if uuid.UUID(batchRow.GroupID.Bytes) != groupID {
		return false, domain.ErrBatchNotFound
	}
	if fmt.Sprintf("%v", batchRow.Status) == domain.BatchStatusCompleted {
		return false, nil
	}

	pending, err := q.CountPendingBatchItems(ctx, pgtype.UUID{Bytes: batchID, Valid: true})
	if err != nil {
		return false, fmt.Errorf("count pending batch items: %w", err)
	}
	if pending > 0 {
		return false, nil
	}

	rows, err := q.CompleteFinalizeBatch(ctx, sqlc.CompleteFinalizeBatchParams{
		ID:               pgtype.UUID{Bytes: batchID, Valid: true},
		FinalizedCount:   batchRow.FinalizedCount,
		FailedCount:      batchRow.FailedCount,
		FinalizedCount_2: batchRow.TargetCount,
	})
	if err != nil {
		return false, fmt.Errorf("complete finalize batch: %w", err)
	}
	if rows == 0 {
		// Một worker khác đã hoàn tất batch giữa hai lần đọc.
		return false, nil
	}

	// Thông báo đi đến Captain active HIỆN TẠI; batch vẫn giữ requester làm
	// audit actor cho activity (Spec 0008 Value sourcing, Batch completion).
	captain, err := q.GetActiveCaptainMembership(ctx, batchRow.GroupID)
	if err != nil {
		return false, fmt.Errorf("get active captain for completion: %w", err)
	}
	notificationIDs, err := r.insertBulkCompletionNotificationTx(ctx, q, batchRow, captain.UserID)
	if err != nil {
		return false, err
	}

	if beforeCommit != nil {
		if err := beforeCommit(ctx, tx, notificationIDs); err != nil {
			return false, fmt.Errorf("before commit completion hook: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit complete batch tx: %w", err)
	}
	return true, nil
}

// CountActiveBatches trả về số batch đang queued/processing của nhóm, dùng làm
// rào chắn archive (Spec 0008 bất biến 6).
func (r *postgresRepository) CountActiveBatches(ctx context.Context, groupID uuid.UUID) (int64, error) {
	return sqlc.New(r.pool).CountActiveBatchesForGroup(ctx, pgtype.UUID{Bytes: groupID, Valid: true})
}

// ============================================================================
// MAPPING HELPERS
// ============================================================================

func mapFinalizeBatch(b *sqlc.GroupBillFinalizeBatch) *domain.FinalizeBatch {
	out := &domain.FinalizeBatch{
		ID:                  uuid.UUID(b.ID.Bytes).String(),
		GroupID:             uuid.UUID(b.GroupID.Bytes).String(),
		RequestedByMemberID: uuid.UUID(b.RequestedByMemberID.Bytes).String(),
		Status:              fmt.Sprintf("%v", b.Status),
		TargetCount:         int(b.TargetCount),
		FinalizedCount:      int(b.FinalizedCount),
		FailedCount:         int(b.FailedCount),
		CreatedAt:           b.CreatedAt.Time,
		UpdatedAt:           b.UpdatedAt.Time,
	}
	if b.StartedAt.Valid {
		v := b.StartedAt.Time
		out.StartedAt = &v
	}
	if b.CompletedAt.Valid {
		v := b.CompletedAt.Time
		out.CompletedAt = &v
	}
	return out
}

func mapFinalizeBatchItem(i *sqlc.GroupBillFinalizeItem) *domain.FinalizeBatchItem {
	out := &domain.FinalizeBatchItem{
		BillID:           uuid.UUID(i.BillID.Bytes).String(),
		BillVersion:      i.BillVersion,
		CapturedReviewed: i.CapturedReviewed,
		Status:           fmt.Sprintf("%v", i.Status),
		CreatedAt:        i.CreatedAt.Time,
	}
	if i.ErrorCode.Valid {
		v := i.ErrorCode.String
		out.ErrorCode = &v
	}
	if i.ProcessedAt.Valid {
		v := i.ProcessedAt.Time
		out.ProcessedAt = &v
	}
	return out
}
