# Verify: bill and OCR v1 (spec 0003)

## Commands and runtime evidence
- [x] `go test -v ./internal/modules/bill/repository/postgres/...` (PostgreSQL integration test with real database transactions) (satisfies AC-1, AC-5, AC-7, AC-9, AC-10, AC-11)
- [x] `go test -v ./internal/modules/bill/jobs/...` (River queue OCR background worker test) (satisfies AC-2, AC-3)
- [x] `go test -v ./internal/modules/bill/delivery/http/...` (SSE hub and HTTP handler streaming test) (satisfies AC-2, AC-8, AC-12)
- [x] `go test -v ./internal/modules/bill/usecase/...` (Hamilton allocation and usecase business logic test) (satisfies AC-4, AC-5, AC-6, AC-7, AC-9, AC-10, AC-13)
- [x] `curl -s -i http://localhost:8080/api/v1/bills` (unauthenticated read returns 401 AUTHENTICATION_REQUIRED) (satisfies AC-8)

## Acceptance criteria coverage
- [x] AC-1: Manual draft creation returns 201 Created and image draft creation returns 202 Accepted with private Cloudinary storage.
- [x] AC-2: River background worker processes OCR jobs and broadcasts events to SSE streams.
- [x] AC-3: LlamaExtract client extracts structured candidate with nonnegative integer VND monetary values.
- [x] AC-4: Explicit candidate application updates draft bill without automatic silent overwrites.
- [x] AC-5: Full draft replacement with optimistic locking version check and up to 100 items.
- [x] AC-6: Item assignment ratios sum to 1.0 and preview uses Hamilton largest remainder method.
- [x] AC-7: Explicit review checks that subtotal and total reconcile before finalizing.
- [x] AC-8: Group member authorization blocks unauthorized callers and generates short lived signed URLs.
- [x] AC-9: Synchronous transactional finalization creates immutable bill shares and positive awaiting debts.
- [x] AC-10: Sum of final shares equals bill total with deterministic UUID tie breaking.
- [x] AC-11: Safe void transitions bill and debts to voided while preserving history for replacement.
- [x] AC-12: List and detail reads expose bill state, breakdown, and signed image URLs.
- [x] AC-13: Draft deletion removes bill records and enqueues Cloudinary media cleanup jobs.
- [x] AC-14: In memory calculation and short database transactions protect performance and safety.
