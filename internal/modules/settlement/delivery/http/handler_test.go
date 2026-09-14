package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"paysplit-backend/internal/modules/settlement/domain"
	"paysplit-backend/internal/modules/settlement/repository"
	"paysplit-backend/internal/modules/settlement/usecase"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

type markReceivedRepo struct {
	repository.Repository
	got repository.MarkReceivedInput
	err error
}

func (r *markReceivedRepo) MarkDebtReceived(_ context.Context, in repository.MarkReceivedInput) (*domain.Payment, []string, error) {
	r.got = in
	if r.err != nil {
		return nil, nil, r.err
	}
	return &domain.Payment{ID: "payment", Status: domain.PaymentConfirmed, ConfirmationSource: domain.ConfirmationManual}, []string{in.DebtID}, nil
}

func serveSettlement(repo repository.Repository, method, path, idempotencyKey string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	fakeAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(authmw.WithAuthContext(req.Context(), "creditor-user", "sid", "user")))
		})
	}
	NewHandler(usecase.NewService(repo), nil).RegisterRoutes(r, fakeAuth)
	req := httptest.NewRequest(method, path, nil)
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestMarkDebtReceivedRoute(t *testing.T) {
	repo := &markReceivedRepo{}
	rec := serveSettlement(repo, http.MethodPost, "/group-1/debts/debt-1/mark-received", "key-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if repo.got.GroupID != "group-1" || repo.got.DebtID != "debt-1" || repo.got.CallerUserID != "creditor-user" || repo.got.IdempotencyKey != "key-1" {
		t.Fatalf("unexpected input: %+v", repo.got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"confirmation_source":"manual"`) || !strings.Contains(body, `"settled_debts":["debt-1"]`) {
		t.Fatalf("unexpected body: %s", body)
	}

	if rec := serveSettlement(&markReceivedRepo{}, http.MethodPost, "/group-1/debts/debt-1/mark-received", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key status=%d, want 400", rec.Code)
	}
	if rec := serveSettlement(&markReceivedRepo{err: domain.ErrForbidden}, http.MethodPost, "/group-1/debts/debt-1/mark-received", "key-2"); rec.Code != http.StatusForbidden {
		t.Fatalf("debtor status=%d, want 403", rec.Code)
	}
}

func TestProofFlowRoutesAreGone(t *testing.T) {
	for _, action := range []string{"proof", "confirm", "reject"} {
		rec := serveSettlement(&markReceivedRepo{}, http.MethodPost, "/group-1/payments/payment-1/"+action, "key")
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST .../%s status=%d, want route removed", action, rec.Code)
		}
	}
}
