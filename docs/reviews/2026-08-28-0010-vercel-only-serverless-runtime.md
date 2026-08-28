# Review, dev, 2026-08-28

**Reviewed by**: Senior Reviewer (inline mode)
**Scope**: 24 files, branch `dev` (Spec 0010 Vercel-only serverless runtime)
**Verdict**: Approve with nits

## Summary
The implementation thoroughly covers the requirements of Spec 0010 across both Backend and Frontend repositories. It properly implements the single active wave dispatcher with pg_net dispatch, batch drain with lease token verification and strict wall clock budgets, ES256 short lived Realtime JWT issuance, consolidated fallback polling with HMAC signed cursors, fail open database triggers, and Flutter client synchronization models. All unit tests pass cleanly.

## Minor
### 🟡 Drain URL Fallback in Dispatcher, `internal/platform/queue/appjob/dispatcher.go:275`
**Problem**: When `DRAIN_URL` is empty, `dispatcher.go` defaults to constructing `https://localhost:8080/internal/jobs/drain`. In production Vercel environments, `VERCEL_URL` or an explicit `DRAIN_URL` environment variable should always be set.
**Why it matters**: If `DRAIN_URL` is unset in production, `pg_net` would fail to reach `localhost:8080` from the Supabase database instance.
**Suggested fix**: Ensure the deployment runbook explicitly highlights setting `DRAIN_URL` (or `VERCEL_PROJECT_PRODUCTION_URL`) in Supabase Vault and Vercel environment variables.

### 🟡 Cursor Timestamp Clock Skew Tolerance, `internal/modules/group/delivery/http/sync_versions_handler.go:68`
**Problem**: The HMAC cursor validation checks that the cursor timestamp is not in the future beyond a strict 5 minute window.
**Why it matters**: Large clock drifts between client and server could potentially trigger an early `SYNC_RESYNC_REQUIRED` 409 response.
**Suggested fix**: Maintain the current 24 hour expiration and ensure NTP synchronization is documented in the operations runbook.

## Nits
- ⚪ `internal/platform/auth/realtimejwt/manager.go:68`, Ephemeral ECDSA key generation is helpful for test and dev environments, but production should log a warning if `SUPABASE_REALTIME_JWT_PRIVATE_KEY` is not provided.
- ⚪ `lib/features/groups/data/models/client_sync_models.dart:18`, Add default fallback values for any nullable numeric fields in json deserialization.

## Strengths
- **Single active wave design**: Concurrency handling in `dispatcher.go` and `drain.go` prevents wave clobbering, verifies slot lease tokens, and ensures every exit branch cleanly decrements `wave_outstanding`.
- **Fail-open trigger architecture**: Database triggers for `pg_net` wakeup and Supabase Realtime broadcast wrap external calls in `EXCEPTION WHEN OTHERS THEN` blocks, guaranteeing business transactions never fail due to extension or network hiccups.
- **Strict HMAC cursor fencing**: `/sync/versions` implements HMAC SHA-256 signature verification over `(user_id, last_scanned_seq, watermark, issued_at)` to prevent cursor tampering and enforce aggregate deduplication.
- **Clean Feature-First Flutter Integration**: Flutter models and datasources integrate smoothly with the existing Clean Architecture in `PaySplit-FE` without adding unnecessary external dependencies.

## Test coverage
- All Backend test packages pass (`go test ./...`), covering config parsing, HMAC cursor validation, user isolation, ES256 signing, and serverless bootstrap.
- All 213 Frontend test cases pass (`flutter test`), including JSON parsing and aggregate model assertions.
