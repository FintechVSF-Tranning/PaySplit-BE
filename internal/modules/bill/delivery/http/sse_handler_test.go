package http_test

import (
	"context"
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
	authmw "paysplit-backend/internal/transport/http/middleware"
)

type mockSSERepo struct {
	repository.Repository
	ocrJob  *domain.OCRJob
	bill    *domain.Bill
	groupID uuid.UUID
}

func (m *mockSSERepo) GetBillByID(ctx context.Context, billID, groupID uuid.UUID) (*domain.Bill, error) {
	if m.bill != nil {
		return m.bill, nil
	}
	return &domain.Bill{ID: billID, GroupID: groupID, Version: 1, Status: domain.BillStatusDraft}, nil
}

func (m *mockSSERepo) GetBillOnlyByID(ctx context.Context, billID uuid.UUID) (*domain.Bill, error) {
	if m.bill != nil {
		return m.bill, nil
	}
	return &domain.Bill{ID: billID, GroupID: m.groupID}, nil
}

func (m *mockSSERepo) GetGroupMember(ctx context.Context, groupID, userID uuid.UUID) (*repository.GroupMember, error) {
	return &repository.GroupMember{
		ID:      uuid.New(),
		GroupID: groupID,
		UserID:  userID,
		Role:    "captain",
		Status:  "active",
	}, nil
}

func (m *mockSSERepo) GetLatestOCRJobByBillID(ctx context.Context, billID uuid.UUID) (*domain.OCRJob, error) {
	if m.ocrJob != nil && m.ocrJob.BillID == billID {
		return m.ocrJob, nil
	}
	return nil, domain.ErrOcrJobNotFound
}

func TestStreamBillEvents_Snapshot(t *testing.T) {
	billID := uuid.New()
	groupID := uuid.New()
	userID := uuid.New()
	jobID := uuid.New()

	hub := billhttp.NewHub(nil)
	repo := &mockSSERepo{
		groupID: groupID,
		bill: &domain.Bill{
			ID:      billID,
			GroupID: groupID,
		},
		ocrJob: &domain.OCRJob{
			ID:     jobID,
			BillID: billID,
			Status: domain.OCRJobStatusQueued,
		},
	}

	handler := billhttp.NewSSEHandler(hub, repo, 100*time.Millisecond, 500*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	ctx = authmw.WithAuthContext(ctx, userID.String(), "s-1", "user")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/bills/"+billID.String()+"/events?group_id="+groupID.String(), nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", billID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.StreamBillEvents(rec, req)
	}()

	// Đợi gửi snapshot và ping rồi cancel context
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "event: snapshot") {
		t.Errorf("expected body to contain snapshot event, got:\n%s", body)
	}
	if !strings.Contains(body, billID.String()) {
		t.Errorf("expected body to contain bill ID %s, got:\n%s", billID, body)
	}
	if !strings.Contains(body, "event: heartbeat") {
		t.Errorf("expected body to contain heartbeat event, got:\n%s", body)
	}
}
