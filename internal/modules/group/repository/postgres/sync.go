package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"paysplit-backend/internal/modules/group/domain"
	dbgen "paysplit-backend/internal/modules/group/repository/postgres/sqlc"
	"paysplit-backend/internal/platform/realtime"
)

// notifyChannel là kênh pg_notify mà mọi instance API lắng nghe để nhận sự kiện
// nhóm phát ra từ bất kỳ tiến trình nào.
const notifyChannel = "group_events"

// maxNotifyPayload là ngưỡng an toàn dưới giới hạn 8000 byte của pg_notify.
// Vượt ngưỡng thì phát bản "gầy" (bỏ data): client thấy version nhảy cóc và tự
// gọi /sync để lấy nội dung đầy đủ — nhánh vốn đã phải đúng sẵn.
// maxSyncEvents chặn trên số sự kiện trả về trong một lần catch-up. Client cần
// nhiều hơn thế thì nhận snapshot rẻ hơn là phát lại từng bước.
const maxSyncEvents = 200

// emitGroupEvent bump roster_version, ghi nhật ký và hẹn pg_notify — tất cả
// trong transaction của mutation gọi nó.
//
// Ba tính chất phải giữ:
//   - Mọi mutation đã gọi database.LockActiveGroup trước đó, nên khóa dòng
//     groups đang được giữ: version cấp ra liền mạch và đúng thứ tự commit.
//   - INSERT nhật ký nằm cùng transaction với thay đổi dữ liệu (transactional
//     outbox): không bao giờ có sự kiện mồ côi hay thay đổi không có sự kiện.
//   - pg_notify trong transaction chỉ được PostgreSQL phát khi COMMIT thành
//     công, nên client không bao giờ nhận sự kiện của một transaction rollback.
func listActiveUserIDs(ctx context.Context, q *dbgen.Queries, gid uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.ListActiveMembers(ctx, pgUUID(gid))
	if err != nil {
		return nil, fmt.Errorf("list active members for audience: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, uuid.UUID(row.UserID.Bytes))
	}
	return realtime.NormalizeAudience(ids), nil
}

func emitGroupEvent(ctx context.Context, tx pgx.Tx, gid uuid.UUID, eventType string, payload map[string]any, audience []uuid.UUID) error {
	q := dbgen.New(tx)

	version, err := q.BumpRosterVersion(ctx, pgUUID(gid))
	if err != nil {
		return fmt.Errorf("bump roster version for %s: %w", eventType, err)
	}

	if payload == nil {
		payload = map[string]any{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s event payload: %w", eventType, err)
	}
	if err = q.InsertGroupEvent(ctx, dbgen.InsertGroupEventParams{
		GroupID:   pgUUID(gid),
		Version:   version,
		EventType: eventType,
		Payload:   body,
	}); err != nil {
		return fmt.Errorf("insert %s event: %w", eventType, err)
	}

	encoded, _, err := realtime.EncodeGroupEnvelope(realtime.GroupEnvelope{
		GroupID:         gid,
		Version:         version,
		Type:            eventType,
		Data:            body,
		AudienceUserIDs: audience,
	})
	if err != nil {
		return fmt.Errorf("encode %s notify envelope: %w", eventType, err)
	}
	if err = realtime.Notify(ctx, tx, notifyChannel, encoded); err != nil {
		return fmt.Errorf("notify %s event: %w", eventType, err)
	}
	return nil
}

// memberEventPayload dựng phần "member" của các sự kiện thêm thành viên. Nó trả
// avatar_object_key chứ không phải URL: tầng delivery mới là nơi biết cách dựng
// URL, giống hệt newMemberResponse.
func memberEventPayload(ctx context.Context, q *dbgen.Queries, gid uuid.UUID, membershipID pgtype.UUID) (map[string]any, error) {
	row, err := q.GetMemberSnapshot(ctx, dbgen.GetMemberSnapshotParams{ID: membershipID, GroupID: pgUUID(gid)})
	if err != nil {
		return nil, fmt.Errorf("load member snapshot: %w", err)
	}
	member := map[string]any{
		"membership_id":     uuid.UUID(row.MembershipID.Bytes).String(),
		"user_id":           uuid.UUID(row.UserID.Bytes).String(),
		"display_name":      row.DisplayName,
		"avatar_object_key": nil,
		"role":              fmt.Sprint(row.Role),
		"joined_at":         row.JoinedAt.Time,
	}
	if row.AvatarObjectKey.Valid {
		member["avatar_object_key"] = row.AvatarObjectKey.String
	}
	return member, nil
}

// activeMemberCount đọc lại sĩ số sau khi mutation đã ghi, để màn hình danh sách
// nhóm cập nhật được con số mà không phải gọi thêm API.
func activeMemberCount(ctx context.Context, q *dbgen.Queries, gid uuid.UUID) (int64, error) {
	count, err := q.CountActiveMembers(ctx, pgUUID(gid))
	if err != nil {
		return 0, fmt.Errorf("count active members for event: %w", err)
	}
	return count, nil
}

func (r *postgresRepository) GetSyncCursor(ctx context.Context, groupID, callerUserID string) (domain.SyncCursor, error) {
	gid, err := uuid.Parse(groupID)
	if err != nil {
		return domain.SyncCursor{}, domain.ErrGroupNotFound
	}
	uid, err := uuid.Parse(callerUserID)
	if err != nil {
		return domain.SyncCursor{}, domain.ErrGroupNotFound
	}

	q := dbgen.New(r.pool)
	// Nhóm không tồn tại và caller không phải thành viên phải cùng trả một lỗi,
	// đúng bất biến 12 của spec 0002.
	if _, err = q.GetActiveMembership(ctx, dbgen.GetActiveMembershipParams{GroupID: pgUUID(gid), UserID: pgUUID(uid)}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.SyncCursor{}, domain.ErrGroupNotFound
		}
		return domain.SyncCursor{}, fmt.Errorf("get caller membership: %w", err)
	}

	row, err := q.GetGroupSyncCursor(ctx, pgUUID(gid))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SyncCursor{}, domain.ErrGroupNotFound
	}
	if err != nil {
		return domain.SyncCursor{}, fmt.Errorf("get group sync cursor: %w", err)
	}
	return domain.SyncCursor{Current: row.CurrentVersion, Oldest: row.OldestVersion}, nil
}

func (r *postgresRepository) ListEventsSince(ctx context.Context, groupID string, since int64, limit int) ([]domain.SyncEvent, error) {
	gid, err := uuid.Parse(groupID)
	if err != nil {
		return nil, domain.ErrGroupNotFound
	}
	if limit <= 0 || limit > maxSyncEvents {
		limit = maxSyncEvents
	}

	rows, err := dbgen.New(r.pool).ListGroupEventsSince(ctx, dbgen.ListGroupEventsSinceParams{
		GroupID: pgUUID(gid),
		Version: since,
		Limit:   int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list group events: %w", err)
	}

	events := make([]domain.SyncEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, domain.SyncEvent{
			Version:   row.Version,
			Type:      row.EventType,
			Payload:   json.RawMessage(row.Payload),
			CreatedAt: row.CreatedAt.Time,
		})
	}
	return events, nil
}

func (r *postgresRepository) DeleteEventsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	deleted, err := dbgen.New(r.pool).DeleteGroupEventsBefore(ctx, pgtype.Timestamptz{Time: cutoff, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("delete group events before %s: %w", cutoff.Format(time.RFC3339), err)
	}
	return deleted, nil
}
