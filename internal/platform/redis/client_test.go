package redis

// Redis nằm trên đường đi của mọi request có xác thực, nên hai tính chất của gói
// này đáng được khoá lại: nó fail fast lúc khởi tạo (không để lỗi lộ ra dưới dạng
// 401 khó hiểu ở request đầu tiên), và Ping của nó trả error thuần để readiness
// probe dùng được. Trước file này gói không có test nào.
//
// Dùng miniredis (đã có sẵn trong go.mod cho internal/platform/session) nên test
// chạy được mà không cần Redis thật.
//
// covers: AC-14 (Redis là dependency bắt buộc, cấu hình sai dừng bootstrap ngay,
// readiness probe báo đúng thành phần hỏng).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"paysplit-backend/internal/config"
)

func cfgFor(addr string) config.RedisConfig {
	return config.RedisConfig{
		URL:         "redis://" + addr + "/0",
		PoolSize:    5,
		DialTimeout: 2 * time.Second,
		ReadTimeout: time.Second,
	}
}

func TestNew_ConnectsToAReachableRedis(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	client, err := New(context.Background(), cfgFor(server.Addr()))
	if err != nil {
		t.Fatalf("New lỗi bất ngờ: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if client == nil || client.Client == nil {
		t.Fatal("New trả client rỗng")
	}
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping trên client vừa tạo lỗi: %v", err)
	}
}

func TestNew_AppliesConfiguredPoolAndTimeouts(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	cfg := cfgFor(server.Addr())
	cfg.PoolSize = 37
	cfg.DialTimeout = 3 * time.Second
	cfg.ReadTimeout = 1500 * time.Millisecond

	client, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New lỗi bất ngờ: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	opts := client.Options()
	if opts.PoolSize != 37 {
		t.Fatalf("PoolSize = %d, want 37 (REDIS_POOL_SIZE bị bỏ qua)", opts.PoolSize)
	}
	if opts.DialTimeout != 3*time.Second {
		t.Fatalf("DialTimeout = %v, want 3s", opts.DialTimeout)
	}
	if opts.ReadTimeout != 1500*time.Millisecond {
		t.Fatalf("ReadTimeout = %v, want 1.5s", opts.ReadTimeout)
	}
	// Ghi phiên phải hoàn tất trong cùng ngân sách thời gian như đọc; một
	// WriteTimeout mặc định dài hơn sẽ để request treo lâu hơn ReadTimeout hứa.
	if opts.WriteTimeout != cfg.ReadTimeout {
		t.Fatalf("WriteTimeout = %v, want %v (bằng ReadTimeout)", opts.WriteTimeout, cfg.ReadTimeout)
	}
}

func TestNew_RejectsAMalformedURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
	}{
		{name: "URL rỗng", url: ""},
		{name: "sai scheme", url: "http://localhost:6379/0"},
		{name: "không có scheme", url: "localhost:6379"},
		{name: "rác", url: "://:::"},
		{name: "số hiệu database không phải số", url: "redis://localhost:6379/abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := cfgFor("localhost:6379")
			cfg.URL = tt.url

			client, err := New(context.Background(), cfg)
			if err == nil {
				_ = client.Close()
				t.Fatalf("New(%q) thành công, muốn lỗi: cấu hình sai phải dừng bootstrap ngay", tt.url)
			}
			if client != nil {
				t.Fatal("New trả cả client lẫn lỗi")
			}
			if !strings.Contains(err.Error(), "parse REDIS_URL") {
				t.Fatalf("lỗi = %q, muốn nêu tên biến REDIS_URL để người vận hành biết sửa ở đâu", err)
			}
		})
	}
}

// Đây là tính chất fail fast: kết nối go-redis được tạo lazy, nên nếu New không
// ping thì ứng dụng lên bình thường và mọi request sau đó trả 401 mà không ai
// hiểu vì sao.
func TestNew_FailsFastWhenRedisIsUnreachable(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	addr := server.Addr()
	server.Close() // cổng đã đóng, không còn ai lắng nghe

	cfg := cfgFor(addr)
	cfg.DialTimeout = 300 * time.Millisecond
	cfg.ReadTimeout = 300 * time.Millisecond

	client, err := New(context.Background(), cfg)
	if err == nil {
		_ = client.Close()
		t.Fatal("New thành công dù Redis không truy cập được: lỗi sẽ chỉ lộ ra ở request đầu tiên")
	}
	if client != nil {
		t.Fatal("New trả cả client lẫn lỗi; client rò rỉ sẽ không ai đóng")
	}
	if !strings.Contains(err.Error(), "ping Redis") {
		t.Fatalf("lỗi = %q, muốn nêu rõ bước ping thất bại", err)
	}
}

func TestNew_FailsWhenPasswordIsWrong(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	server.RequireAuth("dungmatkhau")

	// URL không mang mật khẩu: đây chính là cấu hình sai mà người vận hành dễ
	// mắc nhất, và nó phải dừng bootstrap chứ không âm thầm đi tiếp.
	client, err := New(context.Background(), cfgFor(server.Addr()))
	if err == nil {
		_ = client.Close()
		t.Fatal("New thành công với Redis có mật khẩu mà URL không mang mật khẩu")
	}
	if !strings.Contains(err.Error(), "ping Redis") {
		t.Fatalf("lỗi = %q, muốn nêu rõ bước ping thất bại", err)
	}
}

func TestNew_AcceptsPasswordFromTheURLUserinfo(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	server.RequireAuth("dungmatkhau")

	// RedisConfig cố tình không có field mật khẩu riêng; userinfo của URL là
	// đường duy nhất. Test này khoá lại điều đó.
	cfg := cfgFor(server.Addr())
	cfg.URL = "redis://:dungmatkhau@" + server.Addr() + "/0"

	client, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New lỗi với mật khẩu đặt trong userinfo: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping lỗi: %v", err)
	}
}

func TestPing_ReportsRedisGoingDownAndComingBack(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	cfg := cfgFor(server.Addr())
	cfg.DialTimeout = 300 * time.Millisecond
	cfg.ReadTimeout = 300 * time.Millisecond

	client, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New lỗi bất ngờ: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping lúc khoẻ lỗi: %v", err)
	}

	// /health/ready phải chuyển sang 503 khi Redis mất.
	server.Close()
	if err := client.Ping(context.Background()); err == nil {
		t.Fatal("Ping trả nil khi Redis đã tắt: readiness probe sẽ báo khoẻ trong lúc không ai xác thực được")
	}
}

func TestPing_OnAnUninitialisedClientReportsRatherThanPanics(t *testing.T) {
	t.Parallel()

	// Readiness probe chạy trên mọi đường thoát, kể cả khi bootstrap dừng giữa
	// chừng. Một panic ở đây làm sập tiến trình thay vì trả 503.
	var nilClient *Client
	if err := nilClient.Ping(context.Background()); err == nil {
		t.Fatal("Ping trên client nil trả nil, want lỗi")
	}

	empty := &Client{}
	if err := empty.Ping(context.Background()); err == nil {
		t.Fatal("Ping trên client chưa khởi tạo trả nil, want lỗi")
	}
}

func TestPing_RespectsAContextThatIsAlreadyCancelled(t *testing.T) {
	t.Parallel()

	server := miniredis.RunT(t)
	client, err := New(context.Background(), cfgFor(server.Addr()))
	if err != nil {
		t.Fatalf("New lỗi bất ngờ: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Readiness probe có deadline; Ping phải bỏ cuộc theo context thay vì treo.
	if err := client.Ping(ctx); err == nil {
		t.Fatal("Ping với context đã huỷ trả nil, want lỗi")
	}
}

// stubConfigGetter trả về đúng phản hồi mà một Redis thật sẽ trả cho CONFIG GET.
type stubConfigGetter struct {
	values map[string]string
	err    error
}

func (s stubConfigGetter) ConfigGet(_ context.Context, _ string) *goredis.MapStringStringCmd {
	return goredis.NewMapStringStringResult(s.values, s.err)
}

// Chính sách trục xuất là dành cho cache. Với kho phiên nó xoá phiên của người ít
// hoạt động khi đầy bộ nhớ, tạo ra đăng xuất ngẫu nhiên không log và không tái
// hiện được. Spec 0011 Follow up 4.
func TestVerifyEvictionPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stub    stubConfigGetter
		wantErr bool
	}{
		{name: "noeviction được chấp nhận", stub: stubConfigGetter{values: map[string]string{"maxmemory-policy": "noeviction"}}},
		{name: "hoa thường không quan trọng", stub: stubConfigGetter{values: map[string]string{"maxmemory-policy": "NoEviction"}}},
		{name: "allkeys-lru bị từ chối", stub: stubConfigGetter{values: map[string]string{"maxmemory-policy": "allkeys-lru"}}, wantErr: true},
		{name: "volatile-ttl bị từ chối", stub: stubConfigGetter{values: map[string]string{"maxmemory-policy": "volatile-ttl"}}, wantErr: true},
		// Redis được quản lý thường chặn CONFIG GET. Ở đó không đọc được không
		// đồng nghĩa cấu hình sai, nên chặn khởi động sẽ là dương tính giả.
		{name: "không đọc được thì đi tiếp", stub: stubConfigGetter{err: errors.New("ERR unknown command `config`")}},
		{name: "phản hồi thiếu key thì đi tiếp", stub: stubConfigGetter{values: map[string]string{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := verifyEvictionPolicy(context.Background(), tt.stub)
			if tt.wantErr {
				if err == nil {
					t.Fatal("verifyEvictionPolicy = nil: một chính sách trục xuất được chấp nhận cho kho phiên")
				}
				if !strings.Contains(err.Error(), "noeviction") {
					t.Fatalf("lỗi = %q, muốn nêu giá trị đúng cần đặt", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("verifyEvictionPolicy lỗi bất ngờ: %v", err)
			}
		})
	}
}
