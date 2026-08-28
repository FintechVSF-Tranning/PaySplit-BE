package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"paysplit-backend/internal/config"
	"paysplit-backend/internal/transport/http/helpers"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

// SyncCursorData chứa dữ liệu cursor nội bộ được ký HMAC chống giả mạo
type SyncCursorData struct {
	UserID         string `json:"u"`
	WatermarkSeq   int64  `json:"w"`
	LastScannedSeq int64  `json:"s"`
	PageNo         int    `json:"p"`
	IssuedAt       int64  `json:"t"`
}

// AggregateVersionItem chứa delta phiên bản mới nhất của một aggregate
type AggregateVersionItem struct {
	GroupID       uuid.UUID `json:"group_id"`
	AggregateType string    `json:"aggregate_type"`
	AggregateID   uuid.UUID `json:"aggregate_id"`
	Version       int64     `json:"version"`
}

// SyncVersionsResponse Payload trả về từ GET /api/v1/sync/versions
type SyncVersionsResponse struct {
	Watermark             int64                  `json:"watermark"`
	MembershipSyncVersion int64                  `json:"membership_sync_version"`
	Aggregates            []AggregateVersionItem `json:"aggregates"`
	NextCursor            string                 `json:"next_cursor"`
	HasMore               bool                   `json:"has_more"`
}

// SyncVersionsHandler phục vụ endpoint hợp nhất polling GET /api/v1/sync/versions (Spec 0010 AC-6, AC-7)
type SyncVersionsHandler struct {
	pool      *pgxpool.Pool
	cfg       config.SyncConfig
	secretKey []byte
}

// NewSyncVersionsHandler khởi tạo handler polling hợp nhất
func NewSyncVersionsHandler(pool *pgxpool.Pool, cfg config.SyncConfig, fallbackSecret string) *SyncVersionsHandler {
	secret := cfg.CursorHMACKey
	if strings.TrimSpace(secret) == "" {
		secret = fallbackSecret
	}
	if strings.TrimSpace(secret) == "" {
		secret = "default-sync-cursor-hmac-key-32bytes-long"
	}
	return &SyncVersionsHandler{
		pool:      pool,
		cfg:       cfg,
		secretKey: []byte(secret),
	}
}

// HandleSyncVersions xử lý truy vấn versions delta cho toàn bộ nhóm của user
func (h *SyncVersionsHandler) HandleSyncVersions(w http.ResponseWriter, r *http.Request) {
	userIDStr, ok := authmw.UserID(r.Context())
	if !ok || userIDStr == "" {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user context", nil)
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	pageLimit := h.cfg.PageLimit
	if pageLimit <= 0 || pageLimit > 500 {
		pageLimit = 500
	}
	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if parsedLimit, err := strconv.Atoi(limitParam); err == nil && parsedLimit > 0 && parsedLimit <= 500 {
			pageLimit = parsedLimit
		}
	}

	maxBytes := h.cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 262144 // 256 KiB
	}
	maxPages := h.cfg.MaxPagesPerCycle
	if maxPages <= 0 {
		maxPages = 4
	}

	afterParam := strings.TrimSpace(r.URL.Query().Get("after"))

	// Trường hợp 1: Baseline setup (không truyền cursor after)
	if afterParam == "" {
		var currentWatermark int64
		if h.pool != nil {
			_ = h.pool.QueryRow(r.Context(), `SELECT value FROM sync_sequence_state WHERE id = 1`).Scan(&currentWatermark)
		}

		var memSyncVer int64
		if h.pool != nil {
			_ = h.pool.QueryRow(r.Context(), `SELECT membership_sync_version FROM users WHERE id = $1`, userID).Scan(&memSyncVer)
		}

		initialCursor := SyncCursorData{
			UserID:         userID.String(),
			WatermarkSeq:   currentWatermark,
			LastScannedSeq: currentWatermark,
			PageNo:         1,
			IssuedAt:       time.Now().Unix(),
		}
		signedCursor := h.signCursor(initialCursor)

		_ = helpers.WriteJSON(w, http.StatusOK, SyncVersionsResponse{
			Watermark:             currentWatermark,
			MembershipSyncVersion: memSyncVer,
			Aggregates:            []AggregateVersionItem{},
			NextCursor:            signedCursor,
			HasMore:               false,
		})
		return
	}

	// Trường hợp 2: Polling với cursor
	cursor, err := h.verifyCursor(afterParam)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_CURSOR", "Malformed or tampered sync cursor", nil)
		return
	}

	if cursor.UserID != userID.String() {
		_ = helpers.WriteAPIError(w, http.StatusBadRequest, "INVALID_CURSOR", "Cursor belongs to another user", nil)
		return
	}

	// Kiểm tra retention floor (7 ngày)
	if time.Since(time.Unix(cursor.IssuedAt, 0)) > 7*24*time.Hour {
		_ = helpers.WriteAPIError(w, http.StatusConflict, "SYNC_RESYNC_REQUIRED", "Cursor expired beyond 7-day retention floor", nil)
		return
	}

	// Kiểm tra giới hạn 4 trang trong 1 chu kỳ polling
	if cursor.PageNo > maxPages {
		_ = helpers.WriteAPIError(w, http.StatusConflict, "SYNC_RESYNC_REQUIRED", "Exceeded maximum 4 pages per polling cycle", nil)
		return
	}

	// Lấy membership_sync_version của user
	var memSyncVer int64
	if h.pool != nil {
		_ = h.pool.QueryRow(r.Context(), `SELECT membership_sync_version FROM users WHERE id = $1`, userID).Scan(&memSyncVer)
	}

	// Truy vấn raw invalidations theo raw sequence ASC
	const query = `
		SELECT ri.sequence, ri.group_id, ri.aggregate_type, ri.aggregate_id, ri.version
		FROM realtime_invalidations ri
		JOIN group_members gm ON gm.group_id = ri.group_id AND gm.user_id = $1 AND gm.status = 'active'
		WHERE ri.sequence > $2 AND ri.sequence <= $3
		ORDER BY ri.sequence ASC
		LIMIT $4
	`

	if h.pool == nil {
		_ = helpers.WriteJSON(w, http.StatusOK, SyncVersionsResponse{
			Watermark:             cursor.WatermarkSeq,
			MembershipSyncVersion: memSyncVer,
			Aggregates:            []AggregateVersionItem{},
			NextCursor:            "",
			HasMore:               false,
		})
		return
	}

	fetchLimit := pageLimit + 1
	rows, err := h.pool.Query(r.Context(), query, userID, cursor.LastScannedSeq, cursor.WatermarkSeq, fetchLimit)
	if err != nil {
		_ = helpers.WriteAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to query sync versions", nil)
		return
	}
	defer rows.Close()

	type aggKey struct {
		aggType string
		aggID   uuid.UUID
	}
	aggMap := make(map[aggKey]AggregateVersionItem)
	orderedKeys := make([]aggKey, 0)

	var lastRawSeq int64 = cursor.LastScannedSeq
	rowCount := 0
	hasMore := false

	approxBytes := 128 // base overhead

	for rows.Next() {
		rowCount++
		var seq int64
		var groupID uuid.UUID
		var aggType string
		var aggID uuid.UUID
		var ver int64

		if err := rows.Scan(&seq, &groupID, &aggType, &aggID, &ver); err != nil {
			break
		}

		if rowCount > pageLimit || approxBytes >= maxBytes {
			hasMore = true
			break
		}

		lastRawSeq = seq
		key := aggKey{aggType: aggType, aggID: aggID}
		if _, exists := aggMap[key]; !exists {
			orderedKeys = append(orderedKeys, key)
			approxBytes += 96 // ước tính size mỗi item JSON
		}
		aggMap[key] = AggregateVersionItem{
			GroupID:       groupID,
			AggregateType: aggType,
			AggregateID:   aggID,
			Version:       ver,
		}
	}

	aggregates := make([]AggregateVersionItem, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		aggregates = append(aggregates, aggMap[key])
	}

	var nextCursorStr string
	if hasMore {
		nextCursorData := SyncCursorData{
			UserID:         userID.String(),
			WatermarkSeq:   cursor.WatermarkSeq,
			LastScannedSeq: lastRawSeq,
			PageNo:         cursor.PageNo + 1,
			IssuedAt:       cursor.IssuedAt,
		}
		nextCursorStr = h.signCursor(nextCursorData)
	} else {
		// Hoàn thành đợt watermark này, chuẩn bị cursor cho chu kỳ tiếp theo
		var newWatermark int64
		_ = h.pool.QueryRow(r.Context(), `SELECT value FROM sync_sequence_state WHERE id = 1`).Scan(&newWatermark)
		if newWatermark < cursor.WatermarkSeq {
			newWatermark = cursor.WatermarkSeq
		}
		nextCycleCursor := SyncCursorData{
			UserID:         userID.String(),
			WatermarkSeq:   newWatermark,
			LastScannedSeq: cursor.WatermarkSeq,
			PageNo:         1,
			IssuedAt:       time.Now().Unix(),
		}
		nextCursorStr = h.signCursor(nextCycleCursor)
	}

	helpers.WriteJSON(w, http.StatusOK, SyncVersionsResponse{
		Watermark:             cursor.WatermarkSeq,
		MembershipSyncVersion: memSyncVer,
		Aggregates:            aggregates,
		NextCursor:            nextCursorStr,
		HasMore:               hasMore,
	})
}

func (h *SyncVersionsHandler) signCursor(data SyncCursorData) string {
	raw, _ := json.Marshal(data)
	encodedData := base64.RawURLEncoding.EncodeToString(raw)

	mac := hmac.New(sha256.New, h.secretKey)
	mac.Write([]byte(encodedData))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("%s.%s", encodedData, sig)
}

func (h *SyncVersionsHandler) verifyCursor(cursorStr string) (*SyncCursorData, error) {
	parts := strings.Split(cursorStr, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid cursor format")
	}

	encodedData, sig := parts[0], parts[1]

	mac := hmac.New(sha256.New, h.secretKey)
	mac.Write([]byte(encodedData))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if hmac.Equal([]byte(sig), []byte(expectedSig)) != true {
		return nil, fmt.Errorf("invalid cursor signature")
	}

	raw, err := base64.RawURLEncoding.DecodeString(encodedData)
	if err != nil {
		return nil, fmt.Errorf("decode cursor data: %w", err)
	}

	var data SyncCursorData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("unmarshal cursor data: %w", err)
	}

	return &data, nil
}
