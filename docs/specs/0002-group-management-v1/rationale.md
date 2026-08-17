# Rationale: 0002 Group management v1

## Context

Group management is the access and isolation boundary for every later bill, debt, and payment workflow. Membership history must stay stable after a person leaves because financial rows reference `group_members`, not `users`. Invite redemption, member exit, Captain transfer, and future debt creation can run concurrently, so correctness cannot depend on separate check then write calls.

The PRD requires Captain controlled invites, a 50 member limit, preserved financial history, and removal only after all obligations are settled. The current schema already provides composite group foreign keys, reusable membership identities, invite usage fields, a partial unique Captain index, and a derived member balance view. The design must distinguish a net zero display balance from the absence of open debt.

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

## Rationale

Option 1 provides the strongest practical consistency without introducing a second service or moving the whole usecase into database triggers. A short group row lock gives all conflicting flows one ordering rule. It also lets the current partial unique index keep enforcing at most one active Captain while service transactions preserve at least one.

The exit decision uses absence of open debtor and creditor rows, not only `v_member_balances`. Net values are correct for the group summary but can hide two equal unresolved obligations. This stricter rule matches the PRD instruction that a removed member has neither outstanding debt nor outstanding credit.

Captain only invite mutation follows the PRD permission model. Invite preview still requires authentication because the PRD starts the join flow with an authenticated user, and this avoids adding a public enumeration and rate limit surface in v1. Join is idempotent because mobile deep links and client retries can submit the same valid invite more than once. An already active member does not consume another invite use or create another activity.

Captain transfer uses a nonblocking group lock so a concurrent request receives a deterministic conflict instead of waiting and later appearing to be an ordinary permission failure. Group detail returns the same not found response for missing groups and nonmembers, and group profile DTOs exclude contact and bank data.

## References

**Project sources**:

1. `docs/Product_Requirement_Document.md`: Group creation, Captain invites, join behavior, member removal, and the 50 member limit.
2. `docs/screen_flow.md`: Module 2 routes and mobile group flow.
3. `docs/scope/scope.md`: Group management intent and Tracer Bullet build approach.
4. `db/migrations/000001_init_schema.up.sql`: Group tables, composite foreign keys, activity enum, debts, and `v_member_balances`.
5. `docs/specs/0001-auth-account-v1/index.md`: Live session authentication and shared public error contract.

**Practices and standards**:

1. PostgreSQL short transactions and consistent lock ordering.
2. Atomic upsert for insert or reactivate behavior.
3. Cursor pagination using `(created_at, id)`.
4. Composite foreign keys for group isolation.
