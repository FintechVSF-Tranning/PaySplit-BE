package database

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrGroupNotActive = errors.New("group is missing or archived")

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// LockActiveGroup is the shared transaction boundary for every group scoped
// mutation. Callers must invoke it before their first scoped write and map
// ErrGroupNotActive to their module's public not found or forbidden error.
func LockActiveGroup(ctx context.Context, db rowQuerier, groupID any) error {
	return lockActiveGroup(ctx, db, groupID, false)
}

// LockActiveGroupNowait applies the same active group predicate without
// waiting for a concurrent group mutation. PostgreSQL lock contention remains
// visible to the caller so it can map SQLSTATE 55P03 to a stable conflict.
func LockActiveGroupNowait(ctx context.Context, db rowQuerier, groupID any) error {
	return lockActiveGroup(ctx, db, groupID, true)
}

func lockActiveGroup(ctx context.Context, db rowQuerier, groupID any, nowait bool) error {
	query := `SELECT id FROM groups WHERE id=$1 AND status='active' FOR UPDATE`
	if nowait {
		query += ` NOWAIT`
	}

	var id pgtype.UUID
	if err := db.QueryRow(ctx, query, groupID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGroupNotActive
		}
		return err
	}
	return nil
}
