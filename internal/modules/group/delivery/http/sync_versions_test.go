package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"paysplit-backend/internal/config"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

func TestSyncCursorSigningAndVerification(t *testing.T) {
	h := NewSyncVersionsHandler(nil, config.SyncConfig{
		PageLimit:        500,
		MaxBytes:         262144,
		MaxPagesPerCycle: 4,
	}, "test-sync-secret-key-32bytes")

	userID := uuid.New().String()
	cursorData := SyncCursorData{
		UserID:         userID,
		WatermarkSeq:   1000,
		LastScannedSeq: 500,
		PageNo:         1,
		IssuedAt:       time.Now().Unix(),
	}

	signed := h.signCursor(cursorData)
	verified, err := h.verifyCursor(signed)
	if err != nil {
		t.Fatalf("failed to verify signed cursor: %v", err)
	}

	if verified.UserID != userID || verified.WatermarkSeq != 1000 || verified.LastScannedSeq != 500 {
		t.Fatalf("cursor data mismatch: %+v", verified)
	}

	// Tampered signature test
	tampered := signed + "tampered"
	_, err = h.verifyCursor(tampered)
	if err == nil {
		t.Fatal("expected error on tampered cursor")
	}
}

func TestSyncVersions_InvalidUserCursor(t *testing.T) {
	h := NewSyncVersionsHandler(nil, config.SyncConfig{
		PageLimit:        500,
		MaxBytes:         262144,
		MaxPagesPerCycle: 4,
	}, "test-sync-secret-key-32bytes")

	userA := uuid.New()
	userB := uuid.New()

	cursorData := SyncCursorData{
		UserID:         userB.String(), // Cursor generated for userB
		WatermarkSeq:   1000,
		LastScannedSeq: 500,
		PageNo:         1,
		IssuedAt:       time.Now().Unix(),
	}
	signed := h.signCursor(cursorData)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sync/versions?after="+signed, nil)
	// Authenticated as userA
	ctx := authmw.WithAuthContext(req.Context(), userA.String(), "user", uuid.New().String())
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.HandleSyncVersions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for mismatched user cursor, got %d", rec.Code)
	}

	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	errMap, _ := body["error"].(map[string]any)
	if errMap["code"] != "INVALID_CURSOR" {
		t.Fatalf("expected INVALID_CURSOR code, got %v", errMap["code"])
	}
}
