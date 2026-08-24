# Rationale: 0003 Bill and OCR v1

## Context

Bill entry is where untrusted image data becomes financial state. Receipt layouts vary, OCR can be slow or wrong, mobile requests retry, and a Creditor or Captain may edit while a background job is running. The build must make OCR helpful without letting it silently become a debt.

The prototype supports 50 members and 100 items per bill. Standard APIs target 200 ms, split calculation targets 50 ms, and the OCR success path targets 10 seconds. All money is VND integer arithmetic. Finalized bills must remain explainable through item assignments, charge allocation, rounding, share snapshots, and debt records.

The existing backend is a modular Go service with Chi, PostgreSQL, pgx, sqlc, live database sessions, group scoped entities, and Cloudinary. The PRD selects River for background jobs and allows Llama based vision extraction. The team wants LlamaExtract as the receipt provider and Cloudinary private assets for storage.

PaySplit is a no custody coordination product under the product interpretation of NĐ 52/2024. Module 3 creates accounting records but does not move funds. Audit activity, strict authorization, private receipt storage, and redacted observability are required because receipt and debt data can reveal personal spending.

## Options considered

### Option 1: Synchronous OCR and derived final reads

Upload the receipt, call OCR during the request, write its result directly into the bill, and calculate shares whenever a client reads the bill. (basis: the simplest request and response implementation)

**Pros**:

1. Few tables and no background worker are needed.
2. The first happy path is quick to demonstrate.

**Cons**:

1. Provider latency and mobile retries hold request resources and create duplicate side effects.
2. OCR can overwrite edits unless the whole bill is locked for an external call.
3. Recomputing historical shares makes later code changes alter old financial explanations.

### Option 2: Durable OCR candidate and transactional finalization

Store private images, run each OCR request through River, keep the result as a candidate, and require explicit application and review. Finalize writes immutable share snapshots and debts in one PostgreSQL transaction. (basis: PRD reliability requirements, River, explicit OCR review, idempotency, and short PostgreSQL transactions)

**Pros**:

1. Provider failure and retry stay outside financial transactions.
2. Version checks prevent stale OCR from replacing corrections.
3. Immutable snapshots keep old bills exact and explainable.

**Cons**:

1. More states, cleanup jobs, and integration tests are required.
2. Normalized candidates and final snapshots duplicate some data intentionally.

### Option 3: External OCR workflow as the bill source of truth

Let a hosted workflow own upload, extraction, correction state, and callbacks. PaySplit imports only an approved final payload before allocation. (basis: managed document processing architecture)

**Pros**:

1. Less OCR orchestration code lives in PaySplit.
2. Provider tooling may offer richer document diagnostics.

**Cons**:

1. Bill state and authorization are split across systems.
2. Callback idempotency, data residency, and provider lock in become larger concerns.
3. The current mobile review flow and PostgreSQL source of truth require more reconciliation code.

## Rationale

Option 2 fits the existing modular service and keeps one authority for money. River makes OCR durable without introducing Redis. Explicit candidate application and review satisfy the requirement that unconfirmed OCR cannot become a split. Version checks let users keep working without holding a database lock during an external request.

Final share snapshots are an intentional exception to the usual rule against storing derived values. A finalized financial explanation must not change when rounding code or member data changes later. Draft previews remain derived and are not persisted.

Cloudinary reuse avoids another storage provider, and private assets plus short signed URLs match receipt sensitivity. LlamaExtract is isolated behind an `OCRProvider` interface so provider schema or availability changes do not enter the domain or usecase layers.

The engineer chose live bank profile lookup rather than a finalization snapshot. This keeps Module 3 smaller, but it allows a profile change to affect later QR generation. Module 4 must make that tradeoff visible and choose its own payment time snapshot rule.

## Allocation algorithm revision, 2026 08 20

The original design used Hamilton allocation (largest remainder). A fresh model review on 2026 08 20 found that the exact integer rewrite of that code could produce a total larger than the bill, because a member whose share went negative was clamped to zero and the clamped amount simply vanished. The reconciliation step that the earlier floating point version used to catch this had been removed. See [the review](../../reviews/2026-08-20-fix-ocr-bill.md).

Two ways forward were weighed.

### Option A: Keep Hamilton and restore reconciliation

Keep the largest remainder distribution and add back a step that redistributes any clamped amount, plus an invariant check on the total. (basis: the existing implementation and its test suite)

**Pros**:

1. No spec change, no test rewrite, no change to the meaning of `rounding_adjustment`.
2. Fairest possible spread of indivisible VND, since the member with the largest fractional remainder is the one who absorbs it.

**Cons**:

1. The reconciliation loop is exactly the part that was already written once, removed once, and got the total wrong. It is the fragile piece.
2. Correctness depends on the interaction of four separate distribution passes, which is hard to reason about and hard to test exhaustively.
3. Requires sorting members per component, so cost grows with `n log n` rather than `n`.

### Option B: Floor everything and let the Creditor absorb the remainder (chosen)

Every member receives the floor of each component. The Creditor final amount is computed as bill total minus the sum of every other member final amount. (basis: the pattern used by common bill splitting apps such as Splitwise for remainders too small to be worth spreading)

**Pros**:

1. The total is exact by construction, not by a distribution loop that has to be kept correct. This is what closes the bug class rather than the single bug.
2. Linear in the number of members, no sorting, no fractional remainder bookkeeping.
3. Far simpler to read and to test.
4. The Creditor is the person who paid the whole bill up front, so giving them the leftover VND is defensible to a user.

**Cons**:

1. Less fair in principle for rounding. The Creditor always absorbs the remainder rather than the member who was closest to rounding up. In VND that remainder is at most a few dong per component, so the practical unfairness is negligible.
2. A separate and much larger unfairness comes from the discount cap. When a member is assigned more discount than they owe, the cut portion moves to the Creditor whole, not as a few dong. On a bill with a big discount concentrated on one small share, the Creditor can absorb a real amount of money. This is disclosed here because the rounding argument above does not cover it. When the cascade pushes the Creditor below zero the bill is rejected rather than silently shifted.
3. `rounding_adjustment` changes meaning. It is now zero for everyone except the Creditor. The mobile client still gets a truthful answer to who absorbed the indivisible VND.
4. Nine existing tests named after Hamilton have to be rewritten, and the tie breaking test for largest remainder is dropped.
5. Discount still needs a per member cap, because a discount share can exceed what a member owes. Floor allocation alone does not remove that case.

Option B was chosen. The deciding factor is that it makes the sum to total invariant structural rather than emergent, which is the property the review found missing.

## References

**Project sources**:

1. `docs/Product_Requirement_Document.md`: bill upload, OCR, allocation, review, finalization, limits, reliability, explainability, security, and stack.
2. `docs/screen_flow.md`: Module 3 mobile flow and initial route inventory.
3. `docs/scope/scope.md`: Tracer Bullet build approach and GA workflow.
4. `db/migrations/000001_init_schema.up.sql`: initial bill, item, assignment, OCR, debt, notification, and activity schema.
5. `docs/specs/0001-auth-account-v1/index.md`: live authenticated session and profile contract.
6. `docs/specs/0002-group-management-v1/index.md`: active membership, Captain role, group scoped keys, and group activity contract.
7. `docs/project-structure.md`: modular Port and Adapter boundaries, sqlc ownership, bootstrap, and migration convention.
8. `supabase-postgres-best-practices`: data types, constraints, indexes, pagination, and lock guidance applied to this design.

**Practices and standards**:

1. Idempotency keys for money and external side effects.
2. Exact integer money arithmetic and deterministic floor allocation with Creditor remainder absorption.
3. Short database transactions and consistent row lock ordering.
4. Composite group foreign keys for tenant isolation.
5. Partial indexes for active work and cursor pagination for stable list reads.
6. Private object storage, time limited signed delivery, least privilege, and log redaction.
