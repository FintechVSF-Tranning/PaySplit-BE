# Verify: Connection efficient events · spec 0010 · updated 2026-09-03

_Steps derived from spec 0010 acceptance criteria. `/check verify` runs these; `/test` locks the durable ones._

## Runtime and database

- [x] Start one backend instance with a unique `DB_APPLICATION_NAME`, `RIVER_POLL_ONLY=false`, and a healthy PostgreSQL database. Query `pg_stat_activity` by that application name and record the total physical sessions, acquired pool slots, and three idle sessions whose last query is `LISTEN` → AC-10
- [x] Send interleaved valid notifications to `bill_events` and `group_events`. Confirm one shared listener session receives both, each notification reaches only its matching Hub, same channel ordering is preserved, Bill subscriber capacity remains `16`, and Group subscriber capacity remains `32` → AC-1, AC-2
- [x] Terminate the shared listener backend connection while Bill and Group SSE clients are connected. Confirm `/health/ready` returns `503`, both existing streams close, the listener reconnects with bounded backoff, both channels are registered before readiness returns `200`, Bill reloads its snapshot, and Group recovers through version fencing plus `/sync` → AC-3, AC-12
- [x] Send malformed JSON and semantic failures for each channel. Cover missing Bill ID, missing Bill type, missing Group ID, nonpositive Group version, and missing Group type. Confirm each notification is dropped, the other channel continues, the invalid payload counter increments only for the allowed channel, and no raw payload appears in logs → AC-4
- [x] Stop the backend gracefully while the listener is idle. Confirm River stops before the listener, the listener runs `UNLISTEN *`, the connection returns cleanly to the pool, shutdown respects its timeout, and no listener goroutine or acquired connection remains → AC-9
- [ ] Force the second `LISTEN` command or `UNLISTEN *` cleanup to fail in an isolated test database. Confirm the underlying connection is closed and never returned to the pool with session state attached → AC-9

## River polling

- [x] Start with `RIVER_POLL_ONLY=true`, `RIVER_FETCH_COOLDOWN_MS=100`, and `RIVER_FETCH_POLL_INTERVAL_MS=1000`. Confirm startup logs show poll only mode and the resolved interval without exposing `DATABASE_URL` → AC-5, AC-11
- [x] Query `pg_stat_activity` using the instance `DB_APPLICATION_NAME`. Confirm the shared Bill and Group listener is the only application `LISTEN` session and River has no notifier listener session. River may still execute `NOTIFY` during enqueue → AC-5, AC-10
- [x] Enqueue a job inside a transaction, verify it cannot run before commit, then commit while the queue is idle. Confirm River starts it through polling. Record commit to fetch latency against the theoretical `0 ms` to under `1100 ms` window and apply a separate test tolerance for runtime and database delay → AC-5, AC-7
- [ ] Verify retry, scheduled jobs, periodic jobs, and transactional enqueue behave as before while poll only is enabled → AC-5, AC-7
- [x] Set `RIVER_FETCH_POLL_INTERVAL_MS=0`, then set it below `RIVER_FETCH_COOLDOWN_MS`. Confirm startup fails and names `RIVER_FETCH_POLL_INTERVAL_MS` and `RIVER_FETCH_COOLDOWN_MS` in the validation error → AC-6
- [x] Set `RIVER_POLL_ONLY=false` and restart the process. Confirm the River notifier listener session returns without code changes or database migration → AC-8

## Commands

- [x] `go test -race ./internal/platform/database ./internal/modules/bill/delivery/http ./internal/modules/group/delivery/http ./internal/platform/queue/river ./internal/config ./internal/platform/metrics ./internal/transport/http/router ./internal/bootstrap` → all packages pass → AC-1 through AC-9, AC-11, AC-12
- [x] `TEST_DATABASE_URL=<isolated-postgres-url> go test -count=1 -run Integration ./internal/platform/database ./internal/platform/queue/river` → both integration suites pass and connection assertions match → AC-1, AC-2, AC-5, AC-7, AC-9, AC-10
- [x] `go test ./...` → the complete backend suite passes with live provider credentials disabled unless those integrations are intentionally under test → AC-2, AC-5, AC-8, AC-9, AC-11
- [x] `git diff --check` and `rg -n 'StartPostgresListener|func \(h \*Hub\) listenLoop' internal/modules internal/bootstrap` → clean diff and no legacy listener loop remains → AC-1, AC-9, AC-11

## Acceptance criteria coverage

- AC-1 is covered by runtime steps 1 and 2, plus integration commands.
- AC-2 is covered by runtime step 2 and the complete backend suite.
- AC-3 is covered by runtime step 3.
- AC-4 is covered by runtime step 4.
- AC-5 is covered by River steps 1 through 4.
- AC-6 is covered by River step 5.
- AC-7 is covered by River steps 3 and 4.
- AC-8 is covered by River step 6.
- AC-9 is covered by runtime steps 5 and 6, plus race tests.
- AC-10 is covered by runtime step 1 and River step 2.
- AC-11 is covered by runtime and command comparison steps.
- AC-12 is covered by runtime step 3.
