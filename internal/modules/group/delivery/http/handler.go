package http

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"

	"paysplit-backend/internal/modules/group/domain"
	"paysplit-backend/internal/modules/group/usecase"
	"paysplit-backend/internal/transport/http/helpers"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

type Handler struct {
	service   *usecase.Service
	avatarURL func(string) string
}

func NewHandler(service *usecase.Service, avatarURL func(string) string) *Handler {
	if service == nil {
		panic("group handler service must not be nil")
	}
	if avatarURL == nil {
		avatarURL = func(string) string { return "" }
	}
	return &Handler{service: service, avatarURL: avatarURL}
}

func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var req createGroupRequest
	if !read(w, r, &req) {
		return
	}
	userID, _ := authmw.UserID(r.Context())
	out, err := h.service.CreateGroup(r.Context(), usecase.CreateGroupInput{Name: req.Name, Currency: req.Currency, CreatedBy: userID})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, map[string]any{
		"group":      newGroupResponse(*out.Group),
		"membership": newMembershipResponse(*out.Membership),
	})
}

func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	limit, ok := parseListLimit(w, r)
	if !ok {
		return
	}
	var cursor *string
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor = &raw
	}
	out, err := h.service.ListGroups(r.Context(), usecase.ListGroupsInput{UserID: userID, Cursor: cursor, Limit: limit})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	items := make([]groupListItemResponse, 0, len(out.Items))
	for _, item := range out.Items {
		items = append(items, newGroupListItemResponse(item))
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"groups": items, "next_cursor": out.NextCursor})
}

func (h *Handler) GetGroupDetail(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	groupID := chi.URLParam(r, "id")
	detail, err := h.service.GetGroupDetail(r.Context(), groupID, userID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, r, http.StatusOK, h.newGroupDetailResponse(*detail))
}

func (h *Handler) CreateInvite(w http.ResponseWriter, r *http.Request) {
	var req createInviteRequest
	if !readOptional(w, r, &req) {
		return
	}
	userID, _ := authmw.UserID(r.Context())
	groupID := chi.URLParam(r, "id")
	hasConfiguration := req.hasConfiguration()
	if hasConfiguration {
		// Presence is authoritative for authorization. Values remain raw until
		// after this check so malformed policy fields from a non-Captain still
		// receive the contractually required 403 response.
		if err := h.service.AuthorizeInviteConfiguration(r.Context(), groupID, userID); err != nil {
			writeDomainError(w, err)
			return
		}
	}
	if err := req.decodePolicy(); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body", nil)
		return
	}
	var expiresInHours *int
	if req.ExpiresInHours.Set && !req.ExpiresInHours.Null {
		expiresInHours = &req.ExpiresInHours.Value
	}
	var maxUses *int
	if req.MaxUses.Set && !req.MaxUses.Null {
		maxUses = &req.MaxUses.Value
	}
	var regenerate *bool
	if req.Regenerate.Set && !req.Regenerate.Null {
		regenerate = &req.Regenerate.Value
	}
	out, err := h.service.CreateInvite(r.Context(), usecase.CreateInviteInput{
		GroupID:              groupID,
		CallerUserID:         userID,
		ExpiresInHours:       expiresInHours,
		MaxUses:              maxUses,
		Regenerate:           regenerate,
		HasConfiguration:     hasConfiguration,
		HasNullConfiguration: req.hasNullConfiguration(),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	status := http.StatusOK
	if out.Created {
		status = http.StatusCreated
	}
	writeJSON(w, r, status, map[string]any{"invite": newInviteResponse(*out.Invite, out.InviteURL)})
}

func (h *Handler) ListInvites(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	items, err := h.service.ListAvailableInvites(r.Context(), chi.URLParam(r, "id"), userID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	invites := make([]inviteResponse, 0, len(items))
	for _, item := range items {
		invites = append(invites, newInviteResponse(item.Invite, item.InviteURL))
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"invites": invites})
}

func (h *Handler) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	groupID := chi.URLParam(r, "id")
	inviteID := chi.URLParam(r, "inviteId")
	if err := h.service.RevokeInvite(r.Context(), groupID, inviteID, userID); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) PreviewInvite(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	preview, err := h.service.PreviewInvite(r.Context(), code)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"preview": newInvitePreviewResponse(*preview)})
}

func (h *Handler) JoinGroup(w http.ResponseWriter, r *http.Request) {
	var req joinGroupRequest
	if !read(w, r, &req) {
		return
	}
	userID, _ := authmw.UserID(r.Context())
	result, err := h.service.JoinGroup(r.Context(), req.Code, userID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"join": newJoinResultResponse(*result)})
}

func (h *Handler) LeaveOrRemoveMember(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	groupID := chi.URLParam(r, "id")
	memberID := chi.URLParam(r, "memberId")
	if err := h.service.LeaveOrRemoveMember(r.Context(), groupID, memberID, userID); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) TransferRole(w http.ResponseWriter, r *http.Request) {
	var req transferRoleRequest
	if !read(w, r, &req) {
		return
	}
	if req.Role != domain.RoleCaptain {
		writeDomainError(w, domain.ErrInvalidInput)
		return
	}
	userID, _ := authmw.UserID(r.Context())
	groupID := chi.URLParam(r, "id")
	memberID := chi.URLParam(r, "memberId")
	transfer, err := h.service.TransferCaptain(r.Context(), groupID, memberID, userID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"transfer": newCaptainTransferResponse(*transfer)})
}

func (h *Handler) RenameGroup(w http.ResponseWriter, r *http.Request) {
	var req renameGroupRequest
	if !read(w, r, &req) {
		return
	}
	userID, _ := authmw.UserID(r.Context())
	group, err := h.service.RenameGroup(r.Context(), chi.URLParam(r, "id"), userID, req.Name)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"group": newGroupResponse(*group)})
}

func (h *Handler) DisbandGroup(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	if err := h.service.DisbandGroup(r.Context(), chi.URLParam(r, "id"), userID); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListActivities(w http.ResponseWriter, r *http.Request) {
	userID, _ := authmw.UserID(r.Context())
	groupID := chi.URLParam(r, "id")
	limit, ok := parseListLimit(w, r)
	if !ok {
		return
	}
	var cursor *string
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor = &raw
	}
	out, err := h.service.ListActivities(r.Context(), usecase.ListActivitiesInput{GroupID: groupID, CallerUserID: userID, Cursor: cursor, Limit: limit})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	activities := make([]activityResponse, 0, len(out.Items))
	for _, a := range out.Items {
		activities = append(activities, h.newActivityResponse(a))
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"activities": activities, "next_cursor": out.NextCursor})
}

// parseListLimit reads the optional limit query param, defaulting to 20 and
// accepting 1 through 100 per spec 0002.
func parseListLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 20, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 100 {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "limit must be between 1 and 100", nil)
		return 0, false
	}
	return limit, true
}

func read(w http.ResponseWriter, r *http.Request, d any) bool {
	if err := helpers.ReadJSON(w, r, d); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body", nil)
		return false
	}
	return true
}

func readOptional(w http.ResponseWriter, r *http.Request, d any) bool {
	if err := helpers.ReadOptionalJSON(w, r, d); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body", nil)
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, r *http.Request, status int, data any) {
	if err := helpers.WriteJSON(w, status, data); err != nil {
		log.Printf("event=response_write_failed request_id=%s", chiMiddleware.GetReqID(r.Context()))
	}
}
func writeDomainError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "INTERNAL_ERROR", "unable to process request"
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		status, code, message = http.StatusBadRequest, "VALIDATION_FAILED", "request validation failed"
	case errors.Is(err, domain.ErrInvalidCursor):
		status, code, message = http.StatusBadRequest, "INVALID_CURSOR", "cursor is invalid"
	case errors.Is(err, domain.ErrGroupNotFound):
		status, code, message = http.StatusNotFound, "GROUP_NOT_FOUND", "group not found"
	case errors.Is(err, domain.ErrCaptainRequired):
		status, code, message = http.StatusForbidden, "CAPTAIN_REQUIRED", "the active Captain must perform this action"
	case errors.Is(err, domain.ErrInviteNotFound), errors.Is(err, domain.ErrInviteUnavailable):
		status, code, message = http.StatusNotFound, "INVITE_NOT_FOUND", "invite not found"
	case errors.Is(err, domain.ErrGroupMemberLimitReached):
		status, code, message = http.StatusConflict, "GROUP_MEMBER_LIMIT_REACHED", "group has reached its member limit"
	case errors.Is(err, domain.ErrForbidden):
		status, code, message = http.StatusForbidden, "FORBIDDEN", "not authorized to perform this action"
	case errors.Is(err, domain.ErrCaptainTransferRequired):
		status, code, message = http.StatusConflict, "CAPTAIN_TRANSFER_REQUIRED", "transfer the Captain role before this action"
	case errors.Is(err, domain.ErrMemberNotFound):
		status, code, message = http.StatusNotFound, "MEMBER_NOT_FOUND", "member not found"
	case errors.Is(err, domain.ErrCaptainTransferConflict):
		status, code, message = http.StatusConflict, "CAPTAIN_TRANSFER_CONFLICT", "another transfer is already in progress"
	}
	var openDebts *domain.OpenDebtsError
	if errors.As(err, &openDebts) {
		status, code, message = http.StatusConflict, "GROUP_MEMBER_HAS_OPEN_DEBTS", "member has unsettled debts"
		fields := map[string]string{
			"payable_amount":    strconv.FormatInt(openDebts.PayableAmount, 10),
			"receivable_amount": strconv.FormatInt(openDebts.ReceivableAmount, 10),
		}
		_ = helpers.WriteAPIError(w, status, code, message, fields)
		return
	}
	var obligations *domain.UnsettledObligationsError
	if errors.As(err, &obligations) {
		status, code, message = http.StatusConflict, "GROUP_HAS_UNSETTLED_OBLIGATIONS", "group has unsettled obligations"
		fields := map[string]string{
			"draft_or_reviewed_bill_count": strconv.FormatInt(obligations.DraftOrReviewedBillCount, 10),
			"open_debt_count":              strconv.FormatInt(obligations.OpenDebtCount, 10),
		}
		_ = helpers.WriteAPIError(w, status, code, message, fields)
		return
	}
	var bulkErr *domain.BulkFinalizeInProgressError
	if errors.As(err, &bulkErr) {
		fields := map[string]string{"active_batch_id": bulkErr.ActiveBatchID}
		_ = helpers.WriteAPIError(w, http.StatusConflict, "BULK_FINALIZE_IN_PROGRESS", "another bulk finalize is already in progress", fields)
		return
	}
	_ = helpers.WriteAPIError(w, status, code, message, nil)
}
