package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"paysplit-backend/internal/config"
	authhttp "paysplit-backend/internal/modules/auth/delivery/http"
	authjobs "paysplit-backend/internal/modules/auth/jobs"
	authpostgres "paysplit-backend/internal/modules/auth/repository/postgres"
	"paysplit-backend/internal/modules/auth/usecase"
	notificationhttp "paysplit-backend/internal/modules/notification/delivery/http"
	notificationjobs "paysplit-backend/internal/modules/notification/jobs"
	notificationpostgres "paysplit-backend/internal/modules/notification/repository/postgres"
	notificationusecase "paysplit-backend/internal/modules/notification/usecase"
	authusecase "paysplit-backend/internal/modules/auth/usecase"
	grouphttp "paysplit-backend/internal/modules/group/delivery/http"
	grouppostgres "paysplit-backend/internal/modules/group/repository/postgres"
	groupusecase "paysplit-backend/internal/modules/group/usecase"
	"paysplit-backend/internal/platform/auth/jwt"
	"paysplit-backend/internal/platform/banks"
	"paysplit-backend/internal/platform/database"
	"paysplit-backend/internal/platform/email/gmail"
	avatarimage "paysplit-backend/internal/platform/image/avatar"
	"paysplit-backend/internal/platform/notification/fcm"
	riverpkg "paysplit-backend/internal/platform/queue/river"
	"paysplit-backend/internal/platform/security/password"
	avatarstorage "paysplit-backend/internal/platform/storage/cloudinary"
	transportmw "paysplit-backend/internal/transport/http/middleware"
	"paysplit-backend/internal/transport/http/router"
)

// App đại diện cho ứng dụng API đã được khởi tạo và sở hữu HTTP server cùng
// database pool và job queue; các tài nguyên này phải được giải phóng khi ứng dụng dừng.
type App struct {
	server        *http.Server
	db            *pgxpool.Pool
	riverClient   *river.Client[pgx.Tx]
	cancelWorkers context.CancelFunc
	workers       sync.WaitGroup
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
		db.Close()
		return nil, fmt.Errorf("create JWT issuer: %w", err)
	}
	authRepo := authpostgres.New(db)
	bankDirectory, err := banks.Load()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("load banks: %w", err)
	}
	mailer, err := gmail.New(cfg.SMTP, cfg.Auth.EmailVerificationURL, cfg.Auth.PasswordResetURL)
	if err != nil {
		db.Close()
		return nil, err
	}
	avatarStore, err := avatarstorage.New(cfg.Cloudinary, cfg.Avatar.UploadTimeout)
	if err != nil {
		db.Close()
		return nil, err
	}
	imageProcessor := avatarimage.NewProcessor(cfg.Avatar.ProcessingTimeout, cfg.Avatar.MaxConcurrentConversions)
	authService := authusecase.NewService(authRepo, password.New(), tokens, mailer, bankDirectory, imageProcessor, avatarStore, authusecase.Options{VerificationTTL: cfg.Auth.EmailVerificationTTL, ResetTTL: cfg.Auth.PasswordResetTTL, SessionTTL: cfg.Auth.RefreshTokenTTL})
	authHandler := authhttp.NewHandler(authService, avatarStore.URL)

	fcmNotifier, err := fcm.New(ctx, cfg.Firebase.CredentialsFile, cfg.Firebase.CredentialsJSON, cfg.Firebase.Timeout)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create FCM notifier: %w", err)
	}

	notificationRepo := notificationpostgres.New(db)
	notificationService := notificationusecase.NewService(notificationRepo, fcmNotifier)
	notificationHandler := notificationhttp.NewHandler(notificationService)

	// Khởi tạo River Queue & Worker
	if err := riverpkg.AutoMigrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("auto-migrate river: %w", err)
	}
	riverWorkers := river.NewWorkers()
	river.AddWorker(riverWorkers, notificationjobs.NewNotificationWorker(notificationService))

	riverClient, err := riverpkg.NewClient(db, riverWorkers, riverpkg.Config{MaxWorkers: 20})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create river client: %w", err)
	}

	notificationEnqueuer := notificationjobs.NewEnqueuer(riverClient)
	notificationService.SetEnqueuer(notificationEnqueuer)
	groupRepo := grouppostgres.New(db)
	groupService := groupusecase.NewService(groupRepo, cfg.Group.InviteBaseURL)
	groupHandler := grouphttp.NewHandler(groupService, avatarStore.URL)

	appRouter := router.New(cfg.App)
	// Đăng ký toàn bộ route trước khi server bắt đầu nhận request. Module mới
	// chỉ cần mount tại đây, không phải thêm tham số vào router.New.
	liveAuth := transportmw.Auth(tokens, authRepo)
	tokenAuth := transportmw.TokenAuth(tokens)
	appRouter.Route("/api/v1", func(api chi.Router) {
		api.Route("/auth", func(r chi.Router) { authHandler.RegisterAuthRoutes(r, tokenAuth) })
		api.Route("/users", func(r chi.Router) { authHandler.RegisterUserRoutes(r, liveAuth) })
		api.Route("/notifications", func(r chi.Router) { notificationHandler.RegisterRoutes(r, liveAuth) })
		api.Route("/groups", func(r chi.Router) { groupHandler.RegisterGroupRoutes(r, liveAuth) })
	})

	workerCtx, cancelWorkers := context.WithCancel(ctx)
	if err := riverClient.Start(workerCtx); err != nil {
		cancelWorkers()
		db.Close()
		return nil, fmt.Errorf("start river client: %w", err)
	}

	cleanupWorkers := authjobs.New(authRepo, avatarStore, cfg.Cleanup.Interval, cfg.Cleanup.Retention, cfg.Cleanup.MediaWorkerInterval)

	app := &App{
		db:            db,
		riverClient:   riverClient,
		cancelWorkers: cancelWorkers,
		server: &http.Server{
			Addr:    cfg.App.Address,
			Handler: appRouter,
		},
	}
	app.workers.Add(1)
	go func() { defer app.workers.Done(); cleanupWorkers.Run(workerCtx) }()
	return app, nil
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
// River queue, workers và database pool dùng chung; ctx giới hạn thời gian chờ quá trình này.
func (a *App) Shutdown(ctx context.Context) error {
	if err := a.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	if a.riverClient != nil {
		_ = a.riverClient.Stop(ctx)
	}
	a.cancelWorkers()
	a.workers.Wait()
	// Giữ pool hoạt động cho đến khi các handler đang xử lý request hoàn tất.
	a.db.Close()
	return nil
}
