# Review, nttinix/ocr_bill, 2026-08-18

**Reviewed by**: Senior Code Reviewer (fresh perspective on bill and OCR v1)
**Scope**: 26 files, branch vs `dev` (Bill and OCR v1 / spec 0003 surface — `internal/modules/bill/`, `internal/platform/ocr/`, `internal/platform/image/receipt/`, `internal/platform/storage/cloudinary/bill*.go`, `db/migrations/000006_bill_and_ocr_v1.sql`, `docs/specs/0003-bill-ocr-v1/`)
**Verdict**: Approve with nits

## Summary

The Bill and OCR v1 implementation delivers a robust, secure, and resilient financial core for PaySplit. The architecture adheres cleanly to the project conventions: domain models and business errors are decoupled, the usecase layer enforces invariants and deterministic Hamilton allocation with canonical UUID tie breaking, and PostgreSQL transactions protect data integrity with strict lock hierarchies. Idempotency management, Cloudinary signed URLs, River queue integration, and LlamaExtract normalizers all meet or exceed the spec requirements. No blockers or major defects were identified; a few minor points and nits regarding route naming and full replacement semantics are noted below.

## Minor

### 🟡 Full replacement semantics on PATCH route, `internal/modules/bill/delivery/http/handler.go:49-50`
**Problem**: Both `PUT /{id}` and `PATCH /{id}` are mapped to `UpdateDraftBill`, which executes a full replacement of draft items and assignments (deleting previous items and inserting the new list).
**Why it matters**: A caller might expect `PATCH` to perform partial delta updates (e.g. updating only the merchant name without resending the items list), which would clear items if omitted.
**Suggested fix**: Document in the OpenAPI specification that `PATCH` is an alias for full draft update, or require all items to be supplied when mutating drafts.

## Nits

- ⚪ `internal/modules/bill/delivery/http/handler.go:47-48`, Endpoints `POST /{id}/ocr-retry` and `POST /{id}/apply-candidate` provide convenient flat routes; note in API documentation that `job_id` is passed in JSON payload for candidate application.
- ⚪ `internal/modules/bill/usecase/service.go:944-958`, `toAllocationInput` defensively defaults non positive weights to `1.0`. Since `CreateBill` and `UpdateDraftBill` already validate positive numeric weights upfront, this is safe defensive coding.

## Strengths

- **Exact Hamilton Allocation**: The implementation in `allocation.go` guarantees exact sum preservation (`sum(shares) == total`), prevents negative amounts under heavy discounts, and implements deterministic tie breaking via 16 byte canonical UUID order.
- **Strict Concurrency & Lock Hierarchy**: `FinalizeBill` and `VoidBill` lock the bill row first and then lock associated debt rows in ascending UUID order, eliminating race conditions and deadlocks with Module 4 payment processing.
- **Stale OCR Candidate Protection**: Version checks on candidate application prevent background OCR results from overwriting user edits.
- **Privacy & Security**: Receipt images are stored privately on Cloudinary with short lived signed URLs, and group activities redact sensitive OCR text and bank details.
- **Comprehensive Test Suite**: 63 tests across 11 files cover unit, edge cases, error states, and performance targets (<50ms for 100 items / 50 members).

## Test coverage

Well covered across all 14 Acceptance Criteria:
- Unit tests for Hamilton allocation algorithm, currency parsing, date normalization, receipt preprocessing, and signed URL generation.
- Usecase service tests covering authorization guards, optimistic locking, version conflict detection, bank account prerequisite, and void/delete lifecycles.
- Background worker tests for River OCR execution, retry limits, and SSE event streaming.
- PostgreSQL repository integration tests verifying transactions, cascading constraints, and partial unique indexes.
