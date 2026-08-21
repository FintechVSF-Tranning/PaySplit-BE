package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"paysplit-backend/internal/modules/settlement/domain"
	"paysplit-backend/internal/modules/settlement/repository"
)

type stubRepository struct {
	createFn  func(repository.CreatePaymentInput) (*domain.Payment, bool, error)
	prepareFn func(string, string) (string, *domain.Payment, error)
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
func (s *stubRepository) ProcessAutomatedReminders(context.Context, time.Time, int, func(context.Context, repository.Executor, []string) error) error {
	return nil
}
func (s *stubRepository) ProcessStalledPayments(context.Context, time.Time, func(context.Context, repository.Executor, []string) error) error {
	return nil
}
func (s *stubRepository) DeleteExpiredIdempotency(context.Context) error { return nil }
func (s *stubRepository) ProcessMediaCleanup(context.Context, func(context.Context, string) error) error {
	return nil
}

type stubStorage struct {
	uploads, deletes int
	queuedKey        string
	uploadErr        error
	deleteErr        error
	signedTTL        time.Duration
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
	const id1 = "018f0000-0000-7000-8000-000000000001"
	const id2 = "018f0000-0000-7000-8000-000000000002"
	repo := serviceRepo()
	var hashes []string
	repo.createFn = func(in repository.CreatePaymentInput) (*domain.Payment, bool, error) {
		hashes = append(hashes, in.RequestHash)
		return &domain.Payment{}, true, nil
	}
	svc := NewService(repo)
	for _, ids := range [][]string{{id2, id1}, {id1, id2}} {
		if _, _, err := svc.GeneratePayment(context.Background(), GeneratePaymentInput{GroupID: "group", CreditorMemberID: "creditor", CallerUserID: "user", IdempotencyKey: "key", DebtIDs: ids}); err != nil {
			t.Fatal(err)
		}
	}
	if hashes[0] != hashes[1] {
		t.Fatalf("sorted debt sets produced different hashes: %q %q", hashes[0], hashes[1])
	}
	for _, ids := range [][]string{{}, {id1, id1}, {"not-a-uuid"}} {
		if _, _, err := svc.GeneratePayment(context.Background(), GeneratePaymentInput{CreditorMemberID: "creditor", IdempotencyKey: "key", DebtIDs: ids}); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("DebtIDs=%v error=%v, want invalid input", ids, err)
		}
	}
}

func TestSubmitProof_AC6AcceptsJPEGPNGAndHEIC(t *testing.T) {
	images := map[string][]byte{
		"image/jpeg": {0xff, 0xd8, 0xff},
		"image/png":  {'\x89', 'P', 'N', 'G', '\r', '\n', '\x1a', '\n'},
		"image/heic": {0, 0, 0, 0, 'f', 't', 'y', 'p', 0, 0, 0, 0},
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

func TestSubmitProof_AC6QueuesExactObjectWhenCompensationDeleteFails(t *testing.T) {
	repo := serviceRepo()
	repo.submitFn = func(in repository.SubmitProofInput) (*domain.Payment, error) { return nil, domain.ErrDebtsNotAwaiting }
	repo.queueFn = func(key string) error { repoKey := key; _ = repoKey; return nil }
	storage := &stubStorage{deleteErr: errors.New("delete failed")}
	var queued string
	repo.queueFn = func(key string) error { queued = key; return nil }
	svc := NewService(repo)
	svc.SetProofStorage(storage, 1024, 5*time.Minute)
	_, err := svc.SubmitProof(context.Background(), SubmitProofInput{GroupID: "group", PaymentID: "payment", IdempotencyKey: "key", ContentType: "image/jpeg", Image: []byte{0xff, 0xd8, 0xff}})
	if !errors.Is(err, domain.ErrDebtsNotAwaiting) {
		t.Fatalf("error=%v", err)
	}
	want := "payments/payment/proofs/operation"
	if storage.deletes != 1 || queued != want {
		t.Fatalf("deletes=%d queued=%q, want %q", storage.deletes, queued, want)
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
