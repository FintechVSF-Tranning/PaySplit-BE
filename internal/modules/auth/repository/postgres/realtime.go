package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"paysplit-backend/internal/modules/auth/repository"
	"paysplit-backend/internal/platform/realtime"
)

func SetRealtimePublisher(repo repository.Repository, events *realtime.Publisher) {
	if r, ok := repo.(*postgresRepository); ok {
		r.events = events
	}
}

func collectUUIDs(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, query, args...)
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

func (r *postgresRepository) notifySessionEnded(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) error {
	return r.events.NotifySessionEnded(ctx, tx, ids)
}
