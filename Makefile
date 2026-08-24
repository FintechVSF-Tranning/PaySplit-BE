.PHONY: run build test fmt tidy sqlc goose-install migrate-up migrate-down migrate-status

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

goose-install:
	go install github.com/pressly/goose/v3/cmd/goose@latest

migrate-up:
	goose -dir db/migrations postgres "$(DATABASE_URL)" up

migrate-down:
	goose -dir db/migrations postgres "$(DATABASE_URL)" down

migrate-status:
	goose -dir db/migrations postgres "$(DATABASE_URL)" status
