# 0005. Admin v1

**Date**: 2026-08-17
**Status**: In Progress

## Summary

PaySplit v1 provides an administrative and system monitoring module for platform operators. The module enables operators to search and filter user accounts, inspect account profiles with masked financial details, and update account statuses with immediate session revocation. It also exposes liveness, readiness, system overview statistics, and Prometheus metrics for operational observability.

## Requirements

**User stories**:

1. As an admin, I want to search, filter, and view a paginated list of all user accounts so that I can monitor user registrations and manage user access.
2. As an admin, I want to inspect a comprehensive account profile with masked bank account numbers, active session counts, and group participation so that I can assist users and investigate policy issues without exposing raw financial credentials.
3. As an admin, I want to suspend, lock, or reactivate user accounts with mandatory reason logging so that compromised or violating accounts are immediately disconnected while preserving audit history.
4. As an operator or automated monitoring system, I want to query service health endpoints, platform business summaries, and Prometheus metrics so that I can track platform performance, background job queues, and infrastructure health in real time.

**Acceptance criteria**:

1. **AC-1**: `GET /api/v1/admin/accounts` accepts optional query parameters `page` (default 1), `limit` (default 20, maximum 100), `search` (case insensitive matching on email, display name, or phone number), `status` (`pending_verification`, `active`, `suspended`, `locked`), `role` (`user`, `admin`), `sort_by` (`created_at`, `display_name`, `email`), and `sort_order` (`asc`, `desc`). When `limit` exceeds the maximum, the system silently clamps the value to 100 and returns the applied limit in pagination metadata. It returns a paginated list containing `id`, `email`, `display_name`, `avatar_url`, `phone_number`, `role`, `status`, `email_verified_at`, `created_at`, and `updated_at`, along with pagination metadata (`total`, `page`, `limit`, `total_pages`). Authentication hashes and secret tokens are never returned. Invalid parameter types or negative values return `400 VALIDATION_FAILED`.
2. **AC-2**: `GET /api/v1/admin/accounts/{id}` validates that the path parameter `id` is a well formed UUID (returning `400 VALIDATION_FAILED` if malformed) and that the target account exists (returning `404 ACCOUNT_NOT_FOUND` if not found). It returns the full account detail: safe profile fields, `failed_login_count`, `login_blocked_until`, bank snapshot with bank account number masked showing only the last four digits (for example, `******1234`), count of active non revoked sessions, list of group memberships (`group_id`, `group_name`, `role`, `status`, `joined_at`), financial summary (`outstanding_debts_count`, `total_debt_amount_vnd`, `outstanding_credits_count`, `total_credit_amount_vnd`), and the ten most recent admin audit logs for this target user.
3. **AC-3**: `PUT /api/v1/admin/accounts/{id}/status` validates that the path parameter `id` is a well formed UUID (returning `400 VALIDATION_FAILED` if malformed) and accepts `status` (`active`, `suspended`, `locked`) and `reason` (nonempty trimmed string, required for suspension and locking). It rejects attempts by an admin to modify their own account status with `403 CANNOT_MODIFY_SELF` and attempts to suspend or lock another admin with `403 CANNOT_MODIFY_ADMIN`. Transitions from `pending_verification` return `400 INVALID_STATUS_TRANSITION`.
4. **AC-4**: When transitioning an account to `suspended` or `locked`, the database transaction updates `users.status`, records the action in `admin_audit_logs`, and immediately revokes all active sessions (`sessions.revoked_at = now()`, `sessions.revoked_reason = 'admin_' || status`) and their associated refresh tokens (`session_refresh_tokens.revoked_at = now()`). If the target user has unsettled debts or credits in active groups, the status change still succeeds, and the API response includes a warning summary (`unsettled_debts_count`, `unsettled_credits_count`).
5. **AC-5**: All administrative endpoints under `/api/v1/admin/*` enforce token authentication, active session verification (`liveAuth`), and role authorization (`RequireRole("admin")`). Unauthenticated requests return `401 AUTHENTICATION_REQUIRED`, non admin users return `403 INSUFFICIENT_PERMISSIONS`, and non active admin accounts return `401 AUTHENTICATION_REQUIRED`.
6. **AC-6**: `GET /health` and `GET /health/live` return `200` with `{ "status": "ok" }` when the HTTP server process is running. `GET /health/ready` performs an active database ping and storage connectivity check, returning `200` `{ "status": "ready", "database": "ok" }` when healthy or `503` `{ "status": "degraded", "database": "down" }` when dependencies are unavailable.
7. **AC-7**: `GET /api/v1/admin/system/overview` returns aggregated platform statistics including user counts by status, group counts, finalized and draft bill totals, settlement debt statuses, media cleanup queue depth, OCR job counts by status (`queued`, `processing`, `succeeded`, `failed`), and basic runtime metrics (`goroutines_count`, `alloc_memory_bytes`, `uptime_seconds`).
8. **AC-8**: `GET /metrics` exposes standard Prometheus text exposition format metrics including HTTP request counters, request latency histograms, database connection pool gauges, and domain level counters. The metrics endpoint can be restricted to internal network traffic or an optional authorization token.

## Decision

**Chosen option**: Build a dedicated `internal/modules/admin/` module following clean architecture and existing repository patterns. Wire admin endpoints with existing `transportmw.Auth` and `transportmw.RequireRole("admin")`. Execute account status updates and session revocations in a single PostgreSQL transaction with atomic audit logging in `admin_audit_logs`. Expose dual monitoring interfaces via `GET /health/ready`, `GET /api/v1/admin/system/overview`, and `GET /metrics`.

**Implementation skills**: `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Rationale

Reasoning and options: see [rationale.md](rationale.md).

## Feature design

### Data model sketch

The module uses the existing `users`, `sessions`, `session_refresh_tokens`, `groups`, `group_members`, `debts`, and `admin_audit_logs` tables in PostgreSQL 18.

```sql
-- Table: admin_audit_logs (already present in schema, extended with composite index)
CREATE TABLE IF NOT EXISTS admin_audit_logs (
    id              UUID PRIMARY KEY DEFAULT uuidv7(),
    admin_id        UUID NOT NULL REFERENCES users(id),
    target_user_id  UUID NOT NULL REFERENCES users(id),
    action          admin_action NOT NULL, -- enum: 'suspend','lock','reactivate'
    reason          TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (reason <> '')
);

CREATE INDEX IF NOT EXISTS idx_admin_audit_target ON admin_audit_logs(target_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_audit_admin ON admin_audit_logs(admin_id, created_at DESC);
```

### State transitions

```text
user: active → suspended (revokes sessions, logs audit)
user: active → locked (revokes sessions, logs audit)
user: suspended → active (reactivates, logs audit)
user: suspended → locked (logs audit)
user: locked → active (reactivates, logs audit)
user: locked → suspended (logs audit)
user: pending_verification → (cannot be changed by admin status endpoint; user must verify email)
```

### API surface

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| `/api/v1/admin/accounts` | GET | `page`, `limit`, `search`, `status`, `role`, `sort_by`, `sort_order` | `items: []AccountSummary`, `pagination: PaginationMeta` | Bearer Admin (live session) | `AUTHENTICATION_REQUIRED`, `INSUFFICIENT_PERMISSIONS`, `VALIDATION_FAILED` |
| `/api/v1/admin/accounts/{id}` | GET | `id: UUID` (path) | `account: AccountDetail` | Bearer Admin (live session) | `AUTHENTICATION_REQUIRED`, `INSUFFICIENT_PERMISSIONS`, `ACCOUNT_NOT_FOUND` |
| `/api/v1/admin/accounts/{id}/status` | PUT | `id: UUID` (path), `status: string`, `reason: string` | `account: SafeUser`, `warning: WarningMeta` | Bearer Admin (live session) | `AUTHENTICATION_REQUIRED`, `INSUFFICIENT_PERMISSIONS`, `ACCOUNT_NOT_FOUND`, `CANNOT_MODIFY_SELF`, `CANNOT_MODIFY_ADMIN`, `INVALID_STATUS_TRANSITION`, `VALIDATION_FAILED` |
| `/api/v1/admin/system/overview` | GET | none | `overview: SystemOverviewDTO` | Bearer Admin (live session) | `AUTHENTICATION_REQUIRED`, `INSUFFICIENT_PERMISSIONS` |
| `/health` | GET | none | `status: "ok"` | Public | none |
| `/health/live` | GET | none | `status: "ok"` | Public | none |
| `/health/ready` | GET | none | `status: "ready"`, `database: "ok"` | Public / Internal | `SERVICE_UNAVAILABLE` (503) |
| `/metrics` | GET | none | Prometheus text exposition | Internal / Config token | `AUTHENTICATION_REQUIRED` (if token set) |

### Value sourcing

| Action | Value produced / displayed | Source |
|---|---|---|
| List accounts | Account items list | Filtered, sorted, and paginated query on `users` table |
| List accounts | Pagination total and total pages | Count query on `users` table with identical filter predicates |
| Account detail | User profile and verification dates | `users` table columns (`id`, `email`, `display_name`, `phone_number`, `role`, `status`, `created_at`, etc.) |
| Account detail | Masked bank account number | String transformation `******` + last 4 characters of `default_bank_account_number` |
| Account detail | Active session count | Count of `sessions` WHERE `user_id = $1 AND revoked_at IS NULL AND expires_at > now()` |
| Account detail | Group memberships | Join between `group_members` and `groups` for `user_id = $1` |
| Account detail | Outstanding debts and credits summary | Aggregated count and sum on `debts` joined through `group_members` for `user_id = $1` |
| Account detail | Recent audit history | Last 10 records from `admin_audit_logs` WHERE `target_user_id = $1` joined with `users` for admin email |
| Update status | Session revocation timestamps | Database `now()` applied to `sessions.revoked_at` and `session_refresh_tokens.revoked_at` |
| Update status | Unsettled obligations warning | Dynamic count of debts in `awaiting`, `pending_confirmation`, or `stalled_confirmation` states |
| System overview | User and group aggregate counters | Fast count queries on `users`, `groups`, `bills`, `debts`, `ocr_jobs`, and `media_cleanup_jobs` tables |
| System overview | Runtime memory and uptime | Go `runtime.ReadMemStats`, `runtime.NumGoroutine`, and process start timestamp |
| Readiness probe | Database status | `pgxpool.Ping(ctx)` latency and error return |

### Key invariants

1. **Self protection**: An administrator can never suspend, lock, or downgrade their own account status via the admin API.
2. **Admin role protection**: An administrator cannot suspend or lock another user holding `role = 'admin'`. Role promotions and demotions are performed directly by database administrators.
3. **Atomic revocation**: Modifying an account status to `suspended` or `locked` must update `users.status`, revoke all active sessions and refresh tokens, and insert an audit log within the same database transaction.
4. **No secret leakage**: Admin responses never include `password_hash`, session token hashes, verification token hashes, or full unmasked bank account numbers.
5. **Readiness independence**: A transient failure in third party services must report degraded status on `/health/ready` while `/health/live` remains healthy to prevent orchestrator container restart loops.

### Security model

- **Authentication**: JWT Access Token carrying `user_id`, `role`, and `session_id` (`sid`), validated against active database session via `transportmw.Auth`.
- **Authorization**: `transportmw.RequireRole("admin")` restricts all `/api/v1/admin/*` routes to accounts where `role = 'admin'`.
- **Audit Logging**: Every status mutation creates an immutable record in `admin_audit_logs` capturing `admin_id`, `target_user_id`, `action`, `reason`, and `created_at`.
- **PII and Data Masking**: Bank account numbers are masked by default. Password hashes, salt values, and token secrets are excluded at the repository layer.

### Configuration required

- `METRICS_ENABLED`: boolean flag to enable Prometheus `/metrics` endpoint (default `true`).
- `METRICS_BEARER_TOKEN`: optional secret token required to scrape `/metrics` when exposed publicly.

### Critical test scenarios

- Happy path: Admin lists accounts with search and pagination, inspects detailed profile with masked bank number, and updates an active user to suspended with an audit record, verifying **AC-1**, **AC-2**, **AC-3**, **AC-4**.
- Session revocation: Suspended user attempts to refresh token or access protected resource with existing valid JWT; request is rejected with `401 AUTHENTICATION_REQUIRED`, verifying **AC-4**, **AC-5**.
- Self modification prevention: Admin attempts to update their own account status to locked; request is rejected with `403 CANNOT_MODIFY_SELF`, verifying **AC-3**, **AC-5**.
- Non admin access rejection: Normal user (role `user`) attempts to access admin endpoints; request is rejected with `403 INSUFFICIENT_PERMISSIONS`, verifying **AC-5**.
- Health and readiness check: `/health/live` returns 200, `/health/ready` returns 200 on active database pool and 503 when database pool is closed, verifying **AC-6**.
- System overview metrics: Admin retrieves system overview and validates aggregated counts matching database state, verifying **AC-7**, **AC-8**.

## Build plan

1. Create a new database migration to add the `idx_admin_audit_admin(admin_id, created_at DESC)` index on `admin_audit_logs`, and write sqlc queries in `internal/modules/admin/repository/postgres/queries/admin.sql` for account listing, filtering, detail lookups, atomic status update with session revocation, audit log insertion, and system statistics aggregation including OCR job counts, satisfies **AC-1**, **AC-2**, **AC-3**, **AC-4**, **AC-7**.
2. Implement admin repository interface and PostgreSQL adapter in `internal/modules/admin/repository/` with safe entity mapping and bank account masking, satisfies **AC-1**, **AC-2**, **AC-3**, **AC-4**.
3. Implement admin usecase service in `internal/modules/admin/usecase/service.go` enforcing self protection, admin role guards, valid state transitions, and audit logging orchestration, satisfies **AC-1**, **AC-2**, **AC-3**, **AC-4**, **AC-7**.
4. Implement HTTP handlers and DTO validation in `internal/modules/admin/delivery/http/handler.go` and register routes under `/api/v1/admin` using `liveAuth` and `RequireRole("admin")`, satisfies **AC-1**, **AC-2**, **AC-3**, **AC-4**, **AC-5**, **AC-7**.
5. Implement enhanced readiness probe in `internal/transport/http/router/router.go` and Prometheus metrics exporter integration for HTTP traffic and pool metrics, satisfies **AC-6**, **AC-8**.
6. Wire admin module dependencies in `internal/bootstrap/app.go` and write integration tests covering admin listing, detail views, status mutations, session revocations, and unauthorized access rejections, satisfies **AC-1**, **AC-2**, **AC-3**, **AC-4**, **AC-5**, **AC-6**, **AC-7**, **AC-8**.

## Consequences

**Positive**:
- Centralized administration and account moderation with strict audit trails and immediate session revocation.
- Zero credential leakage through strict repository level mapping and financial detail masking.
- Clear separation between liveness and readiness probes ensuring smooth container orchestrations and dependable alert reporting.

**Negative / tradeoffs**:
- Aggregated system overview queries perform real time counts across primary tables, which may require read replica offloading or caching if data volume grows significantly.
- Admin status updates require multi table writes and lock acquisitions across `users`, `sessions`, `session_refresh_tokens`, and `admin_audit_logs`.

**Neutral**:
- Initial admin user provisioning is managed directly in PostgreSQL via database administration.

## Follow-up

- [ ] Add Prometheus alert rules configuration documentation for operations teams.
- [ ] Evaluate caching or materialized view aggregations for system overview metrics if user base exceeds 100,000 active accounts.
