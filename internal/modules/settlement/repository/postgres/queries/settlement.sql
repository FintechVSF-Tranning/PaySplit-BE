-- name: GetActiveSettlementMembership :one
SELECT gm.id, gm.user_id, gm.role, u.display_name
FROM group_members gm
JOIN users u ON u.id = gm.user_id
JOIN groups g ON g.id = gm.group_id AND g.status = 'active'
WHERE gm.group_id = $1 AND gm.user_id = $2 AND gm.status = 'active';

-- name: ListExpenseRows :many
WITH page AS (
    SELECT DISTINCT b.id, b.created_at
    FROM bill_item_assignments bia
    JOIN bill_items bi ON bi.id = bia.bill_item_id AND bi.group_id = bia.group_id
    JOIN bills b ON b.id = bi.bill_id AND b.group_id = bi.group_id AND b.status = 'finalized'
    WHERE bia.group_id = $1 AND bia.member_id = $2
      AND ($3::timestamptz IS NULL OR b.created_at < $3 OR (b.created_at = $3 AND b.id < $4))
    ORDER BY b.created_at DESC, b.id DESC
    LIMIT $5
), assignment_totals AS (
    SELECT bill_item_id, SUM(weight) AS total_weight
    FROM bill_item_assignments
    GROUP BY bill_item_id
), raw_item_shares AS (
    SELECT bi.bill_id,
           bi.id AS item_id,
           floor(bi.final_price * bia.weight / at.total_weight)::bigint AS item_base,
           (bi.final_price * bia.weight / at.total_weight)
               - floor(bi.final_price * bia.weight / at.total_weight) AS item_fraction,
           bs.item_subtotal
    FROM page
    JOIN bill_items bi ON bi.bill_id = page.id
    JOIN bill_item_assignments bia
      ON bia.bill_item_id = bi.id AND bia.group_id = bi.group_id AND bia.member_id = $2
    JOIN assignment_totals at ON at.bill_item_id = bia.bill_item_id
    JOIN bill_shares bs ON bs.bill_id = bi.bill_id AND bs.member_id = bia.member_id
), ranked_item_shares AS (
    SELECT bill_id,
           item_id,
           item_base,
           item_subtotal - SUM(item_base) OVER (PARTITION BY bill_id) AS residual,
           ROW_NUMBER() OVER (PARTITION BY bill_id ORDER BY item_fraction DESC, item_id ASC) AS remainder_rank
    FROM raw_item_shares
)
SELECT b.id AS bill_id,
       b.bill_date,
       COALESCE(b.merchant_name, '') AS merchant_name,
       bi.name AS item_name,
       bi.quantity::text AS quantity,
       bi.unit_price,
       bi.line_total,
       bi.discount_amount AS item_discount_amount,
       bi.final_price AS item_final_price,
       (bia.weight / at.total_weight)::text AS share_ratio,
       (ris.item_base + CASE WHEN ris.remainder_rank <= ris.residual THEN 1 ELSE 0 END)::bigint AS item_share,
       bs.service_charge_share,
       bs.vat_share,
       bs.discount_share,
       bs.rounding_adjustment,
       bs.final_amount,
       b.creditor_member_id,
       creditor.display_name AS creditor_display_name,
       d.id AS debt_id,
       COALESCE(d.status::text, '') AS debt_status,
       b.created_at
FROM page
JOIN bills b ON b.id = page.id
JOIN bill_items bi ON bi.bill_id = b.id AND bi.group_id = b.group_id
JOIN bill_item_assignments bia ON bia.bill_item_id = bi.id AND bia.group_id = bi.group_id AND bia.member_id = $2
JOIN assignment_totals at ON at.bill_item_id = bia.bill_item_id
JOIN ranked_item_shares ris ON ris.bill_id = b.id AND ris.item_id = bi.id
JOIN bill_shares bs ON bs.bill_id = b.id AND bs.member_id = bia.member_id
JOIN group_members creditor_member ON creditor_member.id = b.creditor_member_id
JOIN users creditor ON creditor.id = creditor_member.user_id
LEFT JOIN debts d ON d.bill_id = b.id AND d.debtor_member_id = bia.member_id AND d.creditor_member_id = b.creditor_member_id
ORDER BY b.created_at DESC, b.id DESC, bi.position, bi.id
;

-- name: GetExpenseSummary :one
SELECT
    COALESCE(SUM(d.amount) FILTER (WHERE d.debtor_member_id = $2 AND d.status IN ('awaiting', 'pending_confirmation')), 0)::bigint AS total_owed,
    COALESCE(SUM(d.amount) FILTER (WHERE d.debtor_member_id = $2 AND d.status = 'settled'), 0)::bigint AS total_settled,
    COALESCE(SUM(d.amount) FILTER (WHERE d.creditor_member_id = $2 AND d.status IN ('awaiting', 'pending_confirmation')), 0)::bigint AS total_receivable,
    COALESCE((SELECT v.net_balance FROM v_member_balances v WHERE v.group_id = $1 AND v.member_id = $2), 0)::bigint AS net_balance
FROM debts d WHERE d.group_id = $1;

-- name: ListDebtRows :many
SELECT d.id, d.bill_id, b.bill_date, COALESCE(b.merchant_name, '') AS merchant_name,
       d.debtor_member_id, du.display_name AS debtor_display_name, du.avatar_object_key AS debtor_avatar_object_key,
       d.creditor_member_id, cu.display_name AS creditor_display_name, cu.avatar_object_key AS creditor_avatar_object_key,
       d.amount, d.status::text AS status, d.reminder_count, d.payment_id, d.created_at, d.settled_at
FROM debts d
JOIN bills b ON b.id = d.bill_id AND b.group_id = d.group_id
JOIN group_members dm ON dm.id = d.debtor_member_id
JOIN users du ON du.id = dm.user_id
JOIN group_members cm ON cm.id = d.creditor_member_id
JOIN users cu ON cu.id = cm.user_id
WHERE d.group_id = $1
  AND (sqlc.narg('debtor_id')::uuid IS NULL OR d.debtor_member_id = sqlc.narg('debtor_id'))
  AND (sqlc.narg('creditor_id')::uuid IS NULL OR d.creditor_member_id = sqlc.narg('creditor_id'))
  AND (sqlc.narg('status')::text IS NULL OR d.status::text = sqlc.narg('status'))
  AND (sqlc.narg('cursor_created_at')::timestamptz IS NULL OR d.created_at < sqlc.narg('cursor_created_at') OR (d.created_at = sqlc.narg('cursor_created_at') AND d.id < sqlc.narg('cursor_id')))
ORDER BY d.created_at DESC, d.id DESC
LIMIT $2;

-- name: GetCallerDebtTotals :one
SELECT
    COALESCE(SUM(amount) FILTER (WHERE debtor_member_id = $2 AND status IN ('awaiting', 'pending_confirmation')), 0)::bigint AS payable,
    COALESCE(SUM(amount) FILTER (WHERE creditor_member_id = $2 AND status IN ('awaiting', 'pending_confirmation')), 0)::bigint AS receivable
FROM debts WHERE group_id = $1;

-- name: GetDebtMatrix :many
WITH directional AS (
    SELECT debtor_member_id, creditor_member_id, SUM(amount)::bigint AS amount, COUNT(*)::bigint AS debt_count
    FROM debts
    WHERE group_id = $1 AND status IN ('awaiting', 'pending_confirmation')
    GROUP BY debtor_member_id, creditor_member_id
), pairs AS (
    SELECT LEAST(debtor_member_id, creditor_member_id) AS left_id,
           GREATEST(debtor_member_id, creditor_member_id) AS right_id,
           SUM(CASE WHEN debtor_member_id < creditor_member_id THEN amount ELSE -amount END)::bigint AS net,
           SUM(debt_count)::bigint AS debt_count
    FROM directional GROUP BY 1, 2
)
SELECT (CASE WHEN net > 0 THEN left_id ELSE right_id END)::uuid AS debtor_member_id,
       (CASE WHEN net > 0 THEN right_id ELSE left_id END)::uuid AS creditor_member_id,
       abs(net)::bigint AS total_amount,
       debt_count
FROM pairs WHERE net <> 0
ORDER BY debtor_member_id, creditor_member_id;
