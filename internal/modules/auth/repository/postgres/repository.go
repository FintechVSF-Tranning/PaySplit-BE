package postgres

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/modules/auth/domain"
	"paysplit-backend/internal/modules/auth/repository"
	dbgen "paysplit-backend/internal/modules/auth/repository/postgres/sqlc"
)

type postgresRepository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) repository.Repository {
	if pool == nil {
		panic("auth repository pool must not be nil")
	}
	return &postgresRepository{pool: pool}
}

func (r *postgresRepository) CreateUser(ctx context.Context, p repository.CreateUserParams) (*domain.User, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin create user: %w", err)
	}
	defer tx.Rollback(ctx)

	row, err := dbgen.New(tx).CreateUser(ctx, dbgen.CreateUserParams{
		Email: p.Email, PhoneNumber: p.PhoneNumber, DisplayName: p.DisplayName, PasswordHash: p.PasswordHash,
	})
	if err != nil {
		return nil, mapWriteError(err)
	}
	user := mapGeneratedUser(row)
	if _, err = tx.Exec(ctx, `INSERT INTO user_tokens (user_id,type,token_hash,expires_at) VALUES ($1,$2,$3,$4)`, user.ID, domain.TokenEmailVerification, p.VerificationTokenHash, p.VerificationExpiresAt); err != nil {
		return nil, fmt.Errorf("insert verification token: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create user: %w", err)
	}
	return user, nil
}

func (r *postgresRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	u, err := dbgen.New(r.pool).GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return mapGeneratedUser(u), nil
}

func (r *postgresRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return nil, domain.ErrUserNotFound
	}
	u, err := dbgen.New(r.pool).GetUserByID(ctx, pgUUID(parsed))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return mapGeneratedUser(u), nil
}

func (r *postgresRepository) CreateUserToken(ctx context.Context, userID, kind string, hash []byte, expires time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var existingID string
	if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&existingID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrUserNotFound
		}
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE user_tokens SET superseded_at=now() WHERE user_id=$1 AND type=$2 AND used_at IS NULL AND superseded_at IS NULL`, userID, kind); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_tokens (user_id,type,token_hash,expires_at,attempt_count) VALUES ($1,$2,$3,$4,0)`, userID, kind, hash, expires); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *postgresRepository) VerifyEmail(ctx context.Context, email string, otpHash []byte, now time.Time) (*domain.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	user, err := scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email=$1 FOR UPDATE`, email))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInvalidOrExpiredToken
	}
	if err != nil {
		return nil, err
	}

	if user.Status == domain.StatusActive {
		var usedHash []byte
		err = tx.QueryRow(ctx, `SELECT token_hash FROM user_tokens WHERE user_id=$1 AND type=$2 AND used_at IS NOT NULL ORDER BY used_at DESC LIMIT 1`, user.ID, domain.TokenEmailVerification).Scan(&usedHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrInvalidOrExpiredToken
		}
		if err != nil {
			return nil, err
		}
		if subtle.ConstantTimeCompare(usedHash, otpHash) != 1 {
			return nil, domain.ErrInvalidOrExpiredToken
		}
		return user, tx.Commit(ctx)
	}
	if user.Status != domain.StatusPendingVerification {
		return nil, domain.ErrAccountUnavailable
	}

	var tokenID string
	var storedHash []byte
	var attemptCount int
	var expires time.Time
	var usedAt, supersededAt *time.Time
	err = tx.QueryRow(ctx, `SELECT id,token_hash,attempt_count,expires_at,used_at,superseded_at FROM user_tokens WHERE user_id=$1 AND type=$2 AND used_at IS NULL AND superseded_at IS NULL FOR UPDATE`, user.ID, domain.TokenEmailVerification).Scan(&tokenID, &storedHash, &attemptCount, &expires, &usedAt, &supersededAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInvalidOrExpiredToken
	}
	if err != nil {
		return nil, err
	}

	if usedAt != nil || supersededAt != nil || !now.Before(expires) {
		return nil, domain.ErrInvalidOrExpiredToken
	}
	if attemptCount >= 5 {
		_, _ = tx.Exec(ctx, `UPDATE user_tokens SET superseded_at=$2 WHERE id=$1`, tokenID, now)
		_ = tx.Commit(ctx)
		return nil, domain.ErrInvalidOrExpiredToken
	}

	if subtle.ConstantTimeCompare(storedHash, otpHash) != 1 {
		attemptCount++
		if attemptCount >= 5 {
			_, _ = tx.Exec(ctx, `UPDATE user_tokens SET attempt_count=$2,superseded_at=$3 WHERE id=$1`, tokenID, attemptCount, now)
		} else {
			_, _ = tx.Exec(ctx, `UPDATE user_tokens SET attempt_count=$2 WHERE id=$1`, tokenID, attemptCount)
		}
		_ = tx.Commit(ctx)
		return nil, domain.ErrInvalidOrExpiredToken
	}

	if _, err = tx.Exec(ctx, `UPDATE users SET status='active',email_verified_at=COALESCE(email_verified_at,$2) WHERE id=$1`, user.ID, now); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE user_tokens SET used_at=$2 WHERE id=$1`, tokenID, now); err != nil {
		return nil, err
	}
	user.Status = domain.StatusActive
	if user.EmailVerifiedAt == nil {
		user.EmailVerifiedAt = &now
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return user, nil
}

func (r *postgresRepository) RecordLoginFailure(ctx context.Context, email string, now time.Time) (time.Duration, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var count int
	var window, blocked *time.Time
	err = tx.QueryRow(ctx, `SELECT failed_login_count,failed_login_window_started_at,login_blocked_until FROM users WHERE email=$1 FOR UPDATE`, email).Scan(&count, &window, &blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, tx.Commit(ctx)
	}
	if err != nil {
		return 0, err
	}
	if blocked != nil && now.Before(*blocked) {
		return blocked.Sub(now), tx.Commit(ctx)
	}
	if window == nil || now.Sub(*window) >= 15*time.Minute {
		count, window = 1, &now
	} else {
		count++
	}
	var blockUntil *time.Time
	if count >= 5 {
		v := now.Add(15 * time.Minute)
		blockUntil = &v
	}
	_, err = tx.Exec(ctx, `UPDATE users SET failed_login_count=$2,failed_login_window_started_at=$3,login_blocked_until=$4 WHERE email=$1`, email, count, window, blockUntil)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	if blockUntil != nil {
		return blockUntil.Sub(now), nil
	}
	return 0, nil
}

func (r *postgresRepository) CreateSession(ctx context.Context, p repository.CreateSessionParams) (*domain.User, *domain.Session, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	user, err := getUserForUpdate(ctx, tx, p.UserID)
	if err != nil {
		return nil, nil, err
	}
	if user.PasswordHash != p.ExpectedPasswordHash {
		return nil, nil, domain.ErrInvalidCredentials
	}
	if user.Status == domain.StatusPendingVerification {
		return nil, nil, domain.ErrEmailNotVerified
	}
	if user.Status != domain.StatusActive {
		return nil, nil, domain.ErrAccountUnavailable
	}
	if user.LoginBlockedUntil != nil && p.Now.Before(*user.LoginBlockedUntil) {
		return nil, nil, &domain.RateLimitError{RetryAfter: user.LoginBlockedUntil.Sub(p.Now)}
	}
	if _, err = tx.Exec(ctx, `UPDATE sessions SET revoked_at=$2,revoked_reason='replaced_by_sign_in' WHERE user_id=$1 AND revoked_at IS NULL`, p.UserID, p.Now); err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE session_refresh_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE session_id IN (SELECT id FROM sessions WHERE user_id=$1 AND revoked_at=$2)`, p.UserID, p.Now); err != nil {
		return nil, nil, err
	}
	var session domain.Session
	err = tx.QueryRow(ctx, `INSERT INTO sessions (user_id,device_id,device_name,fcm_token,issued_at,expires_at) VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),$5,$6) RETURNING id,user_id,device_id,fcm_token,expires_at`, p.UserID, p.DeviceID, p.DeviceName, p.FCMToken, p.Now, p.ExpiresAt).Scan(&session.ID, &session.UserID, &session.DeviceID, &session.FCMToken, &session.ExpiresAt)
	if err != nil {
		return nil, nil, mapWriteError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO session_refresh_tokens (session_id,token_hash,issued_at,expires_at) VALUES ($1,$2,$3,$4)`, session.ID, p.RefreshTokenHash, p.Now, p.ExpiresAt); err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET failed_login_count=0,failed_login_window_started_at=NULL,login_blocked_until=NULL WHERE id=$1`, p.UserID); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return user, &session, nil
}

func (r *postgresRepository) RotateRefresh(ctx context.Context, oldHash, newHash []byte, deviceID string, now time.Time) (*repository.RotateRefreshResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var tokenID, sessionID, storedDevice, userID string
	if err = tx.QueryRow(ctx, `SELECT t.session_id,s.user_id FROM session_refresh_tokens t JOIN sessions s ON s.id=t.session_id WHERE t.token_hash=$1`, oldHash).Scan(&sessionID, &userID); errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInvalidOrExpiredToken
	}
	if err != nil {
		return nil, err
	}
	user, err := getUserForUpdate(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	var tokenExpires, sessionExpires time.Time
	var sessionRevoked *time.Time
	err = tx.QueryRow(ctx, `SELECT device_id,expires_at,revoked_at FROM sessions WHERE id=$1 AND user_id=$2 FOR UPDATE`, sessionID, userID).Scan(&storedDevice, &sessionExpires, &sessionRevoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInvalidOrExpiredToken
	}
	if err != nil {
		return nil, err
	}
	var usedAt, tokenRevoked *time.Time
	err = tx.QueryRow(ctx, `SELECT id,expires_at,used_at,revoked_at FROM session_refresh_tokens WHERE token_hash=$1 AND session_id=$2 FOR UPDATE`, oldHash, sessionID).Scan(&tokenID, &tokenExpires, &usedAt, &tokenRevoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInvalidOrExpiredToken
	}
	if err != nil {
		return nil, err
	}
	if usedAt != nil {
		_, _ = tx.Exec(ctx, `UPDATE sessions SET revoked_at=COALESCE(revoked_at,$2),revoked_reason=COALESCE(revoked_reason,'refresh_reuse') WHERE id=$1`, sessionID, now)
		_, _ = tx.Exec(ctx, `UPDATE session_refresh_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE session_id=$1`, sessionID, now)
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, domain.ErrSessionRevoked
	}
	if tokenRevoked != nil || sessionRevoked != nil || !now.Before(tokenExpires) || !now.Before(sessionExpires) {
		return nil, domain.ErrInvalidOrExpiredToken
	}
	if storedDevice != deviceID {
		return nil, domain.ErrInvalidOrExpiredToken
	}
	if user.Status != domain.StatusActive {
		return nil, domain.ErrAccountUnavailable
	}
	if _, err = tx.Exec(ctx, `UPDATE session_refresh_tokens SET used_at=$2 WHERE id=$1`, tokenID, now); err != nil {
		return nil, err
	}
	newExpiry := now.Add(7 * 24 * time.Hour)
	if sessionExpires.Before(newExpiry) {
		newExpiry = sessionExpires
	}
	if !newExpiry.After(now) {
		return nil, domain.ErrInvalidOrExpiredToken
	}
	if _, err = tx.Exec(ctx, `INSERT INTO session_refresh_tokens (session_id,token_hash,issued_at,expires_at) VALUES ($1,$2,$3,$4)`, sessionID, newHash, now, newExpiry); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &repository.RotateRefreshResult{User: user, SessionID: sessionID, ExpiresAt: newExpiry}, nil
}

func (r *postgresRepository) ValidateSession(ctx context.Context, userID, sessionID string, now time.Time) (*domain.SessionIdentity, error) {
	var out domain.SessionIdentity
	err := r.pool.QueryRow(ctx, `SELECT u.id,u.role,s.id FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.id=$1 AND s.user_id=$2 AND s.revoked_at IS NULL AND s.expires_at>$3 AND u.status='active'`, sessionID, userID, now).Scan(&out.UserID, &out.Role, &out.SessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrSessionRevoked
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *postgresRepository) RevokeSession(ctx context.Context, userID, sessionID, reason string, now time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var existingID string
	if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&existingID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrUserNotFound
		}
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE sessions SET revoked_at=COALESCE(revoked_at,$3),revoked_reason=COALESCE(revoked_reason,$4) WHERE id=$1 AND user_id=$2`, sessionID, userID, now, reason)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE session_refresh_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE session_id=$1`, sessionID, now)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *postgresRepository) ResetPassword(ctx context.Context, email string, otpHash []byte, newHash string, now time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var userID, status string
	err = tx.QueryRow(ctx, `SELECT id,status FROM users WHERE email=$1 FOR UPDATE`, email).Scan(&userID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrInvalidOrExpiredToken
	}
	if err != nil {
		return err
	}
	if status != domain.StatusActive {
		return domain.ErrInvalidOrExpiredToken
	}

	var tokenID string
	var storedHash []byte
	var attemptCount int
	var expires time.Time
	var used, superseded *time.Time
	err = tx.QueryRow(ctx, `SELECT id,token_hash,attempt_count,expires_at,used_at,superseded_at FROM user_tokens WHERE user_id=$1 AND type=$2 AND used_at IS NULL AND superseded_at IS NULL FOR UPDATE`, userID, domain.TokenPasswordReset).Scan(&tokenID, &storedHash, &attemptCount, &expires, &used, &superseded)
	if errors.Is(err, pgx.ErrNoRows) || used != nil || superseded != nil || !now.Before(expires) {
		return domain.ErrInvalidOrExpiredToken
	}
	if err != nil {
		return err
	}
	if attemptCount >= 5 {
		_, _ = tx.Exec(ctx, `UPDATE user_tokens SET superseded_at=$2 WHERE id=$1`, tokenID, now)
		_ = tx.Commit(ctx)
		return domain.ErrInvalidOrExpiredToken
	}

	if subtle.ConstantTimeCompare(storedHash, otpHash) != 1 {
		attemptCount++
		if attemptCount >= 5 {
			_, _ = tx.Exec(ctx, `UPDATE user_tokens SET attempt_count=$2,superseded_at=$3 WHERE id=$1`, tokenID, attemptCount, now)
		} else {
			_, _ = tx.Exec(ctx, `UPDATE user_tokens SET attempt_count=$2 WHERE id=$1`, tokenID, attemptCount)
		}
		_ = tx.Commit(ctx)
		return domain.ErrInvalidOrExpiredToken
	}

	if _, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, userID, newHash); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE user_tokens SET used_at=$2 WHERE id=$1`, tokenID, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sessions SET revoked_at=COALESCE(revoked_at,$2),revoked_reason=COALESCE(revoked_reason,'password_reset') WHERE user_id=$1`, userID, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE session_refresh_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE session_id IN (SELECT id FROM sessions WHERE user_id=$1)`, userID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *postgresRepository) ChangePassword(ctx context.Context, userID, sessionID, newHash string, now time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var existingID string
	if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&existingID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrUserNotFound
		}
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, userID, newHash); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sessions SET revoked_at=COALESCE(revoked_at,$3),revoked_reason=COALESCE(revoked_reason,'password_changed') WHERE user_id=$1 AND id<>$2`, userID, sessionID, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE session_refresh_tokens SET revoked_at=COALESCE(revoked_at,$3) WHERE session_id IN (SELECT id FROM sessions WHERE user_id=$1 AND id<>$2)`, userID, sessionID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *postgresRepository) UpdateProfile(ctx context.Context, userID string, p repository.ProfilePatch) (*domain.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	u, err := getUserForUpdate(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if p.DisplayName != nil {
		u.DisplayName = *p.DisplayName
	}
	if p.PhoneNumber != nil {
		u.PhoneNumber = *p.PhoneNumber
	}
	if p.Bank != nil {
		u.BankCode = p.Bank.Code
		u.BankAccountNumber = p.Bank.AccountNumber
		u.BankAccountHolder = p.Bank.AccountHolder
	}
	_, err = tx.Exec(ctx, `UPDATE users SET display_name=$2,phone_number=$3,default_bank_code=$4,default_bank_account_number=$5,default_bank_account_holder=$6 WHERE id=$1`, userID, u.DisplayName, u.PhoneNumber, u.BankCode, u.BankAccountNumber, u.BankAccountHolder)
	if err != nil {
		return nil, mapWriteError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetByID(ctx, userID)
}

func (r *postgresRepository) SetAvatar(ctx context.Context, userID, key string) (*domain.User, *string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	var old *string
	if err = tx.QueryRow(ctx, `SELECT avatar_object_key FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&old); err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET avatar_object_key=$2 WHERE id=$1`, userID, key); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	u, err := r.GetByID(ctx, userID)
	return u, old, err
}

func (r *postgresRepository) ClearAvatar(ctx context.Context, userID string) (*string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var old *string
	if err = tx.QueryRow(ctx, `SELECT avatar_object_key FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&old); errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET avatar_object_key=NULL WHERE id=$1`, userID); err != nil {
		return nil, err
	}
	return old, tx.Commit(ctx)
}

func (r *postgresRepository) CheckAndRecordRateLimit(ctx context.Context, action string, keys map[string][]byte, now time.Time) (time.Duration, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	dims := make([]string, 0, len(keys))
	for d := range keys {
		dims = append(dims, d)
	}
	sort.Strings(dims)
	for _, d := range dims {
		lockKey := action + ":" + d + ":" + hex.EncodeToString(keys[d])
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
			return 0, err
		}
	}
	var retry time.Duration
	for _, d := range dims {
		var minuteCount, hourCount int
		var minuteOldest, hourOldest *time.Time
		err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE occurred_at>$4),min(occurred_at) FILTER(WHERE occurred_at>$4),count(*),min(occurred_at) FROM auth_rate_limit_events WHERE action=$1 AND dimension=$2 AND key_hash=$3 AND occurred_at>$5`, action, d, keys[d], now.Add(-time.Minute), now.Add(-time.Hour)).Scan(&minuteCount, &minuteOldest, &hourCount, &hourOldest)
		if err != nil {
			return 0, err
		}
		if action != "sign_up" && minuteCount >= 1 && minuteOldest != nil {
			v := minuteOldest.Add(time.Minute).Sub(now)
			if v > retry {
				retry = v
			}
		}
		if hourCount >= 10 && hourOldest != nil {
			v := hourOldest.Add(time.Hour).Sub(now)
			if v > retry {
				retry = v
			}
		}
	}
	if retry > 0 {
		return retry, &domain.RateLimitError{RetryAfter: retry}
	}
	for _, d := range dims {
		if _, err = tx.Exec(ctx, `INSERT INTO auth_rate_limit_events(action,dimension,key_hash,occurred_at)VALUES($1,$2,$3,$4)`, action, d, keys[d], now); err != nil {
			return 0, err
		}
	}
	return 0, tx.Commit(ctx)
}

func (r *postgresRepository) EnqueueMediaCleanup(ctx context.Context, provider, key string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO media_cleanup_jobs(provider,object_key)VALUES($1,$2) ON CONFLICT(provider,object_key) WHERE completed_at IS NULL DO NOTHING`, provider, key)
	return err
}

func (r *postgresRepository) ClaimMediaCleanup(ctx context.Context, now time.Time, limit int) ([]domain.MediaCleanupJob, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `WITH due AS(SELECT id FROM media_cleanup_jobs WHERE completed_at IS NULL AND attempt_count<10 AND next_attempt_at<=$1 ORDER BY next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT $2) UPDATE media_cleanup_jobs j SET attempt_count=j.attempt_count+1,next_attempt_at=$1+interval '5 minutes' FROM due WHERE j.id=due.id RETURNING j.id,j.object_key,j.attempt_count`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]domain.MediaCleanupJob, 0)
	for rows.Next() {
		var job domain.MediaCleanupJob
		if err = rows.Scan(&job.ID, &job.ObjectKey, &job.AttemptCount); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return jobs, nil
}
func (r *postgresRepository) CompleteMediaCleanup(ctx context.Context, id string, now time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE media_cleanup_jobs SET completed_at=$2,last_error_code=NULL WHERE id=$1`, id, now)
	return err
}
func (r *postgresRepository) FailMediaCleanup(ctx context.Context, id, code string, next time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE media_cleanup_jobs SET last_error_code=$2,next_attempt_at=$3 WHERE id=$1 AND completed_at IS NULL`, id, code, next)
	return err
}

func (r *postgresRepository) CleanupExpiredAuth(ctx context.Context, before time.Time, limit int) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtext('paysplit_auth_cleanup'))`).Scan(&locked); err != nil {
		return 0, err
	}
	if !locked {
		return 0, tx.Commit(ctx)
	}
	var total int64
	tag, err := tx.Exec(ctx, `WITH doomed AS(SELECT id FROM auth_rate_limit_events WHERE occurred_at<$1 ORDER BY occurred_at LIMIT $2) DELETE FROM auth_rate_limit_events a USING doomed d WHERE a.id=d.id`, time.Now().Add(-24*time.Hour), limit)
	if err != nil {
		return 0, err
	}
	total += tag.RowsAffected()
	for _, q := range []string{
		`WITH doomed AS(SELECT id FROM user_tokens WHERE created_at<$1 AND (used_at IS NOT NULL OR superseded_at IS NOT NULL OR expires_at<$1) ORDER BY created_at LIMIT $2) DELETE FROM user_tokens a USING doomed d WHERE a.id=d.id`,
		`WITH doomed AS(SELECT id FROM sessions WHERE COALESCE(revoked_at,expires_at)<$1 ORDER BY COALESCE(revoked_at,expires_at) LIMIT $2) DELETE FROM sessions a USING doomed d WHERE a.id=d.id`,
		`WITH doomed AS(SELECT id FROM media_cleanup_jobs WHERE completed_at<$1 ORDER BY completed_at LIMIT $2) DELETE FROM media_cleanup_jobs a USING doomed d WHERE a.id=d.id`,
	} {
		tag, err := tx.Exec(ctx, q, before, limit)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
	}
	if err = tx.Commit(ctx); err != nil {
		return total, err
	}
	return total, nil
}

const userColumns = `id,email,phone_number,display_name,password_hash,avatar_object_key,default_bank_code,default_bank_account_number,default_bank_account_holder,role,status,email_verified_at,created_at,updated_at,failed_login_count,failed_login_window_started_at,login_blocked_until`

type scanner interface{ Scan(...any) error }

func scanUser(row scanner) (*domain.User, error) {
	u := &domain.User{}
	err := row.Scan(&u.ID, &u.Email, &u.PhoneNumber, &u.DisplayName, &u.PasswordHash, &u.AvatarObjectKey, &u.BankCode, &u.BankAccountNumber, &u.BankAccountHolder, &u.Role, &u.Status, &u.EmailVerifiedAt, &u.CreatedAt, &u.UpdatedAt, &u.FailedLoginCount, &u.FailedLoginWindowStartedAt, &u.LoginBlockedUntil)
	return u, err
}
func getUserForUpdate(ctx context.Context, tx pgx.Tx, id string) (*domain.User, error) {
	u, err := scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrUserNotFound
	}
	return u, err
}

func mapGeneratedUser(u dbgen.User) *domain.User {
	id := uuid.UUID(u.ID.Bytes).String()
	role := fmt.Sprint(u.Role)
	status := fmt.Sprint(u.Status)
	out := &domain.User{ID: id, Email: u.Email, PhoneNumber: u.PhoneNumber, DisplayName: u.DisplayName, PasswordHash: u.PasswordHash, Role: role, Status: status, CreatedAt: u.CreatedAt.Time, UpdatedAt: u.UpdatedAt.Time, FailedLoginCount: int(u.FailedLoginCount)}
	if u.AvatarObjectKey.Valid {
		v := u.AvatarObjectKey.String
		out.AvatarObjectKey = &v
	}
	if u.DefaultBankCode.Valid {
		v := u.DefaultBankCode.String
		out.BankCode = &v
	}
	if u.DefaultBankAccountNumber.Valid {
		v := u.DefaultBankAccountNumber.String
		out.BankAccountNumber = &v
	}
	if u.DefaultBankAccountHolder.Valid {
		v := u.DefaultBankAccountHolder.String
		out.BankAccountHolder = &v
	}
	if u.EmailVerifiedAt.Valid {
		v := u.EmailVerifiedAt.Time
		out.EmailVerifiedAt = &v
	}
	if u.FailedLoginWindowStartedAt.Valid {
		v := u.FailedLoginWindowStartedAt.Time
		out.FailedLoginWindowStartedAt = &v
	}
	if u.LoginBlockedUntil.Valid {
		v := u.LoginBlockedUntil.Time
		out.LoginBlockedUntil = &v
	}
	return out
}

func pgUUID(v uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: v, Valid: true} }

func (r *postgresRepository) UpdateSessionFCMToken(ctx context.Context, sessionID, fcmToken string) error {
	sid, err := uuid.Parse(sessionID)
	if err != nil {
		return domain.ErrSessionRevoked
	}
	affected, err := dbgen.New(r.pool).UpdateSessionFCMToken(ctx, dbgen.UpdateSessionFCMTokenParams{
		ID:       pgUUID(sid),
		FcmToken: pgtype.Text{String: fcmToken, Valid: fcmToken != ""},
	})
	if err != nil {
		return fmt.Errorf("update session fcm token: %w", err)
	}
	if affected == 0 {
		return domain.ErrSessionRevoked
	}
	return nil
}

func mapWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "users_email_key":
			return domain.ErrEmailAlreadyExists
		case "users_phone_number_key":
			return domain.ErrPhoneAlreadyExists
		}
	}
	return fmt.Errorf("write auth data: %w", err)
}
