package database

import (
	"context"
	"fmt"

	"paysplit-backend/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ParsePoolConfig chuyển thiết lập database của ứng dụng thành cấu hình
// dành riêng cho driver mà pgxpool sử dụng.
func ParsePoolConfig(cfg config.DatabaseConfig) (*pgxpool.Config, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}

	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolConfig.HealthCheckPeriod = cfg.HealthCheckPeriod
	return poolConfig, nil
}

// NewPostgresPool tạo và kiểm tra pool kết nối PostgreSQL. Bên gọi sở hữu pool
// được trả về và phải đóng pool khi ứng dụng dừng.
func NewPostgresPool(ctx context.Context, cfg config.DatabaseConfig) (*pgxpool.Pool, error) {
	poolConfig, err := ParsePoolConfig(cfg)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	// Pool được khởi tạo theo cơ chế lazy, vì vậy ping ngay để startup thất bại
	// sớm nếu không thể truy cập database thay vì đợi đến request đầu tiên.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return pool, nil
}
