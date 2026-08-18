package banks

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"paysplit-backend/internal/transport/http/helpers"
)

// Handler xử lý các HTTP request liên quan đến danh mục ngân hàng.
type Handler struct {
	directory *Directory
}

// NewHandler tạo mới một Handler cho danh mục ngân hàng.
func NewHandler(directory *Directory) *Handler {
	if directory == nil {
		panic("bank directory must not be nil")
	}
	return &Handler{directory: directory}
}

// RegisterRoutes đăng ký các routes liên quan đến ngân hàng vào router.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/", h.ListBanks)
}

// ListBanks trả về danh sách các ngân hàng từ snapshot VietQR.
// Hỗ trợ query parameter `?supported=true` hoặc `?supported=false`.
func (h *Handler) ListBanks(w http.ResponseWriter, r *http.Request) {
	var supportedFilter *bool
	supportedQuery := r.URL.Query().Get("supported")
	if supportedQuery != "" {
		if val, err := strconv.ParseBool(supportedQuery); err == nil {
			supportedFilter = &val
		}
	}

	banks := h.directory.List(supportedFilter)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_ = helpers.WriteJSON(w, http.StatusOK, map[string]any{
		"banks": banks,
	})
}
