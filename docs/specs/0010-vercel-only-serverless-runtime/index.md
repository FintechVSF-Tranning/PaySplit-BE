# 0010. Vercel only serverless runtime

**Date**: 2026-08-28
**Status**: In Progress
**Architecture review**: Conditionally accepted on 2026-08-28. Implementation and production rollout remain blocked by the seven final pre-acceptance gates.

## Summary

Production runs only as Go Vercel Functions. Supabase supplies PostgreSQL, transaction pooling, Realtime, Cron, and asynchronous database webhooks. PaySplit removes every production process that must stay alive and replaces River consumers and PostgreSQL listeners with durable database state and bounded function invocations.

## Rationale

[Detailed reasoning and options](./rationale.md).

## Requirements

**User stories**:

1. As a mobile user, I want current group, bill, and OCR state even when a realtime signal is late or lost.
2. As an operator, I want Vercel autoscaling to stay within Supabase limits without permanent database sessions.
3. As an operator, I want jobs to recover after timeouts and overlapping triggers without duplicate business effects.

**Acceptance criteria**:

1. **AC-1**: Production exposes an `http.Handler` through the Vercel Go runtime and never calls `ListenAndServe`. Production accepts only `APP_RUNTIME_ROLE=api`. A local runner may exist for development and tests, but production behavior does not depend on it.
2. **AC-2**: Every production database connection uses the Supabase transaction pooler on port `6543`, uses pgx `QueryExecModeExec`, sets `DB_MAX_CONNS=2` and `DB_MIN_CONNS=0`, and closes its invocation scoped pool before the Vercel invocation returns.
3. **AC-3**: Production starts no River migration, River consumer, periodic goroutine, PostgreSQL `LISTEN`, in process cross request hub, or memory state required by a later invocation. Supabase shows zero PaySplit owned `LISTEN` sessions.
4. **AC-4**: A business transaction that changes visible group or bill state inserts one durable invalidation row in the same transaction. Each row contains only `group_id`, aggregate type, aggregate ID, monotonic sync version, and creation time. An `AFTER INSERT` trigger sends the same metadata to an authenticated private Supabase Realtime Broadcast topic for that group. The table contains no bill, member, payment, or OCR payload and is not added to the `supabase_realtime` publication.
5. **AC-5**: Flutter receives invalidations only through authenticated private Broadcast channels. Token issuance verifies the existing PaySplit JWT and live session; private-channel join verifies the five-minute Realtime JWT and active group membership. Supabase may cache join authorization until token refresh or expiry, so a session or membership revoked after a successful join may continue receiving minimal non-authoritative invalidations for no longer than the `300s` token TTL plus `60s` clock-skew allowance. Refresh is denied by the live-session check and the channel disconnects or rejects authorization after that bound. Every REST snapshot immediately rechecks the existing live session and membership, so stale Broadcast authorization never grants authoritative data. Cross-group joins are denied. Deleting an invalidation row publishes no Realtime event.
6. **AC-6**: Realtime is an acceleration path only. Flutter ignores a signal whose version is not greater than its local version, fetches the authoritative REST snapshot after a newer signal, and updates local state only from a snapshot with an equal or newer version. On subscribe, reconnect, app resume, token refresh failure, or a detected version gap, Flutter fetches a snapshot. Polling uses one consolidated `GET /api/v1/sync/versions` request per device every `10s` with a stable per-device phase and `20%` jitter; it never issues one periodic request per group. A normal page contains at most 500 changed aggregates and 256 KiB, is authorized against the caller's live session and active memberships, and advances an opaque monotonic cursor. Additional pages are fetched only when `has_more=true`, are bounded to four pages per cycle, and depend on change volume rather than group count; exceeding that bound triggers an authoritative resync instead of unbounded paging.
7. **AC-7**: Bill snapshots expose `sync_version` and the current OCR job summary. Group detail and group sync retain `roster_version`. Bill and group mutations update their relevant version atomically with durable state and invalidation insertion. Invalidation rows receive a commit-ordered global `sequence` from a singleton counter row acquired after all business-row locks and immediately before invalidation insertion and commit; no later business lock may be acquired. Later invalidation transactions cannot allocate and commit a higher cursor until the current holder commits or rolls back. `/sync/versions` therefore cannot miss a late commit below an advanced cursor. Membership changes atomically increment the affected user's `membership_sync_version`; a changed membership version makes Flutter refresh the authoritative group list, which removes inactive groups without returning unauthorized group metadata from the consolidated version feed. Load tests include counter-lock latency and lock ordering.
8. **AC-8**: Production removes the existing bill and group SSE routes, PostgreSQL listeners, and in-process hubs. Repository evidence shows no release tags or store deployment and the Flutter package remains `1.0.0+1`, so no installed-client compatibility bridge is designed. Before implementation, store and deployment evidence must confirm this premise; if installed clients exist, this spec returns to `Proposed` for a separate globally leased compatibility design instead of silently adding unbounded SSE.
9. **AC-9**: Production River jobs are replaced by `app_jobs`. Job insertion may occur in the same business transaction. The unique pair of job kind and idempotency key prevents duplicate logical jobs. Rows track availability, attempts, maximum attempts, lease token, lease expiry, completion, and terminal failure.
10. **AC-10**: Before claiming work, the dispatcher reserves one of exactly ten `job_drain_slots` with a unique token, and the secured drain atomically activates and commits only its assigned current reservation. No free slot makes the dispatcher return `204`; an absent, duplicate, expired, or stale reservation makes the drain return `204`. After a valid activation, the drain claims one job in a short transaction using `FOR UPDATE SKIP LOCKED`. If no job is claimable, it releases the slot with the current token before returning `204`; a concurrent job becoming due is either claimed or leaves/re-arms dispatch state for the next bounded activation. The drain commits each claim before external work and completes or reschedules in another short transaction. It processes at most five jobs sequentially within a `45s` PaySplit wall-clock budget, reserves the last `5s` for completion and response, gives one external operation at most `35s`, and uses `75s` job and slot leases. Slot release and job completion require the current lease token; a stale invocation cannot release a reclaimed slot, and expired slots recover automatically.
11. **AC-11**: A failed job uses exponential backoff from `30s` up to `1h` with bounded jitter. An expired lease is claimable again. Exhausted jobs enter `discarded`, preserve their error, and appear in metrics. The ten database slots, not process memory or best-effort rate limiting, globally cap drain invocations produced by immediate dispatch, Supabase Cron recovery, and Vercel Cron recovery.
12. **AC-12**: A singleton `job_wakeup_state` coalesces a committed enqueue burst into one dispatcher activation, not one request per job. The dispatcher is globally fenced by a database lease, reconciles expired slot reservations, counts due jobs and free slots, and reserves then dispatches exactly `min(free_slots, ceil(due_jobs / JOB_BATCH_SIZE))`, never more than ten, through token-bound slot reservations. A dispatch wave tracks its outstanding slots. When each drain releases its slot, the last completed slot in the wave uses generation compare-and-set fencing to acknowledge only the generation it observed and queues one new dispatcher activation when due jobs or a newer generation remain. A drain never clears a newer wakeup. Lost or duplicate dispatcher and drain calls are harmless; expired reservations recover. `pg_net` and minute Supabase Cron call `POST /internal/jobs/dispatch` with an explicit `10,000ms` timeout; Vercel Cron calls it with `GET` once daily. The dispatcher has a `5s` budget and queues drain `POST` calls with a `55,000ms` timeout. Each drain retains its `45s` application budget under Vercel `maxDuration=50`. All supported methods use the same exact bearer secret and no-cache behavior; missing or wrong secrets return `401`, and unsupported methods return `405`.
13. **AC-13**: SMTP, FCM, Cloudinary, OCR, VietQR, and every other network call runs after its claim or business transaction commits. Retries use stable effect IDs, deterministic Cloudinary object IDs, compare and set database updates, and client notification deduplication so committed business effects and user visible notification event IDs occur exactly once.
14. **AC-14**: `/health/live` proves only that the function executed. `/health/ready` performs a one second bounded database probe. Neither endpoint exposes connection strings, secrets, provider identifiers, pool counts, or internal errors.
15. **AC-15**: The production connection design envelope is 50 total concurrent public API invocations, explicitly including at most 20 simultaneous fallback polls after jitter, plus at most one globally leased dispatcher and at most 10 drain invocations. The other 30 public invocations represent normal API traffic. At two clients per invocation this is at most `2 × (50 + 1 + 10) = 122` Supavisor clients, leaving 78 below the currently assumed Nano or Micro client limit of 200. Supavisor opens at most 15 backend connections against the currently observed PostgreSQL limit of 60.
16. **AC-16**: A 10 minute load run holds 50 total concurrent public API requests, composed of 30 normal API requests and no more than 20 simultaneous consolidated polls from 50 fallback-poller clients running every `10s` with a stable phase and `20%` jitter. At least ten clients own more than ten groups and 250 bill aggregates; each still sends one periodic `/sync/versions` request, with bounded change-volume paging only when required. The run also covers 150 concurrent Supabase Realtime WebSocket connections with at most ten authenticated private group channels per connection. A single real business transaction enqueues 100 due short jobs; its one coalesced dispatcher activation creates up to ten slot owners and subsequent fenced waves without manually starting drain calls. All due short jobs finish p95 within `2m`. Realtime channel joins are jittered and measured separately from WebSocket connections. Non-validation errors stay below `0.5%`; pool acquisition p95 stays below `250ms` and p99 below `1s`; database-only API latency p95 stays below `750ms` and p99 below `2s`; Realtime-to-authoritative-snapshot p95 stays below `3s`; polling convergence p95 stays below `12s`; duplicate committed business effects and duplicate user-visible notification event IDs are zero.
17. **AC-17**: A forced timeout after an external operation and an overlap of ten drain invocations recover the job within two minutes of lease expiry, produce no duplicate committed business effect, leave no transaction idle longer than `5s`, and never exceed the connection envelope.
18. **AC-18**: Migration and rollback run entirely on Vercel and Supabase. Rollback never enables production role `all`, starts River, or requires a persistent process. Durable jobs and invalidations survive code rollback.

## Decision

**Chosen option**: Use a Vercel only Go API with authenticated private Supabase Realtime Broadcast invalidations and a PostgreSQL durable job queue awakened by coalesced Supabase `pg_net` requests and Supabase Cron. Flutter treats every realtime message as an invalidation and obtains authoritative data through the existing authenticated REST API. (basis: current custom session model, existing group version fencing, Vercel serverless lifecycle guidance, Supabase Realtime Broadcast authorization, Supabase Cron and `pg_net`)

The runner up is version based REST polling without Supabase Realtime. It is simpler and remains the mandatory fallback. The chosen option adds a short lived asymmetric Realtime token and private channel RLS because it restores prompt updates without adding an application runtime. If imported-key verification, rotation, cross-group denial, or the bounded revocation timeline cannot be proven against the project configuration, rollout stays in polling mode instead of weakening membership or session checks.

**Implementation skills**: `supabase` (`supabase/agent-skills`, `.agents/skills/supabase/`) · `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Feature design

### Runtime topology

| Component | Host | Lifetime | Responsibility |
|---|---|---|---|
| Go API | Vercel Function in `sin1` | one invocation | REST, auth, snapshots, Realtime token, health |
| Job dispatcher | Vercel Function in `sin1` | at most 5 seconds | fence one dispatch wave, reserve free slots, queue bounded drains |
| Job drain | Vercel Function in `sin1` | at most 45 seconds | claim and process at most five durable jobs |
| PostgreSQL and Supavisor | Supabase `ap-southeast-1` | managed | state, transaction pooling, RLS, jobs, invalidations |
| Realtime | Supabase | managed | RLS-authorized private Broadcast invalidations to Flutter |
| Scheduler and wakeup | Supabase Cron and `pg_net` | managed | minute recovery, periodic enqueue, coalesced immediate wakeup |

The repository currently has no `vercel.json`, `.vercel/project.json`, or account plan metadata. The safe planning baseline is Vercel Hobby with Go Functions and no Fluid Compute concurrency guarantee. The design therefore gives PaySplit a conservative `45s` application budget under an explicit `50s` Vercel `maxDuration`; `45s` is not claimed as the Hobby platform maximum. It works with once-daily Vercel Cron and depends on Supabase Cron for minute scheduling. Before production cutover, the dashboard plan and limits are recorded as deployment evidence. A paid plan may raise limits but does not change this design.

### Request flow

1. Vercel invokes the exported Go handler.
2. Static dependencies may be reused by a warm process, but no correctness depends on reuse.
3. The invocation creates a pgx pool with max two connections, transaction mode execution, a one second acquisition timeout, and a route specific `application_name`.
4. Middleware and repositories run with the request context and existing authorization rules.
5. The invocation closes the pool before returning.

### Realtime flow

1. A business transaction changes state, increments the entity sync version, and inserts one `realtime_invalidations` row.
2. An `AFTER INSERT` trigger sends the metadata to private topic `group:<group_id>` through Supabase Realtime Broadcast. The durable table is not in the Realtime publication, so later cleanup emits no row-change event.
3. Flutter requests a Realtime token from the Vercel API using its normal PaySplit access token.
4. Supabase verifies the imported asymmetric signing key and applies RLS on `realtime.messages` when Flutter joins each private group topic.
5. One device opens one WebSocket and at most ten group channels. A client with more groups subscribes to its ten most recently active groups and relies on snapshots plus jittered polling for the rest.
6. Flutter receives only group and entity identifiers plus a version, fetches the authoritative snapshot from Vercel, and applies it only when its version does not regress local state.
7. Missed or reordered signals are repaired by snapshot fetch on reconnect and by 10 second fallback polling with 20% per-client jitter.

### Consolidated polling flow

1. After login, app resume, or a membership-version change, Flutter fetches the authoritative group list and relevant snapshots. It then calls `/api/v1/sync/versions` without a cursor to establish the current global invalidation watermark; historical invalidations are not replayed as authority.
2. A steady-state poll sends one signed opaque cursor per device, not one request per group. The cursor encodes the fixed page watermark, last scanned invalidation sequence, and page count but grants no authorization by itself; tampering or malformed state returns `400`.
3. The backend validates the live session, filters every returned aggregate through current active memberships, and returns the latest changed version per authorized aggregate plus the caller's current `membership_sync_version`.
4. A page contains at most 500 aggregates and 256 KiB. If more authorized changes exist before the fixed watermark, `has_more=true` and `next_cursor` continues that same snapshot. Flutter fetches at most four pages in the cycle; overflow or a cursor older than the seven-day retention floor returns `409 SYNC_RESYNC_REQUIRED` and causes one authoritative group-list and snapshot resync rather than unlimited polling requests.
5. When the cursor page completes, Flutter stores the returned watermark. New invalidations have greater sequences and appear in the next cycle. Global sequence gaps caused by unauthorized groups reveal no payload and do not trigger per-group requests.
6. Inactive groups are omitted. A membership change increments `membership_sync_version`; Flutter then refreshes the group list once and removes or adds local groups from that authoritative response. Groups beyond the ten private-channel cap are still covered by the same consolidated poll.

### Background job flow

1. A business transaction inserts one or many `app_jobs` rows with stable idempotency keys. Its statement-level trigger increments `requested_generation`. A compare-and-set transition of `dispatcher_requested` from false to true queues one `pg_net` call to `/internal/jobs/dispatch`; further inserts only advance the generation while a request or dispatcher lease is outstanding.
2. The dispatcher atomically claims the singleton dispatcher lease and consumes only the `dispatcher_requested` flag for that activation; it never clears or lowers a generation. A duplicate or overlapping activation returns `204`. Under the state-row lock it reconciles expired slot reservations and the outstanding count. Only one wave may be active; if a non-expired wave still has outstanding slots, the dispatcher preserves the newer requested generation for that wave's final release and returns `204`. Otherwise it records the observed generation, counts claimable due jobs and free slots, and computes `N = min(free_slots, ceil(due_jobs / JOB_BATCH_SIZE))`.
3. The dispatcher reserves exactly `N` free slots with unique tokens and a new `wave_id`, records `wave_outstanding=N` plus the dispatched generation, and queues `N` token-bound `POST /internal/jobs/drain` requests through `pg_net`. It never acknowledges a generation merely because requests were queued. If no due job exists, it advances `acknowledged_generation` only to the observed generation, records the earliest future `available_at`, releases its lease, and returns `204`. If due jobs exist but no slot is free, it leaves the generation unacknowledged, records the earliest slot expiry, releases its lease, and returns `204`; the active wave's final release or minute recovery re-arms dispatch.
4. A drain atomically changes only its matching slot from `reserved` to `leased`. An invalid or duplicate token returns `204`. It claims and commits one due job before external work, processes at most five sequential jobs, and records completion or retry in short token-fenced transactions. If its slot is valid but no job is claimable, it token-releases the slot before returning `204`.
5. The drain stops claiming at `40s` and reserves `5s` for final state and response. On token-matched slot release it decrements the matching wave's outstanding count. The last completed slot re-counts due jobs and uses compare-and-set to queue one dispatcher activation when backlog or a newer requested generation remains; otherwise it advances `acknowledged_generation` only to the generation the completed wave observed.
6. New enqueue generations cannot be cleared by an older wave. A job becoming due concurrently is either claimed by the current drain, observed during final re-count, or covered by `next_dispatch_at` and the minute dispatcher recovery. Existing due backlog therefore creates successive bounded waves without requiring another insert.
7. Supabase Cron calls the dispatcher every minute even when the requested flag is stale, reconciles expired dispatcher and slot leases, enqueues hourly and daily jobs with bucket idempotency keys, and repairs lost requests. Vercel Cron calls the same dispatcher once daily. Cron is recovery; successful waves re-arm themselves immediately.
8. Caller termination never implies completion. Lost drain requests leave token-bound reservations that expire after `75s`; duplicate requests cannot activate the same slot twice. The next fenced dispatcher or Cron activation safely reconciles them.

Synchronous API work includes validation, durable business changes, notification and job insertion, and local CPU work that fits the request timeout. FCM delivery, OCR, bulk finalize items, cleanup, retention, and settlement scans are eventual jobs. SMTP or storage calls that remain request driven occur only after the related transaction commits.

### Data model sketch

| Entity | Key fields | Constraints and purpose |
|---|---|---|
| `app_jobs` | `id uuid`, `kind text`, `args jsonb`, `idempotency_key text`, `status text`, `priority int`, `available_at timestamptz`, `attempts int`, `max_attempts int`, `lease_token uuid`, `lease_expires_at timestamptz`, `last_error text`, `completed_at timestamptz`, timestamps | Unique `(kind, idempotency_key)`. Status is `available`, `running`, `completed`, or `discarded`. Partial claim index on `(available_at, priority, created_at)` for claimable rows. Args contain identifiers, never provider secrets. |
| `job_drain_slots` | `slot_no smallint`, `state text`, `lease_token uuid`, `lease_expires_at timestamptz`, `dispatch_generation bigint`, `wave_id bigint`, `holder text`, `updated_at timestamptz` | Exactly ten seeded rows with `slot_no` from 1 through 10. The dispatcher moves a free or expired row to `reserved`; only the matching drain token moves it to `leased`. Release requires the current token; expiration is reconciled by the dispatcher. |
| `job_wakeup_state` | singleton key, `requested_generation bigint`, `dispatched_generation bigint`, `acknowledged_generation bigint`, `dispatcher_requested boolean`, `dispatcher_token uuid`, `dispatcher_lease_expires_at timestamptz`, `wave_id bigint`, `wave_outstanding smallint`, `next_dispatch_at timestamptz`, timestamps | Exactly one row coalesces enqueue bursts, fences one dispatcher, and tracks bounded waves. Generation compare-and-set prevents an older drain from acknowledging newer work. Minute Cron does not depend on the requested flag and reconciles expired reservations. |
| `sync_sequence_state` | singleton key, `value bigint`, `updated_at timestamptz` | Allocates the global invalidation cursor through a row-locked update after all business locks and immediately before invalidation insertion and commit. No later business lock is allowed. This makes cursor order safe at the cost of a measured short mutation serialization point. |
| `realtime_invalidations` | `id uuid`, `sequence bigint`, `group_id uuid`, `aggregate_type text`, `aggregate_id uuid`, `version bigint`, `created_at timestamptz` | `sequence` comes from `sync_sequence_state` and is unique. Foreign key to group. Unique `(aggregate_type, aggregate_id, version)`. Backend-only durable metadata. An `AFTER INSERT` trigger broadcasts to a private group topic. The table is not in `supabase_realtime`; seven-day cleanup cannot emit Postgres Changes `DELETE` events. |
| `users.membership_sync_version` | `bigint not null default 0` | Incremented atomically for an affected user when active group membership changes. A mismatch makes Flutter refresh the authoritative group list rather than infer membership from invalidations. |
| `bills.sync_version` | `bigint not null default 0` | Monotonic version for every user visible bill or OCR state change. Separate from optimistic edit `bills.version`. |
| `group_events` | existing group event rows | Remains the durable delta source for `/groups/{id}/sync`. A matching invalidation is inserted with the same `roster_version`. |
| `river_job` and related River tables | existing queue rows during migration | Pending supported jobs are copied once into `app_jobs`; tables are retained through the rollback window and removed only in a later migration. |

### State transitions

| Entity | Transition |
|---|---|
| Job | `available` to `running` to `completed` |
| Job retry | `running` to `available` with later `available_at` |
| Job timeout | `running` lease expires, then a new claim remains `running` with a new token |
| Job terminal failure | `running` to `discarded` when attempts reach `max_attempts` |
| Drain slot | free or expired to reserved, token-matched activation to leased, then token-matched release to free |
| Dispatcher | idle or expired to leased, reserves one bounded wave, then releases; duplicate activation returns no work |
| Wakeup | enqueue advances requested generation, dispatcher records dispatched generation, and the last matching drain advances acknowledged generation only when no due backlog or newer generation remains |
| Realtime client | snapshot to subscribed, then invalidated to snapshot refresh, then polling fallback when disconnected |

### API surface

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| Existing REST business routes | existing | unchanged plus version inputs where already required | unchanged business data | existing live session rules | unchanged |
| `/api/v1/auth/realtime-token` | POST | current access token | Realtime JWT, expiry | live session | 401, 503 when secure signing is unavailable |
| `/api/v1/app-config` | GET | app version and platform headers | realtime mode, polling interval and jitter, maximum private group channels, sync page policy | public values only | 400 |
| `/api/v1/sync/versions` | GET | opaque `after` cursor, optional `limit` capped at 500 | up to 500 authorized aggregate versions, `membership_sync_version`, `watermark`, `next_cursor`, `has_more`; maximum 256 KiB | live session and active memberships | 400 invalid cursor, 401, 409 resync required |
| `/api/v1/bills/{id}` | GET | bill ID and existing group context | existing snapshot plus `sync_version` and OCR job summary | active group member | existing 401, 403, 404 |
| `/api/v1/groups/{id}` and `/sync` | GET | existing inputs | existing data and roster version | active group member | unchanged |
| Existing bill and group `/events` | removed | none | no compatibility stream | none | 404 after route removal |
| `/internal/jobs/dispatch` | POST | empty body from enqueue `pg_net` or Supabase Cron | reserved and dispatched counts, or 204 when another dispatcher owns the lease, no due work, or no free slot | exact bearer secret | 401, 405, 503 |
| `/internal/jobs/dispatch` | GET | Vercel Cron request | reserved and dispatched counts, or 204 when another dispatcher owns the lease, no due work, or no free slot | exact `CRON_SECRET` bearer | 401, 405, 503 |
| `/internal/jobs/drain` | POST | dispatcher-issued slot number, lease token, wave ID, and dispatch generation | claimed, completed, retried, discarded counts; 204 for an invalid reservation or token-released no-work slot | exact bearer secret | 400, 401, 405, 503 |
| `/health/live` | GET | none | `ok` | public | 500 only when handler cannot execute |
| `/health/ready` | GET | none | `ready` or `unavailable` | public | 503 on bounded database probe failure |

### Value sourcing

| Action | Value produced or displayed | Source |
|---|---|---|
| Select production composition | serverless API only | validated `APP_RUNTIME_ROLE=api` and Vercel environment |
| Authorize Realtime | user, session, role, audience, issuer, key ID, expiry | current live session plus server-minted claims `sub`, `sid`, `role=authenticated`, `aud=authenticated`, configured issuer, `iat`, `exp`, and asymmetric JWT header `kid` |
| Authorize invalidation | active group membership | private Broadcast channel RLS using the JWT `sub` and `sid`, `realtime.topic()`, `sessions`, and `group_members` |
| Order invalidations | aggregate version | `bills.sync_version` or `groups.roster_version` incremented in the business transaction |
| Recover client | authoritative state and version | authenticated bill snapshot or group snapshot and sync endpoint |
| Poll all aggregates | bounded authorized version delta | monotonic invalidation `sequence`, opaque cursor, live membership filter, current `membership_sync_version`, and 500-entry or 256-KiB response ceiling |
| Create logical job | kind, args, maximum attempts, effect identity | existing River job kind contract and the business transaction identifiers |
| Dispatch drain capacity | due count, free count, reserved slot tokens | globally leased dispatcher, `min(free_slots, ceil(due_jobs / 5))`, database-generated UUIDs, database `now()`, fixed 75 second reservations |
| Claim work | lease token and expiry | database-generated UUID, database `now()`, fixed 75 second lease |
| Retry work | next availability | attempt count, 30 second exponential base, one hour cap, bounded jitter |
| Identify database usage | `application_name` | route class: `paysplit-api`, `paysplit-job`, or `paysplit-health` |
| Select mobile realtime mode | `supabase` or `polling` | `MOBILE_REALTIME_MODE` returned by `/api/v1/app-config` |

### Key invariants

1. PostgreSQL is the authority. Realtime never carries authoritative domain payloads.
2. Production owns zero session pooled connection and zero `LISTEN` connection.
3. A job lease transaction contains no provider call.
4. Completion requires the current lease token so a stale invocation cannot complete a reclaimed job.
5. Every retry preserves the logical job ID and stable effect ID.
6. Token issuance and private-channel join require an active session and membership. Revocation after join may leave minimal invalidation delivery for at most the five-minute Realtime token TTL plus one minute of allowed clock skew; REST rejects authoritative access immediately and refresh cannot extend the revoked authorization.
7. At most ten drain invocations own capacity, regardless of trigger overlap or Vercel autoscaling.
8. A dispatcher reserves no more than `min(free_slots, ceil(due_jobs / JOB_BATCH_SIZE))`; generation fencing and wave completion keep due backlog moving without acknowledging newer work.
9. One device polling cycle is independent of its group count in steady state: one consolidated cursor request, followed only by snapshots for changed aggregates and bounded change-volume pagination.
10. Invalidation cleanup emits no Realtime `DELETE` event because only private Broadcast on insert is enabled and the durable table is never published.
11. The API remains useful when Supabase Realtime, `pg_net`, or a scheduler is temporarily unavailable.

### Security model

The backend signs a separate five minute Realtime JWT with `ES256` and an imported asymmetric Supabase signing key. The JWT header contains the active `kid`; claims contain `iss`, `aud=authenticated`, `sub` as PaySplit user ID, `sid` as PaySplit session ID, `role=authenticated`, `iat`, and `exp`. Private material exists only in the two controlled backend key stores: Vercel secrets for PaySplit token minting and Supabase's managed signing-key system after import. It never appears in Flutter, repository files, logs, responses, or public configuration. Supabase verifies through the matching public key and publishes it in project JWKS. The token endpoint first runs existing access-token verification and live-session lookup. Flutter receives the Supabase publishable key, never `service_role`, database credentials, Vault secrets, or a signing key.

Rotation imports a new asymmetric key as standby, deploys backend support for its `kid`, promotes it to current in Supabase, switches token minting, waits the five-minute token TTL plus one minute of clock-skew allowance, verifies old and new tokens through the overlap, then revokes the old key and removes its private material. Rollback reverses the minting selection while the old key remains accepted. Legacy `HS256` is disabled by default and may be used only as a documented temporary compatibility exception with dashboard evidence, an expiry date, and polling kept as the client default.

`realtime_invalidations` is backend-only and is not in the Realtime publication. Its insert trigger sends metadata to private topic `group:<uuid>`. RLS on `realtime.messages` permits authenticated receive only after a `SECURITY DEFINER` function with an empty fixed `search_path` verifies JWT session ownership, revocation, expiry, and active group membership derived from `realtime.topic()` at channel authorization time. Supabase does not repeat that live database lookup for every delivered message. If revocation occurs after join, minimal invalidation metadata can remain visible until token expiry plus at most `60s` clock skew. The client attempts refresh before expiry; the backend immediately rejects refresh for a revoked session or membership, and the channel disconnects or fails reauthorization by the bound. Every REST snapshot independently runs the existing live checks and fails immediately after revocation. Clients cannot send Broadcast messages. Cross-group joins are denied, and invalidation cleanup sends no Broadcast and no Postgres Changes `DELETE`.

`/api/v1/sync/versions` verifies the cursor HMAC, live session, and current memberships on every page. Cursor sequence positions are traversal state only and never replace row authorization. Responses omit inactive groups and provider or domain payloads, and the membership version directs the client to an authoritative group-list refresh.

The internal dispatcher and drain endpoints accept only an exact bearer secret with constant-time comparison. Supabase Vault and Vercel hold the same value. All permitted methods return `Cache-Control: no-store`, accept no target job ID, and follow no redirect. The drain body carries only a dispatcher-issued slot reservation, not caller-selected work. Capacity is enforced by the dispatcher lease and `job_drain_slots`, not memory. Logs contain identifiers without secrets, lease tokens, or job payloads.

### Configuration required

| Variable or setting | Value | Purpose |
|---|---|---|
| `APP_RUNTIME_ROLE` | `api` in production | rejects persistent compositions |
| `DATABASE_URL` | Supabase transaction pooler on `6543` | serverless PostgreSQL access |
| `DB_POOL_MODE` | `transaction` | enables compatible pgx execution |
| `DB_MAX_CONNS`, `DB_MIN_CONNS` | `2`, `0` | per invocation client ceiling |
| `DB_ACQUIRE_TIMEOUT_MS` | `1000` | fails fast under pool pressure |
| `DB_IDLE_IN_TRANSACTION_TIMEOUT_SECONDS` | `5` | terminates abandoned transactions |
| `DB_APPLICATION_NAME` | selected by route class | separates API, job, and health evidence |
| `JOB_PROCESSING_ENABLED` | feature flag | disables claims without deleting durable jobs |
| `JOB_TRIGGER_SECRET`, `CRON_SECRET` | same random secret, at least 32 bytes | authenticates Supabase and Vercel triggers |
| `JOB_BATCH_SIZE` | `5` | invocation work ceiling |
| `JOB_DISPATCHER_TIMEOUT_SECONDS` | `5` | short capacity calculation and bounded drain-request enqueue budget |
| `JOB_DISPATCHER_LEASE_SECONDS` | `15` | fences overlapping dispatcher activation and permits recovery |
| `PG_NET_DISPATCH_TIMEOUT_MS` | `10000` | caller timeout for the five-second dispatcher with response margin |
| `JOB_INVOCATION_TIMEOUT_SECONDS` | `45` | conservative PaySplit wall-clock budget, not the Vercel plan maximum |
| Vercel function `maxDuration` | `50` | enforces a platform ceiling above the application budget |
| `JOB_STOP_CLAIMING_AFTER_SECONDS` | `40` | reserves five seconds for completion, release, and response |
| `JOB_EXTERNAL_TIMEOUT_SECONDS` | `35` | maximum for one external operation, further bounded by remaining invocation time |
| `JOB_LEASE_SECONDS`, `JOB_DRAIN_SLOT_LEASE_SECONDS` | `75`, `75` | recover work and one of ten global drain slots after termination |
| `PG_NET_DRAIN_TIMEOUT_MS` | `55000` | explicit caller timeout exceeding the 45 second application budget by ten seconds |
| `SUPABASE_URL`, `SUPABASE_PUBLISHABLE_KEY` | project public values | Flutter Realtime connection |
| `SUPABASE_REALTIME_JWT_PRIVATE_KEY` | backend-only ES256 private key | signs short-lived Realtime tokens for an imported Supabase key |
| `SUPABASE_REALTIME_JWT_KID` | active imported key ID | selects the public verification key and supports rotation |
| `SUPABASE_REALTIME_JWT_ISSUER`, `SUPABASE_REALTIME_JWT_AUDIENCE` | project issuer, `authenticated` | validates token origin and intended consumer |
| `SUPABASE_REALTIME_LEGACY_HS256_SECRET` | unset by default | temporary evidence-backed compatibility only, never the target design |
| `REALTIME_TOKEN_TTL_SECONDS` | `300` | bounds stale session exposure |
| `MOBILE_REALTIME_MODE` | `supabase` or `polling` | remote rollback for new Flutter clients |
| `REALTIME_POLL_INTERVAL_SECONDS` | `10` | mandatory client fallback |
| `REALTIME_POLL_JITTER_PERCENT` | `20` | spreads fallback polling and caps simultaneous poll demand |
| `REALTIME_MAX_GROUP_CHANNELS` | `10` | one WebSocket per device, at most ten private group channels |
| `SYNC_VERSIONS_PAGE_LIMIT`, `SYNC_VERSIONS_MAX_BYTES` | `500`, `262144` | bounds one consolidated delta response |
| `SYNC_VERSIONS_MAX_PAGES_PER_CYCLE` | `4` | prevents an unbounded polling burst; overflow selects authoritative resync |
| `SYNC_CURSOR_HMAC_KEY` | random backend secret, at least 32 bytes | authenticates opaque cursor state without granting data authorization |
| Vercel region | `sin1` | colocates functions with Supabase `ap-southeast-1` |
| Supavisor backend pool size | `15` | bounds PaySplit backend database use |
| Supabase Realtime pools | authorization 3, subscription 2, stream 1 | reserves six managed Realtime connections |

### Connection budget

The current Supabase dashboard shows a database limit of 60, which matches Nano or Micro compute. Official limits allow 200 Supavisor clients for this tier.

Client demand is:

`C = 2 × (I_public + I_dispatch + I_drain)`, where `I_public` already includes fallback polls.

The 50-public-invocation envelope contains at most 30 normal requests plus 20 simultaneous consolidated polls; poll requests are not an extra class outside that 50 and do not grow with group count. One globally leased dispatcher is additional. The production design envelope is therefore `2 × (50 + 1 + 10) = 122`, leaving 78 Supavisor client slots. Fifty poller devices participate in the load profile, but stable per-device scheduling plus `20%` jitter spreads their requests; exceeding 20 simultaneous polls fails the design test and keeps broader rollout blocked until the load is retuned. Vercel can scale beyond this envelope, so `DB_MAX_CONNS=2` is not a global cap. Pool acquisition fails within one second, Vercel WAF limits abusive sources and internal paths, the dispatcher lease caps dispatcher work at one, database drain slots cap drain work at ten, and alerts fire at 100 clients. Preview deployments use a separate database or a maximum of one connection and do not join production load tests.

The Realtime budget counts WebSockets rather than loosely named subscriptions: 150 concurrent device WebSockets, each with at most ten private group channels. Channel joins are jittered. The dashboard's actual connection, channel, join-rate, and message limits must be recorded before this envelope is accepted.

PostgreSQL backend allocation is:

| Consumer | Budget |
|---|---:|
| Supavisor transaction pool for PaySplit | 15 |
| Supabase Realtime authorization, subscription, and stream | 6 |
| Supabase Cron concurrent work | 2 |
| Observed Auth, Storage, Supabase admin, and pooler roles | 7 |
| Migration and operator reserve | 10 |
| Emergency headroom | 20 |
| Total | 60 |

Each invocation closes its pool instead of relying on a Go equivalent of Vercel `attachDatabasePool`, which is not available for this runtime. Static bank data and safe provider clients may be warm reused. Database correctness and cleanup never rely on warm reuse.

### Critical test scenarios

1. Production boot test starts the Vercel handler with role `api` and proves no server, River, listener, worker loop, or periodic goroutine starts, verifies **AC-1**, **AC-3**.
2. Transaction pool integration runs representative sqlc queries with `QueryExecModeExec`, checks invocation pool closure and one second acquisition failure, verifies **AC-2**, **AC-15**.
3. Realtime security test uses valid, expired, wrong-user, inactive-member, and cross-group tokens against private Broadcast channel joins. It then revokes a session and a membership immediately after successful join: REST snapshot access fails immediately, token refresh fails, minimal Broadcast delivery stops no later than `300s + 60s`, and reconnect is denied. Deleting the durable row produces no Broadcast or Postgres Changes event for any client, verifies **AC-4**, **AC-5**.
4. Realtime ordering test delivers duplicate, delayed, dropped, and reversed invalidations. Flutter never regresses and reconnect snapshot converges, verifies **AC-6**, **AC-7**.
5. Consolidated polling test gives users 1, 10, 25, and more than 100 active groups, changes aggregates across subscribed and unsubscribed groups, revokes one membership, creates more than 500 changes, and overlaps concurrent mutation commits. Steady state remains one request per device per interval; paging stays within 500 entries, 256 KiB, and four pages; commit-ordered cursor allocation misses no invalidation; counter-lock latency remains inside **AC-16**; membership change causes one group-list refresh; and no unauthorized version metadata appears, verifies **AC-6**, **AC-7**, **AC-15**, **AC-16**.
6. Route test proves production registers no bill or group SSE route and starts no listener or hub; release evidence confirms no supported installed version requires those routes, verifies **AC-8**.
7. Asymmetric token test verifies `ES256`, `kid`, issuer, audience, subject, session, issue time, expiry, public-key validation, five-minute expiry, staged rotation, two controlled private-key stores, and rejection after revocation. The mobile mode remains polling until this passes, verifies **AC-5**.
8. Job atomicity test rolls back a business transaction and proves its job does not exist. A repeated commit returns the same logical job, verifies **AC-9**.
9. Drain-slot edge test covers no free slot, valid slot but no due job, a job becoming due concurrently, duplicate activation, and a stale invocation releasing a reclaimed slot. Only the current token can activate or release; valid no-work always releases before `204`, verifies **AC-10**, **AC-11**.
10. Dispatcher test enqueues 100 due short jobs through one real business transaction. One coalesced activation computes ten slots and queues ten drains; after the first wave's last release, one fenced re-arm creates the next wave. No manual drain is started, at most ten owners exist, all 100 jobs complete within the **AC-16** SLO, and the observed formula equals `min(free_slots, ceil(due_jobs / 5))`, verifies **AC-10**, **AC-12**, **AC-16**.
11. Dispatcher recovery test loses, duplicates, and overlaps dispatcher and drain requests, inserts a newer generation during an older wave, and force-expires reservations. Generation CAS never acknowledges newer work, existing backlog continues without another insert, minute Cron repairs lost activation, and no more than ten drains activate, verifies **AC-10**, **AC-11**, **AC-12**.
12. Timeout test verifies `10,000ms` dispatcher and `55,000ms` drain caller timeouts, terminates after the provider result and before completion, then reclaims after the `75s` lease. HTTP outcome never marks completion and stable effects produce one committed and user-visible result, verifies **AC-10**, **AC-12**, **AC-13**, **AC-17**.
13. Trigger security test covers dispatcher GET and POST, drain POST, missing and wrong secrets, unsupported methods, `Cache-Control: no-store`, overlapping Supabase and Vercel triggers, minute recovery, and daily recovery, verifies **AC-11**, **AC-12**.
14. Health test confirms liveness never touches the database, readiness has a one-second deadline, and responses contain no internal detail, verifies **AC-14**.
15. Full load profile runs the exact concurrency, duration, WebSocket, channel, consolidated-polling, dispatcher-wave, and latency thresholds in **AC-16** and inspects Supavisor clients, dispatcher/slot leases, and `pg_stat_activity` by `application_name`, verifies **AC-15**, **AC-16**, **AC-17**.
16. Rollback rehearsal disables job claims and dispatcher activation, selects polling through app config, rolls back to the serverless-safe baseline, and proves durable work and wakeup generations remain, verifies **AC-18**.

## Build plan

1. Add a Vercel Go handler, `vercel.json`, `sin1` placement, role validation, invocation-scoped transaction pool configuration, exact dispatcher/drain methods, simple health endpoints, and serverless boot tests, satisfies **AC-1**, **AC-2**, **AC-3**, **AC-12**, **AC-14**, **AC-15**.
2. Add one migration for `app_jobs`, ten seeded stateful `job_drain_slots`, generation- and wave-fenced `job_wakeup_state`, singleton `sync_sequence_state`, commit-ordered `realtime_invalidations`, `bills.sync_version`, `users.membership_sync_version`, indexes, private Broadcast RLS and trigger, extensions, Vault-backed dispatcher activation, and Supabase Cron schedules. Regenerate sqlc, satisfies **AC-4**, **AC-5**, **AC-7**, **AC-9**, **AC-10**, **AC-11**, **AC-12**.
3. Replace River insertion and workers with durable job enqueuers, the globally leased capacity-aware dispatcher, and bounded handlers for notification, OCR, bulk finalize, cleanup, retention, and settlement work. Add stable effect IDs, reservation activation, wave re-arm, generation CAS, explicit caller timeouts, and lost/duplicate/timeout recovery tests, satisfies **AC-9**, **AC-10**, **AC-11**, **AC-12**, **AC-13**, **AC-17**.
4. Insert invalidations in business transactions, add bill snapshot sync and OCR fields, import an asymmetric Supabase signing key, add the Realtime token endpoint and private Broadcast RLS integration tests, satisfies **AC-4**, **AC-5**, **AC-7**.
5. Add the bounded `/api/v1/sync/versions` cursor endpoint and its OpenAPI contract, then update Flutter for private Supabase Realtime Broadcast invalidations, at most ten channels per WebSocket, REST snapshot reconciliation, bounded-stale revocation, token refresh, membership-version refresh, app config, duplicate and ordering fences, and consolidated jittered polling, satisfies **AC-5**, **AC-6**, **AC-7**.
6. Remove production SSE routes, listeners, and hubs after the installed-client evidence gate passes; update OpenAPI and Flutter references, satisfies **AC-8**.
7. Add exact dispatcher GET/POST and drain POST authentication, health, connection observability, application names, WAF runbook, wave/backlog metrics, and the concrete load and rollback suites, satisfies **AC-12**, **AC-14**, **AC-15**, **AC-16**, **AC-17**, **AC-18**.
8. Execute the staged migration, copy pending supported River jobs, switch mobile mode only after JWT and cross-group proof, observe the stated thresholds, and retain the rollback-safe deployment, satisfies **AC-5**, **AC-8**, **AC-9**, **AC-16**, **AC-18**.

The plan uses thin end to end slices. Each slice produces a serverless safe path that can be exercised before it becomes the production owner.

## Review finding resolutions

| Previous finding | Resolution |
|---|---|
| Missing bridge between Vercel bill mutations and realtime publication | The mutation inserts a durable invalidation in its transaction. An insert trigger sends a private Broadcast invalidation to Flutter, which fetches REST state. |
| Production `all` role contradicted rollback | Production accepts only `api`. Rollback uses a serverless safe Vercel deployment and feature flags. |
| Existing Flutter realtime URL migration | New clients use private Supabase Broadcast plus polling fallback. Repository evidence shows no released app, so SSE is removed; dashboard and store evidence remains a pre-implementation gate. |
| No guaranteed single worker owner | There is no worker owner. Ten database drain slots, job leases, and `SKIP LOCKED` coordinate bounded overlapping invocations. |
| Per replica connection limit was not global | The spec defines Supavisor client and PostgreSQL backend formulas, a 61-invocation envelope including one dispatcher, alert points, and headroom. |
| Readiness was ambiguous | Liveness executes only. Readiness performs one bounded database probe. |
| Subscriber backpressure and ordering were undefined | Realtime carries invalidations only. Monotonic versions, snapshot reconciliation, and polling repair duplicates, reordering, and loss. |
| Representative load was undefined | **AC-16** defines concurrency, duration, latency, error, job, and duplicate thresholds. |
| Staged version compatibility was undefined | App config controls new-client mode. Because no installed release is evidenced, production SSE is removed instead of carrying an unproven bridge. |
| `pg_net` timeout could terminate before the drain | Dispatcher activation specifies `10,000ms` for a `5s` budget; drain activation specifies `55,000ms` for a `45s` budget under the `50s` ceiling. Independent minute Cron repairs lost calls. |
| Ten drain invocations were only an assumption | Exactly ten leased database slot rows enforce global capacity and recover after `75s`. |
| One wakeup per inserted job could fan out | A singleton generation and statement-level trigger coalesce committed inserts into one dispatcher activation. The dispatcher reserves a bounded capacity wave instead of emitting one request per job. |
| Postgres Changes `DELETE` bypasses row filtering | The durable table is not published. Insert-only private Broadcast is RLS-authorized; cleanup emits no Realtime event. |
| Shared Realtime JWT secret lacked rotation | The target is imported `ES256` with `kid`, public verification, staged rotation, and polling until proof. |
| Polling and Realtime load accounting was ambiguous | The 50 concurrent public API total explicitly contains 30 normal requests and up to 20 consolidated jittered polls; one dispatcher and ten drains are additional. WebSockets and channels per WebSocket are counted separately. |
| Drain methods and 45-second claim were ambiguous | Supabase calls dispatcher POST, Vercel Cron calls dispatcher GET, and only the dispatcher calls drain POST. Every method is no-store and secret-authenticated; 45 seconds is the drain budget, not a plan maximum. |
| Markdown escaping and rationale link were broken | Raw repository files already use normal Markdown and the index links directly to `./rationale.md`; no Markdown repair was required in this review. |
| One coalesced request could process only five backlog jobs | One short dispatcher activation creates `min(free slots, ceil(due/5))` token-bound drains. Wave completion re-arms the dispatcher until due backlog is empty, while generation CAS prevents lost newer work. |
| Broadcast revocation was described as immediate | Issuance and join check live authorization; post-join invalidation delivery may remain for at most the five-minute TTL plus one-minute skew. REST denies authoritative access immediately. |
| Users above ten groups could poll once per group | `/api/v1/sync/versions` consolidates authorized deltas behind one cursor request per device, with bounded change-volume paging and membership-version refresh. |
| Asymmetric private-key trust boundary was inaccurate | Private material is limited to Vercel secrets and Supabase managed signing-key storage, and is excluded from Flutter, source, logs, responses, and public configuration. |
| Valid slot with no claimable job was ambiguous | A valid no-work drain releases by current token before `204`; tests cover no slot, no due job, concurrent due work, duplicate activation, and stale release. |

## Consequences

**Positive**:

1. Vercel autoscaling creates no permanent application sessions or duplicated workers.
2. Realtime authorization is tied to PaySplit sessions and group membership at token issuance and private-channel join, with an explicit bounded-stale revocation window and immediate REST enforcement.
3. Durable jobs survive function termination, lost wakeups, deployment, and rollback.
4. Vercel and Supabase run in the same AWS region.

**Negative and tradeoffs**:

1. The durable queue, leases, retries, RLS, and migration are application owned instead of River owned.
2. Realtime adds a second short-lived token, bounded stale invalidation authorization after revocation, asymmetric key rotation, private-channel RLS, and Supabase client configuration to Flutter.
3. Database slot leasing, generation fencing, wave dispatch, and wakeup coalescing add application-owned coordination state and recovery tests.
4. Commit-ordered consolidated polling adds one short global counter-row lock to visible mutation transactions; the latency threshold must be proven under the representative load.
5. Exactly-once provider transport is impossible after an ambiguous network result when the provider has no idempotency API. The design guarantees one committed business effect and one user-visible effect ID through stable identities, compare-and-set writes, and client deduplication. A physical FCM or OCR request may be repeated.
6. Supabase Cron and `pg_net` become operational dependencies. Polling and daily Vercel recovery reduce, but do not remove, delayed job risk.

**Neutral**:

1. REST business behavior, authorization, and live session validation remain the authority.
2. Existing River tables remain during the rollback window but are not consumed in the final production composition.
3. The plan remains Hobby compatible because no paid Vercel plan evidence exists in the repository.
4. Existing SSE compatibility is intentionally absent because the repository contains no evidence of installed releases; this must be confirmed externally before implementation.
5. Users with more than ten groups receive prompt Broadcast only for their ten active channels; one consolidated cursor poll covers the remaining groups without linear request growth.

## Follow-up

1. [ ] Record the actual Vercel plan, Go Function duration, project region, Supavisor pool size, Realtime connection/channel/join-rate settings, and client limit from both dashboards before cutover.
2. [ ] Capture project wide Supabase, RLS, and PostgreSQL connection conventions from the installed skills in root `AGENTS.md` before implementation.
3. [ ] Remove River tables in a later migration only after the supported legacy queue is empty and the rollback window ends.

## Migration plan

**Strategy**: Feature flagged strangler migration inside Vercel and Supabase.

**Phases**:

1. Deploy a serverless safe Vercel handler with all new paths disabled. Record this deployment as the rollback baseline. Existing REST remains active.
2. Apply additive database changes for jobs, drain slots, generation- and wave-fenced wakeup state, commit-ordered sync sequence, invalidations, bill and membership sync versions, private Broadcast RLS, Vault, `pg_net`, and Supabase Cron. Keep dispatcher, job claims, and client Realtime disabled.
3. Enable `app_jobs` insertion for one job kind at a time. Copy pending supported River rows using kind specific idempotency keys. Do not dual execute one logical job. Confirm the old queue reaches zero before disabling its path.
4. Import the asymmetric Supabase signing key, prove `kid` verification and rotation, then deploy Flutter with private Broadcast invalidation, jittered polling fallback, app config, and version fencing. Keep `MOBILE_REALTIME_MODE=polling` until cross-group tests pass, then enable Realtime for a staged cohort.
5. Confirm there are no supported installed clients, remove production SSE routes, listeners, hubs, and periodic startup from the Vercel composition, and update OpenAPI. If the premise is false, stop and return the spec to design review.
6. Enable the coalesced capacity-aware dispatcher and minute Cron, run the exact load profile through real enqueue transactions, inspect dispatcher generations, waves, slots, Supavisor, PostgreSQL, polling, WebSocket, and channel evidence, and expand the mobile cohort only while thresholds hold.
7. Retain durable job and invalidation data through the rollback window.

**Rollback**: Set `JOB_PROCESSING_ENABLED=false` so no dispatcher reserves a slot and no new job lease is claimed, while durable rows and generations remain. Set `MOBILE_REALTIME_MODE=polling` so new clients stop depending on Realtime. Disable Supabase Cron and webhook activation, and manually disable Vercel Cron because Vercel Instant Rollback does not update active Cron schedules. Roll back to the recorded serverless-safe Vercel deployment. Keep additive tables, invalidations, jobs, and wakeup generations. After the fault is corrected, re-enable the dispatcher and drain the backlog. Rollback never starts River or role `all`.

**Risks**: An ambiguous provider response may repeat a physical request even though the committed effect is deduplicated. A lost dispatcher or complete wave of drain requests can delay work until lease reconciliation or the next minute Cron. Incorrect private-channel RLS can leak group identifiers, and a correctly joined client may retain minimal invalidation access for up to token TTL plus clock skew after revocation; REST remains the immediate authoritative boundary. Polling remains the default until cross-group and revocation-bound tests pass. If external release evidence reveals installed SSE clients, removing those routes would break them and implementation must pause for a separately bounded compatibility design.

## Worked consistency examples

### Burst of 100 due jobs

Assume ten slots are free, `JOB_BATCH_SIZE=5`, all jobs are immediately due and short, and no request is lost:

1. One business transaction inserts 100 jobs, advances `requested_generation` once for its statement, and creates one dispatcher activation.
2. Dispatcher wave 1 sees 100 due jobs and ten free slots. It computes `min(10, ceil(100 / 5)) = 10`, reserves ten slots, and queues ten drain calls.
3. Those ten drains process five jobs each, for 50 completed jobs. Each token-releases its slot; only the last matching release changes `wave_outstanding` to zero and queues one fenced dispatcher activation.
4. Dispatcher wave 2 sees 50 due jobs and ten free slots. It computes `min(10, ceil(50 / 5)) = 10` and queues ten more drain calls.
5. The second wave processes the remaining 50. Its last release sees no due job and no newer generation, then advances `acknowledged_generation` to the wave's observed generation.

The normal path therefore uses two dispatcher calls and 20 drain calls, with at most ten drain owners at any instant and no manually started invocation. With a worst-case `45s` drain budget plus two short dispatcher activations, two waves fit inside the two-minute load-test target; the SLO still requires measured proof. Lost calls, failures, or newly enqueued generations may add recovery calls but cannot exceed the ten active-slot cap.

### Revocation after channel join

1. At `T+0`, the backend verifies the PaySplit session and issues a Realtime JWT expiring at `T+300s`; Supabase verifies membership when the client joins its private group channel.
2. At `T+60s`, the session or membership is revoked. The next REST snapshot immediately returns the existing 401 or 403 result, and every later Realtime token refresh is rejected.
3. Because Broadcast authorization is cached after join, minimal invalidation metadata may still arrive on the existing channel. It never contains authoritative bill, payment, member, or OCR data.
4. A normal pre-expiry refresh attempt disconnects or fails authorization earlier. Regardless of client behavior, the old channel authorization ends no later than JWT expiry plus the `60s` clock-skew allowance, at `T+360s` in this example.
5. Reconnect after that point is denied. The client can never turn a stale invalidation into data because REST has enforced revocation since `T+60s`.

## Final pre-acceptance gates

The architecture is conditionally accepted. The ADR remains **Proposed**, and implementation plus production rollout remain blocked until all seven gates have recorded evidence:

1. [ ] **Vercel limits**: record the actual plan, Go Function hard duration, configured `maxDuration`, Cron frequency and method, project region, concurrency controls, and WAF/rate-limit capability.
2. [ ] **Supabase limits**: record the actual compute tier, PostgreSQL connection limit, Supavisor client and backend pool limits, Realtime WebSocket/channel/join-rate limits, Cron concurrency, and installed `pg_net` extension version.
3. [ ] **JWT proof**: import an asymmetric signing key and demonstrate the two controlled private-key stores, `ES256` public verification by `kid`, required claims, five-minute expiry, staged rotation, rollback during overlap, refresh denial after revocation, and channel expiry within TTL plus skew. Keep mobile mode `polling` until this passes.
4. [ ] **Realtime isolation proof**: demonstrate that an active member receives only its private-group `INSERT` invalidations, a cross-group join is denied, clients cannot send, invalidation-row `DELETE` cleanup produces no Broadcast or Postgres Changes event, REST denies immediately after post-join revocation, and Broadcast delivery ends within the documented bound.
5. [ ] **`pg_net` timeout proof**: demonstrate explicit `10,000ms` dispatcher and `55,000ms` drain POST timeouts against their `5s` and `45s` application budgets and Vercel ceilings, forced caller timeout without false completion, and independent recovery by the next minute Cron.
6. [ ] **Dispatcher and drain-cap proof**: enqueue 100 due jobs through one real transaction and demonstrate capacity-aware waves, generation fencing, no more than one dispatcher lease or ten valid slot owners, no-slot and token-released no-work `204`, stale-release rejection, expired `75s` slot recovery, and the two-minute SLO without manually started drains.
7. [ ] **Legacy-client decision**: record App Store, Play Store, Vercel analytics, release tags, and supported app-version evidence confirming no installed client needs SSE. If any supported client does, do not accept this spec until a globally leased compatibility path and its connection budget are designed.
