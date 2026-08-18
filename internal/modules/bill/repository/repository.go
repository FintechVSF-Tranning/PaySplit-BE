package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"paysplit-backend/internal/modules/bill/domain"
)

// GroupMember đại diện cho thông tin thành viên trong nhóm.
type GroupMember struct {
	ID      uuid.UUID
	GroupID uuid.UUID
	UserID  uuid.UUID
	Role    string
	Status  string
}

// Debt đại diện cho một bản ghi công nợ sinh ra sau khi Finalize bill.
type Debt struct {
	ID               uuid.UUID
	GroupID          uuid.UUID
	BillID           uuid.UUID
	DebtorMemberID   uuid.UUID
	CreditorMemberID uuid.UUID
	Amount           int64
	Status           string
}

// CreateBillParams chứa dữ liệu để tạo mới một hóa đơn (kèm ảnh, món ăn và job OCR nếu có) trong một transaction.
type CreateBillParams struct {
	Bill   *domain.Bill
	Images []*domain.BillImage
	Items  []*domain.BillItem
	OCRJob *domain.OCRJob
}

// UpdateDraftParams chứa dữ liệu để cập nhật thông tin một hóa đơn nháp (kèm ghi đè danh sách món).
type UpdateDraftParams struct {
	Bill            *domain.Bill
	Items           []*domain.BillItem
	ExpectedVersion int32
}

// FinalizeBillParams chứa dữ liệu chốt hóa đơn và lưu snapshot phân bổ nợ Hamilton.
type FinalizeBillParams struct {
	BillID          uuid.UUID
	GroupID         uuid.UUID
	ExpectedVersion int32
	Shares          []*domain.BillShare
	Debts           []*Debt
	ActorMemberID   uuid.UUID
}

// VoidBillParams chứa dữ liệu để hủy bỏ một hóa đơn đã finalized.
type VoidBillParams struct {
	BillID          uuid.UUID
	GroupID         uuid.UUID
	ExpectedVersion int32
	ActorMemberID   uuid.UUID
	Reason          string
}

// Repository định nghĩa các phương thức thao tác dữ liệu của module Bill & OCR.
type Repository interface {
	// Group Members
	GetGroupMember(ctx context.Context, groupID, userID uuid.UUID) (*GroupMember, error)
	ListActiveGroupMembers(ctx context.Context, groupID uuid.UUID) ([]*GroupMember, error)

	// CreateBill lưu hóa đơn mới (kèm danh sách ảnh, món ăn, phân bổ và ocr job nếu có) trong 1 database transaction.
	CreateBill(ctx context.Context, params CreateBillParams) (*domain.Bill, error)

	// GetBillByID lấy thông tin chi tiết một hóa đơn (bao gồm cả images, items, assignments, shares).
	GetBillByID(ctx context.Context, id, groupID uuid.UUID) (*domain.Bill, error)

	// GetBillByIDForUpdate lấy thông tin hóa đơn và khóa dòng với SELECT ... FOR UPDATE (chống race condition).
	GetBillByIDForUpdate(ctx context.Context, id, groupID uuid.UUID) (*domain.Bill, error)

	// ListBillsByGroup lấy danh sách hóa đơn trong nhóm có phân trang (mới nhất trước).
	ListBillsByGroup(ctx context.Context, groupID uuid.UUID, limit, offset int32) ([]*domain.Bill, error)

	// UpdateDraftBill cập nhật hóa đơn draft và ghi đè danh sách món ăn trong 1 transaction có kiểm tra version.
	UpdateDraftBill(ctx context.Context, params UpdateDraftParams) (*domain.Bill, error)

	// ReviewBill chuyển trạng thái hóa đơn từ draft sang reviewed (Spec 3 AC-7).
	ReviewBill(ctx context.Context, id, groupID uuid.UUID, expectedVersion int32) (*domain.Bill, error)

	// FinalizeBill chuyển trạng thái hóa đơn sang finalized, lưu snapshot bill_shares và sinh debts (Spec 3 AC-9).
	FinalizeBill(ctx context.Context, params FinalizeBillParams) (*domain.Bill, error)

	// VoidBill chuyển trạng thái hóa đơn sang voided và hủy bỏ công nợ debts tương ứng (Spec 3 AC-10).
	VoidBill(ctx context.Context, params VoidBillParams) (*domain.Bill, error)

	// DeleteDraftBill xóa hoàn toàn một hóa đơn nháp và các dữ liệu liên quan.
	DeleteDraftBill(ctx context.Context, id, groupID uuid.UUID) error

	// OCR Jobs
	CreateOCRJob(ctx context.Context, job *domain.OCRJob) (*domain.OCRJob, error)
	GetOCRJobByID(ctx context.Context, id uuid.UUID) (*domain.OCRJob, error)
	GetActiveOCRJobByBillID(ctx context.Context, billID uuid.UUID) (*domain.OCRJob, error)
	GetLatestOCRJobByBillID(ctx context.Context, billID uuid.UUID) (*domain.OCRJob, error)
	UpdateOCRJobProcessing(ctx context.Context, id uuid.UUID, version int32) error
	UpdateOCRJobSuccess(ctx context.Context, id uuid.UUID, version int32, candidate *domain.OCRCandidate, raw []byte) error
	UpdateOCRJobFailed(ctx context.Context, id uuid.UUID, version int32, errReason string) error
	CountManualOCRAttemptsInWindow(ctx context.Context, billID uuid.UUID, since time.Time) (int64, error)

	// Media Cleanup
	EnqueueMediaCleanup(ctx context.Context, prefix, kind string) error
}
