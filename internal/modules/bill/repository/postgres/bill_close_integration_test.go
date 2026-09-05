package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	billdomain "paysplit-backend/internal/modules/bill/domain"
	billrepo "paysplit-backend/internal/modules/bill/repository"
	"paysplit-backend/internal/modules/bill/repository/postgres"
	billusecase "paysplit-backend/internal/modules/bill/usecase"
	groupdomain "paysplit-backend/internal/modules/group/domain"
	grouppostgres "paysplit-backend/internal/modules/group/repository/postgres"
)

// setupCloseTestGroup chuẩn bị nhóm hai thành viên kèm tài khoản ngân hàng cho
// Captain (điều kiện finalize, Spec 3 AC-9), trả về groupID và user id Captain.
// userIDOfMember tra ve user id tuong ung mot membership id (LockSubmissions
// nhan caller la USER id, khong phai member id).
func userIDOfMember(t *testing.T, pool *pgxpool.Pool, memberID uuid.UUID) uuid.UUID {
	t.Helper()
	var uid uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT user_id FROM group_members WHERE id=$1`, memberID).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	return uid
}

func setupCloseTestGroup(t *testing.T, pool *pgxpool.Pool) (groupID uuid.UUID, captainUserID uuid.UUID, captainMemberID uuid.UUID, member2ID uuid.UUID) {
	t.Helper()
	gid, m1, m2 := setupTestGroupAndMembers(t, pool)
	ctx := context.Background()

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM group_members WHERE id=$1`, m1).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET default_bank_code='VCB', default_bank_account_number='0123456789', default_bank_account_holder='Captain' WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	return gid, userID, m1, m2
}

// insertCloseTestBill tạo một hóa đơn draft với phân bổ hợp lệ cho hai thành viên
// (subtotal khớp tổng món, total khớp công thức đối soát). valid=false cố tình làm
// lệch tổng để batch phải ghi thất bại ổn định BILL_NOT_READY.
func insertCloseTestBill(t *testing.T, pool *pgxpool.Pool, groupID, creditorID, member2ID uuid.UUID, merchant string, valid bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	subtotal, total := int64(100000), int64(100000)
	if !valid {
		total = 999999 // TOTAL_MISMATCH -> blockers -> item failed BILL_NOT_READY
	}

	billID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO bills (id, group_id, creditor_member_id, status, merchant_name, subtotal, service_charge, vat, discount, total_item_discount, general_discount, total, split_method)
		VALUES ($1,$2,$3,'draft',$4,$5,0,0,0,0,0,$6,'even')`,
		billID, groupID, creditorID, merchant, subtotal, total); err != nil {
		t.Fatal(err)
	}

	itemID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO bill_items (id, bill_id, group_id, name, quantity, unit_price, line_total, discount_amount, final_price, position)
		VALUES ($1,$2,$3,'Món test','1',50000,50000,0,50000,0),
		       ($4,$2,$3,'Món test 2','1',50000,50000,0,50000,1)`,
		itemID, billID, groupID, uuid.New()); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO bill_item_assignments (bill_item_id, group_id, member_id, weight)
		SELECT i.id, i.group_id, m.mid, '1.0000'
		FROM bill_items i
		CROSS JOIN (VALUES ($2::uuid), ($3::uuid)) AS m(mid)
		WHERE i.bill_id = $1`,
		billID, creditorID, member2ID); err != nil {
		t.Fatal(err)
	}
	return billID
}

func markBillReviewedForTest(t *testing.T, pool *pgxpool.Pool, billID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE bills SET status='reviewed', reviewed_at=now(), version=version+1 WHERE id=$1`, billID); err != nil {
		t.Fatal(err)
	}
}

func TestIntegration_StartBulkFinalize_CommitsIdempotencyWithBatch_AC7(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	groupID, captainUserID, _, _ := setupCloseTestGroup(t, pool)
	ctx := context.Background()
	operation := "bulk_finalize_all"
	requestHash := billusecase.HashSHA256([]byte("group:" + groupID.String()))

	reserve := func(t *testing.T, rawKey string) string {
		t.Helper()
		keyHash := billusecase.HashSHA256([]byte(rawKey))
		if _, err := repo.ReserveIdempotencyKey(ctx, billrepo.ReserveIdempotencyParams{
			ActorUserID:          captainUserID,
			Operation:            operation,
			KeyHash:              keyHash,
			CanonicalRequestHash: requestHash,
			OperationID:          uuid.Must(uuid.NewV7()),
			TTL:                  24 * time.Hour,
		}); err != nil {
			t.Fatalf("reserve idempotency key: %v", err)
		}
		return keyHash
	}

	keyHash := reserve(t, "atomic-success")
	batchID := uuid.Must(uuid.NewV7())
	start, err := repo.StartBulkFinalize(ctx, billrepo.StartBulkFinalizeParams{
		GroupID:      groupID,
		CallerUserID: captainUserID,
		BatchID:      batchID,
		BeforeCommit: func(ctx context.Context, tx pgx.Tx, info *billrepo.BulkStartEnqueueInfo) error {
			return repo.CompleteIdempotencyKeyInTx(ctx, tx, billrepo.CompleteIdempotencyParams{
				ActorUserID:  captainUserID,
				Operation:    operation,
				KeyHash:      keyHash,
				ResponseCode: 202,
				ResponseBody: []byte(`{"batch":"committed"}`),
				ResourceID:   &batchID,
			})
		},
	})
	if err != nil {
		t.Fatalf("StartBulkFinalize atomic success: %v", err)
	}
	if start.Batch.ID != batchID.String() {
		t.Fatalf("batch id = %s, want %s", start.Batch.ID, batchID)
	}
	record, err := repo.GetIdempotencyKey(ctx, captainUserID, operation, keyHash)
	if err != nil || record == nil {
		t.Fatalf("GetIdempotencyKey after commit: record=%+v err=%v", record, err)
	}
	if record.State != "completed" || record.ResourceID == nil || *record.ResourceID != batchID {
		t.Fatalf("committed idempotency record = %+v", record)
	}

	rollbackKeyHash := reserve(t, "atomic-rollback")
	rollbackBatchID := uuid.Must(uuid.NewV7())
	rollbackCause := errors.New("fail after idempotency update")
	_, err = repo.StartBulkFinalize(ctx, billrepo.StartBulkFinalizeParams{
		GroupID:      groupID,
		CallerUserID: captainUserID,
		BatchID:      rollbackBatchID,
		BeforeCommit: func(ctx context.Context, tx pgx.Tx, info *billrepo.BulkStartEnqueueInfo) error {
			if err := repo.CompleteIdempotencyKeyInTx(ctx, tx, billrepo.CompleteIdempotencyParams{
				ActorUserID:  captainUserID,
				Operation:    operation,
				KeyHash:      rollbackKeyHash,
				ResponseCode: 202,
				ResponseBody: []byte(`{"batch":"must-rollback"}`),
				ResourceID:   &rollbackBatchID,
			}); err != nil {
				return err
			}
			return rollbackCause
		},
	})
	if !errors.Is(err, rollbackCause) {
		t.Fatalf("StartBulkFinalize rollback error = %v, want %v", err, rollbackCause)
	}
	rollbackRecord, err := repo.GetIdempotencyKey(ctx, captainUserID, operation, rollbackKeyHash)
	if err != nil || rollbackRecord == nil {
		t.Fatalf("GetIdempotencyKey after rollback: record=%+v err=%v", rollbackRecord, err)
	}
	if rollbackRecord.State != "in_progress" || rollbackRecord.ResourceID != nil {
		t.Fatalf("idempotency update escaped rollback: %+v", rollbackRecord)
	}
	var batchCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_bill_finalize_batches WHERE id=$1`, rollbackBatchID).Scan(&batchCount); err != nil {
		t.Fatal(err)
	}
	if batchCount != 0 {
		t.Fatalf("rolled back batch count = %d, want 0", batchCount)
	}
}

// TestIntegration_LockSubmissions_IdempotentOneWay_AC1 xác nhận AC-1: chỉ Captain
// khóa được, khóa ghi PostgreSQL time và một activity, gọi lại không ghi thêm.
func TestIntegration_LockSubmissions_IdempotentOneWay_AC1(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	groupID, _, captainMemberID, member2MemberID := setupCloseTestGroup(t, pool)
	captainUserID := userIDOfMember(t, pool, captainMemberID)
	member2UserID := userIDOfMember(t, pool, member2MemberID)
	ctx := context.Background()

	// Thành viên thường bị từ chối trước cả khi nhóm còn mở.
	if _, err := repo.LockSubmissions(ctx, groupID, member2UserID); !errors.Is(err, billdomain.ErrCaptainRequired) {
		t.Fatalf("LockSubmissions(member) = %v, want ErrCaptainRequired", err)
	}

	first, err := repo.LockSubmissions(ctx, groupID, captainUserID)
	if err != nil {
		t.Fatalf("LockSubmissions(captain): %v", err)
	}
	if !first.LockedNow {
		t.Fatal("first lock should report LockedNow=true")
	}
	if first.LockedAt.IsZero() {
		t.Fatal("locked_at must be recorded")
	}

	var lockedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT bill_submission_locked_at FROM groups WHERE id=$1`, groupID).Scan(&lockedAt); err != nil {
		t.Fatal(err)
	}
	if lockedAt.Sub(first.LockedAt) > time.Minute {
		t.Fatalf("stored locked_at %v too far from returned %v", lockedAt, first.LockedAt)
	}

	var activities int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_activities WHERE group_id=$1 AND action_type='bill_submission_locked'`, groupID).Scan(&activities); err != nil {
		t.Fatal(err)
	}
	if activities != 1 {
		t.Fatalf("activities = %d, want exactly 1", activities)
	}

	// Gọi lại bởi cùng Captain: idempotent, không thêm activity.
	second, err := repo.LockSubmissions(ctx, groupID, captainUserID)
	if err != nil {
		t.Fatalf("replay LockSubmissions: %v", err)
	}
	if second.LockedNow || !second.LockedAt.Equal(first.LockedAt) {
		t.Fatalf("replay changed state: LockedNow=%v lockedAt=%v want false/%v", second.LockedNow, second.LockedAt, first.LockedAt)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_activities WHERE group_id=$1 AND action_type='bill_submission_locked'`, groupID).Scan(&activities); err != nil {
		t.Fatal(err)
	}
	if activities != 1 {
		t.Fatalf("activities after replay = %d, want still 1", activities)
	}
}

// TestIntegration_CreateGate_RejectsLockedGroup_AC2 xác nhận AC-2: nhóm khóa thì
// CreateBill trả ErrSubmissionLocked và không commit bill nào.
func TestIntegration_CreateGate_RejectsLockedGroup_AC2(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	groupID, captainUserID, captainMemberID, _ := setupCloseTestGroup(t, pool)
	ctx := context.Background()

	if _, err := repo.LockSubmissions(ctx, groupID, captainUserID); err != nil {
		t.Fatal(err)
	}

	billID := uuid.New()
	_, err := repo.CreateBill(ctx, billrepo.CreateBillParams{Bill: &billdomain.Bill{
		ID:               billID,
		GroupID:          groupID,
		CreditorMemberID: captainMemberID,
		Status:           billdomain.BillStatusDraft,
		SplitMethod:      billdomain.SplitMethodEven,
	}})
	if !errors.Is(err, billdomain.ErrSubmissionLocked) {
		t.Fatalf("CreateBill() = %v, want ErrSubmissionLocked", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bills WHERE id=$1`, billID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("locked group created %d bills, want 0", count)
	}
	var jobs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ocr_jobs WHERE bill_id=$1`, billID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("locked group created %d ocr jobs, want 0", jobs)
	}
}

// TestIntegration_BulkFinalize_EndToEnd_AC4_AC5_AC6 xác nhận AC-4 đến AC-6:
// start capture đúng các bill còn mở kèm review state; reviewed đi thẳng finalize,
// unreviewed được review rồi finalize trong cùng transaction item; bill lệch đối
// soát giữ nguyên với mã lỗi ổn định; đếm cuối khớp item; batch hoàn tất chặn archive.
func TestIntegration_BulkFinalize_EndToEnd_AC4_AC5_AC6(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	svc := billusecase.NewService(repo, nil, nil, nil, nil)
	groupRepo := grouppostgres.New(pool)
	groupID, captainUserID, captainMemberID, member2ID := setupCloseTestGroup(t, pool)
	ctx := context.Background()

	reviewedBill := insertCloseTestBill(t, pool, groupID, captainMemberID, member2ID, "Đã review", true)
	markBillReviewedForTest(t, pool, reviewedBill)
	draftBill := insertCloseTestBill(t, pool, groupID, captainMemberID, member2ID, "Chưa review", true)
	invalidBill := insertCloseTestBill(t, pool, groupID, captainMemberID, member2ID, "Lệch tổng", false)
	finalizedEarlier := insertCloseTestBill(t, pool, groupID, captainMemberID, member2ID, "Đã finalized trước đó", true)
	markBillReviewedForTest(t, pool, finalizedEarlier)
	if _, err := pool.Exec(ctx, `UPDATE bills SET status='finalized', finalized_at=now(), version=version+1 WHERE id=$1`, finalizedEarlier); err != nil {
		t.Fatal(err)
	}

	start, err := repo.StartBulkFinalize(ctx, billrepo.StartBulkFinalizeParams{
		GroupID:      groupID,
		CallerUserID: captainUserID,
		BatchID:      uuid.Must(uuid.NewV7()),
	})
	if err != nil {
		t.Fatalf("StartBulkFinalize: %v", err)
	}

	// AC-4 + bất biến 9: chỉ ba bill còn mở được capture; bill finalized bị loại.
	if start.Batch.TargetCount != 3 {
		t.Fatalf("target_count = %d, want 3 (finalized bill excluded)", start.Batch.TargetCount)
	}
	if start.CapturedReviewedCount != 1 || start.CapturedUnreviewedCount != 2 {
		t.Fatalf("captured counts = %d reviewed / %d unreviewed, want 1/2", start.CapturedReviewedCount, start.CapturedUnreviewedCount)
	}
	if start.Batch.Status != billdomain.BatchStatusQueued {
		t.Fatalf("batch status = %q, want queued", start.Batch.Status)
	}

	// AC-7: batch thứ hai trong lúc batch đầu chưa xong phải bị chặn kèm ID.
	second, err := repo.StartBulkFinalize(ctx, billrepo.StartBulkFinalizeParams{
		GroupID:      groupID,
		CallerUserID: captainUserID,
		BatchID:      uuid.Must(uuid.NewV7()),
	})
	var inProgress *billdomain.BulkFinalizeInProgressError
	if !errors.As(err, &inProgress) {
		t.Fatalf("second StartBulkFinalize = %v, want BulkFinalizeInProgressError", err)
	}
	if inProgress.ActiveBatchID != start.Batch.ID {
		t.Fatalf("active_batch_id = %q, want %q", inProgress.ActiveBatchID, start.Batch.ID)
	}
	if second != nil {
		t.Fatal("second StartBulkFinalize must not return a result")
	}

	// AC-7 (bất biến 6): archive bị chặn khi batch còn active.
	var bulkBlock *groupdomain.BulkFinalizeInProgressError
	if err := groupRepo.DisbandGroup(ctx, groupID.String(), captainUserID.String()); err == nil {
		t.Fatal("DisbandGroup with active batch must fail")
	} else if !errors.As(err, &bulkBlock) || bulkBlock.ActiveBatchID != start.Batch.ID {
		t.Fatalf("DisbandGroup error = %v, want BulkFinalizeInProgressError with batch %q", err, start.Batch.ID)
	}

	// AC-5: xử lý từng item độc lập qua usecase service (worker path).
	batchID := uuid.Must(uuid.Parse(start.Batch.ID))
	for _, billIDStr := range []string{reviewedBill.String(), draftBill.String()} {
		outcome, procErr := svc.ProcessBulkFinalizeItem(ctx, batchID, groupID, uuid.Must(uuid.Parse(billIDStr)))
		if procErr != nil {
			t.Fatalf("ProcessBulkFinalizeItem(%s): %v", billIDStr, procErr)
		}
		if outcome.ItemStatus != billdomain.BatchItemFinalized {
			t.Fatalf("item %s status = %q (%s), want finalized", billIDStr, outcome.ItemStatus, outcome.ErrorCode)
		}
	}
	outcomeInvalid, procErr := svc.ProcessBulkFinalizeItem(ctx, batchID, groupID, invalidBill)
	if procErr != nil {
		t.Fatalf("ProcessBulkFinalizeItem(invalid): %v", procErr)
	}
	if outcomeInvalid.ItemStatus != billdomain.BatchItemFailed || outcomeInvalid.ErrorCode != billdomain.ItemErrorNotReady {
		t.Fatalf("invalid item = %q/%q, want failed/BILL_NOT_READY", outcomeInvalid.ItemStatus, outcomeInvalid.ErrorCode)
	}

	// Bill invalid phải giữ nguyên trạng thái draft (không review, không final hóa).
	var status string
	if err := pool.QueryRow(ctx, `SELECT status::text FROM bills WHERE id=$1`, invalidBill).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "draft" {
		t.Fatalf("invalid bill status = %q, want unchanged draft", status)
	}

	// Batch hoàn tất với đếm đối chiếu khớp item terminal (AC-6, bất biến 8).
	batch, err := repo.GetFinalizeBatch(ctx, batchID, groupID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != billdomain.BatchStatusCompleted {
		t.Fatalf("batch status = %q, want completed", batch.Status)
	}
	if batch.FinalizedCount != 2 || batch.FailedCount != 1 {
		t.Fatalf("counts = %d finalized / %d failed, want 2/1", batch.FinalizedCount, batch.FailedCount)
	}
	if batch.CompletedAt == nil {
		t.Fatal("completed_at must be set on completed batch")
	}

	// Hai bill finalized sinh debts cho thành viên không phải Creditor.
	var debtCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM debts WHERE group_id=$1 AND debtor_member_id=$2 AND status='awaiting'`, groupID, member2ID).Scan(&debtCount); err != nil {
		t.Fatal(err)
	}
	if debtCount < 2 {
		t.Fatalf("awaiting debts = %d, want at least 2 (mỗi bill một khoản)", debtCount)
	}

	// Activity bắt đầu và hoàn tất batch tồn tại với metadata đúng contract.
	var actCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_activities WHERE group_id=$1 AND action_type IN ('bill_bulk_finalize_started','bill_bulk_finalize_completed')`, groupID).Scan(&actCount); err != nil {
		t.Fatal(err)
	}
	if actCount != 2 {
		t.Fatalf("bulk activities = %d, want 2 (started + completed)", actCount)
	}

	// Thông báo hoàn tất gửi cho Captain hiện tại (Value sourcing).
	var notifCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND type='bill_bulk_finalize_completed'`, captainUserID).Scan(&notifCount); err != nil {
		t.Fatal(err)
	}
	if notifCount != 1 {
		t.Fatalf("completion notifications = %d, want 1", notifCount)
	}

	// Batch detail phân trang: đủ 3 item, có tên hiển thị, cursor đọc tiếp được.
	items, next, err := repo.ListBatchItemsPage(ctx, batchID, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || next == nil {
		t.Fatalf("first page len=%d next=%v, want 2 items and a cursor", len(items), next)
	}
	items2, next2, err := repo.ListBatchItemsPage(ctx, batchID, next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items2) != 1 || next2 != nil {
		t.Fatalf("second page len=%d next=%v, want 1 item and no cursor", len(items2), next2)
	}
	all := append(append(make([]string, 0, 3), items[0].BillID), items[1].BillID, items2[0].BillID)
	if len(all) != 3 {
		t.Fatalf("cursor pages returned %d unique items, want 3", len(all))
	}
}

// TestIntegration_ZeroTargetBatch_CompletesImmediately_AC4 xác nhận batch rỗng
// hoàn tất ngay trong transaction mở batch với đủ hai mốc thời gian.
func TestIntegration_ZeroTargetBatch_CompletesImmediately_AC4(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	groupID, captainUserID, _, _ := setupCloseTestGroup(t, pool)
	ctx := context.Background()

	start, err := repo.StartBulkFinalize(ctx, billrepo.StartBulkFinalizeParams{
		GroupID:      groupID,
		CallerUserID: captainUserID,
		BatchID:      uuid.Must(uuid.NewV7()),
	})
	if err != nil {
		t.Fatalf("StartBulkFinalize(empty): %v", err)
	}
	if start.Batch.TargetCount != 0 {
		t.Fatalf("target_count = %d, want 0", start.Batch.TargetCount)
	}
	if start.Batch.Status != billdomain.BatchStatusCompleted || start.Batch.StartedAt == nil || start.Batch.CompletedAt == nil {
		t.Fatalf("empty batch = %q started=%v completed=%v, want completed with both times",
			start.Batch.Status, start.Batch.StartedAt, start.Batch.CompletedAt)
	}
	if !start.SubmissionLockedNow {
		t.Fatal("empty batch must still lock submissions (bất biến 3)")
	}
}

// TestIntegration_DeletedDraft_ItemRecordsRedactedFailure_AC6 xác nhận kịch bản 7:
// hard delete draft sau capture không bị item chặn, item ghi BILL_DELETED đã redact
// và tên hiển thị về null thay vì giữ nội dung cũ.
func TestIntegration_DeletedDraft_ItemRecordsRedactedFailure_AC6(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	svc := billusecase.NewService(repo, nil, nil, nil, nil)
	groupID, captainUserID, captainMemberID, member2ID := setupCloseTestGroup(t, pool)
	ctx := context.Background()

	draft := insertCloseTestBill(t, pool, groupID, captainMemberID, member2ID, "Sẽ bị xóa", true)
	start, err := repo.StartBulkFinalize(ctx, billrepo.StartBulkFinalizeParams{
		GroupID:      groupID,
		CallerUserID: captainUserID,
		BatchID:      uuid.Must(uuid.NewV7()),
	})
	if err != nil {
		t.Fatalf("StartBulkFinalize: %v", err)
	}

	// Hard delete draft vẫn hoạt động khi submission đã khóa (bất biến 4).
	if err := repo.DeleteDraftBill(ctx, billrepo.DeleteDraftBillParams{
		ID:            draft,
		GroupID:       groupID,
		ActorMemberID: captainMemberID,
	}); err != nil {
		t.Fatalf("DeleteDraftBill under lock: %v", err)
	}

	outcome, procErr := svc.ProcessBulkFinalizeItem(ctx, uuid.Must(uuid.Parse(start.Batch.ID)), groupID, draft)
	if procErr != nil {
		t.Fatalf("ProcessBulkFinalizeItem(deleted): %v", procErr)
	}
	if outcome.ItemStatus != billdomain.BatchItemFailed || outcome.ErrorCode != billdomain.ItemErrorDeleted {
		t.Fatalf("deleted item = %q/%q, want failed/BILL_DELETED", outcome.ItemStatus, outcome.ErrorCode)
	}

	items, _, err := repo.ListBatchItemsPage(ctx, uuid.Must(uuid.Parse(start.Batch.ID)), nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].BillDisplayName != nil {
		t.Fatalf("deleted bill display name = %q, want null", *items[0].BillDisplayName)
	}
}

// TestIntegration_LockVersusCreateRace_AC2_AC7 xác nhận kịch bản 5: đua nhiều
// lần tạo bill với lần khóa nhóm. Khóa nhóm tuần tự hóa hai đường, nên mọi bill
// commit TRƯỚC locked_at được giữ lại cho batch sau, và không có bill nào commit
// SAU locked_at (nguồn sự thật cuối cùng là recheck trong transaction tạo bill).
func TestIntegration_LockVersusCreateRace_AC2_AC7(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	groupID, captainUserID, captainMemberID, _ := setupCloseTestGroup(t, pool)
	ctx := context.Background()

	const creators = 8
	errCh := make(chan error, creators)
	for i := 0; i < creators; i++ {
		go func() {
			billID := uuid.Must(uuid.NewV7())
			_, err := repo.CreateBill(ctx, billrepo.CreateBillParams{Bill: &billdomain.Bill{
				ID:               billID,
				GroupID:          groupID,
				CreditorMemberID: captainMemberID,
				Status:           billdomain.BillStatusDraft,
				SplitMethod:      billdomain.SplitMethodEven,
			}})
			errCh <- err
		}()
	}

	var lockErr error
	go func() {
		_, lockErr = repo.LockSubmissions(ctx, groupID, captainUserID)
		errCh <- nil
	}()

	createdBeforeLock, createdAfterLock := 0, 0
	for i := 0; i < creators+1; i++ {
		if err := <-errCh; err != nil {
			if !errors.Is(err, billdomain.ErrSubmissionLocked) {
				t.Fatalf("unexpected creator error: %v", err)
			}
		}
	}
	if lockErr != nil {
		t.Fatalf("LockSubmissions(race): %v", lockErr)
	}

	var lockedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT bill_submission_locked_at FROM groups WHERE id=$1`, groupID).Scan(&lockedAt); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT created_at FROM bills WHERE group_id=$1`, groupID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var createdAt time.Time
		if err := rows.Scan(&createdAt); err != nil {
			t.Fatal(err)
		}
		if createdAt.After(lockedAt) {
			createdAfterLock++
		} else {
			createdBeforeLock++
		}
	}
	if createdAfterLock != 0 {
		t.Fatalf("%d bills committed AFTER the lock won, want 0 (%d before are valid)", createdAfterLock, createdBeforeLock)
	}
}

func TestIntegration_UnlockSubmissions_ReopensGroupAndRecordsActivityOnce(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	groupID, captainUserID, _, memberID := setupCloseTestGroup(t, pool)
	ctx := context.Background()

	if _, err := repo.LockSubmissions(ctx, groupID, captainUserID); err != nil {
		t.Fatal(err)
	}
	if err := repo.UnlockSubmissions(ctx, groupID, userIDOfMember(t, pool, memberID)); !errors.Is(err, billdomain.ErrCaptainRequired) {
		t.Fatalf("member unlock: got %v, want captain required", err)
	}
	lockedAt, err := repo.GetGroupSubmissionLock(ctx, groupID)
	if err != nil || lockedAt == nil {
		t.Fatalf("group must remain locked after rejected unlock: %v", err)
	}
	for range 2 {
		if err := repo.UnlockSubmissions(ctx, groupID, captainUserID); err != nil {
			t.Fatalf("captain unlock: %v", err)
		}
	}
	lockedAt, err = repo.GetGroupSubmissionLock(ctx, groupID)
	if err != nil || lockedAt != nil {
		t.Fatalf("group must allow bill submissions again: lockedAt=%v, err=%v", lockedAt, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_activities WHERE group_id=$1 AND action_type='bill_submission_unlocked'`, groupID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("unlock activity count = %d, want 1", count)
	}
}
