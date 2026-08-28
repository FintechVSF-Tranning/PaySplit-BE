package appjob

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Trạng thái của job trong bảng app_jobs
const (
	StatusAvailable = "available"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusDiscarded = "discarded"
)

// Các loại job kind trong hệ thống PaySplit
const (
	KindNotificationPush        = "notification.push"
	KindOCRProcess              = "ocr.process"
	KindBulkFinalizeItem        = "bill.bulk_finalize_item"
	KindCleanupMediaAuth        = "cleanup.media_auth"
	KindSettlementReminder      = "settlement.reminder"
	KindSettlementStalled       = "settlement.stalled_confirmation"
	KindRetentionRawOCR         = "retention.raw_ocr"
	KindRetentionGroupEvents    = "retention.group_events"
)

// Job đại diện cho một bản ghi trong bảng app_jobs
type Job struct {
	ID             uuid.UUID       `json:"id"`
	Kind           string          `json:"kind"`
	Args           json.RawMessage `json:"args"`
	IdempotencyKey string          `json:"idempotency_key"`
	Status         string          `json:"status"`
	Priority       int             `json:"priority"`
	AvailableAt    time.Time       `json:"available_at"`
	Attempts       int             `json:"attempts"`
	MaxAttempts    int             `json:"max_attempts"`
	LeaseToken     *uuid.UUID      `json:"lease_token,omitempty"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	LastError      *string         `json:"last_error,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// JobHandler định nghĩa interface thực thi nghiệp vụ cho một loại job
type JobHandler interface {
	Execute(ctx context.Context, job Job) error
}

// HandlerFunc cho phép sử dụng function thường làm JobHandler
type HandlerFunc func(ctx context.Context, job Job) error

func (f HandlerFunc) Execute(ctx context.Context, job Job) error {
	return f(ctx, job)
}

// Enqueuer cung cấp API chèn job vào bảng app_jobs
type Enqueuer interface {
	Enqueue(ctx context.Context, kind string, idempotencyKey string, args any, priority int, delay time.Duration) (*Job, error)
	EnqueueTx(ctx context.Context, tx pgx.Tx, kind string, idempotencyKey string, args any, priority int, delay time.Duration) (*Job, error)
}

// SlotReservation chứa thông tin đặt chỗ một slot drain
type SlotReservation struct {
	SlotNo             int16     `json:"slot_no"`
	SlotToken          uuid.UUID `json:"slot_token"`
	WaveID             int64     `json:"wave_id"`
	DispatchGeneration int64     `json:"dispatch_generation"`
}

// DrainRequest Payload gửi từ dispatcher tới endpoint /internal/jobs/drain
type DrainRequest struct {
	SlotNo             int16     `json:"slot_no"`
	SlotToken          uuid.UUID `json:"slot_token"`
	WaveID             int64     `json:"wave_id"`
	DispatchGeneration int64     `json:"dispatch_generation"`
}

// DrainResponse Payload kết quả xử lý từ /internal/jobs/drain
type DrainResponse struct {
	Claimed   int `json:"claimed"`
	Completed int `json:"completed"`
	Retried   int `json:"retried"`
	Discarded int `json:"discarded"`
}

// DispatchResponse Payload kết quả điều phối từ /internal/jobs/dispatch
type DispatchResponse struct {
	WaveID          int64 `json:"wave_id"`
	ReservedSlots   int   `json:"reserved_slots"`
	DispatchedSlots int   `json:"dispatched_slots"`
}
