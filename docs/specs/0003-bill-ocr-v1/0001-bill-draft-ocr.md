# 0001. Bill draft and OCR

## Summary

This child spec covers manual draft creation, ordered private receipt images, durable LlamaExtract jobs, explicit candidate application, SSE progress, and cleanup. OCR is advisory. It never changes a bill until an authorized user applies a current candidate.

## Requirements

This child owns **AC-1** through **AC-4**, the OCR and image parts of **AC-8**, the read behavior in **AC-12**, cleanup in **AC-13**, and OCR operations in **AC-14** from [the umbrella spec](index.md).

## Decision

Cloudinary stores normalized private receipt images. One `ocr_jobs` row represents one user initiated extraction attempt across all current images. River owns delivery retry, and LlamaExtract returns a schema checked candidate stored as JSONB. The bill row version recorded at job creation is the candidate concurrency boundary.

## Feature design

### Creation

1. An active member sends an idempotency key and either no image for a manual draft or one to five multipart images.
2. The backend validates file signatures, declared type, per file size, image count, and decodability before accepting the operation. Image positions begin at zero and cannot change later.
3. The request stream is validated, hashed, and staged in bounded temporary files before external work. A short transaction then reserves the idempotency key, canonical request hash, and a server operation ID. Multipart request hashes contain normalized metadata and each original image byte SHA 256 in order. Cloudinary public IDs use `bills/{operation_id}/{position}`, so a process crash and retry address the same assets without duplicates.
4. The backend streams valid content to Cloudinary as private assets. HEIC and orientation differences are normalized into a JPEG representation used by OCR and later signed reads.
5. After every asset is stored, one database transaction creates the bill, ordered image rows, the completed idempotency result, an OCR job and River row when images exist, and a group activity.
6. A failure before commit schedules the reserved Cloudinary operation prefix in `media_cleanup_jobs`. Cleanup retry is durable and can find assets even when the process crashed before recording individual upload responses.
7. Manual creation returns `201`. Image creation returns `202` with the bill and OCR job. A concurrent request using the same in progress key returns `409 IDEMPOTENCY_IN_PROGRESS` and `Retry-After`.
8. Optional `replaces_bill_id` must identify a voided bill in the same group. One unique incoming reference prevents branching. The service walks the bounded replacement chain under locks to reject cycles while permitting a linear correction history.
9. Bill images are immutable after creation. The only v1 recovery for a wrong image is idempotent draft deletion followed by a new bill.

### OCR attempt

1. A user retry requires a draft, at least one image, Creditor or Captain permission, the current version, an idempotency key, no active job, and allowance in the configured manual window.
2. The worker loads ordered image keys, reads private bytes from Cloudinary, then calls the `OCRProvider` adapter outside a bill transaction.
3. Provider calls use `BILL_OCR_PROVIDER_TIMEOUT`. River uses `BILL_OCR_MAX_ATTEMPTS` with exponential backoff and jitter.
4. A schema valid response becomes a normalized JSONB candidate. Its contract contains trimmed merchant and item text, nullable ISO date, decimal quantities, integer VND money fields, nullable confidence, and warning codes. Vietnamese separators are normalized. Ambiguous dates become null with `OCR_DATE_AMBIGUOUS`. Structurally valid but unreconciled totals remain candidates with mismatch warnings. Invalid or exhausted work becomes failed with a stable cleaned reason.
5. PostgreSQL `NOTIFY` publishes committed state changes across API instances. SSE sends the current snapshot on connect, later changes, and heartbeats. It does not promise event replay. The streaming route bypasses the ordinary request timeout and closes cleanly at `BILL_SSE_MAX_CONNECTION_AGE` so the mobile client reconnects.

The LlamaExtract receipt candidate has these canonical fields: nullable `merchant_name`, nullable ISO `bill_date`, `items` with `name`, decimal string `quantity`, integer `unit_price`, and integer `line_total`, then integer `service_charge`, `vat`, `discount`, `subtotal`, and `total`. Candidate level and field level confidence are nullable decimal values from `0` through `1`. Warnings are stable codes. The application job ID is also the candidate ID.

### Candidate application

1. Apply locks the bill, verifies permission, draft state, request version, succeeded job, unapplied marker, and `ocr_jobs.bill_version_at_start = bills.version`.
2. The normalized candidate replaces merchant, date, totals, and all items. Existing assignments are removed.
3. Reconciliation is computed, bill version increments, review clears, the job records its apply actor and time, and one activity is inserted in the same transaction.
4. A stale candidate remains readable but cannot be applied. Running another OCR job is the recovery path.

### Data constraints and indexes

1. `bill_images` has composite bill and group foreign keys, unique object key, unique bill position, byte and dimension checks, and an index beginning with `bill_id`.
2. `ocr_jobs` has an index for bill history and a partial unique index on `bill_id` where status is queued or processing.
3. Raw response cleanup selects completed jobs after `BILL_OCR_RAW_RETENTION`, clears only raw provider content, and retains normalized candidates and job metadata even when the bill remains draft.
4. `bill_idempotency_keys` stores a SHA 256 key hash rather than the client key. The same key and request hash replays the response for 24 hours. In progress reservations carry an operation ID and retry time, return a stable in progress conflict, and expire into durable cleanup.
5. Draft delete removes bill owned rows only. The transaction first copies object keys or the reserved operation prefix into cleanup rows, then deletes the bill. Activity and idempotency rows survive without a hard foreign key to the deleted bill.

### Failure contract

| Failure | Result |
|---|---|
| Invalid or oversized image | `400 INVALID_IMAGE`, no bill, cleanup any earlier upload |
| Cloudinary unavailable | `503 STORAGE_UNAVAILABLE`, no bill |
| OCR job already active | `409 OCR_ALREADY_RUNNING`, include current job ID |
| Manual OCR limit reached | `429 OCR_LIMIT_REACHED`, include retry time |
| Provider timeout or transient error | River retry, then failed candidate state |
| Provider schema invalid | failed state with `OCR_SCHEMA_INVALID` |
| Bill version changed | `409 OCR_RESULT_STALE`, preserve candidate and draft |
| SSE reconnect | current snapshot, new changes, heartbeat |
| Same idempotency key still running | `409 IDEMPOTENCY_IN_PROGRESS` with `Retry-After` |

### SSE contract

1. `snapshot` contains bill ID, bill version, review state, reconciliation state, and current OCR job summaries.
2. `ocr.updated` contains job ID, application status from `ocr_jobs`, retry count, warnings, cleaned error code, and timestamps.
3. `bill.updated` contains bill version, review state, and reconciliation state.
4. `heartbeat` contains server time. No event carries item text, signed URLs, raw responses, or event IDs.
5. A stream failure after headers closes the connection without a custom error event. The client reconnects and receives a new snapshot.

## Build plan

1. Add bill image, OCR job, idempotency, index, and retention schema changes with PostgreSQL integration tests, satisfies **AC-1** through **AC-4**, **AC-12**, and **AC-13**.
2. Build manual and multipart create across Cloudinary, repository, usecase, HTTP, cleanup, and OpenAPI, satisfies **AC-1**, **AC-8**, and **AC-13**.
3. Build River and LlamaExtract adapters, worker retry, normalized receipt schema, metrics, and failure mapping, satisfies **AC-2**, **AC-3**, and **AC-14**.
4. Build candidate application and version conflict coverage, satisfies **AC-4**.
5. Build snapshot based SSE and signed image reads with reconnect and authorization coverage, satisfies **AC-8**, **AC-12**, and **AC-14**.

## Rationale

One durable attempt row per user request separates provider reliability from bill correctness. JSONB is suitable for a transient candidate whose provider shape may evolve, while applied financial entities remain relational and constrained. Keeping provider calls outside transactions prevents slow external work from holding bill locks.
