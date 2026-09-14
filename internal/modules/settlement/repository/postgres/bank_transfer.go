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

// SettleBankTransfer gạch nợ khi ngân hàng báo tiền đã vào tài khoản người nhận.
// Chỉ gạch khi khớp đủ: mã tham chiếu, số tiền, tài khoản nhận, và mọi khoản nợ
// của payment còn mở. Không khớp thì trả Outcome tương ứng, không đổi dữ liệu.
func (r *postgresRepository) SettleBankTransfer(ctx context.Context, in repository.BankTransferInput) (domain.BankMatchResult, error) {
	if r.banks == nil {
		return domain.BankMatchResult{}, errors.New("settlement payment support is not configured")
	}
	t := in.Transfer
	var pid, gid uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT id,group_id FROM payments WHERE reference_code=$1`, t.ReferenceCode).Scan(&pid, &gid)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BankMatchResult{Outcome: domain.BankMatchNotFound}, nil
	}
	if err != nil {
		return domain.BankMatchResult{}, fmt.Errorf("find payment by reference: %w", err)
	}
	paymentID, groupID := pid.String(), gid.String()
	result := func(outcome string) domain.BankMatchResult {
		return domain.BankMatchResult{Outcome: outcome, PaymentID: &paymentID, GroupID: &groupID}
	}

	ctx = WithAudienceCache(ctx)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.BankMatchResult{}, fmt.Errorf("begin bank settlement: %w", err)
	}
	defer tx.Rollback(ctx)
	// Thứ tự khóa nhóm → nợ → payment, giống MarkDebtReceived, để không deadlock
	// khi người nhận bấm "Đã nhận tiền" đúng lúc webhook về.
	if err = lockActiveSettlementGroup(ctx, tx, gid); errors.Is(err, domain.ErrGroupNotFound) {
		return result(domain.BankMatchPaymentClosed), nil
	} else if err != nil {
		return domain.BankMatchResult{}, err
	}
	type debtRow struct {
		id        uuid.UUID
		status    string
		paymentID pgtype.UUID
	}
	rows, err := tx.Query(ctx, `SELECT d.id,d.status::text,d.payment_id FROM payment_debts pd JOIN debts d ON d.id=pd.debt_id WHERE pd.payment_id=$1 ORDER BY d.id FOR UPDATE`, pid)
	if err != nil {
		return domain.BankMatchResult{}, fmt.Errorf("lock bank settlement debts: %w", err)
	}
	debts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (debtRow, error) {
		var d debtRow
		return d, row.Scan(&d.id, &d.status, &d.paymentID)
	})
	if err != nil {
		return domain.BankMatchResult{}, fmt.Errorf("iterate bank settlement debts: %w", err)
	}
	var bankTxID pgtype.Int8
	if err = tx.QueryRow(ctx, `SELECT bank_transaction_id FROM payments WHERE id=$1 FOR UPDATE`, pid).Scan(&bankTxID); err != nil {
		return domain.BankMatchResult{}, fmt.Errorf("lock payment: %w", err)
	}
	payment, err := loadPaymentTx(ctx, tx, pid, gid)
	if err != nil {
		return domain.BankMatchResult{}, err
	}

	switch payment.Status {
	case domain.PaymentConfirmed:
		if bankTxID.Valid && bankTxID.Int64 == t.TransactionID {
			return result(domain.BankMatchAlreadyConfirmed), nil
		}
		return result(domain.BankMatchPaymentClosed), nil
	case domain.PaymentPendingProof, domain.PaymentPendingConfirmation:
	default:
		return result(domain.BankMatchPaymentClosed), nil
	}
	if len(debts) == 0 {
		return result(domain.BankMatchPaymentClosed), nil
	}
	debtIDs := make([]uuid.UUID, 0, len(debts))
	for _, d := range debts {
		open := d.status == "awaiting"
		if payment.Status == domain.PaymentPendingConfirmation {
			open = d.status == "pending_confirmation" && d.paymentID.Valid && uuid.UUID(d.paymentID.Bytes) == pid
		}
		if !open {
			return result(domain.BankMatchPaymentClosed), nil
		}
		debtIDs = append(debtIDs, d.id)
	}
	if t.Amount != payment.Amount {
		return result(domain.BankMatchAmountMismatch), nil
	}

	creditorMemberID, err := storedUUID(payment.CreditorMemberID, "creditor member ID")
	if err != nil {
		return domain.BankMatchResult{}, err
	}
	debtorMemberID, err := storedUUID(payment.DebtorMemberID, "debtor member ID")
	if err != nil {
		return domain.BankMatchResult{}, err
	}
	var creditorUser, debtorUser uuid.UUID
	var codeValue, accountValue, holderValue pgtype.Text
	err = tx.QueryRow(ctx, `SELECT c.user_id,d.user_id,u.default_bank_code,u.default_bank_account_number,u.default_bank_account_holder FROM group_members c JOIN users u ON u.id=c.user_id JOIN group_members d ON d.id=$2 WHERE c.id=$1`, creditorMemberID, debtorMemberID).Scan(&creditorUser, &debtorUser, &codeValue, &accountValue, &holderValue)
	if err != nil {
		return domain.BankMatchResult{}, fmt.Errorf("load bank settlement parties: %w", err)
	}
	// QR của payment đang chờ luôn dựng từ tài khoản mặc định hiện tại của người
	// nhận; payment pending_confirmation (dữ liệu từ luồng minh chứng cũ) dùng
	// snapshot đã chụp lúc gửi.
	recipient := payment.Recipient
	if payment.Status == domain.PaymentPendingProof {
		bank, ok := r.banks(codeValue.String)
		if !codeValue.Valid || !accountValue.Valid || !holderValue.Valid || !ok {
			return result(domain.BankMatchAccountMismatch), nil
		}
		recipient = domain.RecipientBank{Code: bank.BIN, Name: bank.Name, AccountNumber: accountValue.String, AccountHolder: holderValue.String}
	}
	if !containsAccount(t.AccountNumbers, recipient.AccountNumber) {
		return result(domain.BankMatchAccountMismatch), nil
	}

	_, err = tx.Exec(ctx, `UPDATE payments SET status='confirmed',confirmation_source='bank_transfer',bank_transaction_id=$2,confirmed_at=now(),submitted_at=COALESCE(submitted_at,now()),recipient_bank_code=$3,recipient_bank_name=$4,recipient_account_number=$5,recipient_account_holder=$6,updated_at=now() WHERE id=$1`,
		pid, t.TransactionID, recipient.Code, recipient.Name, recipient.AccountNumber, recipient.AccountHolder)
	if err != nil {
		return domain.BankMatchResult{}, fmt.Errorf("confirm payment by bank transfer: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE debts SET status='settled',payment_id=$1,settled_at=now(),updated_at=now() WHERE id=ANY($2::uuid[]) AND status IN ('awaiting','pending_confirmation')`, pid, debtIDs)
	if err != nil {
		return domain.BankMatchResult{}, fmt.Errorf("settle debts by bank transfer: %w", err)
	}
	if tag.RowsAffected() != int64(len(debtIDs)) {
		return domain.BankMatchResult{}, fmt.Errorf("settle debts by bank transfer: %d of %d debts updated", tag.RowsAffected(), len(debtIDs))
	}
	metadata, _ := json.Marshal(map[string]any{"payment_id": pid, "debtor_member_id": payment.DebtorMemberID, "creditor_member_id": payment.CreditorMemberID, "amount": payment.Amount, "settled_debt_count": len(debtIDs), "confirmation_source": domain.ConfirmationBankTransfer, "bank_transaction_id": t.TransactionID})
	if _, err = tx.Exec(ctx, `INSERT INTO group_activities(group_id,actor_member_id,actor_kind,action_type,description,metadata) VALUES($1,NULL,'system','payment_confirmed','Ngân hàng đã ghi nhận chuyển khoản, khoản nợ đã được gạch',$2)`, gid, metadata); err != nil {
		return domain.BankMatchResult{}, fmt.Errorf("insert bank settlement activity: %w", err)
	}

	if err = r.publishPaymentSettled(ctx, tx, gid, pid, debtIDs, debtorUser, creditorUser, []settledNotice{
		{notify: in.NotifyDebtor, user: debtorUser},
		{notify: in.NotifyCreditor, user: creditorUser},
	}); err != nil {
		return domain.BankMatchResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.BankMatchResult{}, fmt.Errorf("commit bank settlement: %w", err)
	}
	return result(domain.BankMatchConfirmed), nil
}

type settledNotice struct {
	notify repository.BeforeCommit
	user   uuid.UUID
}

// publishPaymentSettled phát mọi sự kiện realtime và thông báo sau khi một
// payment vừa gạch nợ, dùng chung cho xác nhận qua ngân hàng và người nhận tự
// xác nhận để hai đường không lệch nhau về những gì client được báo.
func (r *postgresRepository) publishPaymentSettled(ctx context.Context, tx pgx.Tx, gid, pid uuid.UUID, debtIDs []uuid.UUID, debtorUser, creditorUser uuid.UUID, notices []settledNotice) error {
	bills, err := r.distinctBillIDs(ctx, tx, debtIDs)
	if err != nil {
		return err
	}
	if err = r.notifyAll(ctx, tx, gid, "settlement.payment_changed", realtime.ScopeSettlement, &pid); err != nil {
		return err
	}
	if err = r.notifyAll(ctx, tx, gid, "group.debts_changed", realtime.ScopeGroup, nil); err != nil {
		return err
	}
	for i := range bills {
		if err = r.notifyAll(ctx, tx, gid, "bill.settlement_changed", realtime.ScopeBill, &bills[i]); err != nil {
			return err
		}
	}
	if err = r.notifyAll(ctx, tx, gid, "group.activity_changed", realtime.ScopeGroup, nil); err != nil {
		return err
	}
	if err = r.notifyInvalidate(ctx, tx, realtime.NormalizeAudience([]uuid.UUID{debtorUser, creditorUser}), realtime.InvalidateBody{
		Scope:   realtime.ScopeHome,
		GroupID: gid,
		Type:    "home.balance_changed",
	}); err != nil {
		return err
	}
	data := map[string]string{"group_id": gid.String(), "payment_id": pid.String()}
	for _, n := range notices {
		if n.notify == nil {
			continue
		}
		recipients := []string{n.user.String()}
		if err = n.notify(ctx, tx, recipients, data); err != nil {
			return err
		}
		if err = r.notifyNotificationCreated(ctx, tx, gid, recipients); err != nil {
			return err
		}
	}
	return nil
}

func containsAccount(candidates []string, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	for _, c := range candidates {
		if strings.TrimSpace(c) == expected {
			return true
		}
	}
	return false
}
