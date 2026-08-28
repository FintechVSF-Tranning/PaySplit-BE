package http

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"paysplit-backend/internal/config"
	"paysplit-backend/internal/platform/queue/appjob"
	"paysplit-backend/internal/transport/http/helpers"
)

// Handler phục vụ các endpoint nội bộ /internal/jobs/dispatch và /internal/jobs/drain
type Handler struct {
	dispatcher *appjob.Dispatcher
	drain      *appjob.Drain
	cfg        config.JobConfig
}

// NewHandler khởi tạo Handler nội bộ
func NewHandler(dispatcher *appjob.Dispatcher, drain *appjob.Drain, cfg config.JobConfig) *Handler {
	return &Handler{
		dispatcher: dispatcher,
		drain:      drain,
		cfg:        cfg,
	}
}

// RegisterRoutes đăng ký các routes nội bộ vào router
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/internal/jobs", func(jobs chi.Router) {
		jobs.Post("/dispatch", h.HandleDispatch)
		jobs.Get("/dispatch", h.HandleDispatch)
		jobs.Post("/drain", h.HandleDrain)
	})
}

// HandleDispatch xử lý kích hoạt đợt điều phối wave
func (h *Handler) HandleDispatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	if !h.authenticate(r) {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid or missing trigger secret", nil)
		return
	}

	resp, err := h.dispatcher.Dispatch(r.Context())
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to dispatch jobs", nil)
		return
	}

	if resp == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, resp)
}

// HandleDrain xử lý kích hoạt drain một slot
func (h *Handler) HandleDrain(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	if !h.authenticate(r) {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid or missing trigger secret", nil)
		return
	}

	var req appjob.DrainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "Malformed drain request payload", nil)
		return
	}

	resp, err := h.drain.Drain(r.Context(), req)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to drain jobs", nil)
		return
	}

	if resp == nil || resp.Claimed == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) authenticate(r *http.Request) bool {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")

	validSecret := h.cfg.TriggerSecret
	validCron := h.cfg.CronSecret

	matchTrigger := validSecret != "" && subtle.ConstantTimeCompare([]byte(token), []byte(validSecret)) == 1
	matchCron := validCron != "" && subtle.ConstantTimeCompare([]byte(token), []byte(validCron)) == 1

	return matchTrigger || matchCron
}
