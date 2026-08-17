# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

PaySplit backend API (Go) — a bill-splitting app backend. Companion repos: `PaySplit-FE` (Flutter app), `PaySplit-RP` (product docs). Most in-repo docs and comments are written in Vietnamese.

## Commands

```bash
make run              # go run ./cmd/api
make build             # build binary to bin/paysplit-api
make test              # go test ./...
make fmt                # gofmt -w ./cmd ./internal
make tidy               # go mod tidy
make sqlc               # regenerate sqlc code from queries/ + db/migrations/
make migrate-up          # apply pending migrations (goose)
make migrate-down        # roll back the last migration
make migrate-status      # show migration status
make goose-install       # install goose CLI if missing

go test ./internal/config/...          # test a single package
go test -run TestLoad ./internal/config  # run a single test
```

`docker compose up -d postgres` starts local PostgreSQL 18. Copy `.env.example` to `.env` first — the Makefile does `include .env` and exports it, so `DATABASE_URL` etc. must be set there for `make` targets to work.

Repository/handler-level integration tests (`*_integration_test.go`) are skipped automatically unless `TEST_DATABASE_URL` is set in the environment.

## Architecture

Clean architecture, organized by business module under `internal/modules/`. Dependencies point inward:

```
delivery/http  →  usecase  →  repository (interface)  →  repository/postgres (adapter)
                     ↓
                  domain
```

- `domain/` — plain entities and business errors only. No imports from other layers.
- `usecase/` — application services. Defines the interfaces it needs (e.g. `repository.Repository`, `PasswordManager`, `TokenIssuer`) and receives implementations via constructor injection. Never imports `pgx`, `chi`, or `net/http`.
- `repository/repository.go` is the port (interface); `repository/postgres/` is the adapter, translating between sqlc-generated models and domain entities.
- `repository/postgres/queries/*.sql` — hand-written SQL owned by the module. Run `make sqlc` after editing to regenerate `repository/postgres/sqlc/` — **never hand-edit files under `sqlc/`**, they are overwritten on every generation.
- `delivery/http/` — chi handlers, request/response DTOs, and route registration for the module.
- `internal/bootstrap/app.go` is the **only** place concrete implementations are wired together (config → DB pool → module dependencies → router → HTTP server). Adding a new module means constructing it here, not scattering wiring elsewhere.
- `internal/platform/` — shared infrastructure adapters: `database` (pgx pool + health check), `auth/jwt` (access token issuance/verification), `banks` (embedded VietQR bank directory), `email/gmail` (SMTP), `image/avatar` (EXIF/resize/WebP), `storage/cloudinary`, `security/password` (bcrypt).
- `internal/transport/http/` — cross-module HTTP concerns: `router/` (chi root router + `/health`), `middleware/` (auth, CORS, rate limiting, request timeout), `helpers/` (JSON I/O, error formatting, pagination).

To add a new module, mirror the `auth` module's layout (`domain/`, `usecase/`, `repository/` + `repository/postgres/`, `delivery/http/`) and wire it in `internal/bootstrap/app.go`. See [docs/project-structure.md](docs/project-structure.md) for the full annotated tree.

### Database workflow

1. Add a migration in `db/migrations/`, named `NNNNNN_description.sql`. Each version is a **single file** containing both `-- +goose Up` and `-- +goose Down` sections — do not split into separate `.up.sql`/`.down.sql` files, goose treats those as duplicate versions.
2. Write/update queries in the owning module's `repository/postgres/queries/*.sql`.
3. `make sqlc` to regenerate typed Go code.
4. `make migrate-up`.

### Auth specifics

Access JWT (15 min TTL, carries `sid`) + PostgreSQL-backed sessions, one active session per user, refresh token rotation (7-day absolute TTL) with reuse detection, bcrypt passwords, Cloudinary-hosted WebP avatars, and background cleanup workers (`internal/modules/auth/jobs/workers.go`). See [docs/auth-module.md](docs/auth-module.md) for the full flow and extension points, and [docs/specs/0001-auth-account-v1](docs/specs/0001-auth-account-v1) for the locked-in design decisions.

API contract lives in [docs/openapi.yaml](docs/openapi.yaml). Unmatched routes return JSON 404; wrong method returns JSON 405. All requests are bounded by a 15s timeout middleware.

## Skills

This repo uses the `.claude/skills` / `.agents/skills` workflow skills (architect, audit, check, debug, develop, document, scope, sync, test) — see [docs/scope/scope.md](docs/scope/scope.md) for current feature scope and [docs/specs/](docs/specs/) for locked design decisions per feature.
