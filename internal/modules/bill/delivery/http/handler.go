package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/usecase"
	"paysplit-backend/internal/transport/http/helpers"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

// Handler xử lý các HTTP endpoints cho module Bill & OCR.
type Handler struct {
	service    *usecase.Service
	sseHandler *SSEHandler
}

// NewHandler khởi tạo Handler mới.
func NewHandler(service *usecase.Service, sseHandler *SSEHandler) *Handler {
	if service == nil {
		panic("bill handler service must not be nil")
	}
	return &Handler{
		service:    service,
		sseHandler: sseHandler,
	}
}

// RegisterRoutes đăng ký toàn bộ routes của module Bill.
func (h *Handler) RegisterRoutes(r chi.Router, authMiddleware func(http.Handler) http.Handler) {
	r.Group(func(protected chi.Router) {
		protected.Use(authMiddleware)
		protected.Post("/", h.CreateBill)
		protected.Get("/", h.ListBills)
		protected.Get("/{id}", h.GetBillDetail)
		if h.sseHandler != nil {
			protected.Get("/{id}/events", h.sseHandler.StreamBillEvents)
		}
		protected.Post("/{id}/ocr-retry", h.RetryOCR)
		protected.Post("/{id}/apply-candidate", h.ApplyCandidate)
		protected.Patch("/{id}", h.UpdateDraftBill)
		protected.Post("/{id}/review", h.ReviewBill)
		protected.Post("/{id}/finalize", h.FinalizeBill)
		protected.Post("/{id}/void", h.VoidBill)
		protected.Delete("/{id}", h.DeleteDraftBill)
	})
}

// CreateBill xử lý POST /api/v1/bills (hỗ trợ multipart upload 1-5 ảnh hoặc JSON tạo thủ công).
func (h *Handler) CreateBill(w http.ResponseWriter, r *http.Request) {
	userIDStr, _ := authmw.UserID(r.Context())
	callerUserID, err := uuid.Parse(userIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}

	var req usecase.CreateBillRequest
	contentType := r.Header.Get("Content-Type")

	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(10 << 20); err != nil { // 10MB max
			_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_MULTIPART", "failed to parse multipart form", nil)
			return
		}

		// Đọc metadata JSON
		metaJSON := r.FormValue("metadata")
		if metaJSON != "" {
			_ = json.Unmarshal([]byte(metaJSON), &req)
		} else {
			groupIDStr := r.FormValue("group_id")
			if gID, err := uuid.Parse(groupIDStr); err == nil {
				req.GroupID = gID
			}
			merchant := r.FormValue("merchant_name")
			if merchant != "" {
				req.MerchantName = &merchant
			}
		}

		// Đọc các file ảnh từ field "images"
		if r.MultipartForm != nil && r.MultipartForm.File != nil {
			fileHeaders := r.MultipartForm.File["images"]
			if len(fileHeaders) > 5 {
				_ = helpers.WriteAPIError(w, http.StatusBadRequest, "TOO_MANY_IMAGES", "maximum 5 images allowed", nil)
				return
			}
			for _, fh := range fileHeaders {
				f, err := fh.Open()
				if err != nil {
					continue
				}
				data, err := io.ReadAll(f)
				f.Close()
				if err == nil {
					req.Files = append(req.Files, data)
				}
			}
		}
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", nil)
			return
		}
	}

	result, err := h.service.CreateBill(r.Context(), callerUserID, req)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	statusCode := http.StatusCreated
	if result.IsAccepted {
		statusCode = http.StatusAccepted
	}

	_ = helpers.WriteJSON(w, statusCode, result)
}

// GetBillDetail xử lý GET /api/v1/bills/{id}?group_id=...
func (h *Handler) GetBillDetail(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	billID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_BILL_ID", "invalid bill ID", nil)
		return
	}

	groupIDStr := r.URL.Query().Get("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "group_id query param is required", nil)
		return
	}

	detail, err := h.service.GetBillDetail(r.Context(), callerUserID, billID, groupID)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, detail)
}

// ListBills xử lý GET /api/v1/bills?group_id=...&limit=...&offset=...
func (h *Handler) ListBills(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	groupIDStr := r.URL.Query().Get("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "group_id query param is required", nil)
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	bills, err := h.service.ListBills(r.Context(), callerUserID, groupID, int32(limit), int32(offset))
	if err != nil {
		writeDomainError(w, err)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, map[string]any{
		"bills": bills,
	})
}

// RetryOCR xử lý POST /api/v1/bills/{id}/ocr-retry?group_id=...
func (h *Handler) RetryOCR(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	billID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_BILL_ID", "invalid bill ID", nil)
		return
	}

	groupIDStr := r.URL.Query().Get("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "group_id query param is required", nil)
		return
	}

	job, err := h.service.RetryOCR(r.Context(), callerUserID, billID, groupID)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusAccepted, map[string]any{
		"ocr_job": job,
	})
}

// ApplyCandidate xử lý POST /api/v1/bills/{id}/apply-candidate?group_id=...
func (h *Handler) ApplyCandidate(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	billID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_BILL_ID", "invalid bill ID", nil)
		return
	}

	groupIDStr := r.URL.Query().Get("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "group_id query param is required", nil)
		return
	}

	var body struct {
		JobID   uuid.UUID `json:"job_id"`
		Version int32     `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", nil)
		return
	}

	updatedBill, err := h.service.ApplyCandidate(r.Context(), callerUserID, billID, groupID, body.JobID, body.Version)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, map[string]any{
		"bill": updatedBill,
	})
}

// UpdateDraftBill xử lý PATCH /api/v1/bills/{id}?group_id=...
func (h *Handler) UpdateDraftBill(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	billID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_BILL_ID", "invalid bill ID", nil)
		return
	}

	groupIDStr := r.URL.Query().Get("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "group_id query param is required", nil)
		return
	}

	var req usecase.UpdateDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", nil)
		return
	}

	updatedBill, err := h.service.UpdateDraftBill(r.Context(), callerUserID, billID, groupID, req)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, map[string]any{
		"bill": updatedBill,
	})
}

// ReviewBill xử lý POST /api/v1/bills/{id}/review?group_id=...
func (h *Handler) ReviewBill(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	billID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_BILL_ID", "invalid bill ID", nil)
		return
	}

	groupIDStr := r.URL.Query().Get("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "group_id query param is required", nil)
		return
	}

	var body struct {
		Version int32 `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	reviewedBill, err := h.service.ReviewBill(r.Context(), callerUserID, billID, groupID, body.Version)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, map[string]any{
		"bill": reviewedBill,
	})
}

// FinalizeBill xử lý POST /api/v1/bills/{id}/finalize?group_id=...
func (h *Handler) FinalizeBill(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	billID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_BILL_ID", "invalid bill ID", nil)
		return
	}

	groupIDStr := r.URL.Query().Get("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "group_id query param is required", nil)
		return
	}

	var body struct {
		Version int32 `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	finalizedBill, err := h.service.FinalizeBill(r.Context(), callerUserID, billID, groupID, body.Version)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, map[string]any{
		"bill": finalizedBill,
	})
}

// VoidBill xử lý POST /api/v1/bills/{id}/void?group_id=...
func (h *Handler) VoidBill(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	billID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_BILL_ID", "invalid bill ID", nil)
		return
	}

	groupIDStr := r.URL.Query().Get("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "group_id query param is required", nil)
		return
	}

	var body struct {
		Version int32  `json:"version"`
		Reason  string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	voidedBill, err := h.service.VoidBill(r.Context(), callerUserID, billID, groupID, body.Version, body.Reason)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, map[string]any{
		"bill": voidedBill,
	})
}

// DeleteDraftBill xử lý DELETE /api/v1/bills/{id}?group_id=...
func (h *Handler) DeleteDraftBill(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	billID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_BILL_ID", "invalid bill ID", nil)
		return
	}

	groupIDStr := r.URL.Query().Get("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "group_id query param is required", nil)
		return
	}

	err = h.service.DeleteDraftBill(r.Context(), callerUserID, billID, groupID)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func getUserID(r *http.Request) uuid.UUID {
	str, _ := authmw.UserID(r.Context())
	id, _ := uuid.Parse(str)
	return id
}

func writeDomainError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "INTERNAL_ERROR"
	msg := err.Error()

	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		status = http.StatusBadRequest
		code = "VALIDATION_FAILED"
	case errors.Is(err, domain.ErrForbidden):
		status = http.StatusForbidden
		code = "FORBIDDEN"
	case errors.Is(err, domain.ErrBillNotFound):
		status = http.StatusNotFound
		code = "BILL_NOT_FOUND"
	case errors.Is(err, domain.ErrBillConflict), errors.Is(err, domain.ErrVersionConflict):
		status = http.StatusConflict
		code = "VERSION_CONFLICT"
	case errors.Is(err, domain.ErrBillImmutable):
		status = http.StatusConflict
		code = "BILL_IMMUTABLE"
	case errors.Is(err, domain.ErrImagesRequired):
		status = http.StatusBadRequest
		code = "IMAGES_REQUIRED"
	case errors.Is(err, domain.ErrReviewRequired):
		status = http.StatusBadRequest
		code = "REVIEW_REQUIRED"
	case errors.Is(err, domain.ErrBillNotReady):
		status = http.StatusBadRequest
		code = "BILL_NOT_READY"
	case errors.Is(err, domain.ErrOcrAlreadyRunning):
		status = http.StatusConflict
		code = "OCR_ALREADY_RUNNING"
	case errors.Is(err, domain.ErrOcrLimitReached):
		status = http.StatusTooManyRequests
		code = "OCR_LIMIT_REACHED"
	case errors.Is(err, domain.ErrOcrNotReady):
		status = http.StatusBadRequest
		code = "OCR_NOT_READY"
	case errors.Is(err, domain.ErrOcrJobNotFound):
		status = http.StatusNotFound
		code = "OCR_JOB_NOT_FOUND"
	case errors.Is(err, domain.ErrOcrResultStale):
		status = http.StatusConflict
		code = "OCR_RESULT_STALE"
	case errors.Is(err, domain.ErrOcrAlreadyApplied):
		status = http.StatusConflict
		code = "OCR_ALREADY_APPLIED"
	case errors.Is(err, domain.ErrOcrCandidateInvalid):
		status = http.StatusBadRequest
		code = "OCR_CANDIDATE_INVALID"
	case errors.Is(err, domain.ErrOcrProviderUnavailable):
		status = http.StatusServiceUnavailable
		code = "OCR_PROVIDER_UNAVAILABLE"
	case errors.Is(err, domain.ErrOcrSchemaInvalid):
		status = http.StatusBadGateway
		code = "OCR_SCHEMA_INVALID"
	case errors.Is(err, domain.ErrOcrTimeout):
		status = http.StatusGatewayTimeout
		code = "OCR_TIMEOUT"
	}

	_ = helpers.WriteAPIError(w, status, code, msg, nil)
}
