package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"paysplit-backend/internal/modules/settlement/domain"
	"paysplit-backend/internal/modules/settlement/repository"
)

type TxNotifier interface {
	NotifyTx(context.Context, repository.Executor, string, string, map[string]string) error
}

type Service struct {
	repo      repository.Repository
	remindMax int32
	notifier  TxNotifier
}

func (s *Service) SetNotifier(notifier TxNotifier) { s.notifier = notifier }
func (s *Service) notify(kind string) repository.BeforeCommit {
	return func(ctx context.Context, ex repository.Executor, targets []string, data map[string]string) error {
		if s.notifier == nil {
			return nil
		}
		for _, target := range targets {
			if err := s.notifier.NotifyTx(ctx, ex, target, kind, data); err != nil {
				return err
			}
		}
		return nil
	}
}

func NewService(repo repository.Repository) *Service {
	if repo == nil {
		panic("settlement service repository must not be nil")
	}
	return &Service{repo: repo, remindMax: 3}
}

func (s *Service) SetReminderMaxCount(maxCount int) {
	if maxCount < 1 || maxCount > 3 {
		panic("settlement reminder maximum must be between 1 and 3")
	}
	s.remindMax = int32(maxCount)
}

type GeneratePaymentInput struct {
	GroupID, CallerUserID, CreditorMemberID, IdempotencyKey string
	DebtIDs                                                 []string
}

func (s *Service) GeneratePayment(ctx context.Context, in GeneratePaymentInput) (*domain.Payment, bool, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" || strings.TrimSpace(in.CreditorMemberID) == "" {
		return nil, false, domain.ErrInvalidInput
	}
	if in.DebtIDs != nil && (len(in.DebtIDs) < 1 || len(in.DebtIDs) > 100) {
		return nil, false, domain.ErrInvalidInput
	}
	ids := append([]string(nil), in.DebtIDs...)
	seen := make(map[string]struct{}, len(ids))
	for index, raw := range ids {
		id, e := uuid.Parse(raw)
		if e != nil {
			return nil, false, domain.ErrInvalidInput
		}
		normalized := id.String()
		if _, ok := seen[normalized]; ok {
			return nil, false, domain.ErrInvalidInput
		}
		seen[normalized] = struct{}{}
		ids[index] = normalized
	}
	sort.Strings(ids)
	canonical, _ := json.Marshal(map[string]any{"group_id": in.GroupID, "creditor_member_id": in.CreditorMemberID, "debt_ids": ids})
	sum := sha256.Sum256(canonical)
	return s.repo.CreatePayment(ctx, repository.CreatePaymentInput{GroupID: in.GroupID, CallerUserID: in.CallerUserID, CreditorMemberID: in.CreditorMemberID, DebtIDs: in.DebtIDs, IdempotencyKey: in.IdempotencyKey, RequestHash: hex.EncodeToString(sum[:]), BeforeCommit: s.notify("payment_created")})
}

func (s *Service) GetPayment(ctx context.Context, groupID, callerUserID, paymentID string) (*domain.Payment, error) {
	if groupID == "" || callerUserID == "" || paymentID == "" {
		return nil, domain.ErrPaymentNotFound
	}
	return s.repo.GetPayment(ctx, groupID, callerUserID, paymentID)
}

// SettleBankTransfer đối soát một giao dịch tiền vào do ngân hàng báo với
// payment mang cùng mã tham chiếu và gạch nợ nếu khớp.
func (s *Service) SettleBankTransfer(ctx context.Context, transfer domain.BankTransfer) (domain.BankMatchResult, error) {
	if transfer.TransactionID <= 0 || strings.TrimSpace(transfer.ReferenceCode) == "" || transfer.Amount <= 0 || len(transfer.AccountNumbers) == 0 {
		return domain.BankMatchResult{}, domain.ErrInvalidInput
	}
	return s.repo.SettleBankTransfer(ctx, repository.BankTransferInput{
		Transfer:       transfer,
		NotifyDebtor:   s.notify("payment_bank_confirmed"),
		NotifyCreditor: s.notify("payment_bank_received"),
	})
}

type RemindInput struct{ GroupID, CallerUserID, DebtID, IdempotencyKey string }

func (s *Service) RemindDebt(ctx context.Context, in RemindInput) (*domain.ReminderResult, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, domain.ErrInvalidInput
	}
	raw, _ := json.Marshal(map[string]string{"group_id": in.GroupID, "debt_id": in.DebtID})
	sum := sha256.Sum256(raw)
	return s.repo.RemindDebt(ctx, repository.RemindInput{GroupID: in.GroupID, CallerUserID: in.CallerUserID, DebtID: in.DebtID, IdempotencyKey: in.IdempotencyKey, RequestHash: hex.EncodeToString(sum[:]), MaxCount: s.remindMax, BeforeCommit: s.notify("debt_reminded")})
}
func (s *Service) ProcessAutomatedReminders(ctx context.Context, staleBefore time.Time, maxCount int) error {
	return s.repo.ProcessAutomatedReminders(ctx, staleBefore, maxCount, s.notify("debt_reminded"))
}

type MarkReceivedInput struct{ GroupID, CallerUserID, DebtID, IdempotencyKey string }

// MarkDebtReceived là đường dự phòng khi ngân hàng không tự khớp được giao dịch
// (sai số tiền, mất mã tham chiếu, SePay lỗi): người nhận tự xác nhận đã nhận
// đủ tiền và khoản nợ được gạch ngay, không cần minh chứng.
func (s *Service) MarkDebtReceived(ctx context.Context, in MarkReceivedInput) (*domain.Payment, []string, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, nil, domain.ErrInvalidInput
	}
	raw, _ := json.Marshal(map[string]string{"operation": "mark_received", "group_id": in.GroupID, "debt_id": in.DebtID})
	sum := sha256.Sum256(raw)
	return s.repo.MarkDebtReceived(ctx, repository.MarkReceivedInput{GroupID: in.GroupID, CallerUserID: in.CallerUserID, DebtID: in.DebtID, IdempotencyKey: in.IdempotencyKey, RequestHash: hex.EncodeToString(sum[:]), NotifyDebtor: s.notify("payment_marked_received")})
}

func (s *Service) ListExpenses(ctx context.Context, in repository.ListInput) (*domain.ExpensePage, error) {
	if strings.TrimSpace(in.GroupID) == "" || strings.TrimSpace(in.CallerUserID) == "" || in.Limit < 1 || in.Limit > 100 {
		return nil, domain.ErrInvalidInput
	}
	return s.repo.ListExpenses(ctx, in)
}

func (s *Service) ListDebts(ctx context.Context, in repository.ListDebtsInput) (*domain.DebtPage, error) {
	if strings.TrimSpace(in.GroupID) == "" || strings.TrimSpace(in.CallerUserID) == "" || in.Limit < 1 || in.Limit > 100 {
		return nil, domain.ErrInvalidInput
	}
	if in.Status != nil {
		switch *in.Status {
		case "awaiting", "pending_confirmation", "settled":
		default:
			return nil, domain.ErrInvalidInput
		}
	}
	return s.repo.ListDebts(ctx, in)
}
