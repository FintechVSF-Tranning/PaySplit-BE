package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"paysplit-backend/internal/modules/sepay/domain"
	"paysplit-backend/internal/modules/sepay/usecase"
)

const testKey = "0123456789abcdef0123456789abcdef"

const samplePayload = `{"id":92704,"gateway":"Vietcombank","transactionDate":"2023-03-25 14:02:37",
"accountNumber":"0123499999","code":null,"content":"PAYAB3CD4EF chuyen tien","transferType":"in",
"transferAmount":2277000,"accumulated":19077000,"subAccount":null,"referenceCode":"MBVCB.3278907687",
"description":"","someFutureField":"x"}`

type stubRepo struct {
	inserted bool
	err      error
}

func (s stubRepo) SaveTransaction(context.Context, domain.Transaction) (bool, error) {
	return s.inserted, s.err
}

func (s stubRepo) MatchStatus(context.Context, int64) (*string, *string, error) {
	if s.inserted {
		return nil, nil, nil
	}
	status := "confirmed"
	return &status, nil, nil
}

func (s stubRepo) MarkProcessed(context.Context, int64, string, *string) error { return nil }

type stubSettler struct{}

func (stubSettler) Settle(context.Context, domain.SettleRequest) (domain.SettleResult, error) {
	id := "0199a000-0000-7000-8000-000000000001"
	return domain.SettleResult{Outcome: "confirmed", PaymentID: &id}, nil
}

func serve(t *testing.T, repo stubRepo, apiKey, authHeader, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	NewHandler(usecase.NewService(repo, stubSettler{}), apiKey).RegisterRoutes(r)
	req := httptest.NewRequest(http.MethodPost, "/sepay", strings.NewReader(body))
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestReceiveWebhookSuccessBodyHasSuccessTrue(t *testing.T) {
	rec := serve(t, stubRepo{inserted: true}, testKey, "Apikey "+testKey, samplePayload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			PaymentReference *string `json:"payment_reference"`
			MatchStatus      string  `json:"match_status"`
			PaymentID        *string `json:"payment_id"`
			Duplicate        bool    `json:"duplicate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Success || body.Data.PaymentReference == nil || *body.Data.PaymentReference != "PAYAB3CD4EF" || body.Data.Duplicate ||
		body.Data.MatchStatus != "confirmed" || body.Data.PaymentID == nil {
		t.Fatalf("unexpected body: %s", rec.Body)
	}
}

func TestReceiveWebhookDuplicateStillSucceeds(t *testing.T) {
	rec := serve(t, stubRepo{inserted: false}, testKey, "Apikey "+testKey, samplePayload)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"duplicate":true`) {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
}

func TestReceiveWebhookRejectsBadAuth(t *testing.T) {
	for name, header := range map[string]string{
		"missing":      "",
		"wrong key":    "Apikey nope",
		"wrong scheme": "Bearer " + testKey,
	} {
		t.Run(name, func(t *testing.T) {
			if rec := serve(t, stubRepo{inserted: true}, testKey, header, samplePayload); rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

func TestReceiveWebhookDisabledWithoutKey(t *testing.T) {
	if rec := serve(t, stubRepo{inserted: true}, "", "Apikey anything", samplePayload); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestReceiveWebhookValidationAndStorageErrors(t *testing.T) {
	if rec := serve(t, stubRepo{inserted: true}, testKey, "Apikey "+testKey, `{"id":1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid payload status = %d, want 400", rec.Code)
	}
	if rec := serve(t, stubRepo{inserted: true}, testKey, "Apikey "+testKey, `not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-json status = %d, want 400", rec.Code)
	}
	if rec := serve(t, stubRepo{err: errors.New("db down")}, testKey, "Apikey "+testKey, samplePayload); rec.Code != http.StatusInternalServerError {
		t.Fatalf("storage failure status = %d, want 500 so SePay retries", rec.Code)
	}
}

func TestReceiveWebhookAcceptsSepayTestButton(t *testing.T) {
	payload := `{"id":0,"gateway":"SePay","transactionDate":"2026-09-11 17:11:50","accountNumber":"0000000000","subAccount":null,"transferType":"in","transferAmount":10000,"accumulated":10000,"code":"SEPAYTEST","content":"SEPAY TEST WEBHOOK","referenceCode":"TEST1789121510","description":"SePay test webhook delivery"}`
	rec := serve(t, stubRepo{err: errors.New("must not be called")}, testKey, "Apikey "+testKey, payload)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) || !strings.Contains(rec.Body.String(), `"test_delivery"`) {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
}
