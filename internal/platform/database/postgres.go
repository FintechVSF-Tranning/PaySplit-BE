package database

import (
	"context"
	"fmt"
	"strings"

	"paysplit-backend/internal/config"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ParsePoolConfig chuyển thiết lập database của ứng dụng thành cấu hình
// dành riêng cho driver mà pgxpool sử dụng.
func ParsePoolConfig(cfg config.DatabaseConfig) (*pgxpool.Config, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolConfig.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns >= 0 {
		poolConfig.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.HealthCheckPeriod > 0 {
		poolConfig.HealthCheckPeriod = cfg.HealthCheckPeriod
	}

	// Cấu hình QueryExecModeExec khi sử dụng Transaction Pooler (Supabase Supavisor trên cổng 6543).
	// Transaction pooler không hỗ trợ prepared statement xuyên suốt các session, do đó cần dùng Exec mode.
	if strings.EqualFold(cfg.PoolMode, "transaction") || strings.Contains(cfg.URL, ":6543") {
		poolConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	}

	if cfg.ApplicationName != "" {
		if poolConfig.ConnConfig.RuntimeParams == nil {
			poolConfig.ConnConfig.RuntimeParams = make(map[string]string)
		}
		poolConfig.ConnConfig.RuntimeParams["application_name"] = cfg.ApplicationName
	}

	if cfg.IdleInTransactionTimeout > 0 {
		if poolConfig.ConnConfig.RuntimeParams == nil {
			poolConfig.ConnConfig.RuntimeParams = make(map[string]string)
		}
		poolConfig.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = fmt.Sprintf("%dms", cfg.IdleInTransactionTimeout.Milliseconds())
	}

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

