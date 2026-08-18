package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/modules/auth/domain"
	"paysplit-backend/internal/modules/auth/repository"
)

func TestSessionReplacementRotationAndReplay(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const email = "auth.repository.test@example.invalid"
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email=$1`, email)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email=$1`, email) })
	rawVerify, verifyHash, err := domain.NewOTP()
	if err != nil {
		t.Fatal(err)
	}
	repo := New(pool)
	user, err := repo.CreateUser(ctx, repository.CreateUserParams{Email: email, PhoneNumber: "+84987654321", DisplayName: "Repository Test", PasswordHash: "stored-hash", VerificationTokenHash: verifyHash, VerificationExpiresAt: time.Now().Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	user, err = repo.VerifyEmail(ctx, email, domain.HashToken(rawVerify), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if user.Status != domain.StatusActive {
		t.Fatalf("unexpected status %s", user.Status)
	}
	_, refreshOne, err := domain.NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_, sessionOne, err := repo.CreateSession(ctx, repository.CreateSessionParams{UserID: user.ID, ExpectedPasswordHash: user.PasswordHash, DeviceID: "018f0000-0000-7000-8000-000000000001", DeviceName: "first", RefreshTokenHash: refreshOne, Now: now, ExpiresAt: now.Add(7 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	_, refreshTwo, err := domain.NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	_, sessionTwo, err := repo.CreateSession(ctx, repository.CreateSessionParams{UserID: user.ID, ExpectedPasswordHash: user.PasswordHash, DeviceID: "018f0000-0000-7000-8000-000000000002", DeviceName: "second", RefreshTokenHash: refreshTwo, Now: now.Add(time.Second), ExpiresAt: now.Add(7 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ValidateSession(ctx, user.ID, sessionOne.ID, time.Now()); !errors.Is(err, domain.ErrSessionRevoked) {
		t.Fatalf("first session remains live: %v", err)
	}
	if _, err = repo.ValidateSession(ctx, user.ID, sessionTwo.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, replacementHash, err := domain.NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.RotateRefresh(ctx, refreshTwo, replacementHash, sessionTwo.DeviceID, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, anotherHash, err := domain.NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.RotateRefresh(ctx, refreshTwo, anotherHash, sessionTwo.DeviceID, time.Now()); !errors.Is(err, domain.ErrSessionRevoked) {
		t.Fatalf("replay did not revoke session: %v", err)
	}
	if _, err = repo.ValidateSession(ctx, user.ID, sessionTwo.ID, time.Now()); !errors.Is(err, domain.ErrSessionRevoked) {
		t.Fatalf("replayed session remains live: %v", err)
	}
}

func TestOTPMaxAttemptsAndResetPassword(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	const email = "otp.attempt.test@example.invalid"
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email=$1`, email)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email=$1`, email) })

	correctOTP, verifyHash, err := domain.NewOTP()
	if err != nil {
		t.Fatal(err)
	}
	repo := New(pool)
	user, err := repo.CreateUser(ctx, repository.CreateUserParams{
		Email:                 email,
		PhoneNumber:           "+84987654322",
		DisplayName:           "OTP Test",
		PasswordHash:          "old-password-hash",
		VerificationTokenHash: verifyHash,
		VerificationExpiresAt: time.Now().Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	wrongOTP := "000000"
	if correctOTP == wrongOTP {
		wrongOTP = "111111"
	}

	// 4 failed attempts
	for i := 1; i <= 4; i++ {
		_, err = repo.VerifyEmail(ctx, email, domain.HashToken(wrongOTP), time.Now())
		if !errors.Is(err, domain.ErrInvalidOrExpiredToken) {
			t.Fatalf("attempt %d: expected ErrInvalidOrExpiredToken, got %v", i, err)
		}
	}

	// 5th failed attempt should invalidate the OTP
	_, err = repo.VerifyEmail(ctx, email, domain.HashToken(wrongOTP), time.Now())
	if !errors.Is(err, domain.ErrInvalidOrExpiredToken) {
		t.Fatalf("5th attempt: expected ErrInvalidOrExpiredToken, got %v", err)
	}

	// 6th attempt with correct OTP should now fail because OTP was invalidated
	_, err = repo.VerifyEmail(ctx, email, domain.HashToken(correctOTP), time.Now())
	if !errors.Is(err, domain.ErrInvalidOrExpiredToken) {
		t.Fatalf("correct OTP after max attempts: expected ErrInvalidOrExpiredToken, got %v", err)
	}

	// Issue a new verification token
	newOTP, newHash, err := domain.NewOTP()
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateUserToken(ctx, user.ID, domain.TokenEmailVerification, newHash, time.Now().Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Verify with new correct OTP succeeds
	verifiedUser, err := repo.VerifyEmail(ctx, email, domain.HashToken(newOTP), time.Now())
	if err != nil {
		t.Fatalf("failed to verify email with new OTP: %v", err)
	}
	if verifiedUser.Status != domain.StatusActive {
		t.Fatalf("expected status active, got %s", verifiedUser.Status)
	}

	// Regression: Calling VerifyEmail on an active user with a wrong OTP MUST fail
	_, err = repo.VerifyEmail(ctx, email, domain.HashToken("999999"), time.Now())
	if !errors.Is(err, domain.ErrInvalidOrExpiredToken) {
		t.Fatalf("expected ErrInvalidOrExpiredToken for wrong OTP on active user, got %v", err)
	}

	// Regression: Calling VerifyEmail on an active user with the valid consumed OTP MUST succeed idempotently
	reverifiedUser, err := repo.VerifyEmail(ctx, email, domain.HashToken(newOTP), time.Now())
	if err != nil {
		t.Fatalf("expected idempotent success for consumed OTP on active user, got %v", err)
	}
	if reverifiedUser.Status != domain.StatusActive {
		t.Fatalf("expected status active on reverify, got %s", reverifiedUser.Status)
	}

	// Regression: Non-existent user ID in CreateUserToken returns ErrUserNotFound
	if err = repo.CreateUserToken(ctx, "018f0000-0000-7000-8000-999999999999", domain.TokenEmailVerification, newHash, time.Now().Add(10*time.Minute)); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound for missing user in CreateUserToken, got %v", err)
	}

	// Password reset flow
	resetOTP, resetHash, err := domain.NewOTP()
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateUserToken(ctx, user.ID, domain.TokenPasswordReset, resetHash, time.Now().Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Reset with correct OTP
	if err = repo.ResetPassword(ctx, email, domain.HashToken(resetOTP), "new-password-hash", time.Now()); err != nil {
		t.Fatalf("failed to reset password: %v", err)
	}

	// Reusing same OTP must fail
	if err = repo.ResetPassword(ctx, email, domain.HashToken(resetOTP), "another-hash", time.Now()); !errors.Is(err, domain.ErrInvalidOrExpiredToken) {
		t.Fatalf("expected ErrInvalidOrExpiredToken on reused reset OTP, got %v", err)
	}
}
