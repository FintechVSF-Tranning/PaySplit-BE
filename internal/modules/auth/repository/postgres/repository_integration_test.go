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
	rawVerify, verifyHash, err := domain.NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	repo := New(pool)
	user, err := repo.CreateUser(ctx, repository.CreateUserParams{Email: email, PhoneNumber: "+84987654321", DisplayName: "Repository Test", PasswordHash: "stored-hash", VerificationTokenHash: verifyHash, VerificationExpiresAt: time.Now().Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	user, err = repo.VerifyEmail(ctx, domain.HashToken(rawVerify), time.Now())
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
