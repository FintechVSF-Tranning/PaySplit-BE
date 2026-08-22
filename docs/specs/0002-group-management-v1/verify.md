# Verify: Group management v1 · spec 0002 · updated 2026-08-22 (complete, AC-1 through AC-12)

_Steps derived from spec 0002 acceptance criteria. `/check verify` runs these; `/test` locks the durable ones._

_Last run: `/check verify` on 2026-08-22, verdict PASS. The API was launched against PostgreSQL 18 and driven with three live bearer sessions. Migration 11, the full Go suite, cross-module archived write gates, concurrency tests, schema constraints, and all twelve acceptance criteria passed._

## API / manual

- [x] `POST /api/v1/groups` with `{"name":"  Du lịch Đà Lạt  "}` as an authenticated user → `201`, `group.name` trimmed to `Du lịch Đà Lạt`, `group.currency` is `VND`, `membership.role` is `captain`, `membership.status` is `active` → AC-1
- [x] `POST /api/v1/groups` with `{"name":"   "}` (blank after trim) → `400 VALIDATION_FAILED` → AC-1
- [x] `POST /api/v1/groups` with `{"name":"Test","currency":"USD"}` → `400 VALIDATION_FAILED` (only VND accepted in v1) → AC-1
- [x] `GET /api/v1/groups?limit=1` twice, following `next_cursor` → each page returns exactly `limit` active-membership groups ordered by `(created_at, id)` descending, no duplicates or gaps across pages, `next_cursor` is `null` on the last page → AC-2
- [x] `GET /api/v1/groups/{id}` as an active member → `200` with `group`, `members` (active only), `balances`, and `caller_role` → AC-2
- [x] `GET /api/v1/groups/{id}` for a group the caller does not belong to, and for a random nonexistent UUID → both return the identical `404 GROUP_NOT_FOUND` body, so existence is never leaked → AC-2, invariant 12
- [x] `GET /api/v1/groups?cursor=not-a-real-cursor` → `400 INVALID_CURSOR` → AC-2
- [x] `POST /api/v1/groups/{id}/invites` with no body as any active member → `201` with a default invite when none is available, or `200` with the newest available invite unchanged → AC-3
- [x] Repeat the empty request as a standard member after joining → `200` with the identical `invite.id` and `code`; no extra activity is written → AC-3
- [x] `POST /api/v1/groups/{id}/invites` with `{"regenerate":true,"expires_in_hours":48,"max_uses":10}` → `201` with a new `invite.id`; the prior available invite is now revoked → AC-3
- [x] `POST /api/v1/groups/{id}/invites` with `{"expires_in_hours":0}` and with `{"max_uses":100}` → both `400 VALIDATION_FAILED` (ranges are 1-168 and 1-50) → AC-3
- [x] A standard member supplies any policy field, including `{"regenerate":false}`, `{"regenerate":"false"}`, or `{"max_uses":null}` → `403 CAPTAIN_REQUIRED` before policy validation; a Captain supplies a malformed or null policy value → `400 VALIDATION_FAILED`, and a supplied top-level `null` body also returns `400` while an omitted body remains valid → AC-3
- [x] `GET /api/v1/groups/{id}/invites` as an active member → `200` with available invites only, newest first and with the exact public fields; a nonmember or archived group receives `404 GROUP_NOT_FOUND` → AC-10
- [x] `DELETE /api/v1/groups/{id}/invites/{inviteId}` as the Captain → `204`; repeating the same call → `204` again (idempotent) → AC-3
- [x] `DELETE /api/v1/groups/{id}/invites/{inviteId}` with a random invite id → `404 INVITE_NOT_FOUND` → AC-3
- [x] Inspect `group_activities` after the steps above → one `invite_created` row per created invite and one `invite_revoked` row per revoke, each with `metadata` containing only `invite_id` (and `expires_at`/`max_uses` for `invite_created`) and never the invite `code` → AC-8, invariant 10
- [x] `GET /api/v1/groups/invites/{code}` as an authenticated nonmember with a valid invite → `200` with `preview.group_name`, `preview.active_member_count`, `preview.captain_display_name`, and no way to reach group detail without joining first → AC-4
- [x] `GET /api/v1/groups/invites/{code}` and `POST /api/v1/groups/join` with malformed, unknown, expired, revoked, or exhausted codes → byte-identical `404 INVITE_NOT_FOUND` responses for every unavailable state → AC-4, AC-5, AC-12
- [x] Check the server access log line for the preview request above → the path reads `.../groups/invites/[REDACTED]`, the raw code never appears in any log line → AC-8, invariant 10
- [x] `POST /api/v1/groups/join` with `{"code":"<valid>"}` as a nonmember → `200` with `join.result` `joined`, a real `membership_id`, and one `member_joined` activity written → AC-5
- [x] Repeat the same `POST /api/v1/groups/join` call for an already active member → `200` with `join.result` `already_active`, the same `membership_id`, invite `use_count` unchanged, no new activity → AC-5, invariant 11
- [x] Deactivate that membership directly (simulating a future leave), then redeem the same code again → `200` with `join.result` `reactivated`, the identical `membership_id` as the original join, and one `member_reactivated` activity → AC-5, invariant 8
- [x] An already active member repeats join for a code that still resolves to the group after it becomes unavailable → `200 already_active`, usage and activity unchanged → AC-5
- [x] Drive a group to 50 active members, then attempt one more new join → `409 GROUP_MEMBER_LIMIT_REACHED`, and the invite's `use_count` is not incremented → AC-5
- [x] Fire concurrent redemptions of an invite with `max_uses` one below capacity, and separately at exactly the group's last open slot → exactly one redemption wins each race, no over-admission past either limit → AC-5

## Membership and Captain transfer

- [x] `DELETE /api/v1/groups/{id}/members/{memberId}` as the active Captain targeting their own membership → `409 CAPTAIN_TRANSFER_REQUIRED` → AC-7
- [x] `DELETE /api/v1/groups/{id}/members/{memberId}` as a standard member targeting another member, and as a nonmember targeting any member → both `403 FORBIDDEN` → AC-6
- [x] Give a member an unsettled `debts` row (as debtor, then separately as creditor from a bill they financed), then have them leave → `409 GROUP_MEMBER_HAS_OPEN_DEBTS` with `error.fields.payable_amount`/`receivable_amount` reflecting the real unsettled totals; settling or removing the debt then retrying → `204` → AC-6
- [x] `DELETE /api/v1/groups/{id}/members/{memberId}` for a member with no open debts, as themself → `204`; repeating the same call → `204` again (idempotent, `left_at` unchanged); the caller can no longer read group detail → AC-6
- [x] Active Captain removes a standard member with no open debts → `204`, and a `member_removed` activity is written with the Captain as actor → AC-6, AC-8
- [x] `PUT /api/v1/groups/{id}/members/{memberId}/role` with `{"role":"captain"}` targeting another active member, called by the active Captain → `200` with `previous_captain_member_id`/`current_captain_member_id`; the old Captain's role becomes `member`, the target's becomes `captain`, and there is still exactly one active Captain → AC-7
- [x] `PUT .../role` called by a non-Captain → `403 CAPTAIN_REQUIRED`; targeting a nonexistent or inactive membership → `404 MEMBER_NOT_FOUND`; with a `role` other than `"captain"` → `400 VALIDATION_FAILED` → AC-7
- [x] Fire two concurrent transfer requests for the same group → exactly one succeeds, the other returns `409 CAPTAIN_TRANSFER_CONFLICT`, and the group ends with exactly one active Captain → AC-7
- [x] Inspect `group_activities` after the steps above → one `member_left`/`member_removed` row per exit with `member_id`/`target_member_id` metadata, and one `captain_transferred` row per transfer with `previous_captain_member_id`/`current_captain_member_id` metadata → AC-8
- [x] `GET /api/v1/groups/{id}/activities?limit=1` twice, following `next_cursor` → each page returns exactly `limit` activities ordered by `(created_at, id)` descending, no duplicates or gaps, `next_cursor` is `null` on the last page, and every `activity.metadata` contains exactly the keys the Activity contract table names for its `action_type` → AC-8
- [x] `GET /api/v1/groups/{id}/activities` as a nonmember → `403 FORBIDDEN`; with `?cursor=garbage` as an active member → `400 INVALID_CURSOR` → AC-8

## Governance, archive, and invite security

- [x] `PATCH /api/v1/groups/{id}` as a standard member → `403 CAPTAIN_REQUIRED`; as the Captain with surrounding whitespace and Unicode → `200` with the trimmed name and exactly one `group_renamed` activity carrying exact `old_name` and `new_name` metadata → AC-8, AC-11
- [x] Give the group one reviewed bill and one awaiting debt, then call `DELETE /api/v1/groups/{id}` as Captain → `409 GROUP_HAS_UNSETTLED_OBLIGATIONS` with bill and debt counts both `1`; member and outsider calls return `403 CAPTAIN_REQUIRED` and `404 GROUP_NOT_FOUND` respectively → AC-9
- [x] Change that reviewed bill and awaiting debt to `voided`, retry disband → `204` with no body; the live row is `archived`, every membership is inactive, every available invite is revoked, one archive activity exists, and all bills and debts remain → AC-8, AC-9
- [x] After archive, detail, invite create/list, rename, repeat disband, member mutation, bill/OCR writes, and settlement writes are rejected through their normal not-found or forbidden surfaces; the group is absent from active lists → AC-7, AC-9
- [x] Race member exit against debt creation through the shared group lock → debt commit makes exit observe `409`, with no member leaving around an uncommitted obligation; archived bill creation and OCR completion are rejected → AC-6, AC-9
- [x] Generate invite codes repeatedly and force unique collisions → every code is unpredictable eight-character Base62, rejected random bytes do not bias the generator, collisions use at most five fresh transactions, and the live database rejects a malformed direct insert with `group_invites_code_base62_check` → AC-12
- [x] Drive preview attempts from distinct direct loopback IPs under one bearer account → the first 30 account attempts pass through, later attempts and join return `429 RATE_LIMITED` with the same epoch-window `Retry-After`; middleware tests separately prove independent account and direct-IP buckets and ignored forwarding headers → AC-12
- [x] Inspect server logs after preview, join, create, list, and error flows → paths contain `[REDACTED]`; neither the raw code nor `invite_url` appears in captured logs or activity JSON → AC-8, AC-12

## Commands

- [x] Apply migrations to both development and isolated test databases → version 11 (`000011_group_governance_invites_v1.sql`) applies cleanly; live schema exposes `group_status`, both activity enum values, the Base62 check, and zero invalid stored codes → AC-9, AC-11, AC-12
- [x] `go build ./...` and `go vet ./...` → clean → AC-1 through AC-12
- [x] `TEST_DATABASE_URL=<isolated> go test -count=1 ./...` → every package passes, including PostgreSQL and HTTP integration suites → AC-1 through AC-12

## Acceptance-criteria coverage

- AC-1 (create group: trim, 1-100 code points, VND only, one transaction with initial Captain membership, `201`) — met. Verified live.
- AC-2 (list only active memberships, cursor ordered by `(created_at, id)` desc; detail returns group/members/caller role/balances only to an active member, `404` for missing or nonmember) — met. Verified live.
- AC-3 (every active member creates or reuses the default invite; only Captain controls policy, regeneration, and revocation) — met. Verified live with omitted body, explicit false, null, reuse, configuration, and regeneration coverage.
- AC-4 (valid preview for any authenticated caller; every unusable code state is the same `404`) — met. Verified live for unknown, expired, exhausted, and revoked states with byte-identical responses.
- AC-5 (atomic join/reactivation, active-member idempotency, 50-member and invite-use concurrency limits, unified unavailable errors) — met. Verified live and through concurrent PostgreSQL coverage.
- AC-6 (self leave or Captain removal of a standard member; idempotent; blocked by any unsettled debt as debtor or creditor, with amounts in the error) — met. Verified live against real `debts` rows for both debtor and creditor sides.
- AC-7 (active Captain can never leave or be removed; transfer updates both memberships atomically leaving exactly one active Captain; blocked lock returns a conflict, non-Captain caller is forbidden) — met. The concurrent-transfer race was driven for real: two truly parallel `PUT .../role` requests produced exactly one `200` and one `409 CAPTAIN_TRANSFER_CONFLICT`, ending with exactly one active Captain.
- AC-8 (all group mutations append the exact activity atomically; active timeline access and code redaction) — met. Live logs and activity JSON contained no raw code or invite URL.
- AC-9 (safe soft archive with corrected obligation checks, preserved history, and group, bill, OCR, settlement write gates) — met. Verified live plus cross-module PostgreSQL integration and lock races.
- AC-10 (active member invite list, available rows only, newest first, exact public shape) — met. Verified live and against mixed available, expired, revoked, and exhausted rows.
- AC-11 (Captain-only Unicode rename with exact activity) — met. Verified live for permission, trimming, response, description, and metadata.
- AC-12 (Base62 format and constraint, collision retry, path link, unified errors, redaction, account plus direct-IP limiter) — met. Verified against the live schema, server, logs, and deterministic middleware tests.

## Known gaps

- The invite attempt limiter is intentionally process local for the current one-instance deployment. A shared PostgreSQL or Redis counter is required before adding API replicas.
- Universal Link and App Link association files remain infrastructure and mobile release work outside this backend slice.
