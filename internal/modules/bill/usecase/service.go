package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	platformmetrics "paysplit-backend/internal/platform/metrics"
)

// OCRProvider là interface định nghĩa khả năng bóc tách dữ liệu từ ảnh hóa đơn (Spec 3 AC-3).
type OCRProvider interface {
	ExtractReceipt(ctx context.Context, imageBytes []byte, mimeType string) (*domain.OCRCandidate, []byte, error)
}

// BillStorage là interface định nghĩa khả năng lưu trữ ảnh hóa đơn riêng tư trên Cloudinary (Spec 3 AC-1, AC-8, AC-13).
type BillStorage interface {
	Upload(ctx context.Context, data []byte, publicID string) (string, error)
	SignedURL(publicID string, ttl time.Duration) (string, error)
	Download(ctx context.Context, publicID string) ([]byte, error)
	Delete(ctx context.Context, publicID string) error
	DeleteByPrefix(ctx context.Context, prefix string) error
}

// ReceiptProcessor là interface tiền xử lý ảnh hóa đơn (Spec 3 AC-1).
type ReceiptProcessor interface {
	Process(ctx context.Context, input []byte) ([]byte, error)
	IsUnsupported(err error) bool
}

// JobEnqueuer là interface đẩy công việc OCR và thông báo vào hàng đợi River Queue.
type JobEnqueuer interface {
	EnqueueOCRJobTx(ctx context.Context, tx pgx.Tx, billID, jobID, groupID uuid.UUID) error
	EnqueueOCRJob(ctx context.Context, billID, jobID, groupID uuid.UUID) error
	EnqueueNotificationTx(ctx context.Context, tx pgx.Tx, notificationID string) error
}

// Service quản lý toàn bộ nghiệp vụ của module Bill và OCR.
type Service struct {
	repo         repository.Repository
	ocrProvider  OCRProvider
	storage      BillStorage
	processor    ReceiptProcessor
	enqueuer     JobEnqueuer
	manualLimit  int
	manualWindow time.Duration
}

// NewService khởi tạo Bill usecase service với các dependencies.
func NewService(
	repo repository.Repository,
	ocrProvider OCRProvider,
	storage BillStorage,
	processor ReceiptProcessor,
	enqueuer JobEnqueuer,
) *Service {
	return &Service{
		repo:         repo,
		ocrProvider:  ocrProvider,
		storage:      storage,
		processor:    processor,
		enqueuer:     enqueuer,
		manualLimit:  5,
		manualWindow: 24 * time.Hour,
	}
}

// SetManualOCRConfig cấu hình giới hạn số lần và cửa sổ retry OCR thủ công (Spec 3 AC-2).
func (s *Service) SetManualOCRConfig(limit int, window time.Duration) {
	if limit > 0 {
		s.manualLimit = limit
	}
	if window > 0 {
		s.manualWindow = window
	}
}

// ============================================================================
// DTOs (Data Transfer Objects)
// ============================================================================

type CreateBillItemRequest struct {
	Name      string `json:"name"`
	Quantity  string `json:"quantity"`
	UnitPrice int64  `json:"unit_price"`
	LineTotal int64  `json:"line_total"`
	// DiscountAmount là khuyến mãi riêng theo món (VND), mặc định 0 khi không gửi lên.
	// Máy chủ tự tính lại final_price và tổng hợp total_item_discount, không nhận final_price
	// trực tiếp từ client (Spec 3 AC-19, AC-20).
	DiscountAmount int64                         `json:"discount_amount"`
	Assignments    []CreateItemAssignmentRequest `json:"assignments"`
}

type CreateItemAssignmentRequest struct {
	MemberID uuid.UUID `json:"member_id"`
	Weight   string    `json:"weight"`
}

type CreateBillRequest struct {
	GroupID        uuid.UUID               `json:"group_id"`
	MerchantName   *string                 `json:"merchant_name"`
	BillDate       *time.Time              `json:"bill_date"`
	Subtotal       int64                   `json:"subtotal"`
	ServiceCharge  int64                   `json:"service_charge"`
	VAT            int64                   `json:"vat"`
	Discount       int64                   `json:"discount"`
	Total          int64                   `json:"total"`
	SplitMethod    domain.SplitMethod      `json:"split_method"`
	ReplacesBillID *uuid.UUID              `json:"replaces_bill_id"`
	Items          []CreateBillItemRequest `json:"items"`
	Files          [][]byte                `json:"-"`
}

type CreateBillResult struct {
	Bill       *domain.Bill   `json:"bill"`
	OCRJob     *domain.OCRJob `json:"ocr_job,omitempty"`
	IsAccepted bool           `json:"is_accepted"`
}

type BillDetailResponse struct {
	Bill       *domain.Bill        `json:"bill"`
	Breakdown  []*MemberAllocation `json:"breakdown,omitempty"`
	SignedURLs map[string]string   `json:"signed_urls,omitempty"`
}

type UpdateDraftRequest struct {
	Version       int32                   `json:"version"`
	MerchantName  *string                 `json:"merchant_name"`
	BillDate      *time.Time              `json:"bill_date"`
	Subtotal      int64                   `json:"subtotal"`
	ServiceCharge int64                   `json:"service_charge"`
	VAT           int64                   `json:"vat"`
	Discount      int64                   `json:"discount"`
	Total         int64                   `json:"total"`
	SplitMethod   domain.SplitMethod      `json:"split_method"`
	Items         []CreateBillItemRequest `json:"items"`
}

// ============================================================================
// USECASE IMPLEMENTATIONS
// ============================================================================

// CreateBill tạo mới một hóa đơn (thủ công hoặc từ 1-5 ảnh hóa đơn).
func (s *Service) CreateBill(ctx context.Context, callerUserID uuid.UUID, req CreateBillRequest) (*CreateBillResult, error) {
	if req.GroupID == uuid.Nil {
		return nil, domain.ErrInvalidInput
	}

	// 1. Kiểm tra caller có thuộc nhóm không
	member, err := s.repo.GetGroupMember(ctx, req.GroupID, callerUserID)
	if err != nil || member == nil || member.Status != "active" {
		return nil, domain.ErrForbidden
	}

	if len(req.Files) > 5 {
		return nil, fmt.Errorf("%w: maximum 5 receipt images allowed", domain.ErrInvalidInput)
	}
	if len(req.Items) > 100 {
		return nil, fmt.Errorf("%w: maximum 100 items allowed", domain.ErrInvalidInput)
	}
	// Spec 3 AC-21: CreateBill trước đây không có kiểm tra này, khiến discount > 0 vi phạm
	// check_bills_discount_composition ở tầng database và trả về lỗi 500 không kiểm soát.
	if req.Discount < 0 {
		return nil, fmt.Errorf("%w: discount must not be negative", domain.ErrInvalidInput)
	}

	// Kiểm tra tính hợp lệ của replaces_bill_id nếu được truyền (Spec 3 AC-11)
	if req.ReplacesBillID != nil && *req.ReplacesBillID != uuid.Nil {
		replacedBill, err := s.repo.GetBillByID(ctx, *req.ReplacesBillID, req.GroupID)
		if err != nil {
			if errors.Is(err, domain.ErrBillNotFound) {
				return nil, fmt.Errorf("%w: replacement bill not found in group", domain.ErrInvalidInput)
			}
			return nil, err
		}
		if replacedBill.Status != domain.BillStatusVoided {
			return nil, fmt.Errorf("%w: replaced bill must be voided", domain.ErrInvalidInput)
		}
	}

	billID := uuid.Must(uuid.NewV7())
	operationID := uuid.Must(uuid.NewV7())

	uploadedKeys := make([]string, 0, len(req.Files))
	success := false
	defer func() {
		if !success && len(uploadedKeys) > 0 && s.storage != nil {
			for _, key := range uploadedKeys {
				_ = s.storage.Delete(context.Background(), key)
			}
		}
	}()

	// 2. Xử lý và upload ảnh (nếu có)
	images := make([]*domain.BillImage, 0, len(req.Files))
	for i, fileBytes := range req.Files {
		processed, err := s.processor.Process(ctx, fileBytes)
		if err != nil {
			return nil, fmt.Errorf("process image %d: %w", i, err)
		}

		publicID := fmt.Sprintf("bills/%s/%d", operationID, i)
		uploadedKey, err := s.storage.Upload(ctx, processed, publicID)
		if err != nil {
			return nil, fmt.Errorf("upload image %d to storage: %w", i, err)
		}
		uploadedKeys = append(uploadedKeys, uploadedKey)

		images = append(images, &domain.BillImage{
			ID:       uuid.Must(uuid.NewV7()),
			BillID:   billID,
			GroupID:  req.GroupID,
			ImageKey: uploadedKey,
			Position: int16(i),
		})
	}

	// 3. Chuẩn bị danh sách món ăn, kiểm tra discount_amount theo từng món (Spec 3 AC-19, AC-20, AC-21)
	items := make([]*domain.BillItem, 0, len(req.Items))
	var totalItemDiscount int64
	for i, it := range req.Items {
		itemID := uuid.Must(uuid.NewV7())
		lineTotal := it.LineTotal
		if lineTotal == 0 && it.UnitPrice > 0 {
			lineTotal = computeLineTotal(it.UnitPrice, it.Quantity)
		}
		if lineTotal < 0 {
			return nil, fmt.Errorf("%w: item %d: line_total must not be negative", domain.ErrInvalidInput, i)
		}
		if it.DiscountAmount < 0 || it.DiscountAmount > lineTotal {
			return nil, fmt.Errorf("%w: item %d: discount_amount must be between 0 and line_total", domain.ErrInvalidInput, i)
		}
		totalItemDiscount += it.DiscountAmount

		items = append(items, &domain.BillItem{
			ID:             itemID,
			BillID:         billID,
			GroupID:        req.GroupID,
			Name:           it.Name,
			Quantity:       it.Quantity,
			UnitPrice:      it.UnitPrice,
			LineTotal:      lineTotal,
			DiscountAmount: it.DiscountAmount,
			FinalPrice:     lineTotal - it.DiscountAmount,
			Position:       int16(i),
		})
	}

	generalDiscount := req.Discount - totalItemDiscount
	if generalDiscount < 0 {
		return nil, fmt.Errorf("%w: discount is less than the sum of item discounts", domain.ErrInvalidInput)
	}

	// Kiểm tra tính hợp lệ của trọng số assignments, sau khi discount đã hợp lệ (Spec 3 AC-19)
	for i, it := range req.Items {
		for _, assign := range it.Assignments {
			w, err := strconv.ParseFloat(assign.Weight, 64)
			if err != nil || w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
				return nil, fmt.Errorf("%w: assignment weight must be a positive number", domain.ErrInvalidInput)
			}
			items[i].Assignments = append(items[i].Assignments, &domain.BillItemAssignment{
				ID:         uuid.Must(uuid.NewV7()),
				BillItemID: items[i].ID,
				GroupID:    req.GroupID,
				MemberID:   assign.MemberID,
				Weight:     assign.Weight,
			})
		}
	}

	// 4. Chuẩn bị Bill domain entity
	splitMethod := req.SplitMethod
	if splitMethod == "" {
		splitMethod = domain.SplitMethodEven
	}

	bill := &domain.Bill{
		ID:                billID,
		GroupID:           req.GroupID,
		CreditorMemberID:  member.ID,
		MerchantName:      req.MerchantName,
		BillDate:          req.BillDate,
		Subtotal:          req.Subtotal,
		ServiceCharge:     req.ServiceCharge,
		VAT:               req.VAT,
		Discount:          req.Discount,
		TotalItemDiscount: totalItemDiscount,
		GeneralDiscount:   generalDiscount,
		Total:             req.Total,
		SplitMethod:       splitMethod,
		ReplacesBillID:    req.ReplacesBillID,
		Status:            domain.BillStatusDraft,
		Version:           1,
	}

	// 5. Nếu có ảnh -> tạo kèm OCRJob (Spec 3 AC-2)
	var ocrJob *domain.OCRJob
	if len(images) > 0 {
		ocrJob = &domain.OCRJob{
			ID:       uuid.Must(uuid.NewV7()),
			BillID:   billID,
			Status:   domain.OCRJobStatusQueued,
			Provider: "llamaextract",
			Version:  1,
		}
	}

	createdBill, err := s.repo.CreateBill(ctx, repository.CreateBillParams{
		Bill:   bill,
		Images: images,
		Items:  items,
		OCRJob: ocrJob,
		BeforeCommit: func(txCtx context.Context, tx pgx.Tx) error {
			if ocrJob != nil && s.enqueuer != nil {
				return s.enqueuer.EnqueueOCRJobTx(txCtx, tx, billID, ocrJob.ID, req.GroupID)
			}
			return nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create bill in repo: %w", err)
	}

	success = true

	return &CreateBillResult{
		Bill:       createdBill,
		OCRJob:     ocrJob,
		IsAccepted: len(images) > 0,
	}, nil
}

// GetBillDetail lấy thông tin chi tiết hóa đơn (kèm signed image URLs và provisional breakdown).
func (s *Service) GetBillDetail(ctx context.Context, callerUserID, billID, groupID uuid.UUID) (*BillDetailResponse, error) {
	// Kiểm tra caller có thuộc nhóm không
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member == nil || member.Status != "active" {
		return nil, domain.ErrForbidden
	}

	bill, err := s.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		return nil, err
	}

	// Sinh Signed URLs cho danh sách ảnh
	signedURLs := make(map[string]string, len(bill.Images))
	for _, img := range bill.Images {
		if url, err := s.storage.SignedURL(img.ImageKey, 5*time.Minute); err == nil {
			signedURLs[img.ImageKey] = url
		}
	}

	// Tính breakdown tạm thời khi bill còn là draft hoặc reviewed. Dùng đúng hàm đối soát mà review
	// và chốt sổ dùng, nên ba đường đi luôn nói cùng một điều. Khi có mã chặn, chúng được trả về
	// trong mismatch_codes thay vì để client nhận một response trống không giải thích gì
	// (Spec 3 AC-6, 0002 mục 10).
	var breakdown []*MemberAllocation
	if bill.Status == domain.BillStatusDraft || bill.Status == domain.BillStatusReviewed {
		previewStart := time.Now()

		var activeSet map[uuid.UUID]bool
		if activeMembers, listErr := s.repo.ListActiveGroupMembers(ctx, groupID); listErr == nil {
			activeSet = make(map[uuid.UUID]bool, len(activeMembers))
			for _, m := range activeMembers {
				activeSet[m.ID] = true
			}
		}

		allocations, blockers := evaluateAllocation(bill, activeSet)
		breakdown = allocations
		bill.MismatchCodes = mergeMismatchCodes(bill.MismatchCodes, blockers)

		outcome := "success"
		if len(blockers) > 0 {
			outcome = "error"
		}
		platformmetrics.RecordBillPreview(outcome, time.Since(previewStart))
	}

	return &BillDetailResponse{
		Bill:       bill,
		Breakdown:  breakdown,
		SignedURLs: signedURLs,
	}, nil
}

// ListBills lấy danh sách hóa đơn trong nhóm có phân trang (offset legacy).
func (s *Service) ListBills(ctx context.Context, callerUserID, groupID uuid.UUID, limit, offset int32) ([]*domain.Bill, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member == nil || member.Status != "active" {
		return nil, domain.ErrForbidden
	}

	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	return s.repo.ListBillsByGroup(ctx, groupID, limit, offset)
}

// ListBillsCursor lấy danh sách hóa đơn theo keyset cursor (Spec 3 AC-12).
func (s *Service) ListBillsCursor(ctx context.Context, callerUserID, groupID uuid.UUID, cursor *string, limit int32) (*repository.ListBillsCursorResult, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member == nil || member.Status != "active" {
		return nil, domain.ErrForbidden
	}

	return s.repo.ListBillsByGroupCursor(ctx, repository.ListBillsCursorParams{
		GroupID: groupID,
		Cursor:  cursor,
		Limit:   limit,
	})
}

// RetryOCR kích hoạt lại quá trình bóc tách OCR thủ công (tối đa 5 lần / 24h, Spec 3 AC-2, AC-8).
func (s *Service) RetryOCR(ctx context.Context, callerUserID, billID, groupID uuid.UUID) (*domain.OCRJob, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member == nil || member.Status != "active" {
		return nil, domain.ErrForbidden
	}

	bill, err := s.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		return nil, err
	}

	// Chỉ cho phép Creditor hoặc Captain retry OCR (Spec 3 AC-8)
	if member.Role != "captain" && member.ID != bill.CreditorMemberID {
		return nil, domain.ErrForbidden
	}

	// Chỉ cho phép retry khi bill là draft hoặc reviewed và có ảnh
	if bill.Status != domain.BillStatusDraft && bill.Status != domain.BillStatusReviewed {
		return nil, domain.ErrBillImmutable
	}
	if len(bill.Images) == 0 {
		return nil, domain.ErrImagesRequired
	}

	// Kiểm tra xem đã có job OCR nào đang chạy không
	activeJob, _ := s.repo.GetActiveOCRJobByBillID(ctx, billID)
	if activeJob != nil {
		return nil, domain.ErrOcrAlreadyRunning
	}

	// Kiểm tra giới hạn retry thủ công (Spec 3 AC-2)
	limit := s.manualLimit
	if limit <= 0 {
		limit = 5
	}
	window := s.manualWindow
	if window <= 0 {
		window = 24 * time.Hour
	}
	attempts, err := s.repo.CountManualOCRAttemptsInWindow(ctx, billID, time.Now().Add(-window))
	if err == nil && int(attempts) >= limit {
		return nil, domain.ErrOcrLimitReached
	}

	// Tạo job OCR mới và enqueue vào River Queue
	newJob := &domain.OCRJob{
		ID:       uuid.Must(uuid.NewV7()),
		BillID:   billID,
		Status:   domain.OCRJobStatusQueued,
		Provider: "llamaextract",
		Version:  bill.Version,
	}

	// Insert job và enqueue River trong cùng transaction (mirror CreateBill) để một lần enqueue thất
	// bại không để lại job "queued" mồ côi mà không worker nào từng nhận (Spec 3 AC-2).
	var beforeCommit func(ctx context.Context, tx pgx.Tx) error
	if s.enqueuer != nil {
		beforeCommit = func(txCtx context.Context, tx pgx.Tx) error {
			return s.enqueuer.EnqueueOCRJobTx(txCtx, tx, billID, newJob.ID, groupID)
		}
	}

	createdJob, err := s.repo.CreateOCRJob(ctx, newJob, beforeCommit)
	if err != nil {
		return nil, fmt.Errorf("create retry ocr job: %w", err)
	}

	return createdJob, nil
}

// ApplyCandidate đổ dữ liệu bóc tách từ AI vào hóa đơn nháp (Spec 3 AC-4).
func (s *Service) ApplyCandidate(ctx context.Context, callerUserID, billID, groupID, jobID uuid.UUID, expectedVersion int32) (*domain.Bill, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member == nil || member.Status != "active" {
		return nil, domain.ErrForbidden
	}

	bill, err := s.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		return nil, err
	}
	if member.Role != "captain" && member.ID != bill.CreditorMemberID {
		return nil, domain.ErrForbidden
	}
	if bill.Status != domain.BillStatusDraft && bill.Status != domain.BillStatusReviewed {
		if bill.Status == domain.BillStatusFinalized {
			return nil, domain.ErrBillImmutable
		}
		if bill.Status == domain.BillStatusVoided {
			return nil, domain.ErrBillAlreadyVoided
		}
		return nil, domain.ErrBillImmutable
	}

	ocrJob, err := s.repo.GetOCRJobByID(ctx, jobID)
	if err != nil {
		return nil, domain.ErrOcrJobNotFound
	}
	if ocrJob.BillID != billID {
		// Job tồn tại nhưng thuộc bill khác: trả lỗi giống "not found" để không xác nhận sự tồn tại của job (Spec 3 AC-4, AC-8).
		return nil, domain.ErrOcrJobNotFound
	}
	if ocrJob.Status != domain.OCRJobStatusSucceeded || ocrJob.Candidate == nil {
		return nil, domain.ErrOcrNotReady
	}
	if bill.Version != expectedVersion {
		return nil, domain.ErrVersionConflict
	}
	if bill.Version != ocrJob.Version {
		platformmetrics.RecordOCRStaleApply("bill_version_changed")
		return nil, domain.ErrOcrResultStale
	}

	// Lấy danh sách thành viên active trong nhóm để chia đều các món bóc tách
	activeMembers, err := s.repo.ListActiveGroupMembers(ctx, groupID)
	if err != nil || len(activeMembers) == 0 {
		return nil, fmt.Errorf("%w: no active members to assign", domain.ErrBillNotReady)
	}

	candidate := ocrJob.Candidate
	bill.MerchantName = candidate.MerchantName
	if candidate.BillDate != nil {
		if t, err := time.Parse("2006-01-02", *candidate.BillDate); err == nil {
			bill.BillDate = &t
		}
	}
	bill.Subtotal = candidate.Subtotal
	bill.ServiceCharge = candidate.ServiceCharge
	bill.VAT = candidate.VAT
	bill.Discount = candidate.Discount
	bill.TotalItemDiscount = candidate.TotalItemDiscount
	bill.GeneralDiscount = candidate.GeneralDiscount
	bill.Total = candidate.Total
	bill.MismatchCodes = candidate.Warnings

	// Tạo items từ Candidate
	items := make([]*domain.BillItem, 0, len(candidate.Items))
	for i, cItem := range candidate.Items {
		itemID := uuid.Must(uuid.NewV7())
		domItem := &domain.BillItem{
			ID:             itemID,
			BillID:         billID,
			GroupID:        groupID,
			Name:           cItem.Name,
			Quantity:       cItem.Quantity,
			UnitPrice:      cItem.UnitPrice,
			LineTotal:      cItem.LineTotal,
			DiscountAmount: cItem.DiscountAmount,
			FinalPrice:     cItem.FinalPrice,
			Position:       int16(i),
		}

		// Gán chia đều mặc định cho toàn bộ thành viên nhóm
		for _, m := range activeMembers {
			domItem.Assignments = append(domItem.Assignments, &domain.BillItemAssignment{
				ID:         uuid.Must(uuid.NewV7()),
				BillItemID: itemID,
				GroupID:    groupID,
				MemberID:   m.ID,
				Weight:     "1.0000",
			})
		}
		items = append(items, domItem)
	}

	return s.repo.UpdateDraftBill(ctx, repository.UpdateDraftParams{
		Bill:            bill,
		Items:           items,
		ExpectedVersion: expectedVersion,
		ActorMemberID:   member.ID,
	})
}

// UpdateDraftBill cập nhật chỉnh sửa hóa đơn nháp (Spec 3 AC-5, AC-7).
func (s *Service) UpdateDraftBill(ctx context.Context, callerUserID, billID, groupID uuid.UUID, req UpdateDraftRequest) (*domain.Bill, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member == nil || member.Status != "active" {
		return nil, domain.ErrForbidden
	}

	bill, err := s.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		return nil, err
	}
	if member.Role != "captain" && member.ID != bill.CreditorMemberID {
		return nil, domain.ErrForbidden
	}
	// Cho phép chỉnh sửa khi bill đang ở trạng thái draft hoặc reviewed (sửa bill reviewed sẽ tự động đưa về draft)
	if bill.Status != domain.BillStatusDraft && bill.Status != domain.BillStatusReviewed {
		if bill.Status == domain.BillStatusFinalized {
			return nil, domain.ErrBillImmutable
		}
		if bill.Status == domain.BillStatusVoided {
			return nil, domain.ErrBillAlreadyVoided
		}
		return nil, domain.ErrBillImmutable
	}

	if len(req.Items) > 100 {
		return nil, fmt.Errorf("%w: maximum 100 items allowed", domain.ErrInvalidInput)
	}

	// Kiểm tra giá trị discount không được âm
	if req.Discount < 0 {
		return nil, fmt.Errorf("%w: discount must not be negative", domain.ErrInvalidInput)
	}

	// Chuẩn bị danh sách món ăn, kiểm tra discount_amount theo từng món (Spec 3 AC-19)
	items := make([]*domain.BillItem, 0, len(req.Items))
	var totalItemDiscount int64
	for i, it := range req.Items {
		itemID := uuid.Must(uuid.NewV7())
		lineTotal := it.LineTotal
		if lineTotal == 0 && it.UnitPrice > 0 {
			lineTotal = computeLineTotal(it.UnitPrice, it.Quantity)
		}
		if lineTotal < 0 {
			return nil, fmt.Errorf("%w: item %d: line_total must not be negative", domain.ErrInvalidInput, i)
		}
		if it.DiscountAmount < 0 || it.DiscountAmount > lineTotal {
			return nil, fmt.Errorf("%w: item %d: discount_amount must be between 0 and line_total", domain.ErrInvalidInput, i)
		}
		totalItemDiscount += it.DiscountAmount

		items = append(items, &domain.BillItem{
			ID:             itemID,
			BillID:         billID,
			GroupID:        groupID,
			Name:           it.Name,
			Quantity:       it.Quantity,
			UnitPrice:      it.UnitPrice,
			LineTotal:      lineTotal,
			DiscountAmount: it.DiscountAmount,
			FinalPrice:     lineTotal - it.DiscountAmount,
			Position:       int16(i),
		})
	}

	generalDiscount := req.Discount - totalItemDiscount
	if generalDiscount < 0 {
		return nil, fmt.Errorf("%w: discount is less than the sum of item discounts", domain.ErrInvalidInput)
	}

	// Kiểm tra tính hợp lệ của trọng số assignments, sau khi discount đã hợp lệ (Spec 3 AC-19)
	for i, it := range req.Items {
		for _, assign := range it.Assignments {
			w, err := strconv.ParseFloat(assign.Weight, 64)
			if err != nil || w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
				return nil, fmt.Errorf("%w: assignment weight must be a positive number", domain.ErrInvalidInput)
			}
			items[i].Assignments = append(items[i].Assignments, &domain.BillItemAssignment{
				ID:         uuid.Must(uuid.NewV7()),
				BillItemID: items[i].ID,
				GroupID:    groupID,
				MemberID:   assign.MemberID,
				Weight:     assign.Weight,
			})
		}
	}

	bill.MerchantName = req.MerchantName
	bill.BillDate = req.BillDate
	bill.Subtotal = req.Subtotal
	bill.ServiceCharge = req.ServiceCharge
	bill.VAT = req.VAT
	bill.Discount = req.Discount
	bill.TotalItemDiscount = totalItemDiscount
	bill.GeneralDiscount = generalDiscount
	bill.Total = req.Total
	bill.SplitMethod = req.SplitMethod

	return s.repo.UpdateDraftBill(ctx, repository.UpdateDraftParams{
		Bill:            bill,
		Items:           items,
		ExpectedVersion: req.Version,
		ActorMemberID:   member.ID,
	})
}

// ReviewBill kiểm tra điều kiện đối soát và chuyển hóa đơn sang trạng thái reviewed (Spec 3 AC-7).
func (s *Service) ReviewBill(ctx context.Context, callerUserID, billID, groupID uuid.UUID, expectedVersion int32) (*domain.Bill, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member == nil || member.Status != "active" {
		return nil, domain.ErrForbidden
	}

	bill, err := s.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		return nil, err
	}
	if member.Role != "captain" && member.ID != bill.CreditorMemberID {
		return nil, domain.ErrForbidden
	}
	if bill.Status != domain.BillStatusDraft {
		if bill.Status == domain.BillStatusFinalized {
			return nil, domain.ErrBillImmutable
		}
		if bill.Status == domain.BillStatusVoided {
			return nil, domain.ErrBillAlreadyVoided
		}
		if bill.Status == domain.BillStatusReviewed {
			// Đã review ở version này
			if bill.Version == expectedVersion {
				return bill, nil
			}
		}
		return nil, domain.ErrBillImmutable
	}
	if bill.Version != expectedVersion {
		return nil, domain.ErrVersionConflict
	}

	// Đối soát tổng tiền và phân bổ qua đúng hàm mà đường đọc và chốt sổ dùng (Spec 3 AC-5, AC-6).
	activeMembers, err := s.repo.ListActiveGroupMembers(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("list active group members: %w", err)
	}
	activeSet := make(map[uuid.UUID]bool, len(activeMembers))
	for _, m := range activeMembers {
		activeSet[m.ID] = true
	}

	if _, blockers := evaluateAllocation(bill, activeSet); len(blockers) > 0 {
		recordBlockerMetric(blockers)
		return nil, blockersToError(blockers)
	}

	return s.repo.ReviewBill(ctx, billID, groupID, expectedVersion, member.ID)
}

// FinalizeBill chạy phân bổ chia sàn, lưu snapshot bill_shares và sinh debts (Spec 3 AC-9).
// Bọc ngoài finalizeBillImpl để đo paysplit_bill_finalize_duration_seconds (Spec 3 AC-14).
func (s *Service) FinalizeBill(ctx context.Context, callerUserID, billID, groupID uuid.UUID, expectedVersion int32) (*domain.Bill, error) {
	start := time.Now()
	bill, err := s.finalizeBillImpl(ctx, callerUserID, billID, groupID, expectedVersion)
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	platformmetrics.RecordBillFinalize(outcome, time.Since(start))
	return bill, err
}

func (s *Service) finalizeBillImpl(ctx context.Context, callerUserID, billID, groupID uuid.UUID, expectedVersion int32) (*domain.Bill, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member == nil || member.Status != "active" {
		return nil, domain.ErrForbidden
	}

	bill, err := s.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		return nil, err
	}
	if member.Role != "captain" {
		return nil, domain.ErrForbidden
	}

	// 1. Kiểm tra trạng thái và review (Spec 3 AC-7, AC-9)
	if bill.Status != domain.BillStatusReviewed {
		if bill.Status == domain.BillStatusFinalized {
			return nil, domain.ErrBillImmutable
		}
		if bill.Status == domain.BillStatusVoided {
			return nil, domain.ErrBillAlreadyVoided
		}
		return nil, domain.ErrReviewRequired
	}

	// 2. Kiểm tra version khớp trước khi chạy thuật toán Hamilton (Spec 3 AC-9)
	if bill.Version != expectedVersion {
		return nil, domain.ErrVersionConflict
	}

	// 3. Lấy danh sách thành viên active trong nhóm
	activeMembers, err := s.repo.ListActiveGroupMembers(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("list active group members: %w", err)
	}
	activeSet := make(map[uuid.UUID]bool, len(activeMembers))
	memberUserMap := make(map[uuid.UUID]uuid.UUID, len(activeMembers))
	for _, m := range activeMembers {
		activeSet[m.ID] = true
		memberUserMap[m.ID] = m.UserID
	}

	// 4. Kiểm tra tài khoản ngân hàng của Creditor (Spec 3 AC-9: 422 BANK_ACCOUNT_REQUIRED)
	creditorMember, err := s.repo.GetGroupMemberUser(ctx, bill.CreditorMemberID, groupID)
	if err != nil || creditorMember.DefaultBankCode == nil || creditorMember.DefaultBankAccountNum == nil ||
		strings.TrimSpace(*creditorMember.DefaultBankCode) == "" || strings.TrimSpace(*creditorMember.DefaultBankAccountNum) == "" {
		return nil, domain.ErrBankAccountRequired
	}

	// 5. Đối soát lại và chạy phân bổ, cùng một hàm mà đường đọc và review dùng. Người dùng đã thấy
	// đúng những mã chặn này ở bước xem trước, nên không có bất ngờ ở bước chốt sổ (Spec 3 AC-9, AC-10).
	allocations, blockers := evaluateAllocation(bill, activeSet)
	if len(blockers) > 0 {
		recordBlockerMetric(blockers)
		return nil, blockersToError(blockers)
	}

	shares := make([]*domain.BillShare, 0, len(allocations))
	debts := make([]*repository.Debt, 0, len(allocations))
	notifications := make([]*repository.NotificationParam, 0, len(allocations))

	for _, a := range allocations {
		// CalculateFloorAllocation đã bảo đảm FinalAmount không âm và tổng khớp Total, nên ở đây
		// không kẹp lại lần nữa. Việc kẹp thừa từng che mất lỗi tổng vượt hóa đơn (Spec 3 AC-10).
		amount := a.FinalAmount
		roundingAdj := a.RoundingAdjustment
		shares = append(shares, &domain.BillShare{
			ID:                 uuid.Must(uuid.NewV7()),
			BillID:             billID,
			GroupID:            groupID,
			MemberID:           a.MemberID,
			ItemSubtotal:       a.ItemSubtotal,
			ServiceChargeShare: a.ServiceChargeShare,
			VATShare:           a.VATShare,
			DiscountShare:      a.DiscountShare,
			RoundingAdjustment: roundingAdj,
			FinalAmount:        amount,
			ComputedAmount:     amount,
		})

		// Nếu không phải Creditor và có số tiền nợ > 0 -> tạo debt
		if a.MemberID != bill.CreditorMemberID && amount > 0 {
			debts = append(debts, &repository.Debt{
				ID:               uuid.Must(uuid.NewV7()),
				GroupID:          groupID,
				BillID:           billID,
				DebtorMemberID:   a.MemberID,
				CreditorMemberID: bill.CreditorMemberID,
				Amount:           amount,
				Status:           "awaiting",
			})
		}

		// Tạo bản ghi thông báo cho từng thành viên tham gia hóa đơn
		if userID, ok := memberUserMap[a.MemberID]; ok && userID != uuid.Nil {
			notifID := uuid.Must(uuid.NewV7())
			notifTitle := "Hóa đơn đã được chốt"
			notifBody := fmt.Sprintf("Hóa đơn đã được chốt. Phần thanh toán của bạn là %d đ.", amount)
			if a.MemberID == bill.CreditorMemberID {
				notifBody = fmt.Sprintf("Hóa đơn của bạn đã được chốt với tổng cộng %d đ.", bill.Total)
			}
			payloadBytes, _ := json.Marshal(map[string]string{
				"bill_id":  billID.String(),
				"group_id": groupID.String(),
				"amount":   strconv.FormatInt(amount, 10),
			})
			notifications = append(notifications, &repository.NotificationParam{
				ID:      notifID,
				UserID:  userID,
				Type:    "bill_finalized",
				Title:   notifTitle,
				Body:    notifBody,
				Payload: payloadBytes,
			})
		}
	}

	var beforeCommit func(ctx context.Context, tx pgx.Tx) error
	if s.enqueuer != nil && len(notifications) > 0 {
		beforeCommit = func(txCtx context.Context, tx pgx.Tx) error {
			for _, notif := range notifications {
				if err := s.enqueuer.EnqueueNotificationTx(txCtx, tx, notif.ID.String()); err != nil {
					return fmt.Errorf("enqueue finalize notification: %w", err)
				}
			}
			return nil
		}
	}

	return s.repo.FinalizeBill(ctx, repository.FinalizeBillParams{
		BillID:          billID,
		GroupID:         groupID,
		ExpectedVersion: expectedVersion,
		Shares:          shares,
		Debts:           debts,
		ActorMemberID:   member.ID,
		Notifications:   notifications,
		BeforeCommit:    beforeCommit,
	})
}

// VoidBill hủy bỏ hóa đơn đã chốt và đóng các khoản nợ liên quan (Spec 3 AC-11).
func (s *Service) VoidBill(ctx context.Context, callerUserID, billID, groupID uuid.UUID, expectedVersion int32, reason string) (*domain.Bill, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member == nil || member.Status != "active" {
		return nil, domain.ErrForbidden
	}
	if member.Role != "captain" {
		return nil, domain.ErrForbidden
	}

	trimmedReason := strings.TrimSpace(reason)
	if trimmedReason == "" || len(trimmedReason) > 500 {
		return nil, fmt.Errorf("%w: void reason must be between 1 and 500 characters", domain.ErrInvalidInput)
	}

	// 1. Kiểm tra trạng thái bill trước khi thao tác (Spec 3 AC-11)
	bill, err := s.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		return nil, err
	}
	if bill.Status == domain.BillStatusVoided {
		return nil, domain.ErrBillAlreadyVoided
	}
	if bill.Status != domain.BillStatusFinalized {
		return nil, domain.ErrBillNotFinalized
	}
	if bill.Version != expectedVersion {
		return nil, domain.ErrVersionConflict
	}

	return s.repo.VoidBill(ctx, repository.VoidBillParams{
		BillID:          billID,
		GroupID:         groupID,
		ExpectedVersion: expectedVersion,
		ActorMemberID:   member.ID,
		Reason:          trimmedReason,
	})
}

// DeleteDraftBill xóa một hóa đơn nháp và dọn dẹp ảnh trên Cloudinary (Spec 3 AC-13).
func (s *Service) DeleteDraftBill(ctx context.Context, callerUserID, billID, groupID uuid.UUID) error {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member == nil || member.Status != "active" {
		return domain.ErrForbidden
	}

	bill, err := s.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		return err
	}
	if member.Role != "captain" && member.ID != bill.CreditorMemberID {
		return domain.ErrForbidden
	}
	if bill.Status != domain.BillStatusDraft {
		return domain.ErrBillImmutable
	}

	imageKeys := make([]string, 0, len(bill.Images))
	for _, img := range bill.Images {
		imageKeys = append(imageKeys, img.ImageKey)
	}

	return s.repo.DeleteDraftBill(ctx, repository.DeleteDraftBillParams{
		ID:            billID,
		GroupID:       groupID,
		ActorMemberID: member.ID,
		ImageKeys:     imageKeys,
	})
}

// ============================================================================
// HELPER CONVERSIONS
// ============================================================================

func toAllocationInput(b *domain.Bill) AllocationInput {
	items := make([]ItemInput, 0, len(b.Items))
	for _, it := range b.Items {
		assigns := make([]ItemAssignmentInput, 0, len(it.Assignments))
		for _, a := range it.Assignments {
			assigns = append(assigns, ItemAssignmentInput{
				MemberID: a.MemberID,
				Weight:   parseWeightToScaledInt(a.Weight),
			})
		}

		items = append(items, ItemInput{
			ID: it.ID,
			// Dùng FinalPrice (giá đã trừ khuyến mãi riêng của món) chứ không phải LineTotal (giá gốc),
			// để khuyến mãi theo món chỉ có lợi cho đúng người được gán món đó (Spec 3 AC-17).
			LineTotal:   it.FinalPrice,
			Assignments: assigns,
		})
	}

	return AllocationInput{
		CreditorID:    b.CreditorMemberID,
		Subtotal:      b.Subtotal,
		ServiceCharge: b.ServiceCharge,
		VAT:           b.VAT,
		// Chỉ phân bổ giảm giá chung theo tỷ lệ; giảm giá riêng theo món đã nằm sẵn trong FinalPrice
		// ở trên nên không được cộng dồn lại ở đây (Spec 3 AC-17, AC-18).
		Discount: b.GeneralDiscount,
		Total:    b.Total,
		Items:    items,
	}
}

func parseWeightToScaledInt(weightStr string) int64 {
	w, err := strconv.ParseFloat(weightStr, 64)
	if err != nil || w <= 0 {
		return 10000 // default 1.0000
	}
	scaled := int64(math.Round(w * 10000))
	if scaled <= 0 {
		return 10000
	}
	return scaled
}

func computeLineTotal(unitPrice int64, quantityStr string) int64 {
	if unitPrice <= 0 {
		return 0
	}
	qty, err := strconv.ParseFloat(quantityStr, 64)
	if err != nil || qty <= 0 {
		qty = 1.0
	}
	totalFloat := float64(unitPrice) * qty
	if totalFloat > float64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(math.Round(totalFloat))
}

// CheckOrReserveIdempotency kiểm tra hoặc giữ chỗ khóa Idempotency trước khi thực hiện mutation (Spec 3 AC-1, AC-9).
func (s *Service) CheckOrReserveIdempotency(
	ctx context.Context,
	actorUserID uuid.UUID,
	operation, rawKey string,
	reqBody []byte,
) (*repository.IdempotencyRecord, error) {
	if strings.TrimSpace(rawKey) == "" {
		return nil, nil
	}
	keyHash := HashSHA256([]byte(rawKey))
	reqHash := HashSHA256(reqBody)
	opID := uuid.Must(uuid.NewV7())

	rec, err := s.repo.ReserveIdempotencyKey(ctx, repository.ReserveIdempotencyParams{
		ActorUserID:          actorUserID,
		Operation:            operation,
		KeyHash:              keyHash,
		CanonicalRequestHash: reqHash,
		OperationID:          opID,
		TTL:                  24 * time.Hour,
	})
	if err != nil {
		return nil, err
	}
	// Lớp chắn: repository không được phép trả về (nil, nil), nhưng nếu điều đó xảy ra thì báo lỗi
	// thay vì dereference con trỏ nil và làm sập tiến trình (Spec 3 AC-1, AC-9).
	if rec == nil {
		return nil, domain.ErrIdempotencyInProgress
	}

	if rec.CanonicalRequestHash != reqHash {
		return nil, domain.ErrIdempotencyKeyReused
	}

	if rec.State == "in_progress" && rec.OperationID != opID {
		return nil, domain.ErrIdempotencyInProgress
	}

	return rec, nil
}

// ReleaseIdempotency giải phóng một reservation "in_progress" khi mutation thất bại, để retry sau
// với cùng Idempotency-Key không bị kẹt 409 IDEMPOTENCY_IN_PROGRESS đến hết TTL 24h (Spec 3 AC-1, AC-9).
func (s *Service) ReleaseIdempotency(ctx context.Context, actorUserID uuid.UUID, operation, rawKey string) error {
	if strings.TrimSpace(rawKey) == "" {
		return nil
	}
	keyHash := HashSHA256([]byte(rawKey))
	return s.repo.ReleaseIdempotencyKey(ctx, actorUserID, operation, keyHash)
}

// CompleteIdempotency hoàn tất ghi nhận response cho khóa Idempotency.
func (s *Service) CompleteIdempotency(
	ctx context.Context,
	actorUserID uuid.UUID,
	operation, rawKey string,
	statusCode int,
	response any,
	resourceID *uuid.UUID,
) error {
	if strings.TrimSpace(rawKey) == "" {
		return nil
	}
	keyHash := HashSHA256([]byte(rawKey))
	respBytes, err := json.Marshal(response)
	if err != nil {
		return err
	}

	return s.repo.CompleteIdempotencyKey(ctx, repository.CompleteIdempotencyParams{
		ActorUserID:  actorUserID,
		Operation:    operation,
		KeyHash:      keyHash,
		ResponseCode: statusCode,
		ResponseBody: respBytes,
		ResourceID:   resourceID,
	})
}

// HashSHA256 tính mã băm SHA-256 dạng hex.
func HashSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
