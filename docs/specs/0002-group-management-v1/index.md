# 0002. Group management v1

**Date**: 2026-08-17
**Status**: In Progress (reopened 2026-08-21: AC-9 added and an AC-6 drift bug found; see Follow-up items 5 and 6)

## Summary

Group management v1 lets an authenticated user create a VND expense group, view active groups, invite people, join or rejoin, manage membership safely, disband the group, and read the group activity timeline. Captain permissions, short database transactions, and group scoped foreign keys protect access and consistency. A member may leave or be removed, or a Captain may disband the group, only when no unsettled debt (neither `settled` nor `voided`) remains.

## Rationale

Reasoning and options considered: see [rationale.md](rationale.md).

## Requirements

**User stories**:

1. As an authenticated user, I want to create and view expense groups so that I can track shared expenses with my friends.
2. As a Captain, I want to create, reuse, regenerate, and revoke invite codes so that I control access to my group.
3. As an invited authenticated user, I want to preview and join the correct group through an invite code.
4. As a member, I want to leave safely, and as a Captain, I want to remove a member safely, without losing financial history.
5. As a Captain, I want to transfer the Captain role before leaving the group.
6. As an active member, I want to read the group activity timeline so that I can understand important membership changes.

**Acceptance criteria**:

1. **AC-1**: Creating a group applies `strings.TrimSpace`, requires 1 to 100 Unicode code points, accepts only `VND` in v1, creates the group and its initial active Captain membership in one transaction, and returns `201` with the group and membership details.
2. **AC-2**: Listing groups returns only groups where the authenticated user is an active member, ordered by `(created_at, id)` descending with cursor pagination. Group detail returns the group, active members, the caller role, and balances only to an active member.
3. **AC-3**: Only the active Captain may create an invite. Expiry defaults to 24 hours and accepts 1 through 168 hours. `max_uses` is optional and accepts 1 through 50. Without regeneration, the one available invite for the group is returned. Regeneration revokes every available invite and creates one new invite. Revocation is idempotent.
4. **AC-4**: Any authenticated user with a valid unexpired, unrevoked, and unexhausted invite code may preview the group name, active member count, and current Captain display name without already being a group member.
5. **AC-5**: A new join or reactivation validates the invite, enforces group capacity, increments invite usage, activates the standard membership, and writes one activity atomically. An already active member receives idempotent success for any existing code that resolves to that group without availability or capacity checks, usage increment, or activity. A group never exceeds 50 active members, and concurrent redemption never exceeds the invite or group limit.
6. **AC-6**: An active member may leave only by targeting their own membership. An active Captain may remove another standard member. The operation is idempotent and marks the target inactive only when no unsettled `debts` row names the target as debtor or creditor. Otherwise it returns `409 GROUP_MEMBER_HAS_OPEN_DEBTS` with `payable_amount` and `receivable_amount`.
7. **AC-7**: An active Captain of an active (not archived, AC-9) group cannot leave or be removed. Transferring the role to another active member updates both memberships atomically and leaves exactly one active Captain. A transfer that cannot acquire the group lock immediately returns `409 CAPTAIN_TRANSFER_CONFLICT`, while a caller who is not the active Captain returns `403 CAPTAIN_REQUIRED`. An archived group has no active Captain to transfer to or from; leave, removal, and transfer all return `404 GROUP_NOT_FOUND` there, per AC-9.
8. **AC-8**: Group creation, invite creation, invite revocation, member join, member reactivation, member leave, member removal, and Captain transfer append an activity in the same transaction as the mutation. Active members can read activities ordered by `(created_at, id)` descending with cursor pagination. Invite codes never appear in activity metadata, logs, or error messages.
9. **AC-9** (added 2026-08-21, resolves Follow-up item 1): An active Captain can disband a group through `DELETE /api/v1/groups/{id}`. The transaction locks the `groups` row, then rejects with `409 GROUP_HAS_UNSETTLED_OBLIGATIONS` (`draft_or_reviewed_bill_count` and `open_debt_count` in the response) if any `bills` row for the group has `status NOT IN ('finalized', 'voided')`, or any `debts` row for the group has a status other than `settled` **or `voided`**. `bills.status NOT IN ('finalized', 'voided')` also catches a bill still `reviewed` but not finalized, and transitively catches any bill still `draft` while its OCR job runs (a job never changes the bill's own status) and any in-flight payment (`debts_check1` forces a payment-linked debt out of `awaiting`/`voided`, so it cannot be `settled` while a payment is pending). Otherwise it marks every active `group_members` row `inactive` (`left_at = now()`), revokes every available invite, sets `groups.status = 'archived'`, and appends a `group_archived` activity, all in one transaction. Disbanding never deletes the `groups` row or any `bills`/`debts`/`payments` history. A non Captain caller receives `403 CAPTAIN_REQUIRED`; a missing group or non member caller receives `404 GROUP_NOT_FOUND`. Once archived, an archived group is indistinguishable from a group the caller was never a member of: **every** route scoped to it, reads included, returns `404 GROUP_NOT_FOUND` or `403 FORBIDDEN` exactly as it already does for a non member, since disbanding leaves no active membership behind for anyone to read through. There is no archived-only read view in v1.

## Decision

**Chosen option**: Keep group management inside the modular Go application, use PostgreSQL as the consistency boundary, and serialize conflicting group mutations with a short transaction that first locks the `groups` row. Use application authorization for Captain and self service rules, composite foreign keys for group isolation, and explicit activity records for access control changes. (basis: the existing modular architecture, the initial PostgreSQL schema, and PostgreSQL transaction and locking practices)

**Implementation skills**: `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Feature design

### Data model sketch

| Entity | Key fields | Constraints and relationships |
|---|---|---|
| `groups` | UUID v7 `id`, `name`, `currency`, `created_by`, `created_at`, `status` (added 2026-08-21, AC-9) | Primary ID defaults to `uuidv7()`. `created_by` references `users(id)`. Name has 1 to 100 Unicode code points after `strings.TrimSpace`. Currency is exactly `VND` in v1. `status` is `active` or `archived`, default `active`; disbanding a group (AC-9) sets it to `archived` and never deletes the row or any historical `bills`/`debts`/`payments`, matching how `bills.voided`/`debts.voided` already preserve rather than delete. |
| `group_members` | UUID v7 `id`, `group_id`, `user_id`, `role`, `status`, `joined_at`, `left_at` | Unique `(group_id, user_id)` preserves one historical identity. Unique `(id, group_id)` is the composite FK target. A partial unique index permits at most one active Captain. Status and `left_at` must agree. |
| `group_invites` | UUID v7 `id`, `group_id`, `code`, `created_by`, `expires_at`, `max_uses`, `use_count`, `revoked_at`, `created_at` | Code is unique and unguessable. Creator and group reference `group_members(id, group_id)`. Counts are nonnegative and `use_count` cannot exceed `max_uses`. An index supports the active invite lookup by group. |
| `group_activities` | UUID v7 `id`, `group_id`, `actor_member_id`, `action_type`, `description`, `metadata`, `created_at` | Actor and group reference `group_members(id, group_id)`. `activity_type` gains the eight group events required by AC-8. Timeline index is `(group_id, created_at DESC, id DESC)`. |
| `debts` | `group_id`, `debtor_member_id`, `creditor_member_id`, `amount`, `status` | Existing source of truth for removal eligibility. Any status other than `settled` **or `voided`** is an open obligation (corrected 2026-08-21: `debt_status` gained `voided` in spec 0003 after this row was written, see the drift note in `rationale.md`; the live `status <> 'settled'` queries backing AC-6 have not been corrected yet, tracked in Follow-up). |
| `v_member_balances` | `group_id`, `member_id`, `net_balance` | Existing derived display view. It is not sufficient for removal eligibility because equal payable and receivable totals can cancel to zero. |

The schema must add group input checks, the group activity enum values, and the following indexes. PostgreSQL partial uniqueness enforces at most one active Captain. The service transaction enforces that an active group never has zero active Captains.

```sql
CREATE INDEX idx_group_members_user_active
    ON group_members(user_id, group_id)
    WHERE status = 'active';

CREATE INDEX idx_groups_cursor
    ON groups(created_at DESC, id DESC);

CREATE INDEX idx_group_invites_candidate
    ON group_invites(group_id, created_at DESC, id DESC)
    WHERE revoked_at IS NULL;

CREATE INDEX idx_group_activities_timeline
    ON group_activities(group_id, created_at DESC, id DESC);

CREATE INDEX idx_debts_group_debtor_unsettled
    ON debts(group_id, debtor_member_id) INCLUDE (amount)
    WHERE status <> 'settled';

CREATE INDEX idx_debts_group_creditor_unsettled
    ON debts(group_id, creditor_member_id) INCLUDE (amount)
    WHERE status <> 'settled';
```

The migration replaces superseded indexes with these confirmed query indexes instead of retaining redundant forms. List groups first selects the caller active memberships through `idx_group_members_user_active`, joins groups by primary key, then orders and seeks on `(groups.created_at, groups.id)`.

**Follow-up migration required (added 2026-08-21, AC-9 and the drift correction above).** The two indexes above were built before `debt_status` gained `voided` (spec 0003) and now silently mismatch the corrected query filter: a partial index's predicate must imply the query's `WHERE` clause for Postgres to use it, so a query filtering `status NOT IN ('settled', 'voided')` no longer matches an index built `WHERE status <> 'settled'`. A later migration must, in this order: add `groups.status` (`active`/`archived`, default `active`); add `group_archived` to `activity_type`; rebuild both `idx_debts_group_debtor_unsettled` and `idx_debts_group_creditor_unsettled` with `WHERE status NOT IN ('settled', 'voided')`. Rebuild them with `CREATE INDEX CONCURRENTLY` (these tables take live writes; a plain `CREATE INDEX` holds a write lock for the rebuild's duration) and `DROP INDEX CONCURRENTLY` for the old ones, which requires marking this migration file `-- +goose NO TRANSACTION` since `CONCURRENTLY` cannot run inside a transaction block. This spec does not restate the exact SQL here since it is a corrective migration on already shipped objects, not new build content; see Follow-up for the code side of this fix.

### State transitions

```text
group_members: none -> active -> inactive -> active
group_invites: active -> expired | exhausted | revoked
captain transfer: captain(A) + member(B) -> member(A) + captain(B)
```

Inactive membership is historical state, not deletion. Reactivation keeps the original membership ID and always restores role `member`.

### API surface

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| `/api/v1/groups` | POST | `name`: string required, `currency`: string optional | group, caller membership | bearer and live session | `400 VALIDATION_FAILED` |
| `/api/v1/groups` | GET | `cursor`: string optional, `limit`: int optional | groups, `next_cursor` | bearer and live session | `400 INVALID_CURSOR` |
| `/api/v1/groups/{id}` | GET | `id`: UUID path | group, active members, caller role, balances | active member | `404 GROUP_NOT_FOUND` |
| `/api/v1/groups/{id}/invites` | POST | `expires_in_hours`: int optional, `max_uses`: int optional, `regenerate`: bool optional | invite code, invite URL, expiry, usage limits | active Captain | `400 VALIDATION_FAILED`, `403 CAPTAIN_REQUIRED` |
| `/api/v1/groups/{id}/invites/{inviteId}` | DELETE | group and invite UUID paths | no body | active Captain | `403 CAPTAIN_REQUIRED`, `404 INVITE_NOT_FOUND` |
| `/api/v1/groups/invites/{code}` | GET | invite code path | group name, active member count, Captain display name | bearer and live session | `404 INVITE_NOT_FOUND`, `410 INVITE_UNAVAILABLE` |
| `/api/v1/groups/join` | POST | `code`: string required | group ID, membership ID, role, status, result | bearer and live session | `404 INVITE_NOT_FOUND`, `410 INVITE_UNAVAILABLE`, `409 GROUP_MEMBER_LIMIT_REACHED` |
| `/api/v1/groups/{id}/members/{memberId}` | DELETE | group and membership UUID paths | no body | membership owner for self, active Captain for another member | `403 FORBIDDEN`, `409 GROUP_MEMBER_HAS_OPEN_DEBTS`, `409 CAPTAIN_TRANSFER_REQUIRED` |
| `/api/v1/groups/{id}/members/{memberId}/role` | PUT | `role`: literal `captain` | old and new Captain membership IDs | active Captain | `403 CAPTAIN_REQUIRED`, `404 MEMBER_NOT_FOUND`, `409 CAPTAIN_TRANSFER_CONFLICT` |
| `/api/v1/groups/{id}/activities` | GET | `cursor`: string optional, `limit`: int optional | activities, `next_cursor` | active member | `400 INVALID_CURSOR`, `403 FORBIDDEN` |
| `/api/v1/groups/{id}` (added 2026-08-21, AC-9) | DELETE | `id`: UUID path | no body | active Captain | `403 CAPTAIN_REQUIRED`, `404 GROUP_NOT_FOUND`, `409 GROUP_HAS_UNSETTLED_OBLIGATIONS` |

### HTTP contract

All inputs follow the shared JSON error shape from spec 0001. List limits default to 20 and accept 1 through 100. Cursors are opaque encodings of `(created_at, id)`. Timestamps use UTC RFC 3339 strings.

| Action | Success contract |
|---|---|
| Create group | `201` with `group` and `membership` |
| List or read group | `200` with the requested resource and optional `next_cursor` |
| Create a new invite | `201` with `invite` |
| Reuse an existing invite | `200` with the same `invite` |
| Revoke invite | `204` with no body, including a retry after successful revocation |
| Preview invite | `200` with preview fields only |
| Join, reactivate, or open an existing membership | `200` with `result` equal to `joined`, `reactivated`, or `already_active` |
| Leave or remove member | `204` with no body, including an authorized retry after the membership is inactive |
| Transfer Captain | `200` with previous and current Captain membership IDs |
| Read activities | `200` with `activities` and optional `next_cursor` |
| Disband group (added 2026-08-21, AC-9) | `204` with no body |

An unavailable invite uses one public code, `INVITE_UNAVAILABLE`, for expired, revoked, or exhausted state. The balance conflict includes decimal JSON strings for VND integer totals in `error.fields.payable_amount` and `error.fields.receivable_amount`.

### Public response schemas

All money values are base 10 JSON strings in VND. Nullable fields are JSON `null`. Group management never exposes email, phone number, or bank fields.

| Object | Exact public fields |
|---|---|
| `group` | `id`, `name`, `currency`, `created_at` |
| `membership` | `id`, `group_id`, `user_id`, `role`, `status`, `joined_at`, `left_at` |
| `member` | `membership_id`, `user_id`, `display_name`, `avatar_url`, `role`, `joined_at` |
| `balance` | `member_id`, `net_balance` as a decimal string |
| `group_list_item` | `group`, `caller_membership_id`, `caller_role`, `active_member_count` |
| `invite` | `id`, `code`, `invite_url`, `expires_at`, `max_uses`, `use_count` |
| `invite_preview` | `group_name`, `active_member_count`, `captain_display_name` |
| `join_result` | `group_id`, `membership_id`, `role`, `status`, `result` |
| `captain_transfer` | `previous_captain_member_id`, `current_captain_member_id` |
| `activity_actor` | `member_id`, `user_id`, `display_name`, `avatar_url` |
| `activity` | `id`, `action_type`, persisted `description`, `actor`, public `metadata`, `created_at` |

| Endpoint result | Exact response envelope |
|---|---|
| Create group | `{ "group": group, "membership": membership }` |
| List groups | `{ "groups": [group_list_item], "next_cursor": string or null }` |
| Group detail | `{ "group": group, "members": [member], "balances": [balance], "caller_role": string }` |
| Create or reuse invite | `{ "invite": invite }` |
| Invite preview | `{ "preview": invite_preview }` |
| Join group | `{ "join": join_result }` |
| Transfer Captain | `{ "transfer": captain_transfer }` |
| Activity timeline | `{ "activities": [activity], "next_cursor": string or null }` |

### Value sourcing

| Action | Value produced or displayed | Source |
|---|---|---|
| Create group | Group ID, membership ID, timestamps | PostgreSQL 18 defaults and `RETURNING` inside one transaction |
| Create group | Name and currency | Trimmed request name and fixed v1 currency `VND` |
| List groups | Active groups and caller role | Authenticated user ID joined through active `group_members` rows |
| List groups and invite preview | Active member count | Count of `group_members` rows with `status='active'` for the group |
| Group detail | Members and balances | Active `group_members`, group public profile fields from `users`, and `v_member_balances` scoped by group |
| Create invite | Code | 32 cryptographically random bytes encoded as base64url without padding |
| Create invite | Expiry and limit | Transaction time plus validated request duration, and optional validated `max_uses` |
| Create invite | Invite URL | `APP_INVITE_BASE_URL` plus the encoded invite code |
| Preview invite | Group name, member count, Captain name | Invite group, count of active memberships, and active Captain joined to the group public profile fields |
| Join group | Membership result | Existing membership status under the group lock, or inserted membership row |
| Join group | Invite and group capacity | For new join or reactivation, locked invite `use_count` and `max_uses`, plus count of active memberships capped at 50 |
| Leave or remove member | Open payable and receivable totals | Separate sums of unsettled `debts.amount` (`status NOT IN ('settled', 'voided')`, corrected 2026-08-21) for target debtor and target creditor within the group |
| Transfer Captain | Previous and current Captain | Locked active membership rows for the authenticated Captain and target member |
| Disband group (added 2026-08-21, AC-9) | Whether the group can be disbanded | Count of `bills` with `status NOT IN ('finalized', 'voided')` for the group, and count of `debts` with `status NOT IN ('settled', 'voided')` for the group; both must be zero |
| Activity timeline | Actor and event details | Persisted `group_activities`, current group public actor profile, and public metadata that excludes invite codes |
| Paginated list | `next_cursor` | Opaque encoding of the last returned row `(created_at, id)`, omitted when no later row exists |
| Activity timeline | Description | Persisted at mutation time from a stable server template and the actor and target display names loaded in that transaction |

### Key invariants

1. Every group mutation first locks its `groups` row and completes in a short database transaction with no external call.
2. Group creation commits the group, its initial Captain membership, and `group_created` activity together.
3. An active group has at most one active Captain by partial unique index and at least one active Captain by the create, transfer, and exit transactions. An archived group (AC-9) has zero active memberships of any role, by design; this invariant only holds while `groups.status = 'active'`.
4. Captain transfer locks the group first, then both memberships in ascending membership ID order. It demotes the old Captain before promoting the target inside one transaction, which satisfies the immediate partial unique index and exposes only the final committed state.
5. Membership exit is allowed only when separate payable and receivable queries both return zero for unsettled debts (a debt is unsettled when its status is neither `settled` nor `voided`, corrected 2026-08-21, see Follow-up for the matching code fix). A zero value from `v_member_balances` alone never grants exit.
6. Any future operation that creates or changes unsettled debt must acquire the same group row lock before writing, so debt creation cannot race with member exit.
7. Invite creation, regeneration, revocation, and redemption recheck state after acquiring the group row lock. The service creates at most one available invite per group. An available invite is unexpired, unrevoked, and unexhausted.
8. Rejoining uses an atomic upsert on `(group_id, user_id)`, preserves the original membership ID, sets role to `member`, and clears `left_at`.
9. Group capacity is 50 active members. There is no per user active group quota in v1 because the PRD does not define a value.
10. Invite codes are treated as sensitive values. They are returned only by invite creation or reuse responses, accepted only by the preview path and join input, and redacted everywhere else, including HTTP access logs.
11. For an existing invite code whose group already has the caller as an active member, join returns `already_active` before invite availability or group capacity checks. It does not increment usage or append activity.
12. Group detail returns `404 GROUP_NOT_FOUND` when the group is missing or the caller is not an active member, so the endpoint does not reveal group existence.
13. (Added 2026-08-21, AC-9) Disbanding is all or nothing: the `groups` row lock, the `bills`/`debts` precondition check, marking every membership inactive, revoking invites, and the `group_archived` activity all happen in one transaction. An archived group is never deleted and never re-derives `active` status; there is no un-disband path in v1.

### Activity contract

| Action type | Actor | Required metadata |
|---|---|---|
| `group_created` | Initial Captain | `group_id` |
| `invite_created` | Captain | `invite_id`, `expires_at`, and nullable `max_uses`, never code |
| `invite_revoked` | Captain | `invite_id` |
| `member_joined` | Joining member | `member_id` |
| `member_reactivated` | Rejoining member | `member_id` |
| `member_left` | Leaving member | `member_id` |
| `member_removed` | Captain | `target_member_id` |
| `captain_transferred` | Previous Captain | `previous_captain_member_id` and `current_captain_member_id` |
| `group_archived` | Captain | `member_count_deactivated` (added 2026-08-21, AC-9) |

Every activity description is a snapshot written in the mutation transaction. Timeline reads never recompute it from a later display name. Activity metadata returned by the API uses exactly the keys in this table.

### Security model

1. Every endpoint requires a valid bearer token and live active session.
2. Invite preview requires authentication but does not require existing membership.
3. Group detail and activity reads require an active membership in the target group. Group detail maps both a missing group and a nonmember caller to `404 GROUP_NOT_FOUND`.
4. Invite mutation and Captain transfer require the caller to be the current active Captain.
4a. (Added 2026-08-21, AC-9) Disbanding requires the caller to be the current active Captain. Once `groups.status = 'archived'`, every route scoped to that group, read or write, responds `404 GROUP_NOT_FOUND` or `403 FORBIDDEN` exactly as it already does for a non member, because disbanding leaves no active membership behind. **Enforcement mechanism** (there is no shared group-scoped middleware today; each module resolves membership itself): add one lightweight `chi` middleware mounted on every `/api/v1/groups/{id}/...` route and every group scoped `/api/v1/bills/...` route (wherever a `group_id` is resolvable from the path or body before the handler runs). It loads `groups.status` only, and short circuits with `404 GROUP_NOT_FOUND` when it is `archived`; it does not touch, replace, or duplicate any existing Captain or active membership check, those stay exactly as built. Belt and suspenders in front of the fact that no membership survives disband anyway, catching any route that does not itself check active membership.
5. A standard member may target only their own membership for exit. After exit, the membership owner may repeat the same request and receive `204` without regaining group access. A Captain may target another standard member for removal.
6. Group scoped reads and writes include `group_id`. Composite foreign keys prevent a child row from referencing a membership in another group.
7. API responses expose only the group public profile fields defined above. Invite codes and full internal activity metadata are never logged.

### Transaction and concurrency contract

| Flow | Lock and atomic work |
|---|---|
| Create group | Insert group, Captain membership, and activity in one transaction |
| Create or regenerate invite | Lock group, verify Captain, select the available invite by `(created_at, id) DESC`, then reuse it or revoke every available invite and append one revocation activity per invite before inserting the new invite and creation activity |
| Revoke invite | Lock group, verify Captain, find the invite in that group, return `204` for any current state, and update plus append one activity only when `revoked_at IS NULL` |
| Redeem invite | Resolve group from an existing code and lock group. Return `already_active` immediately for an active membership. Otherwise lock and revalidate the invite, enforce capacity, increment use count, upsert membership, and append one join or reactivation activity |
| Leave or remove | Lock group then target membership, verify permission and role, sum open debts, mark inactive, append activity |
| Transfer Captain | Acquire the group row with `FOR UPDATE NOWAIT`, map lock contention to `409 CAPTAIN_TRANSFER_CONFLICT`, lock both memberships by ascending ID, verify roles, demote the old Captain, promote the target, append activity, then commit |
| Disband group (added 2026-08-21, AC-9) | Lock group, verify Captain, check zero bills outside `finalized`/`voided` and zero debts outside `settled`/`voided`, mark every active membership inactive, revoke every available invite, set `groups.status = 'archived'`, append `group_archived` activity, then commit |

### Configuration required

| Variable | Purpose |
|---|---|
| `APP_INVITE_BASE_URL` | Validated HTTPS universal link or Flutter deep link base used to build shareable invite URLs |

### Critical test scenarios

1. Create, list, and read a group through PostgreSQL and HTTP, verifies **AC-1** and **AC-2**.
2. A standard member cannot create or revoke an invite, while the Captain can reuse and regenerate one, verifies **AC-3**.
3. An authenticated nonmember previews a valid invite but cannot read group detail, verifies **AC-4**.
4. Concurrent redemption of the final invite use or the final group slot permits one winner without exceeding either limit, verifies **AC-5**.
5. Joining twice returns `already_active` without consuming another use or writing another activity, including after that invite expires or is revoked. Leaving then rejoining preserves the membership ID, verifies **AC-5**.
6. A member with equal payable and receivable totals cannot leave until every related debt is settled, verifies **AC-6**.
7. Concurrent Captain transfers produce one Captain and one `409 CAPTAIN_TRANSFER_CONFLICT`, while an ordinary non Captain attempt returns `403 CAPTAIN_REQUIRED`, verifies **AC-7**.
8. Every confirmed group mutation writes exactly one activity in the same transaction and paginates without duplicates, verifies **AC-8**.
9. (Added 2026-08-21) A Captain disbands a group with a `voided` (not settled) debt on it and it succeeds; the same group with an `awaiting` debt, or a `reviewed` but not yet finalized bill, is refused with `409 GROUP_HAS_UNSETTLED_OBLIGATIONS` naming the correct counts. After disbanding, every active membership reads `inactive`, `groups.status` reads `archived`, a subsequent bill creation attempt on that group is refused as if the group does not exist, and a group detail read by a former active member also now returns `404 GROUP_NOT_FOUND`, verifies **AC-9**.

## Build plan

The project uses Tracer Bullet, so each slice crosses schema, SQLC, repository, usecase, HTTP, integration tests, and API documentation before the next slice begins.

1. [x] Build the create, list, and detail slice. Add the exact group validation and query indexes, public response DTOs, group and Captain creation transaction, privacy preserving detail authorization, cursor reads, routes, and real PostgreSQL coverage, satisfies **AC-1** and **AC-2**.
2. [x] Build the Captain invite slice. Add invite SQLC queries, cryptographic code generation, configured invite URL construction, single available invite behavior, regeneration, idempotent revocation, authorization, redacted access logs, and activity writes, satisfies **AC-3** and **AC-8**.
3. [x] Build the preview and redemption slice. Add authenticated preview, locked invite redemption, group capacity enforcement, atomic membership upsert, usage increment, and concurrent integration coverage, satisfies **AC-4**, **AC-5**, and **AC-8**.
4. [x] Build the membership exit and Captain transfer slice. Add separate open debt totals, self and Captain permission checks, idempotent inactivation, ordered locking, nonblocking transfer lock, atomic role transfer, and stable conflict mapping, satisfies **AC-6**, **AC-7**, and **AC-8**.
5. [x] Complete the activity timeline and delivery contract. Extend `activity_type`, add the stable timeline index and cursor query, finish shared error mapping, OpenAPI, module documentation, and end to end verification of all eight criteria, satisfies **AC-1** through **AC-8**.
6. [x] (Added 2026-08-21, fixed 2026-08-21 via `/debug`) Fix the `voided`-exclusion drift: corrected `SumOpenDebtorTotal`/`SumOpenCreditorTotal` (`internal/modules/group/repository/postgres/queries/groups.sql`) to `status NOT IN ('settled', 'voided')`, migrated `idx_debts_group_debtor_unsettled`/`idx_debts_group_creditor_unsettled` to match (`db/migrations/000008_group_exit_voided_debt_fix.sql`), and added `TestLeaveOrRemoveMember_VoidedDebtDoesNotBlockExit` (fails without the fix, passes with it), satisfies the corrected **AC-6**. The same bug was also found and fixed in the admin module's `GetOutstandingDebtsByUserID`/`GetOutstandingCreditsByUserID` (`internal/modules/admin/repository/postgres/queries/admin.sql`), not this spec's surface but the same root cause.
7. [ ] (Added 2026-08-21) Build the disband slice. Add the `groups.status` column and `group_archived` activity type via a `NO TRANSACTION` migration that also rebuilds the two debt indexes with `CREATE INDEX CONCURRENTLY` (see the Data model sketch note), the disband transaction (lock, corrected `bills`/`debts` precondition check, mass inactivation, invite revocation, status update, activity), the shared archived-group `chi` middleware across group and bill routes (Security model 4a), `DELETE /api/v1/groups/{id}`, and integration coverage, satisfies **AC-9**.

## Consequences

**Positive**:

1. Membership history and foreign key continuity survive leave and rejoin.
2. Separate debt checks prevent a net zero member from leaving with unresolved obligations.
3. One group coordination lock makes concurrent membership, invite, and future debt writes predictable.
4. Access control changes have a durable activity trail.

**Negative and tradeoffs**:

1. Future bill and settlement modules must honor the same group row lock before creating or changing unsettled debt.
2. Captain transfer and invite redemption require hand written transaction methods around generated SQLC queries.
3. Plain invite codes remain stored in the existing schema so an active invite can be returned again. Database access and log redaction therefore remain important.
4. (Resolved 2026-08-21, AC-9) A sole Captain could not leave in v1 because group archive or deletion behavior was not yet defined; disbanding now gives the sole Captain a defined exit once all obligations are clear, though a sole Captain still cannot merely "leave" and hand the group to nobody.

**Neutral**:

1. `v_member_balances` remains useful for display but is not an authorization source for member exit.

## Follow-up

1. [x] Define group archive or deletion behavior before allowing the sole Captain to leave. Resolved 2026-08-21: **AC-9**, soft archive (`groups.status = 'archived'`), never delete history. This was prompted by `docs/change-req/api-change-request-01.md`'s Disband Group proposal, which had the same `debts.status <> 'settled'` bug as item below and left the delete-vs-archive choice unresolved; both are settled here.
2. [ ] Define a per user active group quota only when product supplies a concrete limit. V1 has no such quota.
3. [x] Reconcile the Group management row in `docs/scope/scope.md` with this updated acceptance contract before implementation tracking advances.
4. [ ] Record the installed PostgreSQL conventions in a root or database area `AGENTS.md` before implementation begins.
5. [x] **Live bug, fixed 2026-08-21 via `/debug`**: `internal/modules/group/repository/postgres/queries/groups.sql`'s `SumOpenDebtorTotal`/`SumOpenCreditorTotal` filtered `status <> 'settled'`, so a member whose only debt was `voided` (their bill got cancelled, spec 0003) was wrongly blocked from leaving the group with `409 GROUP_MEMBER_HAS_OPEN_DEBTS`. Fixed both queries to `status NOT IN ('settled', 'voided')` and the two backing partial indexes via `db/migrations/000008_group_exit_voided_debt_fix.sql` (`CREATE INDEX CONCURRENTLY` in a `NO TRANSACTION` migration, as this spec required above). Verified live: `TestLeaveOrRemoveMember_VoidedDebtDoesNotBlockExit` failed before the fix, passes after; the full suite is green.
6. [ ] `docs/change-req/api-change-request-01.md`'s other proposed group changes (Base62 invite codes, opening invite creation to any active member, the new `GET /{id}/invites` list endpoint, group rename, and per account rate limiting) directly conflict with this spec's **AC-3**, Security model #4, and the invite code Value sourcing row, and were deliberately left out of this update (2026-08-21) since none of them touch `debts`/`0004-split-settlement-v1` and each is its own load bearing decision (especially reversing Captain-only invite creation, an existing Key invariant #7 assumption). Run a separate `/architect` pass scoped to those before building them.
