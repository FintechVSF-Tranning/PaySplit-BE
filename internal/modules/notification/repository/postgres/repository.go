package postgres

import (
	"context"
	"errors"
	"fmt"

	"paysplit-backend/internal/modules/notification/domain"
	"paysplit-backend/internal/modules/notification/repository"

	"github.com/brpaz/lib-go/pagination"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type postgresRepository struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) repository.Repository {
	return &postgresRepository{db: db}
}

func (r *postgresRepository) CreateNotification(ctx context.Context, notif *domain.Notification) error {
	query := `
		INSERT INTO notifications (user_id, type, title, body, payload)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at
	`
	err := r.db.QueryRow(ctx, query, notif.UserID, notif.Type, notif.Title, notif.Body, notif.Payload).
		Scan(&notif.ID, &notif.CreatedAt)
	if err != nil {
		return fmt.Errorf("create notification: %w", err)
	}
	return nil
}

func (r *postgresRepository) ListByUserID(ctx context.Context, userID string, pager pagination.OffsetPager) (pagination.Page[domain.Notification], error) {
	countQuery := `SELECT COUNT(*) FROM notifications WHERE user_id = $1`
	var total int64
	if err := r.db.QueryRow(ctx, countQuery, userID).Scan(&total); err != nil {
		return pagination.Page[domain.Notification]{}, fmt.Errorf("count notifications: %w", err)
	}

	query := `
		SELECT id, user_id, type, title, body, payload, read_at, created_at
		FROM notifications
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.db.Query(ctx, query, userID, pager.Limit(), pager.Offset())
	if err != nil {
		return pagination.Page[domain.Notification]{}, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	items := make([]domain.Notification, 0, pager.Limit())
	for rows.Next() {
		var n domain.Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.Type, &n.Title, &n.Body, &n.Payload, &n.ReadAt, &n.CreatedAt); err != nil {
			return pagination.Page[domain.Notification]{}, fmt.Errorf("scan notification: %w", err)
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return pagination.Page[domain.Notification]{}, fmt.Errorf("iterate notifications: %w", err)
	}

	return pagination.NewPage(items, total, pager), nil
}

func (r *postgresRepository) CountUnread(ctx context.Context, userID string) (int64, error) {
	query := `SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`
	var count int64
	if err := r.db.QueryRow(ctx, query, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return count, nil
}

func (r *postgresRepository) MarkAsRead(ctx context.Context, userID, notificationID string) error {
	query := `
		UPDATE notifications
		SET read_at = now()
		WHERE id = $1 AND user_id = $2 AND read_at IS NULL
	`
	res, err := r.db.Exec(ctx, query, notificationID, userID)
	if err != nil {
		return fmt.Errorf("mark notification as read: %w", err)
	}
	if res.RowsAffected() == 0 {
		return errors.New("notification not found or already read")
	}
	return nil
}

func (r *postgresRepository) MarkAllAsRead(ctx context.Context, userID string) error {
	query := `
		UPDATE notifications
		SET read_at = now()
		WHERE user_id = $1 AND read_at IS NULL
	`
	_, err := r.db.Exec(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("mark all notifications as read: %w", err)
	}
	return nil
}

func (r *postgresRepository) GetActiveFCMTokenByUserID(ctx context.Context, userID string) (string, error) {
	query := `
		SELECT fcm_token
		FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now() AND fcm_token IS NOT NULL AND fcm_token <> ''
		ORDER BY issued_at DESC
		LIMIT 1
	`
	var token string
	err := r.db.QueryRow(ctx, query, userID).Scan(&token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("get active FCM token: %w", err)
	}
	return token, nil
}

func (r *postgresRepository) UpdateSessionFCMToken(ctx context.Context, sessionID, fcmToken string) error {
	query := `
		UPDATE sessions
		SET fcm_token = $2
		WHERE id = $1 AND revoked_at IS NULL
	`
	res, err := r.db.Exec(ctx, query, sessionID, fcmToken)
	if err != nil {
		return fmt.Errorf("update session fcm token: %w", err)
	}
	if res.RowsAffected() == 0 {
		return errors.New("session not found or revoked")
	}
	return nil
}

func (r *postgresRepository) ClearFCMToken(ctx context.Context, fcmToken string) error {
	query := `UPDATE sessions SET fcm_token = NULL WHERE fcm_token = $1`
	_, err := r.db.Exec(ctx, query, fcmToken)
	if err != nil {
		return fmt.Errorf("clear fcm token: %w", err)
	}
	return nil
}
