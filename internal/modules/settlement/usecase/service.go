package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"paysplit-backend/internal/modules/settlement/domain"
	"paysplit-backend/internal/modules/settlement/repository"
)

type ProofStorage interface {
	Upload(context.Context, []byte, string) (string, error)
	SignedURL(string, time.Duration) (string, error)
	Delete(context.Context, string) error
}
type TxNotifier interface {
	NotifyTx(context.Context, repository.Executor, string, string, map[string]string) error
}

type Service struct {
	repo      repository.Repository
	storage   ProofStorage
	proofMax  int64
	signedTTL time.Duration
	notifier  TxNotifier
}

func (s *Service) SetNotifier(notifier TxNotifier) { s.notifier = notifier }
func (s *Service) notify(kind string) func(context.Context, repository.Executor, []string) error {
	return func(ctx context.Context, ex repository.Executor, targets []string) error {
		if s.notifier == nil {
			return nil
		}
		for _, target := range targets {
			if err := s.notifier.NotifyTx(ctx, ex, target, kind, map[string]string{"type": kind}); err != nil {
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
	return &Service{repo: repo}
}

func (s *Service) SetProofStorage(storage ProofStorage, maxBytes int64, signedTTL time.Duration) {
	s.storage = storage
	if maxBytes <= 0 {
		maxBytes = 10 << 20
	}
	if signedTTL <= 0 {
		signedTTL = 5 * time.Minute
	}
	s.proofMax, s.signedTTL = maxBytes, signedTTL
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
	for _, raw := range ids {
		id, e := uuid.Parse(raw)
		if e != nil {
			return nil, false, domain.ErrInvalidInput
		}
		normalized := id.String()
		if _, ok := seen[normalized]; ok {
			return nil, false, domain.ErrInvalidInput
		}
		seen[normalized] = struct{}{}
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
	payment, err := s.repo.GetPayment(ctx, groupID, callerUserID, paymentID)
	if err == nil {
		s.signProof(payment)
	}
	return payment, err
}

type SubmitProofInput struct {
	GroupID, CallerUserID, PaymentID, IdempotencyKey, ContentType string
	Image                                                         []byte
	Note                                                          *string
}

func (s *Service) SubmitProof(ctx context.Context, in SubmitProofInput) (*domain.Payment, error) {
	if s.storage == nil {
		return nil, domain.ErrStorageUnavailable
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, domain.ErrInvalidInput
	}
	if in.ContentType == "" && len(in.Image) > 0 {
		in.ContentType = http.DetectContentType(in.Image)
	}
	if len(in.Image) == 0 || int64(len(in.Image)) > s.proofMax || !validProofImage(in.Image, in.ContentType) {
		return nil, domain.ErrInvalidImage
	}
	if in.Note != nil {
		value := strings.TrimSpace(*in.Note)
		if len([]rune(value)) > 500 {
			return nil, domain.ErrInvalidInput
		}
		in.Note = &value
	}
	imageHash := sha256.Sum256(in.Image)
	canonical, _ := json.Marshal(map[string]any{"group_id": in.GroupID, "payment_id": in.PaymentID, "note": in.Note, "image_sha256": hex.EncodeToString(imageHash[:])})
	requestHash := sha256.Sum256(canonical)
	requestHashValue := hex.EncodeToString(requestHash[:])
	operationID, replay, err := s.repo.PrepareProof(ctx, in.GroupID, in.CallerUserID, in.PaymentID, in.IdempotencyKey, requestHashValue)
	if err != nil {
		return nil, err
	}
	if replay != nil {
		s.signProof(replay)
		return replay, nil
	}
	objectKey := "payments/" + in.PaymentID + "/proofs/" + operationID
	uploaded, err := s.storage.Upload(ctx, in.Image, objectKey)
	if err != nil {
		return nil, domain.ErrStorageUnavailable
	}
	payment, err := s.repo.SubmitProof(ctx, repository.SubmitProofInput{GroupID: in.GroupID, CallerUserID: in.CallerUserID, PaymentID: in.PaymentID, ObjectKey: uploaded, Note: in.Note, IdempotencyKey: in.IdempotencyKey, RequestHash: requestHashValue, OperationID: operationID, BeforeCommit: s.notify("payment_submitted")})
	if err != nil {
		if deleteErr := s.storage.Delete(ctx, uploaded); deleteErr != nil {
			_ = s.repo.QueueMediaCleanup(ctx, uploaded, "proof submission compensation")
		}
		return nil, err
	}
	s.signProof(payment)
	return payment, nil
}

func (s *Service) signProof(payment *domain.Payment) {
	if payment == nil || payment.ImageObjectKey == nil || s.storage == nil {
		return
	}
	if value, err := s.storage.SignedURL(*payment.ImageObjectKey, s.signedTTL); err == nil {
		payment.ImageURL = &value
	}
}

func validProofImage(data []byte, contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if contentType == "image/jpeg" && len(data) >= 3 {
		return data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff
	}
	if contentType == "image/png" && len(data) >= 8 {
		return string(data[:8]) == "\x89PNG\r\n\x1a\n"
	}
	if (contentType == "image/heic" || contentType == "image/heif") && len(data) >= 12 {
		return string(data[4:8]) == "ftyp"
	}
	return false
}

type PaymentMutationInput struct {
	GroupID, CallerUserID, PaymentID, IdempotencyKey string
	Reason                                           *string
}

func (s *Service) ConfirmPayment(ctx context.Context, in PaymentMutationInput) (*domain.Payment, []string, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, nil, domain.ErrInvalidInput
	}
	hash := canonicalMutationHash(in, "confirm")
	payment, ids, err := s.repo.ConfirmPayment(ctx, repository.PaymentMutationInput{GroupID: in.GroupID, CallerUserID: in.CallerUserID, PaymentID: in.PaymentID, IdempotencyKey: in.IdempotencyKey, RequestHash: hash, BeforeCommit: s.notify("payment_confirmed")})
	if err == nil {
		s.signProof(payment)
	}
	return payment, ids, err
}
func (s *Service) RejectPayment(ctx context.Context, in PaymentMutationInput) (*domain.Payment, []string, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" || in.Reason == nil {
		return nil, nil, domain.ErrInvalidInput
	}
	reason := strings.TrimSpace(*in.Reason)
	if len([]rune(reason)) < 1 || len([]rune(reason)) > 500 {
		return nil, nil, domain.ErrInvalidInput
	}
	in.Reason = &reason
	hash := canonicalMutationHash(in, "reject")
	payment, ids, err := s.repo.RejectPayment(ctx, repository.PaymentMutationInput{GroupID: in.GroupID, CallerUserID: in.CallerUserID, PaymentID: in.PaymentID, Reason: &reason, IdempotencyKey: in.IdempotencyKey, RequestHash: hash, BeforeCommit: s.notify("payment_rejected")})
	if err == nil {
		s.signProof(payment)
	}
	return payment, ids, err
}
func canonicalMutationHash(in PaymentMutationInput, operation string) string {
	raw, _ := json.Marshal(map[string]any{"operation": operation, "group_id": in.GroupID, "payment_id": in.PaymentID, "reason": in.Reason})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type RemindInput struct{ GroupID, CallerUserID, DebtID, IdempotencyKey string }

func (s *Service) RemindDebt(ctx context.Context, in RemindInput) (*domain.ReminderResult, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, domain.ErrInvalidInput
	}
	raw, _ := json.Marshal(map[string]string{"group_id": in.GroupID, "debt_id": in.DebtID})
	sum := sha256.Sum256(raw)
	return s.repo.RemindDebt(ctx, repository.RemindInput{GroupID: in.GroupID, CallerUserID: in.CallerUserID, DebtID: in.DebtID, IdempotencyKey: in.IdempotencyKey, RequestHash: hex.EncodeToString(sum[:]), BeforeCommit: s.notify("debt_reminded")})
}
func (s *Service) ProcessAutomatedReminders(ctx context.Context, staleBefore time.Time, maxCount int) error {
	return s.repo.ProcessAutomatedReminders(ctx, staleBefore, maxCount, s.notify("debt_reminded"))
}
func (s *Service) ProcessStalledPayments(ctx context.Context, submittedBefore time.Time) error {
	return s.repo.ProcessStalledPayments(ctx, submittedBefore, s.notify("payment_stalled_confirmation"))
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
