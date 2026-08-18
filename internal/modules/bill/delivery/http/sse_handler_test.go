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
)

type mockSSERepo struct {
	repository.Repository
	ocrJob *domain.OCRJob
}

func (m *mockSSERepo) GetLatestOCRJobByBillID(ctx context.Context, billID uuid.UUID) (*domain.OCRJob, error) {
	if m.ocrJob != nil && m.ocrJob.BillID == billID {
		return m.ocrJob, nil
	}
	return nil, domain.ErrOcrJobNotFound
}

func TestStreamBillEvents_Snapshot(t *testing.T) {
	billID := uuid.New()
	jobID := uuid.New()

	hub := billhttp.NewHub(nil)
	repo := &mockSSERepo{
		ocrJob: &domain.OCRJob{
			ID:     jobID,
			BillID: billID,
			Status: domain.OCRJobStatusQueued,
		},
	}

	handler := billhttp.NewSSEHandler(hub, repo, 100*time.Millisecond, 500*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/bills/"+billID.String()+"/events", nil)

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
	if !strings.Contains(body, "event: ping") {
		t.Errorf("expected body to contain ping event, got:\n%s", body)
	}
}
