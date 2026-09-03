# PaySplit-BE

Go 1.26 HTTP API (`paysplit-backend`). Entry: `cmd/api` → `internal/bootstrap/app.go` (the only wiring site). Companion apps: `PaySplit-FE` (Flutter), `PaySplit-RP` (product docs).

Locked design lives in `docs/specs/`. Public contract: `docs/openapi.yaml`. Feature status: `docs/scope/scope.md`. Module write-ups: `docs/documents/`. Do not contradict a spec without updating it.

## Commands

```bash
cp .env.example .env          # required: Makefile does `include .env` and will fail without it
docker compose up -d postgres # Postgres 18 on host port 5433, not 5432
make migrate-up               # goose against DATABASE_URL
make run                      # go run ./cmd/api
make test                     # go test ./... (exports .env)
make sqlc                     # after editing queries/*.sql or adding a sqlc.yaml block
make fmt                      # gofmt -w ./cmd ./internal  (no golangci-lint / no CI)

go test ./internal/modules/bill/usecase/...
go test -run TestLoad ./internal/config
```

`make goose-install` if `goose` is missing. No other task runner.

## Env and startup

- `config.Load` reads `.env` via godotenv; existing process env wins. Startup `Validate()` requires JWT, Gmail SMTP, Cloudinary, HTTPS `APP_INVITE_BASE_URL`, and **exact** auth TTLs: access 15m, refresh 168h, email/reset 10m. Changing those values will not boot.
- FCM is optional (`FIREBASE_CREDENTIALS_*` empty → disabled). `fcm.New` returns a nil `*Notifier`; check the concrete pointer before assigning to an interface (typed-nil trap is documented in `bootstrap/app.go`).
- `PORT` overrides `HTTP_PORT`. `HTTP_ADDRESS` overrides host:port.
- River tables are auto-migrated at process start (`riverpkg.AutoMigrate`), not by goose. Register every worker **before** `river.NewClient`. `RIVER_WORKER_COUNT` must be `< DB_MAX_CONNS`.

## Architecture

Modules under `internal/modules/{auth,group,notification,bill,admin,settlement}`:

```
delivery/http → usecase → repository (port) → repository/postgres (adapter)
                    ↓
                 domain
```

- `domain`: entities + business errors only. No pgx/chi/http/sqlc.
- `usecase`: interfaces + constructor injection. Never import `pgx`, `chi`, or `net/http`.
- `repository/postgres/queries/*.sql` are hand-written. Generated code is `repository/postgres/sqlc/` — **never edit**. After a new module, add a **separate** block in `sqlc.yaml` (do not share `out` packages).
- Shared infra: `internal/platform/`. Cross-module HTTP: `internal/transport/http/`.
- New module: copy `auth` layout, add sqlc block, migrate, then construct + mount in `bootstrap/app.go` under `/api/v1`.

## Database

- Goose, one file per version: `db/migrations/NNNNNN_description.sql` with both `-- +goose Up` and `-- +goose Down`. Do **not** split into `.up.sql`/`.down.sql` (goose treats them as duplicate versions). `000001_init_schema.up.sql` is a historical exception; it still contains both sections.
- Never edit an already-applied migration. Concurrent index changes use `-- +goose NO TRANSACTION`.
- PKs: PostgreSQL 18 `uuidv7()`. Money: `BIGINT` / Go `int64` VND, no decimals.
- Group-scoped writes take `database.LockActiveGroup` (or `Nowait`) **before** other row locks. Settlement/bill-void lock order: group → debts by UUID → payment.
- sqlc `schema` is `./db/migrations` (goose Up sections).

## HTTP

- Prefix `/api/v1`. Success envelope: `{"success":true,"data":...,"message":"..."}` via `helpers.WriteJSON`. Errors: `helpers.WriteAPIError`. Health/metrics use `WriteRawJSON`.
- `ReadJSON` rejects unknown fields; body cap is 64 KiB (multipart uploads are separate).
- `middleware.Auth` = JWT + live session. `middleware.TokenAuth` = JWT only (sign-out). Use `middleware.UserID` / `UserRole`.
- Global timeout 15s; paths ending in `/events` are exempt (route suffix only, not `Accept`). Invite preview/join uses a separate account+IP limiter (`HTTP_INVITE_ATTEMPTS_PER_MINUTE`), not the global IP limiter.
- Rate limit keys use the TCP remote addr (`ClientIPFromRemoteAddr`), not forwarding headers.
- 404/405 are JSON. Admin UI is embedded at `/admin-portal/` from `web/admin`.

## Tests

- `*_integration_test.go` skip unless `TEST_DATABASE_URL` is set. `.env.example` points at `paysplit_test` on 5433 — compose only creates `paysplit`; create/migrate the test DB yourself.
- `make test` exports `.env`, so a set `TEST_DATABASE_URL` **runs** those tests (they will fail if that DB is missing). `go test ./...` without the env var skips them.
- LlamaExtract / Cloudinary live tests skip without real credentials; they `godotenv.Load` the repo `.env` themselves.
- Receipt fixtures under `testdata/bills/` are gitignored (personal data). Keep `service-account*.json` out of git.

## Do not

- Log proof URLs, VietQR refs, bank account numbers, or transfer notes.
- Hand-edit `sqlc/` output.
- Wire implementations outside `internal/bootstrap/app.go`.
- Trust `X-Forwarded-For` for rate limits.
- Bypass request timeout with `Accept: text/event-stream`.
