# Review, namplh/fix-realtime, 2026-09-03

**Reviewed by**: gpt-5.6-sol (author on opus)
**Scope**: 75 files, uncommitted
**Verdict**: Blocked

## Summary

This change builds the intended single user SSE transport, shared PostgreSQL listener, transaction-scoped audiences, small invalidations, and Flutter interest registry. The backend publication paths are generally well factored and most bill, settlement, auth, and roster notifications are issued from the mutation transaction. It is not safe to merge yet: concurrent stream replacement can close the winning stream, session revocation can silently omit the live SID, client recovery loses both 401s and failed refetches, and an OCR provider response can reach logs.

The architecture search found no forbidden framework imports in FE domain code and no Chi or `net/http` imports in BE domain/usecase code. It did find pre-existing `pgx` imports in `internal/modules/bill/usecase/service.go:17` and `internal/modules/bill/usecase/bill_close.go:12`; those files are outside this diff, while the new core-to-feature dependency is reported below.

## Acceptance criteria matrix

| AC | Status | Evidence |
|---|---|---|
| AC-1 | Pass | Group mutations retain the group-row lock and `emitGroupEvent` bumps the per-group version in `internal/modules/group/repository/postgres/sync.go:52`; the existing concurrent integration test is `sync_integration_test.go:124`. |
| AC-2 | Pass | Version bump, `group_events` insert, mutation, and commit share the caller transaction in `internal/modules/group/repository/postgres/sync.go:52-89`. |
| AC-3 | Pass | Roster `pg_notify` uses that transaction at `internal/modules/group/repository/postgres/sync.go:86`. |
| AC-4 | Pass | `GetGroupDetail` uses read-only `REPEATABLE READ` in `internal/modules/group/repository/postgres/repository.go:165`; the response carries version and caller membership. |
| AC-5 | Pass | Snapshot/delta selection and truncated-delta fallback remain in `internal/modules/group/usecase/sync.go`; domain/usecase tests cover cursor boundaries. |
| AC-6 | Pass | Legacy group SSE subscribes before its initial `/sync` read and fences with `lastSent` in `internal/modules/group/delivery/http/sse_handler.go:83-144`. |
| AC-7 | Pass | Max-age, archive, and caller membership-ended close paths remain in `internal/modules/group/delivery/http/sse_handler.go:125-151`. |
| AC-8 | Pass | Legacy log frames use `{version,type,data}` at `internal/modules/group/delivery/http/sse_handler.go:180-189`, shared with `/sync`. |
| AC-9 | Pass | `RenderEventPayload` deletes `avatar_object_key` and emits `avatar_url` at `internal/modules/group/delivery/http/response.go:114-133`; hub tests inspect the public frame. |
| AC-10 | Pass | Flutter ignores old/duplicate versions and calls `/sync` on gaps in `PaySplit-FE/lib/features/groups/presentation/providers/group_roster_provider.dart:299-318`. |
| AC-11 | Pass | Legacy roster reconnect uses the specified jittered backoff and resyncs on resume in `group_roster_provider.dart:208-226,398-409`. |
| AC-12 | Pass | Retention worker and snapshot-on-old-cursor behavior remain wired; cursor boundary tests cover the snapshot decision. |
| AC-13 | Pass | Bootstrap creates one shared listener for all three channels at `internal/bootstrap/app.go:267-275`; disconnect closes all hubs and unhealthy admission returns 503 before headers. |
| AC-14 | Fail | Concurrent replacement is not commit-order safe, and password reset can truncate `target_sids`; see Blockers 1 and 2. |
| AC-15 | Pass | Current producers expose only frozen frame types and bounded OCR fields; audience, SID, bank, raw OCR, and avatar object keys are absent from SSE bodies. The separate raw-OCR logging violation is Blocker 5. |
| AC-16 | Pass | Server buffer is 64, streams register before publication, and `ready` is written before queued frames in `internal/modules/auth/delivery/http/sse_handler.go:105-165`. |
| AC-17 | Pass | One non-autoDispose owner is watched at app scope, closes for required lifecycle states, and page providers register in-memory interests only. |
| AC-18 | Fail | Failed refreshes are discarded instead of remaining dirty/retrying, and the implementation lacks a refresh single flight; see Blocker 4. |
| AC-19 | Pass | Join/reactivate, leave/remove, and archive audiences are read in their mutation transactions; the final removed user is explicitly added. |
| AC-20 | Pass | Every listed bill mutation calls `notifyBillInvalidate` with the committed/locked bill version before transaction commit; no post-commit bill publisher remains. |
| AC-21 | Fail | Backend OCR transitions publish committed summaries, but the Flutter waiter misses the required pre-wait/after-ready GET and mishandles auto fallback; see Major 1. |
| AC-22 | Fail | Backend settlement/lock transaction dispatch matches the matrix, but the required group snapshot consumer is never registered, leaving mounted balances/lock state stale; see Major 2. |
| AC-23 | Fail | 401 recovery, legacy web streaming/version headers, Retry-After handling, and OpenAPI cutover metadata are incomplete; see Blocker 3, Major 3, and Minor 1. |

## Blockers

### 🔴 Concurrent replace controls can close both streams, `internal/modules/auth/delivery/http/sse_handler.go:113`

**Problem**: A successful subscribe applies `ApplyReplace` directly and the shared listener later applies the same control again. The hub also does not distinguish paused candidates from the active stream. If A commits, B registers/commits before A's notification is dispatched, handling A closes paused B; handling B then closes A even though B has already been detached. The direct local applications can race in the same way and do not preserve PostgreSQL commit order.
**Why it matters**: Two concurrent subscriptions for one session can leave no surviving stream or select the wrong winner, directly breaking the cross-process replacement guarantee in AC-14.
**Suggested fix**: Model paused versus active streams explicitly and serialize successful controls through one commit-ordered application path. An older control must not close a later paused candidate; only the named replacement should activate, close the prior active stream, and acknowledge admission before `ready` is written. Add a deterministic concurrent-control test covering both notification orders and publish failure.

### 🔴 Password reset can omit the live revoked SID, `internal/modules/auth/repository/postgres/repository.go:448`

**Problem**: Password reset uses `COALESCE` without `revoked_at IS NULL`, so `UPDATE ... RETURNING` returns every historical session. `EncodeSessionEnded` then reuses `NormalizeAudience`, which sorts and silently truncates at 50 in `internal/platform/realtime/audience.go:36`. With more than 50 retained UUIDv7 sessions, the newest current SID is likely outside the first 50.
**Why it matters**: The database session is revoked, but its open SSE can continue receiving user-routed group/bill activity until max connection age. This breaks immediate password-reset revocation and exposes events to a session the user explicitly invalidated.
**Suggested fix**: Return only sessions newly revoked by adding `revoked_at IS NULL` to revocation updates, and do not apply the group-audience cap to control `target_sids`. Reject an oversized control or safely chunk it without splitting transaction semantics. Test a user with more than 50 retained sessions and assert the live SID closes.

### 🔴 A user-stream 401 permanently stalls Flutter, `PaySplit-FE/lib/core/realtime/user_realtime_owner.dart:158`

**Problem**: `_onError` simply returns for 401. Native SSE marks every status below 500 as successful and throws the 401 after Dio interceptors in `sse_byte_source_io.dart:18-27`; web uses `fetch` directly. Consequently neither platform's stream 401 reaches `AuthInterceptor`, and the owner performs no refresh, retry, or sign-out.
**Why it matters**: At normal access-token expiry the sole realtime connection dies and remains in `connecting`; a revoked session can also leave the authenticated UI stuck instead of signing out. This directly violates AC-23's refresh-once contract.
**Suggested fix**: Give the owner an injected session refresh operation with a single-flight, retry the stream exactly once with the rotated token, and clear tokens/notify session expiry when refresh fails. Add native and web tests for refresh success, refresh failure, and a second 401.

### 🔴 Failed refetches permanently lose invalidations, `PaySplit-FE/lib/core/realtime/user_realtime_owner.dart:345`

**Problem**: `_flush` copies/clears roster buffers and clears `_pendingTargets` plus `_fullMountedRefetch` before awaiting refresh callbacks. There is no try/catch, dirty state, requeue, backoff retry, or per-provider refresh single flight. A single thrown refresh aborts the unawaited future after its invalidation has already been erased; overlapping timer and ready flushes can also run concurrently.
**Why it matters**: A transient REST failure can leave mounted balances, bills, debts, or roster permanently stale while the stream remains connected, exactly the lost-update scenario AC-18 is meant to prevent.
**Suggested fix**: Track clean/dirty/refreshing per complete interest key, retain failed keys, and retry them with the connection backoff while preserving last-good provider state. Serialize flushes and only clear each key after its refresh succeeds. Test ready refetch failures, concurrent invalidations, overflow, and eventual recovery.

### 🔴 Raw OCR provider bodies can be written to logs, `internal/modules/bill/jobs/ocr_worker.go:219`

**Problem**: The worker logs the full provider error with `%v`. `internal/platform/ocr/llamaextract/client.go:270` embeds the complete non-2xx poll response body in that error, which can contain extracted receipt/OCR data.
**Why it matters**: Receipt content can escape the protected OCR retention path into application logs, violating the explicit no-raw-OCR logging requirement and creating an uncontrolled sensitive-data copy.
**Suggested fix**: Log only the bounded `ocrErrorCode(err)` and safe identifiers; make the provider adapter return typed/bounded errors without response bodies. Add a log-capture test using a sentinel response body and assert it never appears.

## Major

### 🟠 OCR wait starts after the race window and ignores auto fallback, `PaySplit-FE/lib/features/bills/data/datasources/bill_remote_datasource.dart:179`

**Problem**: After the create response, the waiter subscribes directly to the process-wide frame bus without first GETting current bill/OCR state. OCR may complete between POST and subscription, and this data-source call blocks before the new bill-detail provider can register its ready-refetch interest. It also chooses legacy only for `REALTIME_MODE=legacy`, so an `auto` owner that has fallen back still waits on the dead user bus. The only healing GET is after event/timeout at line 234.
**Why it matters**: Fast OCR completion, reconnect, app startup, or rollout fallback causes an avoidable 60-second wait before the UI discovers already-committed OCR state.
**Suggested fix**: Implement the specified waiter as an interest owned by the session stream: GET current summary before waiting, GET after every `ready`, and GET at 60 seconds. Make it observe the owner's effective transport mode so auto fallback uses only legacy transport, never the user bus.

### 🟠 Group snapshot invalidations target an interest that does not exist, `PaySplit-FE/lib/core/realtime/user_realtime_owner.dart:280`

**Problem**: Bill finalization/void/deletion and debt changes dispatch to `group.detail`, but no provider registers `RealtimeInterestKey.groupDetail`. The roster provider registers only `group.roster` at `group_roster_provider.dart:439`, and its `/sync` refresh cannot update balances or the submission lock when roster version did not change. Lock invalidation also calls that ineffective roster `/sync` path.
**Why it matters**: A mounted group detail can keep stale snapshot balances and stale lock/header state after another member finalizes a bill, settles debt, or locks submissions, despite receiving the invalidation.
**Suggested fix**: Register a real group-detail/snapshot interest whose refresh performs `GET /groups/{id}` and updates balances, lock state, and roster/version atomically; dispatch the relevant event types to it and test remote lock/finalize/settlement updates.

### 🟠 Legacy cutover is not functional on Flutter web, `PaySplit-FE/lib/features/groups/data/datasources/group_event_stream_datasource.dart:35`

**Problem**: The new user transport correctly uses streaming `fetch`, but both legacy data sources still use Dio `ResponseType.stream`, which is buffered by the browser XHR adapter. They also send only `Accept`, not `X-App-Version` (`group_event_stream_datasource.dart:39-42`, `bill_event_stream_datasource.dart:37-40`). In addition, the web fetch error wrapper discards response headers at `sse_byte_source_web.dart:35-43`, and `_scheduleReconnect` jitters an explicit Retry-After, allowing an early retry.
**Why it matters**: The required 404/501 fallback cannot deliver live events on web, production telemetry classifies fallback traffic as unknown, and web clients cannot honor the server's 429 window.
**Suggested fix**: Route user and legacy SSE through the same conditional streaming byte source and common app-version header builder. Preserve response headers in web errors and treat Retry-After as the minimum/non-jittered delay. Add web transport tests for chunk delivery, fallback exclusivity, headers, and 429.

### 🟠 New core realtime code depends on feature data and presentation layers, `PaySplit-FE/lib/core/realtime/user_realtime_owner.dart:10`

**Problem**: Core imports the auth presentation provider and the groups data-layer SSE type; `realtime_frame_bus.dart` and `user_event_stream_datasource.dart` also import that feature data source for `SseFrame`/parsing. This reverses the workspace dependency direction and makes an app-wide transport contract owned by one feature adapter.
**Why it matters**: Realtime core cannot evolve or be tested independently, and future feature changes can create cycles or force data/presentation dependencies into unrelated consumers.
**Suggested fix**: Move the SSE frame/parser and auth/session ports into core, inject the stream data source and session-refresh/sign-out interfaces, and keep feature providers dependent on those core/domain abstractions.

### 🟠 The risky state machine and transaction guarantees lack behavioral tests, `PaySplit-FE/test/core/realtime/user_stream_parser_test.dart:7`

**Problem**: The new FE tests exercise only line parsing, registry matching, and an environment value; they do not instantiate the owner or test ready ordering, 401, fallback, lifecycle, coalescing, overflow, dirty retry, or OCR recovery. BE tests cover envelope/hub/listener helpers but do not assert same-transaction commit/rollback and exact audiences for auth revocation, bill/OCR mutations, or the settlement dispatch matrix. The review checklist still marks those exact steps unchecked.
**Why it matters**: Most new logic is branching, asynchronous, security-sensitive recovery code, and the confirmed failures above all pass the current suites.
**Suggested fix**: Add owner tests with fake byte source/clock/interest callbacks and repository integration tests that LISTEN while committing and rolling back each mutation matrix row. Include >50 session revocation and concurrent replace-order regressions.

## Minor

### 🟡 OpenAPI does not describe the cutover contract, `docs/openapi.yaml:162`

**Problem**: The user endpoint description says disabled servers return 404 but its responses omit 404 and 501. The legacy group and bill operations at lines 883 and 1360 are not marked `deprecated` and do not document the future 410 body with `replacement`.
**Why it matters**: Generated clients and API consumers cannot implement or validate the frozen AC-23 response matrix from the published contract.
**Suggested fix**: Add the rollout responses, mark both legacy operations deprecated now, and document the exact 410 `STREAM_REPLACED` response schema while retaining their current runtime behavior.

### 🟡 Static analysis is not clean, `PaySplit-FE/lib/main.dart:18`

**Problem**: `flutter analyze` reports `avoid_redundant_argument_values` for the explicit default `realtimeMode` argument in all four entry points (`main.dart`, `main_development.dart`, `main_staging.dart`, `main_production.dart`).
**Why it matters**: The configured analysis command exits nonzero, so a clean-analysis merge gate will fail.
**Suggested fix**: Either remove redundant development/default arguments where they are truly defaults or adjust the API/default so each flavor's explicit rollout choice is not lint-redundant.

## Strengths

- Bootstrap composes one shared listener for `group_events`, `bill_events`, and `user_events`, with bounded reconnect labels and disconnect fan-out that closes local streams.
- Bill, OCR, settlement, roster, and auth publishers generally capture audiences and enqueue `pg_notify` inside their mutation transaction; public roster rendering removes `avatar_object_key`, and OCR frames use bounded fields/error codes.
- Flutter web's new user-mode transport reads the actual `fetch` response body stream, and CORS allows `X-App-Version`, exposes `Retry-After`, and accepts localhost Flutter-web origins.

## Test coverage

`/usr/local/go/bin/go test ./...` passed. The focused roster database tests exist but were skipped because `TEST_DATABASE_URL` is unset, so this review statically traced AC-1 through AC-4 rather than proving them against PostgreSQL. `flutter test` passed 261 tests with one skipped; `flutter analyze` completed with the four info-level findings noted above. Existing roster unit/integration tests give useful regression coverage for cursor rules, gapless versions, rollback, framing, close reasons, and avatar redaction, while the new user owner and transaction notification matrix remain materially under-tested as described in Major 5.
