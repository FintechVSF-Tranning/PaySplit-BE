package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"paysplit-backend/internal/modules/sepay/domain"
)

type fakeRepo struct {
	saved  map[int64]domain.Transaction
	status map[int64]string
	err    error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{saved: map[int64]domain.Transaction{}, status: map[int64]string{}}
}

func (f *fakeRepo) MatchStatus(_ context.Context, id int64) (*string, *string, error) {
	if v, ok := f.status[id]; ok {
		return &v, nil, nil
	}
	return nil, nil, nil
}

func (f *fakeRepo) MarkProcessed(_ context.Context, id int64, status string, _ *string) error {
	if _, ok := f.status[id]; !ok {
		f.status[id] = status
	}
	return nil
}

type fakeSettler struct {
	calls []domain.SettleRequest
	res   domain.SettleResult
	err   error
}

func (f *fakeSettler) Settle(_ context.Context, req domain.SettleRequest) (domain.SettleResult, error) {
	f.calls = append(f.calls, req)
	return f.res, f.err
}

func newService(repo *fakeRepo) (*Service, *fakeSettler) {
	settler := &fakeSettler{res: domain.SettleResult{Outcome: "confirmed"}}
	return NewService(repo, settler), settler
}

func (f *fakeRepo) SaveTransaction(_ context.Context, tx domain.Transaction) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if _, ok := f.saved[tx.ID]; ok {
		return false, nil
	}
	f.saved[tx.ID] = tx
	return true, nil
}

func validInput() ReceiveInput {
	return ReceiveInput{
		ID: 92704, Gateway: "Vietcombank", TransactionDate: "2023-03-25 14:02:37",
		AccountNumber: "0123499999", Content: "NGUYEN VAN A chuyen tien payab3cd4ef FT123",
		TransferType: "in", TransferAmount: 2277000, Accumulated: 19077000,
		RawPayload: []byte(`{"id":92704}`),
	}
}

func TestReceiveStoresTransactionAndExtractsReference(t *testing.T) {
	repo := newFakeRepo()
	svc, settler := newService(repo)
	result, err := svc.Receive(context.Background(), validInput())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if result.Duplicate || result.MatchStatus != "confirmed" || repo.status[92704] != "confirmed" {
		t.Fatalf("unexpected result %+v, stored status %q", result, repo.status[92704])
	}
	if len(settler.calls) != 1 || settler.calls[0].ReferenceCode != "PAYAB3CD4EF" || settler.calls[0].Amount != 2277000 || settler.calls[0].AccountNumbers[0] != "0123499999" {
		t.Fatalf("unexpected settle calls: %+v", settler.calls)
	}
	tx := repo.saved[92704]
	if tx.PaymentReference == nil || *tx.PaymentReference != "PAYAB3CD4EF" {
		t.Fatalf("PaymentReference = %v, want PAYAB3CD4EF", tx.PaymentReference)
	}
	want := time.Date(2023, 3, 25, 7, 2, 37, 0, time.UTC)
	if !tx.TransactionDate.Equal(want) {
		t.Fatalf("TransactionDate = %v, want %v (UTC+7 quy đổi)", tx.TransactionDate.UTC(), want)
	}
}

func TestReceivePrefersCodeOverContent(t *testing.T) {
	in := validInput()
	code := "PAYZZZZ2222"
	in.Code = &code
	repo := newFakeRepo()
	svc, _ := newService(repo)
	if _, err := svc.Receive(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if got := *repo.saved[in.ID].PaymentReference; got != code {
		t.Fatalf("PaymentReference = %s, want %s", got, code)
	}
}

func TestReceiveWithoutReference(t *testing.T) {
	in := validInput()
	in.Content = "chuyen tien an trua" // không có mã; PAYI0000000 cũng không hợp lệ vì chứa I/0
	repo := newFakeRepo()
	svc, settler := newService(repo)
	if _, err := svc.Receive(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if ref := repo.saved[in.ID].PaymentReference; ref != nil {
		t.Fatalf("PaymentReference = %s, want nil", *ref)
	}
	if len(settler.calls) != 0 || repo.status[in.ID] != domain.MatchNoReference {
		t.Fatalf("settle calls=%d status=%q", len(settler.calls), repo.status[in.ID])
	}
}

func TestReceiveDuplicateIsIdempotent(t *testing.T) {
	repo := newFakeRepo()
	svc, settler := newService(repo)
	if _, err := svc.Receive(context.Background(), validInput()); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Receive(context.Background(), validInput())
	if err != nil || !result.Duplicate || result.MatchStatus != "confirmed" {
		t.Fatalf("second delivery: %+v err=%v, want duplicate with stored status", result, err)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settled %d times, want once", len(settler.calls))
	}
}

func TestReceiveRejectsInvalidInput(t *testing.T) {
	cases := map[string]func(*ReceiveInput){
		"negative id":     func(in *ReceiveInput) { in.ID = -1 },
		"bad type":        func(in *ReceiveInput) { in.TransferType = "sideways" },
		"negative amount": func(in *ReceiveInput) { in.TransferAmount = -1 },
		"bad date":        func(in *ReceiveInput) { in.TransactionDate = "yesterday" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := validInput()
			mutate(&in)
			svc, _ := newService(newFakeRepo())
			_, err := svc.Receive(context.Background(), in)
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestReceiveOutgoingIsNotSettled(t *testing.T) {
	in := validInput()
	in.TransferType = "out"
	repo := newFakeRepo()
	svc, settler := newService(repo)
	if _, err := svc.Receive(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(settler.calls) != 0 || repo.status[in.ID] != domain.MatchIgnoredOutgoing {
		t.Fatalf("settle calls=%d status=%q", len(settler.calls), repo.status[in.ID])
	}
}

func TestReceiveSettleFailureLeavesTransactionForRetry(t *testing.T) {
	repo := newFakeRepo()
	svc, settler := newService(repo)
	settler.err = errors.New("db down")
	if _, err := svc.Receive(context.Background(), validInput()); err == nil {
		t.Fatal("want error so SePay retries")
	}
	if _, ok := repo.status[92704]; ok {
		t.Fatal("failed settlement must not be marked processed")
	}
	settler.err = nil
	result, err := svc.Receive(context.Background(), validInput())
	if err != nil || !result.Duplicate || result.MatchStatus != "confirmed" || len(settler.calls) != 2 {
		t.Fatalf("retry: %+v err=%v calls=%d", result, err, len(settler.calls))
	}
}

func TestReceivePassesSubAccountToSettler(t *testing.T) {
	in := validInput()
	va := "VA123"
	in.SubAccount = &va
	svc, settler := newService(newFakeRepo())
	if _, err := svc.Receive(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if got := settler.calls[0].AccountNumbers; len(got) != 2 || got[1] != "VA123" {
		t.Fatalf("AccountNumbers = %v", got)
	}
}

func TestReceiveAcceptsSparseTestPayload(t *testing.T) {
	cases := map[string]func(*ReceiveInput){
		"no gateway":   func(in *ReceiveInput) { in.Gateway = "" },
		"no account":   func(in *ReceiveInput) { in.AccountNumber = "" },
		"no date":      func(in *ReceiveInput) { in.TransactionDate = "" },
		"iso date":     func(in *ReceiveInput) { in.TransactionDate = "2024-07-02T11:08:33" },
		"rfc3339 date": func(in *ReceiveInput) { in.TransactionDate = "2024-07-02T11:08:33+07:00" },
		"upper type":   func(in *ReceiveInput) { in.TransferType = "IN" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := validInput()
			mutate(&in)
			svc, _ := newService(newFakeRepo())
			if _, err := svc.Receive(context.Background(), in); err != nil {
				t.Fatalf("Receive() error = %v", err)
			}
		})
	}
}

func TestReceiveZeroAmountIsNotSettled(t *testing.T) {
	in := validInput()
	in.TransferAmount = 0
	repo := newFakeRepo()
	svc, settler := newService(repo)
	if _, err := svc.Receive(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(settler.calls) != 0 || repo.status[in.ID] != domain.MatchZeroAmount {
		t.Fatalf("settle calls=%d status=%q", len(settler.calls), repo.status[in.ID])
	}
}

func TestReceiveSepayTestDeliveryIsAcknowledgedWithoutSaving(t *testing.T) {
	in := validInput()
	in.ID = 0 // payload thật của nút "Gửi thử"
	in.Gateway, in.AccountNumber, in.Content = "SePay", "0000000000", "SEPAY TEST WEBHOOK"
	repo := newFakeRepo()
	svc, settler := newService(repo)
	result, err := svc.Receive(context.Background(), in)
	if err != nil || result.MatchStatus != domain.MatchTestDelivery {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(repo.saved) != 0 || len(settler.calls) != 0 {
		t.Fatalf("test delivery must not be saved or settled: saved=%d calls=%d", len(repo.saved), len(settler.calls))
	}
}
