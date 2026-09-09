# Repository Guidelines

## Project Overview

PaySplit Backend (`PaySplit-BE`) is a RESTful API and background worker service written in Go (Go 1.26+) powering the PaySplit smart bill-splitting and group settlement ecosystem. Companion repositories include `PaySplit-FE` (Flutter mobile app) and `PaySplit-RP` (product requirements and documentation).

> **Note on Language:** In-repository code comments, error messages, domain terms, and documentation are predominantly written in Vietnamese. Code symbols, file names, commit messages, and API contracts are written in English.

---

## Project Structure & Clean Architecture

The codebase follows Clean Architecture with a feature-first modular organization under `internal/modules/`. Dependency flow strictly points inward:

```text
HTTP Request → delivery/http → usecase → repository (interface) ← repository/postgres (adapter)
                                  ↓
                                domain
```

### Directory Organization

```text
PaySplit-BE/
├── cmd/
│   └── api/main.go               # Application entry point & graceful shutdown
├── db/migrations/                # Goose database migrations (PostgreSQL)
├── docs/                         # PRD, OpenAPI 3.0 spec, architecture specs & scopes
├── internal/
│   ├── bootstrap/
│   │   └── app.go                # Central dependency injection & application wiring
│   ├── config/                   # Environment configuration loader & validation
│   ├── modules/                  # Business modules (Clean Architecture per module)
│   │   ├── admin/                # System administration, telemetry & analytics
│   │   ├── auth/                 # Identity, sessions, OTP, password reset
│   │   ├── bill/                 # Bill creation, itemization, OCR receipt integration
│   │   ├── group/                # Group management, members, invites & timeline
│   │   ├── notification/         # In-app notifications & Firebase push (FCM)
│   │   └── settlement/           # Debt calculation, expense splitting & VietQR payments
│   ├── platform/                 # Shared infrastructure adapters
│   │   ├── banks/                # Embedded VietQR bank directory
│   │   ├── database/             # PostgreSQL connection pool (pgxpool)
│   │   ├── email/gmail/          # SMTP email adapter
│   │   ├── image/                # Avatar & receipt image processing (EXIF, WebP)
│   │   ├── metrics/              # Prometheus telemetry metrics
│   │   ├── notification/fcm/     # Firebase Cloud Messaging adapter
│   │   ├── ocr/llamaextract/     # Receipt OCR parser adapter
│   │   ├── queue/river/          # River Queue transactional job processor
│   │   ├── redis/                # Redis connection pool (session store)
│   │   ├── session/              # Opaque session store on Redis (Lua scripts)
│   │   ├── security/password/    # Bcrypt password hashing
│   │   ├── storage/cloudinary/   # Cloudinary asset storage
│   │   └── vietqr/               # Dynamic VietQR payment payload generator
│   └── transport/http/           # Cross-cutting HTTP transport layer
│       ├── helpers/              # JSON response helpers, error formatting, pagination
│       ├── middleware/           # Auth, session, CORS, rate limiting, 15s timeout
│       └── router/               # Chi root router, public health check (/health)
├── Makefile                      # Standard project development and build tasks
├── sqlc.yaml                     # SQL-to-Go generation configuration
└── docker-compose.yaml           # Local PostgreSQL 18 + Redis 8 services
```

### Layer Responsibilities & Isolation Rules

- **`domain/`**: Contains pure business entities and domain error sentinels (`ErrNotFound`, `ErrInvalidInput`, etc.). Must **never** import external libraries, HTTP packages, or database drivers.
- **`usecase/`**: Implements application business logic. Declares input/output interfaces (ports) and receives implementations via constructor injection. Must **never** import `pgx`, `chi`, or `net/http`.
- **`repository/`**: Defines the data-access port (`repository.go`).
- **`repository/postgres/`**: Implements repository ports using PostgreSQL. Hand-written queries live in `queries/*.sql`. Typed models and query functions in `sqlc/` are auto-generated via `make sqlc`. **Never manually edit files in `sqlc/`**.
- **`delivery/http/`**: Contains Chi HTTP handlers, request/response DTOs, route mounting, input validation, and HTTP status code mappings.
- **`internal/bootstrap/app.go`**: The **sole location** where concrete implementations are instantiated and wired together (config → database pool → platform adapters → modules → router → server). Never scatter dependency wiring across other packages.

---

## Build, Test, and Development Commands

Local environment variables are managed via `.env` (copied from `.env.example`). The `Makefile` includes and exports `.env` automatically.

```bash
# Infrastructure
docker compose up -d postgres redis # Start local PostgreSQL 18 + Redis 8 containers
cp .env.example .env          # Prepare local environment variables

# Running & Building
make run                      # Run API server (go run ./cmd/api)
make build                    # Compile binary to bin/paysplit-api
make tidy                     # Tidy Go dependencies (go mod tidy)
make fmt                      # Format Go source code (gofmt -w ./cmd ./internal)

# Database & Code Generation
make sqlc                     # Regenerate typed Go code from queries/ and db/migrations/
make migrate-up               # Apply all pending Goose migrations
make migrate-down             # Roll back the latest migration
make migrate-status           # Show current migration version status
make goose-install            # Install goose CLI tool

# Testing
make test                     # Run all unit tests (go test ./...)
go test ./internal/config/... # Run tests for a specific package
go test -run TestLoad ./internal/config # Run a specific unit test
```

> **Integration Tests:** Repository- and handler-level integration tests (`*_integration_test.go`) connect to real infrastructure and are automatically skipped unless `TEST_DATABASE_URL` (and, for session/auth tests, `TEST_REDIS_URL`) is configured. A green `make test` with those unset means the integration suite never ran — set both in `.env`, which the Makefile exports.

---

## Database & Migration Workflow

1. **Migration Files**: Stored in `db/migrations/` using format `NNNNNN_<description>.sql`.
   - Each version must be a **single file** containing both `-- +goose Up` and `-- +goose Down` blocks.
   - Do not split into separate `.up.sql` and `.down.sql` files (Goose treats them as duplicate versions).
2. **Query Files**: SQL queries live in `internal/modules/<module>/repository/postgres/queries/*.sql`.
3. **Code Generation**: Run `make sqlc` whenever schemas or SQL queries are modified.
4. **Application**: Run `make migrate-up` to apply migrations against `DATABASE_URL`.

---

## Coding Style & Naming Conventions

- **Formatting**: Strictly follow standard Go conventions (`gofmt -w ./cmd ./internal`). Run `make fmt` before committing.
- **Context Handling**: Pass `ctx context.Context` as the first parameter to all handlers, usecases, and repository methods.
- **Constructors**: Export constructor functions using `New(...)` or `New<Type>(...)` and return interfaces where appropriate.
- **Error Handling**: Use sentinel domain errors defined in `domain/errors.go` (e.g., `var ErrUserNotFound = errors.New(...)`). Wrap lower-level errors using `fmt.Errorf("action context: %w", err)`.
- **Response Format**: Use helper functions in `internal/transport/http/helpers/` for consistent API envelopes:
  - Success: `{"success": true, "data": { ... }, "message": "Thành công"}`
  - Failure: `{"success": false, "error": {"code": "CODE", "message": "...", "details": { ... }}}`
- **Timeouts**: All incoming HTTP requests are bounded by a 15-second timeout middleware.

---

## Security & Authentication Details

- **Credential**: A single opaque session ID (32 bytes CSPRNG, base64url) issued by `POST /api/v1/auth/sign-in` and sent as `Authorization: Bearer <session_id>`. It is **not a JWT**: it carries no claims and only the server can resolve it. There is no access/refresh pair and no refresh endpoint.
- **Session Store**: Redis is the sole authority on whether a credential is live (`internal/platform/session`). Keys are the SHA-256 **hash** of the credential, so a Redis dump cannot be replayed. Two keys per session: `session:<hash>` (HASH holding `sid`, `user_id`, `role`, `absolute_exp`) and `user_session:<user_id>` (reverse index used to revoke by user).
- **Session lifetime**: sliding idle TTL (`SESSION_IDLE_TTL_HOURS`, default 7 days) refreshed on every authenticated request, capped by a hard absolute TTL (`SESSION_ABSOLUTE_TTL_HOURS`, default 30 days). All multi-key operations run as Lua scripts so they are atomic.
- **Session record**: the Postgres `sessions` table is kept as an **audit/metadata record only** — device, issue time, revocation reason. Nothing reads it to decide whether a request is authorised. `uq_sessions_one_active_per_user` still enforces one live session per user.
- **Revocation**: instant via `DEL`. Ordering between Redis and Postgres differs per flow and is documented at each call site — sign-out writes Redis first (fails closed), while sign-in, password reset and admin suspension commit Postgres first. Password reset must not touch Redis before the OTP is verified, or a wrong OTP becomes an unauthenticated logout DoS.
- **Force logout**: revocation emits `session.ended` over `pg_notify` inside the same transaction, closing any open SSE stream (`internal/platform/realtime`).
- **Suspension backstop**: the auth middleware no longer reads `users.status`, so a `DEL` on Redis is the only thing that enforces an admin lock. Every admin revocation also enqueues a `session_redis_purge` River job inside the same transaction (`internal/modules/auth/jobs/session_purge.go`) so a dropped Redis write converges instead of leaving a locked account usable.
- **Passwords**: Hashed with `bcrypt` at cost 12.
- **Push tokens**: FCM registration tokens live in `device_tokens` keyed by `(user_id, device_id)`, not on the session row — they describe a device and must outlive the session.
- **Background Cleanup**: Expired session audit rows and unverified OTPs are pruned periodically by background workers in `internal/modules/auth/jobs/`. Live sessions are cleaned up by Redis TTL, not by a worker.

---

## Background Jobs & Worker Queue

- **Engine**: [River Queue](https://github.com/riverqueue/river) running transactional jobs on PostgreSQL (`pgx/v5`).
- **Job Workers**: Module-specific job handlers reside under `internal/modules/<module>/jobs/` (e.g., notification pushes, debt settlements, media cleanup).
- **Graceful Shutdown**: All workers listen to `context.Context` cancellation during application shutdown managed by `internal/bootstrap/app.go`.

---

## API Contracts & Specifications

- **REST API Contract**: Fully specified in [`docs/openapi.yaml`](docs/openapi.yaml). Any change to routes, request payloads, or response DTOs must be reflected here.
- **Design Decisions & Specs**: Detailed architectural specs live in `docs/specs/` (e.g., `docs/specs/0001-auth-account-v1/`).
- **Feature Scope**: Tracked in `docs/scope/scope.md`.

---

## Commit & Pull Request Guidelines

- **Commit Style**: Use Conventional Commits (`feat:`, `fix:`, `chore:`, `docs:`, `refactor:`, `test:`).
- **Scope & Clarity**: Keep commit messages concise, imperative, and focused on a single logical change.
- **Verification**: Ensure `make test`, `make fmt`, and `make sqlc` pass cleanly prior to opening PRs. Never commit unformatted code, `.env` files, or secrets.
