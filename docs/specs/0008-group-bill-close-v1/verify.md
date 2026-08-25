# Verify: Group bill close v1 · spec 0008 · updated 2026-08-24

_Steps derived from spec 0008 acceptance criteria. `/check verify` runs these; `/test` locks the durable ones._

## UI / manual

The Flutter surfaces of AC-9 live in the PaySplit-FE companion repo. Verify there:

- [ ] Locked group shows the locked state in Group Hub and every create bill entry is disabled, driven by the server `bill_submission_locked` field, never a client only flag → AC-9
- [ ] Captain sees `Khóa gửi hóa đơn` and `Chốt toàn bộ` actions; bulk finalize shows confirmation, progress, success, partial failure, empty, offline, and retry states → AC-9
- [ ] A finalized Bill Detail renders read only → AC-9

## API / commands

Setup: `docker compose up -d postgres`, `make migrate-up`, `make run`. All calls need a live bearer token for the group's members.

### AC-1 lock semantics

- [x] As Captain `POST /api/v1/groups/{groupId}/bills/lock-submissions` with `Idempotency-Key: k1` → `200` with `bill_submission_locked: true` and a nonnull `bill_submission_locked_at`; one `bill_submission_locked` activity with `group_id` and `locked_at` metadata exists → AC-1
- [x] Repeat with the same key and body → same `200` state, still exactly one lock activity → AC-1
- [x] As an active non Captain member → `403 CAPTAIN_REQUIRED`; as a nonmember or against an archived group → `404 GROUP_NOT_FOUND` → AC-10
- [x] Confirm no unlock route exists and the column never returns to null → AC-1

### AC-2 create gate

- [x] After locking: manual `POST /api/v1/bills` (JSON) by the Captain and by another member → both `409 BILL_SUBMISSION_LOCKED`; no bill row, no OCR job, no `created_bill` activity committed → AC-1, AC-2
- [ ] Image create uploaded before the winning lock: race a multipart create against the lock; either the bill commits before `locked_at` or the attempt images land in `media_cleanup_jobs` → AC-2
- [ ] `GET /bills?group_id=`, bill detail, settlement reads, and `GET /groups/{id}` keep working after the lock → AC-2

### AC-3 drafts stay editable

- [ ] A draft that existed before the lock can still be edited (`PUT /bills/{id}`), OCR retried/applied, reviewed, and hard deleted by its Creditor while locked → AC-3
- [x] The Creditor finalizes their own reviewed bill → `403 FORBIDDEN`; the current Captain finalizes it → success → AC-3

### AC-4 bulk start

- [x] As Captain `POST /groups/{groupId}/bills/finalize-all` with `Idempotency-Key: k2` → `202` with batch `queued` (or `completed` when zero targets), correct counts, and lock state set even with zero target bills → AC-4
- [x] Second call without the key while queued/processing → `409 BULK_FINALIZE_IN_PROGRESS` whose details carry the safe `active_batch_id` → AC-4
- [x] A second concurrent start cannot commit (unique partial index); exactly one active batch row per group → AC-4

### AC-5 item processing

- [x] Batch over one reviewed draft, one valid unreviewed draft, one invalid unreviewed draft: first two become `finalized` items with immutable shares/debts/notifications identical to individual finalize; invalid stays `draft` unchanged with stable `BILL_NOT_READY`; batch completes with exact counters → AC-5, AC-6
- [x] Each failed bill never rolls back another finalized bill (independent transactions) → AC-5

### AC-6 batch detail

- [ ] `GET /groups/{groupId}/bill-finalize-batches/{batchId}?limit=2` then follow `next_cursor` → pages ordered `(created_at, bill_id)`, default 20, max 100 enforced, `400 INVALID_CURSOR` on garbage cursor, `404 BATCH_NOT_FOUND` cross group → AC-6
- [x] Failed bills remain fixable while locked and finalize individually afterwards → AC-6
- [x] Hard delete a captured draft → its item records redacted `BILL_DELETED`, `bill_display_name` null, delete not blocked → AC-6

### AC-7 idempotency and races

- [ ] Replay `finalize-all` with the same key and group → same batch ID, no duplicate items/shares/debts/activities/notifications; same key different group → `409 IDEMPOTENCY_KEY_REUSED` → AC-7
- [ ] Race edit or individual finalize against a batch item: one exact version transition wins, the other records a stable result (`VERSION_CONFLICT`) → AC-7
- [x] Race disband against an active batch → `409 BULK_FINALIZE_IN_PROGRESS`; after completion, disband follows existing obligation rules → AC-7

### AC-8 immutability preserved

- [x] Finalized/voided bills never appear as new batch targets; edit, OCR apply, review, delete, finalize on a finalized bill all still return `409 BILL_IMMUTABLE`; correction requires the void plus replacement flow → AC-8

### AC-10 authorization and privacy

- [x] Every new endpoint rejects expired tokens (401), non Captains (`403 CAPTAIN_REQUIRED`), archived groups and inactive callers (404); ordinary members get no batch navigation fields on group detail → AC-10
- [x] Stored item error codes are only from the bounded set; logs/metrics contain identifiers only (no merchant names, item text, image data, bank data, idempotency keys) → AC-10

## Value sourcing checks

- [x] Vary the group: lock state shown always comes from `groups.bill_submission_locked_at IS NOT NULL` and the stored timestamp, never a client flag → Read group source row
- [ ] Captain navigation: active batch link matches the partial active lookup and latest link matches newest `(created_at, id)`; omitted entirely for a member role token → Read group as Captain row
- [x] Lock time equals PostgreSQL time from the server transaction, not client input → Lock submissions row
- [x] Start bulk counts: build groups where reviewed/unreviewed/excluded counts differ from defaults and confirm capture uses status draft/reviewed plus `reviewed_at IS NOT NULL` at the captured version → Start bulk rows
- [x] Batch detail counts equal terminal item rows on every completed batch (SQL reconcile query) → Batch detail row
- [x] Deleted bill display name returns null rather than stale content → Batch detail optional name row
- [ ] Completion notification goes to the current active Captain after a mid batch Captain transfer, while activities keep the original requester as actor → Batch completion row
- [ ] Create controls enabled/disabled track server lock state across two devices with different cached states → Flutter create controls row

## Acceptance-criteria coverage

AC-1 lock steps · AC-2 gate steps · AC-3 editable steps · AC-4 start steps · AC-5 processing steps · AC-6 detail steps · AC-7 replay/race steps · AC-8 immutability step · AC-9 Flutter section · AC-10 authorization and privacy steps
