package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	billhttp "paysplit-backend/internal/modules/bill/delivery/http"
	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	"paysplit-backend/internal/modules/bill/usecase"
	authmw "paysplit-backend/internal/transport/http/middleware"
)

// ============================================================================
// GROUP BILL CLOSE V1 endpoints (Spec 0008): POST lock-submissions,
// POST finalize-all, GET bill-finalize-batches/{batchId}.
// ============================================================================

func newCloseTestHandler(t *testing.T, repo *mockHandlerRepo, callerUserID uuid.UUID) http.Handler {
	t.Helper()
	service := usecase.NewService(repo, nil, nil, nil, nil)
	handler := billhttp.NewHandler(service, nil)
	r := chi.NewRouter()
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authmw.WithAuthContext(req.Context(), callerUserID.String(), "s-close", "user")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
	// Mount giống hệt bootstrap để đường dẫn test khớp production.
	r.Route("/api/v1", func(api chi.Router) {
		api.Route("/groups", func(groups chi.Router) {
			handler.RegisterGroupCloseRoutes(groups, auth)
		})
	})
	return r
}

func newCloseRepo(groupID, userID uuid.UUID, role string) *mockHandlerRepo {
	return &mockHandlerRepo{
		member: &repository.GroupMember{ID: uuid.New(), GroupID: groupID, UserID: userID, Role: role, Status: "active"},
	}
}

func doClose(t *testing.T, h http.Handler, method, path, key string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %s: %v (raw: %s)", path, err, rec.Body.String())
	}
	return rec.Code, body
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	if e == nil {
		return ""
	}
	c, _ := e["code"].(string)
	return c
}

func errDetails(body map[string]any) map[string]any {
	e, _ := body["error"].(map[string]any)
	if e == nil {
		return nil
	}
	d, _ := e["details"].(map[string]any)
	return d
}

func dataOf(body map[string]any) map[string]any {
	d, _ := body["data"].(map[string]any)
	return d
}

func TestLockSubmissions_Captain_ReturnsLockedState_AC1(t *testing.T) {
	gid, uid := uuid.New(), uuid.New()
	repo := newCloseRepo(gid, uid, "captain")
	lockedAt := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	repo.lockResult = &repository.LockSubmissionsResult{LockedAt: lockedAt, LockedNow: true}
	h := newCloseTestHandler(t, repo, uid)

	status, body := doClose(t, h, http.MethodPost, "/api/v1/groups/"+gid.String()+"/bills/lock-submissions", "k-lock-1")

	if status != http.StatusOK || !body["success"].(bool) {
		t.Fatalf("status=%d success=%v, want 200 true", status, body["success"])
	}
	data := dataOf(body)
	if data["bill_submission_locked"] != true {
		t.Fatalf("bill_submission_locked = %v, want true", data["bill_submission_locked"])
	}
	ts, _ := data["bill_submission_locked_at"].(string)
	if !strings.HasPrefix(ts, "2026-08-25T10:00:00") {
		t.Fatalf("bill_submission_locked_at = %q, want the stored PostgreSQL time", ts)
	}
}

func TestLockSubmissions_ReplaySameKey_ReplaysStoredResponse_AC1(t *testing.T) {
	gid, uid := uuid.New(), uuid.New()
	repo := newCloseRepo(gid, uid, "captain")
	h := newCloseTestHandler(t, repo, uid)

	path := "/api/v1/groups/" + gid.String() + "/bills/lock-submissions"
	s1, b1 := doClose(t, h, http.MethodPost, path, "k-replay")
	s2, b2 := doClose(t, h, http.MethodPost, path, "k-replay")

	raw1, _ := json.Marshal(b1)
	raw2, _ := json.Marshal(b2)
	if s1 != http.StatusOK || s2 != http.StatusOK || string(raw1) != string(raw2) {
		t.Fatalf("replay changed response: first=(%d %s) second=(%d %s)", s1, raw1, s2, raw2)
	}
}

func TestLockSubmissions_NonCaptain_Forbidden_AC10(t *testing.T) {
	gid, uid := uuid.New(), uuid.New()
	repo := newCloseRepo(gid, uid, "member")
	repo.lockErr = domain.ErrCaptainRequired
	h := newCloseTestHandler(t, repo, uid)

	status, body := doClose(t, h, http.MethodPost, "/api/v1/groups/"+gid.String()+"/bills/lock-submissions", "k-mem")
	if status != http.StatusForbidden || errCode(body) != "CAPTAIN_REQUIRED" {
		t.Fatalf("status=%d code=%q, want 403 CAPTAIN_REQUIRED", status, errCode(body))
	}
}

func TestLockSubmissions_OutsiderOrArchived_GroupNotFound_AC10(t *testing.T) {
	gid, uid := uuid.New(), uuid.New()
	repo := newCloseRepo(gid, uid, "member")
	repo.lockErr = domain.ErrGroupNotFound
	h := newCloseTestHandler(t, repo, uid)

	status, body := doClose(t, h, http.MethodPost, "/api/v1/groups/"+gid.String()+"/bills/lock-submissions", "")
	if status != http.StatusNotFound || errCode(body) != "GROUP_NOT_FOUND" {
		t.Fatalf("status=%d code=%q, want 404 GROUP_NOT_FOUND", status, errCode(body))
	}
}

func TestStartBulkFinalize_Accepted_202WithBatchSummaryAndLockState_AC4(t *testing.T) {
	gid, uid := uuid.New(), uuid.New()
	repo := newCloseRepo(gid, uid, "captain")
	batchAt := time.Now().UTC().Truncate(time.Second)
	repo.bulkResult = &repository.StartBulkFinalizeResult{
		Batch: &domain.FinalizeBatch{
			ID: uuid.NewString(), GroupID: gid.String(),
			Status:      domain.BatchStatusQueued,
			TargetCount: 3, FinalizedCount: 0, FailedCount: 0,
			CreatedAt: batchAt, UpdatedAt: batchAt,
		},
		SubmissionLockedAt:      batchAt,
		CapturedReviewedCount:   1,
		CapturedUnreviewedCount: 2,
	}
	h := newCloseTestHandler(t, repo, uid)

	status, body := doClose(t, h, http.MethodPost, "/api/v1/groups/"+gid.String()+"/bills/finalize-all", "k-bulk-1")

	if status != http.StatusAccepted {
		t.Fatalf("status=%d, want 202", status)
	}
	data := dataOf(body)
	for _, k := range []string{"batch", "bill_submission_locked", "bill_submission_locked_at", "captured_reviewed_count", "captured_unreviewed_count"} {
		if _, ok := data[k]; !ok {
			t.Fatalf("response missing key %q in %v", k, data)
		}
	}
	batch := data["batch"].(map[string]any)
	if batch["status"] != domain.BatchStatusQueued || batch["target_count"] != float64(3) {
		t.Fatalf("batch summary = %v, want queued with target 3", batch)
	}
	if data["captured_reviewed_count"] != float64(1) || data["captured_unreviewed_count"] != float64(2) {
		t.Fatalf("capture counts = %v/%v, want 1/2", data["captured_reviewed_count"], data["captured_unreviewed_count"])
	}
}

func TestStartBulkFinalize_ActiveBatchConflict_IncludesSafeBatchID_AC7(t *testing.T) {
	gid, uid := uuid.New(), uuid.New()
	repo := newCloseRepo(gid, uid, "captain")
	activeID := uuid.NewString()
	repo.bulkErr = &domain.BulkFinalizeInProgressError{ActiveBatchID: activeID}
	h := newCloseTestHandler(t, repo, uid)

	status, body := doClose(t, h, http.MethodPost, "/api/v1/groups/"+gid.String()+"/bills/finalize-all", "")

	if status != http.StatusConflict || errCode(body) != "BULK_FINALIZE_IN_PROGRESS" {
		t.Fatalf("status=%d code=%q, want 409 BULK_FINALIZE_IN_PROGRESS", status, errCode(body))
	}
	details := errDetails(body)
	if details == nil || details["active_batch_id"] != activeID {
		t.Fatalf("details = %v, want active_batch_id=%q so the Captain can resume that batch", details, activeID)
	}
}

func TestStartBulkFinalize_InvalidGroupID_400(t *testing.T) {
	uid := uuid.New()
	repo := newCloseRepo(uuid.New(), uid, "captain")
	h := newCloseTestHandler(t, repo, uid)

	status, body := doClose(t, h, http.MethodPost, "/api/v1/groups/not-a-uuid/bills/finalize-all", "")
	if status != http.StatusBadRequest || errCode(body) != "INVALID_GROUP_ID" {
		t.Fatalf("status=%d code=%q, want 400 INVALID_GROUP_ID", status, errCode(body))
	}
}

func TestGetFinalizeBatch_MemberForbidden_403_AC10(t *testing.T) {
	gid, uid := uuid.New(), uuid.New()
	repo := newCloseRepo(gid, uid, "member") // caller is an active ordinary member
	h := newCloseTestHandler(t, repo, uid)

	path := "/api/v1/groups/" + gid.String() + "/bill-finalize-batches/" + uuid.NewString()
	status, body := doClose(t, h, http.MethodGet, path, "")
	if status != http.StatusForbidden || errCode(body) != "CAPTAIN_REQUIRED" {
		t.Fatalf("status=%d code=%q, want 403 CAPTAIN_REQUIRED so members cannot infer batch results", status, errCode(body))
	}
}

func TestGetFinalizeBatch_Captain_ReturnsBatchAndItems_AC6(t *testing.T) {
	gid, uid := uuid.New(), uuid.New()
	repo := newCloseRepo(gid, uid, "captain")
	batchID := uuid.New()
	code := domain.ItemErrorNotReady
	name := "Bill Reviewed"
	processed := time.Now().UTC()
	repo.batch = &domain.FinalizeBatch{
		ID: batchID.String(), GroupID: gid.String(),
		Status:      domain.BatchStatusCompleted,
		TargetCount: 2, FinalizedCount: 1, FailedCount: 1,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	repo.batchItems = []*domain.BatchItemResult{
		{FinalizeBatchItem: domain.FinalizeBatchItem{
			BillID: uuid.NewString(), BillVersion: 1, CapturedReviewed: true,
			Status: domain.BatchItemFinalized, ProcessedAt: &processed,
		}, BillDisplayName: &name},
		{FinalizeBatchItem: domain.FinalizeBatchItem{
			BillID: uuid.NewString(), BillVersion: 4, CapturedReviewed: false,
			Status: domain.BatchItemFailed, ErrorCode: &code, ProcessedAt: &processed,
		}},
	}
	next := "cursor-token"
	repo.batchNext = &next
	h := newCloseTestHandler(t, repo, uid)

	path := "/api/v1/groups/" + gid.String() + "/bill-finalize-batches/" + batchID.String() + "?limit=2"
	status, body := doClose(t, h, http.MethodGet, path, "")

	if status != http.StatusOK {
		t.Fatalf("status=%d, want 200", status)
	}
	data := dataOf(body)
	items, _ := data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	first := items[0].(map[string]any)
	if first["bill_display_name"] != name || first["status"] != domain.BatchItemFinalized {
		t.Fatalf("first item = %v, want display name and finalized status preserved", first)
	}
	second := items[1].(map[string]any)
	if _, ok := second["bill_display_name"]; !ok {
		t.Fatalf("failed item = %v, want bill_display_name key present with null value", second)
	}
	if second["error_code"] != domain.ItemErrorNotReady || second["bill_display_name"] != nil {
		t.Fatalf("failed item = %v, want stable error code and null display name for missing bill", second)
	}
	if data["next_cursor"] != next {
		t.Fatalf("next_cursor = %v, want %q", data["next_cursor"], next)
	}
}

func TestGetFinalizeBatch_BatchNotFound_404(t *testing.T) {
	gid, uid := uuid.New(), uuid.New()
	repo := newCloseRepo(gid, uid, "captain")
	repo.batchErr = domain.ErrBatchNotFound
	h := newCloseTestHandler(t, repo, uid)

	path := "/api/v1/groups/" + gid.String() + "/bill-finalize-batches/" + uuid.NewString()
	status, body := doClose(t, h, http.MethodGet, path, "")
	if status != http.StatusNotFound || errCode(body) != "BATCH_NOT_FOUND" {
		t.Fatalf("status=%d code=%q, want 404 BATCH_NOT_FOUND", status, errCode(body))
	}
}

func TestGetFinalizeBatch_LimitBounds_Validated(t *testing.T) {
	cases := []struct {
		limit  string
		wantOK bool
	}{
		{"", true},
		{"1", true},
		{"100", true},
		{"0", false},
		{"101", false},
		{"abc", false},
		{"-5", false},
	}
	for _, tc := range cases {
		gid, uid := uuid.New(), uuid.New()
		repo := newCloseRepo(gid, uid, "captain")
		repo.batch = &domain.FinalizeBatch{ID: uuid.NewString(), GroupID: gid.String(), Status: domain.BatchStatusCompleted, CreatedAt: time.Now()}
		h := newCloseTestHandler(t, repo, uid)
		path := "/api/v1/groups/" + gid.String() + "/bill-finalize-batches/" + uuid.NewString() + "?limit=" + tc.limit
		status, body := doClose(t, h, http.MethodGet, path, "")
		gotOK := status == http.StatusOK
		if gotOK != tc.wantOK {
			t.Errorf("limit=%q status=%d (%s), wantOK=%v", tc.limit, status, errCode(body), tc.wantOK)
		}
	}
}
