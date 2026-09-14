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

type MarkReceivedInput struct {
	GroupID        string
	CallerUserID   string
	DebtID         string
	IdempotencyKey string
	RequestHash    string
	NotifyDebtor   BeforeCommit
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

type BankTransferInput struct {
	Transfer domain.BankTransfer
	// NotifyDebtor/NotifyCreditor chạy trong transaction gạch nợ, chỉ khi thực sự gạch.
	NotifyDebtor   BeforeCommit
	NotifyCreditor BeforeCommit
}

type Repository interface {
	ListExpenses(context.Context, ListInput) (*domain.ExpensePage, error)
	ListDebts(context.Context, ListDebtsInput) (*domain.DebtPage, error)
	CreatePayment(context.Context, CreatePaymentInput) (*domain.Payment, bool, error)
	GetPayment(context.Context, string, string, string) (*domain.Payment, error)
	SettleBankTransfer(context.Context, BankTransferInput) (domain.BankMatchResult, error)
	MarkDebtReceived(context.Context, MarkReceivedInput) (*domain.Payment, []string, error)
	RemindDebt(context.Context, RemindInput) (*domain.ReminderResult, error)
	ProcessAutomatedReminders(context.Context, time.Time, int, BeforeCommit) error
	DeleteExpiredIdempotency(context.Context) error
}
