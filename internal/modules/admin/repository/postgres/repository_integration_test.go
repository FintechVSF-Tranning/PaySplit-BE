package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/modules/admin/domain"
	"paysplit-backend/internal/modules/admin/repository"
)

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// cleanupAuditLogsFor deletes admin_audit_logs rows involving the given user IDs. Registered
// AFTER the user fixtures so t.Cleanup's LIFO order runs it BEFORE the user deletes: users(id)
// has no ON DELETE CASCADE from admin_audit_logs, so deleting a user first would fail silently.
func cleanupAuditLogsFor(t *testing.T, pool *pgxpool.Pool, userIDs ...string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_audit_logs WHERE admin_id = ANY($1) OR target_user_id = ANY($1)`, userIDs)
	})
}

// seedTestUser inserts a user directly (bypassing the auth module) so admin repository
// tests can set up fixtures without depending on another module.
func seedTestUser(t *testing.T, pool *pgxpool.Pool, email, phone, role, status string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, display_name, phone_number, role, status, email_verified_at)
		VALUES ($1, 'test-hash', 'Repository Test', $2, $3, $4, now())
		RETURNING id`, email, phone, role, status).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id)
	})
	return id
}

// seedTestSession inserts a live (non revoked) session and refresh token for userID, the
// fixture UpdateAccountStatusWithRevocation is expected to revoke on suspend/lock.
func seedTestSession(t *testing.T, pool *pgxpool.Pool, userID string) (sessionID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	err := pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, device_id, device_name, issued_at, expires_at)
		VALUES ($1, gen_random_uuid(), 'repository test device', $2, $3)
		RETURNING id`, userID, now, now.Add(7*24*time.Hour)).Scan(&sessionID)
	if err != nil {
		t.Fatal(err)
	}
	tokenHash := make([]byte, 32)
	if _, err := pool.Exec(ctx, `
		INSERT INTO session_refresh_tokens (session_id, token_hash, issued_at, expires_at)
		VALUES ($1, $2, $3, $4)`, sessionID, tokenHash, now, now.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	return sessionID
}

// TestUpdateAccountStatusWithRevocation_SuspendRevokesSessionsAndLogsAudit covers AC-4: transitioning
// an account to suspended must update users.status, revoke all active sessions and refresh tokens,
// and record the mutation in admin_audit_logs, all within one transaction.
func TestUpdateAccountStatusWithRevocation_SuspendRevokesSessionsAndLogsAudit(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	adminID := seedTestUser(t, pool, "admin.repo.test@example.invalid", "+84900000001", "admin", "active")
	targetID := seedTestUser(t, pool, "target.repo.test@example.invalid", "+84900000002", "user", "active")
	cleanupAuditLogsFor(t, pool, adminID, targetID)
	sessionID := seedTestSession(t, pool, targetID)

	repo := New(pool)
	safeUser, warning, err := repo.UpdateAccountStatusWithRevocation(ctx, repository.UpdateStatusInput{
		TargetUserID: targetID,
		AdminID:      adminID,
		NewStatus:    "suspended",
		Reason:       "Policy violation",
	})
	if err != nil {
		t.Fatalf("UpdateAccountStatusWithRevocation returned error: %v", err)
	}
	if safeUser.Status != "suspended" {
		t.Fatalf("expected status suspended, got %q", safeUser.Status)
	}
	if warning == nil {
		t.Fatal("expected a non nil warning summary")
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, targetID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "suspended" {
		t.Fatalf("users.status not persisted: got %q", status)
	}

	var revokedAt *time.Time
	var revokedReason *string
	if err := pool.QueryRow(ctx, `SELECT revoked_at, revoked_reason FROM sessions WHERE id=$1`, sessionID).Scan(&revokedAt, &revokedReason); err != nil {
		t.Fatal(err)
	}
	if revokedAt == nil {
		t.Fatal("expected session to be revoked, revoked_at is null")
	}
	if revokedReason == nil || *revokedReason != "admin_suspended" {
		t.Fatalf("expected revoked_reason admin_suspended, got %v", revokedReason)
	}

	var refreshRevokedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT revoked_at FROM session_refresh_tokens WHERE session_id=$1`, sessionID).Scan(&refreshRevokedAt); err != nil {
		t.Fatal(err)
	}
	if refreshRevokedAt == nil {
		t.Fatal("expected refresh token to be revoked, revoked_at is null")
	}

	var action, reason string
	if err := pool.QueryRow(ctx, `SELECT action, reason FROM admin_audit_logs WHERE target_user_id=$1 AND admin_id=$2`, targetID, adminID).Scan(&action, &reason); err != nil {
		t.Fatal(err)
	}
	if action != "suspend" {
		t.Fatalf("expected audit action 'suspend' (matching the admin_action enum), got %q", action)
	}
	if reason != "Policy violation" {
		t.Fatalf("expected audit reason to be preserved, got %q", reason)
	}
}

// TestUpdateAccountStatusWithRevocation_LockAndReactivateMapToValidEnumActions covers AC-3/AC-4 for the
// remaining two transitions, guarding against the admin_action enum ('suspend','lock','reactivate')
// getting out of sync with the users.status values ('suspended','locked','active') again.
func TestUpdateAccountStatusWithRevocation_LockAndReactivateMapToValidEnumActions(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	adminID := seedTestUser(t, pool, "admin.repo.test2@example.invalid", "+84900000003", "admin", "active")
	targetID := seedTestUser(t, pool, "target.repo.test2@example.invalid", "+84900000004", "user", "active")
	cleanupAuditLogsFor(t, pool, adminID, targetID)

	repo := New(pool)

	if _, _, err := repo.UpdateAccountStatusWithRevocation(ctx, repository.UpdateStatusInput{
		TargetUserID: targetID, AdminID: adminID, NewStatus: "locked", Reason: "policy",
	}); err != nil {
		t.Fatalf("lock transition failed: %v", err)
	}
	var lockAction string
	if err := pool.QueryRow(ctx, `SELECT action FROM admin_audit_logs WHERE target_user_id=$1 ORDER BY created_at DESC LIMIT 1`, targetID).Scan(&lockAction); err != nil {
		t.Fatal(err)
	}
	if lockAction != "lock" {
		t.Fatalf("expected audit action 'lock', got %q", lockAction)
	}

	if _, _, err := repo.UpdateAccountStatusWithRevocation(ctx, repository.UpdateStatusInput{
		TargetUserID: targetID, AdminID: adminID, NewStatus: "active", Reason: "restored",
	}); err != nil {
		t.Fatalf("reactivate transition failed: %v", err)
	}
	var reactivateAction string
	if err := pool.QueryRow(ctx, `SELECT action FROM admin_audit_logs WHERE target_user_id=$1 ORDER BY created_at DESC LIMIT 1`, targetID).Scan(&reactivateAction); err != nil {
		t.Fatal(err)
	}
	if reactivateAction != "reactivate" {
		t.Fatalf("expected audit action 'reactivate', got %q", reactivateAction)
	}
}

// TestUpdateAccountStatusWithRevocation_SelfAndAdminProtection covers AC-3: an admin cannot modify
// their own status, and cannot suspend or lock another admin.
func TestUpdateAccountStatusWithRevocation_SelfAndAdminProtection(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()

	adminID := seedTestUser(t, pool, "admin.repo.test3@example.invalid", "+84900000005", "admin", "active")
	otherAdminID := seedTestUser(t, pool, "admin.repo.test4@example.invalid", "+84900000006", "admin", "active")

	repo := New(pool)

	if _, _, err := repo.UpdateAccountStatusWithRevocation(ctx, repository.UpdateStatusInput{
		TargetUserID: adminID, AdminID: adminID, NewStatus: "locked", Reason: "test",
	}); !errors.Is(err, domain.ErrCannotModifySelf) {
		t.Fatalf("expected ErrCannotModifySelf, got %v", err)
	}

	if _, _, err := repo.UpdateAccountStatusWithRevocation(ctx, repository.UpdateStatusInput{
		TargetUserID: otherAdminID, AdminID: adminID, NewStatus: "suspended", Reason: "test",
	}); !errors.Is(err, domain.ErrCannotModifyAdmin) {
		t.Fatalf("expected ErrCannotModifyAdmin, got %v", err)
	}
}

// TestGetSystemOverview_MediaCleanupCountsOnlyPendingJobs covers AC-7: the "media cleanup queue
// depth" (DTO field pending_jobs_count) must reflect the actual backlog, not every job ever
// inserted. A prior bug counted completed jobs too, since GetSystemMediaCleanupOverview had no
// WHERE completed_at IS NULL filter; the number only ever grew. media_cleanup_jobs is a shared,
// unscoped table, so this measures the delta the fixture introduces rather than an absolute count.
func TestGetSystemOverview_MediaCleanupCountsOnlyPendingJobs(t *testing.T) {
	pool := openTestPool(t)
	ctx := context.Background()
	repo := New(pool)

	before, err := repo.GetSystemOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const completedKey = "repository.test.media.cleanup.completed"
	const pendingKey = "repository.test.media.cleanup.pending"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_cleanup_jobs WHERE object_key IN ($1, $2)`, completedKey, pendingKey)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO media_cleanup_jobs (provider, object_key, completed_at) VALUES ('cloudinary', $1, now())`, completedKey); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_cleanup_jobs (provider, object_key, completed_at) VALUES ('cloudinary', $1, NULL)`, pendingKey); err != nil {
		t.Fatal(err)
	}

	after, err := repo.GetSystemOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}

	delta := after.MediaCleanup.PendingJobsCount - before.MediaCleanup.PendingJobsCount
	if delta != 1 {
		t.Fatalf("expected pending_jobs_count to increase by 1 (only the still-pending job), got delta %d (before=%d, after=%d)",
			delta, before.MediaCleanup.PendingJobsCount, after.MediaCleanup.PendingJobsCount)
	}
}
