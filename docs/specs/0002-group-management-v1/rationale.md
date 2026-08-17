# Rationale: 0002 Group Management v1

## Context

Group management is a core feature of PaySplit, allowing users to collaborate on shared expenses. A group acts as an isolation boundary for bills, items, item assignments, debts, and payments. PostgreSQL schema design enforces this isolation via composite foreign keys `(id, group_id)`.

Key technical and operational requirements:
1. Every group must have an active captain.
2. Group history must remain intact even if members leave.
3. Members cannot leave if they have outstanding unsettled debts or credits.
4. Invite link generation must prevent spam and duplicate active invites while allowing custom expiration.

## Options considered

### Option 1: Modular group service with database level group isolation and zero net balance safety checks (Chosen)

Build group management under `internal/modules/group/` following Modular Clean Architecture. Use `v_member_balances` SQL view to verify net balance before member deletion/exit. Reactivate member records when rejoining.

**Pros**:
- Maintains data integrity and strict isolation boundaries.
- Preserves full audit trail and foreign key validity for bills and debts.
- Prevents invalid exit states when balance is not zero.

**Cons**:
- Rejoin flow requires special `UPDATE` queries instead of standard `INSERT`.

### Option 2: Soft delete group members and create new records on rejoin

Allow creating new `group_members` records every time a user joins.

**Pros**:
- Standard insert query pattern.

**Cons**:
- Breaks historic FK link continuity for previously assigned bill items and debts when a user rejoins.

## Rationale

Option 1 aligns with the database schema comments and architectural design established in migration `000001_init_schema.up.sql`. By enforcing `status='active'` reactivations and zero balance checks, PaySplit ensures accurate financial tracking without orphaned or inconsistent debt records.
