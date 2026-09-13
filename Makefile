.PHONY: run build test test-integration fmt tidy sqlc goose-install migrate-up migrate-down migrate-status

include .env #Tạo file .env để lưu trữ các biến môi trường, ví dụ DATABASE_URL, và sử dụng chúng trong Makefile.
export

run:
	go run ./cmd/api

build:
	go build -o bin/paysplit-api ./cmd/api

test:
	go test ./...

# test-integration chay dung bo test ma `make test` co the da bo qua trong im lang.
#
# Cac file *_integration_test.go goi t.Skip khi thieu TEST_DATABASE_URL hoac
# TEST_REDIS_URL, va `go test` bao mot package toan skip la "ok". Mot lan
# `make test` xanh voi hai bien nay chua duoc dat KHONG chung minh dieu gi ve
# Postgres hay Redis. Target nay dung han neu thieu bien, thay vi de ban tin nham.
test-integration:
	@test -n "$(TEST_DATABASE_URL)" || { echo "TEST_DATABASE_URL chua duoc dat trong .env — integration test se bi bo qua trong im lang"; exit 1; }
	@test -n "$(TEST_REDIS_URL)" || { echo "TEST_REDIS_URL chua duoc dat trong .env — test cua kho phien Redis se bi bo qua trong im lang"; exit 1; }
	go test -count=1 ./...

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
