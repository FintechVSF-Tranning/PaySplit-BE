package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"paysplit-backend/internal/modules/settlement/domain"
	"paysplit-backend/internal/modules/settlement/repository"
	"paysplit-backend/internal/modules/settlement/usecase"
	platformmetrics "paysplit-backend/internal/platform/metrics"
	"paysplit-backend/internal/transport/http/helpers"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

type Handler struct {
	service   *usecase.Service
	avatarURL func(string) string
}

func NewHandler(service *usecase.Service, avatarURL func(string) string) *Handler {
	if service == nil {
		panic("settlement handler service must not be nil")
	}
	if avatarURL == nil {
		avatarURL = func(string) string { return "" }
	}
	return &Handler{service: service, avatarURL: avatarURL}
}

func (h *Handler) RegisterRoutes(r chi.Router, auth func(http.Handler) http.Handler) {
	r.Group(func(protected chi.Router) {
		protected.Use(auth)
		protected.Get("/{groupId}/expenses/me", h.ListExpenses)
		protected.Get("/{groupId}/debts", h.ListDebts)
		protected.Post("/{groupId}/payments/qr", h.GeneratePayment)
		protected.Get("/{groupId}/payments/{paymentId}", h.GetPayment)
		protected.Post("/{groupId}/debts/{debtId}/remind", h.RemindDebt)
		protected.Post("/{groupId}/debts/{debtId}/mark-received", h.MarkDebtReceived)
	})
}

func (h *Handler) RemindDebt(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	result, err := h.service.RemindDebt(r.Context(), usecase.RemindInput{GroupID: chi.URLParam(r, "groupId"), CallerUserID: userID, DebtID: chi.URLParam(r, "debtId"), IdempotencyKey: r.Header.Get("Idempotency-Key")})
	recordOperation("remind", err)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = helpers.WriteJSON(w, http.StatusOK, map[string]any{"debt_id": result.DebtID, "reminder_count": result.ReminderCount, "reminded_at": result.RemindedAt})
}

// MarkDebtReceived là đường dự phòng khi ngân hàng không tự khớp được giao
// dịch: người nhận tự xác nhận đã nhận đủ tiền của đúng khoản nợ này.
func (h *Handler) MarkDebtReceived(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	payment, ids, err := h.service.MarkDebtReceived(r.Context(), usecase.MarkReceivedInput{GroupID: chi.URLParam(r, "groupId"), CallerUserID: userID, DebtID: chi.URLParam(r, "debtId"), IdempotencyKey: r.Header.Get("Idempotency-Key")})
	recordOperation("mark_received", err)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = helpers.WriteJSON(w, http.StatusOK, map[string]any{"payment": paymentResponse(payment), "settled_debts": ids})
}

type generatePaymentRequest struct {
	CreditorMemberID string   `json:"creditor_member_id"`
	DebtIDs          []string `json:"debt_ids"`
}

func (h *Handler) GeneratePayment(w http.ResponseWriter, r *http.Request) {
	var req generatePaymentRequest
	if err := helpers.ReadJSON(w, r, &req); err != nil {
		_ = helpers.WriteAPIError(w, 400, "VALIDATION_FAILED", "invalid request body", nil)
		return
	}
	userID, _ := authmw.UserID(r.Context())
	payment, created, err := h.service.GeneratePayment(r.Context(), usecase.GeneratePaymentInput{GroupID: chi.URLParam(r, "groupId"), CallerUserID: userID, CreditorMemberID: req.CreditorMemberID, DebtIDs: req.DebtIDs, IdempotencyKey: r.Header.Get("Idempotency-Key")})
	recordOperation("create_qr", err)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	_ = helpers.WriteJSON(w, status, map[string]any{"payment": paymentResponse(payment)})
}
func (h *Handler) GetPayment(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	payment, err := h.service.GetPayment(r.Context(), chi.URLParam(r, "groupId"), userID, chi.URLParam(r, "paymentId"))
	if err != nil {
		writeError(w, err)
		return
	}
	_ = helpers.WriteJSON(w, http.StatusOK, map[string]any{"payment": paymentResponse(payment)})
}

func recordOperation(operation string, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	platformmetrics.RecordSettlementOperation(operation, outcome)
}

func listParams(w http.ResponseWriter, r *http.Request) (int, *string, bool) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "limit must be between 1 and 100", nil)
			return 0, nil, false
		}
		limit = parsed
	}
	var cursor *string
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor = &raw
	}
	return limit, cursor, true
}

func (h *Handler) ListExpenses(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := listParams(w, r)
	if !ok {
		return
	}
	userID, _ := authmw.UserID(r.Context())
	page, err := h.service.ListExpenses(r.Context(), repository.ListInput{GroupID: chi.URLParam(r, "groupId"), CallerUserID: userID, Cursor: cursor, Limit: limit})
	if err != nil {
		writeError(w, err)
		return
	}
	_ = helpers.WriteJSON(w, http.StatusOK, expensePageResponse(page))
}

func (h *Handler) ListDebts(w http.ResponseWriter, r *http.Request) {
	limit, cursor, ok := listParams(w, r)
	if !ok {
		return
	}
	userID, _ := authmw.UserID(r.Context())
	input := repository.ListDebtsInput{ListInput: repository.ListInput{GroupID: chi.URLParam(r, "groupId"), CallerUserID: userID, Cursor: cursor, Limit: limit}}
	if value := r.URL.Query().Get("debtor_member_id"); value != "" {
		input.DebtorID = &value
	}
	if value := r.URL.Query().Get("creditor_member_id"); value != "" {
		input.CreditorID = &value
	}
	if value := r.URL.Query().Get("status"); value != "" {
		input.Status = &value
	}
	page, err := h.service.ListDebts(r.Context(), input)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = helpers.WriteJSON(w, http.StatusOK, h.debtPageResponse(page))
}

func writeError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "unable to process request"
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		status, code, message = http.StatusBadRequest, "VALIDATION_FAILED", "request validation failed"
	case errors.Is(err, domain.ErrInvalidCursor):
		status, code, message = http.StatusBadRequest, "INVALID_CURSOR", "cursor is invalid"
	case errors.Is(err, domain.ErrGroupNotFound):
		status, code, message = http.StatusNotFound, "GROUP_NOT_FOUND", "group not found"
	case errors.Is(err, domain.ErrPaymentNotFound):
		status, code, message = http.StatusNotFound, "PAYMENT_NOT_FOUND", "payment not found"
	case errors.Is(err, domain.ErrCreditorNotFound):
		status, code, message = http.StatusNotFound, "CREDITOR_NOT_FOUND", "creditor not found"
	case errors.Is(err, domain.ErrForbidden):
		status, code, message = http.StatusForbidden, "FORBIDDEN", "forbidden"
	case errors.Is(err, domain.ErrBankAccountRequired):
		status, code, message = http.StatusUnprocessableEntity, "BANK_ACCOUNT_REQUIRED", "creditor bank account is required"
	case errors.Is(err, domain.ErrDebtsNotAwaiting):
		status, code, message = http.StatusConflict, "DEBTS_NOT_AWAITING", "debts are not awaiting"
	case errors.Is(err, domain.ErrIdempotencyConflict):
		status, code, message = http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key was reused"
	case errors.Is(err, domain.ErrIdempotencyInProgress):
		status, code, message = http.StatusConflict, "IDEMPOTENCY_IN_PROGRESS", "operation is in progress"
		w.Header().Set("Retry-After", "1")
	case errors.Is(err, domain.ErrDebtNotFound):
		status, code, message = http.StatusNotFound, "DEBT_NOT_FOUND", "debt not found"
	case errors.Is(err, domain.ErrDebtNotAwaiting):
		status, code, message = http.StatusConflict, "DEBT_NOT_AWAITING", "debt is not awaiting"
	case errors.Is(err, domain.ErrReminderRateLimited):
		status, code, message = http.StatusTooManyRequests, "REMINDER_RATE_LIMITED", "debt reminder is rate limited"
	}
	_ = helpers.WriteAPIError(w, status, code, message, nil)
}
