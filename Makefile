.PHONY: run build test fmt tidy sqlc migrate-up migrate-down migrate-status

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
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

migrate-status:
	go run ./cmd/migrate status
