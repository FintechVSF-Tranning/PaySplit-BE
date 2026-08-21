package repository

import (
	"context"
	"time"

	"paysplit-backend/internal/modules/settlement/domain"
)

type Executor interface{}

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
	BeforeCommit     func(context.Context, Executor, []string) error
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
	BeforeCommit   func(context.Context, Executor, []string) error
}

type PaymentMutationInput struct {
	GroupID        string
	CallerUserID   string
	PaymentID      string
	Reason         *string
	IdempotencyKey string
	RequestHash    string
	BeforeCommit   func(context.Context, Executor, []string) error
}

type RemindInput struct {
	GroupID        string
	CallerUserID   string
	DebtID         string
	IdempotencyKey string
	RequestHash    string
	BeforeCommit   func(context.Context, Executor, []string) error
}

type Repository interface {
	ListExpenses(context.Context, ListInput) (*domain.ExpensePage, error)
	ListDebts(context.Context, ListDebtsInput) (*domain.DebtPage, error)
	CreatePayment(context.Context, CreatePaymentInput) (*domain.Payment, bool, error)
	GetPayment(context.Context, string, string, string) (*domain.Payment, error)
	PrepareProof(context.Context, string, string, string, string, string) (string, *domain.Payment, error)
	SubmitProof(context.Context, SubmitProofInput) (*domain.Payment, error)
	QueueMediaCleanup(context.Context, string, string) error
	ConfirmPayment(context.Context, PaymentMutationInput) (*domain.Payment, []string, error)
	RejectPayment(context.Context, PaymentMutationInput) (*domain.Payment, []string, error)
	RemindDebt(context.Context, RemindInput) (*domain.ReminderResult, error)
	ProcessAutomatedReminders(context.Context, time.Time, int, func(context.Context, Executor, []string) error) error
	ProcessStalledPayments(context.Context, time.Time, func(context.Context, Executor, []string) error) error
	DeleteExpiredIdempotency(context.Context) error
	ProcessMediaCleanup(context.Context, func(context.Context, string) error) error
}
