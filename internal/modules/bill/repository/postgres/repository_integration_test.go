package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	"paysplit-backend/internal/modules/bill/repository/postgres"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	_ = godotenv.Load("../../../../../.env")
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		t.Skip("Bỏ qua integration test: DATABASE_URL hoặc TEST_DATABASE_URL chưa được thiết lập")
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Skipf("Không thể khởi tạo pool database: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("Không thể kết nối database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func setupTestGroupAndMembers(t *testing.T, pool *pgxpool.Pool) (groupID, creditorID, member2ID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	// 1. Tạo 2 users
	u1ID := uuid.New()
	u2ID := uuid.New()
	p1 := fmt.Sprintf("+849%08d", time.Now().UnixNano()%100000000)
	p2 := fmt.Sprintf("+848%08d", (time.Now().UnixNano()+1)%100000000)
	_, err := pool.Exec(ctx, `INSERT INTO users (id, email, phone_number, display_name, password_hash, status)
		VALUES ($1, $2, $3, 'User 1', 'hash', 'active'),
		       ($4, $5, $6, 'User 2', 'hash', 'active')`,
		u1ID, "user1_"+u1ID.String()[:8]+"@test.com", p1,
		u2ID, "user2_"+u2ID.String()[:8]+"@test.com", p2)
	if err != nil {
		t.Fatalf("Setup users failed: %v", err)
	}

	// 2. Tạo 1 group
	gID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO groups (id, name, currency, created_by) VALUES ($1, 'Test Group', 'VND', $2)`, gID, u1ID)
	if err != nil {
		t.Skipf("Setup group failed: %v", err)
	}

	// 3. Tạo 2 group_members
	m1ID := uuid.New()
	m2ID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO group_members (id, group_id, user_id, role, status)
		VALUES ($1, $2, $3, 'captain', 'active'),
		       ($4, $2, $5, 'member', 'active')`,
		m1ID, gID, u1ID, m2ID, u2ID)
	if err != nil {
		t.Skipf("Setup members failed: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM groups WHERE id = $1`, gID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1, $2)`, u1ID, u2ID)
	})

	return gID, m1ID, m2ID
}

// setupTestUser tạo một người dùng thật, vì bill_idempotency_keys.actor_user_id có khóa ngoại tới
// bảng users. setupTestGroupAndMembers trả về group_member ID chứ không phải user ID.
func setupTestUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	userID := uuid.New()
	phone := fmt.Sprintf("+847%08d", time.Now().UnixNano()%100000000)
	_, err := pool.Exec(ctx, `INSERT INTO users (id, email, phone_number, display_name, password_hash, status)
		VALUES ($1, $2, $3, 'Idempotency User', 'hash', 'active')`,
		userID, "idem_"+userID.String()[:8]+"@test.com", phone)
	if err != nil {
		t.Skipf("Setup user failed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID)
	})
	return userID
}

func TestIntegration_ListBillsIncludesPayerAndPaymentProgress(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	groupID, creditorID, debtorID := setupTestGroupAndMembers(t, pool)
	ctx := context.Background()

	finalizedID := uuid.New()
	draftID := uuid.New()
	for _, bill := range []*domain.Bill{
		{
			ID: finalizedID, GroupID: groupID, CreditorMemberID: creditorID,
			Status: domain.BillStatusDraft, Total: 200000, SplitMethod: domain.SplitMethodEven,
		},
		{
			ID: draftID, GroupID: groupID, CreditorMemberID: creditorID,
			Status: domain.BillStatusDraft, Total: 50000, SplitMethod: domain.SplitMethodEven,
		},
	} {
		if _, err := repo.CreateBill(ctx, repository.CreateBillParams{Bill: bill}); err != nil {
			t.Fatalf("CreateBill() error = %v", err)
		}
	}

	if _, err := pool.Exec(ctx, `UPDATE bills SET status='finalized', finalized_at=now() WHERE id=$1`, finalizedID); err != nil {
		t.Fatalf("finalize fixture bill: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO bill_shares(id,bill_id,group_id,member_id,final_amount)
		VALUES ($1,$2,$3,$4,100000),($5,$2,$3,$6,100000)`,
		uuid.New(), finalizedID, groupID, creditorID, uuid.New(), debtorID); err != nil {
		t.Fatalf("insert share fixtures: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO debts(id,group_id,bill_id,debtor_member_id,creditor_member_id,amount,status)
		VALUES ($1,$2,$3,$4,$5,100000,'awaiting')`,
		uuid.New(), groupID, finalizedID, debtorID, creditorID); err != nil {
		t.Fatalf("insert debt fixture: %v", err)
	}

	page, err := repo.ListBillsByGroupCursor(ctx, repository.ListBillsCursorParams{
		GroupID: groupID,
		Limit:   20,
	})
	if err != nil {
		t.Fatalf("ListBillsByGroupCursor() error = %v", err)
	}
	byID := make(map[uuid.UUID]*domain.BillListItem, len(page.Bills))
	for _, item := range page.Bills {
		byID[item.ID] = item
	}
	finalized := byID[finalizedID]
	if finalized == nil {
		t.Fatal("finalized bill missing from list")
	}
	if finalized.PayerDisplayName != "User 1" || finalized.PaidMemberCount != 1 || finalized.MemberCount != 2 {
		t.Fatalf("unexpected finalized summary: %+v", finalized)
	}
	draft := byID[draftID]
	if draft == nil || draft.PaidMemberCount != 0 || draft.MemberCount != 0 {
		t.Fatalf("unexpected draft summary: %+v", draft)
	}

	legacy, err := repo.ListBillsByGroup(ctx, groupID, 20, 0)
	if err != nil {
		t.Fatalf("ListBillsByGroup() error = %v", err)
	}
	if len(legacy) != 2 {
		t.Fatalf("legacy list length = %d, want 2", len(legacy))
	}
}

func TestIntegration_CreateBillRejectsArchivedGroup_AC9(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	groupID, creditorID, _ := setupTestGroupAndMembers(t, pool)
	if _, err := pool.Exec(context.Background(), `UPDATE groups SET status='archived' WHERE id=$1`, groupID); err != nil {
		t.Fatal(err)
	}

	billID := uuid.New()
	_, err := repo.CreateBill(context.Background(), repository.CreateBillParams{Bill: &domain.Bill{
		ID:               billID,
		GroupID:          groupID,
		CreditorMemberID: creditorID,
		Status:           domain.BillStatusDraft,
		SplitMethod:      domain.SplitMethodEven,
	}})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("CreateBill() error = %v, want ErrForbidden for archived group", err)
	}
	var count int
	if countErr := pool.QueryRow(context.Background(), `SELECT count(*) FROM bills WHERE id=$1`, billID).Scan(&count); countErr != nil {
		t.Fatal(countErr)
	}
	if count != 0 {
		t.Fatalf("archived group write created %d bills, want 0", count)
	}
}

func TestIntegration_OCRCompletionRejectsArchivedGroup_AC9(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	groupID, creditorID, _ := setupTestGroupAndMembers(t, pool)
	billID, jobID := uuid.New(), uuid.New()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO bills(id,group_id,creditor_member_id,status) VALUES($1,$2,$3,'draft')`, billID, groupID, creditorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ocr_jobs(id,bill_id,status,provider) VALUES($1,$2,'processing','test')`, jobID, billID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE groups SET status='archived' WHERE id=$1`, groupID); err != nil {
		t.Fatal(err)
	}

	if err := repo.UpdateOCRJobSuccess(ctx, jobID, nil, []byte(`{}`)); !errors.Is(err, domain.ErrOcrJobConflict) {
		t.Fatalf("UpdateOCRJobSuccess() error = %v, want ErrOcrJobConflict", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status::text FROM ocr_jobs WHERE id=$1`, jobID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "processing" {
		t.Fatalf("archived OCR job status = %q, want unchanged processing", status)
	}
}

func TestIntegration_CreateAndGetBill(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	groupID, creditorID, member2ID := setupTestGroupAndMembers(t, pool)

	billID := uuid.New()
	merchant := "Phở Bò Hà Nội"
	now := time.Now().Truncate(time.Second)

	bill := &domain.Bill{
		ID:               billID,
		GroupID:          groupID,
		CreditorMemberID: creditorID,
		Status:           domain.BillStatusDraft,
		MerchantName:     &merchant,
		BillDate:         &now,
		Subtotal:         100000,
		ServiceCharge:    5000,
		VAT:              10000,
		Discount:         0,
		Total:            115000,
		SplitMethod:      domain.SplitMethodEven,
	}

	images := []*domain.BillImage{
		{
			ID:       uuid.New(),
			BillID:   billID,
			GroupID:  groupID,
			ImageKey: "bills/op-123/0",
			Position: 0,
		},
	}

	itemID := uuid.New()
	items := []*domain.BillItem{
		{
			ID:         itemID,
			BillID:     billID,
			GroupID:    groupID,
			Name:       "Phở Đặc Biệt",
			Quantity:   "1",
			UnitPrice:  100000,
			LineTotal:  100000,
			FinalPrice: 100000,
			Position:   0,
			Assignments: []*domain.BillItemAssignment{
				{
					ID:         uuid.New(),
					BillItemID: itemID,
					GroupID:    groupID,
					MemberID:   creditorID,
					Weight:     "1.0000",
				},
				{
					ID:         uuid.New(),
					BillItemID: itemID,
					GroupID:    groupID,
					MemberID:   member2ID,
					Weight:     "1.0000",
				},
			},
		},
	}

	jobID := uuid.New()
	ocrJob := &domain.OCRJob{
		ID:       jobID,
		BillID:   billID,
		Status:   domain.OCRJobStatusQueued,
		Provider: "llamaextract",
	}

	ctx := context.Background()
	createdBill, err := repo.CreateBill(ctx, repository.CreateBillParams{
		Bill:   bill,
		Images: images,
		Items:  items,
		OCRJob: ocrJob,
	})
	if err != nil {
		t.Fatalf("CreateBill() error = %v", err)
	}

	if createdBill.ID != billID {
		t.Errorf("expected bill ID %s, got %s", billID, createdBill.ID)
	}
	if len(createdBill.Images) != 1 {
		t.Errorf("expected 1 image, got %d", len(createdBill.Images))
	}
	if len(createdBill.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(createdBill.Items))
	}

	// Get Bill By ID
	fetched, err := repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		t.Fatalf("GetBillByID() error = %v", err)
	}
	if *fetched.MerchantName != merchant {
		t.Errorf("expected merchant %s, got %s", merchant, *fetched.MerchantName)
	}
	if len(fetched.Images) != 1 || fetched.Images[0].ImageKey != "bills/op-123/0" {
		t.Errorf("expected image key bills/op-123/0, got %+v", fetched.Images)
	}
	if len(fetched.Items) != 1 || len(fetched.Items[0].Assignments) != 2 {
		t.Errorf("expected 1 item with 2 assignments, got %+v", fetched.Items)
	}

	// Get OCR Job
	job, err := repo.GetOCRJobByID(ctx, jobID)
	if err != nil {
		t.Fatalf("GetOCRJobByID() error = %v", err)
	}
	if job.Status != domain.OCRJobStatusQueued {
		t.Errorf("expected job status queued, got %s", job.Status)
	}
}

func TestIntegration_ReviewAndFinalizeBill(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	groupID, creditorID, member2ID := setupTestGroupAndMembers(t, pool)

	billID := uuid.New()
	ctx := context.Background()

	_, err := repo.CreateBill(ctx, repository.CreateBillParams{
		Bill: &domain.Bill{
			ID:               billID,
			GroupID:          groupID,
			CreditorMemberID: creditorID,
			Status:           domain.BillStatusDraft,
			Subtotal:         200000,
			Total:            200000,
			SplitMethod:      domain.SplitMethodEven,
		},
	})
	if err != nil {
		t.Fatalf("CreateBill() error = %v", err)
	}

	// 1. Review Bill
	reviewed, err := repo.ReviewBill(ctx, billID, groupID, 1, creditorID)
	if err != nil {
		t.Fatalf("ReviewBill() error = %v", err)
	}
	if reviewed.Status != domain.BillStatusReviewed || reviewed.Version != 2 {
		t.Errorf("expected reviewed status with version 2, got %s (ver: %d)", reviewed.Status, reviewed.Version)
	}
	if reviewed.ReviewedAt == nil || reviewed.ReviewedByMemberID == nil || *reviewed.ReviewedByMemberID != creditorID {
		t.Errorf("expected reviewed_at and reviewed_by_member_id to be set")
	}

	// 2. Finalize Bill với Shares
	shares := []*domain.BillShare{
		{
			ID:             uuid.New(),
			BillID:         billID,
			GroupID:        groupID,
			MemberID:       creditorID,
			ComputedAmount: 100000,
		},
		{
			ID:             uuid.New(),
			BillID:         billID,
			GroupID:        groupID,
			MemberID:       member2ID,
			ComputedAmount: 100000,
		},
	}

	finalized, err := repo.FinalizeBill(ctx, repository.FinalizeBillParams{
		BillID:          billID,
		GroupID:         groupID,
		ExpectedVersion: 2,
		Shares:          shares,
		ActorMemberID:   creditorID,
	})
	if err != nil {
		t.Fatalf("FinalizeBill() error = %v", err)
	}
	if finalized.Status != domain.BillStatusFinalized || finalized.Version != 3 {
		t.Errorf("expected finalized status with version 3, got %s (ver: %d)", finalized.Status, finalized.Version)
	}

	// 3. Void Bill
	voided, err := repo.VoidBill(ctx, repository.VoidBillParams{
		BillID:          billID,
		GroupID:         groupID,
		ExpectedVersion: 3,
		ActorMemberID:   creditorID,
	})
	if err != nil {
		t.Fatalf("VoidBill() error = %v", err)
	}
	if voided.Status != domain.BillStatusVoided || voided.Version != 4 {
		t.Errorf("expected voided status with version 4, got %s (ver: %d)", voided.Status, voided.Version)
	}
}

// TestIntegration_ReserveIdempotencyKey_ReclaimsExpiredRow chứng minh rằng một bản ghi idempotency
// đã hết hạn được chiếm lại thay vì trả về nil. Trước khi sửa, ON CONFLICT DO NOTHING không trả
// dòng nào, GetIdempotencyKey lọc bỏ bản ghi hết hạn, và tầng usecase dereference con trỏ nil.
func TestIntegration_ReserveIdempotencyKey_ReclaimsExpiredRow(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	ctx := context.Background()

	actorID := setupTestUser(t, pool)
	operation := "bill.create"
	keyHash := fmt.Sprintf("test-expired-%s", uuid.NewString())

	// Chèn thẳng một bản ghi đã hết hạn từ một giờ trước.
	_, err := pool.Exec(ctx, `
		INSERT INTO bill_idempotency_keys (
			actor_user_id, operation, key_hash, canonical_request_hash, operation_id, state,
			response_code, expires_at, created_at, updated_at
		) VALUES ($1, $2, $3, 'old-request-hash', $4, 'completed', 201, now() - interval '1 hour', now(), now());
	`, actorID, operation, keyHash, uuid.Must(uuid.NewV7()))
	if err != nil {
		t.Fatalf("không chèn được bản ghi hết hạn: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM bill_idempotency_keys WHERE actor_user_id = $1 AND operation = $2 AND key_hash = $3;`,
			actorID, operation, keyHash)
	})

	newOpID := uuid.Must(uuid.NewV7())
	rec, err := repo.ReserveIdempotencyKey(ctx, repository.ReserveIdempotencyParams{
		ActorUserID:          actorID,
		Operation:            operation,
		KeyHash:              keyHash,
		CanonicalRequestHash: "new-request-hash",
		OperationID:          newOpID,
		TTL:                  24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("ReserveIdempotencyKey() lỗi = %v", err)
	}
	if rec == nil {
		t.Fatal("ReserveIdempotencyKey() trả về nil cho bản ghi hết hạn, đây chính là lỗi panic cũ")
	}
	if rec.State != "in_progress" {
		t.Errorf("mong đợi state in_progress, nhận %q", rec.State)
	}
	if rec.CanonicalRequestHash != "new-request-hash" {
		t.Errorf("hash yêu cầu chưa được ghi đè, nhận %q", rec.CanonicalRequestHash)
	}
	if rec.OperationID != newOpID {
		t.Errorf("operation ID chưa được ghi đè, nhận %s", rec.OperationID)
	}
	if rec.ResponseCode != 0 {
		t.Errorf("response cũ chưa được xóa, nhận %d", rec.ResponseCode)
	}
	if !rec.ExpiresAt.After(time.Now()) {
		t.Errorf("expires_at chưa được gia hạn, nhận %s", rec.ExpiresAt)
	}
}

// TestIntegration_ReserveIdempotencyKey_KeepsLiveRow xác nhận bản ghi còn hạn không bị chiếm lại.
func TestIntegration_ReserveIdempotencyKey_KeepsLiveRow(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	ctx := context.Background()

	actorID := setupTestUser(t, pool)
	operation := "bill.create"
	keyHash := fmt.Sprintf("test-live-%s", uuid.NewString())
	firstOpID := uuid.Must(uuid.NewV7())

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM bill_idempotency_keys WHERE actor_user_id = $1 AND operation = $2 AND key_hash = $3;`,
			actorID, operation, keyHash)
	})

	first, err := repo.ReserveIdempotencyKey(ctx, repository.ReserveIdempotencyParams{
		ActorUserID: actorID, Operation: operation, KeyHash: keyHash,
		CanonicalRequestHash: "hash-1", OperationID: firstOpID, TTL: 24 * time.Hour,
	})
	if err != nil || first == nil {
		t.Fatalf("lần đặt chỗ đầu tiên thất bại: rec=%v err=%v", first, err)
	}

	second, err := repo.ReserveIdempotencyKey(ctx, repository.ReserveIdempotencyParams{
		ActorUserID: actorID, Operation: operation, KeyHash: keyHash,
		CanonicalRequestHash: "hash-2", OperationID: uuid.Must(uuid.NewV7()), TTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("lần đặt chỗ thứ hai lỗi = %v", err)
	}
	if second == nil {
		t.Fatal("lần đặt chỗ thứ hai trả về nil, phải trả về bản ghi còn hạn")
	}
	if second.CanonicalRequestHash != "hash-1" || second.OperationID != firstOpID {
		t.Errorf("bản ghi còn hạn bị ghi đè: hash=%q opID=%s", second.CanonicalRequestHash, second.OperationID)
	}
}

// TestIntegration_PurgeExpiredIdempotencyKeys xác nhận job dọn dẹp thực sự xóa bản ghi hết hạn.
func TestIntegration_PurgeExpiredIdempotencyKeys(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	ctx := context.Background()

	actorID := setupTestUser(t, pool)
	keyHash := fmt.Sprintf("test-purge-%s", uuid.NewString())
	_, err := pool.Exec(ctx, `
		INSERT INTO bill_idempotency_keys (
			actor_user_id, operation, key_hash, canonical_request_hash, operation_id, state,
			expires_at, created_at, updated_at
		) VALUES ($1, 'bill.create', $2, 'h', $3, 'completed', now() - interval '2 hours', now(), now());
	`, actorID, keyHash, uuid.Must(uuid.NewV7()))
	if err != nil {
		t.Fatalf("không chèn được bản ghi hết hạn: %v", err)
	}

	if _, err := repo.PurgeExpiredIdempotencyKeys(ctx); err != nil {
		t.Fatalf("PurgeExpiredIdempotencyKeys() lỗi = %v", err)
	}

	var remaining int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM bill_idempotency_keys WHERE actor_user_id = $1 AND key_hash = $2;`,
		actorID, keyHash).Scan(&remaining); err != nil {
		t.Fatalf("không đếm được bản ghi còn lại: %v", err)
	}
	if remaining != 0 {
		t.Errorf("mong đợi bản ghi hết hạn bị xóa, còn lại %d", remaining)
	}
}

// TestIntegration_CreateBill_ItemDiscountRoundTrips khớp Spec 3 AC-15, AC-17: discount_amount,
// final_price theo món và total_item_discount, general_discount ở cấp bill phải ghi và đọc lại
// đúng qua tầng repository thật, không chỉ đúng ở tầng usecase (satisfies AC-19, AC-20, AC-21 build
// plan bước 3: chứng minh check_bills_discount_composition và check_bill_items_discount không bị
// vi phạm khi discount_amount > 0).
func TestIntegration_CreateBill_ItemDiscountRoundTrips(t *testing.T) {
	pool := testPool(t)
	repo := postgres.New(pool)
	groupID, creditorID, _ := setupTestGroupAndMembers(t, pool)

	billID := uuid.New()
	itemID := uuid.New()
	ctx := context.Background()

	createdBill, err := repo.CreateBill(ctx, repository.CreateBillParams{
		Bill: &domain.Bill{
			ID:                billID,
			GroupID:           groupID,
			CreditorMemberID:  creditorID,
			Status:            domain.BillStatusDraft,
			Subtotal:          250000,
			Discount:          80000,
			TotalItemDiscount: 50000,
			GeneralDiscount:   30000,
			Total:             170000,
			SplitMethod:       domain.SplitMethodEven,
		},
		Items: []*domain.BillItem{
			{
				ID:             itemID,
				BillID:         billID,
				GroupID:        groupID,
				Name:           "Bò bít tết",
				Quantity:       "1",
				UnitPrice:      250000,
				LineTotal:      250000,
				DiscountAmount: 50000,
				FinalPrice:     200000,
				Position:       0,
				Assignments: []*domain.BillItemAssignment{
					{ID: uuid.New(), BillItemID: itemID, GroupID: groupID, MemberID: creditorID, Weight: "1.0000"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateBill() error = %v", err)
	}
	if createdBill.TotalItemDiscount != 50000 || createdBill.GeneralDiscount != 30000 {
		t.Errorf("expected total_item_discount 50000 and general_discount 30000 on create, got %d/%d",
			createdBill.TotalItemDiscount, createdBill.GeneralDiscount)
	}
	if len(createdBill.Items) != 1 || createdBill.Items[0].DiscountAmount != 50000 || createdBill.Items[0].FinalPrice != 200000 {
		t.Fatalf("expected item discount_amount 50000 and final_price 200000 on create, got %+v", createdBill.Items)
	}

	// Đọc lại từ database (không phải giá trị vừa truyền vào) để chứng minh cột thật sự lưu đúng,
	// không chỉ đúng trên object trong bộ nhớ.
	fetched, err := repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		t.Fatalf("GetBillByID() error = %v", err)
	}
	if fetched.TotalItemDiscount != 50000 || fetched.GeneralDiscount != 30000 || fetched.Discount != 80000 {
		t.Errorf("expected persisted total_item_discount 50000, general_discount 30000, discount 80000, got %d/%d/%d",
			fetched.TotalItemDiscount, fetched.GeneralDiscount, fetched.Discount)
	}
	if len(fetched.Items) != 1 || fetched.Items[0].DiscountAmount != 50000 || fetched.Items[0].FinalPrice != 200000 {
		t.Fatalf("expected persisted item discount_amount 50000 and final_price 200000, got %+v", fetched.Items)
	}
}
