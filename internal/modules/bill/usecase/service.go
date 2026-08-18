package usecase

import (
	"context"
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

// JobEnqueuer là interface đẩy công việc OCR vào hàng đợi River Queue.
type JobEnqueuer interface {
	EnqueueOCRJobTx(ctx context.Context, tx pgx.Tx, billID, jobID, groupID uuid.UUID) error
	EnqueueOCRJob(ctx context.Context, billID, jobID, groupID uuid.UUID) error
}

// Service quản lý toàn bộ nghiệp vụ của module Bill và OCR.
type Service struct {
	repo        repository.Repository
	ocrProvider OCRProvider
	storage     BillStorage
	processor   ReceiptProcessor
	enqueuer    JobEnqueuer
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
		repo:        repo,
		ocrProvider: ocrProvider,
		storage:     storage,
		processor:   processor,
		enqueuer:    enqueuer,
	}
}

// ============================================================================
// DTOs (Data Transfer Objects)
// ============================================================================

type CreateBillItemRequest struct {
	Name        string                        `json:"name"`
	Quantity    string                        `json:"quantity"`
	UnitPrice   int64                         `json:"unit_price"`
	LineTotal   int64                         `json:"line_total"`
	Assignments []CreateItemAssignmentRequest `json:"assignments"`
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
	if err != nil || member.Status != "active" {
		return nil, domain.ErrInvalidInput
	}

	if len(req.Files) > 5 {
		return nil, fmt.Errorf("%w: maximum 5 receipt images allowed", domain.ErrInvalidInput)
	}

	billID := uuid.New()
	operationID := uuid.New()

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
			ID:       uuid.New(),
			BillID:   billID,
			GroupID:  req.GroupID,
			ImageKey: uploadedKey,
			Position: int16(i),
		})
	}

	// 3. Chuẩn bị danh sách món ăn
	items := make([]*domain.BillItem, 0, len(req.Items))
	for i, it := range req.Items {
		itemID := uuid.New()
		lineTotal := it.LineTotal
		if lineTotal <= 0 && it.UnitPrice > 0 {
			lineTotal = computeLineTotal(it.UnitPrice, it.Quantity)
		}

		domItem := &domain.BillItem{
			ID:        itemID,
			BillID:    billID,
			GroupID:   req.GroupID,
			Name:      it.Name,
			Quantity:  it.Quantity,
			UnitPrice: it.UnitPrice,
			LineTotal: lineTotal,
			Position:  int16(i),
		}

		for _, assign := range it.Assignments {
			domItem.Assignments = append(domItem.Assignments, &domain.BillItemAssignment{
				ID:         uuid.New(),
				BillItemID: itemID,
				GroupID:    req.GroupID,
				MemberID:   assign.MemberID,
				Weight:     assign.Weight,
			})
		}
		items = append(items, domItem)
	}

	// 4. Tạo OCR Job nếu có ảnh
	var ocrJob *domain.OCRJob
	if len(images) > 0 {
		ocrJob = &domain.OCRJob{
			ID:       uuid.New(),
			BillID:   billID,
			Status:   domain.OCRJobStatusQueued,
			Provider: "llamaextract",
		}
	}

	bill := &domain.Bill{
		ID:               billID,
		GroupID:          req.GroupID,
		CreditorMemberID: member.ID,
		Status:           domain.BillStatusDraft,
		MerchantName:     req.MerchantName,
		BillDate:         req.BillDate,
		Subtotal:         req.Subtotal,
		ServiceCharge:    req.ServiceCharge,
		VAT:              req.VAT,
		Discount:         req.Discount,
		Total:            req.Total,
		SplitMethod:      req.SplitMethod,
		ReplacesBillID:   req.ReplacesBillID,
	}

	// 5. Lưu hóa đơn vào Database
	createdBill, err := s.repo.CreateBill(ctx, repository.CreateBillParams{
		Bill:   bill,
		Images: images,
		Items:  items,
		OCRJob: ocrJob,
	})
	if err != nil {
		return nil, fmt.Errorf("create bill in repo: %w", err)
	}

	// 6. Đẩy River OCR Job nếu có ảnh
	if ocrJob != nil && s.enqueuer != nil {
		if err := s.enqueuer.EnqueueOCRJob(ctx, billID, ocrJob.ID, req.GroupID); err != nil {
			return nil, fmt.Errorf("enqueue ocr job: %w", err)
		}
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
	if err != nil || member.Status != "active" {
		return nil, domain.ErrInvalidInput
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

	// Tính toán provisional breakdown nếu bill đang ở trạng thái draft/reviewed
	var breakdown []*MemberAllocation
	if bill.Status == domain.BillStatusDraft || bill.Status == domain.BillStatusReviewed {
		allocInput := toAllocationInput(bill)
		breakdown, _ = CalculateHamiltonAllocation(allocInput)
	}

	return &BillDetailResponse{
		Bill:       bill,
		Breakdown:  breakdown,
		SignedURLs: signedURLs,
	}, nil
}

// ListBills lấy danh sách hóa đơn trong nhóm có phân trang.
func (s *Service) ListBills(ctx context.Context, callerUserID, groupID uuid.UUID, limit, offset int32) ([]*domain.Bill, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member.Status != "active" {
		return nil, domain.ErrInvalidInput
	}

	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	return s.repo.ListBillsByGroup(ctx, groupID, limit, offset)
}

// RetryOCR kích hoạt lại quá trình bóc tách OCR thủ công (tối đa 5 lần / 24h, Spec 3 AC-2).
func (s *Service) RetryOCR(ctx context.Context, callerUserID, billID, groupID uuid.UUID) (*domain.OCRJob, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member.Status != "active" {
		return nil, domain.ErrInvalidInput
	}

	bill, err := s.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		return nil, err
	}

	// Chỉ cho phép retry khi bill là draft và có ảnh
	if bill.Status != domain.BillStatusDraft {
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

	// Kiểm tra giới hạn retry thủ công (tối đa 5 lần trong 24h)
	attempts, err := s.repo.CountManualOCRAttemptsInWindow(ctx, billID, time.Now().Add(-24*time.Hour))
	if err == nil && attempts >= 5 {
		return nil, domain.ErrOcrLimitReached
	}

	// Tạo job OCR mới và enqueue vào River Queue
	newJob := &domain.OCRJob{
		ID:       uuid.New(),
		BillID:   billID,
		Status:   domain.OCRJobStatusQueued,
		Provider: "llamaextract",
	}

	createdJob, err := s.repo.CreateOCRJob(ctx, newJob)
	if err != nil {
		return nil, fmt.Errorf("create retry ocr job: %w", err)
	}

	if s.enqueuer != nil {
		if err := s.enqueuer.EnqueueOCRJob(ctx, billID, createdJob.ID, groupID); err != nil {
			return nil, fmt.Errorf("enqueue retry ocr job: %w", err)
		}
	}

	return createdJob, nil
}

// ApplyCandidate đổ dữ liệu bóc tách từ AI vào hóa đơn nháp (Spec 3 AC-4).
func (s *Service) ApplyCandidate(ctx context.Context, callerUserID, billID, groupID, jobID uuid.UUID, expectedVersion int32) (*domain.Bill, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member.Status != "active" {
		return nil, domain.ErrInvalidInput
	}

	bill, err := s.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		return nil, err
	}
	if member.Role != "captain" && member.ID != bill.CreditorMemberID {
		return nil, domain.ErrForbidden
	}
	if bill.Status != domain.BillStatusDraft {
		return nil, domain.ErrBillImmutable
	}

	ocrJob, err := s.repo.GetOCRJobByID(ctx, jobID)
	if err != nil {
		return nil, domain.ErrOcrJobNotFound
	}
	if ocrJob.Status != domain.OCRJobStatusSucceeded || ocrJob.Candidate == nil {
		return nil, domain.ErrOcrNotReady
	}
	if bill.Version != expectedVersion {
		return nil, domain.ErrVersionConflict
	}
	if bill.Version != ocrJob.Version {
		return nil, domain.ErrOcrResultStale
	}

	// Lấy danh sách thành viên active trong nhóm để chia đều các món bóc tách
	activeMembers, err := s.repo.ListActiveGroupMembers(ctx, groupID)
	if err != nil || len(activeMembers) == 0 {
		return nil, errors.New("no active members to assign")
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
	bill.Total = candidate.Total

	// Tạo items từ Candidate
	items := make([]*domain.BillItem, 0, len(candidate.Items))
	for i, cItem := range candidate.Items {
		itemID := uuid.New()
		domItem := &domain.BillItem{
			ID:        itemID,
			BillID:    billID,
			GroupID:   groupID,
			Name:      cItem.Name,
			Quantity:  cItem.Quantity,
			UnitPrice: cItem.UnitPrice,
			LineTotal: cItem.LineTotal,
			Position:  int16(i),
		}

		// Gán chia đều mặc định cho toàn bộ thành viên nhóm
		for _, m := range activeMembers {
			domItem.Assignments = append(domItem.Assignments, &domain.BillItemAssignment{
				ID:         uuid.New(),
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
	})
}

// UpdateDraftBill cập nhật chỉnh sửa hóa đơn nháp (Spec 3 AC-5).
func (s *Service) UpdateDraftBill(ctx context.Context, callerUserID, billID, groupID uuid.UUID, req UpdateDraftRequest) (*domain.Bill, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member.Status != "active" {
		return nil, domain.ErrInvalidInput
	}

	bill, err := s.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		return nil, err
	}
	if member.Role != "captain" && member.ID != bill.CreditorMemberID {
		return nil, domain.ErrForbidden
	}
	if bill.Status != domain.BillStatusDraft {
		return nil, domain.ErrBillImmutable
	}

	bill.MerchantName = req.MerchantName
	bill.BillDate = req.BillDate
	bill.Subtotal = req.Subtotal
	bill.ServiceCharge = req.ServiceCharge
	bill.VAT = req.VAT
	bill.Discount = req.Discount
	bill.Total = req.Total
	bill.SplitMethod = req.SplitMethod

	items := make([]*domain.BillItem, 0, len(req.Items))
	for i, it := range req.Items {
		itemID := uuid.New()
		lineTotal := it.LineTotal
		if lineTotal <= 0 && it.UnitPrice > 0 {
			lineTotal = computeLineTotal(it.UnitPrice, it.Quantity)
		}

		domItem := &domain.BillItem{
			ID:        itemID,
			BillID:    billID,
			GroupID:   groupID,
			Name:      it.Name,
			Quantity:  it.Quantity,
			UnitPrice: it.UnitPrice,
			LineTotal: lineTotal,
			Position:  int16(i),
		}

		for _, assign := range it.Assignments {
			domItem.Assignments = append(domItem.Assignments, &domain.BillItemAssignment{
				ID:         uuid.New(),
				BillItemID: itemID,
				GroupID:    groupID,
				MemberID:   assign.MemberID,
				Weight:     assign.Weight,
			})
		}
		items = append(items, domItem)
	}

	return s.repo.UpdateDraftBill(ctx, repository.UpdateDraftParams{
		Bill:            bill,
		Items:           items,
		ExpectedVersion: req.Version,
	})
}

// ReviewBill kiểm tra điều kiện đối soát và chuyển hóa đơn sang trạng thái reviewed (Spec 3 AC-7).
func (s *Service) ReviewBill(ctx context.Context, callerUserID, billID, groupID uuid.UUID, expectedVersion int32) (*domain.Bill, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member.Status != "active" {
		return nil, domain.ErrInvalidInput
	}

	bill, err := s.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		return nil, err
	}
	if member.Role != "captain" && member.ID != bill.CreditorMemberID {
		return nil, domain.ErrForbidden
	}
	if bill.Status != domain.BillStatusDraft {
		return nil, domain.ErrBillImmutable
	}

	// Kiểm tra đối soát tổng tiền
	var sumItems int64
	for _, it := range bill.Items {
		sumItems += it.LineTotal
		if len(it.Assignments) == 0 {
			return nil, errors.New("every item must have at least one assignee before review")
		}
	}

	expectedTotal := bill.Subtotal + bill.ServiceCharge + bill.VAT - bill.Discount
	if bill.Subtotal != sumItems || bill.Total != expectedTotal {
		return nil, errors.New("bill totals do not reconcile (subtotal or total mismatch)")
	}

	return s.repo.ReviewBill(ctx, billID, groupID, expectedVersion)
}

// FinalizeBill chạy giải thuật Hamilton allocation, lưu snapshot bill_shares và sinh debts (Spec 3 AC-9).
func (s *Service) FinalizeBill(ctx context.Context, callerUserID, billID, groupID uuid.UUID, expectedVersion int32) (*domain.Bill, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member.Status != "active" {
		return nil, domain.ErrInvalidInput
	}

	bill, err := s.repo.GetBillByID(ctx, billID, groupID)
	if err != nil {
		return nil, err
	}
	if member.Role != "captain" {
		return nil, domain.ErrForbidden
	}
	if bill.Status != domain.BillStatusReviewed {
		if bill.Status == domain.BillStatusFinalized {
			return nil, domain.ErrBillImmutable
		}
		return nil, domain.ErrReviewRequired
	}

	// Chạy giải thuật Hamilton
	allocInput := toAllocationInput(bill)
	allocations, err := CalculateHamiltonAllocation(allocInput)
	if err != nil {
		return nil, fmt.Errorf("calculate hamilton allocation: %w", err)
	}

	shares := make([]*domain.BillShare, 0, len(allocations))
	debts := make([]*repository.Debt, 0, len(allocations))

	for _, a := range allocations {
		shares = append(shares, &domain.BillShare{
			ID:             uuid.New(),
			BillID:         billID,
			GroupID:        groupID,
			MemberID:       a.MemberID,
			ComputedAmount: a.FinalAmount,
		})

		// Nếu không phải Creditor và có số tiền nợ > 0 -> tạo debt
		if a.MemberID != bill.CreditorMemberID && a.FinalAmount > 0 {
			debts = append(debts, &repository.Debt{
				ID:               uuid.New(),
				GroupID:          groupID,
				BillID:           billID,
				DebtorMemberID:   a.MemberID,
				CreditorMemberID: bill.CreditorMemberID,
				Amount:           a.FinalAmount,
				Status:           "awaiting",
			})
		}
	}

	return s.repo.FinalizeBill(ctx, repository.FinalizeBillParams{
		BillID:          billID,
		GroupID:         groupID,
		ExpectedVersion: expectedVersion,
		Shares:          shares,
		Debts:           debts,
		ActorMemberID:   member.ID,
	})
}

// VoidBill hủy bỏ hóa đơn đã chốt và đóng các khoản nợ liên quan (Spec 3 AC-10).
func (s *Service) VoidBill(ctx context.Context, callerUserID, billID, groupID uuid.UUID, expectedVersion int32, reason string) (*domain.Bill, error) {
	member, err := s.repo.GetGroupMember(ctx, groupID, callerUserID)
	if err != nil || member.Status != "active" {
		return nil, domain.ErrInvalidInput
	}
	if member.Role != "captain" {
		return nil, domain.ErrForbidden
	}

	trimmedReason := strings.TrimSpace(reason)
	if trimmedReason == "" || len(trimmedReason) > 500 {
		return nil, errors.New("void reason must be between 1 and 500 characters")
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
	if err != nil || member.Status != "active" {
		return domain.ErrInvalidInput
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

	// Đẩy công việc dọn dẹp ảnh Cloudinary vào media cleanup jobs
	for _, img := range bill.Images {
		_ = s.repo.EnqueueMediaCleanup(ctx, img.ImageKey, "bill_image")
	}

	return s.repo.DeleteDraftBill(ctx, billID, groupID)
}

// ============================================================================
// HELPER CONVERSIONS
// ============================================================================

func toAllocationInput(b *domain.Bill) AllocationInput {
	items := make([]ItemInput, 0, len(b.Items))
	for _, it := range b.Items {
		assigns := make([]ItemAssignmentInput, 0, len(it.Assignments))
		totalWeight := 0.0
		for _, a := range it.Assignments {
			w, _ := strconv.ParseFloat(a.Weight, 64)
			if w <= 0 {
				w = 1.0
			}
			totalWeight += w
		}
		if totalWeight <= 0 {
			totalWeight = 1.0
		}

		for _, a := range it.Assignments {
			w, _ := strconv.ParseFloat(a.Weight, 64)
			if w <= 0 {
				w = 1.0
			}
			assigns = append(assigns, ItemAssignmentInput{
				MemberID: a.MemberID,
				Ratio:    w / totalWeight,
			})
		}

		items = append(items, ItemInput{
			ID:          it.ID,
			LineTotal:   it.LineTotal,
			Assignments: assigns,
		})
	}

	return AllocationInput{
		CreditorID:    b.CreditorMemberID,
		Subtotal:      b.Subtotal,
		ServiceCharge: b.ServiceCharge,
		VAT:           b.VAT,
		Discount:      b.Discount,
		Total:         b.Total,
		Items:         items,
	}
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
