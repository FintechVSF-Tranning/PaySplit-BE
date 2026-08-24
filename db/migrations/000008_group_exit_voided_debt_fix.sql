-- +goose NO TRANSACTION
-- +goose Up
-- ---------------------------------------------------------------------------
-- Fix: member exit (Spec 0002 AC-6) wrongly counted a 'voided' debt as still
-- open. debt_status gained 'voided' in spec 0003, after these two partial
-- indexes were built WHERE status <> 'settled'; the backing SumOpenDebtorTotal
-- / SumOpenCreditorTotal queries were fixed in code (queries/groups.sql) to
-- filter status NOT IN ('settled', 'voided'), and these indexes must match
-- that predicate or Postgres will not use them.
-- CONCURRENTLY because these tables take live writes; NO TRANSACTION because
-- CONCURRENTLY cannot run inside a transaction block.
-- ---------------------------------------------------------------------------

DROP INDEX CONCURRENTLY IF EXISTS idx_debts_group_debtor_unsettled;
CREATE INDEX CONCURRENTLY idx_debts_group_debtor_unsettled
    ON debts(group_id, debtor_member_id) INCLUDE (amount)
    WHERE status NOT IN ('settled', 'voided');

DROP INDEX CONCURRENTLY IF EXISTS idx_debts_group_creditor_unsettled;
CREATE INDEX CONCURRENTLY idx_debts_group_creditor_unsettled
    ON debts(group_id, creditor_member_id) INCLUDE (amount)
    WHERE status NOT IN ('settled', 'voided');

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_debts_group_debtor_unsettled;
CREATE INDEX CONCURRENTLY idx_debts_group_debtor_unsettled
    ON debts(group_id, debtor_member_id) INCLUDE (amount)
    WHERE status <> 'settled';

DROP INDEX CONCURRENTLY IF EXISTS idx_debts_group_creditor_unsettled;
CREATE INDEX CONCURRENTLY idx_debts_group_creditor_unsettled
    ON debts(group_id, creditor_member_id) INCLUDE (amount)
    WHERE status <> 'settled';
