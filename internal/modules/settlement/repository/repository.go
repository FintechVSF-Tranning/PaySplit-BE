package repository

import (
	"context"
	"time"

	"paysplit-backend/internal/modules/settlement/domain"
)

type Executor interface{}

type BeforeCommit func(context.Context, Executor, []string, map[string]string) error

type ListInput struct {
	GroupID      string
	CallerUserID string
	Cursor       *string
	Limit        int
}

type ListDebtsInput struct {
	ListInput
	DebtorID   *string
	CreditorID *string
	Status     *string
}

type CreatePaymentInput struct {
	GroupID          string
	CallerUserID     string
	CreditorMemberID string
	DebtIDs          []string
	IdempotencyKey   string
	RequestHash      string
	ReferenceCode    string
	QRPayload        string
	BeforeCommit     BeforeCommit
}

type SubmitProofInput struct {
	GroupID        string
	CallerUserID   string
	PaymentID      string
	ObjectKey      string
	Note           *string
	IdempotencyKey string
	RequestHash    string
	OperationID    string
	BeforeCommit   BeforeCommit
}

type PaymentMutationInput struct {
	GroupID        string
	CallerUserID   string
	PaymentID      string
	Reason         *string
	IdempotencyKey string
	RequestHash    string
	BeforeCommit   BeforeCommit
}

type RemindInput struct {
	GroupID        string
	CallerUserID   string
	DebtID         string
	IdempotencyKey string
	RequestHash    string
	MaxCount       int32
	BeforeCommit   BeforeCommit
}

type Repository interface {
	ListExpenses(context.Context, ListInput) (*domain.ExpensePage, error)
	ListDebts(context.Context, ListDebtsInput) (*domain.DebtPage, error)
	CreatePayment(context.Context, CreatePaymentInput) (*domain.Payment, bool, error)
	GetPayment(context.Context, string, string, string) (*domain.Payment, error)
	PrepareProof(context.Context, string, string, string, string, string) (string, *domain.Payment, error)
	ResetProofAttempt(context.Context, string, string, string, string, bool) error
	SubmitProof(context.Context, SubmitProofInput) (*domain.Payment, error)
	QueueMediaCleanup(context.Context, string, string) error
	ConfirmPayment(context.Context, PaymentMutationInput) (*domain.Payment, []string, error)
	RejectPayment(context.Context, PaymentMutationInput) (*domain.Payment, []string, error)
	RemindDebt(context.Context, RemindInput) (*domain.ReminderResult, error)
	ProcessAutomatedReminders(context.Context, time.Time, int, BeforeCommit) error
	ProcessStalledPayments(context.Context, time.Time, BeforeCommit) error
	DeleteExpiredIdempotency(context.Context) error
	ProcessMediaCleanup(context.Context, func(context.Context, string) error, func(string)) error
}
