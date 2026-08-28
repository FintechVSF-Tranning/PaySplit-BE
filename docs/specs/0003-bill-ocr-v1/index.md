# 0003. Bill and OCR v1

**Date**: 2026-08-27
**Status**: In Progress

## Summary

Bill and OCR v1 lets a group member create a manual bill or upload up to five receipt images, extract structured data through LlamaExtract, correct and allocate the draft, then let the Captain finalize it into immutable shares and debts. Allocation now adds exact fractional item shares before rounding once per member, then distributes indivisible VND by largest remainder. This removes cumulative early rounding and no longer favors the Creditor for ordinary rounding.

## Structure

1. [Bill draft and OCR](0001-bill-draft-ocr.md) defines manual and image drafts, Cloudinary storage, LlamaExtract jobs, candidate application, retry, SSE, and cleanup.
2. [Allocation and review](0002-allocation-review.md) defines draft editing, exact fractional aggregation, largest remainder reconciliation, item ratios, optimistic locking, and explicit review.
3. [Finalize and void](0003-finalize-void.md) defines immutable allocation snapshots, debt creation, notification jobs, and safe void with replacement history.
4. [Item discount OCR parsing](0004-item-discount-ocr-parsing.md) defines sequential folding of item promotions, net item pricing, and separation of item versus general discounts.
5. [Manual edit preserves item level discount](0005-manual-edit-item-discount.md) defines how `POST /bills` and `PUT /bills/{id}` accept and validate `discount_amount` per item so a manual edit does not erase what OCR extracted.

The cross child contract is that every bill scoped row carries `group_id`, every money value is `bigint` VND, every mutable draft operation checks and increments `bills.version`, and every mutation that changes bill meaning clears `reviewed_at` and `reviewed_by_member_id`.

## Requirements

**User stories**:

1. As an active group member, I want to enter a bill manually or upload several ordered receipt images so that I can capture short and long receipts.
2. As a Creditor, I want OCR to prepare an editable draft without overwriting my corrections so that extraction errors never become financial records silently.
3. As a Creditor or Captain, I want to assign each item by visible percentages and review the complete bill so that every participant can understand the preview.
4. As a Captain, I want to finalize one reviewed version atomically so that immutable shares and debts are exact and cannot be duplicated.
5. As a Captain, I want to void an unpaid finalized bill and link a replacement so that corrections preserve history.

**Acceptance criteria**:

1. **AC-1**: An authenticated active group member can idempotently create a manual draft with no image or an image draft with one to five JPEG, PNG, or HEIC files, each at most 10 MB. The creator is the Creditor. Manual create returns `201`; image create returns `202` with the OCR job. Stored receipt assets are private, ordered, normalized for orientation, immutable after creation, and represented by indexed `bill_images` rows.
2. **AC-2**: Creating or retrying OCR idempotently inserts one River job for all bill images, returns `202`, and exposes queued, processing, succeeded, or failed application state through bill detail and SSE. A bill has at most one queued or processing OCR job.
3. **AC-3**: LlamaExtract returns a receipt candidate containing merchant, date, items, service charge, VAT, discount, and total. The job stores raw and normalized responses, validates nonnegative `bigint` VND amounts, retries provider failures using configured timeout and retry values, and leaves the draft available for manual entry when all retries fail.
4. **AC-4**: A successful OCR candidate never edits the draft automatically. Applying it requires the current bill version, replaces bill fields and items, clears assignments and review, and returns `409 OCR_RESULT_STALE` if the bill changed after the job began. Running OCR again preserves every earlier candidate and never overwrites manual edits.
5. **AC-5**: A Creditor for their own bill or the Captain can replace the complete draft through one versioned request. The bill supports at most 100 items and keeps `line_total` independent from `quantity × unit_price`. Reported `subtotal` and `total` may be saved while mismatched, but review and finalize require `subtotal = sum(line_total)` and `total = subtotal + service_charge + vat - discount`.
6. **AC-6**: Each item has one or more active group member assignments whose `numeric(9,8)` ratios are greater than `0`, no greater than `1`, and sum exactly to `1.00000000`. Equal split remains a convenience that writes equal ratios to selected items, it does not mean equal division of the whole bill. Preview uses exact rational arithmetic from integer VND and integer weights. It adds every exact item share for a member before taking an integer part, calculates the existing service charge, VAT, and general discount rules from those exact subtotals, then distributes the remaining VND by descending fractional remainder. Equal fractions use canonical UUID byte order ascending. The Creditor is always part of the allocation set but receives no rounding priority.
7. **AC-7**: The Creditor or Captain can explicitly review the current version. Any later change to bill fields, items, assignments, or applied OCR clears the review. Images are immutable after draft creation. Finalize requires a review of the exact locked version.
8. **AC-8**: Every active group member can list and read group bills, OCR state, candidates, previews, and five minute signed image URLs. Only the Creditor or Captain can mutate a draft or run OCR, only the Captain can finalize or void, and inactive members cannot access bill APIs.
9. **AC-9**: Captain finalize is synchronous and idempotent. In one short PostgreSQL transaction it locks the bill, validates the current version, state, review, assignments, totals, active assignees, and the Creditor bank profile, then writes immutable member share snapshots, positive debts for non Creditor members, one activity, and durable notification jobs. A retry returns the original result and concurrent finalize or edit receives a stable conflict.
10. **AC-10**: Final member shares sum exactly to the computed bill total after largest remainder distribution, every remainder reaches zero, and no member final amount is negative. A member discount share is capped at what that member owes, and the cut portion moves to the Creditor as a discount reconciliation rule, not as a rounding rule. A discount larger than the whole bill and a valid total discount that becomes unallocatable after caps are rejected with their existing distinct errors. `FinalAmount` equals `ItemSubtotal + ServiceChargeShare + VATShare - DiscountShare + RoundingAdjustment`. A member with zero final amount keeps a share snapshot but gets no debt. If subtotal is zero, all service charge, VAT, and discount belong to the Creditor.
11. **AC-11**: A finalized bill, its items, assignments, images, share snapshots, and debts are immutable. The Captain can idempotently void it only while every debt is `awaiting` and has no payment. Void keeps all history, marks the bill and debts `voided`, records a reason and activity, and permits one later draft to reference it through `replaces_bill_id`.
12. **AC-12**: Bill listing uses cursor pagination ordered by `(created_at DESC, id DESC)`. Every list item preserves the existing bill fields and adds `payer_display_name`, `paid_member_count`, and `member_count`. For a finalized bill, `member_count` is the immutable share count, while `paid_member_count` counts the Creditor, every zero amount share, and every non Creditor debt in `settled`. Draft, reviewed, and voided bills return `0/0`. Bill detail returns the draft or immutable breakdown, current version, review state, OCR jobs, assignments, share snapshots, and debt summary without exposing raw provider responses or permanent asset URLs.
13. **AC-13**: The Creditor or Captain can idempotently delete a draft. Bill owned rows are removed atomically, while the completed idempotency record, a group activity with a redacted bill ID, and cleanup payload remain. Stored assets enter the durable media cleanup flow. A partial upload, process interruption, or failed bill transaction is recoverable through the reserved operation prefix.
14. **AC-14**: Standard bill reads and writes meet the 200 ms server target, preview calculation meets 50 ms for 100 items and 50 members, and successful OCR measures no more than 10 seconds from River job start to committed succeeded state. Metrics use the names and bounded labels defined here for queue depth, provider latency and failures, stale applies, mismatch blocks, finalize latency, and cleanup failures. Logs redact images, signed URLs, API keys, raw OCR, bank account numbers, and item text.

## Decision

**Chosen option**: Durable asynchronous OCR with explicit candidate application and transactional finalization

Use the existing modular Go service. Cloudinary owns private receipt assets, River owns durable OCR and notification work, LlamaExtract produces schema checked candidates, and PostgreSQL owns the financial source of truth. Draft reads may be eventually updated by OCR state, but apply, edit, review, finalize, and void use strong transactional consistency. (basis: `docs/Product_Requirement_Document.md`, the current modular service, durable job processing, and short PostgreSQL transactions)

**Implementation skills**: `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Feature design

### Data model sketch

| Entity | Required fields | Nullable fields | Relations and constraints |
|---|---|---|---|
| `bills` | `id uuid`, `group_id uuid`, `creditor_member_id uuid`, `status bill_status`, money fields as `bigint`, `version int`, timestamps | merchant, bill date, review fields, finalize fields, void fields, `replaces_bill_id` | Composite group foreign keys. Status is `draft`, `finalized`, or `voided`. `replaces_bill_id` is unique and must point to a voided bill in the same group. |
| `bill_images` | `id uuid`, `bill_id uuid`, `group_id uuid`, private object key, zero based position, normalized media type, normalized byte size, normalized SHA 256 checksum, normalized dimensions, timestamps | none | Zero to five immutable rows per bill. Unique `(bill_id, position)` and unique object key. Indexed foreign keys. |
| `bill_items` | `id uuid`, `bill_id uuid`, `group_id uuid`, name, `quantity numeric(12,3)`, `unit_price bigint`, `line_total bigint`, timestamps | none | At most 100 rows per bill. Composite foreign key to bill. Money is nonnegative. |
| `bill_item_assignments` | `id uuid`, `bill_item_id uuid`, `group_id uuid`, `member_id uuid`, `share_ratio numeric(9,8)`, timestamp | none | Unique `(bill_item_id, member_id)`. Ratio is in `(0, 1]`. The service checks the per item sum under the bill lock. |
| `ocr_jobs` | `id uuid`, `bill_id uuid`, `created_by_member_id uuid`, status, provider, retry count, bill version at start, raw response, normalized candidate, warnings, timestamps | provider request ID, responses, cleaned error, applied fields | Partial unique index permits one queued or processing row per bill. The creator and timestamp enforce the manual retry window. Candidate application records actor and time. Raw response is cleared 30 days after job completion. |
| `bill_member_shares` | `id uuid`, bill and member keys, item subtotal, service share, VAT share, discount share, rounding adjustment, final amount | none | Unique `(bill_id, member_id)`. Composite group foreign keys. Inserted only during finalize and never updated. |
| `debts` | Existing debt fields plus `voided_at` | payment and settlement fields remain conditional | Status adds `voided`. Unique bill, debtor, creditor remains. Voided debt must have no payment. |
| `bill_idempotency_keys` | actor, operation, key hash, canonical request hash, operation ID, state, response status and body, retry time, expiry | resource ID until committed | Unique `(actor_user_id, operation, key_hash)`. An in progress reservation exists before external calls. Same key and different hash conflicts. Same key while in progress returns a stable retry response. Expired rows are cleaned durably. |
| `group_activities` | Existing fields and timestamp | redacted metadata | Action type adds OCR apply, bill review, bill void, and draft delete values. Money mutations write an activity in their transaction. |

PostgreSQL indexes follow actual access paths: bills by group and cursor, images and items by bill, assignments by item and member, jobs by bill and active status, shares and debts by bill, and idempotency rows by actor and expiry. Every foreign key column or matching composite prefix has an index. (basis: PostgreSQL foreign key indexing, composite indexes, and partial indexes)

### State transitions

```text
bill
draft -> finalized -> voided
draft -> deleted

ocr job
queued -> processing -> succeeded
queued -> processing -> failed
succeeded -> applied marker

debt created by finalize
awaiting -> later Module 4 states
awaiting -> voided when its bill is voided
```

OCR provider calls and Cloudinary operations happen outside a transaction. Apply, edit, review, finalize, and void acquire the bill row first. Finalize then locks assignments and active members in canonical UUID byte order before it writes shares and debts. Any Module 4 payment start must lock the same debt rows in canonical UUID byte order before changing status or `payment_id`. Void uses that order, so payment start and void cannot both commit. (basis: short transactions and consistent lock ordering)

### API surface

All routes require the live bearer session from spec 0001 and are mounted under `/api/v1/groups/{groupId}`.

| Endpoint | Method | Key inputs | Key outputs | Auth | Key errors |
|---|---|---|---|---|---|
| `/bills` | `POST` | `Idempotency-Key`, optional `replaces_bill_id`; either JSON manual draft or ordered multipart files plus JSON draft | manual `201` or image `202`: bill ID, version `1`, status, reconciliation, optional OCR job ID and state | active member | `400 INVALID_IMAGE`, `409 IDEMPOTENCY_IN_PROGRESS`, `409 IDEMPOTENCY_KEY_REUSED`, `409 INVALID_REPLACEMENT`, `503 STORAGE_UNAVAILABLE` |
| `/bills` | `GET` | cursor, limit up to 50, optional status | Existing bill fields plus `payer_display_name`, `paid_member_count`, `member_count`, and next cursor. Counts are aggregate group data and never expose another member's individual debt status | active member | `400 INVALID_CURSOR`, `404 GROUP_NOT_FOUND` |
| `/bills/{billId}` | `GET` | bill ID | common bill fields; draft items, assignments, reconciliation, preview and safe OCR candidates; or finalized shares and debt summary; five minute image URLs | active member | `404 BILL_NOT_FOUND` |
| `/bills/{billId}` | `PUT` | version, reported bill fields, complete items with client UUIDs, complete assignments | new version, `computed_subtotal`, `computed_total`, mismatch codes, preview | Creditor or Captain | `400 INVALID_DRAFT`, `409 VERSION_CONFLICT`, `409 BILL_IMMUTABLE` |
| `/bills/{billId}` | `DELETE` | version, `Idempotency-Key` | `204` | Creditor or Captain | `409 BILL_IMMUTABLE`, `409 VERSION_CONFLICT` |
| `/bills/{billId}/ocr-jobs` | `POST` | version, `Idempotency-Key` | `202`, OCR job ID, application status `queued`, queued time | Creditor or Captain | `400 IMAGES_REQUIRED`, `409 OCR_ALREADY_RUNNING`, `409 IDEMPOTENCY_IN_PROGRESS`, `429 OCR_LIMIT_REACHED` |
| `/bills/{billId}/ocr-jobs/{jobId}/apply` | `POST` | version, `Idempotency-Key` | new version, `candidate_id = jobId`, computed totals, mismatch codes | Creditor or Captain | `409 OCR_RESULT_STALE`, `409 OCR_NOT_READY`, `409 OCR_ALREADY_APPLIED`, `422 OCR_CANDIDATE_INVALID` |
| `/bills/{billId}/events` | `GET` | optional job ID | SSE `snapshot`, `ocr.updated`, `bill.updated`, and `heartbeat` events with no replay ID | active member | `404 BILL_NOT_FOUND`, stream close followed by reconnect |
| `/bills/{billId}/review` | `POST` | version, `Idempotency-Key` | reviewed version and actor | Creditor or Captain | `409 VERSION_CONFLICT`, `422 BILL_NOT_READY` |
| `/bills/{billId}/finalize` | `POST` | version, `Idempotency-Key` | finalized time, immutable shares, awaiting debts, notification count | Captain | `409 VERSION_CONFLICT`, `409 BILL_NOT_READY`, `409 BILL_IMMUTABLE`, `422 BANK_ACCOUNT_REQUIRED` |
| `/bills/{billId}/void` | `POST` | reason from 1 to 500 characters, `Idempotency-Key` | voided time, reason, voided debt IDs | Captain | `409 BILL_NOT_FINALIZED`, `409 BILL_ALREADY_VOIDED`, `409 PAYMENT_ALREADY_STARTED` |

Every mutation replays the stored response for the same actor, operation, key, and canonical request hash. Multipart hashes use normalized metadata plus SHA 256 hashes of original bytes in zero based order. The same key with a different request hash returns `409 IDEMPOTENCY_KEY_REUSED`. A concurrent retry of an in progress operation returns `409 IDEMPOTENCY_IN_PROGRESS` and `Retry-After`. Idempotency rows live for 24 hours. Repeating finalize or void with a different key returns the terminal state conflict rather than creating another activity.

SSE `snapshot` carries bill version and current OCR summaries. `ocr.updated` carries job ID, application status, retry count, cleaned error code, warnings, and timestamps. `bill.updated` carries bill version, review state, and reconciliation state. `heartbeat` carries server time. Streams do not emit raw OCR, signed URLs, item text, or event IDs.

### Value sourcing

| Action | Value produced or displayed | Source |
|---|---|---|
| Create bill | bill ID, version, initial status and money | PostgreSQL UUID v7, constant version `1`, constant `draft`, and request values with missing money defaulted to zero |
| Create bill | group and Creditor | Authenticated user resolved to active `group_members` row in the path group |
| Create bill | replacement history | Optional request `replaces_bill_id`, which must name one voided bill in the same group with no existing replacement and must not create a cycle |
| Create bill | ordered private images | Multipart zero based order plus normalized Cloudinary response and `bill_images.position` |
| Create bill | retry safe Cloudinary object identity | Server operation ID from the reserved idempotency row plus image position |
| Create bill | checksums and image metadata | Request hash uses original byte SHA 256. Stored checksum, type, bytes, and dimensions use the normalized JPEG bytes and Cloudinary response |
| Every mutation | activity actor, time and metadata | Authenticated member, PostgreSQL `now()`, stable action type, bill ID, version, amount or cleaned reason where relevant |
| OCR | extraction input and application state | Immutable `bill_images` ordered by position and `ocr_jobs.status`, never River internal state |
| OCR | candidate | LlamaExtract receipt schema with ISO `YYYY-MM-DD` date, trimmed merchant and item text, decimal quantity, integer VND amounts, warnings, and nullable confidence, stored on the job whose ID is the candidate ID |
| OCR | locale normalization | Vietnamese number separators and VND currency rules. Ambiguous dates become null with `OCR_DATE_AMBIGUOUS` rather than guessed |
| OCR | item IDs and mismatch | New PostgreSQL UUID v7 IDs at apply time plus reported and computed totals from the normalized candidate |
| Apply or edit | reconciliation and next version | Reported request values, server derived `computed_subtotal` and `computed_total`, stable mismatch codes, and locked `bills.version` |
| Preview | exact item subtotal | Sum of `bill_items.final_price × assignment integer weight / item total weight` across items assigned to the member |
| Preview | integer `item_subtotal` | Floor of the member exact item subtotal after aggregation |
| Preview | service, VAT, and discount shares | Exact proportional shares derived from exact member item subtotals, then integer component floors. With zero subtotal, all three values belong to the Creditor |
| Preview | final amount | Floor of each member exact final amount plus any VND awarded by descending fractional remainder and canonical UUID byte order ascending |
| Preview | rounding adjustment | `final_amount - (item_subtotal + service_charge_share + vat_share - discount_share)` for that member |
| Read expense items | nested integer item shares | Floors of the caller exact participated item shares, reconciled to `item_subtotal` by item fractional remainder and item UUID ascending |
| Review | reviewer and reviewed version | Authenticated active member plus locked bill version |
| Finalize | participant snapshot | Active assignment members plus the Creditor, even when their final amount is zero |
| Read bill | member display data | Current display name and avatar from the user linked through `group_members`; immutable money and membership IDs remain from bill rows |
| List bills | payer display name | Current `users.display_name` joined through the bill's immutable `creditor_member_id` and its `group_members.user_id` |
| List bills | participant count | Count of immutable `bill_member_shares` for a finalized bill. Draft, reviewed, and voided bills use zero because payment progress is not applicable |
| List bills | paid participant count | Count of finalized shares whose member is the Creditor, whose immutable final amount is zero, or whose matching debt status is `settled`. `awaiting`, `pending_confirmation`, and `voided` debts are not paid |
| Finalize | debt fields | Group and bill IDs from the bill, debtor from each positive non Creditor share, creditor from the bill, amount from final amount, constant status `awaiting`, null payment and settlement fields |
| Finalize | bank eligibility | Complete bank code from the embedded VietQR directory plus account number and holder constraints from spec 0001, read from the Creditor profile at finalize time. No bank snapshot is stored |
| Finalize | notification jobs | One job for every member in `bill_member_shares`, including the Creditor, with bill ID, group ID, final amount, Creditor ID, and activity ID |
| Read image | five minute URL | Private Cloudinary object key signed at response time |
| Void | eligibility | Locked debts, each in `awaiting` with null `payment_id` |
| Replace | previous bill | Optional `replaces_bill_id` supplied when the new manual or image draft is created |
| List bills | stable next page | `(created_at, id)` cursor from the final returned row |

Application OCR status always comes from `ocr_jobs`. River job IDs and internal retry payloads are not public. OCR queued time uses PostgreSQL `now()`. The manual limit counts user initiated `ocr_jobs` rows for the bill during the configured window, and the provider field uses the configured LlamaExtract adapter name.

Activity metadata is fixed and redacted. Create records bill ID, manual flag, and image count. Update records bill ID, version, and mismatch codes. OCR apply records bill ID, job ID, and version. Review records bill ID and version. Finalize records bill ID, total, participant count, and debt count. Void records bill ID and the cleaned reason. Draft delete records only the former bill ID. No activity has a hard foreign key to a deleted bill.

### Key invariants

1. Public and persisted money uses signed 64 bit integer VND in Go and `bigint` in PostgreSQL. Allocation intermediates use exact rational values constructed only from integer money and integer weights. Conversion back to `int64` checks range before persistence.
2. Drafts store reported subtotal and total. Review and finalize require reported subtotal to equal the sum of item line totals and reported total to equal subtotal plus service charge plus VAT minus discount. Discount cannot exceed the pre discount sum.
3. Each item has assignment ratios totaling exactly `1.00000000` before review and finalize.
4. The same deterministic exact rational allocation function produces preview and final shares. Stored final snapshots exist because financial history must remain immutable and explainable.
5. The Creditor is the creator and cannot change. Bank data is read live from the profile and is not copied into the bill.
6. A reviewed version is exact. Any semantic draft mutation increments version and clears review.
7. Finalize and void commit every financial row, activity, idempotency result, and River notification insert together or commit nothing.
8. Provider calls never run while a bill or debt row lock is held.
9. Pure request shape checks run before a transaction. Version, state, group scope, active membership, ratios, replacement eligibility, and all database dependent rules run again under the bill lock.
10. Images are immutable after creation. Changing receipt input requires deleting the draft and creating another draft.
11. Equal split compares UUIDs by canonical 16 byte order in Go and PostgreSQL. The final UUID in that order receives any decimal ratio residual. Money remainders follow descending exact fractional remainder, then canonical UUID byte order ascending. The Creditor remains required as the payer and for the existing zero subtotal and discount cap rules, but is not a rounding absorber.
12. Payment start in Module 4 and bill void both lock debt rows by canonical UUID byte order before changing payment or debt state.

### Security model

PaySplit remains a no custody coordinator under the product interpretation of NĐ 52/2024. Module 3 does not hold or transfer funds. Authorization is enforced in the usecase and repeated through group scoped foreign keys in PostgreSQL.

Receipt images, OCR responses, item text, and bank details are sensitive. Cloudinary assets are private. Mobile receives only five minute signed URLs. LlamaExtract receives image bytes from the backend. Raw provider content never appears in API responses or logs and is removed 30 days after job completion even when a draft remains open. Activity metadata uses identifiers, amounts, counts, and cleaned reason codes rather than receipt text or bank data.

### Configuration required

1. `LLAMAINDEX_API_KEY`: LlamaExtract credential, supplied through environment or a secret manager.
2. `LLAMAINDEX_EXTRACT_ENDPOINT`: configured receipt extraction endpoint or agent identifier.
3. `BILL_OCR_PROVIDER_TIMEOUT`: provider call timeout, default eight seconds.
4. `BILL_OCR_MAX_ATTEMPTS`: automatic River attempts, default three.
5. `BILL_OCR_RETRY_BASE_DELAY`: initial exponential retry delay.
6. `BILL_OCR_MANUAL_LIMIT`: user initiated OCR runs per bill and window, default five.
7. `BILL_OCR_MANUAL_WINDOW`: manual retry window, default 24 hours.
8. `BILL_OCR_RAW_RETENTION`: raw provider response retention after OCR job completion, default 30 days.
9. `BILL_IMAGE_MAX_COUNT`: image count per bill, default five.
10. `BILL_IMAGE_MAX_BYTES`: bytes per image, default 10 MB.
11. `BILL_IMAGE_SIGNED_URL_TTL`: mobile signed URL lifetime, default five minutes.
12. `BILL_SSE_HEARTBEAT_INTERVAL`: SSE heartbeat interval, default 15 seconds.
13. `BILL_SSE_MAX_CONNECTION_AGE`: maximum streaming connection age before a clean reconnect, default 15 minutes. The SSE route is exempt from the ordinary request timeout.
14. Existing Cloudinary credentials: private receipt storage and image normalization.
15. Existing database URL: PostgreSQL and River persistence.

### Observability contract

1. `paysplit_ocr_queue_depth` is a gauge with no bill or user label.
2. `paysplit_ocr_provider_duration_seconds` is a histogram labeled only by provider and outcome.
3. `paysplit_ocr_jobs_total` is a counter labeled by final application state and cleaned error code.
4. `paysplit_ocr_stale_apply_total`, `paysplit_bill_mismatch_block_total`, and `paysplit_media_cleanup_failures_total` are counters with bounded reason labels.
5. `paysplit_bill_finalize_duration_seconds` and `paysplit_bill_preview_duration_seconds` are histograms with outcome only.
6. OCR success duration starts when the River worker marks the application job processing and ends when `ocr_jobs.status = succeeded` commits. Queue wait and later user apply time are separate measurements.
7. Redaction tests assert that logs and metric labels never contain object keys, signed URLs, item text, raw OCR, API keys, account numbers, or idempotency keys.

### Critical test scenarios

1. Create a five image bill, observe one River job through SSE, apply the candidate, edit ratios, review, and finalize into exact snapshots and debts, verifies **AC-1** through **AC-10**.
2. Create a manual bill with no image, enter one synthetic item, allocate it, review, and finalize without OCR, verifies **AC-1**, **AC-5**, **AC-6**, and **AC-9**.
3. Edit while OCR runs, then reject stale apply without losing edits, verifies **AC-4**.
4. Exhaust configured provider retries, retain a manual draft, then start a user retry within the configured limit, verifies **AC-2** and **AC-3**.
5. Submit concurrent edit and finalize requests. Exactly one version wins and no partial share, debt, activity, or notification rows exist, verifies **AC-7** and **AC-9**.
6. Reject reads by an inactive or cross group member and reject mutations by an ordinary member, verifies **AC-8**.
7. Void an unpaid bill, preserve history, void its debts, then create one linked replacement. Reject void after any debt enters a payment, verifies **AC-11**.
8. Fail after Cloudinary stores one of several images, then prove durable cleanup removes every orphan, verifies **AC-13**.
9. Load 100 items and 50 members, verify exact totals and target latency with metrics and redacted logs, verifies **AC-6**, **AC-10**, and **AC-14**.
10. Paginate bills with duplicate timestamps, verify payer names and exact `0/0`, partial, and complete payment counts without per bill queries, read draft and finalized detail, refresh an expired image URL, and reconnect SSE from a current snapshot, verifies **AC-12**.
11. Split 400000 VND and 800000 VND items equally across six members and verify every final amount is 200000 VND. Repeat with all input orders permuted and verify identical output, verifies revised **AC-6** and **AC-10**.
12. Split 100000 VND equally across three members and verify the lowest canonical UUID receives 33334 VND when all fractional remainders tie. Verify the Creditor receives no priority, verifies revised **AC-6** and **AC-10**.

## Build plan

The project uses Tracer Bullet. Each slice crosses migration, sqlc, repository, usecase, HTTP, PostgreSQL integration tests, OpenAPI, and module documentation before the next slice grows the workflow.

1. Build the manual draft thread. Add the minimum bill migration changes, module boundaries, idempotent manual create, cursor list, detail, full draft replace, preview, authorization, and live PostgreSQL coverage, satisfies **AC-1**, **AC-5**, **AC-6**, **AC-8**, **AC-12**, and **AC-14**.
2. Add private image drafts. Add `bill_images`, multipart streaming, Cloudinary normalization and signing, image constraints, partial failure cleanup, and multi image create coverage, satisfies **AC-1**, **AC-8**, **AC-12**, and **AC-13**.
3. Add the OCR thread. Extend `ocr_jobs`, install River, implement LlamaExtract and Cloudinary read adapters, enqueue with the bill transaction, expose snapshot based SSE, enforce retry and single active job rules, and schedule raw response cleanup, satisfies **AC-2**, **AC-3**, **AC-4**, **AC-12**, and **AC-14**.
4. Complete allocation and review. Add ratio replacement, reconciliation, deterministic preview, explicit review, review invalidation, stable conflicts, and limits under concurrent edits, satisfies **AC-5**, **AC-6**, **AC-7**, **AC-8**, and **AC-10**.
5. Add finalize. Add immutable share snapshots, bank eligibility, ordered locking, deterministic allocation, positive debt creation, activity and notification inserts, idempotent response replay, and concurrent integration coverage, satisfies **AC-7**, **AC-9**, **AC-10**, and **AC-14**.
6. Add void and replacement. Extend bill and debt states, add safe void checks, replacement uniqueness, durable history, draft deletion, cleanup, and activity behavior, satisfies **AC-11** and **AC-13**.
7. Finish the operational contract. Complete OpenAPI, environment validation, metrics, structured redaction, provider and storage failure tests, SSE reconnect tests, module documentation, and end to end verification of every criterion, satisfies **AC-1** through **AC-14**.
8. Enrich both cursor and legacy bill list reads in one PostgreSQL query. Join the Creditor display name and aggregate immutable shares with debt settlement status, preserve pagination and the existing JSON shape, update sqlc, OpenAPI, handler tests, and PostgreSQL integration coverage, satisfies the extended **AC-12**.
9. Replace early item floor calculation with exact rational aggregation, one final floor per member, and deterministic largest remainder distribution. Keep preview and finalize on the same pure function, satisfies revised **AC-6** and **AC-10**.
10. Preserve the public share fields and derive each member `rounding_adjustment` exactly once from their awarded final amount and integer component fields. Reconcile nested item rows to `item_subtotal` by item fractional remainder without assigning an unparticipated item, satisfies revised **AC-6**, **AC-10**, and **AC-12**.
11. Add permutation, large value, discount, zero subtotal, mixed participant, private item, and regression coverage. Invalidate review on existing unfinalized bills before the new allocator serves finalize, satisfies revised **AC-6**, **AC-7**, **AC-9**, **AC-10**, and **AC-14**.

## Consequences

**Positive**:

1. OCR can fail or be retried without corrupting user edits or financial state.
2. Preview, finalized shares, and debts are exact, explainable, and reproducible.
3. PostgreSQL and River keep the architecture within one existing operational system.
4. Void preserves history and gives the Captain a safe correction path before payment begins.

**Negative and tradeoffs**:

1. Full draft replacement can send a large payload for 100 items and many assignments.
2. Keeping normalized OCR candidates and receipt images increases private data storage and cleanup duties.
3. Reading live Creditor bank data means a later profile change can redirect a future QR, and deleting bank data can block Module 4.
4. SSE uses current state snapshots rather than replay, so clients learn the latest state but not every transient progress event.
5. A zero subtotal bill assigns all service charge and VAT to the Creditor, which is simple but may not match every real receipt.
6. Aggregate payment counts intentionally expose group level completion to every active group member, but never identify which other member has or has not paid.
7. Exact rational arithmetic and deterministic sorting cost more than the former linear floor pass, so the 100 item and 50 member performance target must be measured again.

**Neutral**:

1. The initial bill schema is a starting point. Implementation must add a new sequential migration rather than edit migration `000001`.
2. Final share snapshots intentionally store derived values because finalized financial history is immutable and must remain explainable.
3. `bill_status`, `debt_status`, and `activity_type` gain values that later modules must understand.
4. Existing finalized share snapshots never change. Draft and reviewed bills use the new algorithm only after deployment.

## Follow up

1. [ ] Revisit live bank profile lookup in the Module 4 spec. Decide whether payment creation must snapshot recipient bank data and how grouped debts handle profile changes.
2. [ ] Rerun Agent Skill and MCP discovery for River, Cloudinary, and LlamaExtract when registry access works. The current search timed out and confirmed no candidate.
3. [ ] Add `supabase-postgres-best-practices` to the relevant `AGENTS.md` context through the project knowledge workflow because it materially shaped this schema.
4. [x] Add `DISCOUNT_NOT_ALLOCATABLE` to the OpenAPI error code list, together with the domain error behind it. Done, and the full blocker code list is now enumerated on the `Bill.mismatch_codes` schema.
5. [x] Update `verify.md`, it described the preview as using the Hamilton largest remainder method. Done during the verify run.
6. [ ] Update `docs/bill-ocr-module.md`, it still documents `CalculateHamiltonAllocation` and the largest remainder method. That file belongs to `/sync`, not to this spec.
7. [ ] Reverify revised **AC-6** and **AC-10** after the exact aggregation allocator ships. Historical Creditor absorption evidence in `verify.md` does not verify the revised contract.

## Migration plan

**Strategy**: Direct replacement with review invalidation, no schema change.

**Phases**:

1. Ship the exact allocator and its regression tests while leaving finalized snapshots untouched.
2. Clear review fields on every unfinalized reviewed bill in the deployment transaction, then require users to review the new preview before finalize.

**Rollback**: Revert the allocator. Reviews cleared during deployment stay cleared because restoring an approval for different amounts is unsafe.

**Risks**: A draft can show different member amounts after deployment. Preview, review, bulk close, and finalize must all switch together so one bill version never mixes allocation algorithms.

## Rationale

Reasoning and options considered: see [rationale.md](rationale.md).
