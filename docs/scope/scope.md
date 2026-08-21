# Scope: PaySplit Backend

PaySplit Backend provides the account, group expense, settlement, and notification APIs used by the PaySplit mobile application. This scope currently tracks authentication and account, group management, and bill processing with OCR.

**Build approach:** Tracer Bullet (build a narrow real path through database, usecase, API, and provider adapters before adding breadth).
**Workflow:** GA (`/check verify`, `/test`, fresh model `/check review`, then `/document` after `/develop`). The project default level of rigor. `/architect` remains the recommended first stop whenever a feature has an unresolved decision.

_These are recommendations to keep the build orderly. You decide when a feature is done._

## At a glance

| #   | Feature                              | Phase   | Status      |
| --- | ------------------------------------ | ------- | ----------- |
| 1   | Auth and account v1                  | Slice 1 | done        |
| 2   | Group management v1                  | Slice 2 | in-progress |
| 3   | Bill and OCR v1                      | Slice 3 | done        |
| 4   | Split and settlement v1              | Slice 4 | in-progress |
| 5   | Admin v1                             | Slice 5 | in-progress |
| 6   | Notification and background queue v1 | Slice 6 | done        |

## Slice 1: Identity and account

### 1. Auth and account v1 · done

Provide registration, email verification, email password sign in, one active device, rotating refresh tokens, password recovery, profile updates, bank validation, and WebP avatars.

**Done when:** all nineteen acceptance criteria in spec 0001 pass against PostgreSQL 18, protected requests honor live sessions, Gmail and Cloudinary failures follow the public contract, and the API plus module documentation match the implemented behavior.

- [x] Design it (spec): `/architect auth and account v1`
- [x] Build it: `/develop auth and account v1`
  - [x] PostgreSQL 18 schema, generated queries, and live migration verification (AC-1, AC-5, AC-6, AC-8, AC-18, AC-19)
  - [x] Identity, session, refresh rotation, middleware, and shared errors (AC-1, AC-5 through AC-9, AC-16, AC-17)
  - [x] Email verification, password recovery, password change, and persisted rate limits (AC-2 through AC-4, AC-10, AC-11, AC-16, AC-17)
  - [x] Profile, bank snapshot, avatar conversion, Cloudinary storage, and durable cleanup (AC-12 through AC-15, AC-17)
  - [x] Cleanup workers, integration coverage, OpenAPI, environment guide, and module documentation (AC-15 through AC-19)
- [x] Verify it: `/check verify auth and account v1`
- [x] Test it: `/test auth and account v1`
- [x] Review it (fresh model): `/check review auth and account v1`
- [x] Document it: `/document auth and account v1`

Spec [0001](../specs/0001-auth-account-v1/index.md) · code in `internal/modules/auth/`, `internal/platform/`, `internal/transport/http/`, and `internal/bootstrap/`

## Slice 2: Group Management

### 2. Group management v1 · in-progress

Provide group creation, Captain controlled invites, idempotent invite redemption, group preview, membership management, Captain transfer, activity logging, safe member exit with no open debtor or creditor obligations, and safe group disbandment.

**Done when:** all nine acceptance criteria in spec 0002 pass against PostgreSQL 18, active memberships govern access, concurrent invite and Captain operations preserve their limits and invariants, member exit and group disbandment reject every open debtor or creditor obligation (correctly excluding voided debts), and key group mutations write activities atomically.

- [x] Design it (spec): `/architect group management v1`
- [ ] Build it: `/develop group management v1`
  - [x] Create, list, and detail vertical slice with exact validation, DTOs, privacy preserving authorization, cursor reads, and PostgreSQL coverage (satisfies AC-1, AC-2)
  - [x] Captain invite vertical slice with one available invite, reuse, regeneration, revocation, redaction, and atomic activities (satisfies AC-3, AC-8)
  - [x] Preview and redemption vertical slice with idempotent join, capacity limits, membership reactivation, and concurrency coverage (satisfies AC-4, AC-5, AC-8)
  - [x] Member exit and Captain transfer vertical slice with open obligation checks, ordered locks, atomic role transfer, and stable conflicts (satisfies AC-6, AC-7, AC-8)
  - [x] Activity timeline, shared error mapping, OpenAPI, module documentation, and end to end verification (satisfies AC-1 through AC-8)
  - [x] Fix the voided debt exclusion bug in member exit's open obligation queries, plus their backing indexes (satisfies corrected AC-6)
  - [ ] Disband group vertical slice: `groups.status` migration, disband transaction, archived group write gate, and integration coverage (satisfies AC-9)
- [ ] Verify it: `/check verify group management v1`
- [ ] Test it: `/test group management v1`
- [ ] Review it (fresh model): `/check review group management v1`
- [ ] Document it: `/document group management v1`

Spec [0002](../specs/0002-group-management-v1/index.md) · code in `internal/modules/group/` and `internal/bootstrap/`

## Slice 3: Bill and OCR

### 3. Bill and OCR v1 · done

Provide manual and multi image bill drafts, private receipt storage, durable LlamaExtract OCR, versioned correction, ratio based item allocation, explicit review, exact floor allocation with Creditor remainder absorption, transactional finalization into immutable shares and debts, and safe void with replacement history.

**Done when:** all twenty one acceptance criteria in spec 0003 (including its item discount children 0004 and 0005) pass against PostgreSQL 18, OCR retries never overwrite user edits, preview and finalized amounts reconcile exactly, concurrent mutations preserve one reviewed version, item level discounts survive a manual edit, and every storage, queue, financial, authorization, cleanup, SSE, and observability contract is verified.

- [x] Design it (spec): `/architect bill and OCR v1`
- [x] Build it: `/develop bill and OCR v1`
  - [x] Manual and private image draft thread with idempotency, list, detail, full replacement, signed reads, and durable cleanup (satisfies AC-1, AC-5, AC-8, AC-12, AC-13, AC-14)
  - [x] River and LlamaExtract OCR thread with retry, schema normalization, candidate application, stale protection, SSE, and raw response cleanup (satisfies AC-2, AC-3, AC-4, AC-12, AC-14)
  - [x] Ratio allocation and explicit review thread with reconciliation, floor allocation preview, limits, and concurrency coverage (satisfies AC-5, AC-6, AC-7, AC-8, AC-10, AC-14)
  - [x] Transactional finalize thread with immutable member shares, debts, activity, notifications, bank eligibility, and idempotent replay (satisfies AC-7, AC-9, AC-10, AC-14)
  - [x] Safe void and replacement history, payment race protection, OpenAPI, module documentation, metrics, redaction, and end to end verification (satisfies AC-11, AC-12, AC-13, AC-14)
  - [x] Item discount OCR parsing and mapping: sequential promotion folding, net item pricing, item versus general discount separation (satisfies AC-15, AC-16, AC-17, AC-18)
  - [x] Manual edit preserves item level discount: `discount_amount` round trips through `POST /bills` and `PUT /bills/{id}`, plus the pre existing `CreateBill` discount composition bug fix (satisfies AC-19, AC-20, AC-21)
- [x] Verify it: `/check verify bill and OCR v1`
- [x] Test it: `/test bill and OCR v1`
- [ ] Review it (fresh model): `/check review bill and OCR v1`
- [ ] Document it: `/document bill and OCR v1`

Spec [0003](../specs/0003-bill-ocr-v1/index.md) · planned code in `internal/modules/bill/`, `internal/platform/ocr/`, `internal/platform/storage/cloudinary/`, and `internal/bootstrap/`

## Slice 4: Split and settlement

### 4. Split and settlement v1 · in-progress

Provide personal allocated expense breakdown, group debt matrix and cursor listing, multi bill debt aggregation for VietQR payment generation, transfer proof image upload, creditor manual confirmation and rejection, manual debt reminders, and automated River workers for debt reminders and stalled confirmation alerts.

**Done when:** all twelve acceptance criteria in spec 0004 pass against PostgreSQL 18, payments strictly coordinate peer to peer transfers without fund custody, dynamic bank lookups and immutable proof snapshots operate reliably, strict lock ordering eliminates race conditions with bill voiding, and River background jobs process reminder and stalled alerts.

- [x] Design it (spec): `/architect split and settlement v1`
- [x] Build it: `/develop split and settlement v1`
  - [x] Personal expense breakdown and group debt matrix query slice (satisfies AC-1, AC-2)
  - [x] VietQR payment generation and dynamic bank profile lookup slice (satisfies AC-3, AC-4, AC-5, AC-11)
  - [x] Transfer proof submission and Cloudinary private asset slice (satisfies AC-6, AC-11, AC-12)
  - [x] Creditor confirmation and rejection all or nothing settlement slice (satisfies AC-7, AC-8, AC-11)
  - [x] Manual debt reminder and River scheduled background jobs slice (satisfies AC-9, AC-10)
  - [x] Operational hardening, metrics, structured redaction, and end to end verification (satisfies AC-1 through AC-12)
- [x] Verify it: `/check verify split and settlement v1`
- [x] Test it: `/test split and settlement v1`
- [ ] Review it (fresh model): `/check review split and settlement v1`
- [ ] Document it: `/document split and settlement v1`

Spec [0004](../specs/0004-split-settlement-v1/index.md) · code in `internal/modules/settlement/`, `internal/platform/vietqr/`, `internal/platform/storage/cloudinary/`, and `internal/bootstrap/`

## Slice 5: Admin and monitoring

### 5. Admin v1 · in-progress

Provide account search, filtering, and pagination, account detail inspection with masked bank credentials, status mutations (suspend, lock, reactivate) with atomic session revocation and audit logging, system health probes, and Prometheus metrics monitoring.

**Done when:** all eight acceptance criteria in spec 0005 pass against PostgreSQL 18, administrative routes strictly require live admin sessions, account status transitions immediately revoke all active sessions and refresh tokens, audit logs record every mutation reason, and health/metrics endpoints operate reliably.

- [x] Design it (spec): `/architect admin v1`
- [x] Build it: `/develop admin v1`
  - [x] PostgreSQL 18 schema queries, admin repository adapter, and bank detail masking (satisfies AC-1, AC-2, AC-3, AC-4)
  - [x] Admin usecase service, self protection guard, admin role guard, and atomic session revocation (satisfies AC-1, AC-2, AC-3, AC-4)
  - [x] Admin HTTP delivery handlers, DTO validation, and route registration with liveAuth (satisfies AC-1, AC-2, AC-3, AC-4, AC-5, AC-7)
  - [x] Health liveness/readiness probes and Prometheus metrics exporter integration (satisfies AC-6, AC-7, AC-8)
  - [x] Operational verification, integration test coverage, and documentation (satisfies AC-1 through AC-8)
- [x] Verify it: `/check verify admin v1`
- [x] Test it: `/test admin v1`
- [x] Review it (fresh model): `/check review admin v1`
- [x] Document it: `/document admin v1`

Spec [0005](../specs/0005-admin-v1/index.md) · code in `internal/modules/admin/`, `internal/platform/metrics/`, `internal/transport/http/router/`, and `internal/bootstrap/`

## Slice 6: Notification and background queue

### 6. Notification and background queue v1 · in-progress

Provide Firebase Cloud Messaging push notification dispatch, PostgreSQL backed River Queue job processing, device token session binding, dead token pruning, and in-app notification center.

**Done when:** all seven acceptance criteria in spec 0006 pass against PostgreSQL 18, background jobs run reliably through River with exponential backoff on transient errors, dead FCM tokens are pruned automatically, in-app notifications support unread count and pagination, and graceful shutdown drains queue workers cleanly.

- [x] Design it (spec): `/architect notification and background queue v1`
- [x] Build it: `/develop notification and background queue v1`
  - [x] PostgreSQL schema migration for session FCM token and notification records (satisfies AC-1, AC-3)
  - [x] River Queue platform adapter, worker registry, and graceful lifecycle wiring (satisfies AC-2, AC-7)
  - [x] FCM push notification client, payload builders, and dead token pruning (satisfies AC-4, AC-5)
  - [x] In-app notification repository, usecase, and River enqueuer (satisfies AC-3, AC-4, AC-6)
  - [x] HTTP delivery handlers, routes registration, and unit/integration tests (satisfies AC-1, AC-6)
- [x] Verify it: `/check verify notification and background queue v1`
- [x] Test it: `/test notification and background queue v1`
- [x] Review it (fresh model): `/check review notification and background queue v1`
- [x] Document it: `/document notification and background queue v1`

Spec [0006](../specs/0006-notification-queue-v1/index.md) · code in `internal/modules/notification/`, `internal/platform/queue/river/`, `internal/platform/notification/fcm/`, and `internal/bootstrap/`

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
