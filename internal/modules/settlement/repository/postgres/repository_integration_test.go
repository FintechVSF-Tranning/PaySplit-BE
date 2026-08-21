package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"paysplit-backend/internal/modules/settlement/domain"
	"paysplit-backend/internal/modules/settlement/repository"
	"paysplit-backend/internal/platform/vietqr"
)

func settlementTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	_ = godotenv.Load("../../../../../.env")
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		t.Skip("database URL is not configured")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	if err = pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("database unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestSettlementPaymentLifecyclePostgres(t *testing.T) {
	pool := settlementTestPool(t)
	ctx := context.Background()
	payerUser, creditorUser := uuid.New(), uuid.New()
	groupID, payerMember, creditorMember, billID, debtID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	suffix := time.Now().UnixNano()
	payerPhone := fmt.Sprintf("+849%08d", suffix%100000000)
	creditorPhone := fmt.Sprintf("+848%08d", suffix%100000000)
	_, err := pool.Exec(ctx, `INSERT INTO users(id,email,phone_number,display_name,password_hash,status,email_verified_at) VALUES($1,$2,$3,'Payer','x','active',now()),($4,$5,$6,'Creditor','x','active',now())`, payerUser, fmt.Sprintf("payer.%d@example.invalid", suffix), payerPhone, creditorUser, fmt.Sprintf("creditor.%d@example.invalid", suffix), creditorPhone)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM groups WHERE id=$1`, groupID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::uuid[])`, []uuid.UUID{payerUser, creditorUser})
	})
	if _, err = pool.Exec(ctx, `UPDATE users SET default_bank_code='VCB',default_bank_account_number='0123456789',default_bank_account_holder='CREDITOR' WHERE id=$1`, creditorUser); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO groups(id,name,currency,created_by) VALUES($1,'Settlement test','VND',$2)`, groupID, payerUser); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO group_members(id,group_id,user_id,role,status) VALUES($1,$2,$3,'captain','active'),($4,$2,$5,'member','active')`, payerMember, groupID, payerUser, creditorMember, creditorUser); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO bills(id,group_id,creditor_member_id,status,merchant_name,bill_date,total,subtotal,finalized_at) VALUES($1,$2,$3,'finalized','Cafe',current_date,125000,125000,now())`, billID, groupID, creditorMember); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO debts(id,group_id,bill_id,debtor_member_id,creditor_member_id,amount,status) VALUES($1,$2,$3,$4,$5,125000,'awaiting')`, debtID, groupID, billID, payerMember, creditorMember); err != nil {
		t.Fatal(err)
	}
	repo := NewWithPayments(pool, func(string) (BankInfo, bool) {
		return BankInfo{Code: "VCB", Name: "Vietcombank", BIN: "970436", Supported: true}, true
	}, vietqr.New("", ""))
	payment, created, err := repo.CreatePayment(ctx, repository.CreatePaymentInput{GroupID: groupID.String(), CallerUserID: payerUser.String(), CreditorMemberID: creditorMember.String(), DebtIDs: []string{debtID.String()}, IdempotencyKey: "create-1", RequestHash: "create-hash"})
	if err != nil {
		t.Fatal(err)
	}
	if !created || payment.Status != domain.PaymentPendingProof || len(payment.CoveredDebtIDs) != 1 {
		t.Fatalf("unexpected payment: %+v", payment)
	}
	proofOperation, _, err := repo.PrepareProof(ctx, groupID.String(), payerUser.String(), payment.ID, "proof-1", "proof-hash")
	if err != nil {
		t.Fatal(err)
	}
	payment, err = repo.SubmitProof(ctx, repository.SubmitProofInput{GroupID: groupID.String(), CallerUserID: payerUser.String(), PaymentID: payment.ID, ObjectKey: "payments/" + payment.ID + "/proofs/" + proofOperation, IdempotencyKey: "proof-1", RequestHash: "proof-hash", OperationID: proofOperation})
	if err != nil {
		t.Fatal(err)
	}
	if payment.Status != domain.PaymentPendingConfirmation {
		t.Fatalf("status=%s", payment.Status)
	}
	replayedOperation, replayedPayment, err := repo.PrepareProof(ctx, groupID.String(), payerUser.String(), payment.ID, "proof-1", "proof-hash")
	if err != nil {
		t.Fatalf("PrepareProof() exact replay error = %v", err)
	}
	if replayedOperation != proofOperation || replayedPayment == nil || replayedPayment.Status != domain.PaymentPendingConfirmation {
		t.Fatalf("unexpected proof replay: operation=%s payment=%+v", replayedOperation, replayedPayment)
	}
	payment, settled, err := repo.ConfirmPayment(ctx, repository.PaymentMutationInput{GroupID: groupID.String(), CallerUserID: creditorUser.String(), PaymentID: payment.ID, IdempotencyKey: "confirm-1", RequestHash: "confirm-hash"})
	if err != nil {
		t.Fatal(err)
	}
	if payment.Status != domain.PaymentConfirmed || len(settled) != 1 || settled[0] != debtID.String() {
		t.Fatalf("unexpected confirmation: %+v %v", payment, settled)
	}
	var debtStatus string
	var settledAt *time.Time
	if err = pool.QueryRow(ctx, `SELECT status::text,settled_at FROM debts WHERE id=$1`, debtID).Scan(&debtStatus, &settledAt); err != nil {
		t.Fatal(err)
	}
	if debtStatus != "settled" || settledAt == nil {
		t.Fatalf("debt not settled: %s %v", debtStatus, settledAt)
	}

	bill2, debt2 := uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO bills(id,group_id,creditor_member_id,status,merchant_name,bill_date,total,subtotal,finalized_at) VALUES($1,$2,$3,'finalized','Dinner',current_date,50000,50000,now())`, bill2, groupID, creditorMember); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO debts(id,group_id,bill_id,debtor_member_id,creditor_member_id,amount,status) VALUES($1,$2,$3,$4,$5,50000,'awaiting')`, debt2, groupID, bill2, payerMember, creditorMember); err != nil {
		t.Fatal(err)
	}
	payment2, _, err := repo.CreatePayment(ctx, repository.CreatePaymentInput{GroupID: groupID.String(), CallerUserID: payerUser.String(), CreditorMemberID: creditorMember.String(), DebtIDs: []string{debt2.String()}, IdempotencyKey: "create-2", RequestHash: "create-hash-2"})
	if err != nil {
		t.Fatal(err)
	}
	proofOperation2, _, err := repo.PrepareProof(ctx, groupID.String(), payerUser.String(), payment2.ID, "proof-2", "proof-hash-2")
	if err != nil {
		t.Fatal(err)
	}
	payment2, err = repo.SubmitProof(ctx, repository.SubmitProofInput{GroupID: groupID.String(), CallerUserID: payerUser.String(), PaymentID: payment2.ID, ObjectKey: "payments/" + payment2.ID + "/proofs/" + proofOperation2, IdempotencyKey: "proof-2", RequestHash: "proof-hash-2", OperationID: proofOperation2})
	if err != nil {
		t.Fatal(err)
	}
	reason := "bank transfer not found"
	payment2, reset, err := repo.RejectPayment(ctx, repository.PaymentMutationInput{GroupID: groupID.String(), CallerUserID: creditorUser.String(), PaymentID: payment2.ID, Reason: &reason, IdempotencyKey: "reject-2", RequestHash: "reject-hash-2"})
	if err != nil {
		t.Fatal(err)
	}
	if payment2.Status != domain.PaymentRejected || len(reset) != 1 {
		t.Fatalf("unexpected rejection: %+v %v", payment2, reset)
	}
	reminder, err := repo.RemindDebt(ctx, repository.RemindInput{GroupID: groupID.String(), CallerUserID: creditorUser.String(), DebtID: debt2.String(), IdempotencyKey: "remind-2", RequestHash: "remind-hash-2"})
	if err != nil {
		t.Fatal(err)
	}
	if reminder.ReminderCount != 1 {
		t.Fatalf("reminder count=%d", reminder.ReminderCount)
	}
	var paymentPointer *uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT status::text,payment_id FROM debts WHERE id=$1`, debt2).Scan(&debtStatus, &paymentPointer); err != nil {
		t.Fatal(err)
	}
	if debtStatus != "awaiting" || paymentPointer != nil {
		t.Fatalf("rejected debt not reset: %s %v", debtStatus, paymentPointer)
	}
	if _, err = pool.Exec(ctx, `UPDATE debts SET reminder_count=0,last_reminded_at=NULL WHERE id=$1`, debt2); err != nil {
		t.Fatal(err)
	}
	const racers = 8
	var wg sync.WaitGroup
	var successes int
	var mu sync.Mutex
	errs := make(chan error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, raceErr := repo.RemindDebt(context.Background(), repository.RemindInput{GroupID: groupID.String(), CallerUserID: creditorUser.String(), DebtID: debt2.String(), IdempotencyKey: fmt.Sprintf("race-%d", i), RequestHash: fmt.Sprintf("race-hash-%d", i)})
			if raceErr == nil {
				mu.Lock()
				successes++
				mu.Unlock()
				return
			}
			if !errors.Is(raceErr, domain.ErrReminderRateLimited) {
				errs <- raceErr
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for raceErr := range errs {
		t.Fatalf("unexpected reminder race error: %v", raceErr)
	}
	if successes != 1 {
		t.Fatalf("reminder race successes=%d, want 1", successes)
	}
	var finalCount int
	if err = pool.QueryRow(ctx, `SELECT reminder_count FROM debts WHERE id=$1`, debt2).Scan(&finalCount); err != nil {
		t.Fatal(err)
	}
	if finalCount != 1 {
		t.Fatalf("reminder count after race=%d", finalCount)
	}

	// covers: AC-10, automated workers claim each eligible row once.
	if _, err = pool.Exec(ctx, `UPDATE debts SET created_at=now()-interval '73 hours',last_reminded_at=now()-interval '25 hours' WHERE id=$1`, debt2); err != nil {
		t.Fatal(err)
	}
	automatedNotifications := 0
	automatedNotify := func(context.Context, repository.Executor, []string) error { automatedNotifications++; return nil }
	if err = repo.ProcessAutomatedReminders(ctx, time.Now().Add(-72*time.Hour), 3, automatedNotify); err != nil {
		t.Fatal(err)
	}
	if err = repo.ProcessAutomatedReminders(ctx, time.Now().Add(-72*time.Hour), 3, automatedNotify); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT reminder_count FROM debts WHERE id=$1`, debt2).Scan(&finalCount); err != nil {
		t.Fatal(err)
	}
	if finalCount != 2 || automatedNotifications != 1 {
		t.Fatalf("automated reminder count=%d notifications=%d", finalCount, automatedNotifications)
	}

	payment3, _, err := repo.CreatePayment(ctx, repository.CreatePaymentInput{GroupID: groupID.String(), CallerUserID: payerUser.String(), CreditorMemberID: creditorMember.String(), DebtIDs: []string{debt2.String()}, IdempotencyKey: "create-3", RequestHash: "create-hash-3"})
	if err != nil {
		t.Fatal(err)
	}
	proofOperation3, _, err := repo.PrepareProof(ctx, groupID.String(), payerUser.String(), payment3.ID, "proof-3", "proof-hash-3")
	if err != nil {
		t.Fatal(err)
	}
	payment3, err = repo.SubmitProof(ctx, repository.SubmitProofInput{GroupID: groupID.String(), CallerUserID: payerUser.String(), PaymentID: payment3.ID, ObjectKey: "payments/" + payment3.ID + "/proofs/" + proofOperation3, IdempotencyKey: "proof-3", RequestHash: "proof-hash-3", OperationID: proofOperation3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE payments SET submitted_at=now()-interval '49 hours' WHERE id=$1`, payment3.ID); err != nil {
		t.Fatal(err)
	}
	stalledNotifications := 0
	stalledNotify := func(context.Context, repository.Executor, []string) error { stalledNotifications++; return nil }
	if err = repo.ProcessStalledPayments(ctx, time.Now().Add(-48*time.Hour), stalledNotify); err != nil {
		t.Fatal(err)
	}
	if err = repo.ProcessStalledPayments(ctx, time.Now().Add(-48*time.Hour), stalledNotify); err != nil {
		t.Fatal(err)
	}
	var stalledAt *time.Time
	if err = pool.QueryRow(ctx, `SELECT stalled_alerted_at FROM payments WHERE id=$1`, payment3.ID).Scan(&stalledAt); err != nil {
		t.Fatal(err)
	}
	if stalledAt == nil || stalledNotifications != 1 {
		t.Fatalf("stalled_at=%v notifications=%d", stalledAt, stalledNotifications)
	}

	// covers: AC-11, expired idempotency records are removed by the cleanup operation.
	if _, err = pool.Exec(ctx, `UPDATE payment_idempotency_keys SET expires_at=now()-interval '1 second' WHERE actor_user_id=$1 AND operation='create_payment'`, payerUser); err != nil {
		t.Fatal(err)
	}
	if err = repo.DeleteExpiredIdempotency(ctx); err != nil {
		t.Fatal(err)
	}
	var expiredCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM payment_idempotency_keys WHERE actor_user_id=$1 AND expires_at<=now()`, payerUser).Scan(&expiredCount); err != nil {
		t.Fatal(err)
	}
	if expiredCount != 0 {
		t.Fatalf("expired idempotency records=%d", expiredCount)
	}
}

func TestSettlementListDebtsPostgres(t *testing.T) {
	pool := settlementTestPool(t)
	ctx := context.Background()
	payerUser, creditorUser := uuid.New(), uuid.New()
	groupID, payerMember, creditorMember := uuid.New(), uuid.New(), uuid.New()
	billID, debtID := uuid.New(), uuid.New()
	suffix := time.Now().UnixNano()

	_, err := pool.Exec(ctx, `
		INSERT INTO users(id,email,phone_number,display_name,password_hash,status,email_verified_at)
		VALUES($1,$2,$3,'List payer','x','active',now()),
		      ($4,$5,$6,'List creditor','x','active',now())`,
		payerUser, fmt.Sprintf("list.payer.%d@example.invalid", suffix), fmt.Sprintf("+847%08d", suffix%100000000),
		creditorUser, fmt.Sprintf("list.creditor.%d@example.invalid", suffix), fmt.Sprintf("+846%08d", suffix%100000000))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM groups WHERE id=$1`, groupID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::uuid[])`, []uuid.UUID{payerUser, creditorUser})
	})
	if _, err = pool.Exec(ctx, `INSERT INTO groups(id,name,currency,created_by) VALUES($1,'Debt list test','VND',$2)`, groupID, payerUser); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO group_members(id,group_id,user_id,role,status) VALUES($1,$2,$3,'captain','active'),($4,$2,$5,'member','active')`, payerMember, groupID, payerUser, creditorMember, creditorUser); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO bills(id,group_id,creditor_member_id,status,merchant_name,bill_date,total,subtotal,finalized_at) VALUES($1,$2,$3,'finalized','Debt list',current_date,42000,42000,now())`, billID, groupID, creditorMember); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO debts(id,group_id,bill_id,debtor_member_id,creditor_member_id,amount,status) VALUES($1,$2,$3,$4,$5,42000,'awaiting')`, debtID, groupID, billID, payerMember, creditorMember); err != nil {
		t.Fatal(err)
	}

	page, err := New(pool).ListDebts(ctx, repository.ListDebtsInput{ListInput: repository.ListInput{
		GroupID: groupID.String(), CallerUserID: payerUser.String(), Limit: 20,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Debts) != 1 || page.Debts[0].ID != debtID.String() {
		t.Fatalf("unexpected debts: %+v", page.Debts)
	}
	if page.CallerPayable != 42000 || page.CallerReceivable != 0 {
		t.Fatalf("unexpected caller totals: payable=%d receivable=%d", page.CallerPayable, page.CallerReceivable)
	}
	if len(page.NetMatrix) != 1 || page.NetMatrix[0].TotalAmount != 42000 {
		t.Fatalf("unexpected debt matrix: %+v", page.NetMatrix)
	}
	// covers: AC-12, outsiders and inactive members cannot read settlement data.
	if _, err = New(pool).ListDebts(ctx, repository.ListDebtsInput{ListInput: repository.ListInput{GroupID: groupID.String(), CallerUserID: uuid.NewString(), Limit: 20}}); !errors.Is(err, domain.ErrGroupNotFound) {
		t.Fatalf("outsider error=%v, want group not found", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE group_members SET status='inactive',left_at=now() WHERE id=$1`, creditorMember); err != nil {
		t.Fatal(err)
	}
	if _, err = New(pool).ListExpenses(ctx, repository.ListInput{GroupID: groupID.String(), CallerUserID: creditorUser.String(), Limit: 20}); !errors.Is(err, domain.ErrGroupNotFound) {
		t.Fatalf("inactive member error=%v, want group not found", err)
	}
}

func TestSettlementDatabaseConstraints_AC11AndAC12RejectInvalidPaymentRows(t *testing.T) {
	pool := settlementTestPool(t)
	ctx := context.Background()
	userA, userB := uuid.New(), uuid.New()
	groupID, memberA, memberB := uuid.New(), uuid.New(), uuid.New()
	billA, billB, debtA, debtB, paymentID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	suffix := time.Now().UnixNano()
	_, err := pool.Exec(ctx, `INSERT INTO users(id,email,phone_number,display_name,password_hash,status,email_verified_at) VALUES($1,$2,$3,'A','x','active',now()),($4,$5,$6,'B','x','active',now())`, userA, fmt.Sprintf("constraint.a.%d@example.invalid", suffix), fmt.Sprintf("+845%08d", suffix%100000000), userB, fmt.Sprintf("constraint.b.%d@example.invalid", suffix), fmt.Sprintf("+844%08d", suffix%100000000))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payment_debts WHERE group_id=$1`, groupID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM debts WHERE group_id=$1`, groupID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE group_id=$1`, groupID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM bills WHERE group_id=$1`, groupID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM groups WHERE id=$1`, groupID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1::uuid[])`, []uuid.UUID{userA, userB})
	})
	if _, err = pool.Exec(ctx, `INSERT INTO groups(id,name,currency,created_by) VALUES($1,'Constraints','VND',$2)`, groupID, userA); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO group_members(id,group_id,user_id,role,status) VALUES($1,$2,$3,'captain','active'),($4,$2,$5,'member','active')`, memberA, groupID, userA, memberB, userB); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO bills(id,group_id,creditor_member_id,status,total,subtotal,finalized_at) VALUES($1,$2,$3,'finalized',1,1,now()),($4,$2,$5,'finalized',1,1,now())`, billA, groupID, memberB, billB, memberA); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO debts(id,group_id,bill_id,debtor_member_id,creditor_member_id,amount,status) VALUES($1,$2,$3,$4,$5,1,'awaiting'),($6,$2,$7,$5,$4,1,'awaiting')`, debtA, groupID, billA, memberA, memberB, debtB, billB); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO payments(id,group_id,debtor_member_id,creditor_member_id,amount,reference_code,status) VALUES($1,$2,$3,$4,1,'PAYCONSTR01','pending_proof')`, paymentID, groupID, memberA, memberB); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO payment_debts(payment_id,debt_id,group_id,debtor_member_id,creditor_member_id) VALUES($1,$2,$3,$4,$5)`, paymentID, debtB, groupID, memberA, memberB); err == nil {
		t.Fatal("cross pair payment debt link was accepted")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO payments(group_id,debtor_member_id,creditor_member_id,amount,reference_code,status) VALUES($1,$2,$3,1,'PAYBADSTATE','confirmed')`, groupID, memberA, memberB); err == nil {
		t.Fatal("invalid confirmed payment state was accepted")
	}
}

func TestSettlementQueuesMediaCleanupUsingSharedSchema(t *testing.T) {
	pool := settlementTestPool(t)
	ctx := context.Background()
	objectKey := fmt.Sprintf("payments/debug/proofs/%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_cleanup_jobs WHERE provider='cloudinary' AND object_key=$1`, objectKey)
	})

	if err := New(pool).QueueMediaCleanup(ctx, objectKey, "proof submission compensation"); err != nil {
		t.Fatal(err)
	}
	var provider, storedKey string
	var completedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT provider,object_key,completed_at FROM media_cleanup_jobs WHERE provider='cloudinary' AND object_key=$1`, objectKey).Scan(&provider, &storedKey, &completedAt); err != nil {
		t.Fatal(err)
	}
	if provider != "cloudinary" || storedKey != objectKey || completedAt != nil {
		t.Fatalf("unexpected cleanup job: provider=%s key=%s completed_at=%v", provider, storedKey, completedAt)
	}
}
