<!-- Hallmark · pre-emit critique: P5 H5 E5 S5 R5 V4 -->
# 0007. Debtor bill consent v2

**Date**: 2026-08-24
**Status**: Proposed
**Target release**: V2
**V1 contract**: [Group bill close v1](../0008-group-bill-close-v1/index.md)

## Summary

V2 will require consent from every member who would owe money before the Captain can finalize a bill. Each person will approve one exact bill version and one exact amount, with an immutable breakdown of what they saw. This feature is excluded from V1 and must not gate V1 review, individual finalize, or bulk finalize.

## Companion documents

1. [End to end flow from bill upload to settlement](end-to-end-flow.md)
2. [Flutter debtor bill consent UI spec](../../../../PaySplit-FE/docs/specs/0005-debtor-bill-consent-ui-v2.md)
3. [V1 group submission lock and bulk finalize](../0008-group-bill-close-v1/index.md)

## Requirements

**User stories**:

1. As a debtor, I want to see every item and charge assigned to me before I accept so that no debt is created without my informed agreement.
2. As a debtor, I want to accept or reject several pending bills from one page so that reviewing group expenses is quick.
3. As a Creditor or Captain, I want every positive debtor share accepted before finalize so that the resulting debts match what members approved.
4. As a group member, I want a rejected or edited split to start a fresh consent round so that an old acceptance never applies to new numbers.

**Acceptance criteria**:

1. **AC-1**: `POST /bills/{billId}/review` locks the active bill version, reruns the existing allocation, and atomically creates one consent round plus one member request for every active non Creditor member whose final amount is greater than zero. The bill becomes `awaiting_acceptance`. If no such member exists, no round is created and the bill becomes `reviewed` immediately.
2. **AC-2**: Every member request stores the exact positive `proposed_amount` and an immutable, schema versioned JSON breakdown. The snapshot includes the bill header, assigned items, original and final item prices, assignment weight, member item share, service charge share, VAT share, general discount share, rounding adjustment, and final amount.
3. **AC-3**: A debtor can list only their own requests across active groups. The pending and responded views use independent cursor pagination ordered by `(requested_at DESC, id DESC)`, support an optional `group_id` filter, and default to 20 rows with a maximum of 50. The list returns summaries only. Detail loads the immutable snapshot for one request.
4. **AC-4**: `POST /bill-acceptance-requests/responses:batch` accepts at most 50 unique request IDs with the request bill version, decision, and a per bill rejection reason when needed. Each item runs independently. All success returns `200`; any valid item failure returns `207 Multi Status` with a result for every input; an invalid batch envelope returns `400` and writes nothing.
5. **AC-5**: Accept is permanent for that bill version. The first committed response wins, a second response returns `ALREADY_RESPONDED`, and a retry with the same `Idempotency-Key` replays the original batch result. When the last pending member accepts, the same transaction marks the round `approved` and the bill `reviewed`.
6. **AC-6**: Reject requires a trimmed reason from 1 to 500 characters. The same transaction marks the member response rejected, marks the round rejected, returns the bill to `draft`, and makes every acceptance from that round historical and ineffective. The reason is visible only to the rejector, Creditor, and Captain.
7. **AC-7**: Editing, applying OCR, or otherwise changing a bill in `awaiting_acceptance` or `reviewed` checks the bill version, increments it, marks the current round `invalidated`, and returns the bill to `draft`. Every member must accept the new reviewed version again. A draft with any consent history cannot be hard deleted and returns `409 CONSENT_HISTORY_EXISTS`.
8. **AC-8**: Finalize remains Captain only and keeps its existing financial transaction. For consent required bills it additionally requires one approved round for the exact locked bill version and requires every recomputed positive non Creditor share to equal that member accepted `proposed_amount`. There is no Captain override. A mismatch or missing acceptance returns `409 CONSENT_REQUIRED` and creates no share or debt.
9. **AC-9**: Creditor or Captain can remind all pending members on one awaiting bill. The system sends only to people who have not accepted, permits at most three reminders per member request, and requires 24 hours between reminders. Accepted members never receive another reminder for that round.
10. **AC-10**: Only the request owner can accept, reject, list, or read their request. Creditor and Captain can read the current round status matrix and rejection reasons. Other active group members keep their existing bill read access but cannot read the response matrix. Archived groups expose no consent request or history through user APIs.
11. **AC-11**: Leaving or removing a member while they participate in an unfinished consent round is allowed. Under the existing group lock, every affected bill returns to `draft`, its round becomes invalidated, and its prior responses become ineffective. This applies whether that member was pending or had accepted.
12. **AC-12**: Concurrent review, edit, response, member exit, and finalize operations use one lock order. The first valid commit wins, stale operations receive stable version or state conflicts, and no transaction leaves a reviewed bill without an approved exact version round.
13. **AC-13**: The Flutter page has `Chờ xác nhận` and `Đã phản hồi` tabs. Pending cards are selectable accordions with the bill name as header. Opening one lazily loads and caches the full breakdown. Responded cards are read only and retain the snapshot and outcome while the group remains active.
14. **AC-14**: The pending page has a safe area aware sticky action bar with selected count, selected total, `Chấp nhận`, and `Từ chối`. Accept opens a final bottom sheet listing the bill count, total, and selected bills. Reject opens one required reason field per selected bill. A partial response moves successful bills to history while failed bills remain selected with an inline error.
15. **AC-15**: Home shows `Chờ bạn xử lý` with a pending count. In app and push notifications deep link to the consent page and open the matching accordion. A rejected round notifies the Creditor, Captain, and every debtor in that round that the version is ineffective, but only authorized viewers receive the rejection reason.
16. **AC-16**: The page supports loading skeletons, independent tab pagination, empty, offline, item error, batch partial success, disabled, and completed states. Every touch target is at least 44 by 44 logical pixels, action labels never wrap, and the layout is verified at widths 320, 375, 414, and 768.
17. **AC-17**: Consent list and detail reads meet the existing 200 ms server target at the project limits. Metrics cover round creation, response outcome, response latency, reminder outcome, and consent finalize blocks with bounded labels. Logs and notification payloads exclude item text, full snapshots, rejection reasons, and bank data.

## Decision

**Chosen option**: Durable consent rounds before financial finalization

PaySplit will treat consent as approval of one exact draft version, not as a debt or a payment. PostgreSQL stores the round and each member response. The existing finalize transaction remains the only place that creates immutable shares and debts.

**Implementation skills**: `hallmark` (`namplh/agent-skills`, `.agents/skills/hallmark/`) · `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Feature design

### Data model

| Entity | Required fields | Nullable fields | Relations and constraints |
|---|---|---|---|
| `bills` | Existing fields plus `consent_required boolean NOT NULL DEFAULT false` | Existing nullable fields | `bill_status` adds `awaiting_acceptance`. Legacy reviewed bills may keep `consent_required = false`; a review under the enabled feature sets it true. |
| `bill_acceptance_rounds` | `id uuid`, `bill_id uuid`, `group_id uuid`, `bill_version int`, `status acceptance_round_status`, `requested_by_member_id uuid`, `requested_at timestamptz`, timestamps | `resolved_at timestamptz`, `invalidation_reason acceptance_invalidation_reason` | Composite group foreign keys. Unique `(bill_id, bill_version)`. One partial unique index permits one `pending` round per bill. |
| `bill_member_acceptances` | `id uuid`, `round_id uuid`, `bill_id uuid`, `group_id uuid`, `bill_version int`, `member_id uuid`, `proposed_amount bigint`, `breakdown_snapshot jsonb`, `snapshot_schema_version smallint`, `status member_acceptance_status`, `requested_at timestamptz`, `reminder_count smallint`, timestamps | `rejection_reason text`, `responded_at timestamptz`, `last_reminded_at timestamptz` | Unique `(round_id, member_id)` and `(bill_id, bill_version, member_id)`. `proposed_amount > 0`. Reminder count is from 0 through 3. Status, response time, and reason are constrained as one valid matrix. Snapshot must be a JSON object. |

Round status values are `pending`, `approved`, `rejected`, and `invalidated`. Member response values are `pending`, `accepted`, and `rejected`. Invalidation reason values include `draft_edited`, `ocr_applied`, `member_left`, and `member_removed`.

Indexes match the reads in this spec. Member lists use `(member_id, status, requested_at DESC, id DESC)`. Round lookup uses `(bill_id, bill_version)`. Pending round uniqueness uses a partial index on `bill_id WHERE status = 'pending'`. Every foreign key column or matching composite prefix is indexed.

`breakdown_snapshot` schema version 1 has these objects:

1. `bill`: bill ID, group ID, version, merchant name, bill date, Creditor member ID, Creditor display name, currency, requested time.
2. `items`: item ID, name, quantity, unit price, line total, item discount amount, final price, assignment weight, and member item share.
3. `allocation`: item subtotal, service charge share, VAT share, general discount share, rounding adjustment, final amount.

The server builds this document from the same allocation result used by preview and finalize. The client never supplies money or snapshot values.

### State transitions

```text
bill with positive non Creditor shares
draft -> awaiting_acceptance -> reviewed -> finalized
                         |             |
                         v             v
                       draft <---------+

bill without positive non Creditor shares
draft -> reviewed -> finalized

consent round
pending -> approved
pending -> rejected
pending -> invalidated
approved -> invalidated when the bill is edited before finalize

member response
pending -> accepted
pending -> rejected
```

Reject, edit, OCR apply, and member exit preserve old rows. They change round effectiveness, never rewrite a historical response.

### API surface

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| `/api/v1/bills/{billId}/review?group_id={groupId}` | `POST` | version, `Idempotency-Key` | bill status, round summary, request count | Creditor or Captain | `409 VERSION_CONFLICT`, `422 BILL_NOT_READY` |
| `/api/v1/bill-acceptance-requests` | `GET` | `state=pending or responded`, optional group ID, cursor, limit | request summaries, next cursor | request owner | `400 INVALID_CURSOR`, `404 GROUP_NOT_FOUND` |
| `/api/v1/bill-acceptance-requests/{requestId}` | `GET` | request ID | immutable bill and allocation snapshot, response and round outcome | request owner | `404 ACCEPTANCE_REQUEST_NOT_FOUND` |
| `/api/v1/bill-acceptance-requests/pending-count` | `GET` | none | pending count across active groups | authenticated user | none |
| `/api/v1/bill-acceptance-requests/responses:batch` | `POST` | `Idempotency-Key`, 1 through 50 response commands | ordered result for every command | each request owner | `400 INVALID_BATCH`, `207 Multi Status`, item errors below |
| `/api/v1/bills/{billId}/acceptance-round?group_id={groupId}` | `GET` | bill ID | current round, per member status, reminder eligibility | Creditor or Captain | `404 ROUND_NOT_FOUND` |
| `/api/v1/bills/{billId}/acceptance-reminders?group_id={groupId}` | `POST` | `Idempotency-Key` | sent and skipped member results | Creditor or Captain | `409 BILL_NOT_AWAITING_ACCEPTANCE` |
| `/api/v1/bills/{billId}/finalize?group_id={groupId}` | `POST` | version, `Idempotency-Key` | existing finalized bill response | Captain | existing errors plus `409 CONSENT_REQUIRED` |

One batch command is:

```json
{
  "request_id": "uuid",
  "bill_version": 4,
  "decision": "accepted",
  "rejection_reason": null
}
```

The decision is `accepted` or `rejected`. Rejection reason is required only for reject. Duplicate request IDs make the batch envelope invalid. Results keep input order and contain request ID, bill ID, outcome, HTTP status, new request status, new round status, and a stable error object when failed. Any item failure produces `207`. Retrying failed items requires a new key and a body containing only those items.

Stable consent errors are `ACCEPTANCE_REQUEST_NOT_FOUND`, `ACCEPTANCE_VERSION_STALE`, `ALREADY_RESPONDED`, `BILL_NOT_AWAITING_ACCEPTANCE`, `REJECTION_REASON_REQUIRED`, `REMINDER_COOLDOWN`, `REMINDER_LIMIT_REACHED`, `CONSENT_REQUIRED`, and `CONSENT_HISTORY_EXISTS`. Reminder limit and cooldown are per member results, not whole request failures.

### Value sourcing

| Action | Value produced or displayed | Source |
|---|---|---|
| Review | Required debtors | Existing allocation result filtered to active members, non Creditor, and final amount greater than zero |
| Review | Proposed amount | `MemberAllocation.FinalAmount` from the current locked bill version |
| Review | Item and charge breakdown | Existing bill items, assignments, item discount fields, and allocation component values for the same member |
| Review | Snapshot header | Locked bill, group currency, current Creditor display name, and PostgreSQL request time |
| List | Request ownership | Authenticated user joined through active `group_members.user_id` to `bill_member_acceptances.member_id` |
| List | Pending or responded tab | Member response status. Pending means `pending`; responded means `accepted` or `rejected` |
| List | History effectiveness | Linked round status and invalidation reason |
| Detail | All displayed money | Immutable `breakdown_snapshot` and `proposed_amount`, never the mutable live draft |
| Pending count | Badge number | Count of caller owned pending responses whose round and group are active |
| Batch response | Expected version | Command `bill_version`, checked against request and locked bill version |
| Batch accept | Bill transition | Count of pending responses under the locked round after the current acceptance update |
| Batch reject | Reason and visibility | Trimmed command reason plus role and ownership checks at read time |
| Reminder | Eligibility | Response is pending, `reminder_count < 3`, and last reminder is null or at least 24 hours old |
| Finalize | Consent gate | Approved round for exact bill version plus equality between recomputed shares and accepted proposed amounts |
| Home deep link | Target request | Notification payload contains group ID, bill ID, and request ID only |

### Key invariants

1. No consent required bill reaches `reviewed` without an approved round for its exact version.
2. No consent required debt is created unless its amount equals that debtor accepted amount.
3. Creditor never receives a consent request. A zero amount member never receives a consent request.
4. One bill has at most one pending round. One member has at most one response per round and version.
5. Accept and reject are terminal member responses. Only a new bill version creates a new choice.
6. A rejected or invalidated round can never become approved.
7. External notification delivery never runs while database locks are held. Notification rows and River jobs are inserted transactionally.
8. All mutation transactions acquire locks in this order: active group when the operation spans membership, bill, round, then member response rows in UUID byte order. Finalize continues to lock debts after consent checks using its existing order.
9. Batch item transactions are short and independent. No batch transaction spans all selected bills.
10. JSON snapshot schema is server generated, immutable, and read by its stored schema version.

### Events and observability

Group activity types are `bill_consent_requested`, `bill_consent_accepted`, `bill_consent_rejected`, `bill_consent_approved`, and `bill_consent_invalidated`. Notification types are `bill_consent_requested`, `bill_consent_reminder`, `bill_consent_rejected`, `bill_consent_invalidated`, and `bill_consent_approved`. Payloads contain only group ID, bill ID, request ID where applicable, round status, and deep link route.

Metrics are:

1. `paysplit_bill_consent_rounds_total{outcome}` with bounded outcome values `created`, `approved`, `rejected`, `invalidated`, and `failed`.
2. `paysplit_bill_consent_responses_total{decision,outcome}` with decision `accepted` or `rejected` and outcome `success`, `conflict`, or `failed`.
3. `paysplit_bill_consent_response_duration_seconds{decision,outcome}` with the same bounded labels.
4. `paysplit_bill_consent_reminders_total{outcome}` with outcome `sent`, `cooldown`, `limit`, or `failed`.
5. `paysplit_bill_consent_finalize_blocks_total{reason}` with reason `missing_round`, `round_not_approved`, `version_mismatch`, or `amount_mismatch`.

### Security model

1. Every route requires the existing live bearer session.
2. Request owner access is resolved from the authenticated user. The API never accepts a caller supplied member ID as authority.
3. Cross user, cross group, inactive group, and archived group request reads return not found so identifiers cannot be probed.
4. Creditor and Captain may read the response matrix. Only they and the rejector may read a rejection reason.
5. Other active members retain current bill read permission but receive no response matrix or reason.
6. Acceptance means agreement with the split and proposed future debt. It is not proof of payment and not a bank instruction. UI copy must state this before final accept.
7. Activities retain actor, round, bill, version, outcome, and time. Logs and push payloads never include snapshots, item text, or rejection reasons.

### Configuration

`BILL_CONSENT_REQUIRED` is a boolean V2 rollout flag. It remains false for the whole V1 release. It becomes true only when the V2 backend and Flutter contracts are ready, then is removed after rollout. It changes new review behavior only. It never bypasses consent for a bill whose stored `consent_required` is already true.

### Flutter page design

Hallmark genre is utilitarian warm editorial. The page follows [`PaySplit-UI/ui-context.md`](../../../../PaySplit-UI/ui-context.md): Newsreader for editorial titles, Roboto Slab for prose and controls, JetBrains Mono for money, Warm Olive paper, Deep Teal actions, Forui components, and Hugeicons. No new visual theme is introduced. The detailed Flutter contract lives in [FE spec 0005](../../../../PaySplit-FE/docs/specs/0005-debtor-bill-consent-ui-v2.md).

The route is `/bill-acceptance-requests`, with optional `request_id` for a deep link. Home `Chờ bạn xử lý` shows the pending count and opens this route.

The page composition is:

1. A compact app bar with title and pending badge.
2. Two tabs, `Chờ xác nhận` and `Đã phản hồi`, each preserving its own cursor and scroll state.
3. An accordion list. The header contains selection control, bill name, Creditor, requested time, proposed amount, and response state. Only pending rows are selectable.
4. Lazy detail content with assigned item rows and a separated allocation summary for service charge, VAT, discounts, rounding, and total.
5. A safe area aware sticky action bar with selected count, selected total, `Chấp nhận`, and `Từ chối`.
6. An accept bottom sheet with selected bills, count, total, consent wording, and one final action.
7. A reject bottom sheet with a visible 1 through 500 character field for every selected bill.

All interactive controls implement default, hover where supported, focus, pressed, disabled, loading, error, and success states. Accordion and selection controls have at least 44 by 44 logical pixel targets. A loading skeleton preserves card shape. Empty states explain why the list is empty. Failed batch rows keep selection and show an inline stable error while successful rows move to history.

### Critical test scenarios

1. Happy path: review a split with two positive debtors, accept on two accounts, observe the last accept move the bill to reviewed, then finalize exact debts, verifies **AC-1**, **AC-2**, **AC-5**, **AC-8**.
2. Zero debtor path: review a Creditor only bill and move directly to reviewed without a round, verifies **AC-1**.
3. Reject path: reject with a valid reason, preserve all responses, return the bill to draft, enforce reason visibility, and notify every participant without leaking the reason, verifies **AC-6**, **AC-10**, **AC-15**.
4. Edit path: accept, edit the awaiting or reviewed bill, invalidate the old round, increment version, and require a new round, verifies **AC-7**.
5. Batch partial path: send valid, stale, and already answered items, return ordered `207` results, commit only the valid item, and replay the same result by idempotency key, verifies **AC-4**, **AC-5**.
6. Concurrency path: race final accept with edit and finalize. Exactly one state transition wins and no debt appears without approved exact consent, verifies **AC-8**, **AC-12**.
7. Membership path: leave after accepting but before the round completes, invalidate the round and return the bill to draft without blocking exit, verifies **AC-11**.
8. Reminder path: notify pending members only, enforce 24 hours and three reminders independently, verifies **AC-9**.
9. Authorization path: reject cross user, inactive group, archived group, and ordinary member matrix reads without leaking request existence, verifies **AC-10**.
10. UI path: deep link to one accordion, lazy load detail, select several bills, confirm accept, handle `207`, and preserve independent tab pagination at all target widths, verifies **AC-13** through **AC-16**.
11. Performance and redaction path: exercise project limits, inspect query plans and emitted logs, and confirm bounded metrics and no sensitive payloads, verifies **AC-17**.

## Build plan

The repository records no delivery approach for this new feature. Use a Tracer Bullet approach, meaning each slice works through database, API, and Flutter before the next slice expands it.

1. Add the feature flag, enum values, consent round and member response schema, constraints, foreign keys, and indexes. Regenerate sqlc and add migration tests, satisfies **AC-1**, **AC-2**, **AC-12**, **AC-17**.
2. Extend allocation review to atomically create one round, snapshots, requests, activities, notifications, and the no debtor direct review path, satisfies **AC-1**, **AC-2**.
3. Add owner list, pending count, and lazy detail reads with cursor pagination and authorization, then build the Flutter tabs, accordion summary, and detail loading slice, satisfies **AC-3**, **AC-10**, **AC-13**, **AC-15**, **AC-16**.
4. Add idempotent batch accept and reject with per item transactions, exact state transitions, reason visibility, and partial results. Wire Flutter selection, accept confirmation, reject reasons, and inline retry states, satisfies **AC-4** through **AC-6**, **AC-14**.
5. Gate finalize on exact approved consent and invalidate rounds on edits, OCR apply, and protected draft deletion, satisfies **AC-7**, **AC-8**, **AC-12**.
6. Extend member exit and removal under the group lock to invalidate affected rounds and notify participants, satisfies **AC-11**, **AC-12**, **AC-15**.
7. Add current round status and reminder APIs with transactional River enqueue, then add Creditor and Captain status and reminder controls, satisfies **AC-9**, **AC-10**.
8. Add Home badge, notification deep links, responded history, archived group filtering, full UI states, accessibility checks, metrics, redaction, and load tests, satisfies **AC-3**, **AC-10**, **AC-13** through **AC-17**.
9. Update OpenAPI before code rollout, then add unit, repository integration, handler contract, Flutter domain, provider, widget, and navigation tests for every critical scenario, satisfies **AC-1** through **AC-17**.

## Consequences

**Positive**:

1. A debt cannot appear from a split the debtor never approved.
2. Every accepted amount has a durable, readable snapshot.
3. Batch actions reduce mobile work without weakening per bill consistency.

**Negative and tradeoffs**:

1. Finalization can remain blocked indefinitely when one member does not respond.
2. Every draft change after review invalidates all prior acceptance work.
3. Immutable JSON snapshots increase storage and require schema version readers.
4. Batch partial success is more complex for API and Flutter state than atomic batch behavior.

**Neutral**:

1. Existing settlement behavior starts only after finalize and remains unchanged.
2. Existing active group members keep current bill read access.
3. Archived consent data remains stored but unavailable to users.

## Follow up

1. Enroll this feature in `docs/scope/scope.md` before implementation so lifecycle status and work tracking can mirror this spec.
2. Capture Hallmark conventions in the PaySplit FE context before implementation because the new page uses those interaction and responsive rules.
3. Capture PostgreSQL schema and lock conventions in the PaySplit BE context before implementation because consent touches financial rows and member exit.
4. Remove the rollout flag only after all new bill reviews require consent and legacy reviewed bills are no longer active.

## Migration plan

**Strategy**: Feature flagged strangler rollout

**Phases**:

1. Add enum values, nullable compatible tables, indexes, and `bills.consent_required` with default false while old behavior remains active.
2. Deploy reads and writes behind `BILL_CONSENT_REQUIRED=false`. Verify query plans, constraints, idempotency, and notification jobs.
3. Enable consent for new reviews. A reviewed legacy bill with `consent_required=false` may finalize through the old gate. Any later edit or new review sets `consent_required=true` and uses the new flow.
4. Enable Flutter entry points after backend deployment and contract verification.
5. Remove the runtime flag after rollout. Retain `consent_required` so legacy finalized history remains explainable.

**Rollback**: Disable the flag to stop creating new rounds. Bills already awaiting consent remain readable and respondable, but deployment rollback must not drop the new enum value, tables, or history. A follow up deployment may invalidate unfinished rounds and return their bills to draft if product rollback requires it.

**Risks**: An unsafe rollback can strand bills in `awaiting_acceptance`. A missing composite or partial index can slow the Home badge and pending list. Inconsistent lock order can deadlock review, response, edit, and member exit.

## Rationale

Reasoning and options: see [rationale.md](rationale.md).
