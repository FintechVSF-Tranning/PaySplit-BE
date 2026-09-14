package redis

// miniredis không cài lệnh CONFIG, nên unit test của verifyEvictionPolicy chỉ
// chạy được nhánh "không đọc được" và các nhánh dùng stub. Cái stub đó khoá lại
// GIẢ ĐỊNH của chúng ta về hình dạng phản hồi CONFIG GET, chứ không kiểm chứng
// nó. Nếu tên tham số sai, hoặc go-redis đổi cách trả map, unit test vẫn xanh
// trong khi ứng dụng thật im lặng bỏ qua một Redis cấu hình sai.
//
// Những test dưới đây chạy trên Redis thật để đóng đúng khoảng trống đó.
//
// covers: spec 0011 Follow up 4 (cưỡng chế maxmemory-policy thay vì tin vào
// cấu hình), spec 0011 AC-14 (cấu hình sai dừng bootstrap ngay).

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
	goredis "github.com/redis/go-redis/v9"

	"paysplit-backend/internal/config"
)

// integrationRedis mở kết nối tới Redis thật và bỏ qua test khi chưa cấu hình.
// Bỏ qua chứ không fail: một máy chưa dựng Redis vẫn phải chạy được `make test`.
func integrationRedis(t *testing.T) (*goredis.Client, config.RedisConfig) {
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

	return client, config.RedisConfig{
		URL:         url,
		PoolSize:    5,
		DialTimeout: 2 * time.Second,
		ReadTimeout: time.Second,
	}
}

// setPolicy đổi maxmemory-policy và trả nó về giá trị cũ khi test kết thúc.
// Khôi phục là bắt buộc: để lại một instance dùng chung ở chế độ trục xuất sẽ
// làm những lần chạy sau bị đăng xuất ngẫu nhiên, đúng thứ mà spec cấm.
func setPolicy(t *testing.T, client *goredis.Client, policy string) {
	t.Helper()
	ctx := context.Background()
	previous, err := client.ConfigGet(ctx, "maxmemory-policy").Result()
	if err != nil {
		t.Skipf("Redis này không cho chạy CONFIG GET: %v", err)
	}
	original, ok := previous["maxmemory-policy"]
	if !ok {
		t.Skip("CONFIG GET không trả về maxmemory-policy")
	}
	if err := client.ConfigSet(ctx, "maxmemory-policy", policy).Err(); err != nil {
		t.Skipf("Redis này không cho chạy CONFIG SET: %v", err)
	}
	t.Cleanup(func() {
		_ = client.ConfigSet(context.Background(), "maxmemory-policy", original).Err()
	})
}

// Đây là điều mà stub không chứng minh được: tham số đúng tên, và go-redis trả
// về map khoá theo tên tham số như code đang giả định.
func TestVerifyEvictionPolicy_ReadsARealRedis(t *testing.T) {
	client, _ := integrationRedis(t)
	setPolicy(t, client, "noeviction")

	if err := verifyEvictionPolicy(context.Background(), client); err != nil {
		t.Fatalf("Redis thật đang ở noeviction nhưng bị từ chối: %v", err)
	}
}

// Chính sách trục xuất phải bị chặn khi đọc được từ một server thật, không chỉ
// từ stub.
func TestVerifyEvictionPolicy_RejectsARealEvictionPolicy(t *testing.T) {
	client, _ := integrationRedis(t)
	setPolicy(t, client, "allkeys-lru")

	err := verifyEvictionPolicy(context.Background(), client)
	if err == nil {
		t.Fatal("Redis thật đang ở allkeys-lru nhưng được chấp nhận: kho phiên sẽ bị xoá âm thầm khi đầy bộ nhớ")
	}
	if !strings.Contains(err.Error(), "allkeys-lru") || !strings.Contains(err.Error(), "noeviction") {
		t.Fatalf("lỗi = %q, muốn nêu cả giá trị đang sai lẫn giá trị cần đặt", err)
	}
}

// Kiểm tra phải nằm trên đường khởi tạo thật, không chỉ là một hàm rời được gọi
// trong test. Nếu New quên gọi nó thì ứng dụng vẫn lên với cấu hình sai.
func TestNew_RefusesToConnectWhenRedisEvicts(t *testing.T) {
	client, cfg := integrationRedis(t)
	setPolicy(t, client, "allkeys-lru")

	got, err := New(context.Background(), cfg)
	if err == nil {
		if got != nil {
			_ = got.Close()
		}
		t.Fatal("New thành công với một Redis đang trục xuất key: bootstrap phải dừng ở đây")
	}
	if !strings.Contains(err.Error(), "noeviction") {
		t.Fatalf("lỗi = %q, muốn nêu giá trị cần đặt để người vận hành biết sửa gì", err)
	}
}

// Mặt còn lại của cùng một hợp đồng: cấu hình đúng thì New phải đi qua được.
// Không có test này thì một hàm kiểm tra luôn trả lỗi cũng làm test trên xanh.
func TestNew_ConnectsWhenRedisDoesNotEvict(t *testing.T) {
	client, cfg := integrationRedis(t)
	setPolicy(t, client, "noeviction")

	got, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New thất bại với một Redis cấu hình đúng: %v", err)
	}
	t.Cleanup(func() { _ = got.Close() })

	if err := got.Ping(context.Background()); err != nil {
		t.Fatalf("Ping thất bại trên client vừa tạo: %v", err)
	}
}
