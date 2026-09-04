package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/modules/auth/domain"
	"paysplit-backend/internal/modules/auth/repository"
	"paysplit-backend/internal/platform/realtime"
)

// listenUserEvents mở một kết nối riêng đang LISTEN `user_events` và trả về hàm
// thu gom mọi control đã tới. Kết nối riêng là bắt buộc: NOTIFY chỉ được giao
// sau khi transaction sinh ra nó commit, nên đây cũng chính là phép thử "cùng
// transaction" mà bài đánh giá yêu cầu.
//
// Payload không giải mã được bị bỏ qua thay vì làm hỏng test: cùng một database
// test có thể đang phục vụ các gói test khác chạy song song.
func listenUserEvents(t *testing.T, ctx context.Context, pool *pgxpool.Pool) func() []realtime.UserEnvelope {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Release)
	if _, err = conn.Exec(ctx, "LISTEN "+realtime.ChannelUserEvents); err != nil {
		t.Fatal(err)
	}
	return func() []realtime.UserEnvelope {
		var out []realtime.UserEnvelope
		for {
			waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			notification, err := conn.Conn().WaitForNotification(waitCtx)
			cancel()
			if err != nil {
				return out
			}
			env, decodeErr := realtime.DecodeUserEnvelope(notification.Payload)
			if decodeErr != nil {
				continue
			}
			out = append(out, env)
		}
	}
}

func seedVerifiedUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repo repository.Repository, email, phone string) *domain.User {
	t.Helper()
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email=$1`, email)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email=$1`, email) })
	rawVerify, verifyHash, err := domain.NewOTP()
	if err != nil {
		t.Fatal(err)
	}
	user, err := repo.CreateUser(ctx, repository.CreateUserParams{
		Email:                 email,
		PhoneNumber:           phone,
		DisplayName:           "Realtime Test",
		PasswordHash:          "stored-hash",
		VerificationTokenHash: verifyHash,
		VerificationExpiresAt: time.Now().Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	user, err = repo.VerifyEmail(ctx, email, domain.HashToken(rawVerify), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return user
}

// Người dùng giữ lại hơn 50 phiên đã thu hồi. Phiên đang sống là UUIDv7 mới
// nhất, tức nằm cuối danh sách đã sắp xếp — đúng phần bị một giới hạn 50 cắt đi.
func TestResetPasswordControlClosesTheLiveSessionBeyondFiftyRetained(t *testing.T) {
	// covers: AC-14
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	// t.Cleanup chứ không phải defer: kết nối đang LISTEN được giải phóng bằng
	// một cleanup đăng ký sau, và cleanup chạy theo thứ tự ngược. Dùng defer thì
	// Close() chờ mãi một kết nối chưa trả về pool.
	t.Cleanup(pool.Close)

	repo := New(pool)
	SetRealtimePublisher(repo, &realtime.Publisher{Enabled: true})
	user := seedVerifiedUser(t, ctx, pool, repo, "realtime.reset.test@example.invalid", "+84987650001")

	// Mỗi lần đăng nhập thu hồi phiên trước, nên 55 lần tạo để lại 54 phiên đã
	// thu hồi cộng đúng một phiên đang sống.
	const logins = 55
	var liveSID uuid.UUID
	base := time.Now().Add(-time.Duration(logins) * time.Second)
	for i := 0; i < logins; i++ {
		_, refreshHash, tokenErr := domain.NewOpaqueToken()
		if tokenErr != nil {
			t.Fatal(tokenErr)
		}
		now := base.Add(time.Duration(i) * time.Second)
		_, session, sessErr := repo.CreateSession(ctx, repository.CreateSessionParams{
			UserID:               user.ID,
			ExpectedPasswordHash: user.PasswordHash,
			DeviceID:             uuid.Must(uuid.NewV7()).String(),
			DeviceName:           "device",
			RefreshTokenHash:     refreshHash,
			Now:                  now,
			ExpiresAt:            now.Add(7 * 24 * time.Hour),
		})
		if sessErr != nil {
			t.Fatal(sessErr)
		}
		liveSID = uuid.MustParse(session.ID)
	}

	collect := listenUserEvents(t, ctx, pool)

	resetOTP, resetHash, err := domain.NewOTP()
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.CreateUserToken(ctx, user.ID, domain.TokenPasswordReset, resetHash, time.Now().Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = repo.ResetPassword(ctx, user.Email, domain.HashToken(resetOTP), "new-password-hash", time.Now()); err != nil {
		t.Fatal(err)
	}

	controls := collect()
	var sawLive, sawSessionEnded bool
	for _, env := range controls {
		if env.Kind != realtime.KindSessionEnded {
			continue
		}
		sawSessionEnded = true
		for _, sid := range env.TargetSIDs {
			if sid == liveSID {
				sawLive = true
			}
		}
	}
	if !sawSessionEnded {
		t.Fatal("password reset published no session.ended control")
	}
	if !sawLive {
		t.Fatal("the live SID was left out of the revocation control; its SSE stream survives the reset")
	}
}

// Một mutation bị rollback không được để lại control nào: NOTIFY phải nằm trong
// đúng transaction của mutation, không phải sau commit.
func TestRolledBackRevocationPublishesNothing(t *testing.T) {
	// covers: AC-14
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	// t.Cleanup chứ không phải defer: kết nối đang LISTEN được giải phóng bằng
	// một cleanup đăng ký sau, và cleanup chạy theo thứ tự ngược. Dùng defer thì
	// Close() chờ mãi một kết nối chưa trả về pool.
	t.Cleanup(pool.Close)

	repo := New(pool)
	SetRealtimePublisher(repo, &realtime.Publisher{Enabled: true})
	collect := listenUserEvents(t, ctx, pool)

	// Người dùng không tồn tại: transaction rollback trước khi commit.
	missing := uuid.Must(uuid.NewV7())
	targetSID := uuid.Must(uuid.NewV7())
	if err = repo.RevokeSession(ctx, missing.String(), targetSID.String(), "logout", time.Now()); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("RevokeSession on a missing user = %v, want ErrUserNotFound", err)
	}

	if targeted(collect(), targetSID) {
		t.Fatal("a rolled back revocation still published a control for its SID")
	}
}

// targeted cho biết một SID cụ thể có xuất hiện trong bất kỳ control nào không.
// Lọc theo SID thay vì đếm tổng: database test dùng chung có thể mang cả lưu
// lượng của những gói test khác.
func targeted(controls []realtime.UserEnvelope, sid uuid.UUID) bool {
	for _, env := range controls {
		for _, target := range env.TargetSIDs {
			if target == sid {
				return true
			}
		}
	}
	return false
}

// Thu hồi hai lần: lần thứ hai không có phiên nào mới bị thu hồi nên không phát
// control rỗng, và không "hồi sinh" một SID đã đóng từ trước.
func TestSecondRevocationPublishesNoStaleSIDs(t *testing.T) {
	// covers: AC-14
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	// t.Cleanup chứ không phải defer: kết nối đang LISTEN được giải phóng bằng
	// một cleanup đăng ký sau, và cleanup chạy theo thứ tự ngược. Dùng defer thì
	// Close() chờ mãi một kết nối chưa trả về pool.
	t.Cleanup(pool.Close)

	repo := New(pool)
	SetRealtimePublisher(repo, &realtime.Publisher{Enabled: true})
	user := seedVerifiedUser(t, ctx, pool, repo, "realtime.revoke.test@example.invalid", "+84987650002")

	_, refreshHash, err := domain.NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_, session, err := repo.CreateSession(ctx, repository.CreateSessionParams{
		UserID:               user.ID,
		ExpectedPasswordHash: user.PasswordHash,
		DeviceID:             uuid.Must(uuid.NewV7()).String(),
		DeviceName:           "device",
		RefreshTokenHash:     refreshHash,
		Now:                  now,
		ExpiresAt:            now.Add(7 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.RevokeSession(ctx, user.ID, session.ID, "logout", time.Now()); err != nil {
		t.Fatal(err)
	}

	collect := listenUserEvents(t, ctx, pool)
	if err = repo.RevokeSession(ctx, user.ID, session.ID, "logout", time.Now()); err != nil {
		t.Fatal(err)
	}

	if targeted(collect(), uuid.MustParse(session.ID)) {
		t.Fatal("re-revoking an already revoked session published its SID again")
	}
}
