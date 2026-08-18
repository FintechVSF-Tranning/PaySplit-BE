# Verify: Admin v1 · spec 0005 · updated 2026-08-18
_Steps derived from spec 0005 acceptance criteria. `/check verify` runs these; `/test` locks the durable ones._

## UI / manual
- [ ] Admin sends `GET /api/v1/admin/accounts?page=1&limit=20` with valid admin bearer token → returns 200 with paginated user list and pagination metadata → AC-1
- [ ] Admin sends `GET /api/v1/admin/accounts?limit=150` → limit is silently clamped to 100 in metadata and results → AC-1
- [ ] Non-admin user sends `GET /api/v1/admin/accounts` with regular user token → returns 403 INSUFFICIENT_PERMISSIONS → AC-5
- [ ] Admin sends `GET /api/v1/admin/accounts/{id}` with valid user UUID → returns 200 with full account profile, active session count, group list, financial summary, and masked bank account number showing only last 4 digits (e.g. `******1234`) → AC-2
- [ ] Admin sends `PUT /api/v1/admin/accounts/{id}/status` with `status: "suspended"` and `reason: "Policy violation"` → updates status to suspended, logs audit record, immediately revokes active sessions and refresh tokens → AC-3, AC-4
- [ ] Admin attempts to change status of their own account (`CANNOT_MODIFY_SELF`) → returns 403 CANNOT_MODIFY_SELF → AC-3
- [ ] Admin attempts to suspend another admin (`CANNOT_MODIFY_ADMIN`) → returns 403 CANNOT_MODIFY_ADMIN → AC-3
- [ ] Admin attempts status change on `pending_verification` user → returns 400 INVALID_STATUS_TRANSITION → AC-3
- [ ] Admin sends `GET /api/v1/admin/system/overview` → returns 200 with aggregated platform statistics for users, groups, bills, debts, media cleanup queue, OCR jobs, and Go runtime metrics → AC-7
- [ ] Monitor or orchestrator sends `GET /health/live` → returns 200 with `{"status": "ok"}` → AC-6
- [ ] Monitor or orchestrator sends `GET /health/ready` when database is healthy → returns 200 with `{"status": "ready", "database": "ok"}` → AC-6
- [ ] Scraper requests `GET /metrics` → returns 200 with Prometheus text exposition format metrics including HTTP counters and database connection gauges → AC-8

## Commands
- [x] `go test -v ./internal/modules/admin/... ./internal/transport/http/router/...` → all test suites pass → AC-1 through AC-8
- [x] `make build` → compiles `bin/paysplit-api` cleanly with no compilation errors → AC-1 through AC-8

## Acceptance-criteria coverage
- AC-1 covered by account list search, filter, pagination, and limit clamping steps
- AC-2 covered by account detail lookup, masked bank number, and audit log steps
- AC-3 covered by status update validation, self protection, and admin protection steps
- AC-4 covered by atomic status update, session revocation, and warning metadata steps
- AC-5 covered by liveAuth and RequireRole("admin") role restriction steps
- AC-6 covered by health probe endpoints (/health/live, /health/ready)
- AC-7 covered by system overview statistics and runtime metrics endpoint
- AC-8 covered by Prometheus metrics exposition endpoint (/metrics)
