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
	// Statuses lọc theo trạng thái hóa đơn. Rỗng nghĩa là "Tất cả" — giữ nguyên
	// hành vi cho client không gửi bộ lọc.
	Statuses []string
	// CallerMemberID là membership của người đang xem trong nhóm, dùng để lấy
	// phần tiền của chính họ trong từng hóa đơn.
	CallerMemberID uuid.UUID
}

// ListBillsCursorResult chứa kết quả phân trang cursor cho danh sách bill.
type ListBillsCursorResult struct {
	Bills      []*domain.BillListItem
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

// FinalizeBillParams chứa dữ liệu chốt hóa đơn và lưu snapshot phân bổ nợ chính xác.
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

// ReviewBillParams chứa dữ liệu chuyển hóa đơn nháp sang trạng thái đã đối soát.
// Notifications và BeforeCommit chạy trong cùng transaction với lần đổi trạng thái,
// nên không thể có chuyện thông báo "chờ chốt" đã gửi mà review lại bị rollback.
type ReviewBillParams struct {
	BillID           uuid.UUID
	GroupID          uuid.UUID
	ExpectedVersion  int32
	ReviewerMemberID uuid.UUID
	Notifications    []*NotificationParam
	BeforeCommit     func(ctx context.Context, tx pgx.Tx) error
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

// StartBulkFinalizeParams chứa tham số cho transaction mở batch chốt toàn bộ (Spec 0008 AC-4).
type StartBulkFinalizeParams struct {
	GroupID      uuid.UUID
	CallerUserID uuid.UUID
	BatchID      uuid.UUID
	// BeforeCommit chạy trong cùng transaction ngay trước commit, dùng để enqueue
	// các River job item và job thông báo mà không có lời gọi mạng nào giữ khóa nhóm.
	BeforeCommit func(ctx context.Context, tx pgx.Tx, info *BulkStartEnqueueInfo) error
}

// BulkStartEnqueueInfo liệt kê những gì cần enqueue sau khi batch đã được tạo:
// một job item cho từng bill captured và các job thông báo của batch rỗng.
type BulkStartEnqueueInfo struct {
	BatchID         uuid.UUID
	BillIDs         []uuid.UUID
	NotificationIDs []string
	Result          *StartBulkFinalizeResult
}

// StartBulkFinalizeResult là kết quả của transaction mở batch (Spec 0008 AC-4, bước 10).
type StartBulkFinalizeResult struct {
	Batch                   *domain.FinalizeBatch
	CapturedReviewedCount   int
	CapturedUnreviewedCount int
	SubmissionLockedNow     bool
	SubmissionLockedAt      time.Time
}

// LockSubmissionsResult là kết quả của hành động khóa gửi hóa đơn (Spec 0008 AC-1).
type LockSubmissionsResult struct {
	LockedAt  time.Time
	LockedNow bool
}

// IdempotencyRecord đại diện cho bản ghi quản lý tính lũy đẳng (Spec 3 AC-1, AC-9).
type IdempotencyRecord struct {
	ActorUserID          uuid.UUID
	Operation            string
	KeyHash              string
	CanonicalRequestHash string
	OperationID          uuid.UUID
	State                string // "in_progress", "completed"
	ResponseCode         int
	ResponseBody         []byte
	ResourceID           *uuid.UUID
	RetryAfter           *time.Time
	ExpiresAt            time.Time
	CreatedAt            time.Time
}

// ReserveIdempotencyParams tham số giữ chỗ trước khi thực hiện mutation.
type ReserveIdempotencyParams struct {
	ActorUserID          uuid.UUID
	Operation            string
	KeyHash              string
	CanonicalRequestHash string
	OperationID          uuid.UUID
	RetryAfter           *time.Time
	TTL                  time.Duration
}

// CompleteIdempotencyParams tham số ghi nhận kết quả hoàn thành mutation.
type CompleteIdempotencyParams struct {
	ActorUserID  uuid.UUID
	Operation    string
	KeyHash      string
	ResponseCode int
	ResponseBody []byte
	ResourceID   *uuid.UUID
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
	ListBillsByGroup(ctx context.Context, groupID uuid.UUID, limit, offset int32) ([]*domain.BillListItem, error)

	// ListBillsByGroupCursor lấy danh sách hóa đơn trong nhóm theo cursor pagination (created_at DESC, id DESC).
	ListBillsByGroupCursor(ctx context.Context, params ListBillsCursorParams) (*ListBillsCursorResult, error)

	// CountBillsByGroupStatus đếm hóa đơn của nhóm theo từng trạng thái.
	CountBillsByGroupStatus(ctx context.Context, groupID uuid.UUID) (map[string]int64, error)

	// UpdateDraftBill cập nhật hóa đơn draft và ghi đè danh sách món ăn trong 1 transaction có kiểm tra version.
	UpdateDraftBill(ctx context.Context, params UpdateDraftParams) (*domain.Bill, error)

	// ReviewBill chuyển trạng thái hóa đơn từ draft sang reviewed (Spec 3 AC-7).
	ReviewBill(ctx context.Context, p ReviewBillParams) (*domain.Bill, error)

	// FinalizeBill chuyển trạng thái hóa đơn sang finalized, lưu snapshot bill_shares và sinh debts (Spec 3 AC-9).
	FinalizeBill(ctx context.Context, params FinalizeBillParams) (*domain.Bill, error)

	// VoidBill chuyển trạng thái hóa đơn sang voided và hủy bỏ công nợ debts tương ứng (Spec 3 AC-10).
	VoidBill(ctx context.Context, params VoidBillParams) (*domain.Bill, error)

	// DeleteDraftBill xóa hoàn toàn một hóa đơn nháp và các dữ liệu liên quan trong 1 transaction.
	DeleteDraftBill(ctx context.Context, params DeleteDraftBillParams) error

	// OCR Jobs
	// beforeCommit chạy trong cùng transaction với việc insert job, dùng để enqueue River trong
	// cùng transaction (mirror CreateBill), tránh trường hợp insert thành công nhưng enqueue thất
	// bại để lại một job "queued" mồ côi mà không worker nào từng nhận (Spec 3 AC-2).
	CreateOCRJob(ctx context.Context, job *domain.OCRJob, beforeCommit func(ctx context.Context, tx pgx.Tx) error) (*domain.OCRJob, error)
	GetOCRJobByID(ctx context.Context, id uuid.UUID) (*domain.OCRJob, error)
	GetActiveOCRJobByBillID(ctx context.Context, billID uuid.UUID) (*domain.OCRJob, error)
	GetLatestOCRJobByBillID(ctx context.Context, billID uuid.UUID) (*domain.OCRJob, error)
	UpdateOCRJobProcessing(ctx context.Context, id uuid.UUID) error
	UpdateOCRJobSuccess(ctx context.Context, id uuid.UUID, candidate *domain.OCRCandidate, raw []byte) error
	UpdateOCRJobFailed(ctx context.Context, id uuid.UUID, errReason string) error
	CountManualOCRAttemptsInWindow(ctx context.Context, billID uuid.UUID, since time.Time) (int64, error)
	PurgeExpiredRawOCRResponses(ctx context.Context, olderThan time.Duration) (int64, error)
	CountActiveOCRJobs(ctx context.Context) (int64, error)
	// FailStaleOCRJobs đánh 'failed' cho các job đứng yên ở queued/processing quá
	// olderThan. River có thể bỏ một job lại (hết attempt vì lỗi ngoài bộ mã đóng,
	// worker panic, tiến trình chết giữa chừng) mà không ai chuyển ocr_jobs sang
	// trạng thái kết thúc; hàng đó nằm lại vĩnh viễn, giữ spinner "đang quét" trên
	// client và chiếm chỗ trong uq_ocr_jobs_active_bill nên chính bill đó không
	// bao giờ retry OCR được nữa.
	FailStaleOCRJobs(ctx context.Context, olderThan time.Duration) (int64, error)

	// Media Cleanup
	EnqueueMediaCleanup(ctx context.Context, prefix, kind string) error

	// Idempotency (Spec 3 AC-1, AC-9, AC-11, AC-13)
	ReserveIdempotencyKey(ctx context.Context, params ReserveIdempotencyParams) (*IdempotencyRecord, error)
	CompleteIdempotencyKey(ctx context.Context, params CompleteIdempotencyParams) error
	// CompleteIdempotencyKeyInTx ghi kết quả trong transaction mutation đang mở,
	// để resource và response idempotency cùng commit hoặc cùng rollback.
	CompleteIdempotencyKeyInTx(ctx context.Context, tx pgx.Tx, params CompleteIdempotencyParams) error
	GetIdempotencyKey(ctx context.Context, actorUserID uuid.UUID, operation, keyHash string) (*IdempotencyRecord, error)
	// ReleaseIdempotencyKey xóa một reservation "in_progress" khi mutation thất bại, để lần retry sau
	// với cùng key không bị kẹt 409 IDEMPOTENCY_IN_PROGRESS suốt 24h (Spec 3 AC-1, AC-9).
	ReleaseIdempotencyKey(ctx context.Context, actorUserID uuid.UUID, operation, keyHash string) error
	// PurgeExpiredIdempotencyKeys xóa các bản ghi idempotency đã hết hạn. Chạy định kỳ, nếu không
	// bảng phình vô hạn (Spec 3 AC-11, AC-13).
	PurgeExpiredIdempotencyKeys(ctx context.Context) (int64, error)

	// =========================================================================
	// GROUP BILL CLOSE V1 (Spec 0008)
	// =========================================================================

	// GetGroupSubmissionLock đọc trạng thái khóa gửi hóa đơn của nhóm đang active.
	// Nhóm không tồn tại hoặc đã archive trả về ErrBillNotFound (Spec 0008 Bill creation gate).
	GetGroupSubmissionLock(ctx context.Context, groupID uuid.UUID) (*time.Time, error)

	// LockSubmissions thực hiện trọn vẹn bước khóa trong một transaction:
	// khóa dòng nhóm, kiểm tra Captain, bật bill_submission_locked_at khi chưa có,
	// ghi activity bill_submission_locked khi khóa thay đổi.
	LockSubmissions(ctx context.Context, groupID, callerUserID uuid.UUID) (*LockSubmissionsResult, error)

	// UnlockSubmissions mở khóa gửi hóa đơn cho nhóm trong một transaction:
	// khóa dòng nhóm, kiểm tra Captain, xóa bill_submission_locked_at (về NULL),
	// ghi activity bill_submission_unlocked khi có thay đổi.
	UnlockSubmissions(ctx context.Context, groupID, callerUserID uuid.UUID) error

	// StartBulkFinalize mở batch chốt toàn bộ trong một transaction theo đúng trình tự
	// spec: khóa nhóm, kiểm tra Captain, bật khóa gửi hóa đơn, từ chối khi còn batch
	// queued/processing, capture các bill còn mở kèm version và review state, tạo batch
	// cùng item, ghi activity và hoàn tất ngay batch rỗng (Spec 0008 AC-4).
	StartBulkFinalize(ctx context.Context, p StartBulkFinalizeParams) (*StartBulkFinalizeResult, error)

	// GetFinalizeBatch đọc tóm tắt batch theo (batchID, groupID); không thấy trả về ErrBatchNotFound.
	GetFinalizeBatch(ctx context.Context, batchID, groupID uuid.UUID) (*domain.FinalizeBatch, error)

	// ListBatchItemsPage đọc kết quả item phân trang cursor theo (created_at, bill_id) tăng dần,
	// kèm tên hiển thị hiện tại của bill khi bill vẫn tồn tại (Spec 0008 AC-6).
	ListBatchItemsPage(ctx context.Context, batchID uuid.UUID, cursor *string, limit int32) ([]*domain.BatchItemResult, *string, error)

	// BeginTx mở một transaction dùng chung cho worker xử lý item để mọi thao tác
	// đọc ghi của một item nằm đúng một transaction (Spec 0008 Batch item transaction).
	BeginTx(ctx context.Context) (pgx.Tx, error)

	// LockActiveGroupInTx khóa dòng group active trước mọi batch, item và bill.
	LockActiveGroupInTx(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) error

	// LockBatchForItem khóa dòng batch trong transaction item; trả về trạng thái hiện tại.
	LockBatchForItem(ctx context.Context, tx pgx.Tx, batchID uuid.UUID) (*domain.FinalizeBatch, error)

	// PromoteBatchToProcessing chuyển batch queued sang processing trong transaction item;
	// batch đã processing hoặc completed là no-op an toàn khi retry (Spec 0008 AC-4).
	PromoteBatchToProcessing(ctx context.Context, tx pgx.Tx, batchID uuid.UUID) error

	// LockBatchItemForUpdate khóa dòng item và trả về trạng thái kèm dữ liệu capture;
	// không thấy trả về domain.ErrBatchNotFound (Spec 0008 Batch item transaction bước 2).
	LockBatchItemForUpdate(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID) (*domain.FinalizeBatchItem, error)

	// GetBillStateForUpdateInTx khóa dòng bill theo (billID, groupID) và trả về trạng thái cơ bản
	// phục vụ phân loại item; bill không còn tồn tại trả về domain.ErrBillNotFound (bước 3).
	GetBillStateForUpdateInTx(ctx context.Context, tx pgx.Tx, billID, groupID uuid.UUID) (*domain.Bill, error)

	// GetBillByIDInTx đọc đầy đủ bill kèm items và assignments bên trong transaction item,
	// sau khi bill đã được khóa bởi GetBillStateForUpdateInTx.
	GetBillByIDInTx(ctx context.Context, tx pgx.Tx, id, groupID uuid.UUID) (*domain.Bill, error)

	// ListActiveGroupMembersInTx đọc thành viên active trong nhóm bên trong transaction item.
	ListActiveGroupMembersInTx(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) ([]*GroupMember, error)

	// GetGroupMemberUserInTx đọc thông tin ngân hàng của một thành viên bên trong transaction item.
	GetGroupMemberUserInTx(ctx context.Context, tx pgx.Tx, memberID, groupID uuid.UUID) (*GroupMemberWithUser, error)

	// ApplyReviewInTx ghi nhận review cho version hiện tại của bill draft bên trong transaction
	// item; version không khớp trả về domain.ErrVersionConflict (Spec 0008 Batch item transaction bước 6).
	ApplyReviewInTx(ctx context.Context, tx pgx.Tx, billID, groupID uuid.UUID, expectedVersion int32, reviewerMemberID uuid.UUID) error

	// FinalizeBillInTx chạy phần ghi của finalize (bill, shares, debts, notifications,
	// activity finalized_bill) bên trong transaction item do caller sở hữu, tái dùng đúng
	// luật chốt sổ hiện có của Spec 3 (Spec 0008 Batch item transaction bước 6).
	FinalizeBillInTx(ctx context.Context, tx pgx.Tx, p FinalizeBillParams) (*domain.Bill, error)

	// MarkBatchItemFinalized đánh dấu item finalized và tăng finalized_count trong transaction item.
	MarkBatchItemFinalized(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID) error

	// MarkBatchItemFailed đánh dấu item failed với mã lỗi ổn định và tăng failed_count trong transaction item.
	MarkBatchItemFailed(ctx context.Context, tx pgx.Tx, batchID, billID uuid.UUID, errorCode string) error

	// RecordBatchItemFailure ghi nhận item failed trong một transaction ngắn riêng sau khi
	// transaction finalize chính đã rollback toàn bộ (Spec 0008 Batch item transaction bước 8).
	RecordBatchItemFailure(ctx context.Context, batchID, groupID, billID uuid.UUID, errorCode string) error

	// TryCompleteBatch khóa batch, và khi không còn item pending thì chuyển completed, đặt
	// mốc thời gian, đếm đối chiếu, ghi activity bill_bulk_finalize_completed và tạo thông báo
	// cho Captain active hiện tại. beforeCommit enqueue các job gửi thông báo trong cùng transaction.
	TryCompleteBatch(ctx context.Context, batchID, groupID uuid.UUID, beforeCommit func(ctx context.Context, tx pgx.Tx, notificationIDs []string) error) (completed bool, err error)

	// CountActiveBatches trả về số batch đang queued/processing của nhóm, dùng làm rào chắn archive.
	CountActiveBatches(ctx context.Context, groupID uuid.UUID) (int64, error)
}
