# Review, namplh/admin-module, 2026-08-18

**Reviewed by**: Claude Sonnet 5 (author on Claude Sonnet 5)
**Scope**: 28 files, branch vs `main` (scoped to Admin v1 / spec 0005 surface only — `internal/modules/admin`, `internal/platform/metrics`, `internal/transport/http/router`, `internal/bootstrap`, `docs/specs/0005-admin-v1`, `db/migrations/000004_admin_v1.sql`)
**Verdict**: Changes requested

## Summary

The Admin v1 module (account listing, account detail, status mutation with atomic session revocation, system overview, health probes, Prometheus metrics) is well structured and mostly follows the repo's clean-architecture conventions. Input validation, UUID checks, and the previously-fixed `admin_action` enum mapping bug are all solid, and the enum/status mapping now has good regression coverage via `repository_integration_test.go`. Two fresh correctness bugs remain: reactivating an account with no `reason` (explicitly allowed by AC-3) will 500 the whole request because of a `CHECK (reason <> '')` constraint on `admin_audit_logs` that the usecase layer doesn't account for, and the system-overview media-cleanup counter counts every historical job instead of the pending queue depth it's named for and documented to report. There's also an architecture concern: the self-protection / admin-protection / status-transition invariants live in the Postgres adapter rather than the usecase, which the build plan called for, and as a result those invariants have zero coverage at the usecase/mock level.

## Blockers

### 🔴 Reactivating an account without a reason causes a 500 and rolls back the whole transaction, `internal/modules/admin/usecase/service.go:157`
**Problem**: `UpdateAccountStatus` only requires a non-empty `reason` when `status` is `suspended` or `locked` (line 158: `if (status == "suspended" || status == "locked") && reason == ""`). For `status: "active"` (reactivate), an empty/omitted `reason` is passed straight through to `repository.UpdateStatusInput.Reason` and from there into `CreateAdminAuditLog` (`internal/modules/admin/repository/postgres/repository.go:294-301`). But `admin_audit_logs.reason` is `NOT NULL` **and** has `CHECK (reason <> '')` (`db/migrations/000001_init_schema.up.sql:427-429`). Inserting an empty reason violates that constraint, the whole transaction rolls back (status update + session revocation none needed for reactivate, but also the audit log), and the handler falls through `writeDomainError`'s default case to `500 INTERNAL_ERROR`.
**Why it matters**: AC-3 explicitly frames `reason` as "required for suspension and locking" only, meaning a bare `PUT /api/v1/admin/accounts/{id}/status` with `{"status":"active"}` is a documented-valid request. As written it always 500s instead of reactivating the account, for a case admins will hit routinely (undoing a suspension without re-typing the original reason, or a client that omits the field for `active`). None of the existing tests catch this because every reactivate case in `repository_integration_test.go` and `service_test.go` supplies a non-empty reason (e.g. `"restored"`).
**Suggested fix**: Either require a non-empty reason unconditionally in the usecase (reject with `ErrReasonRequired` if empty regardless of target status, matching the DB constraint), or supply a default reason string (e.g. `"reactivated by admin"`) when reactivating with no reason supplied. Add a test that reactivates with an empty/omitted reason and asserts the actual behavior.

## Major

### 🟠 System overview "media cleanup" counter reports all-time job count, not queue depth, `internal/modules/admin/repository/postgres/queries/admin.sql:162-163`
**Problem**: `GetSystemMediaCleanupOverview` is `SELECT count(*)::bigint AS pending_cleanup_jobs FROM media_cleanup_jobs;` with no `WHERE` clause. `media_cleanup_jobs` has a `completed_at` column (NULL while pending, set once processed — see `db/migrations/000001_init_schema.up.sql:134-151`), but the query counts every row ever inserted, completed or not.
**Why it matters**: AC-7 calls this "media cleanup queue depth" and the spec's Value Sourcing table lists it as a queue-depth metric; the DTO field is literally `pending_jobs_count`. As implemented the number only grows and never reflects actual backlog, making it useless (and actively misleading) for the operational monitoring this endpoint exists to provide.
**Suggested fix**: Filter to `WHERE completed_at IS NULL` (optionally also excluding permanently-failed jobs past `attempt_count` threshold if that's the intended "actionable backlog" semantics — check against the cleanup worker's own definition of "still pending").

### 🟠 Core status-change invariants live in the Postgres adapter, not the usecase, and are consequently untested at the usecase/mock level, `internal/modules/admin/repository/postgres/repository.go:251-267` vs `internal/modules/admin/usecase/service.go:144-168`
**Problem**: Self-protection (`CANNOT_MODIFY_SELF`), admin-protection (`CANNOT_MODIFY_ADMIN`), and the `pending_verification` transition guard are all implemented inside `postgresRepository.UpdateAccountStatusWithRevocation`, reading `targetUser.Role`/`targetUser.Status` fetched inside the same DB transaction. `usecase/service.go`'s `UpdateAccountStatus` only validates UUID shape, the `status` enum, and reason presence — none of the domain invariants the build plan assigned to it ("Implement admin usecase service ... enforcing self protection, admin role guards, valid state transitions", spec 0005 Build plan step 3).
**Why it matters**: Two consequences: (1) it violates the module's own architecture convention (`repository/postgres` is supposed to be a translation adapter, not where business rules live — see CLAUDE.md's "repository/repository.go is the port; repository/postgres/ is the adapter"), so a future alternate `Repository` implementation (or a refactor that swaps `UpdateAccountStatusWithRevocation`'s internals) could silently drop these protections. (2) `usecase/service_test.go`'s `TestUpdateAccountStatus_Validation` uses a `mockRepository` that trivially echoes back whatever the usecase passes it — none of its subtests exercise self/admin/transition protection, because that logic doesn't exist at this layer. The only coverage for these three invariants is `repository_integration_test.go`, which is skipped entirely unless `TEST_DATABASE_URL` is set (no CI workflow in this repo sets it, per `find .github`), so in practice these security-relevant checks currently have no coverage in a default `go test ./...` run.
**Suggested fix**: Move the self/admin/transition checks into `usecase.Service.UpdateAccountStatus` (fetch the target's role/status via a repository read, or have the repository return enough info before the mutating call) so they're covered by fast mock-based unit tests, and keep the Postgres layer as a pure atomic-write adapter. If keeping them in the transaction is intentional (to avoid a TOCTOU race between check and update), at minimum add usecase-level tests using a mock that returns the sentinel errors, and note in the code why the check is duplicated/placed there.

## Minor

### 🟡 Metrics bearer token comparison is not constant-time, `internal/platform/metrics/prometheus.go:136`
**Problem**: `parts[1] != bearerToken` is a plain string comparison for the `/metrics` auth token.
**Why it matters**: Theoretically enables a timing side-channel to brute-force the token; low risk here since `/metrics` is meant for internal/infra scraping, but it's a cheap fix and the module's own security model calls out this exact defense-in-depth pattern for other secrets.
**Suggested fix**: Use `subtle.ConstantTimeCompare` (or `crypto/hmac.Equal`) instead of `!=`.

### 🟡 `domain.ErrForbidden` is declared and switched on but never returned, `internal/modules/admin/domain/errors.go:24-25`, `internal/modules/admin/delivery/http/handler.go:204`
**Problem**: No code path in the usecase or repository ever returns `ErrForbidden`; `INSUFFICIENT_PERMISSIONS` is enforced entirely by `transportmw.RequireRole("admin")` before requests reach the handler.
**Why it matters**: Dead code; a reader might assume some domain-level authorization check exists here when it doesn't, which could mask a future gap if `RequireRole` middleware is ever accidentally omitted from a new route.
**Suggested fix**: Either remove the unused error/branch, or use it for a real domain-level authorization decision if one is intended.

## Nits

- ⚪ `internal/modules/admin/repository/postgres/queries/admin.sql:73-87`, `GetOutstandingDebtsByUserID`/`GetOutstandingCreditsByUserID` don't filter on `group_members.status = 'active'` even though AC-4's wording says "unsettled debts or credits in active groups" — plausibly intentional (a debt obligation should still count after someone leaves a group), but worth a one-line comment confirming that's the intended semantics rather than an oversight.
- ⚪ `internal/modules/admin/usecase/service_test.go:275-282`, `repository_MaskBankAccount` duplicates `postgres.MaskBankAccount` instead of importing/exporting it, so the two copies could drift; consider testing the exported `postgres.MaskBankAccount` directly (there's already a `TestBankMasking`-equivalent opportunity in the `postgres` package instead of a duplicated helper in `usecase`).

## Strengths

- The previously-fixed `admin_action` enum mapping bug is well guarded now: `repository_integration_test.go` seeds real Postgres fixtures and specifically asserts `suspend`/`lock`/`reactivate` map correctly and that sessions/refresh tokens are actually revoked in the same transaction — good regression coverage against exactly the class of bug that shipped before.
- `ListAccounts`'s dynamic sort is done via parameterized `CASE WHEN @sort_by = ... THEN col END` rather than string-built SQL, avoiding SQL injection on the `sort_by`/`sort_order` inputs while still allowing dynamic ordering — good pattern.
- Bank account masking, avatar URL derivation, and audit log admin-email joins are all handled at the repository/DTO boundary with no accidental leakage of `password_hash` or raw account numbers anywhere in the response DTOs.

## Test coverage

Well covered: input validation and clamping (`ListAccounts`), UUID validation, bank masking, handler-level status-code mapping for each domain error, health/readiness probes, and metrics bearer-token gating.

Gaps: (1) the reactivate-with-empty-reason path (the Blocker above) has no test anywhere, at usecase, handler, or integration level — every existing reactivate test case supplies a reason. (2) Self-protection/admin-protection/invalid-transition are only covered by the `TEST_DATABASE_URL`-gated integration test, not by the usecase's mock-based tests, so a default `go test ./...` run currently gives no signal on these three security-relevant invariants. (3) No test exercises `GetSystemOverview`'s `media_cleanup` count against a mix of completed and pending fixture rows, which is how the counting bug above would have been caught.
