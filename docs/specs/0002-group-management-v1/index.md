# 0002. Group management v1

**Date**: 2026-08-17
**Status**: Proposed

## Summary

Group management v1 provides expense group creation, member management, invite code generation, invite redemption, group preview, activity logging, and safe member exit. The captain creates the group and becomes the initial active captain. Members can generate or reuse invite codes with a default 24 hour expiration. Members leaving or being removed must have a net zero balance, and captains must transfer their role before leaving. Rejoining a group reactivates the original member record to preserve expense history.

## Rationale

Reasoning and options considered: see [rationale.md](rationale.md).

## Requirements

**User stories**:

1. As an authenticated user, I want to create a new expense group so that I can track shared expenses with my friends.
2. As a group member, I want to view my active groups and group details so that I can stay updated on group activities.
3. As a group member, I want to generate or reuse invite codes so that I can invite others to join the group.
4. As an invited user, I want to preview group details before joining so that I can confirm the group identity.
5. As an invited user, I want to join a group using an invite code so that I can participate in group expense splitting.
6. As a group member or captain, I want to leave or remove members safely so that group history is preserved and no unsettled debts are left behind.

**Acceptance criteria**:

- **AC-1**: Creating a group with a valid name sets the creator as captain in `group_members`, defaults currency to `VND`, and returns `201` with the group details.
- **AC-2**: Listing groups returns all groups where the authenticated user is currently an active member, ordered by creation time descending.
- **AC-3**: Invite code generation accepts a custom expiration duration defaulting to 24 hours. If an active unexpired invite code created by the user exists, it is returned instead of creating a duplicate.
- **AC-4**: Invite code preview returns basic group information (group name, member count, captain display name) for any valid unexpired invite code without requiring group membership.
- **AC-5**: Joining a group via a valid invite code adds the user as an active member or reactivates an existing inactive member record (`status='active'`, `left_at=NULL`). It rejects expired, revoked, or maxed out invite codes, as well as users who are already active members.
- **AC-6**: Removing a member or leaving a group enforces that the member's net balance in `v_member_balances` is zero. If the net balance is non zero, the request is rejected with `400` and `GROUP_MEMBER_BALANCE_NOT_ZERO`.
- **AC-7**: A captain cannot leave the group or be removed while active. The captain role must first be transferred to another active member in the group.
- **AC-8**: Group activity log retrieves chronological activities for a group and is accessible only to active group members.

## Decision

**Chosen option**: Option 1: Modular group service with database level group isolation and zero net balance safety checks.

Implement group operations under `internal/modules/group/` following Modular Clean Architecture. Store group memberships with composite keys `(id, group_id)` and enforce RBAC and zero balance checks in the usecase layer.

**Implementation skills**: `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Feature design

### Data model sketch

| Entity | Key fields | Constraints and relationships |
|---|---|---|
| `groups` | UUID v7 `id`, `name`, `currency`, `created_by`, `created_at` | Primary ID defaults to `uuidv7()`. `created_by` references `users(id)`. Currency defaults to `VND`. |
| `group_members` | UUID v7 `id`, `group_id`, `user_id`, `role`, `status`, `joined_at`, `left_at` | Composite unique `(group_id, user_id)` and `(id, group_id)`. Partial unique index ensures exactly one active captain per group. |
| `group_invites` | UUID v7 `id`, `group_id`, `code`, `created_by`, `expires_at`, `max_uses`, `use_count`, `revoked_at`, `created_at` | `created_by` and `group_id` foreign key references `group_members(id, group_id)`. `code` is unique string. |
| `group_activities` | UUID v7 `id`, `group_id`, `actor_member_id`, `action_type`, `description`, `metadata`, `created_at` | `actor_member_id` and `group_id` foreign key references `group_members(id, group_id)`. |
| `v_member_balances` | `group_id`, `member_id`, `net_balance` | SQL view calculating net settled debts balance per group member. |

### State transitions

```text
group_members: (none) → active → inactive → active (reactivated)
group_invites: active → expired | revoked | max_used
```

### API surface

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| `/api/v1/groups` | POST | `name`: string (req), `currency`: string (opt) | group ID, name, currency, role, timestamps | bearer | `VALIDATION_FAILED` |
| `/api/v1/groups` | GET | none | list of groups with role and member count | bearer | `AUTHENTICATION_REQUIRED` |
| `/api/v1/groups/{id}` | GET | `id`: path UUID | group details, members list, net balance | bearer + member | `GROUP_NOT_FOUND`, `FORBIDDEN` |
| `/api/v1/groups/{id}/invites` | POST | `expires_in_hours`: int (opt) | invite code, invite URL, expires_at | bearer + member | `GROUP_NOT_FOUND`, `FORBIDDEN` |
| `/api/v1/groups/invites/{code}` | GET | `code`: path string | group name, captain name, member count | bearer | `INVITE_NOT_FOUND`, `INVITE_EXPIRED` |
| `/api/v1/groups/join` | POST | `code`: string (req) | group ID, member ID, role, status | bearer | `INVITE_EXPIRED`, `ALREADY_MEMBER` |
| `/api/v1/groups/{id}/members/{memberId}` | DELETE | `id`: path UUID, `memberId`: path UUID | no body (`204`) | bearer + member | `BALANCE_NOT_ZERO`, `CAPTAIN_CANNOT_LEAVE` |
| `/api/v1/groups/{id}/members/{memberId}/role` | PUT | `role`: string (req) | updated member role | bearer + captain | `MEMBER_NOT_FOUND`, `INVALID_ROLE` |
| `/api/v1/groups/{id}/activities` | GET | `page`: int (opt), `limit`: int (opt) | list of group activity logs | bearer + member | `GROUP_NOT_FOUND`, `FORBIDDEN` |

### Value sourcing

| Action | Value produced / displayed | Source |
|---|---|---|
| Create group | Group ID and captain member ID | Database generated UUID v7 from `groups` and `group_members` insert |
| List groups | Active groups for user | `group_members` filtered by `user_id` and `status='active'` joined with `groups` |
| Invite code | Code string and expiration | Generated random string, `expires_at` computed from current time plus requested hours |
| Join group | Member status and role | Inserted or updated `group_members` row with `status='active'` and `role='member'` |
| Member exit | Net balance verification | Computed sum from `v_member_balances` for `member_id` |

### Key invariants

1. Every active group must have exactly one active captain enforced by PostgreSQL partial unique index.
2. A member cannot leave or be removed if their net balance in `v_member_balances` is not zero.
3. Captain role transfer requires the target member to be an active member of the same group.
4. Rejoining a group must update the existing `group_members` row (`status='active'`, `left_at=NULL`) to preserve foreign key historical references.

### Security model

- All group endpoints except public invite preview require a valid Bearer JWT.
- Group member access middleware verifies that the authenticated user has an active record in `group_members` for the requested group ID.
- Captain restricted endpoints (role transfer, member removal of non self) check that the caller's role is `captain`.

### Configuration required

No new environment variables required. Group management uses existing PostgreSQL connection and JWT authentication settings.

### Critical test scenarios

- Happy path: User creates group, generates invite code, second user joins via code, verifies AC-1, AC-3, AC-5.
- Failure case: Member with non zero debt tries to leave group and receives `400` with `GROUP_MEMBER_BALANCE_NOT_ZERO`, verifies AC-6.
- Auth/permission: Non member tries to access group details or activities and receives `403 FORBIDDEN`, verifies AC-8.
- Captain protection: Captain attempts to leave group without transferring role and receives `400 CAPTAIN_CANNOT_LEAVE`, verifies AC-7.

## Build plan

1. SQLC queries for groups, group_members, group_invites, group_activities, and v_member_balances, satisfies **AC-1**, **AC-2**, **AC-3**, **AC-4**, **AC-5**, **AC-6**, **AC-7**, **AC-8**
2. Group domain entities, repository interfaces, and business errors in `internal/modules/group/domain/`, satisfies **AC-1**, **AC-6**, **AC-7**
3. Postgres repository implementation translating SQLC models in `internal/modules/group/repository/postgres/`, satisfies **AC-1**, **AC-2**, **AC-3**, **AC-4**, **AC-5**, **AC-6**, **AC-7**, **AC-8**
4. Group usecase service enforcing business logic, RBAC, balance checks, and invite code handling in `internal/modules/group/usecase/`, satisfies **AC-1**, **AC-2**, **AC-3**, **AC-4**, **AC-5**, **AC-6**, **AC-7**, **AC-8**
5. Group HTTP delivery layer (handlers, DTOs, group authorization middleware, and Chi routes) in `internal/modules/group/delivery/http/`, satisfies **AC-1**, **AC-2**, **AC-3**, **AC-4**, **AC-5**, **AC-6**, **AC-7**, **AC-8**
6. Integration into `internal/bootstrap/app.go` for dependency injection and route registration under `/api/v1`, satisfies **AC-1**, **AC-2**

## Consequences

**Positive**:
- Strict database constraints prevent cross group data leakage.
- Zero balance check prevents unresolved debts when members leave.
- Member reactivation preserves audit trail for bills and payment historical records.

**Negative / tradeoffs**:
- Rejoining reactivates existing rows instead of creating new member IDs, requiring specific SQL update logic.
- Member deletion is blocked when balance is non zero, requiring debt settlement before exiting.

**Neutral**:
- Requires group authorization middleware on all group scoped routes.

## Follow-up

- [ ] Add integration test suite for group creation, join, and leave scenarios.

## References

**Project sources**:
- `db/migrations/000001_init_schema.up.sql`: Table definitions for `groups`, `group_members`, `group_invites`, `group_activities`, and `v_member_balances`
- `docs/screen_flow.md`: Module 2 Group Management API endpoints
- `docs/scope/scope.md`: Slice 2 Group Management acceptance criteria

**Practices & standards**:
- Modular Clean Architecture backend organization in `internal/modules/`
- PostgreSQL composite foreign keys for multi tenant boundary isolation
