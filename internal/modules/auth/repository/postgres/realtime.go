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

func uuidStrings(ids []uuid.UUID) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// PublishSessionEnded phát sự kiện đóng phiên NGOÀI transaction.
//
// Đường bình thường là notifySessionEnded chạy bên trong tx, nên NOTIFY chỉ thoát
// ra khi commit thành công. Hàm này dành cho một ca duy nhất: phiên đã bị xoá khỏi
// Redis (nguồn phán quyết) nhưng transaction ghi audit sau đó hỏng. Khi ấy người
// dùng đã thực sự bị đăng xuất, nhưng không control nào được phát, và stream SSE
// đang mở sẽ tiếp tục chạy tới maxConnectionAge.
func (r *postgresRepository) PublishSessionEnded(ctx context.Context, sids []string) error {
	if len(sids) == 0 {
		return nil
	}
	parsed := make([]uuid.UUID, 0, len(sids))
	for _, raw := range sids {
		id, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		parsed = append(parsed, id)
	}
	if len(parsed) == 0 {
		return nil
	}
	return r.events.NotifySessionEnded(ctx, r.pool, parsed)
}
