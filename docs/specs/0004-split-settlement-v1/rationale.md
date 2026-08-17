# 0004. Split and settlement v1: rationale

## Context

Shared group activities like meals, trips, and shared living expenses frequently generate multiple debts between the same members across different bills. For example, member A may owe member B 50,000 VND for lunch, 120,000 VND for dinner, and 30,000 VND for drinks. Forcing member A to make three separate banking transactions is tedious, error prone, and clutters the banking history for both payer and creditor.

At the same time, Vietnam digital payment regulations (Decree 52/2024/ND-CP) impose strict licensing requirements on payment intermediaries that hold or pool user funds. PaySplit deliberately avoids holding funds or automating bank transfers. Money flows peer to peer directly between user bank accounts using the national VietQR standard.

This architectural decision introduces several key technical challenges:
1. PaySplit cannot automatically verify whether money was received in the creditor bank account. It must rely on a secure workflow where payers submit proof and creditors manually confirm or reject.
2. Generating a VietQR requires the creditor latest valid bank account. If an unpaid QR is generated and the creditor later changes their bank account, the unpaid QR must reflect the new bank account, while an already submitted payment must freeze the bank snapshot to keep audit history accurate.
3. Concurrent group actions, such as a Captain voiding an unpaid bill while a debtor is submitting payment proof for that bill debt, can cause data inconsistencies or deadlocks if lock ordering is not strictly enforced.
4. Without automated reminders and stalled payment alerts, pending debts and confirmations can stall indefinitely.

## Options considered

### Option 1: Direct single bill debt settlement without aggregation

Each finalized debt row must be paid individually. A separate VietQR is generated for each bill debt, and each payment maps to exactly one `debts` record.

**Pros**:
- Simpler data model and queries because payments map 1 to 1 with debts.
- No need to manage debt grouping or partial debt selection across bills.

**Cons**:
- Poor user experience when multiple bills exist between the same members, requiring multiple bank transfers.
- Higher friction leads to delayed settlements and higher drop off rates.

### Option 2: Transactionally coordinated multi bill aggregation with dynamic VietQR (Chosen option)

A Payer can group multiple awaiting debts owed to the same Creditor into a single payment record with one unique reference code and one VietQR image. Unpaid payments dynamically display the Creditor latest bank account, while submitted payments freeze the bank account snapshot for historical audit. Confirmation and rejection operate atomically across all covered debts under strict row lock ordering.

**Pros**:
- Optimal user experience: clears all outstanding debts to a creditor in one single bank transfer.
- Preserves full auditability by linking the payment record to underlying bill debts.
- Enforces strict concurrency control with ordered locking (`groups` row first, then `debts` in ascending UUID order, then `payments`).
- Dynamic bank lookup prevents misdirected transfers if a creditor updates their account before payment.

**Cons**:
- Slightly more complex transaction logic and state reconciliation when a payment is rejected.
- All or nothing settlement means a dispute on one bill debt in a grouped payment resets all covered debts.

### Option 3: Off ledger peer to peer confirmation without proof storage

The app only calculates amounts and lets users mark debts as paid without uploading image proof or tracking reference codes.

**Pros**:
- Lowest implementation effort; no storage service integration needed.
- No storage costs for screenshot files.

**Cons**:
- High risk of disputes and confusion when a debtor claims payment but a creditor has no transfer proof or reference code to search their bank statement.
- Fails the core trust and coordination value proposition of PaySplit.

## Rationale

Option 2 was chosen because it directly solves the user friction of multiple bank transfers while preserving complete auditability and regulatory compliance.

Grouping debts into a single payment record with a unique `reference_code` (e.g. `PAY8K3M9X2Z`) allows the creditor to search their bank mobile app or bank statement instantly for the exact reference code and aggregated amount.

The two phase bank snapshot strategy balances accuracy with auditability:
- In `pending_proof` status (before money is sent), the app dynamically queries the Creditor active bank profile. If the Creditor updates their receiving account, the Payer immediately sees the updated account number and new QR code, preventing transfers to stale accounts.
- Once the Payer submits transfer proof (`pending_confirmation`), the system copies the Creditor bank code, account number, and holder name onto the `payments` record as an immutable snapshot. This ensures that even if the Creditor changes their bank account later, the dispute record reflects the exact account where money was claimed to be sent.

Strict lock ordering across transactions (`groups` row first, then `debts` in ascending UUID byte order, then `payments`) eliminates deadlocks between concurrent payment confirmations, rejections, and Module 3 bill voids.

## References

**Project sources**:
- `docs/Product_Requirement_Document.md`, sections 4.1.16, 4.1.17, 4.1.18, 4.1.19, 4.2.1
- `docs/screen_flow.md`, Module 4 Split and Settlement screen flows
- `docs/specs/0002-group-management-v1/index.md`, group coordination lock
- `docs/specs/0003-bill-ocr-v1/index.md`, debt creation and voiding invariants

**Practices & standards**:
- Vietnam National Payment Standard VietQR (NAPAS EMVCo QR code specification)
- PostgreSQL ordered row locking for deadlock prevention in financial operations
- Idempotency keys pattern for safe financial state transitions
- Time limited signed URLs for private asset protection in cloud storage
