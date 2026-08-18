package http

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"paysplit-backend/internal/modules/admin/domain"
	"paysplit-backend/internal/modules/admin/usecase"
	"paysplit-backend/internal/transport/http/helpers"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

type Handler struct {
	service   *usecase.Service
	avatarURL func(string) string
}

func NewHandler(service *usecase.Service, avatarURL func(string) string) *Handler {
	if service == nil {
		panic("admin handler service must not be nil")
	}
	if avatarURL == nil {
		avatarURL = func(string) string { return "" }
	}
	return &Handler{
		service:   service,
		avatarURL: avatarURL,
	}
}

// ListAccounts xử lý GET /api/v1/admin/accounts (AC-1)
func (h *Handler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	page := 1
	if rawPage := r.URL.Query().Get("page"); rawPage != "" {
		p, err := strconv.Atoi(rawPage)
		if err != nil || p <= 0 {
			_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid page parameter", nil)
			return
		}
		page = p
	}

	limit := 20
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		l, err := strconv.Atoi(rawLimit)
		if err != nil || l <= 0 {
			_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid limit parameter", nil)
			return
		}
		limit = l
	}

	var status *string
	if rawStatus := strings.TrimSpace(r.URL.Query().Get("status")); rawStatus != "" {
		status = &rawStatus
	}

	var role *string
	if rawRole := strings.TrimSpace(r.URL.Query().Get("role")); rawRole != "" {
		role = &rawRole
	}

	search := r.URL.Query().Get("search")
	sortBy := r.URL.Query().Get("sort_by")
	sortOrder := r.URL.Query().Get("sort_order")

	out, err := h.service.ListAccounts(r.Context(), usecase.ListAccountsInput{
		Page:      page,
		Limit:     limit,
		Search:    search,
		Status:    status,
		Role:      role,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	items := make([]accountSummaryResponse, 0, len(out.Items))
	for _, item := range out.Items {
		items = append(items, newAccountSummaryResponse(item, h.avatarURL))
	}

	writeJSON(w, r, http.StatusOK, listAccountsResponse{
		Items: items,
		Pagination: paginationResponse{
			Total:      out.Pagination.Total,
			Page:       out.Pagination.Page,
			Limit:      out.Pagination.Limit,
			TotalPages: out.Pagination.TotalPages,
		},
	})
}

// GetAccountDetail xử lý GET /api/v1/admin/accounts/{id} (AC-2)
func (h *Handler) GetAccountDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid account id format", nil)
		return
	}

	detail, err := h.service.GetAccountDetail(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, r, http.StatusOK, newAccountDetailResponse(*detail, h.avatarURL))
}

// UpdateAccountStatus xử lý PUT /api/v1/admin/accounts/{id}/status (AC-3, AC-4)
func (h *Handler) UpdateAccountStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid account id format", nil)
		return
	}

	adminID, ok := authmw.UserID(r.Context())
	if !ok || strings.TrimSpace(adminID) == "" {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "authentication required", nil)
		return
	}

	var req updateStatusRequest
	if !read(w, r, &req) {
		return
	}

	safeUser, warning, err := h.service.UpdateAccountStatus(r.Context(), usecase.UpdateAccountStatusInput{
		TargetUserID: id,
		AdminID:      adminID,
		Status:       req.Status,
		Reason:       req.Reason,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, r, http.StatusOK, updateStatusResponse{
		Account: newAccountSummaryResponse(*safeUser, h.avatarURL),
		Warning: warningResponse{
			UnsettledDebtsCount:   warning.UnsettledDebtsCount,
			UnsettledCreditsCount: warning.UnsettledCreditsCount,
		},
	})
}

// GetSystemOverview xử lý GET /api/v1/admin/system/overview (AC-7)
func (h *Handler) GetSystemOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := h.service.GetSystemOverview(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, r, http.StatusOK, newSystemOverviewResponse(*overview))
}

func read(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid JSON payload", nil)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, data any) {
	if err := helpers.WriteJSON(w, status, data); err != nil {
		log.Printf("event=response_write_failed request_id=%s err=%v", chiMiddleware.GetReqID(r.Context()), err)
	}
}

func writeDomainError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "unable to process request"
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		status, code, message = http.StatusBadRequest, "VALIDATION_FAILED", "request validation failed"
	case errors.Is(err, domain.ErrReasonRequired):
		status, code, message = http.StatusBadRequest, "VALIDATION_FAILED", "reason is required"
	case errors.Is(err, domain.ErrInvalidStatusTransition):
		status, code, message = http.StatusBadRequest, "INVALID_STATUS_TRANSITION", "invalid status transition"
	case errors.Is(err, domain.ErrAccountNotFound):
		status, code, message = http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found"
	case errors.Is(err, domain.ErrCannotModifySelf):
		status, code, message = http.StatusForbidden, "CANNOT_MODIFY_SELF", "cannot modify self account status"
	case errors.Is(err, domain.ErrCannotModifyAdmin):
		status, code, message = http.StatusForbidden, "CANNOT_MODIFY_ADMIN", "cannot modify another admin account status"
	case errors.Is(err, domain.ErrForbidden):
		status, code, message = http.StatusForbidden, "INSUFFICIENT_PERMISSIONS", "insufficient permissions"
	}
	_ = helpers.WriteAPIError(w, status, code, message, nil)
}
