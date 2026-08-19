package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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

const (
	defaultImageMaxBytes = 10 * 1024 * 1024
	defaultImageMaxCount = 5
)

// Handler xử lý các HTTP endpoints cho module Bill & OCR.
type Handler struct {
	service       *usecase.Service
	sseHandler    *SSEHandler
	imageMaxBytes int64
	imageMaxCount int
}

// NewHandler khởi tạo Handler mới.
func NewHandler(service *usecase.Service, sseHandler *SSEHandler) *Handler {
	if service == nil {
		panic("bill handler service must not be nil")
	}
	return &Handler{
		service:       service,
		sseHandler:    sseHandler,
		imageMaxBytes: defaultImageMaxBytes,
		imageMaxCount: defaultImageMaxCount,
	}
}

// SetImageLimits cấu hình giới hạn ảnh hóa đơn từ BILL_IMAGE_MAX_BYTES / BILL_IMAGE_MAX_COUNT
// (Spec 3 AC-1). Phải được gọi trước khi request đầu tiên tới; giá trị <=0 giữ nguyên mặc định.
func (h *Handler) SetImageLimits(maxBytes int64, maxCount int) {
	if maxBytes > 0 {
		h.imageMaxBytes = maxBytes
	}
	if maxCount > 0 {
		h.imageMaxCount = maxCount
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
		protected.Put("/{id}", h.UpdateDraftBill)
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
	var reqBodyBytes []byte
	contentType := r.Header.Get("Content-Type")

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Chặn ngay tại tầng đọc body: không cho phần thân request vượt quá tổng dung lượng ảnh tối đa
		// cộng phần dư cho metadata/boundary, trước khi ParseMultipartForm ghi bất cứ gì ra đĩa/bộ nhớ
		// (Spec 3 AC-1: "at most 10 MB" mỗi ảnh phải được chặn sớm, không phải sau khi đã đọc hết).
		const multipartOverheadSlack = 1 << 20 // 1MB cho metadata JSON, boundary, tên trường
		maxBodyBytes := h.imageMaxBytes*int64(h.imageMaxCount) + multipartOverheadSlack
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

		if err := r.ParseMultipartForm(h.imageMaxBytes); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				_ = helpers.WriteAPIError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body exceeds the maximum allowed size", nil)
				return
			}
			_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_MULTIPART", "failed to parse multipart form", nil)
			return
		}

		// Đọc metadata JSON
		metaJSON := r.FormValue("metadata")
		if metaJSON != "" {
			if err := json.Unmarshal([]byte(metaJSON), &req); err != nil {
				_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_JSON", "invalid metadata JSON", nil)
				return
			}
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
			if len(fileHeaders) > h.imageMaxCount {
				_ = helpers.WriteAPIError(w, http.StatusBadRequest, "TOO_MANY_IMAGES", fmt.Sprintf("maximum %d images allowed", h.imageMaxCount), nil)
				return
			}
			for _, fh := range fileHeaders {
				// Từ chối ngay theo kích thước khai báo trước khi mở/đọc file (Spec 3 AC-1).
				if fh.Size > h.imageMaxBytes {
					_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_IMAGE", "image exceeds the maximum allowed size", nil)
					return
				}
				f, err := fh.Open()
				if err != nil {
					_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_MULTIPART", "failed to open uploaded file", nil)
					return
				}
				// io.LimitReader như một lớp chặn thứ hai phòng khi Size khai báo không đúng thực tế.
				data, err := io.ReadAll(io.LimitReader(f, h.imageMaxBytes+1))
				f.Close()
				if err != nil {
					_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_MULTIPART", "failed to read uploaded file", nil)
					return
				}
				if int64(len(data)) > h.imageMaxBytes {
					_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_IMAGE", "image exceeds the maximum allowed size", nil)
					return
				}
				req.Files = append(req.Files, data)
			}
		}
		reqBodyBytes, _ = json.Marshal(map[string]any{
			"group_id":    req.GroupID,
			"merchant":    req.MerchantName,
			"items_count": len(req.Items),
			"files_count": len(req.Files),
		})
	} else {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_JSON", "cannot read request body", nil)
			return
		}
		reqBodyBytes = bodyBytes
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", nil)
			return
		}
	}

	handled, rawKey := h.checkIdempotency(w, r, callerUserID, "create_bill", reqBodyBytes)
	if handled {
		return
	}

	result, err := h.service.CreateBill(r.Context(), callerUserID, req)
	if err != nil {
		_ = h.service.ReleaseIdempotency(r.Context(), callerUserID, "create_bill", rawKey)
		writeDomainError(w, err)
		return
	}

	statusCode := http.StatusCreated
	if result.IsAccepted {
		statusCode = http.StatusAccepted
	}

	var resourceID *uuid.UUID
	if result.Bill != nil {
		resourceID = &result.Bill.ID
	}
	_ = h.service.CompleteIdempotency(r.Context(), callerUserID, "create_bill", rawKey, statusCode, result, resourceID)

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

// ListBills xử lý GET /api/v1/bills?group_id=...&limit=...&cursor=...
func (h *Handler) ListBills(w http.ResponseWriter, r *http.Request) {
	callerUserID := getUserID(r)
	groupIDStr := r.URL.Query().Get("group_id")
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_GROUP_ID", "group_id query param is required", nil)
		return
	}

	limit := 20
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	cursorStr := r.URL.Query().Get("cursor")
	var cursor *string
	if cursorStr != "" {
		cursor = &cursorStr
	}

	offsetStr := r.URL.Query().Get("offset")
	if offsetStr != "" && cursor == nil {
		offset := 0
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
		bills, err := h.service.ListBills(r.Context(), callerUserID, groupID, int32(limit), int32(offset))
		if err != nil {
			writeDomainError(w, err)
			return
		}
		_ = helpers.WriteJSON(w, http.StatusOK, map[string]any{
			"bills": bills,
		})
		return
	}

	res, err := h.service.ListBillsCursor(r.Context(), callerUserID, groupID, cursor, int32(limit))
	if err != nil {
		writeDomainError(w, err)
		return
	}

	resp := map[string]any{
		"bills": res.Bills,
	}
	if res.NextCursor != nil {
		resp["next_cursor"] = *res.NextCursor
	}

	_ = helpers.WriteJSON(w, http.StatusOK, resp)
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

	handled, rawKey := h.checkIdempotency(w, r, callerUserID, "retry_ocr", []byte(billID.String()+groupID.String()))
	if handled {
		return
	}

	job, err := h.service.RetryOCR(r.Context(), callerUserID, billID, groupID)
	if err != nil {
		_ = h.service.ReleaseIdempotency(r.Context(), callerUserID, "retry_ocr", rawKey)
		writeDomainError(w, err)
		return
	}

	resp := map[string]any{
		"ocr_job": job,
	}
	var resID *uuid.UUID
	if job != nil {
		resID = &job.ID
	}
	_ = h.service.CompleteIdempotency(r.Context(), callerUserID, "retry_ocr", rawKey, http.StatusAccepted, resp, resID)

	_ = helpers.WriteJSON(w, http.StatusAccepted, resp)
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

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_JSON", "cannot read request body", nil)
		return
	}

	var body struct {
		JobID   uuid.UUID `json:"job_id"`
		Version int32     `json:"version"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", nil)
		return
	}

	handled, rawKey := h.checkIdempotency(w, r, callerUserID, "apply_candidate", bodyBytes)
	if handled {
		return
	}

	updatedBill, err := h.service.ApplyCandidate(r.Context(), callerUserID, billID, groupID, body.JobID, body.Version)
	if err != nil {
		_ = h.service.ReleaseIdempotency(r.Context(), callerUserID, "apply_candidate", rawKey)
		writeDomainError(w, err)
		return
	}

	resp := map[string]any{
		"bill": updatedBill,
	}
	_ = h.service.CompleteIdempotency(r.Context(), callerUserID, "apply_candidate", rawKey, http.StatusOK, resp, &billID)

	_ = helpers.WriteJSON(w, http.StatusOK, resp)
}

// UpdateDraftBill xử lý PUT /api/v1/bills/{id}?group_id=...
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

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_JSON", "cannot read request body", nil)
		return
	}

	var req usecase.UpdateDraftRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", nil)
		return
	}

	handled, rawKey := h.checkIdempotency(w, r, callerUserID, "update_draft", bodyBytes)
	if handled {
		return
	}

	updatedBill, err := h.service.UpdateDraftBill(r.Context(), callerUserID, billID, groupID, req)
	if err != nil {
		_ = h.service.ReleaseIdempotency(r.Context(), callerUserID, "update_draft", rawKey)
		writeDomainError(w, err)
		return
	}

	resp := map[string]any{
		"bill": updatedBill,
	}
	_ = h.service.CompleteIdempotency(r.Context(), callerUserID, "update_draft", rawKey, http.StatusOK, resp, &billID)

	_ = helpers.WriteJSON(w, http.StatusOK, resp)
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

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "cannot read request body", nil)
		return
	}

	var body struct {
		Version int32 `json:"version"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body", nil)
		return
	}

	handled, rawKey := h.checkIdempotency(w, r, callerUserID, "review_bill", bodyBytes)
	if handled {
		return
	}

	reviewedBill, err := h.service.ReviewBill(r.Context(), callerUserID, billID, groupID, body.Version)
	if err != nil {
		_ = h.service.ReleaseIdempotency(r.Context(), callerUserID, "review_bill", rawKey)
		writeDomainError(w, err)
		return
	}

	resp := map[string]any{
		"bill": reviewedBill,
	}
	_ = h.service.CompleteIdempotency(r.Context(), callerUserID, "review_bill", rawKey, http.StatusOK, resp, &billID)

	_ = helpers.WriteJSON(w, http.StatusOK, resp)
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

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "cannot read request body", nil)
		return
	}

	var body struct {
		Version int32 `json:"version"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body", nil)
		return
	}

	handled, rawKey := h.checkIdempotency(w, r, callerUserID, "finalize_bill", bodyBytes)
	if handled {
		return
	}

	finalizedBill, err := h.service.FinalizeBill(r.Context(), callerUserID, billID, groupID, body.Version)
	if err != nil {
		_ = h.service.ReleaseIdempotency(r.Context(), callerUserID, "finalize_bill", rawKey)
		writeDomainError(w, err)
		return
	}

	resp := map[string]any{
		"bill": finalizedBill,
	}
	_ = h.service.CompleteIdempotency(r.Context(), callerUserID, "finalize_bill", rawKey, http.StatusOK, resp, &billID)

	_ = helpers.WriteJSON(w, http.StatusOK, resp)
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

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "cannot read request body", nil)
		return
	}

	var body struct {
		Version int32  `json:"version"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body", nil)
		return
	}

	handled, rawKey := h.checkIdempotency(w, r, callerUserID, "void_bill", bodyBytes)
	if handled {
		return
	}

	voidedBill, err := h.service.VoidBill(r.Context(), callerUserID, billID, groupID, body.Version, body.Reason)
	if err != nil {
		_ = h.service.ReleaseIdempotency(r.Context(), callerUserID, "void_bill", rawKey)
		writeDomainError(w, err)
		return
	}

	resp := map[string]any{
		"bill": voidedBill,
	}
	_ = h.service.CompleteIdempotency(r.Context(), callerUserID, "void_bill", rawKey, http.StatusOK, resp, &billID)

	_ = helpers.WriteJSON(w, http.StatusOK, resp)
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

	handled, rawKey := h.checkIdempotency(w, r, callerUserID, "delete_draft", []byte(billID.String()+groupID.String()))
	if handled {
		return
	}

	err = h.service.DeleteDraftBill(r.Context(), callerUserID, billID, groupID)
	if err != nil {
		_ = h.service.ReleaseIdempotency(r.Context(), callerUserID, "delete_draft", rawKey)
		writeDomainError(w, err)
		return
	}

	_ = h.service.CompleteIdempotency(r.Context(), callerUserID, "delete_draft", rawKey, http.StatusNoContent, nil, &billID)

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) checkIdempotency(w http.ResponseWriter, r *http.Request, actorUserID uuid.UUID, operation string, reqBody []byte) (bool, string) {
	rawKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if rawKey == "" {
		return false, ""
	}

	rec, err := h.service.CheckOrReserveIdempotency(r.Context(), actorUserID, operation, rawKey, reqBody)
	if err != nil {
		writeDomainError(w, err)
		return true, ""
	}

	if rec != nil && rec.State == "completed" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rec.ResponseCode)
		if len(rec.ResponseBody) > 0 {
			_, _ = w.Write(rec.ResponseBody)
		}
		return true, ""
	}

	return false, rawKey
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
	mapped := true

	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		status = http.StatusBadRequest
		code = "VALIDATION_FAILED"
	case errors.Is(err, domain.ErrInvalidCursor):
		status = http.StatusBadRequest
		code = "INVALID_CURSOR"
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
	case errors.Is(err, domain.ErrBillNotFinalized):
		status = http.StatusConflict
		code = "BILL_NOT_FINALIZED"
	case errors.Is(err, domain.ErrBillAlreadyVoided):
		status = http.StatusConflict
		code = "BILL_ALREADY_VOIDED"
	case errors.Is(err, domain.ErrBankAccountRequired):
		status = http.StatusUnprocessableEntity
		code = "BANK_ACCOUNT_REQUIRED"
	case errors.Is(err, domain.ErrImagesRequired):
		status = http.StatusBadRequest
		code = "IMAGES_REQUIRED"
	case errors.Is(err, domain.ErrReviewRequired):
		status = http.StatusUnprocessableEntity
		code = "BILL_NOT_READY"
	case errors.Is(err, domain.ErrBillNotReady):
		status = http.StatusUnprocessableEntity
		code = "BILL_NOT_READY"
	case errors.Is(err, domain.ErrOcrAlreadyRunning):
		status = http.StatusConflict
		code = "OCR_ALREADY_RUNNING"
	case errors.Is(err, domain.ErrOcrLimitReached):
		status = http.StatusTooManyRequests
		code = "OCR_LIMIT_REACHED"
	case errors.Is(err, domain.ErrOcrNotReady):
		status = http.StatusConflict
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
		status = http.StatusUnprocessableEntity
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
	case errors.Is(err, domain.ErrPaymentAlreadyStarted):
		status = http.StatusConflict
		code = "PAYMENT_ALREADY_STARTED"
	case errors.Is(err, domain.ErrIdempotencyInProgress):
		status = http.StatusConflict
		code = "IDEMPOTENCY_IN_PROGRESS"
	case errors.Is(err, domain.ErrIdempotencyKeyReused):
		status = http.StatusConflict
		code = "IDEMPOTENCY_KEY_REUSED"
	default:
		mapped = false
	}

	if !mapped {
		// Không trả nguyên văn lỗi nội bộ (pgx, Cloudinary, ...) cho client; log server side để điều tra
		// (Spec 3 security model: "Raw provider content never appears in API responses or logs").
		log.Printf("event=bill_internal_error err=%v", err)
		msg = "internal error"
	}

	_ = helpers.WriteAPIError(w, status, code, msg, nil)
}
