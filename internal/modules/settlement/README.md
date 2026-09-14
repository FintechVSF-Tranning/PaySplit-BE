# Settlement module

This module coordinates peer to peer payments. It never holds or routes money.

The repository owns PostgreSQL transactions and follows one lock order. It locks the group, then debt rows in UUID order, then the payment. Bill void uses the same group and debt order before it supersedes any pending QR payment.

Payments are confirmed without proof images, in one of two ways. The bank path is `SettleBankTransfer`, driven by the SePay webhook module: a transfer settles a payment only when reference code, exact amount and receiving account all match. The fallback is `MarkDebtReceived`: the creditor confirms receipt of one debt, any pending QR covering it is superseded so a late webhook cannot settle twice. The HTTP layer exposes personal expenses, group debts, payment QR, payment detail, reminder and mark-received routes under `/api/v1/groups/{groupId}`.

VietQR payloads are encoded locally by `internal/platform/vietqr`. River runs an hourly reminder scan and a daily cleanup of expired idempotency records.

Logs must not contain reference codes, bank account numbers, or transfer notes. The module does not log these values. Metrics use only fixed operation and outcome labels.
