package domain

import (
	"time"

	"github.com/google/uuid"
)

// Trạng thái batch chốt toàn bộ (Spec 0008 Data model, State transitions).
const (
	BatchStatusQueued     = "queued"
	BatchStatusProcessing = "processing"
	BatchStatusCompleted  = "completed"
)

// Trạng thái item trong batch (Spec 0008 Data model).
const (
	BatchItemPending   = "pending"
	BatchItemFinalized = "finalized"
	BatchItemFailed    = "failed"
)

// Mã lỗi ổn định của item, chỉ chứa định danh và kết quả, không bao giờ chứa
// tên merchant, nội dung món ăn, dữ liệu ảnh, dữ liệu ngân hàng hay output nhà
// cung cấp (Spec 0008 Security model 7).
const (
	ItemErrorDeleted         = "BILL_DELETED"
	ItemErrorVersionConflict = "VERSION_CONFLICT"
	ItemErrorVoided          = "BILL_ALREADY_VOIDED"
	ItemErrorNotReady        = "BILL_NOT_READY"
	ItemErrorDiscount        = "DISCOUNT_NOT_ALLOCATABLE"
	ItemErrorBankRequired    = "BANK_ACCOUNT_REQUIRED"
	ItemErrorInternalFailed  = "BILL_FINALIZE_FAILED"
)

// FinalizeBatch là một lần "Chốt toàn bộ" của Captain: một snapshot cố định
// các bill còn mở kèm version và trạng thái review tại thời điểm capture.
type FinalizeBatch struct {
	ID                  string     `json:"id"`
	GroupID             string     `json:"group_id"`
	RequestedByMemberID string     `json:"requested_by_member_id"`
	Status              string     `json:"status"`
	TargetCount         int        `json:"target_count"`
	FinalizedCount      int        `json:"finalized_count"`
	FailedCount         int        `json:"failed_count"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
}

// FinalizeBatchItem là kết quả xử lý một bill đã được capture trong batch.
type FinalizeBatchItem struct {
	BillID           string     `json:"bill_id"`
	BillVersion      int32      `json:"bill_version"`
	CapturedReviewed bool       `json:"captured_reviewed"`
	Status           string     `json:"status"`
	ErrorCode        *string    `json:"error_code,omitempty"`
	ProcessedAt      *time.Time `json:"processed_at,omitempty"`
	CreatedAt        time.Time  `json:"-"`
}

// CapturedBill là một bill còn mở tại thời điểm capture: ID bất biến, version
// hiện tại và việc version đó đã được review hay chưa (Spec 0008 AC-4).
type CapturedBill struct {
	BillID           uuid.UUID
	Version          int32
	CapturedReviewed bool
}

// BatchItemResult là một dòng kết quả trả về cho GET batch detail, kèm tên
// hiển thị hiện tại của bill nếu bill vẫn còn tồn tại; bill đã bị xóa hard
// delete trả về null thay vì giữ lại nội dung cũ (Spec 0008 Value sourcing).
type BatchItemResult struct {
	FinalizeBatchItem
	BillDisplayName *string `json:"bill_display_name"`
}
