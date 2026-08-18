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
	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	"paysplit-backend/internal/modules/bill/repository/postgres/sqlc"
)

type postgresRepository struct {
	pool *pgxpool.Pool
}

// New khởi tạo Postgres repository cho module Bill & OCR.
func New(pool *pgxpool.Pool) repository.Repository {
	if pool == nil {
		panic("bill repository pool must not be nil")
	}
	return &postgresRepository{pool: pool}
}

// CreateBill lưu hóa đơn mới (kèm danh sách ảnh, món ăn và ocr job nếu có) trong 1 database transaction nguyên tử.
func (r *postgresRepository) CreateBill(ctx context.Context, p repository.CreateBillParams) (*domain.Bill, error) {
	if p.Bill == nil {
		return nil, domain.ErrInvalidInput
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create bill tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := sqlc.New(tx)

	// 1. Tạo bản ghi bills
	var billDate pgtype.Date
	if p.Bill.BillDate != nil {
		billDate = pgtype.Date{Time: *p.Bill.BillDate, Valid: true}
	}

	var replacesID pgtype.UUID
	if p.Bill.ReplacesBillID != nil {
		replacesID = pgtype.UUID{Bytes: *p.Bill.ReplacesBillID, Valid: true}
	}

	merchantName := pgtype.Text{}
	if p.Bill.MerchantName != nil {
		merchantName = pgtype.Text{String: *p.Bill.MerchantName, Valid: true}
	}

	statusStr := string(p.Bill.Status)
	if statusStr == "" {
		statusStr = string(domain.BillStatusDraft)
	}

	splitMethod := string(p.Bill.SplitMethod)
	if splitMethod == "" {
		splitMethod = string(domain.SplitMethodEven)
	}

	mismatchCodes := p.Bill.MismatchCodes
	if mismatchCodes == nil {
		mismatchCodes = []string{}
	}

	dbBill, err := q.CreateBill(ctx, sqlc.CreateBillParams{
		ID:               pgtype.UUID{Bytes: p.Bill.ID, Valid: true},
		GroupID:          pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true},
		CreditorMemberID: pgtype.UUID{Bytes: p.Bill.CreditorMemberID, Valid: true},
		Status:           statusStr,
		MerchantName:     merchantName,
		BillDate:         billDate,
		Subtotal:         p.Bill.Subtotal,
		ServiceCharge:    p.Bill.ServiceCharge,
		Vat:              p.Bill.VAT,
		Discount:         p.Bill.Discount,
		Total:            p.Bill.Total,
		SplitMethod:      splitMethod,
		MismatchCodes:    mismatchCodes,
		ReplacesBillID:   replacesID,
	})
	if err != nil {
		return nil, fmt.Errorf("create bill row: %w", err)
	}

	// 2. Lưu danh sách ảnh bill_images (1-5 ảnh)
	createdImages := make([]*domain.BillImage, 0, len(p.Images))
	for _, img := range p.Images {
		dbImg, err := q.CreateBillImage(ctx, sqlc.CreateBillImageParams{
			ID:       pgtype.UUID{Bytes: img.ID, Valid: true},
			BillID:   pgtype.UUID{Bytes: p.Bill.ID, Valid: true},
			GroupID:  pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true},
			ImageKey: img.ImageKey,
			Position: img.Position,
		})
		if err != nil {
			return nil, fmt.Errorf("create bill image pos %d: %w", img.Position, err)
		}
		createdImages = append(createdImages, toDomainBillImage(&dbImg))
	}

	// 3. Lưu danh sách món ăn bill_items và assignments
	createdItems := make([]*domain.BillItem, 0, len(p.Items))
	for _, item := range p.Items {
		var qtyNum pgtype.Numeric
		if err := qtyNum.Scan(item.Quantity); err != nil {
			qtyNum.Scan("1")
		}

		dbItem, err := q.CreateBillItem(ctx, sqlc.CreateBillItemParams{
			ID:        pgtype.UUID{Bytes: item.ID, Valid: true},
			BillID:    pgtype.UUID{Bytes: p.Bill.ID, Valid: true},
			GroupID:   pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true},
			Name:      item.Name,
			Quantity:  qtyNum,
			UnitPrice: item.UnitPrice,
			LineTotal: item.LineTotal,
			Position:  item.Position,
		})
		if err != nil {
			return nil, fmt.Errorf("create bill item %s: %w", item.Name, err)
		}

		domItem := toDomainBillItem(&dbItem)

		for _, assign := range item.Assignments {
			var wNum pgtype.Numeric
			if err := wNum.Scan(assign.Weight); err != nil {
				wNum.Scan("1")
			}

			dbAssign, err := q.CreateBillItemAssignment(ctx, sqlc.CreateBillItemAssignmentParams{
				ID:         pgtype.UUID{Bytes: assign.ID, Valid: true},
				BillItemID: pgtype.UUID{Bytes: item.ID, Valid: true},
				GroupID:    pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true},
				MemberID:   pgtype.UUID{Bytes: assign.MemberID, Valid: true},
				Weight:     wNum,
			})
			if err != nil {
				return nil, fmt.Errorf("create item assignment: %w", err)
			}
			domItem.Assignments = append(domItem.Assignments, toDomainBillItemAssignment(&dbAssign))
		}

		createdItems = append(createdItems, domItem)
	}

	// 4. Tạo OCR Job nếu có ảnh
	if p.OCRJob != nil {
		provider := p.OCRJob.Provider
		if provider == "" {
			provider = "llamaextract"
		}
		statusStr := string(p.OCRJob.Status)
		if statusStr == "" {
			statusStr = string(domain.OCRJobStatusQueued)
		}

		var candidateBytes []byte
		if p.OCRJob.Candidate != nil {
			candidateBytes, _ = json.Marshal(p.OCRJob.Candidate)
		}

		_, err := q.CreateOCRJob(ctx, sqlc.CreateOCRJobParams{
			ID:          pgtype.UUID{Bytes: p.OCRJob.ID, Valid: true},
			BillID:      pgtype.UUID{Bytes: p.Bill.ID, Valid: true},
			Status:      statusStr,
			Provider:    provider,
			Attempts:    p.OCRJob.Attempts,
			RawResponse: p.OCRJob.RawResponse,
			Candidate:   candidateBytes,
		})
		if err != nil {
			return nil, fmt.Errorf("create ocr job: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create bill tx: %w", err)
	}

	result := toDomainBill(&dbBill)
	result.Images = createdImages
	result.Items = createdItems
	return result, nil
}

// GetBillByID lấy thông tin chi tiết một hóa đơn (bao gồm cả images, items, assignments, shares).
func (r *postgresRepository) GetBillByID(ctx context.Context, id, groupID uuid.UUID) (*domain.Bill, error) {
	q := sqlc.New(r.pool)

	dbBill, err := q.GetBillByID(ctx, sqlc.GetBillByIDParams{
		ID:      pgtype.UUID{Bytes: id, Valid: true},
		GroupID: pgtype.UUID{Bytes: groupID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBillNotFound
		}
		return nil, fmt.Errorf("get bill by id: %w", err)
	}

	bill := toDomainBill(&dbBill)

	// Lấy danh sách ảnh
	dbImages, err := q.ListBillImages(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err == nil {
		bill.Images = make([]*domain.BillImage, 0, len(dbImages))
		for _, img := range dbImages {
			bill.Images = append(bill.Images, toDomainBillImage(&img))
		}
	}

	// Lấy danh sách món ăn và phân bổ
	dbItems, err := q.ListBillItems(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err == nil {
		dbAssignments, _ := q.ListBillItemAssignmentsByBill(ctx, pgtype.UUID{Bytes: id, Valid: true})
		assignMap := make(map[uuid.UUID][]*domain.BillItemAssignment)
		for _, a := range dbAssignments {
			itemID := uuid.UUID(a.BillItemID.Bytes)
			assignMap[itemID] = append(assignMap[itemID], toDomainBillItemAssignment(&a))
		}

		bill.Items = make([]*domain.BillItem, 0, len(dbItems))
		for _, it := range dbItems {
			domItem := toDomainBillItem(&it)
			domItem.Assignments = assignMap[domItem.ID]
			bill.Items = append(bill.Items, domItem)
		}
	}

	// Lấy danh sách shares nếu hóa đơn đã finalized
	dbShares, err := q.ListBillShares(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err == nil {
		bill.Shares = make([]*domain.BillShare, 0, len(dbShares))
		for _, s := range dbShares {
			bill.Shares = append(bill.Shares, toDomainBillShare(&s))
		}
	}

	return bill, nil
}

// GetBillByIDForUpdate lấy thông tin hóa đơn và khóa dòng với SELECT ... FOR UPDATE.
func (r *postgresRepository) GetBillByIDForUpdate(ctx context.Context, id, groupID uuid.UUID) (*domain.Bill, error) {
	q := sqlc.New(r.pool)

	dbBill, err := q.GetBillByIDForUpdate(ctx, sqlc.GetBillByIDForUpdateParams{
		ID:      pgtype.UUID{Bytes: id, Valid: true},
		GroupID: pgtype.UUID{Bytes: groupID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBillNotFound
		}
		return nil, fmt.Errorf("get bill by id for update: %w", err)
	}

	return toDomainBill(&dbBill), nil
}

// ListBillsByGroup lấy danh sách hóa đơn trong nhóm có phân trang (mới nhất trước).
func (r *postgresRepository) ListBillsByGroup(ctx context.Context, groupID uuid.UUID, limit, offset int32) ([]*domain.Bill, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	q := sqlc.New(r.pool)
	rows, err := q.ListBillsByGroup(ctx, sqlc.ListBillsByGroupParams{
		GroupID: pgtype.UUID{Bytes: groupID, Valid: true},
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list bills by group: %w", err)
	}

	results := make([]*domain.Bill, 0, len(rows))
	for _, row := range rows {
		results = append(results, toDomainBill(&row))
	}
	return results, nil
}

// UpdateDraftBill cập nhật hóa đơn draft và ghi đè danh sách món ăn trong 1 transaction có kiểm tra version.
func (r *postgresRepository) UpdateDraftBill(ctx context.Context, p repository.UpdateDraftParams) (*domain.Bill, error) {
	if p.Bill == nil {
		return nil, domain.ErrInvalidInput
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin update draft bill tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := sqlc.New(tx)

	var billDate pgtype.Date
	if p.Bill.BillDate != nil {
		billDate = pgtype.Date{Time: *p.Bill.BillDate, Valid: true}
	}

	merchantName := pgtype.Text{}
	if p.Bill.MerchantName != nil {
		merchantName = pgtype.Text{String: *p.Bill.MerchantName, Valid: true}
	}

	splitMethod := string(p.Bill.SplitMethod)
	if splitMethod == "" {
		splitMethod = string(domain.SplitMethodEven)
	}

	mismatchCodes := p.Bill.MismatchCodes
	if mismatchCodes == nil {
		mismatchCodes = []string{}
	}

	dbBill, err := q.UpdateDraftBill(ctx, sqlc.UpdateDraftBillParams{
		ID:            pgtype.UUID{Bytes: p.Bill.ID, Valid: true},
		GroupID:       pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true},
		MerchantName:  merchantName,
		BillDate:      billDate,
		Subtotal:      p.Bill.Subtotal,
		ServiceCharge: p.Bill.ServiceCharge,
		Vat:           p.Bill.VAT,
		Discount:      p.Bill.Discount,
		Total:         p.Bill.Total,
		SplitMethod:   splitMethod,
		MismatchCodes: mismatchCodes,
		Version:       p.ExpectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBillConflict
		}
		return nil, fmt.Errorf("update draft bill row: %w", err)
	}

	// Ghi đè danh sách món ăn: Xóa cũ -> Thêm mới
	if err := q.DeleteBillItems(ctx, pgtype.UUID{Bytes: p.Bill.ID, Valid: true}); err != nil {
		return nil, fmt.Errorf("delete old bill items: %w", err)
	}

	createdItems := make([]*domain.BillItem, 0, len(p.Items))
	for _, item := range p.Items {
		var qtyNum pgtype.Numeric
		if err := qtyNum.Scan(item.Quantity); err != nil {
			qtyNum.Scan("1")
		}

		dbItem, err := q.CreateBillItem(ctx, sqlc.CreateBillItemParams{
			ID:        pgtype.UUID{Bytes: item.ID, Valid: true},
			BillID:    pgtype.UUID{Bytes: p.Bill.ID, Valid: true},
			GroupID:   pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true},
			Name:      item.Name,
			Quantity:  qtyNum,
			UnitPrice: item.UnitPrice,
			LineTotal: item.LineTotal,
			Position:  item.Position,
		})
		if err != nil {
			return nil, fmt.Errorf("create replacement bill item %s: %w", item.Name, err)
		}

		domItem := toDomainBillItem(&dbItem)

		for _, assign := range item.Assignments {
			var wNum pgtype.Numeric
			if err := wNum.Scan(assign.Weight); err != nil {
				wNum.Scan("1")
			}

			dbAssign, err := q.CreateBillItemAssignment(ctx, sqlc.CreateBillItemAssignmentParams{
				ID:         pgtype.UUID{Bytes: assign.ID, Valid: true},
				BillItemID: pgtype.UUID{Bytes: item.ID, Valid: true},
				GroupID:    pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true},
				MemberID:   pgtype.UUID{Bytes: assign.MemberID, Valid: true},
				Weight:     wNum,
			})
			if err != nil {
				return nil, fmt.Errorf("create replacement item assignment: %w", err)
			}
			domItem.Assignments = append(domItem.Assignments, toDomainBillItemAssignment(&dbAssign))
		}

		createdItems = append(createdItems, domItem)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit update draft bill tx: %w", err)
	}

	result := toDomainBill(&dbBill)
	result.Items = createdItems
	return result, nil
}

// ReviewBill chuyển trạng thái hóa đơn từ draft sang reviewed (Spec 3 AC-7).
func (r *postgresRepository) ReviewBill(ctx context.Context, id, groupID uuid.UUID, expectedVersion int32) (*domain.Bill, error) {
	q := sqlc.New(r.pool)

	dbBill, err := q.ReviewBill(ctx, sqlc.ReviewBillParams{
		ID:      pgtype.UUID{Bytes: id, Valid: true},
		GroupID: pgtype.UUID{Bytes: groupID, Valid: true},
		Version: expectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBillConflict
		}
		return nil, fmt.Errorf("review bill: %w", err)
	}

	return toDomainBill(&dbBill), nil
}

// FinalizeBill chuyển trạng thái hóa đơn sang finalized, lưu snapshot bill_shares và sinh debts (Spec 3 AC-9).
func (r *postgresRepository) FinalizeBill(ctx context.Context, p repository.FinalizeBillParams) (*domain.Bill, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin finalize bill tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := sqlc.New(tx)

	// 1. Cập nhật status bill thành finalized
	dbBill, err := q.FinalizeBill(ctx, sqlc.FinalizeBillParams{
		ID:      pgtype.UUID{Bytes: p.BillID, Valid: true},
		GroupID: pgtype.UUID{Bytes: p.GroupID, Valid: true},
		Version: p.ExpectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBillConflict
		}
		return nil, fmt.Errorf("finalize bill row: %w", err)
	}

	// 2. Xóa và lưu mới snapshot bill_shares
	if err := q.DeleteBillShares(ctx, pgtype.UUID{Bytes: p.BillID, Valid: true}); err != nil {
		return nil, fmt.Errorf("delete old bill shares: %w", err)
	}

	createdShares := make([]*domain.BillShare, 0, len(p.Shares))
	for _, share := range p.Shares {
		dbShare, err := q.CreateBillShare(ctx, sqlc.CreateBillShareParams{
			ID:             pgtype.UUID{Bytes: share.ID, Valid: true},
			BillID:         pgtype.UUID{Bytes: p.BillID, Valid: true},
			GroupID:        pgtype.UUID{Bytes: p.GroupID, Valid: true},
			MemberID:       pgtype.UUID{Bytes: share.MemberID, Valid: true},
			ComputedAmount: share.ComputedAmount,
		})
		if err != nil {
			return nil, fmt.Errorf("create bill share: %w", err)
		}
		createdShares = append(createdShares, toDomainBillShare(&dbShare))
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit finalize bill tx: %w", err)
	}

	result := toDomainBill(&dbBill)
	result.Shares = createdShares
	return result, nil
}

// VoidBill chuyển trạng thái hóa đơn sang voided (Spec 3 AC-10).
func (r *postgresRepository) VoidBill(ctx context.Context, p repository.VoidBillParams) (*domain.Bill, error) {
	q := sqlc.New(r.pool)

	dbBill, err := q.VoidBill(ctx, sqlc.VoidBillParams{
		ID:      pgtype.UUID{Bytes: p.BillID, Valid: true},
		GroupID: pgtype.UUID{Bytes: p.GroupID, Valid: true},
		Version: p.ExpectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBillConflict
		}
		return nil, fmt.Errorf("void bill: %w", err)
	}

	return toDomainBill(&dbBill), nil
}

// DeleteDraftBill xóa hoàn toàn một hóa đơn nháp và các dữ liệu liên quan.
func (r *postgresRepository) DeleteDraftBill(ctx context.Context, id, groupID uuid.UUID) error {
	q := sqlc.New(r.pool)

	err := q.DeleteDraftBill(ctx, sqlc.DeleteDraftBillParams{
		ID:      pgtype.UUID{Bytes: id, Valid: true},
		GroupID: pgtype.UUID{Bytes: groupID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("delete draft bill: %w", err)
	}
	return nil
}

// ============================================================================
// OCR JOBS
// ============================================================================

func (r *postgresRepository) CreateOCRJob(ctx context.Context, job *domain.OCRJob) (*domain.OCRJob, error) {
	if job == nil {
		return nil, domain.ErrInvalidInput
	}

	q := sqlc.New(r.pool)

	var candidateBytes []byte
	if job.Candidate != nil {
		candidateBytes, _ = json.Marshal(job.Candidate)
	}

	statusStr := string(job.Status)
	if statusStr == "" {
		statusStr = string(domain.OCRJobStatusQueued)
	}

	provider := job.Provider
	if provider == "" {
		provider = "llamaextract"
	}

	dbJob, err := q.CreateOCRJob(ctx, sqlc.CreateOCRJobParams{
		ID:          pgtype.UUID{Bytes: job.ID, Valid: true},
		BillID:      pgtype.UUID{Bytes: job.BillID, Valid: true},
		Status:      statusStr,
		Provider:    provider,
		Attempts:    job.Attempts,
		RawResponse: job.RawResponse,
		Candidate:   candidateBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("create ocr job: %w", err)
	}

	return toDomainOCRJob(&dbJob)
}

func (r *postgresRepository) GetOCRJobByID(ctx context.Context, id uuid.UUID) (*domain.OCRJob, error) {
	q := sqlc.New(r.pool)

	dbJob, err := q.GetOCRJobByID(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrOcrJobNotFound
		}
		return nil, fmt.Errorf("get ocr job by id: %w", err)
	}

	return toDomainOCRJob(&dbJob)
}

func (r *postgresRepository) GetActiveOCRJobByBillID(ctx context.Context, billID uuid.UUID) (*domain.OCRJob, error) {
	q := sqlc.New(r.pool)

	dbJob, err := q.GetActiveOCRJobByBillID(ctx, pgtype.UUID{Bytes: billID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // Không có job nào đang chạy
		}
		return nil, fmt.Errorf("get active ocr job: %w", err)
	}

	return toDomainOCRJob(&dbJob)
}

func (r *postgresRepository) GetLatestOCRJobByBillID(ctx context.Context, billID uuid.UUID) (*domain.OCRJob, error) {
	q := sqlc.New(r.pool)

	dbJob, err := q.GetLatestOCRJobByBillID(ctx, pgtype.UUID{Bytes: billID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrOcrJobNotFound
		}
		return nil, fmt.Errorf("get latest ocr job: %w", err)
	}

	return toDomainOCRJob(&dbJob)
}

func (r *postgresRepository) UpdateOCRJobProcessing(ctx context.Context, id uuid.UUID, version int32) error {
	q := sqlc.New(r.pool)

	_, err := q.UpdateOCRJobProcessing(ctx, sqlc.UpdateOCRJobProcessingParams{
		ID:      pgtype.UUID{Bytes: id, Valid: true},
		Version: version,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrOcrJobConflict
		}
		return fmt.Errorf("update ocr job processing: %w", err)
	}
	return nil
}

func (r *postgresRepository) UpdateOCRJobSuccess(ctx context.Context, id uuid.UUID, version int32, candidate *domain.OCRCandidate, raw []byte) error {
	q := sqlc.New(r.pool)

	var candidateBytes []byte
	if candidate != nil {
		candidateBytes, _ = json.Marshal(candidate)
	}

	_, err := q.UpdateOCRJobSuccess(ctx, sqlc.UpdateOCRJobSuccessParams{
		ID:          pgtype.UUID{Bytes: id, Valid: true},
		Version:     version,
		Candidate:   candidateBytes,
		RawResponse: raw,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrOcrJobConflict
		}
		return fmt.Errorf("update ocr job success: %w", err)
	}
	return nil
}

func (r *postgresRepository) UpdateOCRJobFailed(ctx context.Context, id uuid.UUID, version int32, errReason string) error {
	q := sqlc.New(r.pool)

	_, err := q.UpdateOCRJobFailed(ctx, sqlc.UpdateOCRJobFailedParams{
		ID:           pgtype.UUID{Bytes: id, Valid: true},
		Version:      version,
		ErrorMessage: pgtype.Text{String: errReason, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrOcrJobConflict
		}
		return fmt.Errorf("update ocr job failed: %w", err)
	}
	return nil
}

func (r *postgresRepository) CountManualOCRAttemptsInWindow(ctx context.Context, billID uuid.UUID, since time.Time) (int64, error) {
	q := sqlc.New(r.pool)

	count, err := q.CountManualOCRAttemptsInWindow(ctx, sqlc.CountManualOCRAttemptsInWindowParams{
		BillID:    pgtype.UUID{Bytes: billID, Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: since, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("count manual ocr attempts: %w", err)
	}
	return count, nil
}

func (r *postgresRepository) EnqueueMediaCleanup(ctx context.Context, prefix, kind string) error {
	jobID := uuid.New()
	query := `INSERT INTO media_cleanup_jobs (id, image_object_key, kind, status, created_at, updated_at)
			  VALUES ($1, $2, $3, 'pending', now(), now())
			  ON CONFLICT (image_object_key) DO NOTHING`
	_, err := r.pool.Exec(ctx, query, jobID, prefix, kind)
	if err != nil {
		return fmt.Errorf("enqueue media cleanup job: %w", err)
	}
	return nil
}

// ============================================================================
// MAPPING HELPERS
// ============================================================================

func toDomainBill(b *sqlc.Bill) *domain.Bill {
	if b == nil {
		return nil
	}

	var billDate *time.Time
	if b.BillDate.Valid {
		t := b.BillDate.Time
		billDate = &t
	}

	var replacesID *uuid.UUID
	if b.ReplacesBillID.Valid {
		id := uuid.UUID(b.ReplacesBillID.Bytes)
		replacesID = &id
	}

	var merchantName *string
	if b.MerchantName.Valid {
		merchantName = &b.MerchantName.String
	}

	var finalizedAt *time.Time
	if b.FinalizedAt.Valid {
		t := b.FinalizedAt.Time
		finalizedAt = &t
	}

	var voidedAt *time.Time
	if b.VoidedAt.Valid {
		t := b.VoidedAt.Time
		voidedAt = &t
	}

	statusStr := fmt.Sprintf("%v", b.Status)

	return &domain.Bill{
		ID:               uuid.UUID(b.ID.Bytes),
		GroupID:          uuid.UUID(b.GroupID.Bytes),
		CreditorMemberID: uuid.UUID(b.CreditorMemberID.Bytes),
		Status:           domain.BillStatus(statusStr),
		MerchantName:     merchantName,
		BillDate:         billDate,
		Subtotal:         b.Subtotal,
		ServiceCharge:    b.ServiceCharge,
		VAT:              b.Vat,
		Discount:         b.Discount,
		Total:            b.Total,
		SplitMethod:      domain.SplitMethod(b.SplitMethod),
		MismatchCodes:    b.MismatchCodes,
		ReplacesBillID:   replacesID,
		Version:          b.Version,
		FinalizedAt:      finalizedAt,
		VoidedAt:         voidedAt,
		CreatedAt:        b.CreatedAt.Time,
		UpdatedAt:        b.UpdatedAt.Time,
	}
}

func toDomainBillImage(img *sqlc.BillImage) *domain.BillImage {
	if img == nil {
		return nil
	}
	return &domain.BillImage{
		ID:        uuid.UUID(img.ID.Bytes),
		BillID:    uuid.UUID(img.BillID.Bytes),
		GroupID:   uuid.UUID(img.GroupID.Bytes),
		ImageKey:  img.ImageKey,
		Position:  img.Position,
		CreatedAt: img.CreatedAt.Time,
	}
}

func toDomainBillItem(it *sqlc.BillItem) *domain.BillItem {
	if it == nil {
		return nil
	}
	qtyStr := "1"
	if it.Quantity.Valid {
		if val, err := it.Quantity.Value(); err == nil && val != nil {
			qtyStr = fmt.Sprintf("%v", val)
		}
	}

	return &domain.BillItem{
		ID:        uuid.UUID(it.ID.Bytes),
		BillID:    uuid.UUID(it.BillID.Bytes),
		GroupID:   uuid.UUID(it.GroupID.Bytes),
		Name:      it.Name,
		Quantity:  qtyStr,
		UnitPrice: it.UnitPrice,
		LineTotal: it.LineTotal,
		Position:  it.Position,
		CreatedAt: it.CreatedAt.Time,
		UpdatedAt: it.UpdatedAt.Time,
	}
}

func toDomainBillItemAssignment(a *sqlc.BillItemAssignment) *domain.BillItemAssignment {
	if a == nil {
		return nil
	}
	weightStr := "1.0000"
	if a.Weight.Valid {
		if val, err := a.Weight.Value(); err == nil && val != nil {
			weightStr = fmt.Sprintf("%v", val)
		}
	}
	return &domain.BillItemAssignment{
		ID:         uuid.UUID(a.ID.Bytes),
		BillItemID: uuid.UUID(a.BillItemID.Bytes),
		GroupID:    uuid.UUID(a.GroupID.Bytes),
		MemberID:   uuid.UUID(a.MemberID.Bytes),
		Weight:     weightStr,
		CreatedAt:  a.CreatedAt.Time,
	}
}

func toDomainBillShare(s *sqlc.BillShare) *domain.BillShare {
	if s == nil {
		return nil
	}
	return &domain.BillShare{
		ID:             uuid.UUID(s.ID.Bytes),
		BillID:         uuid.UUID(s.BillID.Bytes),
		GroupID:        uuid.UUID(s.GroupID.Bytes),
		MemberID:       uuid.UUID(s.MemberID.Bytes),
		ComputedAmount: s.ComputedAmount,
		CreatedAt:      s.CreatedAt.Time,
	}
}

func toDomainOCRJob(j *sqlc.OcrJob) (*domain.OCRJob, error) {
	if j == nil {
		return nil, nil
	}

	var candidate *domain.OCRCandidate
	if len(j.Candidate) > 0 {
		var cand domain.OCRCandidate
		if err := json.Unmarshal(j.Candidate, &cand); err == nil {
			candidate = &cand
		}
	}

	var completedAt *time.Time
	if j.CompletedAt.Valid {
		t := j.CompletedAt.Time
		completedAt = &t
	}

	var errMsg *string
	if j.ErrorMessage.Valid {
		errMsg = &j.ErrorMessage.String
	}

	statusStr := fmt.Sprintf("%v", j.Status)

	return &domain.OCRJob{
		ID:           uuid.UUID(j.ID.Bytes),
		BillID:       uuid.UUID(j.BillID.Bytes),
		Status:       domain.OCRJobStatus(statusStr),
		Provider:     j.Provider,
		Attempts:     j.Attempts,
		RawResponse:  j.RawResponse,
		Candidate:    candidate,
		ErrorMessage: errMsg,
		Version:      j.Version,
		CreatedAt:    j.CreatedAt.Time,
		UpdatedAt:    j.UpdatedAt.Time,
		CompletedAt:  completedAt,
	}, nil
}
