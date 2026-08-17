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

Provide group creation, Captain controlled invites, idempotent invite redemption, group preview, membership management, Captain transfer, activity logging, and safe member exit with no open debtor or creditor obligations.

**Done when:** all eight acceptance criteria in spec 0002 pass against PostgreSQL 18, active memberships govern access, concurrent invite and Captain operations preserve their limits and invariants, member exit rejects every open debtor or creditor obligation, and key group mutations write activities atomically.

- [x] Design it (spec): `/architect group management v1`
- [ ] Build it: `/develop group management v1`
  - [ ] Create, list, and detail vertical slice with exact validation, DTOs, privacy preserving authorization, cursor reads, and PostgreSQL coverage (satisfies AC-1, AC-2)
  - [ ] Captain invite vertical slice with one available invite, reuse, regeneration, revocation, redaction, and atomic activities (satisfies AC-3, AC-8)
  - [ ] Preview and redemption vertical slice with idempotent join, capacity limits, membership reactivation, and concurrency coverage (satisfies AC-4, AC-5, AC-8)
  - [ ] Member exit and Captain transfer vertical slice with open obligation checks, ordered locks, atomic role transfer, and stable conflicts (satisfies AC-6, AC-7, AC-8)
  - [ ] Activity timeline, shared error mapping, OpenAPI, module documentation, and end to end verification (satisfies AC-1 through AC-8)
- [ ] Verify it: `/check verify group management v1`
- [ ] Test it: `/test group management v1`
- [ ] Review it (fresh model): `/check review group management v1`
- [ ] Document it: `/document group management v1`

Spec [0002](../specs/0002-group-management-v1/index.md) · code in `internal/modules/group/` and `internal/bootstrap/`

## Deferred

The remaining PaySplit capabilities stay in the PRD until a later scope pass enrolls them.

### Group lifecycle closure · deferred · from spec 0002

Define group archive or deletion behavior so the sole Captain can close or leave a group without breaking history.

### Active group quota policy · deferred · from spec 0002

Choose a concrete per user active group limit before adding quota enforcement.

- **Phone verification and phone sign in**: versioned follow up from spec 0001, needs a decision
- **Production PII encryption and transactional email**: production hardening follow up from spec 0001, needs a decision

## Legend

**Feature lifecycle:** `planned` means queued, `in-progress` means design or build has started, `done` means the chosen workflow stages are complete, and `existing` means the code predates this workflow.

**Next step:** the first unticked checkbox is the next command or milestone. Atomic implementation tasks remain in the linked spec.

**Workflow:** GA runs `/develop`, `/check verify`, `/test`, fresh model `/check review`, and `/document`. A real design decision still runs `/architect` first.
