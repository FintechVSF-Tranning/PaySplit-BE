# PaySplit Backend

Modular monolith scaffold using Go, Chi, PostgreSQL, pgxpool and sqlc.

## Start

```sh
cp .env.example .env
docker compose up -d postgres
go run ./cmd/migrate up
go run ./cmd/api
```

## Endpoints

- `GET /health`
- `POST /api/v1/auth/register`
- `POST /api/v1/auth/login`
