# Scope: PaySplit Backend

PaySplit Backend provides the account, group expense, settlement, and notification APIs used by the PaySplit mobile application. This scope currently tracks the authentication and account slice, and the group management slice.

**Build approach:** Tracer Bullet (build a narrow real path through database, usecase, API, and provider adapters before adding breadth).
**Workflow:** GA (`/check verify`, `/test`, fresh model `/check review`, then `/document` after `/develop`). The project default level of rigor. `/architect` remains the recommended first stop whenever a feature has an unresolved decision.

_These are recommendations to keep the build orderly. You decide when a feature is done._

## At a glance

| # | Feature | Phase | Status |
|---|---|---|---|
| 1 | Auth and account v1 | Slice 1 | in-progress |
| 2 | Group management v1 | Slice 2 | in-progress |

## Slice 1: Identity and account

### 1. Auth and account v1 · in-progress

Provide registration, email verification, email password sign in, one active device, rotating refresh tokens, password recovery, profile updates, bank validation, and WebP avatars.

**Done when:** all nineteen acceptance criteria in spec 0001 pass against PostgreSQL 18, protected requests honor live sessions, Gmail and Cloudinary failures follow the public contract, and the API plus module documentation match the implemented behavior.

- [x] Design it (spec): `/architect auth and account v1`
- [x] Build it: `/develop auth and account v1`
  - [x] PostgreSQL 18 schema, generated queries, and live migration verification (AC-1, AC-5, AC-6, AC-8, AC-18, AC-19)
  - [x] Identity, session, refresh rotation, middleware, and shared errors (AC-1, AC-5 through AC-9, AC-16, AC-17)
  - [x] Email verification, password recovery, password change, and persisted rate limits (AC-2 through AC-4, AC-10, AC-11, AC-16, AC-17)
  - [x] Profile, bank snapshot, avatar conversion, Cloudinary storage, and durable cleanup (AC-12 through AC-15, AC-17)
  - [x] Cleanup workers, integration coverage, OpenAPI, environment guide, and module documentation (AC-15 through AC-19)
- [ ] Verify it: `/check verify auth and account v1`
- [ ] Test it: `/test auth and account v1`
- [ ] Review it (fresh model): `/check review auth and account v1`
- [ ] Document it: `/document auth and account v1`

Spec [0001](../specs/0001-auth-account-v1/index.md) · code in `internal/modules/auth/`, `internal/platform/`, `internal/transport/http/`, and `internal/bootstrap/`

## Slice 2: Group Management

### 2. Group management v1 · in-progress

Provide group creation, membership management, invite code generation, invite redemption, group preview, activity logging, and safe member removal enforcing zero net balance.

**Done when:** all eight acceptance criteria in spec 0002 pass against PostgreSQL 18, active memberships govern access, invite codes function correctly, member removal enforces net zero balance, and activity logs capture key events.

- [x] Design it (spec): `/architect group management v1`
- [ ] Build it: `/develop group management v1`
  - [ ] SQLC queries for groups, group_members, group_invites, group_activities, and v_member_balances (satisfies AC-1 through AC-8)
  - [ ] Group domain models, repository ports, and domain errors in `internal/modules/group/domain/` (satisfies AC-1, AC-6, AC-7)
  - [ ] Postgres repository implementation translating SQLC models in `internal/modules/group/repository/postgres/` (satisfies AC-1 through AC-8)
  - [ ] Group usecase service enforcing RBAC, balance checks, and invite links in `internal/modules/group/usecase/` (satisfies AC-1 through AC-8)
  - [ ] Group HTTP handlers, DTOs, authorization middleware, and routes in `internal/modules/group/delivery/http/` (satisfies AC-1 through AC-8)
  - [ ] App bootstrap integration and module wiring in `internal/bootstrap/app.go` (satisfies AC-1, AC-2)
- [ ] Verify it: `/check verify group management v1`
- [ ] Test it: `/test group management v1`
- [ ] Review it (fresh model): `/check review group management v1`
- [ ] Document it: `/document group management v1`

Spec [0002](../specs/0002-group-management-v1/index.md) · code in `internal/modules/group/` and `internal/bootstrap/`

## Deferred

The remaining PaySplit capabilities stay in the PRD until a later scope pass enrolls them.

- **Phone verification and phone sign in**: versioned follow up from spec 0001, needs a decision
- **Production PII encryption and transactional email**: production hardening follow up from spec 0001, needs a decision

## Legend

**Feature lifecycle:** `planned` means queued, `in-progress` means design or build has started, `done` means the chosen workflow stages are complete, and `existing` means the code predates this workflow.

**Next step:** the first unticked checkbox is the next command or milestone. Atomic implementation tasks remain in the linked spec.

**Workflow:** GA runs `/develop`, `/check verify`, `/test`, fresh model `/check review`, and `/document`. A real design decision still runs `/architect` first.
