package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"paysplit-backend/internal/config"
	adminhttp "paysplit-backend/internal/modules/admin/delivery/http"
	adminpostgres "paysplit-backend/internal/modules/admin/repository/postgres"
	adminusecase "paysplit-backend/internal/modules/admin/usecase"
	authhttp "paysplit-backend/internal/modules/auth/delivery/http"
	authjobs "paysplit-backend/internal/modules/auth/jobs"
	authpostgres "paysplit-backend/internal/modules/auth/repository/postgres"
	authusecase "paysplit-backend/internal/modules/auth/usecase"
	grouphttp "paysplit-backend/internal/modules/group/delivery/http"
	grouppostgres "paysplit-backend/internal/modules/group/repository/postgres"
	groupusecase "paysplit-backend/internal/modules/group/usecase"
	notificationhttp "paysplit-backend/internal/modules/notification/delivery/http"
	notificationjobs "paysplit-backend/internal/modules/notification/jobs"
	notificationpostgres "paysplit-backend/internal/modules/notification/repository/postgres"
	notificationusecase "paysplit-backend/internal/modules/notification/usecase"
	"paysplit-backend/internal/platform/auth/jwt"
	"paysplit-backend/internal/platform/banks"
	"paysplit-backend/internal/platform/database"
	"paysplit-backend/internal/platform/email/gmail"
	avatarimage "paysplit-backend/internal/platform/image/avatar"
	platformmetrics "paysplit-backend/internal/platform/metrics"
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

// New khởi tạo ứng dụng API bằng cách:
// 1. Tải cấu hình môi trường (.env / environment variables)
// 2. Mở kết nối Database PostgreSQL pool
// 3. Khởi tạo các adapter hạ tầng dùng chung (JWT, Banks, Email, Avatar Storage & Processing)
// 4. Khởi tạo các module nghiệp vụ (Auth, Notification, Group, Admin)
// 5. Cấu hình River Queue (Background Job Worker) và router HTTP server
func New(ctx context.Context) (*App, error) {
	// 1. Nạp và kiểm tra cấu hình runtime (.env)
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	log.Printf("[Config] Loaded environment: %s (host: %s, port: %s)", cfg.App.Environment, cfg.App.Host, cfg.App.Port)

	// 2. Mở pool kết nối PostgreSQL dùng chung cho toàn bộ ứng dụng
	db, err := database.NewPostgresPool(ctx, cfg.Database)
	if err != nil {
		return nil, err
	}
	log.Printf("[Database] Connected to PostgreSQL pool (max_conns: %d, min_conns: %d)", cfg.Database.MaxConns, cfg.Database.MinConns)

	// 3. Khởi tạo các adapter hạ tầng (Platform Layer)
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
	log.Printf("[Banks] Bank directory loaded (%d banks loaded)", len(bankDirectory.All()))

	mailer, err := gmail.New(cfg.SMTP, cfg.Auth.EmailVerificationURL, cfg.Auth.PasswordResetURL)
	if err != nil {
		db.Close()
		return nil, err
	}
	log.Printf("[SMTP] Gmail mailer initialized (sender: %s, host: %s:%d)", cfg.SMTP.Username, cfg.SMTP.Host, cfg.SMTP.Port)

	avatarStore, err := avatarstorage.New(cfg.Cloudinary, cfg.Avatar.UploadTimeout)
	if err != nil {
		db.Close()
		return nil, err
	}
	log.Printf("[Storage] Cloudinary avatar storage initialized (cloud: %s)", cfg.Cloudinary.CloudName)

	imageProcessor := avatarimage.NewProcessor(cfg.Avatar.ProcessingTimeout, cfg.Avatar.MaxConcurrentConversions)

	// 4. Khởi tạo Module Auth
	authService := authusecase.NewService(authRepo, password.New(), tokens, mailer, bankDirectory, imageProcessor, avatarStore, authusecase.Options{VerificationTTL: cfg.Auth.EmailVerificationTTL, ResetTTL: cfg.Auth.PasswordResetTTL, SessionTTL: cfg.Auth.RefreshTokenTTL})
	authHandler := authhttp.NewHandler(authService, avatarStore.URL)

	// 5. Khởi tạo Firebase Cloud Messaging (FCM)
	// fcm.New trả về (*fcm.Notifier)(nil) khi FCM bị tắt (thiếu credentials file). Phải kiểm tra bằng
	// so sánh con trỏ cụ thể TRƯỚC khi gán vào các tham số kiểu interface bên dưới, vì một con
	// trỏ nil được đặt vào interface sẽ tạo ra "typed nil" khiến các so sánh `== nil` sau đó trên
	// interface luôn trả về false, làm ẩn đi các nhánh xử lý khi FCM bị tắt.
	fcmClient, err := fcm.New(ctx, cfg.Firebase.CredentialsFile, cfg.Firebase.CredentialsJSON, cfg.Firebase.Timeout)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create FCM notifier: %w", err)
	}
	fcmEnabled := fcmClient != nil
	if fcmEnabled {
		log.Println("[FCM] Firebase Cloud Messaging initialized and enabled")
	} else {
		log.Println("[FCM] Firebase Cloud Messaging is disabled (no credentials provided)")
	}

	notificationRepo := notificationpostgres.New(db)

	// 6. Khởi tạo River Queue & Background Workers
	// Tự động migrate bảng `river_job` nếu chưa có trên PostgreSQL
	if err := riverpkg.AutoMigrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("auto-migrate river: %w", err)
	}
	log.Println("[Queue] River queue database tables verified/migrated")

	// Đăng ký Worker xử lý job gửi thông báo vào bundle `riverWorkers`.
	// Lưu ý: River Queue bắt buộc bundle phải có ít nhất 1 worker được đăng ký trước khi start.
	// NotificationWorker đã được thiết kế an toàn khi pushNotifier = nil (tự động bỏ qua gửi push nếu FCM tắt),
	// do đó worker luôn được đăng ký để server khởi động bình thường ở cả môi trường có hoặc không có Firebase.
	riverWorkers := river.NewWorkers()
	var fcmNotifier notificationusecase.PushNotifier
	var notificationJobNotifier notificationjobs.PushNotifier
	if fcmEnabled {
		fcmNotifier = fcmClient
		notificationJobNotifier = fcmClient
	}
	river.AddWorker(riverWorkers, notificationjobs.NewNotificationWorker(notificationRepo, notificationJobNotifier))

	riverClient, err := riverpkg.NewClient(db, riverWorkers, riverpkg.Config{MaxWorkers: cfg.River.WorkerCount, FetchCooldown: cfg.River.FetchCooldown})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create river client: %w", err)
	}

	// 7. Khởi tạo Module Notification
	notificationEnqueuer := notificationjobs.NewEnqueuer(riverClient)
	notificationService := notificationusecase.NewService(notificationRepo, fcmNotifier, notificationEnqueuer)
	notificationHandler := notificationhttp.NewHandler(notificationService)

	// 8. Khởi tạo Module Group
	groupRepo := grouppostgres.New(db)
	groupService := groupusecase.NewService(groupRepo, cfg.Group.InviteBaseURL)
	groupHandler := grouphttp.NewHandler(groupService, avatarStore.URL)

	// 9. Khởi tạo Module Admin & Bank Directory Handler
	adminRepo := adminpostgres.New(db)
	adminService := adminusecase.NewService(adminRepo)
	adminHandler := adminhttp.NewHandler(adminService, avatarStore.URL)
	bankHandler := banks.NewHandler(bankDirectory)

	platformmetrics.RegisterDBPool(db)

	// 10. Xây dựng Router và đăng ký tất cả các Endpoint API
	appRouter := router.New(cfg.App, cfg.Metrics, db)
	liveAuth := transportmw.Auth(tokens, authRepo)
	tokenAuth := transportmw.TokenAuth(tokens)
	appRouter.Route("/api/v1", func(api chi.Router) {
		api.Route("/auth", func(r chi.Router) { authHandler.RegisterAuthRoutes(r, tokenAuth) })
		api.Route("/users", func(r chi.Router) { authHandler.RegisterUserRoutes(r, liveAuth) })
		api.Route("/notifications", func(r chi.Router) { notificationHandler.RegisterRoutes(r, liveAuth) })
		api.Route("/groups", func(r chi.Router) { groupHandler.RegisterGroupRoutes(r, liveAuth) })
		api.Route("/admin", func(r chi.Router) { adminHandler.RegisterRoutes(r, liveAuth) })
		api.Route("/banks", func(r chi.Router) { bankHandler.RegisterRoutes(r) })
	})
	log.Println("[HTTP] API routes registered (/api/v1: auth, users, notifications, groups, admin, banks)")

	// 11. Khởi chạy River Queue Worker Engine
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	if err := riverClient.Start(workerCtx); err != nil {
		cancelWorkers()
		db.Close()
		return nil, fmt.Errorf("start river client: %w", err)
	}
	log.Printf("[Queue] River queue worker engine started (%d workers)", cfg.River.WorkerCount)

	// 12. Khởi chạy tác vụ dọn dẹp định kỳ (Media Cleanup & Auth Cleanup)
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
	log.Printf("[Workers] Periodic cleanup workers started (interval: %v, media_interval: %v)", cfg.Cleanup.Interval, cfg.Cleanup.MediaWorkerInterval)

	return app, nil
}

// Address trả về địa chỉ mạng đã cấu hình cho HTTP server (lấy từ biến HTTP_ADDRESS trong cấu hình).
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
// Việc dừng River/workers/pool luôn chạy, kể cả khi HTTP server drain quá thời hạn ctx, để
// không rò rỉ tài nguyên đúng vào lúc quá trình tắt orderly quan trọng nhất.
func (a *App) Shutdown(ctx context.Context) error {
	httpErr := a.server.Shutdown(ctx)
	if a.riverClient != nil {
		_ = a.riverClient.Stop(ctx)
	}
	a.cancelWorkers()
	a.workers.Wait()
	// Giữ pool hoạt động cho đến khi các handler đang xử lý request hoàn tất.
	a.db.Close()
	if httpErr != nil {
		return fmt.Errorf("shutdown HTTP server: %w", httpErr)
	}
	return nil
}
