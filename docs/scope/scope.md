# Scope: PaySplit Backend

PaySplit Backend provides the account, group expense, settlement, and notification APIs used by the PaySplit mobile application. This scope currently tracks authentication and account, group management, and bill processing with OCR.

**Build approach:** Tracer Bullet (build a narrow real path through database, usecase, API, and provider adapters before adding breadth).
**Workflow:** GA (`/check verify`, `/test`, fresh model `/check review`, then `/document` after `/develop`). The project default level of rigor. `/architect` remains the recommended first stop whenever a feature has an unresolved decision.

_These are recommendations to keep the build orderly. You decide when a feature is done._

## At a glance

| # | Feature | Phase | Status |
|---|---|---|---|
| 1 | Auth and account v1 | Slice 1 | in-progress |
| 2 | Group management v1 | Slice 2 | in-progress |
| 3 | Bill and OCR v1 | Slice 3 | in-progress |
| 4 | Notification and background queue v1 | Slice 4 | in-progress |
| 5 | Debt and VietQR payment v1 | Slice 5 | planned |

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

## Slice 3: Bill and OCR

### 3. Bill and OCR v1 · in-progress

Provide manual and multi image bill drafts, private receipt storage, durable LlamaExtract OCR, versioned correction, ratio based item allocation, explicit review, exact Hamilton calculation, transactional finalization into immutable shares and debts, and safe void with replacement history.

**Done when:** all fourteen acceptance criteria in spec 0003 pass against PostgreSQL 18, OCR retries never overwrite user edits, preview and finalized amounts reconcile exactly, concurrent mutations preserve one reviewed version, and every storage, queue, financial, authorization, cleanup, SSE, and observability contract is verified.

- [x] Design it (spec): `/architect bill and OCR v1`
- [ ] Build it: `/develop bill and OCR v1`
  - [ ] Manual and private image draft thread with idempotency, list, detail, full replacement, signed reads, and durable cleanup (satisfies AC-1, AC-5, AC-8, AC-12, AC-13, AC-14)
  - [ ] River and LlamaExtract OCR thread with retry, schema normalization, candidate application, stale protection, SSE, and raw response cleanup (satisfies AC-2, AC-3, AC-4, AC-12, AC-14)
  - [ ] Ratio allocation and explicit review thread with reconciliation, Hamilton preview, limits, and concurrency coverage (satisfies AC-5, AC-6, AC-7, AC-8, AC-10, AC-14)
  - [ ] Transactional finalize thread with immutable member shares, debts, activity, notifications, bank eligibility, and idempotent replay (satisfies AC-7, AC-9, AC-10, AC-14)
  - [ ] Safe void and replacement history, payment race protection, OpenAPI, module documentation, metrics, redaction, and end to end verification (satisfies AC-11, AC-12, AC-13, AC-14)
- [ ] Verify it: `/check verify bill and OCR v1`
- [ ] Test it: `/test bill and OCR v1`
- [ ] Review it (fresh model): `/check review bill and OCR v1`
- [ ] Document it: `/document bill and OCR v1`

Spec [0003](../specs/0003-bill-ocr-v1/index.md) · planned code in `internal/modules/bill/`, `internal/platform/ocr/`, `internal/platform/storage/cloudinary/`, and `internal/bootstrap/`

## Slice 4: Notification and background queue

### 4. Notification and background queue v1 · in-progress

Provide Firebase Cloud Messaging push notification dispatch, PostgreSQL backed River Queue job processing, device token session binding, dead token pruning, and in-app notification center.

**Done when:** all seven acceptance criteria in spec 0004 pass against PostgreSQL 18, background jobs run reliably through River with exponential backoff on transient errors, dead FCM tokens are pruned automatically, in-app notifications support unread count and pagination, and graceful shutdown drains queue workers cleanly.

- [x] Design it (spec): `/architect notification and background queue v1`
- [ ] Build it: `/develop notification and background queue v1`
  - [ ] PostgreSQL schema migration for session FCM token and notification records (satisfies AC-1, AC-3)
  - [ ] River Queue platform adapter, worker registry, and graceful lifecycle wiring (satisfies AC-2, AC-7)
  - [ ] FCM push notification client, payload builders, and dead token pruning (satisfies AC-4, AC-5)
  - [ ] In-app notification repository, usecase, and River enqueuer (satisfies AC-3, AC-4, AC-6)
  - [ ] HTTP delivery handlers, routes registration, and unit/integration tests (satisfies AC-1, AC-6)
- [ ] Verify it: `/check verify notification and background queue v1`
- [ ] Test it: `/test notification and background queue v1`
- [ ] Review it (fresh model): `/check review notification and background queue v1`
- [ ] Document it: `/document notification and background queue v1`

Spec [0004](../specs/0004-notification-queue-v1/index.md) · code in `internal/modules/notification/`, `internal/platform/queue/river/`, `internal/platform/notification/fcm/`, and `internal/bootstrap/`

## Slice 5: Settlement and Payment

### 5. Debt and VietQR payment v1 · planned · needs a decision

Provide debt tracking across group members, VietQR generation with embedded NAPAS 247 reference codes, payment proof submission, creditor manual confirmation or rejection, and debt reminder jobs.

**Done when:** debt balances calculate accurately across multiple bills, payment QR encodes valid banking and reference data, payment proofs transition debt statuses safely without auto-settlement, and stalled payment rules notify both parties.

- [ ] Design it (spec): `/architect debt and VietQR payment v1`

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
