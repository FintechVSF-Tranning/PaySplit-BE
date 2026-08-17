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
2. Exact integer money arithmetic and deterministic Hamilton allocation.
3. Short database transactions and consistent row lock ordering.
4. Composite group foreign keys for tenant isolation.
5. Partial indexes for active work and cursor pagination for stable list reads.
6. Private object storage, time limited signed delivery, least privilege, and log redaction.
