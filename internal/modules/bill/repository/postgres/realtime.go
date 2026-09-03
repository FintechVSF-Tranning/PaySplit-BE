package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/repository"
	"paysplit-backend/internal/modules/bill/repository/postgres/sqlc"
	"paysplit-backend/internal/platform/realtime"
)

func SetRealtimePublisher(repo repository.Repository, events *realtime.Publisher) {
	if r, ok := repo.(*postgresRepository); ok {
		r.events = events
	}
}

func (r *postgresRepository) activeUserIDs(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) ([]uuid.UUID, error) {
	members, err := sqlc.New(tx).ListActiveGroupMembers(ctx, pgtype.UUID{Bytes: groupID, Valid: true})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(members))
	for _, member := range members {
		ids = append(ids, uuid.UUID(member.UserID.Bytes))
	}
	return realtime.NormalizeAudience(ids), nil
}

func (r *postgresRepository) notifyBillInvalidate(ctx context.Context, tx pgx.Tx, groupID, billID uuid.UUID, version int32, typ string) error {
	audience, err := r.activeUserIDs(ctx, tx, groupID)
	if err != nil {
		return err
	}
	ver := version
	return r.events.NotifyInvalidate(ctx, tx, audience, realtime.InvalidateBody{
		Scope:           realtime.ScopeBill,
		GroupID:         groupID,
		ResourceID:      &billID,
		ResourceVersion: &ver,
		Type:            typ,
	})
}

func (r *postgresRepository) notifyGroupInvalidate(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, typ string) error {
	audience, err := r.activeUserIDs(ctx, tx, groupID)
	if err != nil {
		return err
	}
	return r.events.NotifyInvalidate(ctx, tx, audience, realtime.InvalidateBody{
		Scope:   realtime.ScopeGroup,
		GroupID: groupID,
		Type:    typ,
	})
}

func (r *postgresRepository) notifyOCR(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, job *domain.OCRJob) error {
	if job == nil {
		return nil
	}
	audience, err := r.activeUserIDs(ctx, tx, groupID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(ocrPublicData(job))
	if err != nil {
		return err
	}
	return realtime.NotifyBill(ctx, tx, realtime.BillEnvelope{
		GroupID:         groupID,
		BillID:          job.BillID,
		Type:            "ocr.updated",
		Data:            payload,
		AudienceUserIDs: audience,
	})
}

func ocrPublicData(job *domain.OCRJob) map[string]any {
	warnings := []string{}
	if job.Candidate != nil {
		warnings = job.Candidate.Warnings
		if warnings == nil {
			warnings = []string{}
		}
	}
	var errMsg any
	if job.Status == domain.OCRJobStatusFailed && job.ErrorMessage != nil {
		errMsg = *job.ErrorMessage
	}
	var completed any
	if job.CompletedAt != nil {
		completed = job.CompletedAt.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"job_id":       job.ID,
		"status":       string(job.Status),
		"attempts":     job.Attempts,
		"error":        errMsg,
		"warnings":     warnings,
		"created_at":   job.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":   job.UpdatedAt.UTC().Format(time.RFC3339),
		"completed_at": completed,
	}
}
