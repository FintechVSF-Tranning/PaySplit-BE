# Settlement module

This module coordinates peer to peer payments. It never holds or routes money.

The repository owns PostgreSQL transactions and follows one lock order. It locks the group, then debt rows in UUID order, then the payment. Bill void uses the same group and debt order before it supersedes any pending QR payment.

The usecase validates payment selection, proof images, notes, rejection reasons, and idempotency inputs. The HTTP layer exposes personal expenses, group debts, payment QR, proof, confirmation, rejection, and reminder routes under `/api/v1/groups/{groupId}`.

VietQR payloads are encoded locally by `internal/platform/vietqr`. Proof uploads accept JPEG, PNG, and HEIC signatures, then Cloudinary stores them as private WebP assets using the lossless `q_100` incoming transformation; signed URLs expire after five minutes. River runs hourly reminder and stalled confirmation scans. It also removes expired idempotency records and retries failed proof cleanup with capped exponential backoff until deletion succeeds.

Logs must not contain proof URLs, reference codes, bank account numbers, or transfer notes. The module does not log these values. Metrics use only fixed operation and outcome labels.
