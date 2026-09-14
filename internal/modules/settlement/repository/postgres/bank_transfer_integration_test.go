package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"paysplit-backend/internal/modules/settlement/domain"
	"paysplit-backend/internal/modules/settlement/repository"
	"paysplit-backend/internal/platform/vietqr"
)

func TestSettleBankTransferPostgres(t *testing.T) {
	pool := settlementTestPool(t)
	ctx := context.Background()
	payerUser, creditorUser := uuid.New(), uuid.New()
	groupID, payerMember, creditorMember, billID, debtID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	suffix := time.Now().UnixNano()
	_, err := pool.Exec(ctx, `INSERT INTO users(id,email,phone_number,display_name,password_hash,status,email_verified_at) VALUES($1,$2,$3,'Payer','x','active',now()),($4,$5,$6,'Creditor','x','active',now())`,
		payerUser, fmt.Sprintf("bank.payer.%d@example.invalid", suffix), fmt.Sprintf("+846%08d", suffix%100000000),
		creditorUser, fmt.Sprintf("bank.creditor.%d@example.invalid", suffix), fmt.Sprintf("+845%08d", suffix%100000000))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM groups WHERE id=$1`, groupID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::uuid[])`, []uuid.UUID{payerUser, creditorUser})
	})
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE users SET default_bank_code='VCB',default_bank_account_number='0123456789',default_bank_account_holder='CREDITOR' WHERE id=$1`, []any{creditorUser}},
		{`INSERT INTO groups(id,name,currency,created_by) VALUES($1,'Bank settlement test','VND',$2)`, []any{groupID, payerUser}},
		{`INSERT INTO group_members(id,group_id,user_id,role,status) VALUES($1,$2,$3,'captain','active'),($4,$2,$5,'member','active')`, []any{payerMember, groupID, payerUser, creditorMember, creditorUser}},
		{`INSERT INTO bills(id,group_id,creditor_member_id,status,merchant_name,bill_date,total,subtotal,finalized_at) VALUES($1,$2,$3,'finalized','Cafe',current_date,300000,300000,now())`, []any{billID, groupID, creditorMember}},
		{`INSERT INTO debts(id,group_id,bill_id,debtor_member_id,creditor_member_id,amount,status) VALUES($1,$2,$3,$4,$5,300000,'awaiting')`, []any{debtID, groupID, billID, payerMember, creditorMember}},
	} {
		if _, err = pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewWithPayments(pool, func(string) (BankInfo, bool) {
		return BankInfo{Code: "VCB", Name: "Vietcombank", BIN: "970436", Supported: true}, true
	}, vietqr.New("", "", "SEVQR"))
	payment, _, err := repo.CreatePayment(ctx, repository.CreatePaymentInput{GroupID: groupID.String(), CallerUserID: payerUser.String(), CreditorMemberID: creditorMember.String(), DebtIDs: []string{debtID.String()}, IdempotencyKey: fmt.Sprintf("bank-create-%d", suffix), RequestHash: "bank-create-hash"})
	if err != nil {
		t.Fatal(err)
	}

	notified := map[string][]string{}
	notify := func(kind string) repository.BeforeCommit {
		return func(_ context.Context, _ repository.Executor, targets []string, _ map[string]string) error {
			notified[kind] = append(notified[kind], targets...)
			return nil
		}
	}
	txID := suffix // bank_transaction_id là UNIQUE toàn cục, dùng suffix để các lần chạy không đụng nhau
	settle := func(ref string, amount int64, account string, id int64) domain.BankMatchResult {
		t.Helper()
		res, err := repo.SettleBankTransfer(ctx, repository.BankTransferInput{
			Transfer:     domain.BankTransfer{TransactionID: id, ReferenceCode: ref, Amount: amount, AccountNumbers: []string{account}},
			NotifyDebtor: notify("debtor"), NotifyCreditor: notify("creditor"),
		})
		if err != nil {
			t.Fatalf("SettleBankTransfer(%s,%d,%s) error = %v", ref, amount, account, err)
		}
		return res
	}

	if res := settle("PAYZZZZZZZZ", 300000, "0123456789", txID); res.Outcome != domain.BankMatchNotFound {
		t.Fatalf("unknown reference outcome = %s", res.Outcome)
	}
	if res := settle(payment.ReferenceCode, 299999, "0123456789", txID); res.Outcome != domain.BankMatchAmountMismatch {
		t.Fatalf("short amount outcome = %s", res.Outcome)
	}
	if res := settle(payment.ReferenceCode, 300000, "9999999999", txID); res.Outcome != domain.BankMatchAccountMismatch {
		t.Fatalf("wrong account outcome = %s", res.Outcome)
	}
	if len(notified) != 0 {
		t.Fatalf("mismatches must not notify: %v", notified)
	}

	res := settle(payment.ReferenceCode, 300000, "0123456789", txID)
	if res.Outcome != domain.BankMatchConfirmed || res.PaymentID == nil || *res.PaymentID != payment.ID {
		t.Fatalf("matching transfer result = %+v", res)
	}
	var status, source, debtStatus string
	var bankTx pgtype.Int8
	var image pgtype.Text
	if err = pool.QueryRow(ctx, `SELECT status::text,confirmation_source,bank_transaction_id,image_object_key FROM payments WHERE id=$1`, payment.ID).Scan(&status, &source, &bankTx, &image); err != nil {
		t.Fatal(err)
	}
	if status != domain.PaymentConfirmed || source != domain.ConfirmationBankTransfer || bankTx.Int64 != txID || image.Valid {
		t.Fatalf("payment status=%s source=%s bank_tx=%v image=%v", status, source, bankTx, image)
	}
	if err = pool.QueryRow(ctx, `SELECT status::text FROM debts WHERE id=$1`, debtID).Scan(&debtStatus); err != nil || debtStatus != "settled" {
		t.Fatalf("debt status=%s err=%v", debtStatus, err)
	}
	var systemActivities int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM group_activities WHERE group_id=$1 AND actor_kind='system' AND action_type='payment_confirmed'`, groupID).Scan(&systemActivities); err != nil || systemActivities != 1 {
		t.Fatalf("system activities=%d err=%v", systemActivities, err)
	}
	if len(notified["debtor"]) != 1 || notified["debtor"][0] != payerUser.String() || len(notified["creditor"]) != 1 || notified["creditor"][0] != creditorUser.String() {
		t.Fatalf("notifications = %v", notified)
	}
	loaded, err := repo.GetPayment(ctx, groupID.String(), payerUser.String(), payment.ID)
	if err != nil || loaded.ConfirmationSource != domain.ConfirmationBankTransfer {
		t.Fatalf("GetPayment source=%v err=%v", loaded, err)
	}

	if res := settle(payment.ReferenceCode, 300000, "0123456789", txID); res.Outcome != domain.BankMatchAlreadyConfirmed {
		t.Fatalf("retry outcome = %s", res.Outcome)
	}
	if res := settle(payment.ReferenceCode, 300000, "0123456789", txID+1); res.Outcome != domain.BankMatchPaymentClosed {
		t.Fatalf("second transfer for paid payment outcome = %s", res.Outcome)
	}
	if len(notified["debtor"]) != 1 {
		t.Fatalf("retries must not notify again: %v", notified)
	}
}
