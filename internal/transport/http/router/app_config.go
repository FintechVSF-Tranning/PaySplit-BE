package router

import (
	"net/http"

	"paysplit-backend/internal/config"
	"paysplit-backend/internal/transport/http/helpers"
)

// AppConfigResponse chứa cấu hình công khai cho mobile app (Spec 0010 AC-6)
type AppConfigResponse struct {
	RealtimeMode           string `json:"realtime_mode"`
	PollIntervalSeconds    int    `json:"poll_interval_seconds"`
	PollJitterPercent      int    `json:"poll_jitter_percent"`
	MaxGroupChannels       int    `json:"max_group_channels"`
	SyncPageLimit          int    `json:"sync_page_limit"`
	SyncMaxBytes           int    `json:"sync_max_bytes"`
	SyncMaxPagesPerCycle   int    `json:"sync_max_pages_per_cycle"`
	SupabaseURL            string `json:"supabase_url"`
	SupabasePublishableKey string `json:"supabase_publishable_key"`
}

// AppConfigHandler phục vụ endpoint GET /api/v1/app-config
type AppConfigHandler struct {
	cfg config.Config
}

// NewAppConfigHandler khởi tạo handler cấu hình app
func NewAppConfigHandler(cfg config.Config) *AppConfigHandler {
	return &AppConfigHandler{cfg: cfg}
}

// HandleGetConfig trả về cấu hình realtime và fallback polling cho mobile app
func (h *AppConfigHandler) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	resp := AppConfigResponse{
		RealtimeMode:           h.cfg.Realtime.MobileRealtimeMode,
		PollIntervalSeconds:    int(h.cfg.Realtime.PollInterval.Seconds()),
		PollJitterPercent:      h.cfg.Realtime.PollJitterPercent,
		MaxGroupChannels:       h.cfg.Realtime.MaxGroupChannels,
		SyncPageLimit:          h.cfg.Sync.PageLimit,
		SyncMaxBytes:           h.cfg.Sync.MaxBytes,
		SyncMaxPagesPerCycle:   h.cfg.Sync.MaxPagesPerCycle,
		SupabaseURL:            h.cfg.Realtime.SupabaseURL,
		SupabasePublishableKey: h.cfg.Realtime.SupabasePublishableKey,
	}

	helpers.WriteJSON(w, http.StatusOK, resp)
}
