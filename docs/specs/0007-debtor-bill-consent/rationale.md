# 0007. Debtor bill consent v2 rationale

## Context

This decision is deferred to V2. V1 uses the group bill submission lock and bulk finalize contract in spec 0008, with no debtor consent gate.

> ⚠️ Premise note: The system should not describe consent as making someone a debtor immediately. That wording would blur draft review with the immutable financial ledger. The safer boundary is that a member accepts one proposed split, while finalize remains the only action that creates debt.

Today a Creditor or Captain can assign any active member to bill items. Captain review and finalize can then create a debt without an explicit response from that member. Active membership proves group access, but it does not prove agreement with a particular expense or amount.

Consent must survive retries, concurrent edits, mobile partial failures, and later disputes. The existing draft editor replaces items and assignments, so old item detail cannot be reconstructed reliably after a new version. This makes an immutable server generated snapshot necessary.

The feature spans the Go API, PostgreSQL, River notifications, the existing bill state machine, group exit, and one Flutter page. It must preserve the existing allocation math and settlement flow rather than create a second financial calculation.

## Options considered

### Option 1: Durable consent rounds before finalize

Create one version bound round and one response row per positive debtor. Store an immutable breakdown and require the approved round during finalize.

**Pros**:

1. Consent is explicit, auditable, and tied to exact money.
2. Draft and finalized financial states remain separate.
3. Rejection and invalidation are visible state transitions.

**Cons**:

1. Adds two tables, more API state, and more UI states.
2. One silent member can block finalization indefinitely.

### Option 2: Consent fields on item assignments

Store acceptance directly on every item assignment.

**Pros**:

1. Direct relation between an item and its assignee.
2. No separate request join for item reads.

**Cons**:

1. One member must respond many times for one bill.
2. Full draft replacement destroys or rewrites the evidence.
3. Shared VAT, service charge, discount, and rounding do not belong to one item assignment.

### Option 3: Create debt first, then let the debtor approve it

Finalize creates pending debts that are ineffective until accepted.

**Pros**:

1. Keeps review unchanged.
2. Consent acts on a simple debt amount.

**Cons**:

1. The financial ledger contains obligations that are not agreed.
2. Settlement queries and balances need a second meaning for pending debt.
3. Reject must unwind finalized history and conflicts with existing immutability.

### Option 4: Store only current response state

Keep one mutable response per member and derive old detail from the bill.

**Pros**:

1. Small schema and simple reads.

**Cons**:

1. Editing erases what the user actually accepted.
2. Disputes cannot recover old item and charge detail.

## Rationale

Option 1 is chosen because it places consent before the existing immutable ledger boundary. It reuses the exact allocation function, so preview, accepted amount, and finalized debt have one source of truth. A round represents shared version state, while member rows represent independent decisions and support partial batch results cleanly.

The JSON snapshot is deliberate. The data is written once and read as one document on a mobile detail surface. It is not used for relational filtering or financial recomputation. A schema version protects future readers, while relational columns retain ownership, status, amount, pagination, and constraint enforcement.

The batch endpoint processes each bill independently because selected bills have no shared financial transaction. This permits useful progress on mobile and confines locks to one bill at a time. Idempotency applies to the whole submitted batch, and a retry of only failed items uses a new key.

The rollout keeps legacy reviewed bills explainable with `consent_required=false`. New reviews opt into the consent gate behind a feature flag. This avoids forcing already reviewed bills into a state they never entered while allowing one safe rollback switch during deployment.
