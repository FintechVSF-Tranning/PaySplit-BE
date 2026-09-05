package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"paysplit-backend/internal/modules/settlement/repository"
	"paysplit-backend/internal/platform/realtime"
)

func SetRealtimePublisher(repo repository.Repository, events *realtime.Publisher) {
	if r, ok := repo.(*postgresRepository); ok {
		r.events = events
	}
}

func (r *postgresRepository) activeUserIDs(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT user_id FROM group_members WHERE group_id=$1 AND status='active'`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return realtime.NormalizeAudience(ids), rows.Err()
}

func (r *postgresRepository) distinctBillIDs(ctx context.Context, tx pgx.Tx, debtIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(debtIDs) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT bill_id FROM debts WHERE id = ANY($1::uuid[]) ORDER BY bill_id`, debtIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *postgresRepository) notifyInvalidate(ctx context.Context, tx pgx.Tx, audience []uuid.UUID, body realtime.InvalidateBody) error {
	return r.events.NotifyInvalidate(ctx, tx, audience, body)
}

// notifyNotificationCreated báo danh sách thông báo đã đổi cho đúng người vừa
// nhận thông báo. Khác notifyAll, audience ở đây không phải cả nhóm — máy của
// người không nhận gì thì không có lý do gì phải gọi lại API danh sách.
func (r *postgresRepository) notifyNotificationCreated(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, recipients []string) error {
	ids := make([]uuid.UUID, 0, len(recipients))
	for _, raw := range recipients {
		id, err := uuid.Parse(raw)
		if err != nil {
			return fmt.Errorf("parse notification recipient %q: %w", raw, err)
		}
		ids = append(ids, id)
	}
	ids = realtime.NormalizeAudience(ids)
	if len(ids) == 0 {
		return nil
	}
	return r.notifyInvalidate(ctx, tx, ids, realtime.InvalidateBody{
		Scope:   realtime.ScopeNotification,
		GroupID: groupID,
		Type:    realtime.TypeNotificationCreated,
	})
}

func (r *postgresRepository) notifyAll(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, typ, scope string, resourceID *uuid.UUID) error {
	audience, err := r.activeUserIDs(ctx, tx, groupID)
	if err != nil {
		return err
	}
	return r.notifyInvalidate(ctx, tx, audience, realtime.InvalidateBody{
		Scope:      scope,
		GroupID:    groupID,
		ResourceID: resourceID,
		Type:       typ,
	})
}
