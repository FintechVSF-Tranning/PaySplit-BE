package domain

import (
	"time"

	"github.com/google/uuid"
)

// BillStatus đại diện cho các trạng thái trong vòng đời hóa đơn (Spec 3 AC-1, AC-7, AC-9, AC-10).
type BillStatus string

const (
	BillStatusDraft     BillStatus = "draft"
	BillStatusReviewed  BillStatus = "reviewed"
	BillStatusFinalized BillStatus = "finalized"
	BillStatusVoided    BillStatus = "voided"
)

// SplitMethod đại diện cho phương thức phân bổ tiền (Spec 3 AC-4, AC-5).
type SplitMethod string

const (
	SplitMethodEven       SplitMethod = "even"
	SplitMethodItemRatio  SplitMethod = "item_ratio"
	SplitMethodExact      SplitMethod = "exact"
	SplitMethodShares     SplitMethod = "shares"
	SplitMethodPercentage SplitMethod = "percentage"
)

// OCRJobStatus đại diện cho trạng thái của tiến trình bóc tách OCR (Spec 3 AC-2).
type OCRJobStatus string

const (
	OCRJobStatusQueued     OCRJobStatus = "queued"
	OCRJobStatusProcessing OCRJobStatus = "processing"
	OCRJobStatusSucceeded  OCRJobStatus = "succeeded"
	OCRJobStatusFailed     OCRJobStatus = "failed"
)

// Bill là thực thể hóa đơn chính trong hệ thống.
type Bill struct {
	ID                 uuid.UUID   `json:"id"`
	GroupID            uuid.UUID   `json:"group_id"`
	CreditorMemberID   uuid.UUID   `json:"creditor_member_id"`
	Status             BillStatus  `json:"status"`
	MerchantName       *string     `json:"merchant_name,omitempty"`
	BillDate           *time.Time  `json:"bill_date,omitempty"`
	Subtotal           int64       `json:"subtotal"`
	ServiceCharge      int64       `json:"service_charge"`
	VAT                int64       `json:"vat"`
	Discount           int64       `json:"discount"`
	Total              int64       `json:"total"`
	SplitMethod        SplitMethod `json:"split_method"`
	MismatchCodes      []string    `json:"mismatch_codes"`
	ReplacesBillID     *uuid.UUID  `json:"replaces_bill_id,omitempty"`
	Version            int32       `json:"version"`
	FinalizedAt        *time.Time  `json:"finalized_at,omitempty"`
	VoidedAt           *time.Time  `json:"voided_at,omitempty"`
	ReviewedAt         *time.Time  `json:"reviewed_at,omitempty"`
	ReviewedByMemberID *uuid.UUID  `json:"reviewed_by_member_id,omitempty"`
	CreatedAt          time.Time   `json:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at"`

	// Quan hệ (populated khi load chi tiết)
	Images []*BillImage `json:"images,omitempty"`
	Items  []*BillItem  `json:"items,omitempty"`
	Shares []*BillShare `json:"shares,omitempty"`
}

// BillImage đại diện cho một ảnh chụp hóa đơn được lưu trên Cloudinary (Spec 3 AC-1).
type BillImage struct {
	ID        uuid.UUID `json:"id"`
	BillID    uuid.UUID `json:"bill_id"`
	GroupID   uuid.UUID `json:"group_id"`
	ImageKey  string    `json:"image_key"` // Cloudinary Public ID: "bills/{op_id}/{position}"
	Position  int16     `json:"position"`  // 0 đến 4
	CreatedAt time.Time `json:"created_at"`
}

// BillItem đại diện cho một món hoặc dịch vụ trong hóa đơn.
type BillItem struct {
	ID          uuid.UUID             `json:"id"`
	BillID      uuid.UUID             `json:"bill_id"`
	GroupID     uuid.UUID             `json:"group_id"`
	Name        string                `json:"name"`
	Quantity    string                `json:"quantity"` // Chuỗi số thập phân, ví dụ "1", "2.5"
	UnitPrice   int64                 `json:"unit_price"`
	LineTotal   int64                 `json:"line_total"`
	Position    int16                 `json:"position"`
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
	Assignments []*BillItemAssignment `json:"assignments,omitempty"`
}

// BillItemAssignment ghi nhận thành viên gánh món nào và tỷ trọng/portions (Spec 3 AC-5).
type BillItemAssignment struct {
	ID         uuid.UUID `json:"id"`
	BillItemID uuid.UUID `json:"bill_item_id"`
	GroupID    uuid.UUID `json:"group_id"`
	MemberID   uuid.UUID `json:"member_id"`
	Weight     string    `json:"weight"` // Trọng số, mặc định "1.0000"
	CreatedAt  time.Time `json:"created_at"`
}

// BillShare là snapshot số tiền nợ chi tiết của thành viên sau khi Hamilton allocation (Spec 3 AC-9).
type BillShare struct {
	ID                 uuid.UUID `json:"id"`
	BillID             uuid.UUID `json:"bill_id"`
	GroupID            uuid.UUID `json:"group_id"`
	MemberID           uuid.UUID `json:"member_id"`
	ItemSubtotal       int64     `json:"item_subtotal"`
	ServiceChargeShare int64     `json:"service_charge_share"`
	VATShare           int64     `json:"vat_share"`
	DiscountShare      int64     `json:"discount_share"`
	RoundingAdjustment int64     `json:"rounding_adjustment"`
	FinalAmount        int64     `json:"final_amount"`
	ComputedAmount     int64     `json:"computed_amount,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

// OCRJob theo dõi trạng thái tiến trình bóc tách ảnh bằng AI (Spec 3 AC-2).
type OCRJob struct {
	ID           uuid.UUID     `json:"id"`
	BillID       uuid.UUID     `json:"bill_id"`
	Status       OCRJobStatus  `json:"status"`
	Provider     string        `json:"provider"`
	Attempts     int32         `json:"attempts"`
	RawResponse  []byte        `json:"raw_response,omitempty"`
	Candidate    *OCRCandidate `json:"candidate,omitempty"`
	ErrorMessage *string       `json:"error_message,omitempty"`
	Version      int32         `json:"version"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
	CompletedAt  *time.Time    `json:"completed_at,omitempty"`
}
