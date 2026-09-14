package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"paysplit-backend/internal/modules/settlement/domain"
	"paysplit-backend/internal/modules/settlement/repository"
)

type stubRepository struct {
	createFn   func(repository.CreatePaymentInput) (*domain.Payment, bool, error)
	remindFn   func(repository.RemindInput) (*domain.ReminderResult, error)
	bankFn     func(repository.BankTransferInput) (domain.BankMatchResult, error)
	receivedFn func(repository.MarkReceivedInput) (*domain.Payment, []string, error)
}

func (s *stubRepository) ListExpenses(context.Context, repository.ListInput) (*domain.ExpensePage, error) {
	return &domain.ExpensePage{}, nil
}
func (s *stubRepository) ListDebts(context.Context, repository.ListDebtsInput) (*domain.DebtPage, error) {
	return &domain.DebtPage{}, nil
}
func (s *stubRepository) CreatePayment(_ context.Context, in repository.CreatePaymentInput) (*domain.Payment, bool, error) {
	return s.createFn(in)
}
func (s *stubRepository) GetPayment(context.Context, string, string, string) (*domain.Payment, error) {
	return &domain.Payment{}, nil
}
func (s *stubRepository) SettleBankTransfer(_ context.Context, in repository.BankTransferInput) (domain.BankMatchResult, error) {
	return s.bankFn(in)
}
func (s *stubRepository) MarkDebtReceived(_ context.Context, in repository.MarkReceivedInput) (*domain.Payment, []string, error) {
	return s.receivedFn(in)
}
func (s *stubRepository) RemindDebt(_ context.Context, in repository.RemindInput) (*domain.ReminderResult, error) {
	return s.remindFn(in)
}
func (s *stubRepository) ProcessAutomatedReminders(context.Context, time.Time, int, repository.BeforeCommit) error {
	return nil
}
func (s *stubRepository) DeleteExpiredIdempotency(context.Context) error { return nil }

type stubNotifier struct {
	kind string
	data map[string]string
}

func (s *stubNotifier) NotifyTx(_ context.Context, _ repository.Executor, _, kind string, data map[string]string) error {
	s.kind = kind
	s.data = data
	return nil
}

func serviceRepo() *stubRepository {
	return &stubRepository{
		createFn: func(repository.CreatePaymentInput) (*domain.Payment, bool, error) {
			return &domain.Payment{}, true, nil
		},
		remindFn: func(repository.RemindInput) (*domain.ReminderResult, error) { return &domain.ReminderResult{}, nil },
	}
}

func TestGeneratePayment_AC3AndAC11ValidateAndCanonicalizeDebtIDs(t *testing.T) {
	const id1 = "018f0000-0000-7000-8000-abcdefabcdef"
	const id2 = "018f0000-0000-7000-8000-abcdefabcdee"
	repo := serviceRepo()
	var hashes []string
	repo.createFn = func(in repository.CreatePaymentInput) (*domain.Payment, bool, error) {
		hashes = append(hashes, in.RequestHash)
		return &domain.Payment{}, true, nil
	}
	svc := NewService(repo)
	for _, ids := range [][]string{{id2, id1}, {id1, id2}, {strings.ToUpper(id2), strings.ToUpper(id1)}} {
		if _, _, err := svc.GeneratePayment(context.Background(), GeneratePaymentInput{GroupID: "group", CreditorMemberID: "creditor", CallerUserID: "user", IdempotencyKey: "key", DebtIDs: ids}); err != nil {
			t.Fatal(err)
		}
	}
	if hashes[0] != hashes[1] || hashes[0] != hashes[2] {
		t.Fatalf("normalized debt sets produced different hashes: %q", hashes)
	}
	for _, ids := range [][]string{{}, {id1, id1}, {"not-a-uuid"}} {
		if _, _, err := svc.GeneratePayment(context.Background(), GeneratePaymentInput{CreditorMemberID: "creditor", IdempotencyKey: "key", DebtIDs: ids}); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("DebtIDs=%v error=%v, want invalid input", ids, err)
		}
	}
}

func TestNotifyPreservesRoutingIdentifiers(t *testing.T) {
	svc := NewService(serviceRepo())
	notifier := &stubNotifier{}
	svc.SetNotifier(notifier)
	want := map[string]string{"group_id": "group", "payment_id": "payment"}
	if err := svc.notify("payment_confirmed")(context.Background(), struct{}{}, []string{"user"}, want); err != nil {
		t.Fatal(err)
	}
	if notifier.kind != "payment_confirmed" || notifier.data["group_id"] != "group" || notifier.data["payment_id"] != "payment" {
		t.Fatalf("unexpected notification kind=%q data=%v", notifier.kind, notifier.data)
	}
	if _, exists := notifier.data["type"]; exists {
		t.Fatalf("payload repeats top level notification type: %v", notifier.data)
	}
}

func TestRemindDebt_AC9UsesConfiguredMaximum(t *testing.T) {
	repo := serviceRepo()
	var maxCount int32
	repo.remindFn = func(in repository.RemindInput) (*domain.ReminderResult, error) {
		maxCount = in.MaxCount
		return &domain.ReminderResult{}, nil
	}
	svc := NewService(repo)
	svc.SetReminderMaxCount(2)
	if _, err := svc.RemindDebt(context.Background(), RemindInput{GroupID: "group", DebtID: "debt", IdempotencyKey: "key"}); err != nil {
		t.Fatal(err)
	}
	if maxCount != 2 {
		t.Fatalf("max count=%d, want 2", maxCount)
	}
}

func TestListInputs_AC1AndAC2RejectInvalidLimitsAndStatuses(t *testing.T) {
	svc := NewService(serviceRepo())
	if _, err := svc.ListExpenses(context.Background(), repository.ListInput{GroupID: "group", CallerUserID: "user", Limit: 0}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("ListExpenses error=%v", err)
	}
	status := "voided"
	if _, err := svc.ListDebts(context.Background(), repository.ListDebtsInput{ListInput: repository.ListInput{GroupID: "group", CallerUserID: "user", Limit: 20}, Status: &status}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("ListDebts error=%v", err)
	}
}

func TestSettleBankTransferValidatesAndWiresBothNotifications(t *testing.T) {
	var got repository.BankTransferInput
	repo := &stubRepository{bankFn: func(in repository.BankTransferInput) (domain.BankMatchResult, error) {
		got = in
		return domain.BankMatchResult{Outcome: domain.BankMatchConfirmed}, nil
	}}
	svc := NewService(repo)
	valid := domain.BankTransfer{TransactionID: 1, ReferenceCode: "PAYAB3CD4EF", Amount: 300000, AccountNumbers: []string{"0123"}}
	res, err := svc.SettleBankTransfer(context.Background(), valid)
	if err != nil || res.Outcome != domain.BankMatchConfirmed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got.Transfer.ReferenceCode != valid.ReferenceCode || got.NotifyDebtor == nil || got.NotifyCreditor == nil {
		t.Fatalf("unexpected repository input: %+v", got)
	}
	for _, bad := range []domain.BankTransfer{
		{ReferenceCode: "PAYAB3CD4EF", Amount: 1, AccountNumbers: []string{"1"}},
		{TransactionID: 1, Amount: 1, AccountNumbers: []string{"1"}},
		{TransactionID: 1, ReferenceCode: "PAYAB3CD4EF", AccountNumbers: []string{"1"}},
		{TransactionID: 1, ReferenceCode: "PAYAB3CD4EF", Amount: 1},
	} {
		if _, err := svc.SettleBankTransfer(context.Background(), bad); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("%+v: err=%v, want ErrInvalidInput", bad, err)
		}
	}
}

func TestMarkDebtReceivedRequiresKeyAndNotifiesDebtorOnly(t *testing.T) {
	repo := serviceRepo()
	var got repository.MarkReceivedInput
	repo.receivedFn = func(in repository.MarkReceivedInput) (*domain.Payment, []string, error) {
		got = in
		return &domain.Payment{}, []string{in.DebtID}, nil
	}
	svc := NewService(repo)
	notifier := &stubNotifier{}
	svc.SetNotifier(notifier)
	if _, _, err := svc.MarkDebtReceived(context.Background(), MarkReceivedInput{GroupID: "group", DebtID: "debt"}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("missing idempotency key err=%v", err)
	}
	_, ids, err := svc.MarkDebtReceived(context.Background(), MarkReceivedInput{GroupID: "group", CallerUserID: "user", DebtID: "debt", IdempotencyKey: "key"})
	if err != nil || len(ids) != 1 || ids[0] != "debt" {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	if got.RequestHash == "" || got.NotifyDebtor == nil {
		t.Fatalf("unexpected repository input: %+v", got)
	}
	if err = got.NotifyDebtor(context.Background(), struct{}{}, []string{"debtor"}, nil); err != nil || notifier.kind != "payment_marked_received" {
		t.Fatalf("notification kind=%q err=%v", notifier.kind, err)
	}
}
