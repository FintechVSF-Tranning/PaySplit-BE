package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"paysplit-backend/internal/modules/notification/domain"
	"paysplit-backend/internal/modules/notification/repository"
	dbgen "paysplit-backend/internal/modules/notification/repository/postgres/sqlc"

	"github.com/brpaz/lib-go/pagination"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type postgresRepository struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) repository.Repository {
	if db == nil {
		panic("notification repository pool must not be nil")
	}
	return &postgresRepository{db: db}
}

func (r *postgresRepository) CreateNotification(ctx context.Context, notif *domain.Notification) error {
	return r.createNotification(ctx, dbgen.New(r.db), notif)
}

func (r *postgresRepository) CreateNotificationTx(ctx context.Context, ex repository.Executor, notif *domain.Notification) error {
	tx, ok := ex.(pgx.Tx)
	if !ok {
		return fmt.Errorf("create notification: invalid transaction executor")
	}
	return r.createNotification(ctx, dbgen.New(tx), notif)
}

func (r *postgresRepository) createNotification(ctx context.Context, q *dbgen.Queries, notif *domain.Notification) error {
	userID, err := uuid.Parse(notif.UserID)
	if err != nil {
		return domain.ErrInvalidInput
	}

	row, err := q.CreateNotification(ctx, dbgen.CreateNotificationParams{
		UserID:  pgUUID(userID),
		Type:    notif.Type,
		Title:   notif.Title,
		Body:    notif.Body,
		Payload: []byte(notif.Payload),
	})
	if err != nil {
		return fmt.Errorf("create notification: %w", err)
	}

	mapped := mapGeneratedNotification(row)
	notif.ID = mapped.ID
	notif.CreatedAt = mapped.CreatedAt
	return nil
}

// WithTx mở một transaction Postgres, chạy fn với Executor tương ứng, rồi commit khi fn
// thành công hoặc rollback khi fn lỗi (rollback sau commit là no-op an toàn).
func (r *postgresRepository) WithTx(ctx context.Context, fn func(ctx context.Context, ex repository.Executor) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (r *postgresRepository) GetNotificationByID(ctx context.Context, notificationID string) (domain.Notification, error) {
	nid, err := uuid.Parse(notificationID)
	if err != nil {
		return domain.Notification{}, domain.ErrInvalidInput
	}

	row, err := dbgen.New(r.db).GetNotificationByID(ctx, pgUUID(nid))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Notification{}, domain.ErrNotificationNotFound
		}
		return domain.Notification{}, fmt.Errorf("get notification: %w", err)
	}
	return mapGeneratedNotification(row), nil
}

func (r *postgresRepository) ListByUserID(ctx context.Context, userID string, pager pagination.OffsetPager) (pagination.Page[domain.Notification], error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return pagination.Page[domain.Notification]{}, domain.ErrInvalidInput
	}

	total, err := dbgen.New(r.db).CountNotificationsByUserID(ctx, pgUUID(uid))
	if err != nil {
		return pagination.Page[domain.Notification]{}, fmt.Errorf("count notifications: %w", err)
	}

	rows, err := dbgen.New(r.db).ListNotificationsByUserID(ctx, dbgen.ListNotificationsByUserIDParams{
		UserID: pgUUID(uid),
		Limit:  int32(pager.Limit()),
		Offset: int32(pager.Offset()),
	})
	if err != nil {
		return pagination.Page[domain.Notification]{}, fmt.Errorf("list notifications: %w", err)
	}

	items := make([]domain.Notification, 0, len(rows))
	for _, row := range rows {
		items = append(items, mapGeneratedNotification(row))
	}

	return pagination.NewPage(items, total, pager), nil
}

func (r *postgresRepository) CountUnread(ctx context.Context, userID string) (int64, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return 0, domain.ErrInvalidInput
	}

	count, err := dbgen.New(r.db).CountUnreadNotifications(ctx, pgUUID(uid))
	if err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return count, nil
}

func (r *postgresRepository) MarkAsRead(ctx context.Context, userID, notificationID string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return domain.ErrInvalidInput
	}
	nid, err := uuid.Parse(notificationID)
	if err != nil {
		return domain.ErrInvalidInput
	}

	affected, err := dbgen.New(r.db).MarkNotificationAsRead(ctx, dbgen.MarkNotificationAsReadParams{
		ID:     pgUUID(nid),
		UserID: pgUUID(uid),
	})
	if err != nil {
		return fmt.Errorf("mark notification as read: %w", err)
	}
	if affected == 0 {
		return domain.ErrNotificationNotFound
	}
	return nil
}

func (r *postgresRepository) MarkAllAsRead(ctx context.Context, userID string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return domain.ErrInvalidInput
	}

	if err := dbgen.New(r.db).MarkAllNotificationsAsRead(ctx, pgUUID(uid)); err != nil {
		return fmt.Errorf("mark all notifications as read: %w", err)
	}
	return nil
}

func (r *postgresRepository) GetActiveFCMTokenByUserID(ctx context.Context, userID string) (string, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return "", domain.ErrInvalidInput
	}

	res, err := dbgen.New(r.db).GetActiveFCMTokenByUserID(ctx, pgUUID(uid))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("get active FCM token: %w", err)
	}
	if res.Valid {
		return res.String, nil
	}
	return "", nil
}

func (r *postgresRepository) ClearFCMToken(ctx context.Context, userID, fcmToken string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return domain.ErrInvalidInput
	}

	err = dbgen.New(r.db).ClearFCMToken(ctx, dbgen.ClearFCMTokenParams{
		FcmToken: pgtype.Text{String: fcmToken, Valid: fcmToken != ""},
		UserID:   pgUUID(uid),
	})
	if err != nil {
		return fmt.Errorf("clear fcm token: %w", err)
	}
	return nil
}

func mapGeneratedNotification(n dbgen.Notification) domain.Notification {
	id := uuid.UUID(n.ID.Bytes).String()
	userID := uuid.UUID(n.UserID.Bytes).String()
	var readAt *time.Time
	if n.ReadAt.Valid {
		t := n.ReadAt.Time
		readAt = &t
	}
	return domain.Notification{
		ID:        id,
		UserID:    userID,
		Type:      n.Type,
		Title:     n.Title,
		Body:      n.Body,
		Payload:   json.RawMessage(n.Payload),
		ReadAt:    readAt,
		CreatedAt: n.CreatedAt.Time,
	}
}

func pgUUID(v uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: v, Valid: true} }
