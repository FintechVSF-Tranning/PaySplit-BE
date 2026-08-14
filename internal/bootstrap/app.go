package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/config"
	authhttp "paysplit-backend/internal/modules/auth/delivery/http"
	authpostgres "paysplit-backend/internal/modules/auth/repository/postgres"
	"paysplit-backend/internal/modules/auth/usecase"
	"paysplit-backend/internal/platform/auth/jwt"
	"paysplit-backend/internal/platform/database"
	"paysplit-backend/internal/platform/security/password"
	"paysplit-backend/internal/transport/http/router"
)

// App đại diện cho ứng dụng API đã được khởi tạo và sở hữu HTTP server cùng
// database pool; các tài nguyên này phải được giải phóng khi ứng dụng dừng.
type App struct {
	server *http.Server
	db     *pgxpool.Pool
}

// New khởi tạo ứng dụng API bằng cách tải cấu hình, mở hạ tầng dùng chung,
// kết nối các dependency của module và xây dựng HTTP server.
func New(ctx context.Context) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	db, err := database.NewPostgresPool(ctx, cfg.Database)
	if err != nil {
		return nil, err
	}

	tokens, err := jwt.NewAccessTokenManager(cfg.Auth.JWTSecret, cfg.Auth.JWTIssuer, cfg.Auth.AccessTokenTTL)
	if err != nil {
		// New sở hữu pool sau khi tạo, vì vậy phải giải phóng nếu bước kết nối
		// dependency tiếp theo thất bại.
		db.Close()
		return nil, fmt.Errorf("create JWT issuer: %w", err)
	}
	authService := usecase.NewService(authpostgres.New(db), password.New(), tokens)
	authHandler := authhttp.NewHandler(authService)

	appRouter := router.New(cfg.App)
	// Đăng ký toàn bộ route trước khi server bắt đầu nhận request. Module mới
	// chỉ cần mount tại đây, không phải thêm tham số vào router.New.
	appRouter.Route("/api/v1/auth", authHandler.RegisterRoutes)

	return &App{
		db: db,
		server: &http.Server{
			Addr:    cfg.App.Address,
			Handler: appRouter,
		},
	}, nil
}

// Address trả về địa chỉ mạng đã cấu hình cho HTTP server.
func (a *App) Address() string {
	return a.server.Addr
}

// Start phục vụ HTTP request và chặn luồng cho đến khi server dừng hoặc gặp
// lỗi ngoài dự kiến.
func (a *App) Start() error {
	if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}

// Shutdown chờ HTTP server xử lý xong các request đang chạy trước khi đóng
// database pool dùng chung; ctx giới hạn thời gian chờ quá trình này.
func (a *App) Shutdown(ctx context.Context) error {
	if err := a.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	// Giữ pool hoạt động cho đến khi các handler đang xử lý request hoàn tất.
	a.db.Close()
	return nil
}
