.PHONY: run build test fmt tidy sqlc migrate-up migrate-down migrate-status

include .env #Tạo file .env để lưu trữ các biến môi trường, ví dụ DATABASE_URL, và sử dụng chúng trong Makefile.
export

run:
	go run ./cmd/api

build:
	go build -o bin/paysplit-api ./cmd/api

test:
	go test ./...

fmt:
	gofmt -w ./cmd ./internal

tidy:
	go mod tidy

sqlc:
	sqlc generate

migrate-up:
	migrate -path db/migrations -database "$(DATABASE_URL)" -verbose up

migrate-down:
	migrate -path db/migrations -database "$(DATABASE_URL)" -verbose down

migrate-status:
	migrate -path db/migrations -database "$(DATABASE_URL)" version
