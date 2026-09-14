package http

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"paysplit-backend/internal/modules/sepay/domain"
	"paysplit-backend/internal/modules/sepay/usecase"
	"paysplit-backend/internal/transport/http/helpers"
)

// maxWebhookBodyBytes đủ rộng cho một payload SePay (vài trăm byte) và chặn
// body vô hạn từ một endpoint không có session.
const maxWebhookBodyBytes = 64 << 10

type Handler struct {
	service *usecase.Service
	apiKey  string
}

// NewHandler nhận API key đã cấu hình ở SePay. Key rỗng nghĩa là tính năng chưa
// bật: endpoint trả 503 thay vì chấp nhận request không xác thực.
func NewHandler(service *usecase.Service, apiKey string) *Handler {
	if service == nil {
		panic("sepay handler service must not be nil")
	}
	return &Handler{service: service, apiKey: strings.TrimSpace(apiKey)}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/sepay", h.ReceiveWebhook)
}

// webhookRequest khớp payload SePay. Không dùng helpers.ReadJSON vì hàm đó từ
// chối field lạ: SePay thêm field mới không được làm rớt giao dịch.
type webhookRequest struct {
	ID              int64   `json:"id"`
	Gateway         string  `json:"gateway"`
	TransactionDate string  `json:"transactionDate"`
	AccountNumber   string  `json:"accountNumber"`
	SubAccount      *string `json:"subAccount"`
	Code            *string `json:"code"`
	Content         string  `json:"content"`
	TransferType    string  `json:"transferType"`
	TransferAmount  int64   `json:"transferAmount"`
	Accumulated     int64   `json:"accumulated"`
	ReferenceCode   *string `json:"referenceCode"`
	Description     string  `json:"description"`
}

func (h *Handler) ReceiveWebhook(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" {
		_ = helpers.WriteAPIError(w, http.StatusServiceUnavailable, "SEPAY_WEBHOOK_DISABLED", "SePay webhook chưa được cấu hình", nil)
		return
	}
	if !h.authorized(r.Header.Get("Authorization")) {
		log.Printf("event=sepay_webhook_unauthorized remote=%s", r.RemoteAddr)
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "API key không hợp lệ", nil)
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes))
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "Payload quá lớn", nil)
		return
	}
	var req webhookRequest
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&req); err != nil {
		log.Printf("event=sepay_webhook_invalid_json content_type=%q bytes=%d", r.Header.Get("Content-Type"), len(raw))
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Payload không phải JSON hợp lệ", nil)
		return
	}

	result, err := h.service.Receive(r.Context(), usecase.ReceiveInput{
		ID: req.ID, Gateway: req.Gateway, TransactionDate: req.TransactionDate,
		AccountNumber: req.AccountNumber, SubAccount: req.SubAccount, Code: req.Code,
		Content: req.Content, TransferType: req.TransferType, TransferAmount: req.TransferAmount,
		Accumulated: req.Accumulated, ReferenceCode: req.ReferenceCode, Description: req.Description,
		RawPayload: raw,
	})
	if errors.Is(err, domain.ErrInvalidInput) {
		// reason chỉ nêu field sai (id, loại, số tiền, ngày), không chứa số tài
		// khoản hay nội dung chuyển khoản.
		log.Printf("event=sepay_webhook_invalid id=%d reason=%q", req.ID, err.Error())
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "VALIDATION_FAILED", "Dữ liệu giao dịch không hợp lệ", nil)
		return
	}
	if err != nil {
		// 500 để SePay tự retry (tối đa 7 lần trong 5 giờ); nhờ khóa id nên retry an toàn.
		log.Printf("event=sepay_webhook_failed id=%d err=%v", req.ID, err)
		_ = helpers.WriteAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Lỗi hệ thống", nil)
		return
	}

	tx := result.Transaction
	// Không log mã tham chiếu, số tài khoản hay nội dung CK (xem settlement/README.md).
	log.Printf("event=sepay_webhook_received id=%d type=%s amount=%d has_reference=%t match_status=%s payment_id=%s duplicate=%t",
		tx.ID, tx.TransferType, tx.TransferAmount, tx.PaymentReference != nil, result.MatchStatus, deref(result.PaymentID), result.Duplicate)
	// SePay coi là thành công khi HTTP 200/201 và body có "success": true;
	// envelope chuẩn đã chứa sẵn field đó. Giao dịch không khớp vẫn trả 200:
	// đó là kết quả nghiệp vụ, SePay gửi lại cũng không đổi được gì.
	_ = helpers.WriteJSON(w, http.StatusOK, map[string]any{
		"transaction_id":    tx.ID,
		"payment_reference": tx.PaymentReference,
		"match_status":      result.MatchStatus,
		"payment_id":        result.PaymentID,
		"duplicate":         result.Duplicate,
	})
}

func (h *Handler) authorized(header string) bool {
	scheme, key, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Apikey") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(key)), []byte(h.apiKey)) == 1
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
