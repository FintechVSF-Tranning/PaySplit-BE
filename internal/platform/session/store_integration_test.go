package session

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	goredis "github.com/redis/go-redis/v9"
)

// miniredis chạy Lua bằng gopher-lua chứ không phải bộ thông dịch của Redis, nên
// unit test không chứng minh được script hoạt động thật. Những test dưới đây chạy
// trên Redis thật để bắt các khác biệt về EVALSHA, ép kiểu, và quy ước giá trị trả
// về của redis.call.
func integrationStore(t *testing.T, idleTTL, absoluteTTL time.Duration) (*RedisStore, *goredis.Client) {
	t.Helper()
	_ = godotenv.Load("../../../.env")
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not configured")
	}
	opts, err := goredis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse TEST_REDIS_URL: %v", err)
	}
	client := goredis.NewClient(opts)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("redis unavailable: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisStore(client, idleTTL, absoluteTTL), client
}

// Mỗi test dùng user ID ngẫu nhiên và tự dọn key của mình, thay vì FLUSHDB — để
// lỡ ai trỏ TEST_REDIS_URL vào một instance dùng chung thì cũng không xoá nhầm.
func integrationSession(t *testing.T, client *goredis.Client) (raw string, sess Session) {
	t.Helper()
	raw, err := NewCredential()
	if err != nil {
		t.Fatalf("new credential: %v", err)
	}
	sess = Session{SID: uuid.NewString(), UserID: uuid.NewString(), Role: "user"}
	t.Cleanup(func() {
		client.Del(context.Background(), "session:"+credentialHash(raw), "user_session:"+sess.UserID)
	})
	return raw, sess
}

func TestIntegrationCreateGetRevokeRoundTrip(t *testing.T) {
	store, client := integrationStore(t, 7*24*time.Hour, 30*24*time.Hour)
	ctx := context.Background()
	now := time.Now()
	raw, want := integrationSession(t, client)

	if err := store.Create(ctx, raw, want, now); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.Get(ctx, raw, now)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SID != want.SID || got.UserID != want.UserID || got.Role != want.Role {
		t.Fatalf("session = %+v, want %+v", got, want)
	}

	revoked, err := store.RevokeUserSIDs(ctx, want.UserID, []string{want.SID})
	if err != nil || !revoked {
		t.Fatalf("revoke = %v, err = %v", revoked, err)
	}
	if _, err := store.Get(ctx, raw, now); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// Con trỏ user_session phải mang TTL tuyệt đối, không phải TTL trượt: bản ghi
// phiên được gia hạn mỗi request nên sống lâu hơn 7 ngày, và nếu con trỏ hết hạn
// trước thì mất khả năng thu hồi theo user.
func TestIntegrationPointerCarriesAbsoluteTTL(t *testing.T) {
	store, client := integrationStore(t, 7*24*time.Hour, 30*24*time.Hour)
	ctx := context.Background()
	raw, sess := integrationSession(t, client)
	if err := store.Create(ctx, raw, sess, time.Now()); err != nil {
		t.Fatalf("create: %v", err)
	}

	sessionTTL := client.TTL(ctx, "session:"+credentialHash(raw)).Val()
	pointerTTL := client.TTL(ctx, "user_session:"+sess.UserID).Val()

	if sessionTTL > 7*24*time.Hour || sessionTTL < 6*24*time.Hour {
		t.Errorf("session TTL = %v, want about the 7 day idle window", sessionTTL)
	}
	if pointerTTL < 29*24*time.Hour {
		t.Errorf("pointer TTL = %v, want about the 30 day absolute window", pointerTTL)
	}
}

// TTL trượt phải bị kẹp vào phần thời gian tuyệt đối còn lại, nếu không mỗi request
// lại đẩy hạn ra thêm và key sống lâu hơn chính mốc absolute_exp của nó.
func TestIntegrationSlidingTTLIsClampedToRemainingAbsoluteWindow(t *testing.T) {
	// Trần chỉ hơn cửa sổ trượt vài giây, nên phần còn lại chắc chắn nhỏ hơn.
	store, client := integrationStore(t, time.Hour, time.Hour+10*time.Second)
	ctx := context.Background()
	now := time.Now()
	raw, sess := integrationSession(t, client)
	if err := store.Create(ctx, raw, sess, now); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := store.Get(ctx, raw, now); err != nil {
		t.Fatalf("get: %v", err)
	}
	ttl := client.TTL(ctx, "session:"+credentialHash(raw)).Val()
	if ttl > time.Hour+10*time.Second {
		t.Fatalf("session TTL = %v, want no more than the remaining absolute window", ttl)
	}
}

// Phiên quá trần tuyệt đối phải bị từ chối VÀ bị xoá ngay, chứ không nằm lại chiếm
// bộ nhớ tới hết TTL trượt.
func TestIntegrationExpiredSessionIsRejectedAndSelfHeals(t *testing.T) {
	store, client := integrationStore(t, time.Hour, 2*time.Hour)
	ctx := context.Background()
	now := time.Now()
	raw, sess := integrationSession(t, client)
	if err := store.Create(ctx, raw, sess, now); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Đẩy thời gian đọc vượt trần thay vì chờ: absolute_exp được so với ARGV.
	if _, err := store.Get(ctx, raw, now.Add(3*time.Hour)); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound past the absolute cap", err)
	}
	exists, err := client.Exists(ctx, "session:"+credentialHash(raw)).Result()
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if exists != 0 {
		t.Error("expired session record should be deleted by the script, not left behind")
	}
}

// Đăng nhập lại phải xoá bản ghi phiên cũ. Nếu chỉ ghi đè con trỏ, HASH cũ giữ TTL
// riêng và thiết bị cũ vẫn đăng nhập được, trong khi không chỉ mục nào còn trỏ tới
// nó để cơ chế dọn tìm ra.
func TestIntegrationReLoginLeavesNoOrphanRecord(t *testing.T) {
	store, client := integrationStore(t, 7*24*time.Hour, 30*24*time.Hour)
	ctx := context.Background()
	now := time.Now()

	first, sess := integrationSession(t, client)
	if err := store.Create(ctx, first, sess, now); err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := NewCredential()
	if err != nil {
		t.Fatalf("new credential: %v", err)
	}
	t.Cleanup(func() { client.Del(context.Background(), "session:"+credentialHash(second)) })

	next := Session{SID: uuid.NewString(), UserID: sess.UserID, Role: sess.Role}
	if err := store.Create(ctx, second, next, now); err != nil {
		t.Fatalf("create second: %v", err)
	}

	stale, err := client.Exists(ctx, "session:"+credentialHash(first)).Result()
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if stale != 0 {
		t.Fatal("previous session record must be deleted on re-login")
	}
	if _, err := store.Get(ctx, second, now); err != nil {
		t.Fatalf("new session should be live: %v", err)
	}
}

// Script được nạp một lần rồi gọi bằng EVALSHA. Chạy nhiều lượt để chắc chắn
// đường EVALSHA hoạt động, không chỉ lần EVAL đầu tiên.
func TestIntegrationScriptsSurviveRepeatedInvocation(t *testing.T) {
	store, client := integrationStore(t, time.Hour, 2*time.Hour)
	ctx := context.Background()
	now := time.Now()
	raw, sess := integrationSession(t, client)
	if err := store.Create(ctx, raw, sess, now); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 20; i++ {
		if _, err := store.Get(ctx, raw, now); err != nil {
			t.Fatalf("get %d: %v", i, err)
		}
	}
	if _, err := store.PeekAndDelete(ctx, raw); err != nil {
		t.Fatalf("peek and delete: %v", err)
	}
	if _, err := store.PeekAndDelete(ctx, raw); err != ErrNotFound {
		t.Fatalf("second sign-out must stay idempotent, got %v", fmt.Sprint(err))
	}
}
