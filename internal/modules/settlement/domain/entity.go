package domain

import "time"

const (
	PaymentPendingProof        = "pending_proof"
	PaymentPendingConfirmation = "pending_confirmation"
	PaymentConfirmed           = "confirmed"
	PaymentRejected            = "rejected"
	PaymentSuperseded          = "superseded"
)

type Membership struct {
	ID          string
	UserID      string
	Role        string
	DisplayName string
}

type ExpenseSummary struct {
	TotalOwed       int64
	TotalSettled    int64
	TotalReceivable int64
	NetBalance      int64
}

type ExpenseItem struct {
	BillID              string
	BillDate            time.Time
	MerchantName        string
	ItemName            string
	Quantity            string
	UnitPrice           int64
	LineTotal           int64
	ItemDiscountAmount  int64
	ItemFinalPrice      int64
	ShareRatio          string
	ItemShare           int64
	ServiceChargeShare  int64
	VATShare            int64
	DiscountShare       int64
	RoundingAdjustment  int64
	FinalAmount         int64
	CreditorMemberID    string
	CreditorDisplayName string
	DebtID              *string
	DebtStatus          string
	CreatedAt           time.Time
}

type Debt struct {
	ID                      string
	BillID                  string
	BillDate                time.Time
	MerchantName            string
	DebtorMemberID          string
	DebtorDisplayName       string
	DebtorAvatarObjectKey   *string
	CreditorMemberID        string
	CreditorDisplayName     string
	CreditorAvatarObjectKey *string
	Amount                  int64
	Status                  string
	ReminderCount           int32
	PaymentID               *string
	CreatedAt               time.Time
	SettledAt               *time.Time
}

type DebtMatrixEntry struct {
	DebtorMemberID   string
	CreditorMemberID string
	TotalAmount      int64
	DebtCount        int64
}

type RecipientBank struct {
	Code          string
	Name          string
	AccountNumber string
	AccountHolder string
	BIN           string
}

type Payment struct {
	ID               string
	GroupID          string
	DebtorMemberID   string
	CreditorMemberID string
	Amount           int64
	ReferenceCode    string
	Status           string
	QRPayload        string
	QRImageURL       string
	Recipient        RecipientBank
	ImageObjectKey   *string
	ImageURL         *string
	Note             *string
	RejectionReason  *string
	CoveredDebtIDs   []string
	CreatedAt        time.Time
	SubmittedAt      *time.Time
	ConfirmedAt      *time.Time
	RejectedAt       *time.Time
}

type ExpensePage struct {
	Summary    ExpenseSummary
	Items      []ExpenseItem
	NextCursor *string
}

type DebtPage struct {
	Debts            []Debt
	NetMatrix        []DebtMatrixEntry
	CallerPayable    int64
	CallerReceivable int64
	NextCursor       *string
}

type ReminderResult struct {
	DebtID        string
	ReminderCount int32
	RemindedAt    time.Time
}
