# 0010. Vercel only serverless runtime: Rationale

**Architecture review**: Conditionally accepted on 2026-08-28. This records agreement with the design direction, not permission to implement or deploy before the seven evidence gates in the index are complete.

## Context

> ⚠️ Premise note: Production hosting is Vercel only. The prior Railway split violated this constraint and preserved a second stateful runtime. The correct problem is not how to host a worker elsewhere. It is how to remove every application responsibility that needs a permanent process.

The current Go bootstrap behaves like an always active server. Every replica opens a pgx pool, runs River migration and consumers, starts periodic loops, and pins PostgreSQL connections for bill and group listeners. Vercel Functions scale to zero and may terminate after one invocation, so none of those lifecycles is a production ownership boundary.

The database dashboard shows 24 of 60 PostgreSQL connections and repeated copies of listeners and worker queries. The production environment currently allows six connections and three River workers per process. A per process limit cannot cap total Vercel clients because the platform may create more function instances.

PaySplit already has useful recovery primitives. Groups have a monotonic roster version, durable `group_events`, snapshot, and sync endpoint. Bills have optimistic versions, and OCR jobs have durable state, but bill detail does not currently expose the OCR summary required to replace SSE. Existing auth uses PaySplit JWTs and a live `sessions` lookup rather than a native Supabase client session.

No repository file identifies the current Vercel account plan. The safe assumption is Hobby because no paid capability is proven. Official Vercel documentation currently describes configurable Go Function durations and daily Hobby Cron, but the dashboard remains the authority for this project. The design uses a conservative `45s` PaySplit application budget under `maxDuration=50`; it does not claim that 45 seconds is the Hobby platform maximum. Supabase Cron owns minute scheduling.

The repository also contains no release tag, store URL, or installed-client evidence, and the Flutter package is still `1.0.0+1`. The design therefore removes SSE rather than budgeting an unproven compatibility bridge. Store and deployment analytics remain a final gate because repository absence cannot prove production absence.

## Options considered

### Option 1: Private Supabase Broadcast invalidations plus durable PostgreSQL jobs

Insert payload-free invalidations and jobs in the same transactions as business state. An insert trigger sends metadata through authenticated private Supabase Realtime Broadcast topics; the durable table is not published through Postgres Changes. Coalesced Supabase `pg_net` calls and Cron invoke database-slot-bounded Vercel handlers. (basis: existing version fencing, Supabase Broadcast authorization, asymmetric custom Realtime tokens, Supabase Cron, `pg_net`, Vercel function lifecycle)

**Pros**:

1. Preserves prompt updates and durable background work without an application process.
2. Business state, invalidations, job insertion, and Broadcast enqueue remain transactionally coupled in PostgreSQL.
3. Uses the existing Supabase provider and one Vercel deployment.

**Cons**:

1. Requires new RLS, token, queue, retry, and operational code.
2. Adds private-channel RLS, asymmetric-key rotation, provider-level Realtime budgets, and application-owned drain coordination.

### Option 2: Version based REST polling plus durable PostgreSQL jobs

Flutter polls authenticated snapshots and uses the same durable job system, with no Supabase Realtime client path. (basis: serverless stateless polling, existing snapshots and versions)

**Pros**:

1. Simplest authorization because every read uses existing middleware.
2. No Realtime JWT, private-channel RLS, or WebSocket dependency.

**Cons**:

1. Adds continuous API and database reads even when nothing changes.
2. Update latency is bounded by the polling interval rather than event delivery.

### Option 3: Direct Postgres Changes on business tables

Expose bill, group, member, and OCR tables through Supabase Realtime and let Flutter consume row changes directly. (basis: Supabase Postgres Changes)

**Pros**:

1. Fewer outbox writes and prompt row updates.
2. Native Realtime filtering and delivery.

**Cons**:

1. Expands the client data and RLS surface across sensitive domain tables.
2. Couples Flutter to storage schema and makes payload evolution harder.
3. Encourages clients to treat change payloads as authority.
4. Postgres Changes does not apply row filtering to `DELETE` events, so cleanup can expose unauthorized row metadata unless the published design avoids deletes entirely.

### Option 4: Start River or listeners inside Vercel Functions

Start River consumers, periodic tasks, or PostgreSQL listeners during a request and hope a warm instance stays active. (basis: current bootstrap and Vercel lifecycle)

**Pros**:

1. Reuses the current code with fewer immediate changes.

**Cons**:

1. Work stops when the invocation ends or instance is frozen.
2. Autoscaling duplicates consumers and permanent connections.
3. Correctness depends on undocumented process reuse.

## Rationale

Option 1 is selected because it preserves responsive mobile behavior without treating a Vercel Function as a server. Invalidations contain no domain payload, so duplicate, delayed, and reordered messages are harmless. The existing REST authorization and snapshots remain the final authority. Private Broadcast is preferred over publishing the durable invalidation table because only insert-triggered messages exist and cleanup cannot leak a Postgres Changes `DELETE`. Option 2 is the runner up and mandatory fallback because it remains secure during Realtime or signing failures. (basis: invalidation pattern, specs 0003 and 0009, Supabase Broadcast authorization)

Supabase accepts custom JWTs for Realtime and applies RLS when a client joins or reauthorizes a private Broadcast channel; it does not repeat PaySplit's live-session database lookup for every message. PaySplit therefore issues a separate five-minute token after its normal live-session check and accepts a bounded stale-metadata window after post-join revocation. REST remains the immediate authority and rejects snapshots as soon as session or membership revocation commits. The target trust model is an imported asymmetric `ES256` signing key: private material is confined to Vercel secrets and Supabase's managed signing-key store, Supabase verifies the public key selected by `kid`, and the token contains explicit issuer, audience, subject, session, issue time, and expiry claims. Rotation keeps old and new keys accepted for the five-minute TTL plus clock skew before revocation. Legacy `HS256` is not the target and requires temporary evidence if used. If imported-key verification, rotation, cross-group denial, or the revocation bound cannot be demonstrated, polling remains selected rather than accepting a public channel or `service_role` token. (basis: Supabase signing-key and Realtime authorization guidance)

River is a good queue for a persistent Go process but not for this deployment. A small job table with leases replaces the production consumer lifecycle. Exactly ten seeded `job_drain_slots` enforce global concurrency across all Vercel instances. A singleton generation coalesces a committed enqueue burst into one short dispatcher activation. The globally leased dispatcher computes `min(free_slots, ceil(due_jobs / batch_size))`, reserves that bounded wave, and queues token-bound drain POSTs. The last completed drain re-arms one dispatcher while due backlog or a newer generation remains, so a 100-job burst uses available capacity without waiting for one Cron batch per minute. Generation compare-and-set prevents an older wave from acknowledging newer work; expired reservations and minute Cron repair lost calls. Dispatcher calls use an explicit `10,000ms` timeout above their `5s` budget, while drain calls use `55,000ms` above the `45s` application budget and `50s` function ceiling. Caller timeout never implies completion. Vercel Cron supplies daily recovery compatible with Hobby. External work begins only after the job claim commits. (basis: Supabase `pg_net`, Cron, short transaction, `SKIP LOCKED`, lease, and generation-fencing patterns)

The ten-channel device cap bounds private Realtime authorization work but must not turn fallback into one poll per group. A sequenced invalidation cursor lets `/api/v1/sync/versions` return authorized version deltas for all active groups in one steady-state request. PostgreSQL identity allocation alone is not commit ordered, so the sequence comes from a singleton counter row whose lock is held through the visible mutation transaction; the representative load must prove that this short serialization point satisfies API latency thresholds. A separate membership sync version triggers one authoritative group-list refresh when membership changes. Fixed page and byte ceilings bound database and network work; limited paging depends on change volume rather than the number of groups. (basis: existing snapshot authority, commit-ordered invalidation sequence, bounded cursor pagination)

The current Supabase endpoint is in `ap-southeast-1`, so Vercel moves from the observed `hkg1` invocation region to `sin1`. This removes avoidable database round trip latency. Transaction mode on port `6543` multiplexes many clients over a backend pool and requires prepared statements to be disabled. (basis: current production endpoint, Vercel region list, Supabase connection guidance)

## References

**Project sources**:

1. `AGENTS.md`, PaySplit modular backend, PostgreSQL, Flutter, and API contract.
2. `internal/bootstrap/app.go`, current server, River, periodic, and listener lifecycle.
3. `internal/platform/database/postgres.go`, current pgx pool setup.
4. `internal/platform/queue/river/client.go`, current River migration and consumer client.
5. `internal/modules/bill/delivery/http/sse_hub.go`, current bill listener and process hub.
6. `internal/modules/group/delivery/http/sse_hub.go`, current group listener and process hub.
7. `docs/specs/0003-bill-ocr-v1/`, bill and OCR state requirements.
8. `docs/specs/0006-notification-queue-v1/`, transactional queue requirements.
9. `docs/specs/0009-group-realtime-sync-v1/`, durable group events and version fencing.
10. Current Supabase dashboard evidence, 24 of 60 database connections.
11. Installed `supabase` and `supabase-postgres-best-practices` skills.

**Practices and standards**:

1. Durable outbox and invalidation pattern.
2. Short transaction and lease based job claiming.
3. Idempotency keys and compare and set completion.
4. RLS deny by default and least privilege.
5. Strangler migration with additive database changes.
6. Fail fast connection acquisition and explicit global budgeting.
7. Database-backed concurrency leases instead of process-local limits.
8. Asymmetric signing keys with key IDs and overlap-based rotation.
9. Capacity-aware dispatch waves with generation compare-and-set fencing.
10. Consolidated cursor polling with bounded page and byte limits.
11. Bounded stale Realtime metadata with immediate REST authorization.
12. Commit-ordered cursor allocation with one documented lock order and measured contention.

**Links**:

1. Vercel Functions lifecycle: https://vercel.com/docs/functions
2. Vercel backend serverless guidance: https://vercel.com/docs/frameworks/backend
3. Vercel Go runtime: https://vercel.com/docs/functions/runtimes/go
4. Vercel function duration: https://vercel.com/docs/functions/configuring-functions/duration
5. Vercel Cron limits: https://vercel.com/docs/cron-jobs/usage-and-pricing
6. Vercel Cron security and rollback: https://vercel.com/docs/cron-jobs/manage-cron-jobs
7. Vercel regions: https://vercel.com/docs/regions
8. Supabase connection methods: https://supabase.com/docs/guides/database/connecting-to-postgres
9. Supabase connection limits: https://supabase.com/docs/guides/platform/compute-and-disk
10. Supabase connection management: https://supabase.com/docs/guides/database/connection-management
11. Supabase Realtime Broadcast: https://supabase.com/docs/guides/realtime/broadcast
12. Supabase Realtime authorization: https://supabase.com/docs/guides/realtime/authorization
13. Supabase JWT signing keys: https://supabase.com/docs/guides/auth/signing-keys
14. Supabase JWT claims and verification: https://supabase.com/docs/guides/auth/jwts
15. Supabase Postgres Changes delete behavior: https://supabase.com/docs/guides/realtime/postgres-changes
16. Supabase Realtime settings: https://supabase.com/docs/guides/realtime/settings
17. Supabase Realtime limits: https://supabase.com/docs/guides/realtime/limits
18. Supabase Database Webhooks: https://supabase.com/docs/guides/database/webhooks
19. Supabase `pg_net`: https://supabase.com/docs/guides/database/extensions/pg_net
20. `pg_net` SQL function source and timeout parameter: https://github.com/supabase/pg_net/blob/master/sql/pg_net.sql
21. Supabase Cron: https://supabase.com/docs/guides/cron
22. Supabase Vault: https://supabase.com/docs/guides/database/vault
