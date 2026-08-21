package http

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"paysplit-backend/internal/modules/settlement/domain"
)

func TestExpensePageResponse_AC1GroupsItemsUnderOneAllocation(t *testing.T) {
	debtID := "debt"
	page := &domain.ExpensePage{
		Summary: domain.ExpenseSummary{TotalOwed: 120, TotalSettled: 30, TotalReceivable: 50, NetBalance: -70},
		Items: []domain.ExpenseItem{
			{BillID: "bill", BillDate: time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC), ItemName: "A", ItemShare: 40, FinalAmount: 100, DebtID: &debtID, DebtStatus: "awaiting"},
			{BillID: "bill", BillDate: time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC), ItemName: "B", ItemShare: 60, FinalAmount: 100, DebtID: &debtID, DebtStatus: "awaiting"},
		},
	}
	response := expensePageResponse(page)
	bills := response["bills"].([]expenseBillResponse)
	if len(bills) != 1 || len(bills[0].Items) != 2 || bills[0].Allocation.ItemSubtotal != "100" || bills[0].DebtStatus == nil {
		t.Fatalf("unexpected grouped response: %+v", bills)
	}
	summary := response["summary"].(map[string]string)
	if summary["total_owed"] != "120" || summary["net_balance"] != "-70" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestPaymentResponse_AC5HidesInactiveRecipientAndQR(t *testing.T) {
	response := paymentResponse(&domain.Payment{ID: "payment", Status: domain.PaymentSuperseded})
	if response["recipient"] != nil || response["qr_payload"] != nil || response["qr_image_url"] != nil {
		t.Fatalf("superseded payment exposed active payment data: %+v", response)
	}
}

func TestDebtPageResponse_AC2MapsAvatarObjectsAtTheBoundary(t *testing.T) {
	debtor, creditor := "avatars/debtor", "avatars/creditor"
	h := NewHandlerForResponseTest(func(key string) string { return "signed:" + key })
	response := h.debtPageResponse(&domain.DebtPage{Debts: []domain.Debt{{ID: "debt", Amount: 42, DebtorAvatarObjectKey: &debtor, CreditorAvatarObjectKey: &creditor}}, CallerPayable: 42})
	debts := response["debts"].([]debtResponse)
	if len(debts) != 1 || debts[0].DebtorAvatarURL == nil || *debts[0].DebtorAvatarURL != "signed:avatars/debtor" || response["caller_payable"] != "42" {
		t.Fatalf("unexpected debt response: %+v", response)
	}
}

func NewHandlerForResponseTest(avatarURL func(string) string) *Handler {
	return &Handler{avatarURL: avatarURL}
}

func TestWriteError_AC3ThroughAC12MapsPublicErrorsWithoutDetails(t *testing.T) {
	cases := []struct {
		name, code string
		status     int
		err        error
	}{
		{"invalid", "VALIDATION_FAILED", 400, domain.ErrInvalidInput},
		{"outsider", "GROUP_NOT_FOUND", 404, domain.ErrGroupNotFound},
		{"bank", "BANK_ACCOUNT_REQUIRED", 422, domain.ErrBankAccountRequired},
		{"state", "PAYMENT_NOT_PENDING_PROOF", 409, domain.ErrPaymentNotPendingProof},
		{"storage", "STORAGE_UNAVAILABLE", 503, domain.ErrStorageUnavailable},
		{"reminder", "REMINDER_RATE_LIMITED", 429, domain.ErrReminderRateLimited},
		{"idempotency", "IDEMPOTENCY_IN_PROGRESS", 409, domain.ErrIdempotencyInProgress},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeError(recorder, tc.err)
			if recorder.Code != tc.status {
				t.Fatalf("status=%d, want %d", recorder.Code, tc.status)
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tc.code {
				t.Fatalf("code=%q, want %q", body.Error.Code, tc.code)
			}
			if tc.err == domain.ErrIdempotencyInProgress && recorder.Header().Get("Retry-After") != "1" {
				t.Fatalf("Retry-After=%q", recorder.Header().Get("Retry-After"))
			}
		})
	}
}
