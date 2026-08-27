package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	billusecase "paysplit-backend/internal/modules/bill/usecase"
)

// ============================================================================
// GROUP BILL CLOSE V1 (Spec 0008): ma trận phân loại item và các nhánh usecase,
// chạy thuần với scripted fake, không cần database.
// ============================================================================

// nopTx là một pgx.Tx tối giản: Commit/Rollback no-op (usecase defer Rollback
// sau khi Commit, pgx cho phép điều đó), còn lại panic vì không bao giờ được gọi
// trên đường fake.
type nopTx struct{}

func (nopTx) Begin(context.Context) (pgx.Tx, error) { return nil, errors.New("not implemented") }
func (nopTx) Commit(context.Context) error          { return nil }
func (nopTx) Rollback(context.Context) error        { return nil }
func (nopTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("not implemented")
}
func (nopTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (nopTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (nopTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("not implemented")
}
func (nopTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("not implemented")
}
func (nopTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}
func (nopTx) QueryRow(context.Context, string, ...any) pgx.Row { return nil }
func (nopTx) Conn() *pgx.Conn                                  { return nil }

// recordingEnqueuer ghi nhận mọi job được enqueue qua hook BeforeCommit.
type recordingEnqueuer struct {
	bulkItems     []uuid.UUID
	ocrJobs       int
	notifications []string
}

func (e *recordingEnqueuer) EnqueueOCRJobTx(ctx context.Context, tx pgx.Tx, billID, jobID, groupID uuid.UUID) error {
	e.ocrJobs++
	return nil
}
func (e *recordingEnqueuer) EnqueueOCRJob(ctx context.Context, billID, jobID, groupID uuid.UUID) error {
	e.ocrJobs++
	return nil
}
func (e *recordingEnqueuer) EnqueueNotificationTx(ctx context.Context, tx pgx.Tx, notificationID string) error {
	e.notifications = append(e.notifications, notificationID)
	return nil
}
func (e *recordingEnqueuer) EnqueueBulkFinalizeItemTx(ctx context.Context, tx pgx.Tx, batchID, billID, groupID uuid.UUID) error {
	e.bulkItems = append(e.bulkItems, billID)
	return nil
}

// scriptedCloseRepo dựng toàn bộ trạng thái cần thiết cho một kịch bản
// ProcessBulkFinalizeItem, cùng bộ đếm để khẳng định những gì ĐÃ và CHƯA được ghi.
type scriptedCloseRepo struct {
	repository.Repository

	batch      *domain.FinalizeBatch
	item       *domain.FinalizeBatchItem
	batchGone  bool
	itemLocked bool

	stateErr    error        // GetBillStateForUpdateInTx trả lỗi này (vd ErrBillNotFound)
	state       *domain.Bill // trạng thái bill dưới khóa
	fullBill    *domain.Bill // aggregate đầy đủ cho đường finalize
	members     []*repository.GroupMember
	creditor    *repository.GroupMemberWithUser
	creditorErr error
	reviewErr   error
	finalizeErr error

	tryComplete      bool
	tryCompleteErrs  []error
	tryCompleteCalls int
	completedSummary *domain.FinalizeBatch
	lockOrder        []string
	idempotencyTx    pgx.Tx
	idempotencyParam *repository.CompleteIdempotencyParams

	beginErr error

	committed        bool
	rollbacks        int
	reviewApplied    []int32
	finalizeVersions []int32
	finalizeCalls    int
	failedCodes      []string
	recordedFailures []string
	finalizedMarks   int
	promoted         bool
}

func (s *scriptedCloseRepo) BeginTx(ctx context.Context) (pgx.Tx, error) {
	if s.beginErr != nil {
		return nil, s.beginErr
	}
	return scriptedTx{s: s}, nil
}

func (s *scriptedCloseRepo) LockActiveGroupInTx(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) error {
	s.lockOrder = append(s.lockOrder, "group")
	return nil
}

// scriptedTx ghi nhận Commit/Rollback của usecase lên repo để test khẳng định.
type scriptedTx struct {
	nopTx
	s *scriptedCloseRepo
}

func (t scriptedTx) Commit(context.Context) error {
	t.s.committed = true
	return nil
}

func (t scriptedTx) Rollback(context.Context) error {
	t.s.rollbacks++
	return nil
}

func (s *scriptedCloseRepo) LockBatchForItem(ctx context.Context, tx pgx.Tx, batchID uuid.UUID) (*domain.FinalizeBatch, error) {
	s.lockOrder = append(s.lockOrder, "batch")
	if s.batchGone {
		return nil, domain.ErrBatchNotFound
	}
	return s.batch, nil
}

func (s *scriptedCloseRepo) PromoteBatchToProcessing(ctx context.Context, tx pgx.Tx, batchID uuid.UUID) error {
	s.promoted = true
	return nil
}

func (s *scriptedCloseRepo) LockBatchItemForUpdate(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID) (*domain.FinalizeBatchItem, error) {
	if s.batchGone {
		return nil, domain.ErrBatchNotFound
	}
	s.itemLocked = true
	s.lockOrder = append(s.lockOrder, "item")
	return s.item, nil
}

func (s *scriptedCloseRepo) GetBillStateForUpdateInTx(ctx context.Context, tx pgx.Tx, billID, groupID uuid.UUID) (*domain.Bill, error) {
	s.lockOrder = append(s.lockOrder, "bill")
	if s.stateErr != nil {
		return nil, s.stateErr
	}
	return s.state, nil
}

func (s *scriptedCloseRepo) GetBillByIDInTx(ctx context.Context, tx pgx.Tx, id, groupID uuid.UUID) (*domain.Bill, error) {
	return s.fullBill, nil
}

func (s *scriptedCloseRepo) ListActiveGroupMembersInTx(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) ([]*repository.GroupMember, error) {
	return s.members, nil
}

func (s *scriptedCloseRepo) GetGroupMemberUserInTx(ctx context.Context, tx pgx.Tx, memberID, groupID uuid.UUID) (*repository.GroupMemberWithUser, error) {
	if s.creditorErr != nil {
		return nil, s.creditorErr
	}
	return s.creditor, nil
}

func (s *scriptedCloseRepo) ApplyReviewInTx(ctx context.Context, tx pgx.Tx, billID, groupID uuid.UUID, expectedVersion int32, reviewerMemberID uuid.UUID) error {
	if s.reviewErr != nil {
		return s.reviewErr
	}
	s.reviewApplied = append(s.reviewApplied, expectedVersion)
	return nil
}

func (s *scriptedCloseRepo) FinalizeBillInTx(ctx context.Context, tx pgx.Tx, p repository.FinalizeBillParams) (*domain.Bill, error) {
	s.finalizeCalls++
	s.finalizeVersions = append(s.finalizeVersions, p.ExpectedVersion)
	if s.finalizeErr != nil {
		return nil, s.finalizeErr
	}
	return &domain.Bill{ID: p.BillID, Status: domain.BillStatusFinalized}, nil
}

func (s *scriptedCloseRepo) MarkBatchItemFinalized(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID) error {
	s.finalizedMarks++
	return nil
}

func (s *scriptedCloseRepo) MarkBatchItemFailed(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID, errorCode string) error {
	s.failedCodes = append(s.failedCodes, errorCode)
	return nil
}

func (s *scriptedCloseRepo) RecordBatchItemFailure(ctx context.Context, batchID, groupID, billID uuid.UUID, errorCode string) error {
	s.recordedFailures = append(s.recordedFailures, errorCode)
	return nil
}

func (s *scriptedCloseRepo) TryCompleteBatch(ctx context.Context, batchID, groupID uuid.UUID, beforeCommit func(ctx context.Context, tx pgx.Tx, notificationIDs []string) error) (bool, error) {
	s.tryCompleteCalls++
	if len(s.tryCompleteErrs) > 0 {
		err := s.tryCompleteErrs[0]
		s.tryCompleteErrs = s.tryCompleteErrs[1:]
		if err != nil {
			return false, err
		}
	}
	if s.tryComplete {
		s.batch.Status = domain.BatchStatusCompleted
	}
	return s.tryComplete, nil
}

func (s *scriptedCloseRepo) GetFinalizeBatch(ctx context.Context, batchID, groupID uuid.UUID) (*domain.FinalizeBatch, error) {
	return s.completedSummary, nil
}

// newValidFullBill dựng một hóa đơn sạch cho đối soát: subtotal khớp tổng món,
// total khớp công thức, mọi món được gán cho hai thành viên active.
func newValidFullBill(groupID, creditorID, memberID uuid.UUID, version int32, status domain.BillStatus) *domain.Bill {
	item := func(id uuid.UUID, price int64) *domain.BillItem {
		return &domain.BillItem{
			ID: id, BillID: uuid.New(), GroupID: groupID,
			Name: "Mon", Quantity: "1", UnitPrice: price, LineTotal: price, FinalPrice: price,
			Assignments: []*domain.BillItemAssignment{
				{ID: uuid.New(), BillItemID: id, MemberID: creditorID, Weight: "1.0000"},
				{ID: uuid.New(), BillItemID: id, MemberID: memberID, Weight: "1.0000"},
			},
		}
	}
	return &domain.Bill{
		ID: uuid.New(), GroupID: groupID, CreditorMemberID: creditorID,
		Status: status, Version: version,
		Subtotal: 100000, Total: 100000, SplitMethod: domain.SplitMethodEven,
		Items: []*domain.BillItem{item(uuid.New(), 60000), item(uuid.New(), 40000)},
	}
}

func newCreditorWithBank() *repository.GroupMemberWithUser {
	code, acc, holder := "VCB", "0123456789", "Owner"
	return &repository.GroupMemberWithUser{DefaultBankCode: &code, DefaultBankAccountNum: &acc, DefaultBankHolder: &holder}
}

func newScriptedFixtures() (groupID, captainID, memberID uuid.UUID, repo *scriptedCloseRepo) {
	groupID, captainID, memberID = uuid.New(), uuid.New(), uuid.New()
	repo = &scriptedCloseRepo{
		batch: &domain.FinalizeBatch{ID: uuid.NewString(), GroupID: groupID.String(), Status: domain.BatchStatusQueued},
		item:  &domain.FinalizeBatchItem{BillID: uuid.NewString(), Status: domain.BatchItemPending},
		members: []*repository.GroupMember{
			{ID: captainID, UserID: uuid.New(), Role: "captain", Status: "active"},
			{ID: memberID, UserID: uuid.New(), Role: "member", Status: "active"},
		},
		creditor:         newCreditorWithBank(),
		completedSummary: &domain.FinalizeBatch{Status: domain.BatchStatusCompleted, TargetCount: 1, FinalizedCount: 1},
	}
	return groupID, captainID, memberID, repo
}

func runItem(t *testing.T, svc *billusecase.Service, repo *scriptedCloseRepo, groupID uuid.UUID) *billusecase.ProcessItemOutcome {
	t.Helper()
	batchID, _ := uuid.Parse(repo.batch.ID)
	billID, _ := uuid.Parse(repo.item.BillID)
	outcome, err := svc.ProcessBulkFinalizeItem(context.Background(), batchID, groupID, billID)
	if err != nil {
		t.Fatalf("ProcessBulkFinalizeItem unexpected error = %v", err)
	}
	if !repo.committed && outcome.MetricOutcome != "skipped" {
		t.Fatal("transaction was never committed")
	}
	return outcome
}

func TestProcessBulkFinalizeItem_BatchAlreadyCompleted_SkipsWithoutTouchingItem_AC7(t *testing.T) {
	gid, _, _, repo := newScriptedFixtures()
	repo.batch.Status = domain.BatchStatusCompleted
	svc := billusecase.NewService(repo, nil, nil, nil, nil)

	outcome := runItem(t, svc, repo, gid)

	if outcome.MetricOutcome != "skipped" || repo.itemLocked {
		t.Fatalf("outcome=%v itemLocked=%v, want skipped and no item lock on a completed batch", outcome, repo.itemLocked)
	}
}

func TestProcessBulkFinalizeItem_ItemNotPending_SkipsDuplicateDelivery_AC7(t *testing.T) {
	gid, _, _, repo := newScriptedFixtures()
	repo.item.Status = domain.BatchItemFailed
	svc := billusecase.NewService(repo, nil, nil, nil, nil)

	outcome := runItem(t, svc, repo, gid)

	// Batch vẫn được promote đúng bước 1 của spec; điều quan trọng là item KHÔNG
	// bị ghi đè lần hai và không có bản ghi tài chính nào được chạm tới.
	if outcome.ItemStatus != domain.BatchItemFailed {
		t.Fatalf("duplicate delivery returned %v, want the stored failed status", outcome)
	}
	if len(repo.failedCodes) != 0 || repo.finalizeCalls != 0 || repo.finalizedMarks != 0 {
		t.Fatalf("duplicate delivery wrote again: codes=%v finalizes=%d marks=%d",
			repo.failedCodes, repo.finalizeCalls, repo.finalizedMarks)
	}
}

func TestProcessBulkFinalizeItem_CompletionFailureRetriesOnDuplicateDelivery_AC7(t *testing.T) {
	gid, capID, memID, repo := newScriptedFixtures()
	captured := int32(4)
	repo.state = &domain.Bill{ID: uuid.MustParse(repo.item.BillID), CreditorMemberID: capID, Status: domain.BillStatusReviewed, Version: captured}
	repo.item.BillVersion = captured
	repo.fullBill = newValidFullBill(gid, capID, memID, captured, domain.BillStatusReviewed)
	repo.tryComplete = true
	repo.tryCompleteErrs = []error{errors.New("temporary completion failure")}
	svc := billusecase.NewService(repo, nil, nil, nil, nil)
	batchID := uuid.MustParse(repo.batch.ID)
	billID := uuid.MustParse(repo.item.BillID)

	if _, err := svc.ProcessBulkFinalizeItem(context.Background(), batchID, gid, billID); err == nil {
		t.Fatal("last item swallowed completion failure, want a retryable worker error")
	}
	if repo.finalizedMarks != 1 || repo.finalizeCalls != 1 || repo.tryCompleteCalls != 1 {
		t.Fatalf("first delivery marks=%d finalizes=%d completions=%d, want 1/1/1",
			repo.finalizedMarks, repo.finalizeCalls, repo.tryCompleteCalls)
	}

	repo.item.Status = domain.BatchItemFinalized
	outcome, err := svc.ProcessBulkFinalizeItem(context.Background(), batchID, gid, billID)
	if err != nil {
		t.Fatalf("duplicate delivery did not recover completion: %v", err)
	}
	if outcome.MetricOutcome != "skipped" || repo.tryCompleteCalls != 2 {
		t.Fatalf("retry outcome=%+v completion calls=%d, want skipped and second completion attempt", outcome, repo.tryCompleteCalls)
	}
	if repo.finalizedMarks != 1 || repo.finalizeCalls != 1 {
		t.Fatalf("retry rewrote item or financial data: marks=%d finalizes=%d", repo.finalizedMarks, repo.finalizeCalls)
	}

	if _, err := svc.ProcessBulkFinalizeItem(context.Background(), batchID, gid, billID); err != nil {
		t.Fatalf("delivery after completed batch: %v", err)
	}
	if repo.tryCompleteCalls != 2 {
		t.Fatalf("completed batch attempted completion %d times, want exactly 2 total", repo.tryCompleteCalls)
	}
}

func TestProcessBulkFinalizeItem_LocksGroupBeforeBatchItemAndBill(t *testing.T) {
	gid, _, _, repo := newScriptedFixtures()
	repo.stateErr = domain.ErrBillNotFound
	svc := billusecase.NewService(repo, nil, nil, nil, nil)

	_ = runItem(t, svc, repo, gid)

	want := []string{"group", "batch", "item", "bill"}
	if len(repo.lockOrder) < len(want) {
		t.Fatalf("lock order = %v, want prefix %v", repo.lockOrder, want)
	}
	for i := range want {
		if repo.lockOrder[i] != want[i] {
			t.Fatalf("lock order = %v, want prefix %v", repo.lockOrder, want)
		}
	}
}

func TestProcessBulkFinalizeItem_RejectsBatchFromAnotherGroupBeforeItemLock(t *testing.T) {
	gid, _, _, repo := newScriptedFixtures()
	repo.batch.GroupID = uuid.NewString()
	svc := billusecase.NewService(repo, nil, nil, nil, nil)
	batchID := uuid.MustParse(repo.batch.ID)
	billID := uuid.MustParse(repo.item.BillID)

	outcome, err := svc.ProcessBulkFinalizeItem(context.Background(), batchID, gid, billID)
	if err != nil {
		t.Fatalf("mismatched group should be ignored safely: %v", err)
	}
	if outcome.MetricOutcome != "skipped" || repo.itemLocked || repo.promoted {
		t.Fatalf("mismatched batch outcome=%+v itemLocked=%v promoted=%v", outcome, repo.itemLocked, repo.promoted)
	}
	if len(repo.lockOrder) != 2 || repo.lockOrder[0] != "group" || repo.lockOrder[1] != "batch" {
		t.Fatalf("lock order = %v, want [group batch]", repo.lockOrder)
	}
}

func TestProcessBulkFinalizeItem_BillDeleted_RecordsRedactedFailure_AC6(t *testing.T) {
	gid, _, _, repo := newScriptedFixtures()
	repo.stateErr = domain.ErrBillNotFound
	svc := billusecase.NewService(repo, nil, nil, nil, nil)

	outcome := runItem(t, svc, repo, gid)

	if outcome.ErrorCode != domain.ItemErrorDeleted || len(repo.failedCodes) != 1 || repo.failedCodes[0] != domain.ItemErrorDeleted {
		t.Fatalf("deleted bill outcome=%v codes=%v, want stable BILL_DELETED recorded inline", outcome, repo.failedCodes)
	}
}

func TestProcessBulkFinalizeItem_VersionDrift_RecordsStableConflict_AC5(t *testing.T) {
	gid, _, _, repo := newScriptedFixtures()
	repo.state = &domain.Bill{ID: uuid.MustParse(repo.item.BillID), Status: domain.BillStatusDraft, Version: 6}
	svc := billusecase.NewService(repo, nil, nil, nil, nil)

	outcome := runItem(t, svc, repo, gid)

	if outcome.ErrorCode != domain.ItemErrorVersionConflict || len(repo.failedCodes) != 1 {
		t.Fatalf("version drift outcome=%v codes=%v, want one VERSION_CONFLICT failure", outcome, repo.failedCodes)
	}
	if repo.reviewApplied != nil || repo.finalizeCalls != 0 {
		t.Fatalf("drifted bill was reviewed (%v) or finalized (%d times), want neither", repo.reviewApplied, repo.finalizeCalls)
	}
}

func TestProcessBulkFinalizeItem_VoidedAfterCapture_StableFailure_AC8(t *testing.T) {
	gid, _, _, repo := newScriptedFixtures()
	captured := int32(2)
	repo.state = &domain.Bill{ID: uuid.MustParse(repo.item.BillID), Status: domain.BillStatusVoided, Version: captured}
	repo.item.BillVersion = captured
	svc := billusecase.NewService(repo, nil, nil, nil, nil)

	outcome := runItem(t, svc, repo, gid)

	if outcome.ErrorCode != domain.ItemErrorVoided || outcome.ItemStatus != domain.BatchItemFailed {
		t.Fatalf("voided outcome=%v, want failed/BILL_ALREADY_VOIDED", outcome)
	}
}

func TestProcessBulkFinalizeItem_AlreadyFinalizedFromCapture_IdempotentSuccess_AC7(t *testing.T) {
	gid, _, _, repo := newScriptedFixtures()
	captured := int32(3)
	repo.state = &domain.Bill{ID: uuid.MustParse(repo.item.BillID), Status: domain.BillStatusFinalized, Version: captured + 1}
	repo.item.BillVersion = captured
	svc := billusecase.NewService(repo, nil, nil, nil, nil)

	outcome := runItem(t, svc, repo, gid)

	if outcome.ItemStatus != domain.BatchItemFinalized || repo.finalizeCalls != 0 || repo.reviewApplied != nil {
		t.Fatalf("already-finalized bill rewrote financial data: outcome=%v finalizeCalls=%d reviews=%v",
			outcome, repo.finalizeCalls, repo.reviewApplied)
	}
}

func TestProcessBulkFinalizeItem_ReviewedDraft_FinalizesAtCapturedVersion_AC5(t *testing.T) {
	gid, capID, memID, repo := newScriptedFixtures()
	captured := int32(5)
	repo.state = &domain.Bill{ID: uuid.MustParse(repo.item.BillID), CreditorMemberID: capID, Status: domain.BillStatusReviewed, Version: captured}
	repo.item.BillVersion = captured
	repo.fullBill = newValidFullBill(gid, capID, memID, captured, domain.BillStatusReviewed)
	svc := billusecase.NewService(repo, nil, nil, nil, nil)

	outcome := runItem(t, svc, repo, gid)

	if outcome.ItemStatus != domain.BatchItemFinalized || repo.reviewApplied != nil {
		t.Fatalf("reviewed bill was re-reviewed (%v) or outcome=%v, want direct finalize without a second review", repo.reviewApplied, outcome)
	}
	if len(repo.finalizeVersions) != 1 || repo.finalizeVersions[0] != captured {
		t.Fatalf("finalize versions = %v, want exactly [%d] (existing exact version rules)", repo.finalizeVersions, captured)
	}
}

func TestProcessBulkFinalizeItem_UnreviewedDraft_ReviewsThenFinalizesInOneTransaction_AC5(t *testing.T) {
	gid, capID, memID, repo := newScriptedFixtures()
	captured := int32(2)
	repo.state = &domain.Bill{ID: uuid.MustParse(repo.item.BillID), CreditorMemberID: capID, Status: domain.BillStatusDraft, Version: captured}
	repo.item.BillVersion = captured
	repo.fullBill = newValidFullBill(gid, capID, memID, captured, domain.BillStatusDraft)
	svc := billusecase.NewService(repo, nil, nil, nil, nil)

	outcome := runItem(t, svc, repo, gid)

	if len(repo.reviewApplied) != 1 || repo.reviewApplied[0] != captured {
		t.Fatalf("reviews = %v, want exactly one review at the captured version %d", repo.reviewApplied, captured)
	}
	if len(repo.finalizeVersions) != 1 || repo.finalizeVersions[0] != captured+1 {
		t.Fatalf("finalize versions = %v, want [%d] right after the in transaction review bump", repo.finalizeVersions, captured+1)
	}
	if outcome.ItemStatus != domain.BatchItemFinalized || repo.finalizedMarks != 1 {
		t.Fatalf("outcome=%v marks=%d, want finalized with exactly one mark", outcome, repo.finalizedMarks)
	}
}

func TestProcessBulkFinalizeItem_CreditorBankMissing_InlineFailureWithoutWrites_AC5(t *testing.T) {
	gid, capID, memID, repo := newScriptedFixtures()
	repo.creditor = &repository.GroupMemberWithUser{}
	repo.state = &domain.Bill{ID: uuid.MustParse(repo.item.BillID), CreditorMemberID: capID, Status: domain.BillStatusDraft, Version: 1}
	repo.item.BillVersion = 1
	repo.fullBill = newValidFullBill(gid, capID, memID, 1, domain.BillStatusDraft)
	svc := billusecase.NewService(repo, nil, nil, nil, nil)

	outcome := runItem(t, svc, repo, gid)

	if outcome.ErrorCode != domain.ItemErrorBankRequired || outcome.ItemStatus != domain.BatchItemFailed {
		t.Fatalf("outcome=%v, want failed/BANK_ACCOUNT_REQUIRED", outcome)
	}
	if repo.reviewApplied != nil || repo.finalizeCalls != 0 {
		t.Fatalf("bank gate ran after writes: reviews=%v finalizes=%d", repo.reviewApplied, repo.finalizeCalls)
	}
	if len(repo.failedCodes) != 1 || repo.failedCodes[0] != domain.ItemErrorBankRequired {
		t.Fatalf("failure codes = %v, want exactly [BANK_ACCOUNT_REQUIRED]", repo.failedCodes)
	}
}

func TestProcessBulkFinalizeItem_ReconciliationBlocker_RecordsNotReady_AC5(t *testing.T) {
	gid, capID, memID, repo := newScriptedFixtures()
	repo.state = &domain.Bill{ID: uuid.MustParse(repo.item.BillID), CreditorMemberID: capID, Status: domain.BillStatusDraft, Version: 1}
	repo.item.BillVersion = 1
	repo.fullBill = newValidFullBill(gid, capID, memID, 1, domain.BillStatusDraft)
	repo.fullBill.Total = 999999 // TOTAL_MISMATCH -> blockers
	svc := billusecase.NewService(repo, nil, nil, nil, nil)

	outcome := runItem(t, svc, repo, gid)

	if outcome.ErrorCode != domain.ItemErrorNotReady {
		t.Fatalf("outcome=%v, want BILL_NOT_READY for a reconciliation blocker", outcome)
	}
	if repo.reviewApplied != nil || repo.finalizeCalls != 0 {
		t.Fatal("blocked bill reached the review or finalize write")
	}
}

func TestProcessBulkFinalizeItem_TransientFailure_ReturnsErrorKeepsItemPending_AC5(t *testing.T) {
	gid, capID, memID, repo := newScriptedFixtures()
	repo.state = &domain.Bill{ID: uuid.MustParse(repo.item.BillID), CreditorMemberID: capID, Status: domain.BillStatusDraft, Version: 1}
	repo.item.BillVersion = 1
	repo.fullBill = newValidFullBill(gid, capID, memID, 1, domain.BillStatusDraft)
	repo.finalizeErr = errors.New("db connection reset")
	svc := billusecase.NewService(repo, nil, nil, nil, nil)

	batchID, _ := uuid.Parse(repo.batch.ID)
	billID, _ := uuid.Parse(repo.item.BillID)
	_, err := svc.ProcessBulkFinalizeItem(context.Background(), batchID, gid, billID)
	if err == nil {
		t.Fatal("transient database error was swallowed, worker would never retry")
	}
	if len(repo.recordedFailures) != 0 || len(repo.failedCodes) != 0 {
		t.Fatalf("transient error recorded a stable failure: %v/%v, want item left pending for River retry",
			repo.recordedFailures, repo.failedCodes)
	}
}

func TestStartBulkFinalize_EnqueuesOneJobPerCapturedBill_AC4(t *testing.T) {
	gid := uuid.New()
	captain := uuid.New()
	b1, b2 := uuid.New(), uuid.New()

	repo := &startFake{result: &repository.StartBulkFinalizeResult{
		Batch:                   &domain.FinalizeBatch{ID: uuid.NewString(), GroupID: gid.String(), Status: domain.BatchStatusQueued},
		SubmissionLockedAt:      time.Now().UTC(),
		CapturedReviewedCount:   1,
		CapturedUnreviewedCount: 1,
	}}
	enqueuer := &recordingEnqueuer{}
	svc := billusecase.NewService(repo, nil, nil, nil, enqueuer)

	res, err := svc.StartBulkFinalize(context.Background(), captain, gid)
	if err != nil {
		t.Fatalf("StartBulkFinalize: %v", err)
	}
	if !res.BillSubmissionLocked || res.CapturedReviewedCount != 1 || res.CapturedUnreviewedCount != 1 {
		t.Fatalf("result = %+v, want locked true with 1/1 capture counts", res)
	}

	hook := repo.params.BeforeCommit
	if hook == nil {
		t.Fatal("service did not pass an enqueue hook into the start transaction")
	}
	if err := hook(context.Background(), nopTx{}, &repository.BulkStartEnqueueInfo{
		BatchID: uuid.MustParse(res.Batch.ID), BillIDs: []uuid.UUID{b1, b2}, NotificationIDs: []string{"n-1"},
	}); err != nil {
		t.Fatalf("enqueue hook: %v", err)
	}
	if len(enqueuer.bulkItems) != 2 || enqueuer.bulkItems[0] != b1 || enqueuer.bulkItems[1] != b2 {
		t.Fatalf("bulk jobs = %v, want one per captured bill [%s %s]", enqueuer.bulkItems, b1, b2)
	}
	if len(enqueuer.notifications) != 1 {
		t.Fatalf("notification jobs = %v, want the empty-batch completion notice", enqueuer.notifications)
	}
}

func TestStartBulkFinalize_IdempotencyCompletesInsideBatchTransaction_AC7(t *testing.T) {
	gid := uuid.New()
	captain := uuid.New()
	batchID := uuid.New()
	repo := &startFake{result: &repository.StartBulkFinalizeResult{
		Batch:                   &domain.FinalizeBatch{ID: batchID.String(), GroupID: gid.String(), Status: domain.BatchStatusQueued},
		SubmissionLockedAt:      time.Now().UTC(),
		CapturedReviewedCount:   2,
		CapturedUnreviewedCount: 1,
	}}
	svc := billusecase.NewService(repo, nil, nil, nil, nil)

	if _, err := svc.StartBulkFinalizeIdempotent(context.Background(), captain, gid, "retry-key"); err != nil {
		t.Fatalf("StartBulkFinalizeIdempotent: %v", err)
	}
	tx := nopTx{}
	if err := repo.params.BeforeCommit(context.Background(), tx, &repository.BulkStartEnqueueInfo{
		BatchID: repo.params.BatchID,
		Result:  repo.result,
	}); err != nil {
		t.Fatalf("before commit hook: %v", err)
	}
	if repo.idempotencyTx != tx || repo.idempotencyParam == nil {
		t.Fatal("idempotency result was not written through the batch transaction")
	}
	if repo.idempotencyParam.Operation != "bulk_finalize_all" || repo.idempotencyParam.KeyHash != billusecase.HashSHA256([]byte("retry-key")) {
		t.Fatalf("idempotency identity = %+v", repo.idempotencyParam)
	}
	if repo.idempotencyParam.ResourceID == nil || *repo.idempotencyParam.ResourceID != repo.params.BatchID {
		t.Fatalf("resource id = %v, want generated batch %s", repo.idempotencyParam.ResourceID, repo.params.BatchID)
	}
	var replay billusecase.StartBulkResult
	if err := json.Unmarshal(repo.idempotencyParam.ResponseBody, &replay); err != nil {
		t.Fatalf("stored response is invalid JSON: %v", err)
	}
	if replay.Batch == nil || replay.Batch.ID != batchID.String() || replay.CapturedReviewedCount != 2 || replay.CapturedUnreviewedCount != 1 {
		t.Fatalf("stored replay = %+v, want the exact 202 result", replay)
	}
}

func TestGetFinalizeBatch_AuthorizationMatrix_AC10(t *testing.T) {
	gid := uuid.New()
	caller := uuid.New()
	batchID := uuid.New()

	build := func(role string, memberErr error) *billusecase.Service {
		repo := &authzFake{role: role, memberErr: memberErr}
		return billusecase.NewService(repo, nil, nil, nil, nil)
	}

	if _, err := build("", domain.ErrInvalidInput).GetFinalizeBatch(context.Background(), caller, gid, batchID, nil, 20); !errors.Is(err, domain.ErrGroupNotFound) {
		t.Fatalf("nonmember = %v, want ErrGroupNotFound so outsiders learn nothing", err)
	}
	if _, err := build("member", nil).GetFinalizeBatch(context.Background(), caller, gid, batchID, nil, 20); !errors.Is(err, domain.ErrCaptainRequired) {
		t.Fatalf("ordinary member = %v, want ErrCaptainRequired (members must not infer batch results)", err)
	}
	svc := build("captain", nil)
	detail, err := svc.GetFinalizeBatch(context.Background(), caller, gid, batchID, nil, 20)
	if err != nil || detail == nil || detail.Batch == nil {
		t.Fatalf("captain = %v detail=%v, want the batch detail", err, detail)
	}
}

// startFake chỉ phục vụ StartBulkFinalize: lưu lại params để test gọi trực tiếp hook.
type startFake struct {
	repository.Repository
	params           repository.StartBulkFinalizeParams
	result           *repository.StartBulkFinalizeResult
	idempotencyTx    pgx.Tx
	idempotencyParam *repository.CompleteIdempotencyParams
}

func (f *startFake) StartBulkFinalize(ctx context.Context, p repository.StartBulkFinalizeParams) (*repository.StartBulkFinalizeResult, error) {
	f.params = p
	return f.result, nil
}

func (f *startFake) CompleteIdempotencyKeyInTx(ctx context.Context, tx pgx.Tx, p repository.CompleteIdempotencyParams) error {
	f.idempotencyTx = tx
	f.idempotencyParam = &p
	return nil
}

// authzFake chỉ phục vụ GetFinalizeBatch authorization.
type authzFake struct {
	repository.Repository
	role      string
	memberErr error
}

func (f *authzFake) GetGroupMember(ctx context.Context, groupID, userID uuid.UUID) (*repository.GroupMember, error) {
	if f.memberErr != nil {
		return nil, f.memberErr
	}
	return &repository.GroupMember{Role: f.role, Status: "active"}, nil
}

func (f *authzFake) GetFinalizeBatch(ctx context.Context, batchID, groupID uuid.UUID) (*domain.FinalizeBatch, error) {
	return &domain.FinalizeBatch{ID: batchID.String()}, nil
}

func (f *authzFake) ListBatchItemsPage(ctx context.Context, batchID uuid.UUID, cursor *string, limit int32) ([]*domain.BatchItemResult, *string, error) {
	return []*domain.BatchItemResult{}, nil, nil
}

func TestUnlockSubmissions_Usecase(t *testing.T) {
	gid, uid := uuid.New(), uuid.New()
	mockRepo := &mockServiceRepo{}
	svc := billusecase.NewService(mockRepo, nil, nil, nil, nil)

	if err := svc.UnlockSubmissions(context.Background(), uid, gid); err != nil {
		t.Fatalf("UnlockSubmissions returned error: %v", err)
	}
}
