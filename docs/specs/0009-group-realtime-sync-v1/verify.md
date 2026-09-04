# Verify: User realtime stream v1 · spec 0009 · updated 2026-09-03

_Steps derived from spec 0009 acceptance criteria. `/check verify` runs these; `/test` locks the durable ones._

## UI / manual

- [ ] Sign in on Flutter, confirm one `GET /api/v1/users/me/events` stays open while Home, group detail, and bill detail change screens → AC-14, AC-17
- [ ] Join a group from a second device and watch the first device roster plus Home cards update without pull to refresh → AC-16, AC-19
- [ ] Background the app (`paused` or `hidden`), then resume; stream closes then reconnects, runs ready refetch, and last good data remains visible if a refetch fails → AC-17, AC-18
- [x] Sign in on a second device with the same account; the first stream receives `close` reason `replaced` or `session_ended` → AC-14
- [ ] With `REALTIME_MODE=auto`, point at a backend with `USER_SSE_ENABLED=false` and confirm Flutter falls back to legacy group/bill SSE only before any `ready` → AC-23
- [ ] After a successful `ready`, force a `503` or network drop; Flutter stays in user mode and does not open a legacy stream → AC-23

## Commands

- [x] `USER_SSE_ENABLED=true` then `GET /api/v1/users/me/events` with a live bearer → `200` `text/event-stream`, first frame `ready` with `stream_id` and RFC3339 `timestamp` → AC-13, AC-14, AC-15, AC-16
- [ ] Same request while the shared listener is disconnected → `503 REALTIME_UNAVAILABLE` before SSE headers → AC-13
- [x] Eleven subscribe attempts in 60 seconds for one `sid` → eleventh is `429 RATE_LIMITED` with `Retry-After` → AC-14
- [x] `USER_SSE_ENABLED=false` → `404` on `/users/me/events` → AC-23
- [x] Count `pg_stat_activity` `LISTEN` sessions for `DB_APPLICATION_NAME` with Home, group, and bill streams open → exactly one session listening on `group_events`, `bill_events`, and `user_events` → AC-13
- [ ] Sign out, password change, password reset, refresh reuse, admin suspend/lock → each revoked `sid` from `UPDATE ... RETURNING id` closes with `session_ended` in the same transaction → AC-14
- [ ] Create, edit, review, delete draft, finalize, void a bill; roll back one forced failure → only committed rows emit one `invalidate` with the returned `bills.version` → AC-20
- [ ] Queue, process, succeed, and fail OCR; waiter does GET before wait, after ready, and on 60s timeout → public `ocr.updated` has job fields without `audience_user_ids` → AC-21
- [ ] Lock bill submissions then run create payment, submit proof, confirm, reject, remind, and stalled alert → invalidation types and audiences match the settlement matrix; confirm Home balance only reaches debtor and creditor → AC-22
- [ ] Parallel roster mutations plus a dropped legacy frame → versions stay gapless and `/sync` heals → AC-1 through AC-12
- [x] `X-App-Version: 1.4.0+27` on user and legacy SSE; missing header classifies as `unknown` in `paysplit_legacy_sse_requests_total` → AC-23
- [x] Publish one `pg_notify` through `DATABASE_URL` and observe it on `DATABASE_LISTENER_URL` before production traffic → AC-13

## Value sourcing

- [x] `stream_id` is a server UUID v7, never taken from the request → AC-14
- [ ] Replacement winner is commit order of `stream.replace`; the surviving stream is `replacement_stream_id` → AC-14
- [ ] Session `target_sids` equal the IDs returned by the revocation `UPDATE` → AC-14
- [x] Roster public `data` has `avatar_url` and never `avatar_object_key` or `audience_user_ids` → AC-9, AC-15
- [ ] Bill invalidation `resource_version` is the locked `bills.version`; settlement invalidations omit `resource_version` and still refetch → AC-20, AC-22
- [ ] OCR timestamps and attempts come from the committed `ocr_jobs` row; missing warnings are `[]`; nonfailed error is `null` → AC-21
- [ ] Flutter `X-App-Version` comes from `package_info_plus` (`major.minor.patch+build`) or `unknown` → AC-23
- [x] `Retry-After` is remaining time in the local 10 per 60s per `sid` window, rounded up → AC-14

## Acceptance-criteria coverage

- AC-1 through AC-12 covered by roster mutation plus `/sync` heal step
- AC-13 covered by listener session count, 503 admission, and DSN round trip
- AC-14 covered by subscribe, replace, rate limit, and session revocation steps
- AC-15 covered by public frame inspection
- AC-16 through AC-18 covered by ready refetch, buffer overflow, lifecycle, and coalesce steps
- AC-19 covered by join/leave/archive audience checks
- AC-20 covered by bill mutation commit/rollback
- AC-21 covered by OCR GET plus `ocr.updated`
- AC-22 covered by lock and settlement matrix
- AC-23 covered by exclusive mode HTTP matrix and version buckets
