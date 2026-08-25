package http

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"paysplit-backend/internal/modules/group/domain"
)

// ============================================================================
// GROUP BILL CLOSE V1 (Spec 0008): lock fields trên group payload, điều hướng
// batch của Captain và ánh xạ lỗi BULK_FINALIZE_IN_PROGRESS.
// ============================================================================

func TestNewGroupResponse_LockFields_AC1(t *testing.T) {
	lockedAt := time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC)

	open := newGroupResponse(domain.Group{ID: "g-1", Name: "Open", BillSubmissionLockedAt: nil})
	if open.BillSubmissionLocked || open.BillSubmissionLockedAt != nil {
		t.Fatalf("open group response = locked %v at %v, want false and nil", open.BillSubmissionLocked, open.BillSubmissionLockedAt)
	}

	locked := newGroupResponse(domain.Group{ID: "g-1", Name: "Locked", BillSubmissionLockedAt: &lockedAt})
	if !locked.BillSubmissionLocked || locked.BillSubmissionLockedAt == nil ||
		!locked.BillSubmissionLockedAt.Equal(lockedAt) {
		t.Fatalf("locked group response = locked %v at %v, want true at stored time",
			locked.BillSubmissionLocked, locked.BillSubmissionLockedAt)
	}
}

func TestGroupResponse_JSONFieldNames_Spec0008(t *testing.T) {
	lockedAt := time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC)
	raw, err := json.Marshal(newGroupResponse(domain.Group{ID: "g", Name: "n", Currency: "VND", BillSubmissionLockedAt: &lockedAt}))
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"id", "name", "currency", "created_at", "bill_submission_locked", "bill_submission_locked_at"} {
		if _, ok := keys[want]; !ok {
			t.Fatalf("group JSON missing public field %q in %v", want, keys)
		}
	}
}

func TestWriteDomainError_BulkFinalizeInProgress_409WithBatchID(t *testing.T) {
	rec := httptest.NewRecorder()
	writeDomainError(rec, &domain.BulkFinalizeInProgressError{ActiveBatchID: "batch-123"})

	if rec.Code != 409 {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var body struct {
		Error struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope: %v (%s)", err, rec.Body.String())
	}
	if body.Error.Code != "BULK_FINALIZE_IN_PROGRESS" {
		t.Fatalf("code = %q, want BULK_FINALIZE_IN_PROGRESS", body.Error.Code)
	}
	if body.Error.Details["active_batch_id"] != "batch-123" {
		t.Fatalf("details = %v, want active_batch_id=batch-123", body.Error.Details)
	}
}

func TestBulkFinalizeInProgressError_IsTypedAndWrappedSafe(t *testing.T) {
	var typed *domain.BulkFinalizeInProgressError
	err := error(&domain.BulkFinalizeInProgressError{ActiveBatchID: "b"})
	if !errors.As(err, &typed) || typed.ActiveBatchID != "b" {
		t.Fatalf("errors.As failed for typed bulk conflict: %v", err)
	}
}
