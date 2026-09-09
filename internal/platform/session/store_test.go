package session

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

const (
	testIdleTTL     = 7 * 24 * time.Hour
	testAbsoluteTTL = 30 * 24 * time.Hour
)

func newTestStore(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedisStore(client, testIdleTTL, testAbsoluteTTL), server
}

func mustCredential(t *testing.T) string {
	t.Helper()
	raw, err := NewCredential()
	if err != nil {
		t.Fatalf("new credential: %v", err)
	}
	return raw
}

func TestCreateThenGetReturnsSession(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	raw := mustCredential(t)

	want := Session{SID: "1f0a0000-0000-7000-8000-000000000001", UserID: "user-1", Role: "user"}
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
	if !got.AbsoluteExp.Equal(now.Add(testAbsoluteTTL).Truncate(time.Second)) {
		t.Errorf("absolute exp = %v, want %v", got.AbsoluteExp, now.Add(testAbsoluteTTL))
	}
}

func TestGetRejectsUnknownCredential(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Get(context.Background(), mustCredential(t), time.Now()); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// Credential được lưu dưới dạng hash, không phải chuỗi trần: ai dump được Redis,
// file AOF hay output MONITOR cũng không thể đăng nhập lại bằng thứ đọc được.
func TestCredentialIsNeverStoredInClear(t *testing.T) {
	store, server := newTestStore(t)
	raw := mustCredential(t)
	if err := store.Create(context.Background(), raw, Session{SID: "sid-1", UserID: "user-1", Role: "user"}, time.Now()); err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, key := range server.Keys() {
		if key == "session:"+raw {
			t.Fatal("credential appears verbatim in a Redis key")
		}
		if server.Type(key) == "string" {
			value, err := server.Get(key)
			if err == nil && value == raw {
				t.Fatalf("credential stored in clear at key %q", key)
			}
		}
	}
}

// Mỗi request có auth phải đẩy hạn phiên ra xa, nếu không người dùng bị đăng xuất
// đúng 7 ngày sau lần đăng nhập bất kể họ dùng app hằng ngày.
func TestGetRefreshesSlidingTTL(t *testing.T) {
	store, server := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	raw := mustCredential(t)
	if err := store.Create(ctx, raw, Session{SID: "sid-1", UserID: "user-1", Role: "user"}, now); err != nil {
		t.Fatalf("create: %v", err)
	}

	server.FastForward(6 * 24 * time.Hour)
	if _, err := store.Get(ctx, raw, now.Add(6*24*time.Hour)); err != nil {
		t.Fatalf("get after 6 days: %v", err)
	}

	// Nếu TTL không được gia hạn, phiên đã chết ở mốc 7 ngày.
	server.FastForward(5 * 24 * time.Hour)
	if _, err := store.Get(ctx, raw, now.Add(11*24*time.Hour)); err != nil {
		t.Fatalf("session should still be alive after a sliding refresh, got %v", err)
	}
}

// Trần tuyệt đối là thứ chặn phiên sống vĩnh viễn nhờ TTL trượt. Nó phải thắng
// ngay cả khi người dùng hoạt động liên tục.
func TestGetRejectsSessionPastAbsoluteExpiry(t *testing.T) {
	store, server := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	raw := mustCredential(t)
	if err := store.Create(ctx, raw, Session{SID: "sid-1", UserID: "user-1", Role: "user"}, now); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Gia hạn đều đặn 6 ngày một lần, đủ để TTL trượt không bao giờ hết.
	for day := 6; day < 30; day += 6 {
		server.FastForward(6 * 24 * time.Hour)
		if _, err := store.Get(ctx, raw, now.Add(time.Duration(day)*24*time.Hour)); err != nil {
			t.Fatalf("get at day %d: %v", day, err)
		}
	}

	server.FastForward(6 * 24 * time.Hour)
	if _, err := store.Get(ctx, raw, now.Add(31*24*time.Hour)); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound past the absolute cap", err)
	}
	// Bản ghi phải được dọn luôn chứ không nằm lại tới hết TTL trượt.
	if server.Exists("session:" + credentialHash(raw)) {
		t.Error("expired session record should be deleted, not left behind")
	}
}

// Đăng nhập lại phải giết phiên cũ. Chỉ ghi đè con trỏ user_session mà quên xoá
// bản ghi phiên cũ sẽ để lại một HASH mồ côi giữ TTL riêng: thiết bị cũ tiếp tục
// đăng nhập được tới 7 ngày, và không chỉ mục nào còn trỏ tới nó để dọn.
func TestCreateKillsPreviousSessionWithoutLeavingOrphans(t *testing.T) {
	store, server := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	first := mustCredential(t)
	if err := store.Create(ctx, first, Session{SID: "sid-1", UserID: "user-1", Role: "user"}, now); err != nil {
		t.Fatalf("create first: %v", err)
	}
	second := mustCredential(t)
	if err := store.Create(ctx, second, Session{SID: "sid-2", UserID: "user-1", Role: "user"}, now); err != nil {
		t.Fatalf("create second: %v", err)
	}

	if _, err := store.Get(ctx, first, now); err != ErrNotFound {
		t.Fatalf("first credential err = %v, want ErrNotFound after re-login", err)
	}
	if _, err := store.Get(ctx, second, now); err != nil {
		t.Fatalf("second credential should be live: %v", err)
	}

	var sessionKeys int
	for _, key := range server.Keys() {
		if len(key) > 8 && key[:8] == "session:" {
			sessionKeys++
		}
	}
	if sessionKeys != 1 {
		t.Fatalf("session record count = %d, want exactly 1 (orphan left behind)", sessionKeys)
	}
}

func TestPeekAndDeleteReturnsSessionThenRemovesIt(t *testing.T) {
	store, server := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	raw := mustCredential(t)
	if err := store.Create(ctx, raw, Session{SID: "sid-1", UserID: "user-1", Role: "user"}, now); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := store.PeekAndDelete(ctx, raw)
	if err != nil {
		t.Fatalf("peek and delete: %v", err)
	}
	if got.SID != "sid-1" || got.UserID != "user-1" {
		t.Fatalf("session = %+v, want sid-1/user-1", got)
	}
	if server.Exists("user_session:user-1") {
		t.Error("reverse index should be cleared on sign-out")
	}
	// Đăng xuất lần hai bằng cùng credential vẫn phải im lặng, để handler trả 204.
	if _, err := store.PeekAndDelete(ctx, raw); err != ErrNotFound {
		t.Fatalf("second sign-out err = %v, want ErrNotFound", err)
	}
}

// Đăng xuất một phiên đã bị thay thế không được xoá con trỏ của phiên mới, nếu
// không thì đổi mật khẩu hay admin khoá tài khoản sẽ không thu hồi được phiên đó.
func TestPeekAndDeleteKeepsPointerOfANewerSession(t *testing.T) {
	store, server := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	stale := mustCredential(t)
	if err := store.Create(ctx, stale, Session{SID: "sid-1", UserID: "user-1", Role: "user"}, now); err != nil {
		t.Fatalf("create stale: %v", err)
	}
	fresh := mustCredential(t)
	if err := store.Create(ctx, fresh, Session{SID: "sid-2", UserID: "user-1", Role: "user"}, now); err != nil {
		t.Fatalf("create fresh: %v", err)
	}
	// Hồi sinh bản ghi cũ SAU khi phiên mới đã chiếm con trỏ, để mô phỏng một
	// client vẫn cầm credential đã bị thay thế và bấm đăng xuất muộn.
	server.HSet("session:"+credentialHash(stale), "sid", "sid-1", "user_id", "user-1", "role", "user",
		"absolute_exp", "99999999999")

	if _, err := store.PeekAndDelete(ctx, stale); err != nil {
		t.Fatalf("peek and delete stale: %v", err)
	}
	if got, _ := server.Get("user_session:user-1"); got != credentialHash(fresh) {
		t.Fatal("signing out a stale credential must not clear the newer session's pointer")
	}
	if _, err := store.Get(ctx, fresh, now); err != nil {
		t.Fatalf("newer session should survive: %v", err)
	}
}

func TestRevokeUserSIDsRemovesMatchingSession(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	raw := mustCredential(t)
	if err := store.Create(ctx, raw, Session{SID: "sid-1", UserID: "user-1", Role: "user"}, now); err != nil {
		t.Fatalf("create: %v", err)
	}

	revoked, err := store.RevokeUserSIDs(ctx, "user-1", []string{"sid-1"})
	if err != nil || !revoked {
		t.Fatalf("revoke = %v, err = %v, want true/nil", revoked, err)
	}
	if _, err := store.Get(ctx, raw, now); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound after revoke", err)
	}
}

// Job thu hồi có thể chạy lại vài phút sau sự kiện. Nếu trong lúc đó người dùng
// đã đăng nhập lại, phiên mới mang SID khác và không được giết oan.
func TestRevokeUserSIDsSparesASessionCreatedAfterwards(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	newer := mustCredential(t)
	if err := store.Create(ctx, newer, Session{SID: "sid-2", UserID: "user-1", Role: "user"}, now); err != nil {
		t.Fatalf("create: %v", err)
	}

	revoked, err := store.RevokeUserSIDs(ctx, "user-1", []string{"sid-1"})
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked {
		t.Fatal("revoking a stale SID must not report a revocation")
	}
	if _, err := store.Get(ctx, newer, now); err != nil {
		t.Fatalf("session created after the revocation must survive: %v", err)
	}
}

func TestRevokeUserSIDsIsIdempotentAndSafeWhenNothingLives(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	revoked, err := store.RevokeUserSIDs(ctx, "user-without-session", []string{"sid-1"})
	if err != nil || revoked {
		t.Fatalf("revoke = %v, err = %v, want false/nil", revoked, err)
	}
}

// RevokeUser là đường thu hồi vô điều kiện, dành riêng cho việc khóa tài khoản.
//
// Nó tồn tại vì RevokeUserSIDs chỉ giết được phiên có SID nằm trong danh sách mà
// Postgres trả về, nên khi Postgres đã đánh dấu hàng audit là revoked mà key
// Redis còn sống thì không lệnh nào chạm tới được nữa.
//
// covers: AC-11
func TestRevokeUserKillsTheLiveSessionWhateverItsSID(t *testing.T) {
	store, server := newTestStore(t)
	ctx := context.Background()
	raw := mustCredential(t)
	now := time.Now()

	if err := store.Create(ctx, raw, Session{SID: "sid-khong-ai-biet", UserID: "user-1", Role: "user"}, now); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Không truyền SID nào cả, vẫn phải giết được phiên.
	revoked, err := store.RevokeUser(ctx, "user-1")
	if err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}
	if !revoked {
		t.Fatal("RevokeUser trả false dù user đang có phiên sống")
	}
	if _, err = store.Get(ctx, raw, now); err == nil {
		t.Fatal("phiên vẫn sống sau RevokeUser")
	}
	if keys := server.Keys(); len(keys) != 0 {
		t.Fatalf("còn sót key sau khi thu hồi: %v", keys)
	}
}

func TestRevokeUserIsSafeWhenThereIsNothingToRevoke(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"user chưa từng đăng nhập", "user_id rỗng"} {
		userID := "user-khong-ton-tai"
		if name == "user_id rỗng" {
			userID = ""
		}
		t.Run(name, func(t *testing.T) {
			revoked, err := store.RevokeUser(ctx, userID)
			if err != nil {
				t.Fatalf("RevokeUser lỗi bất ngờ: %v", err)
			}
			if revoked {
				t.Fatal("RevokeUser báo đã xoá dù không có gì để xoá")
			}
		})
	}
}

func TestRevokeUserCleansUpAnOrphanPointer(t *testing.T) {
	store, server := newTestStore(t)
	ctx := context.Background()
	raw := mustCredential(t)

	if err := store.Create(ctx, raw, Session{SID: "sid-1", UserID: "user-1", Role: "user"}, time.Now()); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Bản ghi phiên hết hạn trước, con trỏ còn lại một mình.
	server.Del(credentialKey(raw))

	revoked, err := store.RevokeUser(ctx, "user-1")
	if err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}
	if revoked {
		t.Fatal("RevokeUser báo đã xoá một phiên vốn đã hết hạn")
	}
	if keys := server.Keys(); len(keys) != 0 {
		t.Fatalf("con trỏ mồ côi không được dọn: %v", keys)
	}
}

// Khóa tài khoản chỉ được giết phiên của đúng user đó.
func TestRevokeUserLeavesOtherUsersAlone(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	now := time.Now()

	rawA := mustCredential(t)
	rawB := mustCredential(t)
	if err := store.Create(ctx, rawA, Session{SID: "sid-a", UserID: "user-a", Role: "user"}, now); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := store.Create(ctx, rawB, Session{SID: "sid-b", UserID: "user-b", Role: "user"}, now); err != nil {
		t.Fatalf("create B: %v", err)
	}

	if _, err := store.RevokeUser(ctx, "user-a"); err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}

	if _, err := store.Get(ctx, rawB, now); err != nil {
		t.Fatalf("phiên của user-b bị giết oan: %v", err)
	}
}
