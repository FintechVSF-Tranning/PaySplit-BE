# 0002. Group management v1

**Date**: 2026-08-17
**Status**: In Progress (expanded 2026-08-22 for the approved member invite and group governance change request)

> **V1 group bill close amendment**: [spec 0008](../0008-group-bill-close-v1/index.md) adds a one way bill submission lock. Group disband also returns `409 BULK_FINALIZE_IN_PROGRESS` while a bulk finalize batch is queued or processing, so an archived group never strands active batch work.

## Summary

Group management v1 lets active members share one short invite while the Captain keeps control of invite policy, group naming, and disbandment. Eight character Base62 codes work as both manual codes and the final path segment of the app link. Short PostgreSQL transactions, active membership checks, and group scoped foreign keys protect access and financial history.

## Rationale

Reasoning and options considered: see [rationale.md](rationale.md).

## Requirements

**User stories**:

1. As an authenticated user, I want to create and view expense groups so that I can track shared expenses with my friends.
2. As an active member, I want to reuse or create the current invite so that I can share the group without waiting for the Captain.
3. As an invited authenticated user, I want to preview and join the correct group through an invite code.
4. As a member, I want to leave safely, and as a Captain, I want to remove a member safely, without losing financial history.
5. As a Captain, I want to transfer the Captain role before leaving the group.
6. As an active member, I want to read the group activity timeline so that I can understand important membership changes.
7. As a Captain, I want to rename or disband the group while preserving its audit and financial history.

**Acceptance criteria**:

1. **AC-1**: Creating a group applies `strings.TrimSpace`, requires 1 to 100 Unicode code points, accepts only `VND` in v1, creates the group and its initial active Captain membership in one transaction, and returns `201` with the group and membership details.
2. **AC-2**: Listing groups returns only groups where the authenticated user is an active member, ordered by `(created_at, id)` descending with cursor pagination. Group detail returns the group, active members, the caller role, and balances only to an active member.
3. **AC-3**: Every active member may call `POST /api/v1/groups/{id}/invites` with no body or `{}`. The call reuses the newest available invite unchanged, or creates one with the 24 hour default and no use limit. Only the active Captain may include `expires_in_hours`, `max_uses`, or `regenerate`, including an explicit false value. A standard member who includes any policy field receives `403 CAPTAIN_REQUIRED`. Expiry accepts 1 through 168 hours and `max_uses` accepts 1 through 50. When an available invite exists and `regenerate` is absent or false, the API reuses it unchanged; supplied expiry or limit values are validated but apply only if no available invite exists. `regenerate: true` revokes every available invite and creates a replacement, using the supplied values and defaulting omitted expiry to 24 hours and omitted `max_uses` to unlimited. Explicit revocation remains Captain only and idempotent.
4. **AC-4**: Any authenticated user with a valid unexpired, unrevoked, and unexhausted invite code may preview the group name, active member count, and current Captain display name without already being a group member. Unknown, malformed, expired, revoked, and exhausted codes all return the identical `404 INVITE_NOT_FOUND` response.
5. **AC-5**: A new join or reactivation validates the invite, enforces group capacity, increments invite usage, activates the standard membership, and writes one activity atomically. An already active member receives idempotent success for any existing code that resolves to that group without availability or capacity checks, usage increment, or activity. A group never exceeds 50 active members, and concurrent redemption never exceeds the invite or group limit.
6. **AC-6**: An active member may leave only by targeting their own membership. An active Captain may remove another standard member. The operation is idempotent and marks the target inactive only when no unsettled `debts` row names the target as debtor or creditor. Otherwise it returns `409 GROUP_MEMBER_HAS_OPEN_DEBTS` with `payable_amount` and `receivable_amount`.
7. **AC-7**: An active Captain of an active (not archived, AC-9) group cannot leave or be removed. Transferring the role to another active member updates both memberships atomically and leaves exactly one active Captain. A transfer that cannot acquire the group lock immediately returns `409 CAPTAIN_TRANSFER_CONFLICT`, while a caller who is not the active Captain returns `403 CAPTAIN_REQUIRED`. An archived group has no active Captain to transfer to or from; leave, removal, and transfer all return `404 GROUP_NOT_FOUND` there, per AC-9.
8. **AC-8**: Group creation, invite creation, invite revocation, member join, member reactivation, member leave, member removal, Captain transfer, group rename, and group archive append an activity in the same transaction as the mutation. Active members can read activities ordered by `(created_at, id)` descending with cursor pagination. Invite codes never appear in activity metadata, logs, or error messages.
9. **AC-9** (added 2026-08-21, resolves Follow-up item 1): An active Captain can disband a group through `DELETE /api/v1/groups/{id}`. The transaction locks the `groups` row, then rejects with `409 GROUP_HAS_UNSETTLED_OBLIGATIONS` (`draft_or_reviewed_bill_count` and `open_debt_count` in the response) if any `bills` row for the group has `status NOT IN ('finalized', 'voided')`, or any `debts` row for the group has a status other than `settled` **or `voided`**. `bills.status NOT IN ('finalized', 'voided')` also catches a bill still `reviewed` but not finalized, and transitively catches any bill still `draft` while its OCR job runs (a job never changes the bill's own status) and any in-flight payment (`debts_check1` forces a payment-linked debt out of `awaiting`/`voided`, so it cannot be `settled` while a payment is pending). Otherwise it marks every active `group_members` row `inactive` (`left_at = now()`), revokes every available invite, sets `groups.status = 'archived'`, and appends a `group_archived` activity, all in one transaction. Disbanding never deletes the `groups` row or any `bills`/`debts`/`payments` history. A non Captain caller receives `403 CAPTAIN_REQUIRED`; a missing group or non member caller receives `404 GROUP_NOT_FOUND`. Once archived, an archived group is indistinguishable from a group the caller was never a member of: **every** route scoped to it, reads included, returns `404 GROUP_NOT_FOUND` or `403 FORBIDDEN` exactly as it already does for a non member, since disbanding leaves no active membership behind for anyone to read through. There is no archived-only read view in v1.
10. **AC-10**: `GET /api/v1/groups/{id}/invites` returns only unrevoked, unexpired, and unexhausted invites to an active member, ordered by `(created_at, id)` descending. Every item contains `id`, the raw eight character code, `invite_url`, `expires_at`, nullable `max_uses`, and `use_count`. A missing group or inactive caller receives `404 GROUP_NOT_FOUND`, and inactive or historical invites are never exposed.
11. **AC-11**: The active Captain may rename a group through `PATCH /api/v1/groups/{id}`. The service applies `strings.TrimSpace`, accepts 1 through 100 Unicode code points, locks the group, updates the name, and writes one `group_renamed` activity with the exact old and new names in metadata. A standard member receives `403 CAPTAIN_REQUIRED`; a missing group or non member receives `404 GROUP_NOT_FOUND`.
12. **AC-12**: Every newly created invite uses exactly eight case sensitive Base62 characters matching `^[A-Za-z0-9]{8}$`. The database enforces the same format and uniqueness. A unique code collision retries with a fresh code at a new transaction boundary. `invite_url` is normalized `APP_INVITE_BASE_URL` plus one path segment containing that same raw code. Preview and join share a UTC epoch aligned one minute fixed window limiter keyed independently by authenticated account and the direct TCP peer IP, using `HTTP_RATE_LIMIT_REQUESTS_PER_MINUTE`. Forwarding headers are never trusted in v1. Codes and invite URLs appear only in active member list and create responses, never in logs, activity metadata, analytics, or error details.

## Decision

**Chosen option**: Improve the existing group module in place. Keep PostgreSQL as the consistency boundary, let every active member share the current invite, and reserve policy changes, rename, revocation, and disbandment for the Captain. Use short group row transactions, repository level active group checks, and a route specific account plus IP limiter for invite attempts. (basis: the existing modular architecture, `docs/change-req/api-change-request-01.md`, and PostgreSQL transaction and locking practices)

**Implementation skills**: `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Feature design

### Data model sketch

| Entity | Key fields | Constraints and relationships |
|---|---|---|
| `groups` | UUID v7 `id`, `name`, `currency`, `created_by`, `created_at`, `status` | Primary ID defaults to `uuidv7()`. `created_by` references `users(id)`. Name has 1 to 100 Unicode code points after `strings.TrimSpace`. Currency is exactly `VND` in v1. `status` is `active` or `archived`, default `active`. Disbanding sets it to `archived` and never deletes financial history. |
| `group_members` | UUID v7 `id`, `group_id`, `user_id`, `role`, `status`, `joined_at`, `left_at` | Unique `(group_id, user_id)` preserves one historical identity. Unique `(id, group_id)` is the composite FK target. A partial unique index permits at most one active Captain. Status and `left_at` must agree. |
| `group_invites` | UUID v7 `id`, `group_id`, `code`, `created_by`, `expires_at`, `max_uses`, `use_count`, `revoked_at`, `created_at` | Code is unique and matches `^[A-Za-z0-9]{8}$`. Creator is the active member who created it and references `group_members(id, group_id)`. Counts are nonnegative and `use_count` cannot exceed `max_uses`. The candidate index supports newest available lookup and active invite listing. |
| `group_activities` | UUID v7 `id`, `group_id`, `actor_member_id`, `action_type`, `description`, `metadata`, `created_at` | Actor and group reference `group_members(id, group_id)`. `activity_type` includes `group_renamed` and `group_archived` in addition to the existing group events. Timeline index is `(group_id, created_at DESC, id DESC)`. |
| `debts` | `group_id`, `debtor_member_id`, `creditor_member_id`, `amount`, `status` | Existing source of truth for removal eligibility. Any status other than `settled` or `voided` is an open obligation. Migration 000008 and the live queries already use that predicate. |
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
    WHERE status NOT IN ('settled', 'voided');

CREATE INDEX idx_debts_group_creditor_unsettled
    ON debts(group_id, creditor_member_id) INCLUDE (amount)
    WHERE status NOT IN ('settled', 'voided');
```

The migration replaces superseded indexes with these confirmed query indexes instead of retaining redundant forms. List groups first selects the caller active memberships through `idx_group_members_user_active`, joins active groups by primary key, then orders and seeks on `(groups.created_at, groups.id)`.

Migration `000008_group_exit_voided_debt_fix.sql` already rebuilt both open debt indexes with the corrected predicate. The governance migration first aborts with the offending group IDs when any active group has anything other than exactly one active Captain; it never guesses a repair for malformed governance data. It then adds `groups.status`, adds the two activity values, and revokes all legacy invites. It temporarily moves every old code out of the Base62 namespace, then assigns rows ordered by invite ID the left padded eight character Base62 encoding of their zero based row number. The migration aborts if the row count reaches `62^8`, then validates the code check. These predictable values are safe because every migrated row is revoked and never returned. Revoking legacy invites is intentional because changing their raw code already invalidates every previously shared link.

### State transitions

```text
group_members: none -> active -> inactive -> active
group_invites: active -> expired | exhausted | revoked
captain transfer: captain(A) + member(B) -> member(A) + captain(B)
groups: active -> archived
```

Inactive membership is historical state, not deletion. Reactivation keeps the original membership ID and always restores role `member`.

### API surface

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| `/api/v1/groups` | POST | `name`: string required, `currency`: string optional | group, caller membership | bearer and live session | `400 VALIDATION_FAILED` |
| `/api/v1/groups` | GET | `cursor`: string optional, `limit`: int optional | groups, `next_cursor` | bearer and live session | `400 INVALID_CURSOR` |
| `/api/v1/groups/{id}` | GET | `id`: UUID path | group, active members, caller role, balances | active member | `404 GROUP_NOT_FOUND` |
| `/api/v1/groups/{id}` | PATCH | `name`: string required | updated group | active Captain | `400 VALIDATION_FAILED`, `403 CAPTAIN_REQUIRED`, `404 GROUP_NOT_FOUND` |
| `/api/v1/groups/{id}/invites` | GET | group UUID path | active invites newest first | active member | `404 GROUP_NOT_FOUND` |
| `/api/v1/groups/{id}/invites` | POST | optional empty body, or Captain policy fields | invite code, invite URL, expiry, usage limits | active member, Captain for policy fields | `400 VALIDATION_FAILED`, `403 CAPTAIN_REQUIRED`, `404 GROUP_NOT_FOUND` |
| `/api/v1/groups/{id}/invites/{inviteId}` | DELETE | group and invite UUID paths | no body | active Captain | `403 CAPTAIN_REQUIRED`, `404 INVITE_NOT_FOUND` |
| `/api/v1/groups/invites/{code}` | GET | eight character Base62 code path | group name, active member count, Captain display name | bearer, live session, account plus IP rate limit | `404 INVITE_NOT_FOUND`, `429 RATE_LIMITED` |
| `/api/v1/groups/join` | POST | `code`: string required | group ID, membership ID, role, status, result | bearer, live session, account plus IP rate limit | `404 INVITE_NOT_FOUND`, `409 GROUP_MEMBER_LIMIT_REACHED`, `429 RATE_LIMITED` |
| `/api/v1/groups/{id}/members/{memberId}` | DELETE | group and membership UUID paths | no body | membership owner for self, active Captain for another member | `403 FORBIDDEN`, `409 GROUP_MEMBER_HAS_OPEN_DEBTS`, `409 CAPTAIN_TRANSFER_REQUIRED` |
| `/api/v1/groups/{id}/members/{memberId}/role` | PUT | `role`: literal `captain` | old and new Captain membership IDs | active Captain | `403 CAPTAIN_REQUIRED`, `404 MEMBER_NOT_FOUND`, `409 CAPTAIN_TRANSFER_CONFLICT` |
| `/api/v1/groups/{id}/activities` | GET | `cursor`: string optional, `limit`: int optional | activities, `next_cursor` | active member | `400 INVALID_CURSOR`, `403 FORBIDDEN` |
| `/api/v1/groups/{id}` | DELETE | `id`: UUID path | no body | active Captain | `403 CAPTAIN_REQUIRED`, `404 GROUP_NOT_FOUND`, `409 GROUP_HAS_UNSETTLED_OBLIGATIONS` |

### HTTP contract

All inputs follow the shared JSON error shape from spec 0001. List limits default to 20 and accept 1 through 100. Cursors are opaque encodings of `(created_at, id)`. Timestamps use UTC RFC 3339 strings. A create invite request accepts no body or one JSON object. A present policy field counts as configuration even when it is `false` or `null`. For an active standard member that presence returns `403 CAPTAIN_REQUIRED` before value validation. For a Captain, `null` policy values return `400 VALIDATION_FAILED`. A malformed preview path code and a malformed JSON `code` value for join both return the same `404 INVITE_NOT_FOUND` as every other unusable invite state.

| Action | Success contract |
|---|---|
| Create group | `201` with `group` and `membership` |
| List or read group | `200` with the requested resource and optional `next_cursor` |
| Rename group | `200` with `group` |
| List active invites | `200` with `invites`, using an empty JSON array when none are available |
| Create a new invite | `201` with `invite` |
| Reuse an existing invite | `200` with the same `invite` |
| Revoke invite | `204` with no body, including a retry after successful revocation |
| Preview invite | `200` with preview fields only |
| Join, reactivate, or open an existing membership | `200` with `result` equal to `joined`, `reactivated`, or `already_active` |
| Leave or remove member | `204` with no body, including an authorized retry after the membership is inactive |
| Transfer Captain | `200` with previous and current Captain membership IDs |
| Read activities | `200` with `activities` and optional `next_cursor` |
| Disband group (added 2026-08-21, AC-9) | `204` with no body |

Every unusable invite state uses the same `404 INVITE_NOT_FOUND` response. The balance conflict includes decimal JSON strings for VND integer totals in `error.fields.payable_amount` and `error.fields.receivable_amount`. The disband conflict includes decimal integer strings in `error.fields.draft_or_reviewed_bill_count` and `error.fields.open_debt_count`.

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
| List groups | `{ "groups": [group_list_item], "next_cursor": string or null }`; `next_cursor` is always present and is `null` when exhausted |
| Group detail | `{ "group": group, "members": [member], "balances": [balance], "caller_role": string }` |
| Rename group | `{ "group": group }` |
| List active invites | `{ "invites": [invite] }` |
| Create or reuse invite | `{ "invite": invite }` |
| Invite preview | `{ "preview": invite_preview }` |
| Join group | `{ "join": join_result }` |
| Transfer Captain | `{ "transfer": captain_transfer }` |
| Activity timeline | `{ "activities": [activity], "next_cursor": string or null }`; `next_cursor` is always present and is `null` when exhausted |

### Value sourcing

| Action | Value produced or displayed | Source |
|---|---|---|
| Create group | Group ID, membership ID, timestamps | PostgreSQL 18 defaults and `RETURNING` inside one transaction |
| Create group | Name and currency | Trimmed request name and fixed v1 currency `VND` |
| List groups | Active groups and caller role | Authenticated user ID joined through active `group_members` rows |
| List groups and invite preview | Active member count | Count of `group_members` rows with `status='active'` for the group |
| Group detail | Members and balances | Active `group_members` and their public profile fields ordered by `(joined_at, membership_id)` ascending; balances are restricted to those same active memberships and returned in the same order |
| Create invite | Code | Eight unbiased Base62 characters sampled with `crypto/rand`; a unique constraint collision causes a fresh code and transaction retry |
| Create invite | Expiry and limit | One transaction timestamp plus validated request duration, and optional validated `max_uses`; regeneration defaults omitted expiry to 24 hours and omitted limit to unlimited |
| Create or reuse invite | Creator and policy permission | Active caller membership under the group row lock; only role `captain` may supply any policy field |
| Create or list invite | Invite URL | Parsed HTTPS `APP_INVITE_BASE_URL` plus the raw code as one path segment |
| List active invites | Rows and order | `group_invites` filtered by current transaction time and ordered by `(created_at, id)` descending after active membership authorization |
| Preview invite | Group name, member count, Captain name | Invite group, count of active memberships, and active Captain joined to the group public profile fields |
| Join group | Membership result | Existing membership status under the group lock, or inserted membership row |
| Join group | Invite and group capacity | For new join or reactivation, locked invite `use_count` and `max_uses`, plus count of active memberships capped at 50 |
| Leave or remove member | Open payable and receivable totals | Separate sums of unsettled `debts.amount` (`status NOT IN ('settled', 'voided')`, corrected 2026-08-21) for target debtor and target creditor within the group |
| Transfer Captain | Previous and current Captain | Locked active membership rows for the authenticated Captain and target member |
| Rename group | Old name, new name, actor, and description | Locked active group row, trimmed request name, active Captain membership, current user display name, and the template `{CaptainName} đã đổi tên nhóm thành "{NewName}"` |
| Disband group (added 2026-08-21, AC-9) | Whether the group can be disbanded | Count of `bills` with `status NOT IN ('finalized', 'voided')` for the group, and count of `debts` with `status NOT IN ('settled', 'voided')` for the group; both must be zero |
| Preview and join rate limit | Limit, window, account key, IP key, retry delay | `HTTP_RATE_LIMIT_REQUESTS_PER_MINUTE`, one shared UTC epoch minute bucket across both endpoints, authenticated user ID, the direct TCP peer IP supplied by Chi `ClientIPFromRemoteAddr`, and seconds until the next epoch minute with a minimum of one; `X-Forwarded-For`, `Forwarded`, and similar headers are ignored |
| Legacy invite migration | Replacement code and terminal state | Invite ID order, the left padded Base62 encoding of its zero based row number, and `revoked_at = COALESCE(revoked_at, migration_time)` |
| Activity timeline | Actor and event details | Persisted `group_activities`, current group public actor profile, and public metadata that excludes invite codes |
| Paginated list | `next_cursor` | Opaque encoding of the last returned row `(created_at, id)`, or JSON `null` when no later row exists |
| Activity timeline | Description | Persisted at mutation time from a stable server template and the actor and target display names loaded in that transaction |

### Key invariants

1. Every group mutation first locks its `groups` row and completes in a short database transaction with no external call.
2. Group creation commits the group, its initial Captain membership, and `group_created` activity together.
3. An active group has at most one active Captain by partial unique index and at least one active Captain by the create, transfer, and exit transactions. An archived group (AC-9) has zero active memberships of any role, by design; this invariant only holds while `groups.status = 'active'`.
4. Captain transfer locks the group first, then both memberships in ascending membership ID order. It demotes the old Captain before promoting the target inside one transaction, which satisfies the immediate partial unique index and exposes only the final committed state.
5. Membership exit is allowed only when separate payable and receivable queries both return zero for unsettled debts (a debt is unsettled when its status is neither `settled` nor `voided`, corrected 2026-08-21, see Follow-up for the matching code fix). A zero value from `v_member_balances` alone never grants exit.
6. Every group, bill, and settlement transaction that creates or changes group scoped state calls the shared `database.LockActiveGroup` repository helper before its first scoped write. The helper runs `SELECT id FROM groups WHERE id = $1 AND status = 'active' FOR UPDATE`; this makes the lock and active status guard mechanically shared across modules, so debt creation cannot race with member exit or disbandment. The Captain transfer variant uses the same predicate with `NOWAIT`.
7. Invite creation, regeneration, revocation, and redemption recheck state after acquiring the active group row lock. The service creates at most one available invite per group. At one captured transaction timestamp, an invite is available exactly when `revoked_at IS NULL`, `expires_at > transaction_time`, and either `max_uses IS NULL` or `use_count < max_uses`.
8. Rejoining uses an atomic upsert on `(group_id, user_id)`, preserves the original membership ID, sets role to `member`, and clears `left_at`.
9. Group capacity is 50 active members. There is no per user active group quota in v1 because the PRD does not define a value.
10. Invite codes are treated as sensitive values. They are returned only by active invite list and create or reuse responses, accepted only by preview and join, and redacted everywhere else, including HTTP access logs.
11. For an existing invite code whose group already has the caller as an active member, join returns `already_active` before invite availability or group capacity checks. It does not increment usage or append activity.
12. Group detail returns `404 GROUP_NOT_FOUND` when the group is missing or the caller is not an active member, so the endpoint does not reveal group existence.
13. (Added 2026-08-21, AC-9) Disbanding is all or nothing: the `groups` row lock, the `bills`/`debts` precondition check, marking every membership inactive, revoking invites, and the `group_archived` activity all happen in one transaction. An archived group is never deleted and never re-derives `active` status; there is no un-disband path in v1.
14. Every generated code matches the database Base62 check. Bytes at or above 248 are discarded before modulo 62 so every character is unbiased. A unique collision retries the whole repository operation with a new transaction, at most five attempts. An exhausted retry budget returns an internal error and never revokes or creates an invite partially.
15. Preview and join consume one shared route limiter before accessing an invite. The account and IP buckets are independent and both must be below the limit. Every attempt increments both UTC epoch minute buckets under one mutex, buckets older than the current minute are pruned, and exceeding either bucket returns `429 RATE_LIMITED` with `Retry-After` equal to whole seconds until the next epoch minute, rounded up with a minimum of one. Only the direct TCP peer address is used; deployments behind a proxy therefore share that proxy's IP bucket unless they add an explicitly trusted proxy boundary in a future version.
16. Every valid rename request writes one `group_renamed` activity, including when the trimmed new name equals the current name. This keeps the success and audit contract deterministic.

### Activity contract

| Action type | Actor | Stable description template | Required metadata |
|---|---|---|---|
| `group_created` | Initial Captain | `{ActorName} created the group "{GroupName}"` | `group_id` |
| `invite_created` | Active creator member | `{ActorName} created an invite` | `invite_id`, `expires_at`, and nullable `max_uses`, never code |
| `invite_revoked` | Captain | `{ActorName} revoked an invite` | `invite_id` |
| `member_joined` | Joining member | `{ActorName} joined the group` | `member_id` |
| `member_reactivated` | Rejoining member | `{ActorName} rejoined the group` | `member_id` |
| `member_left` | Leaving member | `{ActorName} left the group` | `member_id` |
| `member_removed` | Captain | `{ActorName} removed {TargetName} from the group` | `target_member_id` |
| `captain_transferred` | Previous Captain | `{ActorName} transferred the Captain role` | `previous_captain_member_id` and `current_captain_member_id` |
| `group_renamed` | Captain | `{ActorName} đã đổi tên nhóm thành "{NewName}"` | `old_name` and `new_name` |
| `group_archived` | Captain | `{ActorName} archived the group` | `member_count_deactivated`, sourced from the affected row count of the locked membership update |

Every activity description is a snapshot written in the mutation transaction. Timeline reads never recompute it from a later display name. Activity metadata returned by the API uses exactly the keys in this table.

### Security model

1. Every endpoint requires a valid bearer token and live active session.
2. Invite preview requires authentication but does not require existing membership.
3. Group detail and activity reads require an active membership in the target group. Group detail maps both a missing group and a nonmember caller to `404 GROUP_NOT_FOUND`.
4. Invite creation with no policy fields and active invite listing require an active member. Invite policy fields, invite revocation, rename, Captain transfer, and disbandment require the current active Captain.
4a. Disbanding requires the caller to be the current active Captain. Once `groups.status = 'archived'`, every group scoped read or write responds as it already does for a non member. **Enforcement mechanism**: disbandment removes every active membership. Group queries filter `groups.status = 'active'`, and every group, bill, and settlement mutation uses the shared active group lock helper inside its transaction before the first scoped write. Background OCR, reminder, and stalled payment candidate queries join only active groups; their draft bill or open debt or payment candidate also independently blocks disbandment. This repository level gate covers bill requests whose group ID exists only in a JSON or multipart body, where a path middleware cannot reliably resolve it.
5. A standard member may target only their own membership for exit. After exit, the membership owner may repeat the same request and receive `204` without regaining group access. A Captain may target another standard member for removal.
6. Group scoped reads and writes include `group_id`. Composite foreign keys prevent a child row from referencing a membership in another group.
7. API responses expose only the group public profile fields defined above. Invite codes and full internal activity metadata are never logged.
8. Preview and join enforce independent account and IP buckets after live authentication. The v1 limiter is process local because deployment currently runs one API instance. A multi replica deployment must move the counters to a shared store first.

### Transaction and concurrency contract

| Flow | Lock and atomic work |
|---|---|
| Create group | Insert group, Captain membership, and activity in one transaction |
| Create or regenerate invite | Lock the active group, verify active membership, reject policy fields for a non Captain, select the available invite by `(created_at, id) DESC`, then reuse it or revoke every available invite before inserting the new invite and creation activity |
| Rename group | Lock the active group, verify Captain, capture the old name and actor display name, update the name, append `group_renamed`, then commit |
| Revoke invite | Lock group, verify Captain, find the invite in that group, return `204` for any current state, and update plus append one activity only when `revoked_at IS NULL` |
| Redeem invite | Resolve group from an existing code and lock group. Return `already_active` immediately for an active membership. Otherwise lock and revalidate the invite, enforce capacity, increment use count, upsert membership, and append one join or reactivation activity |
| Leave or remove | Lock group then target membership, verify permission and role, sum open debts, mark inactive, append activity |
| Transfer Captain | Acquire the group row with `FOR UPDATE NOWAIT`, map lock contention to `409 CAPTAIN_TRANSFER_CONFLICT`, lock both memberships by ascending ID, verify roles, demote the old Captain, promote the target, append activity, then commit |
| Disband group (added 2026-08-21, AC-9) | Lock group, verify Captain, check zero bills outside `finalized`/`voided` and zero debts outside `settled`/`voided`, mark every active membership inactive, revoke every available invite, set `groups.status = 'archived'`, append `group_archived` activity, then commit |

### Configuration required

| Variable | Purpose |
|---|---|
| `APP_INVITE_BASE_URL` | Required at startup and parsed as an absolute HTTPS URL with a host, no user info, query, or fragment; trailing slashes are removed from a non root path, for example `https://paysplit.app/join`, then the raw code is appended as exactly one path segment. Missing or invalid configuration prevents API startup |
| `HTTP_RATE_LIMIT_REQUESTS_PER_MINUTE` | Shared positive request count for the global IP limiter and the invite attempt account plus IP limiter; default 30 |

### Critical test scenarios

1. Create, list, and read a group through PostgreSQL and HTTP, verifies **AC-1** and **AC-2**.
2. A standard member reuses or creates the default invite with no body, cannot include even `regenerate: false`, and cannot revoke, while the Captain can configure and regenerate, verifies **AC-3**.
3. An authenticated nonmember previews a valid invite but cannot read group detail, verifies **AC-4**.
4. Concurrent redemption of the final invite use or the final group slot permits one winner without exceeding either limit, verifies **AC-5**.
5. Joining twice returns `already_active` without consuming another use or writing another activity, including after that invite expires or is revoked. Leaving then rejoining preserves the membership ID, verifies **AC-5**.
6. A member with equal payable and receivable totals cannot leave until every related debt is settled, verifies **AC-6**.
7. Concurrent Captain transfers produce one Captain and one `409 CAPTAIN_TRANSFER_CONFLICT`, while an ordinary non Captain attempt returns `403 CAPTAIN_REQUIRED`, verifies **AC-7**.
8. Every confirmed group mutation writes exactly one activity in the same transaction and paginates without duplicates, verifies **AC-8**.
9. A Captain disbands a group with a `voided` debt and only finalized or voided bills. An awaiting debt or reviewed bill returns the correct `409` counts. After success, every membership is inactive, the group is archived, and later group, bill, or settlement writes are refused. Concurrent disband versus bill creation or OCR completion proves that the active group lock or blocking draft bill wins safely, verifies **AC-9**.
10. Active invite listing returns only available rows newest first to a member and returns the same `404` for a missing group or non member, verifies **AC-10**.
11. Rename trims Unicode input, returns the updated public group, and writes exactly one activity. Invalid names and non Captain callers receive the specified errors, verifies **AC-11**.
12. Generated codes, the database constraint, URL path construction, forced unique collision retry, unified unavailable response, redaction, and independent account plus IP limits all hold, verifies **AC-12**.
13. Concurrent member exit versus bill finalization and debt creation uses the shared active group lock, so either the exit commits before the writer is rejected or the debt commits before exit observes and rejects the open obligation, verifies **AC-6** and **AC-9**.

## Build plan

The project uses Tracer Bullet, so each slice crosses schema, SQLC, repository, usecase, HTTP, integration tests, and API documentation before the next slice begins.

1. [x] Build the create, list, and detail slice. Add the exact group validation and query indexes, public response DTOs, group and Captain creation transaction, privacy preserving detail authorization, cursor reads, routes, and real PostgreSQL coverage, satisfies **AC-1** and **AC-2**.
2. [x] Build the Captain invite slice. Add invite SQLC queries, cryptographic code generation, configured invite URL construction, single available invite behavior, regeneration, idempotent revocation, authorization, redacted access logs, and activity writes, satisfies **AC-3** and **AC-8**.
3. [x] Build the preview and redemption slice. Add authenticated preview, locked invite redemption, group capacity enforcement, atomic membership upsert, usage increment, and concurrent integration coverage, satisfies **AC-4**, **AC-5**, and **AC-8**.
4. [x] Build the membership exit and Captain transfer slice. Add separate open debt totals, self and Captain permission checks, idempotent inactivation, ordered locking, nonblocking transfer lock, atomic role transfer, and stable conflict mapping, satisfies **AC-6**, **AC-7**, and **AC-8**.
5. [x] Complete the activity timeline and delivery contract. Extend `activity_type`, add the stable timeline index and cursor query, finish shared error mapping, OpenAPI, module documentation, and end to end verification of all eight criteria, satisfies **AC-1** through **AC-8**.
6. [x] (Added 2026-08-21, fixed 2026-08-21 via `/debug`) Fix the `voided`-exclusion drift: corrected `SumOpenDebtorTotal`/`SumOpenCreditorTotal` (`internal/modules/group/repository/postgres/queries/groups.sql`) to `status NOT IN ('settled', 'voided')`, migrated `idx_debts_group_debtor_unsettled`/`idx_debts_group_creditor_unsettled` to match (`db/migrations/000008_group_exit_voided_debt_fix.sql`), and added `TestLeaveOrRemoveMember_VoidedDebtDoesNotBlockExit` (fails without the fix, passes with it), satisfies the corrected **AC-6**. The same bug was also found and fixed in the admin module's `GetOutstandingDebtsByUserID`/`GetOutstandingCreditsByUserID` (`internal/modules/admin/repository/postgres/queries/admin.sql`), not this spec's surface but the same root cause.
7. [x] Build the invite sharing slice. Add the governance migration, Base62 generator and collision retry, path based invite URLs, active member create or reuse authorization, active invite list, unified unavailable response, OpenAPI, and database plus HTTP coverage, satisfies **AC-3**, **AC-4**, **AC-10**, and **AC-12**.
8. [x] Build the rename slice. Add the locked Captain transaction, exact Unicode validation, `group_renamed` activity, `PATCH /api/v1/groups/{id}`, OpenAPI, and integration coverage, satisfies **AC-8** and **AC-11**.
9. [x] Build the disband slice. Add the disband transaction, corrected bill and debt precondition checks, membership inactivation, invite revocation, active group write gates in group, bill, and settlement repositories, `DELETE /api/v1/groups/{id}`, OpenAPI, and integration coverage, satisfies **AC-9**.
10. [x] Complete invite security hardening. Mount the account plus IP limiter after live authentication on preview and join, prove independent buckets, rerun log redaction checks, and finish end to end verification, satisfies **AC-4**, **AC-5**, and **AC-12**.

## Consequences

**Positive**:

1. Membership history and foreign key continuity survive leave and rejoin.
2. Separate debt checks prevent a net zero member from leaving with unresolved obligations.
3. One group coordination lock makes concurrent membership, invite, and future debt writes predictable.
4. Access control changes have a durable activity trail.
5. Members can share a stable active invite without receiving Captain governance powers.

**Negative and tradeoffs**:

1. Future bill and settlement modules must honor the same group row lock before creating or changing unsettled debt.
2. Captain transfer and invite redemption require hand written transaction methods around generated SQLC queries.
3. Plain invite codes remain stored in the existing schema so an active invite can be returned again. Database access and log redaction therefore remain important.
4. (Resolved 2026-08-21, AC-9) A sole Captain could not leave in v1 because group archive or deletion behavior was not yet defined; disbanding now gives the sole Captain a defined exit once all obligations are clear, though a sole Captain still cannot merely "leave" and hand the group to nobody.
5. The process local account and IP limiter is correct only while one API instance serves traffic. Moving to multiple replicas requires a shared counter store.
6. Rotating and revoking legacy invite rows invalidates previously shared old format links. Members must share the newly issued eight character code after deployment.

**Neutral**:

1. `v_member_balances` remains useful for display but is not an authorization source for member exit.

## Follow-up

1. [x] Define group archive or deletion behavior before allowing the sole Captain to leave. Resolved 2026-08-21: **AC-9**, soft archive (`groups.status = 'archived'`), never delete history. This was prompted by `docs/change-req/api-change-request-01.md`'s Disband Group proposal, which had the same `debts.status <> 'settled'` bug as item below and left the delete-vs-archive choice unresolved; both are settled here.
2. [ ] Define a per user active group quota only when product supplies a concrete limit. V1 has no such quota.
3. [x] Reconcile the Group management row in `docs/scope/scope.md` with this updated acceptance contract before implementation tracking advances.
4. [ ] Record the installed PostgreSQL conventions in a root or database area `AGENTS.md`.
5. [x] **Live bug, fixed 2026-08-21 via `/debug`**: `internal/modules/group/repository/postgres/queries/groups.sql`'s `SumOpenDebtorTotal`/`SumOpenCreditorTotal` filtered `status <> 'settled'`, so a member whose only debt was `voided` (their bill got cancelled, spec 0003) was wrongly blocked from leaving the group with `409 GROUP_MEMBER_HAS_OPEN_DEBTS`. Fixed both queries to `status NOT IN ('settled', 'voided')` and the two backing partial indexes via `db/migrations/000008_group_exit_voided_debt_fix.sql` (`CREATE INDEX CONCURRENTLY` in a `NO TRANSACTION` migration, as this spec required above). Verified live: `TestLeaveOrRemoveMember_VoidedDebtDoesNotBlockExit` failed before the fix, passes after; the full suite is green.
6. [x] Reconcile `docs/change-req/api-change-request-01.md` with this spec. Resolved 2026-08-22 by **AC-3**, **AC-10**, **AC-11**, and **AC-12**.
7. [ ] Replace the process local invite attempt limiter with a shared PostgreSQL or Redis counter before deploying more than one API replica.

## Migration plan

**Strategy**: Direct replacement inside one maintenance window. Stop request and worker traffic, run the transactional schema and data migration, deploy the compatible API and workers, verify invite creation and archived group gates, then resume traffic.

**Phases**:

1. Stop API mutation traffic and background workers. Run the governance preflight; abort without changing data if an active group does not have exactly one active Captain.
2. Deploy the migration. It adds group status and activity values, revokes every legacy invite, replaces legacy raw codes with unique compliant historical placeholders, and validates the Base62 check.
3. Deploy the API and worker code that generates eight character codes, builds path links, applies the new permissions and active group lock, and serves rename and disband routes.
4. Run smoke verification, then resume traffic.

**Rollback**: Forward fix only after phase 2 begins. The pre change API generator is incompatible with the Base62 database check, activity enum values cannot be removed safely, archived groups cannot be reconstructed automatically, and legacy raw links cannot be restored. Keep traffic stopped and deploy a corrected API or migration that remains compatible with the new schema. Before phase 2, the preflight abort is a normal rollback with no changes.

**Risks**: A deployment that applies the migration but does not deploy the API leaves every legacy invite revoked and the old generator unable to insert. The maintenance window and forward fix rule are therefore mandatory. Archived history is intentionally storage only in v1: no user route can read it after disbandment, while operators retain it for database audit and a future constrained history surface.
