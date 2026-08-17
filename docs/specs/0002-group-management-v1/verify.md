# Verify: Group management v1 · spec 0002 · updated 2026-08-17 (complete, AC-1 through AC-8)

_Steps derived from spec 0002 acceptance criteria. `/check verify` runs these; `/test` locks the durable ones._

_Last run: `/check verify` on 2026-08-17, verdict PASS. Every step below was executed against a live PostgreSQL 18 instance with real HTTP requests; see the `/check verify` report for cited evidence per behavior._

## API / manual

- [x] `POST /api/v1/groups` with `{"name":"  Du lịch Đà Lạt  "}` as an authenticated user → `201`, `group.name` trimmed to `Du lịch Đà Lạt`, `group.currency` is `VND`, `membership.role` is `captain`, `membership.status` is `active` → AC-1
- [x] `POST /api/v1/groups` with `{"name":"   "}` (blank after trim) → `400 VALIDATION_FAILED` → AC-1
- [x] `POST /api/v1/groups` with `{"name":"Test","currency":"USD"}` → `400 VALIDATION_FAILED` (only VND accepted in v1) → AC-1
- [x] `GET /api/v1/groups?limit=1` twice, following `next_cursor` → each page returns exactly `limit` active-membership groups ordered by `(created_at, id)` descending, no duplicates or gaps across pages, `next_cursor` is `null` on the last page → AC-2
- [x] `GET /api/v1/groups/{id}` as an active member → `200` with `group`, `members` (active only), `balances`, and `caller_role` → AC-2
- [x] `GET /api/v1/groups/{id}` for a group the caller does not belong to, and for a random nonexistent UUID → both return the identical `404 GROUP_NOT_FOUND` body, so existence is never leaked → AC-2, invariant 12
- [x] `GET /api/v1/groups?cursor=not-a-real-cursor` → `400 INVALID_CURSOR` → AC-2
- [x] `POST /api/v1/groups/{id}/invites` with `{}` as the group's Captain → `201` with a new `invite` (`max_uses` null, `use_count` 0, `expires_at` 24h out) → AC-3
- [x] `POST /api/v1/groups/{id}/invites` with `{}` again, same Captain → `200` with the identical `invite.id` and `code` (reused, not recreated) → AC-3
- [x] `POST /api/v1/groups/{id}/invites` with `{"regenerate":true,"expires_in_hours":48,"max_uses":10}` → `201` with a new `invite.id`; the prior available invite is now revoked → AC-3
- [x] `POST /api/v1/groups/{id}/invites` with `{"expires_in_hours":0}` and with `{"max_uses":100}` → both `400 VALIDATION_FAILED` (ranges are 1-168 and 1-50) → AC-3
- [x] `POST /api/v1/groups/{id}/invites` as a non-Captain member, and as any caller against a nonexistent group id → both `403 CAPTAIN_REQUIRED` → AC-3
- [x] `DELETE /api/v1/groups/{id}/invites/{inviteId}` as the Captain → `204`; repeating the same call → `204` again (idempotent) → AC-3
- [x] `DELETE /api/v1/groups/{id}/invites/{inviteId}` with a random invite id → `404 INVITE_NOT_FOUND` → AC-3
- [x] Inspect `group_activities` after the steps above → one `invite_created` row per created invite and one `invite_revoked` row per revoke, each with `metadata` containing only `invite_id` (and `expires_at`/`max_uses` for `invite_created`) and never the invite `code` → AC-8, invariant 10
- [x] `GET /api/v1/groups/invites/{code}` as an authenticated nonmember with a valid invite → `200` with `preview.group_name`, `preview.active_member_count`, `preview.captain_display_name`, and no way to reach group detail without joining first → AC-4
- [x] `GET /api/v1/groups/invites/{code}` with an unknown code → `404 INVITE_NOT_FOUND`; with a revoked or expired code → `410 INVITE_UNAVAILABLE` → AC-4
- [x] Check the server access log line for the preview request above → the path reads `.../groups/invites/[REDACTED]`, the raw code never appears in any log line → AC-8, invariant 10
- [x] `POST /api/v1/groups/join` with `{"code":"<valid>"}` as a nonmember → `200` with `join.result` `joined`, a real `membership_id`, and one `member_joined` activity written → AC-5
- [x] Repeat the same `POST /api/v1/groups/join` call for an already active member → `200` with `join.result` `already_active`, the same `membership_id`, invite `use_count` unchanged, no new activity → AC-5, invariant 11
- [x] Deactivate that membership directly (simulating a future leave), then redeem the same code again → `200` with `join.result` `reactivated`, the identical `membership_id` as the original join, and one `member_reactivated` activity → AC-5, invariant 8
- [x] `POST /api/v1/groups/join` with an unknown code → `404 INVITE_NOT_FOUND`; with a revoked or expired code → `410 INVITE_UNAVAILABLE` → AC-5
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

## Commands

- [x] `make migrate-status` → version 2 (`000002_group_management_v1.sql`) applied → AC-1 through AC-8
- [x] `go build ./... && go vet ./...` → clean → AC-1 through AC-8
- [x] `go test ./...` → all existing suites still pass (no group test suite yet; owed to `/test`) → AC-1 through AC-8

## Acceptance-criteria coverage

- AC-1 (create group: trim, 1-100 code points, VND only, one transaction with initial Captain membership, `201`) — met. Verified live.
- AC-2 (list only active memberships, cursor ordered by `(created_at, id)` desc; detail returns group/members/caller role/balances only to an active member, `404` for missing or nonmember) — met. Verified live.
- AC-3 (only the active Captain creates invites; expiry 1-168h default 24, max_uses 1-50 optional; reuse the one available invite; regenerate revokes and replaces it; revocation is idempotent) — met. Verified live.
- AC-4 (any authenticated user with a valid invite previews group name, active member count, and Captain name without membership) — met. Verified live.
- AC-5 (new join or reactivation validates the invite, enforces capacity, increments usage, activates membership, writes one activity atomically; an already active member gets idempotent success; 50-member cap holds under concurrency) — met. The 50-member boundary and the invite-limit race were both driven for real: 10 truly parallel join requests against a `max_uses=1` invite produced exactly one `200` and nine `410`, with `use_count` staying at 1.
- AC-6 (self leave or Captain removal of a standard member; idempotent; blocked by any unsettled debt as debtor or creditor, with amounts in the error) — met. Verified live against real `debts` rows for both debtor and creditor sides.
- AC-7 (active Captain can never leave or be removed; transfer updates both memberships atomically leaving exactly one active Captain; blocked lock returns a conflict, non-Captain caller is forbidden) — met. The concurrent-transfer race was driven for real: two truly parallel `PUT .../role` requests produced exactly one `200` and one `409 CAPTAIN_TRANSFER_CONFLICT`, ending with exactly one active Captain.
- AC-8 (invite, join, exit, and transfer mutations each append one activity in the same transaction; active members can read the timeline with cursor pagination; invite codes never appear in activity metadata or access logs) — met. All eight `action_type` values were exercised and inspected directly against `group_activities`, each with exactly the metadata keys the Activity contract table names.

## Known gaps

- No automated test suite exists yet for the group module (`internal/modules/group/...` has zero `_test.go` files). Every check above was run manually against a live PostgreSQL 18 instance; none of it is locked in as regression coverage. Owed to `/test`.
