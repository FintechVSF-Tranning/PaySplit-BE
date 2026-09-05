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

// activeUserIDs đọc audience của nhóm, có nhớ kết quả trong phạm vi một
// transaction.
//
// Một lượt xác nhận thanh toán gọi notifyAll 3 + N lần (N là số hóa đơn liên
// quan), và trước đây mỗi lần lại chạy đúng truy vấn này. Danh sách thành viên
// không thể đổi giữa chừng vì transaction đang giữ khóa, nên đọc một lần là đủ.
func (r *postgresRepository) activeUserIDs(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) ([]uuid.UUID, error) {
	if cached, ok := audienceFromContext(ctx, groupID); ok {
		return cached, nil
	}
	ids, err := r.readActiveUserIDs(ctx, tx, groupID)
	if err != nil {
		return nil, err
	}
	rememberAudience(ctx, groupID, ids)
	return ids, nil
}

func (r *postgresRepository) readActiveUserIDs(ctx context.Context, tx pgx.Tx, groupID uuid.UUID) ([]uuid.UUID, error) {
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

// audienceCacheKey là khóa context mang bộ nhớ audience của một transaction.
type audienceCacheKey struct{}

type audienceCache struct {
	byGroup map[uuid.UUID][]uuid.UUID
}

// WithAudienceCache gắn bộ nhớ audience vào ctx cho trọn một transaction.
// Không gắn thì mọi thứ vẫn chạy đúng, chỉ là mỗi lần notify lại một truy vấn.
func WithAudienceCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(audienceCacheKey{}).(*audienceCache); ok {
		return ctx
	}
	return context.WithValue(ctx, audienceCacheKey{}, &audienceCache{
		byGroup: make(map[uuid.UUID][]uuid.UUID, 1),
	})
}

func audienceFromContext(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, bool) {
	cache, ok := ctx.Value(audienceCacheKey{}).(*audienceCache)
	if !ok {
		return nil, false
	}
	ids, ok := cache.byGroup[groupID]
	return ids, ok
}

func rememberAudience(ctx context.Context, groupID uuid.UUID, ids []uuid.UUID) {
	if cache, ok := ctx.Value(audienceCacheKey{}).(*audienceCache); ok {
		cache.byGroup[groupID] = ids
	}
}
