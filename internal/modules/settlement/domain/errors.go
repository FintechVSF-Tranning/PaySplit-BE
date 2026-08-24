package domain

import "errors"

var (
	ErrInvalidInput                  = errors.New("invalid input")
	ErrInvalidImage                  = errors.New("invalid image")
	ErrInvalidCursor                 = errors.New("invalid cursor")
	ErrGroupNotFound                 = errors.New("group not found")
	ErrDebtNotFound                  = errors.New("debt not found")
	ErrPaymentNotFound               = errors.New("payment not found")
	ErrCreditorNotFound              = errors.New("creditor not found")
	ErrForbidden                     = errors.New("forbidden")
	ErrBankAccountRequired           = errors.New("bank account required")
	ErrDebtsNotAwaiting              = errors.New("debts not awaiting")
	ErrPaymentNotPendingProof        = errors.New("payment not pending proof")
	ErrPaymentNotPendingConfirmation = errors.New("payment not pending confirmation")
	ErrDebtNotAwaiting               = errors.New("debt not awaiting")
	ErrReminderRateLimited           = errors.New("reminder rate limited")
	ErrIdempotencyConflict           = errors.New("idempotency conflict")
	ErrIdempotencyInProgress         = errors.New("idempotency in progress")
	ErrStorageUnavailable            = errors.New("storage unavailable")
)
