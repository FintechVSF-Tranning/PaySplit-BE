# Scope: PaySplit Backend

PaySplit Backend provides the account, group expense, settlement, and notification APIs used by the PaySplit mobile application. This scope currently tracks the authentication and account slice that is ready to build.

**Build approach:** Tracer Bullet (build a narrow real path through database, usecase, API, and provider adapters before adding breadth).
**Workflow:** GA (`/check verify`, `/test`, fresh model `/check review`, then `/document` after `/develop`). The project default level of rigor. `/architect` remains the recommended first stop whenever a feature has an unresolved decision.

_These are recommendations to keep the build orderly. You decide when a feature is done._

## At a glance

| # | Feature | Phase | Status |
|---|---|---|---|
| 1 | Auth and account v1 | Slice 1 | in-progress |

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

## Deferred

The remaining PaySplit capabilities stay in the PRD until a later scope pass enrolls them.

- **Phone verification and phone sign in**: versioned follow up from spec 0001, needs a decision
- **Production PII encryption and transactional email**: production hardening follow up from spec 0001, needs a decision

## Legend

**Feature lifecycle:** `planned` means queued, `in-progress` means design or build has started, `done` means the chosen workflow stages are complete, and `existing` means the code predates this workflow.

**Next step:** the first unticked checkbox is the next command or milestone. Atomic implementation tasks remain in the linked spec.

**Workflow:** GA runs `/develop`, `/check verify`, `/test`, fresh model `/check review`, and `/document`. A real design decision still runs `/architect` first.
