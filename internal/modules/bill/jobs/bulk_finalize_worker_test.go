package jobs

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	billrepo "paysplit-backend/internal/modules/bill/repository"
	billusecase "paysplit-backend/internal/modules/bill/usecase"
)

// Job kind là hợp đồng giữa producer (hook BeforeCommit của StartBulkFinalize)
// và consumer (worker đăng ký trong bootstrap); đổi tên làm job mất tiêu.
func TestBulkFinalizeItemArgs_Kind_IsStable_AC4(t *testing.T) {
	if got := (BulkFinalizeItemArgs{}).Kind(); got != "bill_bulk_finalize_item" {
		t.Fatalf("Kind() = %q, want bill_bulk_finalize_item", got)
	}
}

func TestBulkFinalizeEnqueuer_NilClient_NoOp(t *testing.T) {
	e := NewEnqueuer(nil, 3)
	if err := e.EnqueueBulkFinalizeItemTx(context.Background(), nil, uuid.New(), uuid.New(), uuid.New()); err != nil {
		t.Fatalf("EnqueueBulkFinalizeItemTx with nil client = %v, want silent no-op", err)
	}
}

// Các lỗi parse phải bị bắt trước khi chạm vào nghiệp vụ, để River không giao
// lại mãi một job có payload hỏng mà không ai hiểu vì sao.
func TestBulkFinalizeWorker_MalformedArgs_RejectedBeforeBusinessLogic(t *testing.T) {
	repo := &workerFakeRepo{}
	w := NewBulkFinalizeWorker()
	w.SetService(billusecase.NewService(repo, nil, nil, nil, nil))

	cases := []BulkFinalizeItemArgs{
		{BatchID: "not-a-uuid", BillID: uuid.NewString(), GroupID: uuid.NewString()},
		{BatchID: uuid.NewString(), BillID: "nope", GroupID: uuid.NewString()},
		{BatchID: uuid.NewString(), BillID: uuid.NewString(), GroupID: ""},
	}
	for _, args := range cases {
		job := &river.Job[BulkFinalizeItemArgs]{Args: args}
		if err := w.Work(context.Background(), job); err == nil {
			t.Errorf("args %+v accepted, want a parse error", args)
		}
	}
	if repo.beginCalls != 0 {
		t.Fatalf("business layer was reached %d times on malformed payloads, want 0", repo.beginCalls)
	}
}

type workerFakeRepo struct {
	billrepo.Repository
	beginCalls int
}

func (f *workerFakeRepo) BeginTx(ctx context.Context) (pgx.Tx, error) {
	f.beginCalls++
	return nil, nil
}
