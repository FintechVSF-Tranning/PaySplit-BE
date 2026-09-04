package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	"paysplit-backend/internal/modules/bill/repository/postgres/sqlc"
	"paysplit-backend/internal/platform/database"
	"paysplit-backend/internal/platform/realtime"
)

type postgresRepository struct {
	pool   *pgxpool.Pool
	events *realtime.Publisher
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

	if err = lockActiveBillGroup(ctx, tx, p.Bill.GroupID); err != nil {
		return nil, err
	}
	q := sqlc.New(tx)

	// Spec 0008 AC-2: kiểm tra lại khóa gửi hóa đơn sau khi giữ khóa nhóm. Đây là
	// nguồn sự thật cuối cùng: nếu nhóm đã bị khóa thì không bill, OCR job hay
	// activity nào được commit; các ảnh attempt sẽ do lớp usecase đưa vào hàng
	// đợi dọn dẹp bền vững.
	groupLockedAt, err := q.GetGroupSubmissionLock(ctx, pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("read submission lock in create tx: %w", err)
	}
	if groupLockedAt.Valid {
		return nil, domain.ErrSubmissionLocked
	}

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
		ID:                pgtype.UUID{Bytes: p.Bill.ID, Valid: true},
		GroupID:           pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true},
		CreditorMemberID:  pgtype.UUID{Bytes: p.Bill.CreditorMemberID, Valid: true},
		Status:            statusStr,
		MerchantName:      merchantName,
		BillDate:          billDate,
		Subtotal:          p.Bill.Subtotal,
		ServiceCharge:     p.Bill.ServiceCharge,
		Vat:               p.Bill.VAT,
		Discount:          p.Bill.Discount,
		TotalItemDiscount: p.Bill.TotalItemDiscount,
		GeneralDiscount:   p.Bill.GeneralDiscount,
		Total:             p.Bill.Total,
		SplitMethod:       splitMethod,
		MismatchCodes:     mismatchCodes,
		ReplacesBillID:    replacesID,
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
			ID:             pgtype.UUID{Bytes: item.ID, Valid: true},
			BillID:         pgtype.UUID{Bytes: p.Bill.ID, Valid: true},
			GroupID:        pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true},
			Name:           item.Name,
			Quantity:       qtyNum,
			UnitPrice:      item.UnitPrice,
			LineTotal:      item.LineTotal,
			DiscountAmount: item.DiscountAmount,
			FinalPrice:     item.FinalPrice,
			Position:       item.Position,
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

		var jobVersion int32 = 1
		if p.OCRJob.Version > 0 {
			jobVersion = p.OCRJob.Version
		}

		dbJob, err := q.CreateOCRJob(ctx, sqlc.CreateOCRJobParams{
			ID:          pgtype.UUID{Bytes: p.OCRJob.ID, Valid: true},
			BillID:      pgtype.UUID{Bytes: p.Bill.ID, Valid: true},
			Status:      statusStr,
			Provider:    provider,
			Attempts:    p.OCRJob.Attempts,
			RawResponse: p.OCRJob.RawResponse,
			Candidate:   candidateBytes,
			Version:     jobVersion,
		})
		if err != nil {
			return nil, fmt.Errorf("create ocr job: %w", err)
		}
		ocrJob, err := toDomainOCRJob(&dbJob)
		if err != nil {
			return nil, err
		}
		if err = r.notifyOCR(ctx, tx, p.Bill.GroupID, ocrJob); err != nil {
			return nil, err
		}
	}

	if p.BeforeCommit != nil {
		if err := p.BeforeCommit(ctx, tx); err != nil {
			return nil, fmt.Errorf("before commit hook: %w", err)
		}
	}

	// Ghi nhận hoạt động tạo hóa đơn vào group_activities (Spec 3 AC-1)
	var billName string
	if p.Bill.MerchantName != nil && *p.Bill.MerchantName != "" {
		billName = *p.Bill.MerchantName
	} else {
		billName = "Hóa đơn mới"
	}
	metaBytes, _ := json.Marshal(map[string]any{
		"bill_id":     p.Bill.ID.String(),
		"manual":      len(p.Images) == 0,
		"image_count": len(p.Images),
	})
	desc := fmt.Sprintf("Đã tạo hóa đơn nháp %q", billName)
	_, _ = q.InsertGroupActivity(ctx, sqlc.InsertGroupActivityParams{
		ID:            pgtype.UUID{Bytes: uuid.Must(uuid.NewV7()), Valid: true},
		GroupID:       pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true},
		ActorMemberID: pgtype.UUID{Bytes: p.Bill.CreditorMemberID, Valid: true},
		ActionType:    "created_bill",
		Description:   desc,
		Metadata:      metaBytes,
	})

	if err := r.notifyBillInvalidate(ctx, tx, p.Bill.GroupID, p.Bill.ID, dbBill.Version, "bill.created"); err != nil {
		return nil, err
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
	return loadBillByID(ctx, sqlc.New(r.pool), id, groupID)
}

// loadBillByID đọc đầy đủ bill kèm ảnh, món, phân bổ và shares bằng một Querier
// đã gắn với pool hoặc transaction bất kỳ.
func loadBillByID(ctx context.Context, q *sqlc.Queries, id, groupID uuid.UUID) (*domain.Bill, error) {
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

// GetBillOnlyByID lấy thông tin cơ bản của hóa đơn chỉ bằng billID (dùng cho auth resolution).
func (r *postgresRepository) GetBillOnlyByID(ctx context.Context, id uuid.UUID) (*domain.Bill, error) {
	q := sqlc.New(r.pool)
	row, err := q.GetBillOnlyByID(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBillNotFound
		}
		return nil, fmt.Errorf("get bill only by id: %w", err)
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
func (r *postgresRepository) ListBillsByGroup(ctx context.Context, groupID uuid.UUID, limit, offset int32) ([]*domain.BillListItem, error) {
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

	results := make([]*domain.BillListItem, 0, len(rows))
	for _, row := range rows {
		results = append(results, &domain.BillListItem{
			Bill:             toDomainBill(&row.Bill),
			PayerDisplayName: row.PayerDisplayName,
			PaidMemberCount:  row.PaidMemberCount,
			MemberCount:      row.MemberCount,
		})
	}
	return results, nil
}

// ListBillsByGroupCursor lấy danh sách hóa đơn trong nhóm theo cursor pagination (created_at DESC, id DESC).
func (r *postgresRepository) ListBillsByGroupCursor(ctx context.Context, p repository.ListBillsCursorParams) (*repository.ListBillsCursorResult, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	var cursorCreatedAt pgtype.Timestamptz
	var cursorID pgtype.UUID
	if p.Cursor != nil && *p.Cursor != "" {
		createdAt, id, err := decodeCursor(*p.Cursor)
		if err != nil {
			return nil, domain.ErrInvalidCursor
		}
		cursorCreatedAt = pgtype.Timestamptz{Time: createdAt, Valid: true}
		cursorID = pgtype.UUID{Bytes: id, Valid: true}
	}

	q := sqlc.New(r.pool)
	statuses := p.Statuses
	if statuses == nil {
		statuses = []string{}
	}

	rows, err := q.ListBillsByGroupCursor(ctx, sqlc.ListBillsByGroupCursorParams{
		GroupID:  pgtype.UUID{Bytes: p.GroupID, Valid: true},
		Column2:  cursorCreatedAt,
		ID:       cursorID,
		Limit:    limit + 1,
		Column5:  statuses,
		MemberID: pgtype.UUID{Bytes: p.CallerMemberID, Valid: p.CallerMemberID != uuid.Nil},
	})
	if err != nil {
		return nil, fmt.Errorf("list bills cursor: %w", err)
	}

	hasMore := len(rows) > int(limit)
	if hasMore {
		rows = rows[:limit]
	}

	bills := make([]*domain.BillListItem, 0, len(rows))
	for _, row := range rows {
		item := &domain.BillListItem{
			Bill:             toDomainBill(&row.Bill),
			PayerDisplayName: row.PayerDisplayName,
			PaidMemberCount:  row.PaidMemberCount,
			MemberCount:      row.MemberCount,
			MyShareStatus:    domain.MyShareStatusNone,
			OCRStatus:        domain.OCRJobStatus(row.OcrStatus),
		}
		if row.MyShare.Valid {
			amount := row.MyShare.Int64
			item.MyShare = &amount
		}
		item.MyShareStatus = myShareStatus(item, p.CallerMemberID, row.MyDebtStatus)
		bills = append(bills, item)
	}

	var nextCursor *string
	if hasMore && len(bills) > 0 {
		last := bills[len(bills)-1].Bill
		c := encodeCursor(last.CreatedAt, last.ID.String())
		nextCursor = &c
	}

	return &repository.ListBillsCursorResult{
		Bills:      bills,
		NextCursor: nextCursor,
	}, nil
}

// myShareStatus diễn giải phần tiền của người đang xem. Người ứng tiền không
// bao giờ tự nợ mình, còn khoản nợ đã 'voided' (thành viên rời nhóm, số tiền
// được xóa) tính như đã xong — cùng quy ước với paid_member_count.
func myShareStatus(item *domain.BillListItem, callerMemberID uuid.UUID, debtStatus string) domain.MyShareStatus {
	if callerMemberID != uuid.Nil && item.CreditorMemberID == callerMemberID {
		return domain.MyShareStatusCreditor
	}
	if item.MyShare == nil {
		return domain.MyShareStatusNone
	}
	switch debtStatus {
	case "settled", "voided":
		return domain.MyShareStatusSettled
	case "":
		// Có share nhưng chưa sinh nợ (phần bằng 0) thì coi như không phải trả.
		if *item.MyShare == 0 {
			return domain.MyShareStatusSettled
		}
		return domain.MyShareStatusPending
	default:
		return domain.MyShareStatusPending
	}
}

// CountBillsByGroupStatus đếm hóa đơn của nhóm theo từng trạng thái. Trạng thái
// không có hóa đơn nào sẽ vắng mặt trong map — caller tự coi là 0.
func (r *postgresRepository) CountBillsByGroupStatus(ctx context.Context, groupID uuid.UUID) (map[string]int64, error) {
	rows, err := sqlc.New(r.pool).CountBillsByGroupStatus(ctx, pgtype.UUID{Bytes: groupID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("count bills by status: %w", err)
	}

	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.Status] = row.Count
	}
	return counts, nil
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

	if err = lockActiveBillGroup(ctx, tx, p.Bill.GroupID); err != nil {
		return nil, err
	}
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
		ID:                pgtype.UUID{Bytes: p.Bill.ID, Valid: true},
		GroupID:           pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true},
		MerchantName:      merchantName,
		BillDate:          billDate,
		Subtotal:          p.Bill.Subtotal,
		ServiceCharge:     p.Bill.ServiceCharge,
		Vat:               p.Bill.VAT,
		Discount:          p.Bill.Discount,
		TotalItemDiscount: p.Bill.TotalItemDiscount,
		GeneralDiscount:   p.Bill.GeneralDiscount,
		Total:             p.Bill.Total,
		SplitMethod:       splitMethod,
		MismatchCodes:     mismatchCodes,
		Version:           p.ExpectedVersion,
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
			ID:             pgtype.UUID{Bytes: item.ID, Valid: true},
			BillID:         pgtype.UUID{Bytes: p.Bill.ID, Valid: true},
			GroupID:        pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true},
			Name:           item.Name,
			Quantity:       qtyNum,
			UnitPrice:      item.UnitPrice,
			LineTotal:      item.LineTotal,
			DiscountAmount: item.DiscountAmount,
			FinalPrice:     item.FinalPrice,
			Position:       item.Position,
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

	// Ghi nhận hoạt động sửa hóa đơn vào group_activities
	actorID := p.ActorMemberID
	if actorID == uuid.Nil {
		actorID = p.Bill.CreditorMemberID
	}
	metaBytes, _ := json.Marshal(map[string]any{
		"bill_id":        p.Bill.ID.String(),
		"version":        dbBill.Version,
		"mismatch_codes": dbBill.MismatchCodes,
	})
	var billName string
	if dbBill.MerchantName.Valid && dbBill.MerchantName.String != "" {
		billName = dbBill.MerchantName.String
	} else if p.Bill.MerchantName != nil && *p.Bill.MerchantName != "" {
		billName = *p.Bill.MerchantName
	} else {
		billName = "Hóa đơn"
	}
	desc := fmt.Sprintf("Đã cập nhật hóa đơn %q (phiên bản %d)", billName, dbBill.Version)
	_, _ = q.InsertGroupActivity(ctx, sqlc.InsertGroupActivityParams{
		ID:            pgtype.UUID{Bytes: uuid.Must(uuid.NewV7()), Valid: true},
		GroupID:       pgtype.UUID{Bytes: p.Bill.GroupID, Valid: true},
		ActorMemberID: pgtype.UUID{Bytes: actorID, Valid: true},
		ActionType:    "updated_bill",
		Description:   desc,
		Metadata:      metaBytes,
	})

	if err := r.notifyBillInvalidate(ctx, tx, p.Bill.GroupID, p.Bill.ID, dbBill.Version, "bill.content_changed"); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit update draft bill tx: %w", err)
	}

	result := toDomainBill(&dbBill)
	result.Items = createdItems
	return result, nil
}

// ReviewBill chuyển trạng thái hóa đơn từ draft sang reviewed (Spec 3 AC-7).
func (r *postgresRepository) ReviewBill(ctx context.Context, id, groupID uuid.UUID, expectedVersion int32, reviewerMemberID uuid.UUID) (*domain.Bill, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin review bill tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err = lockActiveBillGroup(ctx, tx, groupID); err != nil {
		return nil, err
	}
	q := sqlc.New(tx)

	dbBill, err := q.ReviewBill(ctx, sqlc.ReviewBillParams{
		ID:                 pgtype.UUID{Bytes: id, Valid: true},
		GroupID:            pgtype.UUID{Bytes: groupID, Valid: true},
		Version:            expectedVersion,
		ReviewedByMemberID: pgtype.UUID{Bytes: reviewerMemberID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBillConflict
		}
		return nil, fmt.Errorf("review bill: %w", err)
	}

	// Ghi nhận hoạt động review hóa đơn vào group_activities
	metaBytes, _ := json.Marshal(map[string]any{
		"bill_id": id.String(),
		"version": dbBill.Version,
	})
	var billName string
	if dbBill.MerchantName.Valid && dbBill.MerchantName.String != "" {
		billName = dbBill.MerchantName.String
	} else {
		billName = "Hóa đơn"
	}
	desc := fmt.Sprintf("Đã duyệt hóa đơn %q (phiên bản %d)", billName, dbBill.Version)
	_, _ = q.InsertGroupActivity(ctx, sqlc.InsertGroupActivityParams{
		ID:            pgtype.UUID{Bytes: uuid.Must(uuid.NewV7()), Valid: true},
		GroupID:       pgtype.UUID{Bytes: groupID, Valid: true},
		ActorMemberID: pgtype.UUID{Bytes: reviewerMemberID, Valid: true},
		ActionType:    "reviewed_bill",
		Description:   desc,
		Metadata:      metaBytes,
	})

	if err := r.notifyBillInvalidate(ctx, tx, groupID, id, dbBill.Version, "bill.reviewed"); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit review bill tx: %w", err)
	}

	return toDomainBill(&dbBill), nil
}

// GetGroupMember lấy thông tin thành viên trong nhóm theo user ID.
func (r *postgresRepository) GetGroupMember(ctx context.Context, groupID, userID uuid.UUID) (*repository.GroupMember, error) {
	q := sqlc.New(r.pool)
	m, err := q.GetGroupMember(ctx, sqlc.GetGroupMemberParams{
		GroupID: pgtype.UUID{Bytes: groupID, Valid: true},
		UserID:  pgtype.UUID{Bytes: userID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrInvalidInput
		}
		return nil, fmt.Errorf("get group member: %w", err)
	}

	roleStr := fmt.Sprintf("%v", m.Role)
	statusStr := fmt.Sprintf("%v", m.Status)

	return &repository.GroupMember{
		ID:      uuid.UUID(m.ID.Bytes),
		GroupID: uuid.UUID(m.GroupID.Bytes),
		UserID:  uuid.UUID(m.UserID.Bytes),
		Role:    roleStr,
		Status:  statusStr,
	}, nil
}

// GetGroupMemberUser lấy thông tin thành viên kèm thông tin user/tài khoản ngân hàng.
func (r *postgresRepository) GetGroupMemberUser(ctx context.Context, memberID, groupID uuid.UUID) (*repository.GroupMemberWithUser, error) {
	q := sqlc.New(r.pool)
	m, err := q.GetGroupMemberUser(ctx, sqlc.GetGroupMemberUserParams{
		ID:      pgtype.UUID{Bytes: memberID, Valid: true},
		GroupID: pgtype.UUID{Bytes: groupID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrInvalidInput
		}
		return nil, fmt.Errorf("get group member user: %w", err)
	}

	roleStr := fmt.Sprintf("%v", m.Role)
	statusStr := fmt.Sprintf("%v", m.Status)

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
		Role:                  roleStr,
		Status:                statusStr,
		DefaultBankCode:       bankCode,
		DefaultBankAccountNum: bankAcc,
		DefaultBankHolder:     bankHolder,
	}, nil
}

// ListActiveGroupMembers lấy danh sách các thành viên active trong nhóm.
func (r *postgresRepository) ListActiveGroupMembers(ctx context.Context, groupID uuid.UUID) ([]*repository.GroupMember, error) {
	q := sqlc.New(r.pool)
	members, err := q.ListActiveGroupMembers(ctx, pgtype.UUID{Bytes: groupID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list active group members: %w", err)
	}

	result := make([]*repository.GroupMember, 0, len(members))
	for _, m := range members {
		roleStr := fmt.Sprintf("%v", m.Role)
		statusStr := fmt.Sprintf("%v", m.Status)
		result = append(result, &repository.GroupMember{
			ID:      uuid.UUID(m.ID.Bytes),
			GroupID: uuid.UUID(m.GroupID.Bytes),
			UserID:  uuid.UUID(m.UserID.Bytes),
			Role:    roleStr,
			Status:  statusStr,
		})
	}
	return result, nil
}

// FinalizeBill chuyển trạng thái hóa đơn sang finalized, lưu snapshot bill_shares và sinh debts (Spec 3 AC-9).
func (r *postgresRepository) FinalizeBill(ctx context.Context, p repository.FinalizeBillParams) (*domain.Bill, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin finalize bill tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err = lockActiveBillGroup(ctx, tx, p.GroupID); err != nil {
		return nil, err
	}

	result, err := r.finalizeCore(ctx, tx, p)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit finalize bill tx: %w", err)
	}
	return result, nil
}

// FinalizeBillInTx chạy phần ghi của finalize bên trong một transaction do caller
// sở hữu (batch item worker). Caller phải đã khóa dòng nhóm và dòng bill trước khi
// gọi; hàm này không mở hay commit transaction (Spec 0008 Batch item transaction).
func (r *postgresRepository) FinalizeBillInTx(ctx context.Context, tx pgx.Tx, p repository.FinalizeBillParams) (*domain.Bill, error) {
	return r.finalizeCore(ctx, tx, p)
}

// finalizeCore là phần ghi dùng chung của hai đường finalize: cập nhật bill sang
// finalized, ghi đè snapshot shares, sinh debts, tạo notifications, chạy hook
// beforeCommit và ghi activity finalized_bill (Spec 3 AC-9).
func (r *postgresRepository) finalizeCore(ctx context.Context, tx pgx.Tx, p repository.FinalizeBillParams) (*domain.Bill, error) {
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
			ID:                 pgtype.UUID{Bytes: share.ID, Valid: true},
			BillID:             pgtype.UUID{Bytes: p.BillID, Valid: true},
			GroupID:            pgtype.UUID{Bytes: p.GroupID, Valid: true},
			MemberID:           pgtype.UUID{Bytes: share.MemberID, Valid: true},
			ItemSubtotal:       share.ItemSubtotal,
			ServiceChargeShare: share.ServiceChargeShare,
			VatShare:           share.VATShare,
			DiscountShare:      share.DiscountShare,
			RoundingAdjustment: share.RoundingAdjustment,
			FinalAmount:        share.FinalAmount,
		})
		if err != nil {
			return nil, fmt.Errorf("create bill share: %w", err)
		}
		createdShares = append(createdShares, toDomainBillShare(&dbShare))
	}

	// 3. Tạo các bản ghi debts cho từng thành viên nợ (Spec 3 AC-9)
	for _, debt := range p.Debts {
		if debt.Amount <= 0 {
			continue
		}
		_, err := q.CreateDebt(ctx, sqlc.CreateDebtParams{
			ID:               pgtype.UUID{Bytes: debt.ID, Valid: true},
			GroupID:          pgtype.UUID{Bytes: debt.GroupID, Valid: true},
			BillID:           pgtype.UUID{Bytes: debt.BillID, Valid: true},
			DebtorMemberID:   pgtype.UUID{Bytes: debt.DebtorMemberID, Valid: true},
			CreditorMemberID: pgtype.UUID{Bytes: debt.CreditorMemberID, Valid: true},
			Amount:           debt.Amount,
		})
		if err != nil {
			return nil, fmt.Errorf("create debt for member %s: %w", debt.DebtorMemberID, err)
		}
	}

	// 4. Tạo các bản ghi notifications cho các thành viên tham gia (Spec 3 AC-9)
	for _, notif := range p.Notifications {
		_, err := q.CreateNotification(ctx, sqlc.CreateNotificationParams{
			ID:      pgtype.UUID{Bytes: notif.ID, Valid: true},
			UserID:  pgtype.UUID{Bytes: notif.UserID, Valid: true},
			Type:    notif.Type,
			Title:   notif.Title,
			Body:    notif.Body,
			Payload: notif.Payload,
		})
		if err != nil {
			return nil, fmt.Errorf("create notification for user %s: %w", notif.UserID, err)
		}
	}

	// 5. Gọi hook trước khi commit (ví dụ enqueue notification River jobs trong cùng tx)
	if p.BeforeCommit != nil {
		if err := p.BeforeCommit(ctx, tx); err != nil {
			return nil, fmt.Errorf("before commit finalize hook: %w", err)
		}
	}

	// 6. Ghi nhận hoạt động chốt hóa đơn vào group_activities (Spec 3 AC-9)
	metaBytes, _ := json.Marshal(map[string]any{
		"bill_id":           p.BillID.String(),
		"total":             dbBill.Total,
		"participant_count": len(p.Shares),
		"debt_count":        len(p.Debts),
	})
	var billName string
	if dbBill.MerchantName.Valid && dbBill.MerchantName.String != "" {
		billName = dbBill.MerchantName.String
	} else {
		billName = "Hóa đơn"
	}
	desc := fmt.Sprintf("Đã chốt hóa đơn %q (tổng %s đ)", billName, formatVND(dbBill.Total))
	_, _ = q.InsertGroupActivity(ctx, sqlc.InsertGroupActivityParams{
		ID:            pgtype.UUID{Bytes: uuid.Must(uuid.NewV7()), Valid: true},
		GroupID:       pgtype.UUID{Bytes: p.GroupID, Valid: true},
		ActorMemberID: pgtype.UUID{Bytes: p.ActorMemberID, Valid: true},
		ActionType:    "finalized_bill",
		Description:   desc,
		Metadata:      metaBytes,
	})

	result := toDomainBill(&dbBill)
	result.Shares = createdShares
	if err := r.notifyBillInvalidate(ctx, tx, p.GroupID, p.BillID, dbBill.Version, "bill.finalized"); err != nil {
		return nil, err
	}
	return result, nil
}

// VoidBill chuyển trạng thái hóa đơn sang voided và hủy debts (Spec 3 AC-11).
func (r *postgresRepository) VoidBill(ctx context.Context, p repository.VoidBillParams) (*domain.Bill, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin void bill tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := sqlc.New(tx)
	// Settlement v1 serializes every debt or payment mutation through the group
	// row. This keeps bill void and proof submission on the same lock order.
	if err = lockActiveBillGroup(ctx, tx, p.GroupID); err != nil {
		return nil, err
	}

	// 1. Khóa bản ghi bills trước (bills FOR UPDATE) tuân thủ lock ordering hierarchy
	// để ngăn ngừa deadlock với luồng finalize và module thanh toán (Spec 3 index.md:88, 0003:50, Invariant 12).
	billRow, err := q.GetBillByIDForUpdate(ctx, sqlc.GetBillByIDForUpdateParams{
		ID:      pgtype.UUID{Bytes: p.BillID, Valid: true},
		GroupID: pgtype.UUID{Bytes: p.GroupID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBillNotFound
		}
		return nil, fmt.Errorf("lock bill for void: %w", err)
	}
	if billRow.Status == "voided" {
		return nil, domain.ErrBillAlreadyVoided
	}
	if billRow.Status != "finalized" {
		return nil, domain.ErrBillNotFinalized
	}
	if billRow.Version != p.ExpectedVersion {
		return nil, domain.ErrVersionConflict
	}

	// 2. Sau khi đã giữ lock bill, tiến hành khóa toàn bộ các dòng nợ debts của bill theo thứ tự UUID tăng dần (FOR UPDATE)
	// để ngăn chặn race condition với luồng bắt đầu thanh toán ở Module 4 (Spec 3 AC-11, Invariant 12).
	debts, err := q.ListDebtsByBillIDForUpdate(ctx, pgtype.UUID{Bytes: p.BillID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("lock debts for void: %w", err)
	}

	for _, d := range debts {
		if d.Status != "awaiting" || d.PaymentID.Valid {
			return nil, domain.ErrPaymentAlreadyStarted
		}
	}

	// A pending proof payment is only a QR intent and does not reserve a debt.
	// Preserve its immutable payment_debts audit links, but make its QR inactive
	// before the linked awaiting debts are voided.
	debtIDs := make([]uuid.UUID, 0, len(debts))
	for _, debt := range debts {
		debtIDs = append(debtIDs, uuid.UUID(debt.ID.Bytes))
	}
	if len(debtIDs) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE payments p SET status='superseded',updated_at=now() FROM (SELECT DISTINCT payment_id FROM payment_debts WHERE debt_id=ANY($1::uuid[])) linked WHERE p.id=linked.payment_id AND p.status='pending_proof'`, debtIDs); err != nil {
			return nil, fmt.Errorf("supersede pending payments for void: %w", err)
		}
	}

	// 3. Chuyển trạng thái bill sang voided
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

	// 4. Hủy bỏ toàn bộ các khoản nợ awaiting liên quan đến bill này
	if err := q.VoidDebtsByBillID(ctx, pgtype.UUID{Bytes: p.BillID, Valid: true}); err != nil {
		return nil, fmt.Errorf("void debts: %w", err)
	}

	// 5. Ghi nhận hoạt động hủy hóa đơn vào group_activities (Spec 3 AC-11)
	metaBytes, _ := json.Marshal(map[string]any{
		"bill_id": p.BillID.String(),
		"reason":  p.Reason,
	})
	var billName string
	if dbBill.MerchantName.Valid && dbBill.MerchantName.String != "" {
		billName = dbBill.MerchantName.String
	} else {
		billName = "Hóa đơn"
	}
	desc := fmt.Sprintf("Đã hủy hóa đơn %q: %s", billName, p.Reason)
	_, _ = q.InsertGroupActivity(ctx, sqlc.InsertGroupActivityParams{
		ID:            pgtype.UUID{Bytes: uuid.Must(uuid.NewV7()), Valid: true},
		GroupID:       pgtype.UUID{Bytes: p.GroupID, Valid: true},
		ActorMemberID: pgtype.UUID{Bytes: p.ActorMemberID, Valid: true},
		ActionType:    "voided_bill",
		Description:   desc,
		Metadata:      metaBytes,
	})

	if err := r.notifyBillInvalidate(ctx, tx, p.GroupID, p.BillID, dbBill.Version, "bill.voided"); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit void bill tx: %w", err)
	}

	return toDomainBill(&dbBill), nil
}

// DeleteDraftBill xóa hoàn toàn một hóa đơn nháp và các dữ liệu liên quan trong 1 transaction nguyên tử.
func (r *postgresRepository) DeleteDraftBill(ctx context.Context, p repository.DeleteDraftBillParams) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete draft bill tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err = lockActiveBillGroup(ctx, tx, p.GroupID); err != nil {
		return err
	}
	q := sqlc.New(tx)

	// Lấy thông tin bill trước khi xóa để kiểm tra
	billRow, err := q.GetBillByIDForUpdate(ctx, sqlc.GetBillByIDForUpdateParams{
		ID:      pgtype.UUID{Bytes: p.ID, Valid: true},
		GroupID: pgtype.UUID{Bytes: p.GroupID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrBillNotFound
		}
		return fmt.Errorf("get bill for delete: %w", err)
	}

	// Xếp hàng các ảnh cần dọn dẹp vào media_cleanup_jobs ngay trong transaction xóa
	for _, key := range p.ImageKeys {
		_ = q.InsertMediaCleanupJob(ctx, sqlc.InsertMediaCleanupJobParams{
			ID:        pgtype.UUID{Bytes: uuid.Must(uuid.NewV7()), Valid: true},
			ObjectKey: key,
		})
	}

	rowsAffected, err := q.DeleteDraftBill(ctx, sqlc.DeleteDraftBillParams{
		ID:      pgtype.UUID{Bytes: p.ID, Valid: true},
		GroupID: pgtype.UUID{Bytes: p.GroupID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("delete draft bill: %w", err)
	}
	if rowsAffected == 0 {
		return domain.ErrBillNotFound
	}

	actorID := p.ActorMemberID
	if actorID == uuid.Nil {
		actorID = uuid.UUID(billRow.CreditorMemberID.Bytes)
	}

	metaBytes, _ := json.Marshal(map[string]any{
		"bill_id": p.ID.String(),
	})
	var billName string
	if billRow.MerchantName.Valid && billRow.MerchantName.String != "" {
		billName = billRow.MerchantName.String
	} else {
		billName = "Hóa đơn"
	}
	desc := fmt.Sprintf("Đã xóa hóa đơn nháp %q", billName)
	_, _ = q.InsertGroupActivity(ctx, sqlc.InsertGroupActivityParams{
		ID:            pgtype.UUID{Bytes: uuid.Must(uuid.NewV7()), Valid: true},
		GroupID:       pgtype.UUID{Bytes: p.GroupID, Valid: true},
		ActorMemberID: pgtype.UUID{Bytes: actorID, Valid: true},
		ActionType:    "deleted_bill",
		Description:   desc,
		Metadata:      metaBytes,
	})

	if err := r.notifyBillInvalidate(ctx, tx, p.GroupID, p.ID, billRow.Version, "bill.deleted"); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ============================================================================
// OCR JOBS
// ============================================================================

func (r *postgresRepository) CreateOCRJob(ctx context.Context, job *domain.OCRJob, beforeCommit func(ctx context.Context, tx pgx.Tx) error) (*domain.OCRJob, error) {
	if job == nil {
		return nil, domain.ErrInvalidInput
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create ocr job tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := sqlc.New(tx)
	billRow, err := q.GetBillOnlyByID(ctx, pgtype.UUID{Bytes: job.BillID, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrBillNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get OCR job bill: %w", err)
	}
	if err = lockActiveBillGroup(ctx, tx, uuid.UUID(billRow.GroupID.Bytes)); err != nil {
		return nil, err
	}

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

	var jobVersion int32 = 1
	if job.Version > 0 {
		jobVersion = job.Version
	}

	dbJob, err := q.CreateOCRJob(ctx, sqlc.CreateOCRJobParams{
		ID:          pgtype.UUID{Bytes: job.ID, Valid: true},
		BillID:      pgtype.UUID{Bytes: job.BillID, Valid: true},
		Status:      statusStr,
		Provider:    provider,
		Attempts:    job.Attempts,
		RawResponse: job.RawResponse,
		Candidate:   candidateBytes,
		Version:     jobVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("create ocr job: %w", err)
	}

	created, err := toDomainOCRJob(&dbJob)
	if err != nil {
		return nil, err
	}
	if err = r.notifyOCR(ctx, tx, uuid.UUID(billRow.GroupID.Bytes), created); err != nil {
		return nil, err
	}

	if beforeCommit != nil {
		if err := beforeCommit(ctx, tx); err != nil {
			return nil, fmt.Errorf("before commit hook: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create ocr job tx: %w", err)
	}

	return created, nil
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

func (r *postgresRepository) UpdateOCRJobProcessing(ctx context.Context, id uuid.UUID) error {
	return r.updateOCRJobWithNotify(ctx, func(ctx context.Context, q *sqlc.Queries) (sqlc.OcrJob, error) {
		return q.UpdateOCRJobProcessing(ctx, pgtype.UUID{Bytes: id, Valid: true})
	})
}

func (r *postgresRepository) UpdateOCRJobSuccess(ctx context.Context, id uuid.UUID, candidate *domain.OCRCandidate, raw []byte) error {
	var candidateBytes []byte
	if candidate != nil {
		candidateBytes, _ = json.Marshal(candidate)
	}
	return r.updateOCRJobWithNotify(ctx, func(ctx context.Context, q *sqlc.Queries) (sqlc.OcrJob, error) {
		return q.UpdateOCRJobSuccess(ctx, sqlc.UpdateOCRJobSuccessParams{
			ID:          pgtype.UUID{Bytes: id, Valid: true},
			Candidate:   candidateBytes,
			RawResponse: raw,
		})
	})
}

func (r *postgresRepository) UpdateOCRJobFailed(ctx context.Context, id uuid.UUID, errReason string) error {
	return r.updateOCRJobWithNotify(ctx, func(ctx context.Context, q *sqlc.Queries) (sqlc.OcrJob, error) {
		return q.UpdateOCRJobFailed(ctx, sqlc.UpdateOCRJobFailedParams{
			ID:           pgtype.UUID{Bytes: id, Valid: true},
			ErrorMessage: pgtype.Text{String: errReason, Valid: true},
		})
	})
}

func (r *postgresRepository) updateOCRJobWithNotify(ctx context.Context, update func(context.Context, *sqlc.Queries) (sqlc.OcrJob, error)) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin ocr job update tx: %w", err)
	}
	defer tx.Rollback(ctx)
	q := sqlc.New(tx)
	dbJob, err := update(ctx, q)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrOcrJobConflict
		}
		return fmt.Errorf("update ocr job: %w", err)
	}
	job, err := toDomainOCRJob(&dbJob)
	if err != nil {
		return err
	}
	billRow, err := q.GetBillOnlyByID(ctx, pgtype.UUID{Bytes: job.BillID, Valid: true})
	if err != nil {
		return fmt.Errorf("get ocr job bill: %w", err)
	}
	if err = r.notifyOCR(ctx, tx, uuid.UUID(billRow.GroupID.Bytes), job); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit ocr job update tx: %w", err)
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

// CountActiveOCRJobs đếm số job OCR đang ở trạng thái queued hoặc processing, dùng làm nguồn cho
// gauge paysplit_ocr_queue_depth (Spec 3 AC-14).
func (r *postgresRepository) CountActiveOCRJobs(ctx context.Context) (int64, error) {
	q := sqlc.New(r.pool)
	count, err := q.CountActiveOCRJobs(ctx)
	if err != nil {
		return 0, fmt.Errorf("count active ocr jobs: %w", err)
	}
	return count, nil
}

func (r *postgresRepository) EnqueueMediaCleanup(ctx context.Context, prefix, kind string) error {
	q := sqlc.New(r.pool)
	return q.InsertMediaCleanupJob(ctx, sqlc.InsertMediaCleanupJobParams{
		ID:        pgtype.UUID{Bytes: uuid.Must(uuid.NewV7()), Valid: true},
		ObjectKey: prefix,
	})
}

func lockActiveBillGroup(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) error {
	if err := database.LockActiveGroup(ctx, tx, groupID); err != nil {
		if errors.Is(err, database.ErrGroupNotActive) {
			return domain.ErrForbidden
		}
		return fmt.Errorf("lock active group: %w", err)
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

	var reviewedAt *time.Time
	if b.ReviewedAt.Valid {
		t := b.ReviewedAt.Time
		reviewedAt = &t
	}

	var reviewedByMemberID *uuid.UUID
	if b.ReviewedByMemberID.Valid {
		id := uuid.UUID(b.ReviewedByMemberID.Bytes)
		reviewedByMemberID = &id
	}

	statusStr := fmt.Sprintf("%v", b.Status)

	return &domain.Bill{
		ID:                 uuid.UUID(b.ID.Bytes),
		GroupID:            uuid.UUID(b.GroupID.Bytes),
		CreditorMemberID:   uuid.UUID(b.CreditorMemberID.Bytes),
		Status:             domain.BillStatus(statusStr),
		MerchantName:       merchantName,
		BillDate:           billDate,
		Subtotal:           b.Subtotal,
		ServiceCharge:      b.ServiceCharge,
		VAT:                b.Vat,
		Discount:           b.Discount,
		TotalItemDiscount:  b.TotalItemDiscount,
		GeneralDiscount:    b.GeneralDiscount,
		Total:              b.Total,
		SplitMethod:        domain.SplitMethod(b.SplitMethod),
		MismatchCodes:      b.MismatchCodes,
		ReplacesBillID:     replacesID,
		Version:            b.Version,
		FinalizedAt:        finalizedAt,
		VoidedAt:           voidedAt,
		ReviewedAt:         reviewedAt,
		ReviewedByMemberID: reviewedByMemberID,
		CreatedAt:          b.CreatedAt.Time,
		UpdatedAt:          b.UpdatedAt.Time,
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
		ID:             uuid.UUID(it.ID.Bytes),
		BillID:         uuid.UUID(it.BillID.Bytes),
		GroupID:        uuid.UUID(it.GroupID.Bytes),
		Name:           it.Name,
		Quantity:       qtyStr,
		UnitPrice:      it.UnitPrice,
		LineTotal:      it.LineTotal,
		DiscountAmount: it.DiscountAmount,
		FinalPrice:     it.FinalPrice,
		Position:       it.Position,
		CreatedAt:      it.CreatedAt.Time,
		UpdatedAt:      it.UpdatedAt.Time,
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
		ID:                 uuid.UUID(s.ID.Bytes),
		BillID:             uuid.UUID(s.BillID.Bytes),
		GroupID:            uuid.UUID(s.GroupID.Bytes),
		MemberID:           uuid.UUID(s.MemberID.Bytes),
		ItemSubtotal:       s.ItemSubtotal,
		ServiceChargeShare: s.ServiceChargeShare,
		VATShare:           s.VatShare,
		DiscountShare:      s.DiscountShare,
		RoundingAdjustment: s.RoundingAdjustment,
		FinalAmount:        s.FinalAmount,
		ComputedAmount:     s.FinalAmount,
		CreatedAt:          s.CreatedAt.Time,
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

func encodeCursor(t time.Time, id string) string {
	raw := t.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(s string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.UUID{}, err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.UUID{}, errors.New("malformed cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.UUID{}, err
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.UUID{}, err
	}
	return t, id, nil
}

// ============================================================================
// IDEMPOTENCY & RETENTION (Spec 3 AC-1, AC-9, AC-11, AC-13, AC-14)
// ============================================================================

func (r *postgresRepository) ReserveIdempotencyKey(ctx context.Context, p repository.ReserveIdempotencyParams) (*repository.IdempotencyRecord, error) {
	ttl := p.TTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	expiresAt := time.Now().Add(ttl)

	query := `
		INSERT INTO bill_idempotency_keys (
			actor_user_id, operation, key_hash, canonical_request_hash, operation_id, state, retry_after, expires_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, 'in_progress', $6, $7, now(), now()
		)
		ON CONFLICT (actor_user_id, operation, key_hash) DO UPDATE
		SET canonical_request_hash = EXCLUDED.canonical_request_hash,
		    operation_id = EXCLUDED.operation_id,
		    state = 'in_progress',
		    response_code = NULL,
		    response_body = NULL,
		    resource_id = NULL,
		    retry_after = EXCLUDED.retry_after,
		    expires_at = EXCLUDED.expires_at,
		    updated_at = now()
		WHERE bill_idempotency_keys.expires_at <= now()
		RETURNING actor_user_id, operation, key_hash, canonical_request_hash, operation_id, state, response_code, response_body, resource_id, retry_after, expires_at, created_at;
	`

	var rec repository.IdempotencyRecord
	var resID pgtype.UUID
	var respCode pgtype.Int4
	var respBody []byte
	var retryAfter pgtype.Timestamptz

	err := r.pool.QueryRow(ctx, query,
		p.ActorUserID, p.Operation, p.KeyHash, p.CanonicalRequestHash, p.OperationID, p.RetryAfter, expiresAt,
	).Scan(
		&rec.ActorUserID, &rec.Operation, &rec.KeyHash, &rec.CanonicalRequestHash, &rec.OperationID,
		&rec.State, &respCode, &respBody, &resID, &retryAfter, &rec.ExpiresAt, &rec.CreatedAt,
	)
	if err == nil {
		if respCode.Valid {
			rec.ResponseCode = int(respCode.Int32)
		}
		rec.ResponseBody = respBody
		if resID.Valid {
			u := uuid.UUID(resID.Bytes)
			rec.ResourceID = &u
		}
		if retryAfter.Valid {
			t := retryAfter.Time
			rec.RetryAfter = &t
		}
		return &rec, nil
	}

	// Không có dòng trả về nghĩa là đã tồn tại một bản ghi CÒN HẠN cho khóa này (bản ghi hết hạn
	// đã bị nhánh DO UPDATE ở trên chiếm lại). Đọc lại bản ghi còn hạn đó để tầng usecase quyết định
	// replay, xung đột hash, hay đang chạy dở (Spec 3 AC-1, AC-9).
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err := r.GetIdempotencyKey(ctx, p.ActorUserID, p.Operation, p.KeyHash)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			// Chỉ xảy ra khi bản ghi hết hạn ngay giữa hai câu lệnh. Coi như xung đột tạm thời
			// thay vì trả về nil để tầng trên tự dereference.
			return nil, domain.ErrIdempotencyInProgress
		}
		return existing, nil
	}

	return nil, fmt.Errorf("reserve idempotency key: %w", err)
}

// PurgeExpiredIdempotencyKeys xóa các bản ghi idempotency đã hết hạn. Không có bước dọn này thì
// bảng phình vô hạn và mọi khóa hết hạn phụ thuộc hoàn toàn vào nhánh chiếm lại ở trên.
func (r *postgresRepository) PurgeExpiredIdempotencyKeys(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM bill_idempotency_keys WHERE expires_at <= now();`)
	if err != nil {
		return 0, fmt.Errorf("purge expired idempotency keys: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *postgresRepository) ReleaseIdempotencyKey(ctx context.Context, actorUserID uuid.UUID, operation, keyHash string) error {
	query := `
		DELETE FROM bill_idempotency_keys
		WHERE actor_user_id = $1 AND operation = $2 AND key_hash = $3 AND state = 'in_progress';
	`
	_, err := r.pool.Exec(ctx, query, actorUserID, operation, keyHash)
	if err != nil {
		return fmt.Errorf("release idempotency key: %w", err)
	}
	return nil
}

func (r *postgresRepository) CompleteIdempotencyKey(ctx context.Context, p repository.CompleteIdempotencyParams) error {
	return completeIdempotencyKey(ctx, r.pool, p)
}

func (r *postgresRepository) CompleteIdempotencyKeyInTx(ctx context.Context, tx pgx.Tx, p repository.CompleteIdempotencyParams) error {
	return completeIdempotencyKey(ctx, tx, p)
}

type idempotencyExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func completeIdempotencyKey(ctx context.Context, execer idempotencyExecer, p repository.CompleteIdempotencyParams) error {
	query := `
		UPDATE bill_idempotency_keys
		SET state = 'completed',
		    response_code = $4,
		    response_body = $5,
		    resource_id = $6,
		    updated_at = now()
		WHERE actor_user_id = $1 AND operation = $2 AND key_hash = $3;
	`
	var resID pgtype.UUID
	if p.ResourceID != nil {
		resID = pgtype.UUID{Bytes: *p.ResourceID, Valid: true}
	}

	tag, err := execer.Exec(ctx, query, p.ActorUserID, p.Operation, p.KeyHash, p.ResponseCode, p.ResponseBody, resID)
	if err != nil {
		return fmt.Errorf("complete idempotency key: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("complete idempotency key: reservation not found")
	}
	return nil
}

func (r *postgresRepository) GetIdempotencyKey(ctx context.Context, actorUserID uuid.UUID, operation, keyHash string) (*repository.IdempotencyRecord, error) {
	query := `
		SELECT actor_user_id, operation, key_hash, canonical_request_hash, operation_id, state, response_code, response_body, resource_id, retry_after, expires_at, created_at
		FROM bill_idempotency_keys
		WHERE actor_user_id = $1 AND operation = $2 AND key_hash = $3 AND expires_at > now();
	`
	var rec repository.IdempotencyRecord
	var resID pgtype.UUID
	var respCode pgtype.Int4
	var respBody []byte
	var retryAfter pgtype.Timestamptz

	err := r.pool.QueryRow(ctx, query, actorUserID, operation, keyHash).Scan(
		&rec.ActorUserID, &rec.Operation, &rec.KeyHash, &rec.CanonicalRequestHash, &rec.OperationID,
		&rec.State, &respCode, &respBody, &resID, &retryAfter, &rec.ExpiresAt, &rec.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get idempotency key: %w", err)
	}

	if respCode.Valid {
		rec.ResponseCode = int(respCode.Int32)
	}
	rec.ResponseBody = respBody
	if resID.Valid {
		u := uuid.UUID(resID.Bytes)
		rec.ResourceID = &u
	}
	if retryAfter.Valid {
		t := retryAfter.Time
		rec.RetryAfter = &t
	}
	return &rec, nil
}

func (r *postgresRepository) PurgeExpiredRawOCRResponses(ctx context.Context, olderThan time.Duration) (int64, error) {
	if olderThan <= 0 {
		olderThan = 30 * 24 * time.Hour
	}
	threshold := time.Now().Add(-olderThan)
	query := `
		UPDATE ocr_jobs
		SET raw_response = NULL,
		    updated_at = now()
		WHERE completed_at < $1 AND raw_response IS NOT NULL AND status IN ('succeeded', 'failed');
	`
	tag, err := r.pool.Exec(ctx, query, threshold)
	if err != nil {
		return 0, fmt.Errorf("purge expired raw ocr responses: %w", err)
	}
	return tag.RowsAffected(), nil
}

func formatVND(n int64) string {
	in := strconv.FormatInt(n, 10)
	var out []byte
	l := len(in)
	for i, c := range in {
		if i > 0 && (l-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, byte(c))
	}
	return string(out)
}
