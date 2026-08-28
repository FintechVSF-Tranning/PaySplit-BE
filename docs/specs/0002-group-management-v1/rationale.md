# Rationale: 0002 Group management v1

## Context

Group management is the access and isolation boundary for every later bill, debt, and payment workflow. Membership history must stay stable after a person leaves because financial rows reference `group_members`, not `users`. Invite redemption, member exit, Captain transfer, and future debt creation can run concurrently, so correctness cannot depend on separate check then write calls.

The original PRD required Captain controlled invites. The approved FE change request now requires every active member to share the current invite while the Captain keeps control of invite policy, rename, revocation, and disbandment. The current schema already provides composite group foreign keys, reusable membership identities, invite usage fields, a partial unique Captain index, and a derived member balance view. The design must preserve those consistency rules while changing the public invite contract and keeping financial history intact.

## Drift check against shipped spec 0003, and the disband proposal (2026-08-21)

This spec shipped (`Accepted`) before spec 0003 (`docs/specs/0003-bill-ocr-v1/`) added a `voided` value to `debt_status`. A cross check triggered by reviewing `docs/change-req/api-change-request-01.md` (a separate, non `docs/specs/` document proposing invite and group governance changes, requested by the FE team 2026-08-20) found two consequences:

- **A shipped bug, now fixed.** This spec's AC-6 member exit queries originally filtered `status <> 'settled'`. Once `voided` existed, that filter wrongly blocked a member whose only debt had been cancelled. Migration 000008, the corrected queries, and the regression test now exclude both `settled` and `voided`; Build plan item 6 and Follow-up item 5 preserve the repair history.
- **The Disband Group proposal in `docs/change-req/api-change-request-01.md` had the identical bug** in its own precondition query, plus left the delete-versus-archive decision unresolved ("Xóa mềm / Lưu trữ nhóm ... hoặc xóa nhóm theo chính sách xóa cascade dữ liệu"). Decision (below, under Options considered's original Option 1 spirit: prefer the boring, already-established pattern) resolves both as **AC-9**: soft archive via `groups.status`, corrected debt predicate, never delete. This is additive to this spec, not a reopening of its already accepted decisions.

The remaining proposals in `docs/change-req/api-change-request-01.md` were reconciled on 2026-08-22. AC-3 now allows an active member to share the default invite, AC-10 adds active invite listing, AC-11 adds rename, and AC-12 fixes the code, link, retry, redaction, unified error, and account plus IP rate limit contract.

## Governance enhancement decision (2026-08-22)

The existing group row remains the coordination point. Member sharing is not a new governance role: a standard member can only reuse the current invite or create one with fixed defaults. Supplying any policy field remains a Captain action, even when the supplied value is `false` or otherwise matches a default.

Legacy invite rows cannot satisfy the new eight character check. Keeping them active with a hidden mapping would add a second code authority and conflict with the change request. The migration therefore revokes every legacy invite, replaces its stored value with a unique compliant historical placeholder, and lets a later member request create the new shareable code.

Preview and join use one route specific UTC epoch minute window keyed independently by authenticated account and the direct TCP peer IP. The router already uses Chi `ClientIPFromRemoteAddr`, so forwarding headers remain untrusted and cannot spoof the key; a deployment behind a proxy intentionally shares that proxy's IP bucket until an explicit trusted proxy boundary is designed. The limiter has its own positive `HTTP_INVITE_ATTEMPTS_PER_MINUTE` value. It started out sharing the global per IP budget, but that coupling meant raising the ceiling for ordinary read traffic also widened the invite code guessing window, so the two are now sized independently. This is suitable only for the current single instance deployment. A shared PostgreSQL or Redis counter is required before adding replicas.

## Options considered

### Option 1: Modular service with group scoped transactions (Chosen)

Keep the existing modular Go and PostgreSQL architecture. Use the `groups` row as the coordination lock for invite, membership, Captain, and future debt mutations. Keep composite foreign keys and reactivate the existing membership row. (basis: the existing module boundaries, initial migration, and PostgreSQL short transaction practices)

**Pros**:

1. Uses the current stack and schema direction.
2. Gives deterministic behavior under concurrent requests.
3. Preserves historical membership IDs and group isolation.

**Cons**:

1. Every future debt writer must follow the group lock contract.
2. Multi row flows require explicit repository transaction methods.

### Option 2: Application checks without coordination locks

Read invite, membership, Captain, or balance state in the usecase, then issue separate writes when validation passes. Use the existing indexes only for uniqueness. (basis: the simplest conventional CRUD implementation)

**Pros**:

1. Requires less transaction code for the first implementation.
2. Individual repository methods stay small.

**Cons**:

1. Two concurrent invite redemptions can exceed `max_uses` or the 50 member limit.
2. Debt creation can race with member exit.
3. Captain transfer can temporarily produce zero Captains or fail halfway.

### Option 3: Database triggers for every business invariant

Move Captain existence, invite capacity, membership exit eligibility, and activity creation into PostgreSQL triggers and database functions. (basis: database enforced invariant design)

**Pros**:

1. Protects invariants even when writes bypass the application.
2. Centralizes consistency next to the data.

**Cons**:

1. Authorization and public error mapping become harder to understand and test.
2. Business rules split between Go and procedural SQL.
3. Trigger ordering and migration changes raise operational complexity for this team and scope.

### Invite governance options considered

**Fix in place (chosen)**: Extend the existing group transaction so active members can share defaults while the Captain retains policy control. Rotate legacy codes once and keep one raw code authority. (basis: the existing group module, the approved change request, and the strangler pattern for live enhancements)

**Keep Captain only sharing**: Preserve the existing API and ask the Captain to relay every invite. This has the lowest migration cost, but it does not satisfy the Group Hub workflow.

**Create a separate short code service**: Keep old codes and map new eight character codes through another table or service. This permits gradual link migration, but it creates two code authorities, extra lookup state, and a larger security surface for a small feature.

For rate limiting, a shared PostgreSQL event table was the runner up. It works across replicas and survives restarts, but it adds write load and cleanup work that the current one instance deployment does not need.

## Rationale

Option 1 provides the strongest practical consistency without introducing a second service or moving the whole usecase into database triggers. A short group row lock gives all conflicting flows one ordering rule. It also lets the current partial unique index keep enforcing at most one active Captain while service transactions preserve at least one.

The exit decision uses absence of open debtor and creditor rows, not only `v_member_balances`. Net values are correct for the group summary but can hide two equal unresolved obligations. This stricter rule matches the PRD instruction that a removed member has neither outstanding debt nor outstanding credit.

Member invite sharing follows the Group Hub workflow without granting policy mutation. Invite preview still requires authentication, invalid invite states share one public response, and preview plus join are limited by both account and IP. Join remains idempotent because mobile links and client retries can submit the same valid invite more than once. An already active member does not consume another invite use or create another activity.

Captain transfer uses a nonblocking group lock so a concurrent request receives a deterministic conflict instead of waiting and later appearing to be an ordinary permission failure. Group detail returns the same not found response for missing groups and nonmembers, and group profile DTOs exclude contact and bank data.

## References

**Project sources**:

1. `docs/Product_Requirement_Document.md`: Group creation, Captain invites, join behavior, member removal, and the 50 member limit.
2. `docs/screen_flow.md`: Module 2 routes and mobile group flow.
3. `docs/scope/scope.md`: Group management intent and Tracer Bullet build approach.
4. `db/migrations/000001_init_schema.up.sql`: Group tables, composite foreign keys, activity enum, debts, and `v_member_balances`.
5. `docs/specs/0001-auth-account-v1/index.md`: Live session authentication and shared public error contract.
6. `docs/specs/0003-bill-ocr-v1/index.md`: source of the `debt_status.voided` value this spec's AC-6/AC-9 debt predicates must exclude alongside `settled`.
7. `docs/change-req/api-change-request-01.md`: the FE requested invite and governance change now covered by AC-3 and AC-9 through AC-12.

**Practices and standards**:

1. PostgreSQL short transactions and consistent lock ordering.
2. Atomic upsert for insert or reactivate behavior.
3. Cursor pagination using `(created_at, id)`.
4. Composite foreign keys for group isolation.
