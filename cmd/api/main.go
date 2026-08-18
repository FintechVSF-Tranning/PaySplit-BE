package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"paysplit-backend/internal/bootstrap"
)

func main() {
	// Gắn context gốc của ứng dụng với các tín hiệu mà terminal hoặc hệ thống
	// điều phối container dùng để yêu cầu ứng dụng dừng một cách an toàn.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("==================================================")
	log.Println("           Starting PaySplit Backend API          ")
	log.Println("==================================================")

	app, err := bootstrap.New(ctx)
	if err != nil {
		log.Fatalf("Fatal: bootstrap failed: %v", err)
	}

	log.Printf("[Server] PaySplit Backend is ready and listening on %s", app.Address())
	log.Println("==================================================")

	// Start sẽ chặn luồng trong khi phục vụ request, vì vậy cần chạy riêng để
	// main có thể chờ server thoát bất thường hoặc nhận tín hiệu dừng ứng dụng.
	errors := make(chan error, 1)
	go func() { errors <- app.Start() }()

	// Khi này Start đã chaỵ 1 luồng riêng, main sẽ chạy tiếp vào hàm select này
	// tồn tại để chờ server lỗi hoặc hệ thống gửi tín hiệu dừng.
	select {
	case err := <-errors:
		if err != nil {
			log.Fatalf("Fatal: server error: %v", err)
		}
	case <-ctx.Done():
		log.Println("Shutting down PaySplit Backend gracefully...")
		// Giới hạn thời gian graceful shutdown để request bị treo không thể giữ
		// tiến trình tiếp tục chạy vô hạn sau khi đã nhận yêu cầu dừng.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := app.Shutdown(shutdownCtx); err != nil {
			log.Printf("Shutdown error: %v", err)
		} else {
			log.Println("Server stopped cleanly.")
		}
	}
}
