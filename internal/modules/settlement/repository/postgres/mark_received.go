package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"paysplit-backend/internal/modules/settlement/domain"
	"paysplit-backend/internal/modules/settlement/repository"
	"paysplit-backend/internal/platform/realtime"
)

// MarkDebtReceived gạch đúng một khoản nợ khi người nhận xác nhận đã nhận đủ
// tiền mà ngân hàng không tự khớp được. Mọi mã QR đang chờ có gom khoản nợ này
// bị chuyển superseded, nên webhook của mã đó về sau chỉ ra payment_closed và
// không thể gạch lần hai.
func (r *postgresRepository) MarkDebtReceived(ctx context.Context, in repository.MarkReceivedInput) (*domain.Payment, []string, error) {
	if r.banks == nil {
		return nil, nil, errors.New("settlement payment support is not configured")
	}
	gid, err := uuid.Parse(in.GroupID)
	if err != nil {
		return nil, nil, domain.ErrGroupNotFound
	}
	uid, err := uuid.Parse(in.CallerUserID)
	if err != nil {
		return nil, nil, domain.ErrGroupNotFound
	}
	did, err := uuid.Parse(in.DebtID)
	if err != nil {
		return nil, nil, domain.ErrDebtNotFound
	}
	ctx = WithAudienceCache(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin mark received: %w", err)
	}
	defer tx.Rollback(ctx)
	if err = lockActiveSettlementGroup(ctx, tx, gid); err != nil {
		return nil, nil, err
	}
	var callerMember uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM group_members WHERE group_id=$1 AND user_id=$2 AND status='active'`, gid, uid).Scan(&callerMember); errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, domain.ErrGroupNotFound
	} else if err != nil {
		return nil, nil, fmt.Errorf("load caller membership: %w", err)
	}
	keyHash := hashString(in.IdempotencyKey)
	replay, _, done, err := beginIdempotency(ctx, tx, uid, "mark_received", keyHash, in.RequestHash)
	if err != nil {
		return nil, nil, err
	}
	if done {
		if err = tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		return replay, replay.CoveredDebtIDs, nil
	}

	var debtorMember, creditorMember uuid.UUID
	var amount int64
	var status string
	err = tx.QueryRow(ctx, `SELECT debtor_member_id,creditor_member_id,amount,status::text FROM debts WHERE id=$1 AND group_id=$2 FOR UPDATE`, did, gid).Scan(&debtorMember, &creditorMember, &amount, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, domain.ErrDebtNotFound
	} else if err != nil {
		return nil, nil, fmt.Errorf("lock debt: %w", err)
	}
	if creditorMember != callerMember {
		return nil, nil, domain.ErrForbidden
	}
	if status != "awaiting" {
		return nil, nil, domain.ErrDebtNotAwaiting
	}

	rows, err := tx.Query(ctx, `SELECT p.id FROM payments p JOIN payment_debts pd ON pd.payment_id=p.id WHERE pd.debt_id=$1 AND p.status='pending_proof' ORDER BY p.id FOR UPDATE OF p`, did)
	if err != nil {
		return nil, nil, fmt.Errorf("lock open payments: %w", err)
	}
	superseded, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, nil, fmt.Errorf("iterate open payments: %w", err)
	}
	if len(superseded) > 0 {
		if _, err = tx.Exec(ctx, `UPDATE payments SET status='superseded',updated_at=now() WHERE id=ANY($1::uuid[])`, superseded); err != nil {
			return nil, nil, fmt.Errorf("supersede open payments: %w", err)
		}
	}

	var creditorUser, debtorUser uuid.UUID
	var codeValue, accountValue, holderValue pgtype.Text
	err = tx.QueryRow(ctx, `SELECT c.user_id,d.user_id,u.default_bank_code,u.default_bank_account_number,u.default_bank_account_holder FROM group_members c JOIN users u ON u.id=c.user_id JOIN group_members d ON d.id=$2 WHERE c.id=$1`, creditorMember, debtorMember).Scan(&creditorUser, &debtorUser, &codeValue, &accountValue, &holderValue)
	if err != nil {
		return nil, nil, fmt.Errorf("load mark received parties: %w", err)
	}
	bank, ok := r.banks(codeValue.String)
	if !codeValue.Valid || !accountValue.Valid || !holderValue.Valid || !ok || strings.TrimSpace(accountValue.String) == "" || strings.TrimSpace(holderValue.String) == "" {
		return nil, nil, domain.ErrBankAccountRequired
	}
	ref, err := newReferenceCode()
	if err != nil {
		return nil, nil, err
	}
	pid := uuid.Must(uuid.NewV7())
	_, err = tx.Exec(ctx, `INSERT INTO payments(id,group_id,debtor_member_id,creditor_member_id,amount,reference_code,status,confirmation_source,submitted_at,confirmed_at,recipient_bank_code,recipient_bank_name,recipient_account_number,recipient_account_holder) VALUES($1,$2,$3,$4,$5,$6,'confirmed','manual',now(),now(),$7,$8,$9,$10)`,
		pid, gid, debtorMember, creditorMember, amount, ref, bank.BIN, bank.Name, accountValue.String, holderValue.String)
	if err != nil {
		return nil, nil, fmt.Errorf("insert manual payment: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO payment_debts(payment_id,debt_id,group_id,debtor_member_id,creditor_member_id) VALUES($1,$2,$3,$4,$5)`, pid, did, gid, debtorMember, creditorMember); err != nil {
		return nil, nil, fmt.Errorf("link manual payment debt: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE debts SET status='settled',payment_id=$1,settled_at=now(),updated_at=now() WHERE id=$2 AND status='awaiting'`, pid, did)
	if err != nil {
		return nil, nil, fmt.Errorf("settle debt: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return nil, nil, domain.ErrDebtNotAwaiting
	}
	metadata, _ := json.Marshal(map[string]any{"payment_id": pid, "debtor_member_id": debtorMember, "amount": amount, "settled_debt_count": 1, "confirmation_source": domain.ConfirmationManual})
	if _, err = tx.Exec(ctx, `INSERT INTO group_activities(group_id,actor_member_id,actor_kind,action_type,description,metadata) VALUES($1,$2,'member','payment_confirmed','Đã xác nhận đã nhận tiền',$3)`, gid, callerMember, metadata); err != nil {
		return nil, nil, fmt.Errorf("insert mark received activity: %w", err)
	}

	// Sheet QR bên người trả đang nghe đúng payment cũ: phải báo cho nó biết mã đã hết hiệu lực.
	for i := range superseded {
		if err = r.notifyAll(ctx, tx, gid, "settlement.payment_changed", realtime.ScopeSettlement, &superseded[i]); err != nil {
			return nil, nil, err
		}
	}
	if err = r.publishPaymentSettled(ctx, tx, gid, pid, []uuid.UUID{did}, debtorUser, creditorUser, []settledNotice{
		{notify: in.NotifyDebtor, user: debtorUser},
	}); err != nil {
		return nil, nil, err
	}
	payment, err := loadPaymentTx(ctx, tx, pid, gid)
	if err != nil {
		return nil, nil, err
	}
	if err = completeIdempotency(ctx, tx, uid, "mark_received", keyHash, httpStatusOK, payment); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit mark received: %w", err)
	}
	return payment, []string{did.String()}, nil
}
