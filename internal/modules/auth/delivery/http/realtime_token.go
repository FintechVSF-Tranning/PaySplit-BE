package http

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"paysplit-backend/internal/platform/auth/realtimejwt"
	"paysplit-backend/internal/transport/http/helpers"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

type realtimeTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (h *Handler) SetRealtimeJWTManager(mgr *realtimejwt.Manager) {
	h.realtimeJWT = mgr
}

// GetRealtimeToken phát hành ES256 Realtime JWT cho người dùng đã xác thực (Spec 0010 AC-5)
func (h *Handler) GetRealtimeToken(w http.ResponseWriter, r *http.Request) {
	if h.realtimeJWT == nil {
		_ = helpers.WriteAPIError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Realtime signing is unavailable", nil)
		return
	}

	userIDStr, ok := authmw.UserID(r.Context())
	if !ok || userIDStr == "" {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user context", nil)
		return
	}

	sessionIDStr, ok := authmw.SessionID(r.Context())
	if !ok || sessionIDStr == "" {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Missing session context", nil)
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid session ID", nil)
		return
	}

	tokenStr, expiresAt, err := h.realtimeJWT.Sign(userID, sessionID)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to sign realtime token", nil)
		return
	}

	_ = helpers.WriteJSON(w, http.StatusOK, realtimeTokenResponse{
		Token:     tokenStr,
		ExpiresAt: expiresAt,
	})
}
