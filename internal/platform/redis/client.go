// Package redis cung cấp kết nối tới Redis — nơi lưu phiên đăng nhập.
//
// Redis nằm trên đường đi của mọi request có xác thực, nên nó là dependency bắt
// buộc chứ không phải cache tuỳ chọn: mất Redis là mất khả năng xác thực. Vì thế
// gói này ping ngay lúc khởi tạo (fail fast) và phơi ra Ping để readiness probe
// dùng, thay vì để lỗi lộ ra ở request đầu tiên.
package redis

import (
	"context"
	"fmt"
	"log"
	"strings"

	goredis "github.com/redis/go-redis/v9"

	"paysplit-backend/internal/config"
)

// Client bọc *goredis.Client để phơi ra Ping trả về error thuần — go-redis trả
// *StatusCmd nên không tự thoả interface probe của router.
type Client struct {
	*goredis.Client
}

// New tạo và kiểm tra kết nối Redis. Bên gọi sở hữu client được trả về và phải
// đóng khi ứng dụng dừng.
func New(ctx context.Context, cfg config.RedisConfig) (*Client, error) {
	opts, err := goredis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	opts.PoolSize = cfg.PoolSize
	opts.DialTimeout = cfg.DialTimeout
	opts.ReadTimeout = cfg.ReadTimeout
	// Ghi phiên là thao tác nhỏ và phải hoàn tất trong cùng ngân sách thời gian
	// như đọc; dùng chung một giá trị để không phát sinh thêm biến môi trường.
	opts.WriteTimeout = cfg.ReadTimeout

	client := goredis.NewClient(opts)
	// Kết nối được tạo lazy, nên ping ngay để startup thất bại sớm nếu Redis
	// không truy cập được, thay vì để mọi request trả 401 một cách khó hiểu.
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping Redis: %w", err)
	}
	if err := verifyEvictionPolicy(ctx, client); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &Client{Client: client}, nil
}

// verifyEvictionPolicy từ chối khởi động khi Redis được cấu hình để trục xuất key.
//
// Chính sách kiểu `allkeys-lru` là dành cho cache. Với kho phiên nó âm thầm xoá
// phiên của người ít hoạt động khi bộ nhớ đầy, tạo ra những lần đăng xuất ngẫu
// nhiên không log và không tái hiện được — loại sự cố tốn hàng ngày để lần ra.
// Chỉ `noeviction` là đúng: đầy bộ nhớ thì lệnh ghi phải lỗi ra mặt.
//
// Redis được quản lý (Upstash, ElastiCache) thường chặn CONFIG GET. Ở đó không
// đọc được không có nghĩa là cấu hình sai, nên chỉ cảnh báo rồi đi tiếp; chỉ khi
// đọc được VÀ giá trị sai mới dừng.
// configGetter là bề mặt tối thiểu mà verifyEvictionPolicy cần, để kiểm tra được
// cả hai nhánh mà không phải dựng một server RESP thật.
type configGetter interface {
	ConfigGet(ctx context.Context, parameter string) *goredis.MapStringStringCmd
}

func verifyEvictionPolicy(ctx context.Context, client configGetter) error {
	values, err := client.ConfigGet(ctx, "maxmemory-policy").Result()
	if err != nil {
		log.Printf("event=redis_eviction_policy_unverified err=%v", err)
		return nil
	}
	policy, ok := values["maxmemory-policy"]
	if !ok {
		log.Printf("event=redis_eviction_policy_unverified reason=absent_from_response")
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(policy), "noeviction") {
		return fmt.Errorf("Redis maxmemory-policy is %q but the session store requires \"noeviction\"; any eviction policy silently signs users out", policy)
	}
	return nil
}

// Ping thoả interface probe của readiness handler.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.Client == nil {
		return fmt.Errorf("redis client is not initialised")
	}
	return c.Client.Ping(ctx).Err()
}
