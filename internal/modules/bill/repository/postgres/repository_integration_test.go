package postgres_test

import (
	"context"
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
			ID:        itemID,
			BillID:    billID,
			GroupID:   groupID,
			Name:      "Phở Đặc Biệt",
			Quantity:  "1",
			UnitPrice: 100000,
			LineTotal: 100000,
			Position:  0,
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
