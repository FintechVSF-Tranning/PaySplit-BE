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
	createFn  func(repository.CreatePaymentInput) (*domain.Payment, bool, error)
	prepareFn func(string, string) (string, *domain.Payment, error)
	resetFn   func(string, string, bool) error
	submitFn  func(repository.SubmitProofInput) (*domain.Payment, error)
	queueFn   func(string) error
	confirmFn func(repository.PaymentMutationInput) (*domain.Payment, []string, error)
	rejectFn  func(repository.PaymentMutationInput) (*domain.Payment, []string, error)
	remindFn  func(repository.RemindInput) (*domain.ReminderResult, error)
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
func (s *stubRepository) PrepareProof(_ context.Context, _, _, _, key, hash string) (string, *domain.Payment, error) {
	return s.prepareFn(key, hash)
}
func (s *stubRepository) ResetProofAttempt(_ context.Context, _, _, requestHash, operationID string, replaceOperation bool) error {
	return s.resetFn(operationID, requestHash, replaceOperation)
}
func (s *stubRepository) SubmitProof(_ context.Context, in repository.SubmitProofInput) (*domain.Payment, error) {
	return s.submitFn(in)
}
func (s *stubRepository) QueueMediaCleanup(_ context.Context, key, _ string) error {
	return s.queueFn(key)
}
func (s *stubRepository) ConfirmPayment(_ context.Context, in repository.PaymentMutationInput) (*domain.Payment, []string, error) {
	return s.confirmFn(in)
}
func (s *stubRepository) RejectPayment(_ context.Context, in repository.PaymentMutationInput) (*domain.Payment, []string, error) {
	return s.rejectFn(in)
}
func (s *stubRepository) RemindDebt(_ context.Context, in repository.RemindInput) (*domain.ReminderResult, error) {
	return s.remindFn(in)
}
func (s *stubRepository) ProcessAutomatedReminders(context.Context, time.Time, int, repository.BeforeCommit) error {
	return nil
}
func (s *stubRepository) ProcessStalledPayments(context.Context, time.Time, repository.BeforeCommit) error {
	return nil
}
func (s *stubRepository) DeleteExpiredIdempotency(context.Context) error { return nil }
func (s *stubRepository) ProcessMediaCleanup(context.Context, func(context.Context, string) error, func(string)) error {
	return nil
}

type stubStorage struct {
	uploads, deletes int
	queuedKey        string
	uploadErr        error
	deleteErr        error
	signedTTL        time.Duration
}

type stubNotifier struct {
	kind string
	data map[string]string
}

func (s *stubNotifier) NotifyTx(_ context.Context, _ repository.Executor, _, kind string, data map[string]string) error {
	s.kind = kind
	s.data = data
	return nil
}

func (s *stubStorage) Upload(_ context.Context, _ []byte, key string) (string, error) {
	s.uploads++
	return key, s.uploadErr
}
func (s *stubStorage) SignedURL(key string, ttl time.Duration) (string, error) {
	s.signedTTL = ttl
	return "signed:" + key, nil
}
func (s *stubStorage) Delete(context.Context, string) error { s.deletes++; return s.deleteErr }

func serviceRepo() *stubRepository {
	return &stubRepository{
		createFn: func(repository.CreatePaymentInput) (*domain.Payment, bool, error) {
			return &domain.Payment{}, true, nil
		},
		prepareFn: func(string, string) (string, *domain.Payment, error) { return "operation", nil, nil },
		resetFn:   func(string, string, bool) error { return nil },
		submitFn:  func(repository.SubmitProofInput) (*domain.Payment, error) { return &domain.Payment{}, nil },
		queueFn:   func(string) error { return nil },
		confirmFn: func(repository.PaymentMutationInput) (*domain.Payment, []string, error) {
			return &domain.Payment{}, nil, nil
		},
		rejectFn: func(repository.PaymentMutationInput) (*domain.Payment, []string, error) {
			return &domain.Payment{}, nil, nil
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

func TestSubmitProof_AC6AcceptsJPEGPNGAndHEIC(t *testing.T) {
	images := map[string][]byte{
		"image/jpeg": {0xff, 0xd8, 0xff},
		"image/png":  {'\x89', 'P', 'N', 'G', '\r', '\n', '\x1a', '\n'},
		"image/heic": {0, 0, 0, 12, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c'},
	}
	for contentType, image := range images {
		t.Run(contentType, func(t *testing.T) {
			repo := serviceRepo()
			storage := &stubStorage{}
			svc := NewService(repo)
			svc.SetProofStorage(storage, 1024, 5*time.Minute)
			if _, err := svc.SubmitProof(context.Background(), SubmitProofInput{GroupID: "group", CallerUserID: "user", PaymentID: "payment", IdempotencyKey: "key", ContentType: contentType, Image: image}); err != nil {
				t.Fatal(err)
			}
			if storage.uploads != 1 {
				t.Fatalf("uploads=%d, want 1", storage.uploads)
			}
		})
	}
}

func TestDetectProofContentTypeRejectsNonHEICISOBaseMedia(t *testing.T) {
	mp4 := []byte{0, 0, 0, 12, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	if got := DetectProofContentType(mp4); got != "" {
		t.Fatalf("detected MP4 as %q", got)
	}
}

func TestSubmitProof_AC6RejectsInvalidAndOversizedInput(t *testing.T) {
	repo := serviceRepo()
	svc := NewService(repo)
	svc.SetProofStorage(&stubStorage{}, 8, 5*time.Minute)
	longNote := string(make([]rune, 501))
	cases := []SubmitProofInput{
		{IdempotencyKey: "key", ContentType: "text/plain", Image: []byte("text")},
		{IdempotencyKey: "key", ContentType: "image/jpeg", Image: append([]byte{0xff, 0xd8, 0xff}, make([]byte, 6)...)},
		{IdempotencyKey: "key", ContentType: "image/jpeg", Image: []byte{0xff, 0xd8, 0xff}, Note: &longNote},
	}
	for _, in := range cases {
		if _, err := svc.SubmitProof(context.Background(), in); err == nil {
			t.Fatalf("SubmitProof(%+v) succeeded, want validation error", in)
		}
	}
}

func TestSubmitProof_AC11ReleasesIdempotencyAfterUploadFailure(t *testing.T) {
	repo := serviceRepo()
	var operationID, requestHash string
	var replaceOperation bool
	repo.resetFn = func(operation, hash string, replace bool) error {
		operationID, requestHash, replaceOperation = operation, hash, replace
		return nil
	}
	svc := NewService(repo)
	svc.SetProofStorage(&stubStorage{uploadErr: errors.New("upload failed")}, 1024, 5*time.Minute)
	_, err := svc.SubmitProof(context.Background(), SubmitProofInput{GroupID: "group", CallerUserID: "user", PaymentID: "payment", IdempotencyKey: "key", ContentType: "image/jpeg", Image: []byte{0xff, 0xd8, 0xff}})
	if !errors.Is(err, domain.ErrStorageUnavailable) {
		t.Fatalf("error=%v, want storage unavailable", err)
	}
	if operationID != "operation" || requestHash == "" || replaceOperation {
		t.Fatalf("unexpected reset operation=%q hash=%q replace=%v", operationID, requestHash, replaceOperation)
	}
}

func TestSubmitProof_AC6AndAC11ReplaysWithoutUploading(t *testing.T) {
	repo := serviceRepo()
	objectKey := "payments/payment/proofs/operation"
	repo.prepareFn = func(string, string) (string, *domain.Payment, error) {
		return "operation", &domain.Payment{Status: domain.PaymentPendingConfirmation, ImageObjectKey: &objectKey}, nil
	}
	storage := &stubStorage{}
	svc := NewService(repo)
	svc.SetProofStorage(storage, 1024, 5*time.Minute)
	payment, err := svc.SubmitProof(context.Background(), SubmitProofInput{GroupID: "group", PaymentID: "payment", IdempotencyKey: "key", ContentType: "image/jpeg", Image: []byte{0xff, 0xd8, 0xff}})
	if err != nil {
		t.Fatal(err)
	}
	if storage.uploads != 0 || payment.ImageURL == nil || storage.signedTTL != 5*time.Minute {
		t.Fatalf("unexpected replay: uploads=%d payment=%+v ttl=%s", storage.uploads, payment, storage.signedTTL)
	}
}

func TestSubmitProof_AC6AndAC11InProgressDoesNotUploadOrDelete(t *testing.T) {
	repo := serviceRepo()
	repo.prepareFn = func(string, string) (string, *domain.Payment, error) {
		return "", nil, domain.ErrIdempotencyInProgress
	}
	storage := &stubStorage{}
	svc := NewService(repo)
	svc.SetProofStorage(storage, 1024, 5*time.Minute)
	_, err := svc.SubmitProof(context.Background(), SubmitProofInput{GroupID: "group", PaymentID: "payment", IdempotencyKey: "key", ContentType: "image/jpeg", Image: []byte{0xff, 0xd8, 0xff}})
	if !errors.Is(err, domain.ErrIdempotencyInProgress) {
		t.Fatalf("error=%v, want idempotency in progress", err)
	}
	if storage.uploads != 0 || storage.deletes != 0 {
		t.Fatalf("uploads=%d deletes=%d, want no storage mutation", storage.uploads, storage.deletes)
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

func TestSubmitProof_AC6QueuesExactObjectWhenCompensationDeleteFails(t *testing.T) {
	repo := serviceRepo()
	repo.submitFn = func(in repository.SubmitProofInput) (*domain.Payment, error) { return nil, domain.ErrDebtsNotAwaiting }
	repo.queueFn = func(key string) error { repoKey := key; _ = repoKey; return nil }
	storage := &stubStorage{deleteErr: errors.New("delete failed")}
	var queued string
	var resetOperation string
	var replaceOperation bool
	repo.queueFn = func(key string) error { queued = key; return nil }
	repo.resetFn = func(operation, _ string, replace bool) error {
		resetOperation, replaceOperation = operation, replace
		return nil
	}
	svc := NewService(repo)
	svc.SetProofStorage(storage, 1024, 5*time.Minute)
	_, err := svc.SubmitProof(context.Background(), SubmitProofInput{GroupID: "group", PaymentID: "payment", IdempotencyKey: "key", ContentType: "image/jpeg", Image: []byte{0xff, 0xd8, 0xff}})
	if !errors.Is(err, domain.ErrDebtsNotAwaiting) {
		t.Fatalf("error=%v", err)
	}
	want := "payments/payment/proofs/operation"
	if storage.deletes != 1 || queued != want || resetOperation != "operation" || !replaceOperation {
		t.Fatalf("deletes=%d queued=%q reset=%q replace=%v, want isolated retry for %q", storage.deletes, queued, resetOperation, replaceOperation, want)
	}
}

func TestRejectPayment_AC8TrimsReasonAndRejectsBounds(t *testing.T) {
	repo := serviceRepo()
	var got string
	repo.rejectFn = func(in repository.PaymentMutationInput) (*domain.Payment, []string, error) {
		got = *in.Reason
		return &domain.Payment{}, nil, nil
	}
	svc := NewService(repo)
	reason := "  not received  "
	if _, _, err := svc.RejectPayment(context.Background(), PaymentMutationInput{IdempotencyKey: "key", Reason: &reason}); err != nil {
		t.Fatal(err)
	}
	if got != "not received" {
		t.Fatalf("reason=%q", got)
	}
	for _, value := range []string{"   ", string(make([]rune, 501))} {
		if _, _, err := svc.RejectPayment(context.Background(), PaymentMutationInput{IdempotencyKey: "key", Reason: &value}); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("reason length=%d error=%v", len([]rune(value)), err)
		}
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
