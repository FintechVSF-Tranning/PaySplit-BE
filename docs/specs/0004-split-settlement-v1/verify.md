# Verify: Split and settlement v1 · spec 0004

## API and runtime

- [x] Start the real API with the project run command, confirm the health endpoint responds, and confirm migration `000009_split_settlement_v1.sql` is applied in the live PostgreSQL database. → AC-1 through AC-12
- [x] As an active member, call `GET /api/v1/groups/{groupId}/expenses/me` and observe the aggregate summary, paginated finalized bills, one allocation summary per bill, item only rows, and nullable debt fields for a creditor allocation. Confirm the summary values against live debts and `v_member_balances`. → AC-1
- [x] As an active member, call `GET /api/v1/groups/{groupId}/debts` with no filters and with each debtor, creditor, and status filter. Observe cursor pagination and confirm payable, receivable, and the debt matrix remain based on every unsettled group debt rather than the filtered list. → AC-2
- [x] Generate QR payments with omitted debt IDs, an explicit valid set, an empty array, duplicate IDs, a missing creditor, a wrong pair debt, and a non awaiting debt. Observe the specified `201`, `200`, `400`, `404`, and `409` responses and error codes. → AC-3
- [x] Replay an exact debt set, then request a different set. Observe immutable payment debt links, exact set replay, supersession, a global `PAY` reference code, a local NAPAS TLV payload, and the encoded `img.vietqr.io` URL fields. → AC-4
- [x] Remove or invalidate the creditor bank profile before QR creation and while a payment is pending proof. Observe `422 BANK_ACCOUNT_REQUIRED`, preservation of the payment, and dynamic regeneration after correction. After proof submission, change the profile and observe the immutable bank snapshot. Observe that a superseded payment has no active recipient or QR output. → AC-5
- [x] Submit valid JPEG, PNG, and HEIC proof images and reject an oversized or unsupported image and an overlong note. Observe the operation ID based object key, private upload result, winning payment transition, bank snapshot, covered debt transitions, and queued notification. Exercise a losing attempt and observe that it deletes only its own object or records an exact durable cleanup job. → AC-6
- [x] As the creditor, confirm a pending payment and observe one transaction changing the payment to confirmed, all covered debts to settled, a `payment_confirmed` activity, and a notification for the payer. → AC-7
- [x] As the creditor, reject a pending payment with valid and invalid reasons. Observe the rejected payment fields, every covered debt returned to awaiting with no payment ID, a `payment_rejected` activity, and a notification for the payer. → AC-8
- [x] As a creditor and as a captain, manually remind an awaiting debt. Exercise concurrent calls, the 24 hour interval, and the total limit of three. Observe one state update and one notification per eligible reminder. → AC-9
- [x] Run the River reminder and stalled payment workers against eligible rows, including concurrent worker attempts. Observe hourly eligibility, conditional claims, at most three debt reminders, one stalled timestamp, and no duplicate notification or activity. → AC-10
- [x] For QR creation, proof upload, confirmation, rejection, and reminder, exercise a fresh idempotency key, an exact replay, a conflicting request hash, an in progress record, and an expired record. Observe the stored response, `409 IDEMPOTENCY_KEY_REUSED`, `409 IDEMPOTENCY_IN_PROGRESS` with `Retry-After`, expiry cleanup, normalized request hashing, and the proof image digest. Exercise proof against concurrent bill void in both winning orders. → AC-11
- [x] Call every expense, debt, and payment endpoint as an inactive member and an outsider and observe access denial. Read a payment with proof and observe a five minute signed URL. Inspect logs to confirm proof URLs, reference codes, bank account numbers, and notes are redacted. Attempt cross pair payment debt links and invalid state rows and observe PostgreSQL constraints reject them. → AC-12

## Commands

- [x] `go test -run '^$' ./...` completes successfully and proves every package compiles. → AC-1 through AC-12
- [x] `go test ./internal/modules/settlement/... ./internal/platform/vietqr/... ./internal/platform/metrics/... ./internal/config/...` completes successfully. → AC-1 through AC-12
- [x] `go test -tags=integration ./internal/modules/settlement/repository/postgres` completes against the live PostgreSQL database. → AC-3 through AC-12
- [x] Parse `docs/openapi.yaml` and observe every split and settlement route, request body, response, enum, and schema in the API contract. → AC-1 through AC-12

## Acceptance criteria coverage

- AC-1: personal expense summary and finalized bill allocations
- AC-2: debt list filters, pagination, totals, and matrix
- AC-3: QR debt selection and validation
- AC-4: payment creation, replay, supersession, reference, and VietQR output
- AC-5: dynamic bank profile and immutable submitted snapshot
- AC-6: proof validation, storage, locking, cleanup, and notification
- AC-7: atomic confirmation and settlement
- AC-8: atomic rejection and debt reset
- AC-9: manual reminder authorization, interval, limit, and concurrency
- AC-10: River reminder and stalled alert claims
- AC-11: idempotency, expiry, canonical hashes, and lock ordering
- AC-12: access control, signed proof URLs, log redaction, and database constraints
