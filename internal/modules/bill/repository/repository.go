package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

// GroupMemberWithUser đại diện cho thông tin thành viên kèm thông tin user/ngân hàng.
type GroupMemberWithUser struct {
	ID                    uuid.UUID
	GroupID               uuid.UUID
	UserID                uuid.UUID
	Role                  string
	Status                string
	DefaultBankCode       *string
	DefaultBankAccountNum *string
	DefaultBankHolder     *string
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
	Bill         *domain.Bill
	Images       []*domain.BillImage
	Items        []*domain.BillItem
	OCRJob       *domain.OCRJob
	BeforeCommit func(ctx context.Context, tx pgx.Tx) error
}

// ListBillsCursorParams chứa tham số phân trang cursor cho danh sách bill.
type ListBillsCursorParams struct {
	GroupID uuid.UUID
	Cursor  *string
	Limit   int32
}

// ListBillsCursorResult chứa kết quả phân trang cursor cho danh sách bill.
type ListBillsCursorResult struct {
	Bills      []*domain.Bill
	NextCursor *string
}

// NotificationParam chứa dữ liệu tạo một bản ghi thông báo trong cùng transaction.
type NotificationParam struct {
	ID      uuid.UUID
	UserID  uuid.UUID
	Type    string
	Title   string
	Body    string
	Payload []byte
}

// UpdateDraftParams chứa dữ liệu để cập nhật thông tin một hóa đơn nháp (kèm ghi đè danh sách món).
type UpdateDraftParams struct {
	Bill            *domain.Bill
	Items           []*domain.BillItem
	ExpectedVersion int32
	ActorMemberID   uuid.UUID
}

// FinalizeBillParams chứa dữ liệu chốt hóa đơn và lưu snapshot phân bổ nợ Hamilton.
type FinalizeBillParams struct {
	BillID          uuid.UUID
	GroupID         uuid.UUID
	ExpectedVersion int32
	Shares          []*domain.BillShare
	Debts           []*Debt
	ActorMemberID   uuid.UUID
	Notifications   []*NotificationParam
	BeforeCommit    func(ctx context.Context, tx pgx.Tx) error
}

// VoidBillParams chứa dữ liệu để hủy bỏ một hóa đơn đã finalized.
type VoidBillParams struct {
	BillID          uuid.UUID
	GroupID         uuid.UUID
	ExpectedVersion int32
	ActorMemberID   uuid.UUID
	Reason          string
}

// DeleteDraftBillParams chứa tham số để xóa một hóa đơn nháp và dọn dẹp ảnh liên quan trong cùng transaction.
type DeleteDraftBillParams struct {
	ID            uuid.UUID
	GroupID       uuid.UUID
	ActorMemberID uuid.UUID
	ImageKeys     []string
}

// Repository định nghĩa các phương thức thao tác dữ liệu của module Bill & OCR.
type Repository interface {
	// Group Members
	GetGroupMember(ctx context.Context, groupID, userID uuid.UUID) (*GroupMember, error)
	GetGroupMemberUser(ctx context.Context, memberID, groupID uuid.UUID) (*GroupMemberWithUser, error)
	ListActiveGroupMembers(ctx context.Context, groupID uuid.UUID) ([]*GroupMember, error)

	// CreateBill lưu hóa đơn mới (kèm danh sách ảnh, món ăn, phân bổ và ocr job nếu có) trong 1 database transaction.
	CreateBill(ctx context.Context, params CreateBillParams) (*domain.Bill, error)

	// GetBillByID lấy thông tin chi tiết một hóa đơn (bao gồm cả images, items, assignments, shares).
	GetBillByID(ctx context.Context, id, groupID uuid.UUID) (*domain.Bill, error)

	// GetBillOnlyByID lấy thông tin cơ bản của hóa đơn chỉ bằng billID (dùng cho auth resolution).
	GetBillOnlyByID(ctx context.Context, id uuid.UUID) (*domain.Bill, error)

	// GetBillByIDForUpdate lấy thông tin hóa đơn và khóa dòng với SELECT ... FOR UPDATE (chống race condition).
	GetBillByIDForUpdate(ctx context.Context, id, groupID uuid.UUID) (*domain.Bill, error)

	// ListBillsByGroup lấy danh sách hóa đơn trong nhóm có phân trang offset (legacy).
	ListBillsByGroup(ctx context.Context, groupID uuid.UUID, limit, offset int32) ([]*domain.Bill, error)

	// ListBillsByGroupCursor lấy danh sách hóa đơn trong nhóm theo cursor pagination (created_at DESC, id DESC).
	ListBillsByGroupCursor(ctx context.Context, params ListBillsCursorParams) (*ListBillsCursorResult, error)

	// UpdateDraftBill cập nhật hóa đơn draft và ghi đè danh sách món ăn trong 1 transaction có kiểm tra version.
	UpdateDraftBill(ctx context.Context, params UpdateDraftParams) (*domain.Bill, error)

	// ReviewBill chuyển trạng thái hóa đơn từ draft sang reviewed (Spec 3 AC-7).
	ReviewBill(ctx context.Context, id, groupID uuid.UUID, expectedVersion int32, reviewerMemberID uuid.UUID) (*domain.Bill, error)

	// FinalizeBill chuyển trạng thái hóa đơn sang finalized, lưu snapshot bill_shares và sinh debts (Spec 3 AC-9).
	FinalizeBill(ctx context.Context, params FinalizeBillParams) (*domain.Bill, error)

	// VoidBill chuyển trạng thái hóa đơn sang voided và hủy bỏ công nợ debts tương ứng (Spec 3 AC-10).
	VoidBill(ctx context.Context, params VoidBillParams) (*domain.Bill, error)

	// DeleteDraftBill xóa hoàn toàn một hóa đơn nháp và các dữ liệu liên quan trong 1 transaction.
	DeleteDraftBill(ctx context.Context, params DeleteDraftBillParams) error

	// OCR Jobs
	CreateOCRJob(ctx context.Context, job *domain.OCRJob) (*domain.OCRJob, error)
	GetOCRJobByID(ctx context.Context, id uuid.UUID) (*domain.OCRJob, error)
	GetActiveOCRJobByBillID(ctx context.Context, billID uuid.UUID) (*domain.OCRJob, error)
	GetLatestOCRJobByBillID(ctx context.Context, billID uuid.UUID) (*domain.OCRJob, error)
	UpdateOCRJobProcessing(ctx context.Context, id uuid.UUID) error
	UpdateOCRJobSuccess(ctx context.Context, id uuid.UUID, candidate *domain.OCRCandidate, raw []byte) error
	UpdateOCRJobFailed(ctx context.Context, id uuid.UUID, errReason string) error
	CountManualOCRAttemptsInWindow(ctx context.Context, billID uuid.UUID, since time.Time) (int64, error)

	// Media Cleanup
	EnqueueMediaCleanup(ctx context.Context, prefix, kind string) error
}
