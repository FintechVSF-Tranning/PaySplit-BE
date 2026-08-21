package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/modules/settlement/domain"
	"paysplit-backend/internal/modules/settlement/repository"
	dbgen "paysplit-backend/internal/modules/settlement/repository/postgres/sqlc"
)

const (
	defaultLimit = 20
	maxLimit     = 100
)

type BankInfo struct {
	Code, Name, BIN string
	Supported       bool
}
type BankDirectory interface{ Get(string) (BankInfo, bool) }
type QRGenerator interface {
	Build(string, string, string, string, int64) (string, string, error)
}

type postgresRepository struct {
	pool  *pgxpool.Pool
	banks func(string) (BankInfo, bool)
	qr    QRGenerator
}

func New(pool *pgxpool.Pool) repository.Repository {
	if pool == nil {
		panic("settlement repository pool must not be nil")
	}
	return &postgresRepository{pool: pool}
}

func NewWithPayments(pool *pgxpool.Pool, lookup func(string) (BankInfo, bool), qr QRGenerator) repository.Repository {
	if pool == nil {
		panic("settlement repository pool must not be nil")
	}
	if lookup == nil || qr == nil {
		panic("settlement payment dependencies must not be nil")
	}
	return &postgresRepository{pool: pool, banks: lookup, qr: qr}
}

func (r *postgresRepository) activeMembership(ctx context.Context, groupID, userID string) (dbgen.GetActiveSettlementMembershipRow, error) {
	gid, err := uuid.Parse(groupID)
	if err != nil {
		return dbgen.GetActiveSettlementMembershipRow{}, domain.ErrGroupNotFound
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		return dbgen.GetActiveSettlementMembershipRow{}, domain.ErrGroupNotFound
	}
	row, err := dbgen.New(r.pool).GetActiveSettlementMembership(ctx, dbgen.GetActiveSettlementMembershipParams{GroupID: pgUUID(gid), UserID: pgUUID(uid)})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, domain.ErrGroupNotFound
	}
	if err != nil {
		return row, fmt.Errorf("load active settlement membership: %w", err)
	}
	return row, nil
}

func normalizedLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

func (r *postgresRepository) ListExpenses(ctx context.Context, in repository.ListInput) (*domain.ExpensePage, error) {
	membership, err := r.activeMembership(ctx, in.GroupID, in.CallerUserID)
	if err != nil {
		return nil, err
	}
	gid := uuid.UUID(membership.ID.Bytes)
	limit := normalizedLimit(in.Limit)
	var cursorAt pgtype.Timestamptz
	var cursorID pgtype.UUID
	if in.Cursor != nil {
		at, id, decodeErr := decodeCursor(*in.Cursor)
		if decodeErr != nil {
			return nil, domain.ErrInvalidCursor
		}
		cursorAt, cursorID = pgtype.Timestamptz{Time: at, Valid: true}, pgUUID(id)
	}
	q := dbgen.New(r.pool)
	rows, err := q.ListExpenseRows(ctx, dbgen.ListExpenseRowsParams{
		GroupID: pgUUID(uuid.MustParse(in.GroupID)), MemberID: pgUUID(gid), Column3: cursorAt, ID: cursorID, Limit: int32(limit + 1),
	})
	if err != nil {
		return nil, fmt.Errorf("list personal expenses: %w", err)
	}

	seen := make(map[string]struct{}, limit+1)
	billOrder := make([]string, 0, limit+1)
	for _, row := range rows {
		id := uuid.UUID(row.BillID.Bytes).String()
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			billOrder = append(billOrder, id)
		}
	}
	hasMore := len(billOrder) > limit
	allowed := seen
	if hasMore {
		allowed = make(map[string]struct{}, limit)
		for _, id := range billOrder[:limit] {
			allowed[id] = struct{}{}
		}
	}
	items := make([]domain.ExpenseItem, 0, len(rows))
	for _, row := range rows {
		billID := uuid.UUID(row.BillID.Bytes).String()
		if _, ok := allowed[billID]; !ok {
			continue
		}
		item := domain.ExpenseItem{
			BillID: billID, BillDate: row.BillDate.Time, MerchantName: row.MerchantName,
			ItemName: row.ItemName, Quantity: row.Quantity, UnitPrice: row.UnitPrice,
			LineTotal: row.LineTotal, ItemDiscountAmount: row.ItemDiscountAmount,
			ItemFinalPrice: row.ItemFinalPrice, ShareRatio: row.ShareRatio, ItemShare: row.ItemShare,
			ServiceChargeShare: row.ServiceChargeShare, VATShare: row.VatShare,
			DiscountShare: row.DiscountShare, RoundingAdjustment: row.RoundingAdjustment,
			FinalAmount: row.FinalAmount, CreditorMemberID: uuid.UUID(row.CreditorMemberID.Bytes).String(),
			CreditorDisplayName: row.CreditorDisplayName, DebtStatus: fmt.Sprint(row.DebtStatus), CreatedAt: row.CreatedAt.Time,
		}
		if row.DebtID.Valid {
			v := uuid.UUID(row.DebtID.Bytes).String()
			item.DebtID = &v
		}
		items = append(items, item)
	}
	summaryRow, err := q.GetExpenseSummary(ctx, dbgen.GetExpenseSummaryParams{GroupID: pgUUID(uuid.MustParse(in.GroupID)), DebtorMemberID: membership.ID})
	if err != nil {
		return nil, fmt.Errorf("load expense summary: %w", err)
	}
	page := &domain.ExpensePage{Items: items, Summary: domain.ExpenseSummary{
		TotalOwed: summaryRow.TotalOwed, TotalSettled: summaryRow.TotalSettled,
		TotalReceivable: summaryRow.TotalReceivable, NetBalance: summaryRow.NetBalance,
	}}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		v := encodeCursor(last.CreatedAt, last.BillID)
		page.NextCursor = &v
	}
	return page, nil
}

func (r *postgresRepository) ListDebts(ctx context.Context, in repository.ListDebtsInput) (*domain.DebtPage, error) {
	membership, err := r.activeMembership(ctx, in.GroupID, in.CallerUserID)
	if err != nil {
		return nil, err
	}
	gid := uuid.MustParse(in.GroupID)
	limit := normalizedLimit(in.Limit)
	params := dbgen.ListDebtRowsParams{GroupID: pgUUID(gid), Limit: int32(limit + 1)}
	if in.DebtorID != nil {
		id, parseErr := uuid.Parse(*in.DebtorID)
		if parseErr != nil {
			return nil, domain.ErrInvalidInput
		}
		params.DebtorID = pgUUID(id)
	}
	if in.CreditorID != nil {
		id, parseErr := uuid.Parse(*in.CreditorID)
		if parseErr != nil {
			return nil, domain.ErrInvalidInput
		}
		params.CreditorID = pgUUID(id)
	}
	if in.Status != nil {
		params.Status = pgtype.Text{String: *in.Status, Valid: true}
	}
	if in.Cursor != nil {
		at, id, decodeErr := decodeCursor(*in.Cursor)
		if decodeErr != nil {
			return nil, domain.ErrInvalidCursor
		}
		params.CursorCreatedAt, params.CursorID = pgtype.Timestamptz{Time: at, Valid: true}, pgUUID(id)
	}
	q := dbgen.New(r.pool)
	rows, err := q.ListDebtRows(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list debts: %w", err)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	debts := make([]domain.Debt, 0, len(rows))
	for _, row := range rows {
		d := domain.Debt{ID: uuid.UUID(row.ID.Bytes).String(), BillID: uuid.UUID(row.BillID.Bytes).String(), BillDate: row.BillDate.Time,
			MerchantName: row.MerchantName, DebtorMemberID: uuid.UUID(row.DebtorMemberID.Bytes).String(), DebtorDisplayName: row.DebtorDisplayName,
			CreditorMemberID: uuid.UUID(row.CreditorMemberID.Bytes).String(), CreditorDisplayName: row.CreditorDisplayName,
			Amount: row.Amount, Status: row.Status, ReminderCount: row.ReminderCount, CreatedAt: row.CreatedAt.Time}
		if row.DebtorAvatarObjectKey.Valid {
			v := row.DebtorAvatarObjectKey.String
			d.DebtorAvatarObjectKey = &v
		}
		if row.CreditorAvatarObjectKey.Valid {
			v := row.CreditorAvatarObjectKey.String
			d.CreditorAvatarObjectKey = &v
		}
		if row.PaymentID.Valid {
			v := uuid.UUID(row.PaymentID.Bytes).String()
			d.PaymentID = &v
		}
		if row.SettledAt.Valid {
			v := row.SettledAt.Time
			d.SettledAt = &v
		}
		debts = append(debts, d)
	}
	totals, err := q.GetCallerDebtTotals(ctx, dbgen.GetCallerDebtTotalsParams{GroupID: pgUUID(gid), DebtorMemberID: membership.ID})
	if err != nil {
		return nil, fmt.Errorf("load caller debt totals: %w", err)
	}
	matrixRows, err := q.GetDebtMatrix(ctx, pgUUID(gid))
	if err != nil {
		return nil, fmt.Errorf("load debt matrix: %w", err)
	}
	matrix := make([]domain.DebtMatrixEntry, 0, len(matrixRows))
	for _, row := range matrixRows {
		debtor, err1 := uuidFromValue(row.DebtorMemberID)
		creditor, err2 := uuidFromValue(row.CreditorMemberID)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("map debt matrix identifiers")
		}
		matrix = append(matrix, domain.DebtMatrixEntry{DebtorMemberID: debtor, CreditorMemberID: creditor, TotalAmount: row.TotalAmount, DebtCount: row.DebtCount})
	}
	page := &domain.DebtPage{Debts: debts, NetMatrix: matrix, CallerPayable: totals.Payable, CallerReceivable: totals.Receivable}
	if hasMore && len(debts) > 0 {
		last := debts[len(debts)-1]
		v := encodeCursor(last.CreatedAt, last.ID)
		page.NextCursor = &v
	}
	return page, nil
}

func uuidFromValue(v any) (string, error) {
	switch x := v.(type) {
	case pgtype.UUID:
		if x.Valid {
			return uuid.UUID(x.Bytes).String(), nil
		}
	case uuid.UUID:
		return x.String(), nil
	case string:
		id, err := uuid.Parse(x)
		if err == nil {
			return id.String(), nil
		}
	}
	return "", errors.New("invalid uuid value")
}

func pgUUID(v uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: v, Valid: true} }

func encodeCursor(t time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(value string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, uuid.UUID{}, err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.UUID{}, errors.New("malformed cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.UUID{}, err
	}
	id, err := uuid.Parse(parts[1])
	return at, id, err
}

func (r *postgresRepository) CreatePayment(ctx context.Context, in repository.CreatePaymentInput) (*domain.Payment, bool, error) {
	if r.banks == nil || r.qr == nil {
		return nil, false, errors.New("settlement payment support is not configured")
	}
	gid, err := uuid.Parse(in.GroupID)
	if err != nil {
		return nil, false, domain.ErrGroupNotFound
	}
	uid, err := uuid.Parse(in.CallerUserID)
	if err != nil {
		return nil, false, domain.ErrGroupNotFound
	}
	cid, err := uuid.Parse(in.CreditorMemberID)
	if err != nil {
		return nil, false, domain.ErrCreditorNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin create payment: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM groups WHERE id=$1 FOR UPDATE`, gid); err != nil {
		return nil, false, fmt.Errorf("lock group: %w", err)
	}
	var debtorID uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM group_members WHERE group_id=$1 AND user_id=$2 AND status='active'`, gid, uid).Scan(&debtorID); errors.Is(err, pgx.ErrNoRows) {
		return nil, false, domain.ErrGroupNotFound
	} else if err != nil {
		return nil, false, fmt.Errorf("load debtor: %w", err)
	}
	var bankCode, account, holder string
	var creditorUser uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT gm.user_id,u.default_bank_code,u.default_bank_account_number,u.default_bank_account_holder FROM group_members gm JOIN users u ON u.id=gm.user_id WHERE gm.id=$1 AND gm.group_id=$2 AND gm.status='active'`, cid, gid).Scan(&creditorUser, &bankCode, &account, &holder); errors.Is(err, pgx.ErrNoRows) {
		return nil, false, domain.ErrCreditorNotFound
	} else if err != nil {
		return nil, false, domain.ErrBankAccountRequired
	}
	bank, ok := r.banks(bankCode)
	if !ok || !bank.Supported || strings.TrimSpace(account) == "" || strings.TrimSpace(holder) == "" {
		return nil, false, domain.ErrBankAccountRequired
	}
	keyHash := hashString(in.IdempotencyKey)
	replay, done, err := beginIdempotency(ctx, tx, uid, "create_payment", keyHash, in.RequestHash)
	if err != nil {
		return nil, false, err
	}
	if done {
		return replay, false, nil
	}

	ids := make([]uuid.UUID, 0, len(in.DebtIDs))
	for _, raw := range in.DebtIDs {
		id, e := uuid.Parse(raw)
		if e != nil {
			return nil, false, domain.ErrInvalidInput
		}
		ids = append(ids, id)
	}
	var rows pgx.Rows
	if len(ids) == 0 {
		rows, err = tx.Query(ctx, `SELECT id,amount FROM debts WHERE group_id=$1 AND debtor_member_id=$2 AND creditor_member_id=$3 AND status='awaiting' ORDER BY id FOR UPDATE`, gid, debtorID, cid)
	} else {
		rows, err = tx.Query(ctx, `SELECT id,amount FROM debts WHERE id=ANY($1::uuid[]) AND group_id=$2 AND debtor_member_id=$3 AND creditor_member_id=$4 AND status='awaiting' ORDER BY id FOR UPDATE`, ids, gid, debtorID, cid)
	}
	if err != nil {
		return nil, false, fmt.Errorf("lock payment debts: %w", err)
	}
	selected := make([]uuid.UUID, 0)
	var amount int64
	for rows.Next() {
		var id uuid.UUID
		var value int64
		if e := rows.Scan(&id, &value); e != nil {
			rows.Close()
			return nil, false, e
		}
		selected = append(selected, id)
		amount += value
	}
	rows.Close()
	if len(selected) == 0 || (len(ids) > 0 && len(selected) != len(ids)) {
		return nil, false, domain.ErrDebtsNotAwaiting
	}
	var oldID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM payments WHERE group_id=$1 AND debtor_member_id=$2 AND creditor_member_id=$3 AND status='pending_proof' FOR UPDATE`, gid, debtorID, cid).Scan(&oldID)
	if err == nil {
		existing, loadErr := loadPaymentTx(ctx, tx, oldID, gid)
		if loadErr != nil {
			return nil, false, loadErr
		}
		if sameUUIDSet(existing.CoveredDebtIDs, selected) {
			payload, imageURL, buildErr := r.qr.Build(bank.BIN, account, holder, existing.ReferenceCode, existing.Amount)
			if buildErr != nil {
				return nil, false, domain.ErrBankAccountRequired
			}
			existing.QRPayload, existing.QRImageURL = payload, imageURL
			existing.Recipient = domain.RecipientBank{Code: bank.BIN, Name: bank.Name, AccountNumber: account, AccountHolder: holder, BIN: bank.BIN}
			if err = completeIdempotency(ctx, tx, uid, "create_payment", keyHash, httpStatusOK, existing); err != nil {
				return nil, false, err
			}
			if err = tx.Commit(ctx); err != nil {
				return nil, false, err
			}
			return existing, false, nil
		}
		if _, err = tx.Exec(ctx, `UPDATE payments SET status='superseded',updated_at=now() WHERE id=$1`, oldID); err != nil {
			return nil, false, err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	ref := in.ReferenceCode
	if ref == "" {
		ref, err = newReferenceCode()
		if err != nil {
			return nil, false, err
		}
	}
	payload, imageURL, err := r.qr.Build(bank.BIN, account, holder, ref, amount)
	if err != nil {
		return nil, false, err
	}
	pid := uuid.Must(uuid.NewV7())
	_, err = tx.Exec(ctx, `INSERT INTO payments(id,group_id,debtor_member_id,creditor_member_id,amount,reference_code,qr_payload,status) VALUES($1,$2,$3,$4,$5,$6,$7,'pending_proof')`, pid, gid, debtorID, cid, amount, ref, payload)
	if err != nil {
		return nil, false, fmt.Errorf("insert payment: %w", err)
	}
	batch := &pgx.Batch{}
	for _, id := range selected {
		batch.Queue(`INSERT INTO payment_debts(payment_id,debt_id,group_id,debtor_member_id,creditor_member_id) VALUES($1,$2,$3,$4,$5)`, pid, id, gid, debtorID, cid)
	}
	br := tx.SendBatch(ctx, batch)
	if err = br.Close(); err != nil {
		return nil, false, fmt.Errorf("link payment debts: %w", err)
	}
	metadata, _ := json.Marshal(map[string]any{"payment_id": pid, "creditor_member_id": cid, "amount": amount, "covered_debt_count": len(selected)})
	if _, err = tx.Exec(ctx, `INSERT INTO group_activities(group_id,actor_member_id,actor_kind,action_type,description,metadata) VALUES($1,$2,'member','payment_created','Payment QR created',$3)`, gid, debtorID, metadata); err != nil {
		return nil, false, err
	}
	if in.BeforeCommit != nil {
		if err = in.BeforeCommit(ctx, tx, []string{creditorUser.String()}); err != nil {
			return nil, false, err
		}
	}
	p, err := loadPaymentTx(ctx, tx, pid, gid)
	if err != nil {
		return nil, false, err
	}
	p.QRPayload, p.QRImageURL = payload, imageURL
	p.Recipient = domain.RecipientBank{Code: bank.BIN, Name: bank.Name, AccountNumber: account, AccountHolder: holder, BIN: bank.BIN}
	if err = completeIdempotency(ctx, tx, uid, "create_payment", keyHash, httpStatusCreated, p); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit create payment: %w", err)
	}
	return p, true, nil
}

const (
	httpStatusOK      = 200
	httpStatusCreated = 201
)

func (r *postgresRepository) GetPayment(ctx context.Context, groupID, callerUserID, paymentID string) (*domain.Payment, error) {
	if r.banks == nil || r.qr == nil {
		return nil, errors.New("settlement payment support is not configured")
	}
	gid, e := uuid.Parse(groupID)
	if e != nil {
		return nil, domain.ErrGroupNotFound
	}
	uid, e := uuid.Parse(callerUserID)
	if e != nil {
		return nil, domain.ErrGroupNotFound
	}
	pid, e := uuid.Parse(paymentID)
	if e != nil {
		return nil, domain.ErrPaymentNotFound
	}
	if _, e = r.activeMembership(ctx, groupID, callerUserID); e != nil {
		return nil, e
	}
	p, e := loadPaymentPool(ctx, r.pool, pid, gid)
	if e != nil {
		return nil, e
	}
	var debtorUser, creditorUser uuid.UUID
	e = r.pool.QueryRow(ctx, `SELECT d.user_id,c.user_id FROM group_members d JOIN group_members c ON c.id=$2 WHERE d.id=$1`, uuid.MustParse(p.DebtorMemberID), uuid.MustParse(p.CreditorMemberID)).Scan(&debtorUser, &creditorUser)
	if e != nil {
		return nil, e
	}
	if uid != debtorUser && uid != creditorUser {
		var role string
		if e = r.pool.QueryRow(ctx, `SELECT role::text FROM group_members WHERE group_id=$1 AND user_id=$2 AND status='active'`, gid, uid).Scan(&role); e != nil || role != "captain" {
			return nil, domain.ErrForbidden
		}
	}
	if p.Status == domain.PaymentPendingProof {
		var code, account, holder string
		e = r.pool.QueryRow(ctx, `SELECT default_bank_code,default_bank_account_number,default_bank_account_holder FROM users WHERE id=$1`, creditorUser).Scan(&code, &account, &holder)
		if e != nil {
			return nil, domain.ErrBankAccountRequired
		}
		bank, ok := r.banks(code)
		if !ok || !bank.Supported {
			return nil, domain.ErrBankAccountRequired
		}
		payload, image, e := r.qr.Build(bank.BIN, account, holder, p.ReferenceCode, p.Amount)
		if e != nil {
			return nil, domain.ErrBankAccountRequired
		}
		p.QRPayload, p.QRImageURL, p.Recipient = payload, image, domain.RecipientBank{Code: bank.BIN, Name: bank.Name, AccountNumber: account, AccountHolder: holder, BIN: bank.BIN}
	} else if p.Status == domain.PaymentSuperseded {
		p.QRPayload = ""
		p.QRImageURL = ""
		p.Recipient = domain.RecipientBank{}
	}
	return p, nil
}

func (r *postgresRepository) PrepareProof(ctx context.Context, groupID, callerUserID, paymentID, idempotencyKey, requestHash string) (string, *domain.Payment, error) {
	if r.banks == nil || r.qr == nil {
		return "", nil, errors.New("settlement payment support is not configured")
	}
	gid, e := uuid.Parse(groupID)
	if e != nil {
		return "", nil, domain.ErrGroupNotFound
	}
	uid, e := uuid.Parse(callerUserID)
	if e != nil {
		return "", nil, domain.ErrGroupNotFound
	}
	pid, e := uuid.Parse(paymentID)
	if e != nil {
		return "", nil, domain.ErrPaymentNotFound
	}
	if _, e = r.activeMembership(ctx, groupID, callerUserID); e != nil {
		return "", nil, e
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return "", nil, e
	}
	defer tx.Rollback(ctx)
	keyHash := hashString(idempotencyKey)
	operationID := uuid.Must(uuid.NewV7())
	tag, e := tx.Exec(ctx, `INSERT INTO payment_idempotency_keys(actor_user_id,operation,key_hash,canonical_request_hash,operation_id) VALUES($1,'submit_proof',$2,$3,$4) ON CONFLICT DO NOTHING`, uid, keyHash, requestHash, operationID)
	if e != nil {
		return "", nil, e
	}
	if tag.RowsAffected() == 0 {
		var storedHash, state string
		var storedOperation uuid.UUID
		var body []byte
		e = tx.QueryRow(ctx, `SELECT canonical_request_hash,state::text,operation_id,response_body FROM payment_idempotency_keys WHERE actor_user_id=$1 AND operation='submit_proof' AND key_hash=$2 FOR UPDATE`, uid, keyHash).Scan(&storedHash, &state, &storedOperation, &body)
		if e != nil {
			return "", nil, e
		}
		if storedHash != requestHash {
			return "", nil, domain.ErrIdempotencyConflict
		}
		if state == "completed" {
			var replay domain.Payment
			if e = json.Unmarshal(body, &replay); e != nil {
				return "", nil, e
			}
			if e = tx.Commit(ctx); e != nil {
				return "", nil, e
			}
			return storedOperation.String(), &replay, nil
		}
		operationID = storedOperation
	}
	payment, e := loadPaymentTx(ctx, tx, pid, gid)
	if e != nil {
		return "", nil, e
	}
	var debtorUser uuid.UUID
	if e = tx.QueryRow(ctx, `SELECT user_id FROM group_members WHERE id=$1 AND group_id=$2`, uuid.MustParse(payment.DebtorMemberID), gid).Scan(&debtorUser); e != nil {
		return "", nil, e
	}
	if debtorUser != uid {
		return "", nil, domain.ErrForbidden
	}
	if payment.Status != domain.PaymentPendingProof {
		return "", nil, domain.ErrPaymentNotPendingProof
	}
	var code, account, holder string
	e = tx.QueryRow(ctx, `SELECT u.default_bank_code,u.default_bank_account_number,u.default_bank_account_holder FROM group_members gm JOIN users u ON u.id=gm.user_id WHERE gm.id=$1 AND gm.group_id=$2 AND gm.status='active'`, uuid.MustParse(payment.CreditorMemberID), gid).Scan(&code, &account, &holder)
	if e != nil {
		return "", nil, domain.ErrBankAccountRequired
	}
	bank, ok := r.banks(code)
	if !ok || !bank.Supported {
		return "", nil, domain.ErrBankAccountRequired
	}
	if _, _, e = r.qr.Build(bank.BIN, account, holder, payment.ReferenceCode, payment.Amount); e != nil {
		return "", nil, domain.ErrBankAccountRequired
	}
	if e = tx.Commit(ctx); e != nil {
		return "", nil, e
	}
	return operationID.String(), nil, nil
}

func loadPaymentPool(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, pid, gid uuid.UUID) (*domain.Payment, error) {
	return loadPayment(ctx, q, pid, gid)
}
func loadPaymentTx(ctx context.Context, q pgx.Tx, pid, gid uuid.UUID) (*domain.Payment, error) {
	return loadPayment(ctx, q, pid, gid)
}
func loadPayment(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, pid, gid uuid.UUID) (*domain.Payment, error) {
	var p domain.Payment
	var debtor, creditor uuid.UUID
	var qr pgtype.Text
	var image, note, rejection pgtype.Text
	var submitted, confirmed, rejected pgtype.Timestamptz
	var bankCode, bankName, account, holder pgtype.Text
	err := q.QueryRow(ctx, `SELECT id::text,group_id::text,debtor_member_id,creditor_member_id,amount,reference_code,status::text,qr_payload,image_object_key,note,rejection_reason,created_at,submitted_at,confirmed_at,rejected_at,recipient_bank_code,recipient_bank_name,recipient_account_number,recipient_account_holder FROM payments WHERE id=$1 AND group_id=$2`, pid, gid).Scan(&p.ID, &p.GroupID, &debtor, &creditor, &p.Amount, &p.ReferenceCode, &p.Status, &qr, &image, &note, &rejection, &p.CreatedAt, &submitted, &confirmed, &rejected, &bankCode, &bankName, &account, &holder)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrPaymentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load payment: %w", err)
	}
	p.DebtorMemberID, p.CreditorMemberID = debtor.String(), creditor.String()
	p.QRPayload = qr.String
	if image.Valid {
		v := image.String
		p.ImageObjectKey = &v
	}
	if note.Valid {
		v := note.String
		p.Note = &v
	}
	if rejection.Valid {
		v := rejection.String
		p.RejectionReason = &v
	}
	if submitted.Valid {
		v := submitted.Time
		p.SubmittedAt = &v
	}
	if confirmed.Valid {
		v := confirmed.Time
		p.ConfirmedAt = &v
	}
	if rejected.Valid {
		v := rejected.Time
		p.RejectedAt = &v
	}
	if bankCode.Valid {
		p.Recipient = domain.RecipientBank{Code: bankCode.String, Name: bankName.String, AccountNumber: account.String, AccountHolder: holder.String}
	}
	rows, e := q.Query(ctx, `SELECT debt_id FROM payment_debts WHERE payment_id=$1 ORDER BY debt_id`, pid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		p.CoveredDebtIDs = append(p.CoveredDebtIDs, id.String())
	}
	return &p, rows.Err()
}

func beginIdempotency(ctx context.Context, tx pgx.Tx, actor uuid.UUID, op, keyHash, requestHash string) (*domain.Payment, bool, error) {
	var storedHash, state string
	var body []byte
	existing := false
	err := tx.QueryRow(ctx, `INSERT INTO payment_idempotency_keys(actor_user_id,operation,key_hash,canonical_request_hash,operation_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING canonical_request_hash,state::text,response_body`, actor, op, keyHash, requestHash, uuid.Must(uuid.NewV7())).Scan(&storedHash, &state, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		existing = true
		err = tx.QueryRow(ctx, `SELECT canonical_request_hash,state::text,response_body FROM payment_idempotency_keys WHERE actor_user_id=$1 AND operation=$2 AND key_hash=$3 FOR UPDATE`, actor, op, keyHash).Scan(&storedHash, &state, &body)
	}
	if err != nil {
		return nil, false, err
	}
	if storedHash != requestHash {
		return nil, false, domain.ErrIdempotencyConflict
	}
	if existing && state == "in_progress" {
		return nil, false, domain.ErrIdempotencyInProgress
	}
	if state == "completed" {
		var p domain.Payment
		if e := json.Unmarshal(body, &p); e != nil {
			return nil, false, e
		}
		return &p, true, nil
	}
	return nil, false, nil
}

func resumeProofIdempotency(ctx context.Context, tx pgx.Tx, actor uuid.UUID, keyHash, requestHash, operationID string) (*domain.Payment, bool, error) {
	opID, e := uuid.Parse(operationID)
	if e != nil {
		return nil, false, domain.ErrIdempotencyConflict
	}
	var storedHash, state string
	var storedOperation uuid.UUID
	var body []byte
	e = tx.QueryRow(ctx, `SELECT canonical_request_hash,state::text,operation_id,response_body FROM payment_idempotency_keys WHERE actor_user_id=$1 AND operation='submit_proof' AND key_hash=$2 FOR UPDATE`, actor, keyHash).Scan(&storedHash, &state, &storedOperation, &body)
	if e != nil {
		return nil, false, e
	}
	if storedHash != requestHash || storedOperation != opID {
		return nil, false, domain.ErrIdempotencyConflict
	}
	if state == "completed" {
		var replay domain.Payment
		if e = json.Unmarshal(body, &replay); e != nil {
			return nil, false, e
		}
		return &replay, true, nil
	}
	if state != "in_progress" {
		return nil, false, domain.ErrIdempotencyConflict
	}
	return nil, false, nil
}
func completeIdempotency(ctx context.Context, tx pgx.Tx, actor uuid.UUID, op, keyHash string, status int, p *domain.Payment) error {
	body, e := json.Marshal(p)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE payment_idempotency_keys SET state='completed',response_code=$4,response_body=$5 WHERE actor_user_id=$1 AND operation=$2 AND key_hash=$3`, actor, op, keyHash, status, body)
	return e
}
func hashString(v string) string { sum := sha256.Sum256([]byte(v)); return hex.EncodeToString(sum[:]) }
func newReferenceCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	raw := make([]byte, 8)
	if _, e := rand.Read(raw); e != nil {
		return "", e
	}
	for i := range b {
		b[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return "PAY" + string(b), nil
}
func sameUUIDSet(existing []string, selected []uuid.UUID) bool {
	if len(existing) != len(selected) {
		return false
	}
	m := make(map[string]struct{}, len(existing))
	for _, v := range existing {
		m[v] = struct{}{}
	}
	for _, v := range selected {
		if _, ok := m[v.String()]; !ok {
			return false
		}
	}
	return true
}
func uuidStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}
func (r *postgresRepository) SubmitProof(ctx context.Context, in repository.SubmitProofInput) (*domain.Payment, error) {
	if r.banks == nil || r.qr == nil {
		return nil, errors.New("settlement payment support is not configured")
	}
	gid, e := uuid.Parse(in.GroupID)
	if e != nil {
		return nil, domain.ErrGroupNotFound
	}
	uid, e := uuid.Parse(in.CallerUserID)
	if e != nil {
		return nil, domain.ErrGroupNotFound
	}
	pid, e := uuid.Parse(in.PaymentID)
	if e != nil {
		return nil, domain.ErrPaymentNotFound
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM groups WHERE id=$1 FOR UPDATE`, gid); e != nil {
		return nil, e
	}
	var memberID uuid.UUID
	if e = tx.QueryRow(ctx, `SELECT id FROM group_members WHERE group_id=$1 AND user_id=$2 AND status='active'`, gid, uid).Scan(&memberID); errors.Is(e, pgx.ErrNoRows) {
		return nil, domain.ErrGroupNotFound
	} else if e != nil {
		return nil, e
	}
	keyHash := hashString(in.IdempotencyKey)
	replay, done, e := resumeProofIdempotency(ctx, tx, uid, keyHash, in.RequestHash, in.OperationID)
	if e != nil {
		return nil, e
	}
	if done {
		if e = tx.Commit(ctx); e != nil {
			return nil, e
		}
		return replay, nil
	}
	rows, e := tx.Query(ctx, `SELECT d.id,d.status::text FROM payment_debts pd JOIN debts d ON d.id=pd.debt_id WHERE pd.payment_id=$1 ORDER BY d.id FOR UPDATE`, pid)
	if e != nil {
		return nil, e
	}
	var debtIDs []uuid.UUID
	valid := true
	for rows.Next() {
		var id uuid.UUID
		var status string
		if e = rows.Scan(&id, &status); e != nil {
			rows.Close()
			return nil, e
		}
		debtIDs = append(debtIDs, id)
		valid = valid && status == "awaiting"
	}
	rows.Close()
	if len(debtIDs) == 0 || !valid {
		return nil, domain.ErrDebtsNotAwaiting
	}
	if _, e = tx.Exec(ctx, `SELECT id FROM payments WHERE id=$1 AND group_id=$2 FOR UPDATE`, pid, gid); e != nil {
		return nil, e
	}
	payment, e := loadPaymentTx(ctx, tx, pid, gid)
	if e != nil {
		return nil, e
	}
	if payment.DebtorMemberID != memberID.String() {
		return nil, domain.ErrForbidden
	}
	if payment.Status != domain.PaymentPendingProof {
		return nil, domain.ErrPaymentNotPendingProof
	}
	var creditorUser uuid.UUID
	var code, account, holder string
	e = tx.QueryRow(ctx, `SELECT gm.user_id,u.default_bank_code,u.default_bank_account_number,u.default_bank_account_holder FROM group_members gm JOIN users u ON u.id=gm.user_id WHERE gm.id=$1 AND gm.group_id=$2 AND gm.status='active'`, uuid.MustParse(payment.CreditorMemberID), gid).Scan(&creditorUser, &code, &account, &holder)
	if e != nil {
		return nil, domain.ErrBankAccountRequired
	}
	bank, ok := r.banks(code)
	if !ok || !bank.Supported {
		return nil, domain.ErrBankAccountRequired
	}
	payload, imageURL, e := r.qr.Build(bank.BIN, account, holder, payment.ReferenceCode, payment.Amount)
	if e != nil {
		return nil, domain.ErrBankAccountRequired
	}
	_, e = tx.Exec(ctx, `UPDATE payments SET status='pending_confirmation',qr_payload=$2,image_object_key=$3,note=$4,recipient_bank_code=$5,recipient_bank_name=$6,recipient_account_number=$7,recipient_account_holder=$8,submitted_at=now(),updated_at=now() WHERE id=$1`, pid, payload, in.ObjectKey, in.Note, bank.BIN, bank.Name, account, holder)
	if e != nil {
		return nil, fmt.Errorf("submit proof: %w", e)
	}
	tag, e := tx.Exec(ctx, `UPDATE debts SET status='pending_confirmation',payment_id=$1,updated_at=now() WHERE id=ANY($2::uuid[]) AND status='awaiting'`, pid, debtIDs)
	if e != nil || tag.RowsAffected() != int64(len(debtIDs)) {
		return nil, domain.ErrDebtsNotAwaiting
	}
	metadata, _ := json.Marshal(map[string]any{"payment_id": pid, "creditor_member_id": payment.CreditorMemberID, "amount": payment.Amount, "has_note": in.Note != nil})
	_, e = tx.Exec(ctx, `INSERT INTO group_activities(group_id,actor_member_id,actor_kind,action_type,description,metadata) VALUES($1,$2,'member','payment_submitted','Payment proof submitted',$3)`, gid, memberID, metadata)
	if e != nil {
		return nil, e
	}
	if in.BeforeCommit != nil {
		if e = in.BeforeCommit(ctx, tx, []string{creditorUser.String()}); e != nil {
			return nil, e
		}
	}
	payment, e = loadPaymentTx(ctx, tx, pid, gid)
	if e != nil {
		return nil, e
	}
	payment.QRImageURL = imageURL
	if e = completeIdempotency(ctx, tx, uid, "submit_proof", keyHash, httpStatusOK, payment); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return payment, nil
}

func (r *postgresRepository) QueueMediaCleanup(ctx context.Context, objectKey, _ string) error {
	_, e := r.pool.Exec(ctx, `INSERT INTO media_cleanup_jobs(provider,object_key) VALUES('cloudinary',$1) ON CONFLICT (provider,object_key) WHERE completed_at IS NULL DO NOTHING`, objectKey)
	return e
}
func (r *postgresRepository) ConfirmPayment(ctx context.Context, in repository.PaymentMutationInput) (*domain.Payment, []string, error) {
	return r.finishPayment(ctx, in, true)
}
func (r *postgresRepository) RejectPayment(ctx context.Context, in repository.PaymentMutationInput) (*domain.Payment, []string, error) {
	return r.finishPayment(ctx, in, false)
}
func (r *postgresRepository) finishPayment(ctx context.Context, in repository.PaymentMutationInput, confirm bool) (*domain.Payment, []string, error) {
	gid, e := uuid.Parse(in.GroupID)
	if e != nil {
		return nil, nil, domain.ErrGroupNotFound
	}
	uid, e := uuid.Parse(in.CallerUserID)
	if e != nil {
		return nil, nil, domain.ErrGroupNotFound
	}
	pid, e := uuid.Parse(in.PaymentID)
	if e != nil {
		return nil, nil, domain.ErrPaymentNotFound
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, nil, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM groups WHERE id=$1 FOR UPDATE`, gid); e != nil {
		return nil, nil, e
	}
	var memberID uuid.UUID
	if e = tx.QueryRow(ctx, `SELECT id FROM group_members WHERE group_id=$1 AND user_id=$2 AND status='active'`, gid, uid).Scan(&memberID); errors.Is(e, pgx.ErrNoRows) {
		return nil, nil, domain.ErrGroupNotFound
	} else if e != nil {
		return nil, nil, e
	}
	op := "reject_payment"
	if confirm {
		op = "confirm_payment"
	}
	keyHash := hashString(in.IdempotencyKey)
	replay, done, e := beginIdempotency(ctx, tx, uid, op, keyHash, in.RequestHash)
	if e != nil {
		return nil, nil, e
	}
	if done {
		if e = tx.Commit(ctx); e != nil {
			return nil, nil, e
		}
		return replay, replay.CoveredDebtIDs, nil
	}
	rows, e := tx.Query(ctx, `SELECT d.id,d.status::text,d.payment_id FROM payment_debts pd JOIN debts d ON d.id=pd.debt_id WHERE pd.payment_id=$1 ORDER BY d.id FOR UPDATE`, pid)
	if e != nil {
		return nil, nil, e
	}
	var ids []uuid.UUID
	valid := true
	for rows.Next() {
		var id uuid.UUID
		var status string
		var active pgtype.UUID
		if e = rows.Scan(&id, &status, &active); e != nil {
			rows.Close()
			return nil, nil, e
		}
		ids = append(ids, id)
		valid = valid && status == "pending_confirmation" && active.Valid && uuid.UUID(active.Bytes) == pid
	}
	rows.Close()
	if len(ids) == 0 || !valid {
		return nil, nil, domain.ErrPaymentNotPendingConfirmation
	}
	if _, e = tx.Exec(ctx, `SELECT id FROM payments WHERE id=$1 AND group_id=$2 FOR UPDATE`, pid, gid); e != nil {
		return nil, nil, e
	}
	payment, e := loadPaymentTx(ctx, tx, pid, gid)
	if e != nil {
		return nil, nil, e
	}
	if payment.CreditorMemberID != memberID.String() {
		return nil, nil, domain.ErrForbidden
	}
	if payment.Status != domain.PaymentPendingConfirmation {
		return nil, nil, domain.ErrPaymentNotPendingConfirmation
	}
	var debtorUser uuid.UUID
	if e = tx.QueryRow(ctx, `SELECT user_id FROM group_members WHERE id=$1`, uuid.MustParse(payment.DebtorMemberID)).Scan(&debtorUser); e != nil {
		return nil, nil, e
	}
	var activity string
	var metadata []byte
	if confirm {
		_, e = tx.Exec(ctx, `UPDATE payments SET status='confirmed',confirmed_at=now(),updated_at=now() WHERE id=$1`, pid)
		if e == nil {
			var tag pgconn.CommandTag
			tag, e = tx.Exec(ctx, `UPDATE debts SET status='settled',settled_at=now(),updated_at=now() WHERE id=ANY($1::uuid[]) AND status='pending_confirmation' AND payment_id=$2`, ids, pid)
			if e == nil && tag.RowsAffected() != int64(len(ids)) {
				e = domain.ErrPaymentNotPendingConfirmation
			}
		}
		activity = "payment_confirmed"
		metadata, _ = json.Marshal(map[string]any{"payment_id": pid, "debtor_member_id": payment.DebtorMemberID, "amount": payment.Amount, "settled_debt_count": len(ids)})
	} else {
		reason := ""
		if in.Reason != nil {
			reason = *in.Reason
		}
		_, e = tx.Exec(ctx, `UPDATE payments SET status='rejected',rejected_at=now(),rejection_reason=$2,updated_at=now() WHERE id=$1`, pid, reason)
		if e == nil {
			var tag pgconn.CommandTag
			tag, e = tx.Exec(ctx, `UPDATE debts SET status='awaiting',payment_id=NULL,settled_at=NULL,updated_at=now() WHERE id=ANY($1::uuid[]) AND status='pending_confirmation' AND payment_id=$2`, ids, pid)
			if e == nil && tag.RowsAffected() != int64(len(ids)) {
				e = domain.ErrPaymentNotPendingConfirmation
			}
		}
		activity = "payment_rejected"
		metadata, _ = json.Marshal(map[string]any{"payment_id": pid, "debtor_member_id": payment.DebtorMemberID, "amount": payment.Amount, "rejection_reason": reason})
	}
	if e != nil {
		return nil, nil, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO group_activities(group_id,actor_member_id,actor_kind,action_type,description,metadata) VALUES($1,$2,'member',$3,$4,$5)`, gid, memberID, activity, "Payment "+strings.TrimPrefix(activity, "payment_"), metadata)
	if e != nil {
		return nil, nil, e
	}
	if in.BeforeCommit != nil {
		if e = in.BeforeCommit(ctx, tx, []string{debtorUser.String()}); e != nil {
			return nil, nil, e
		}
	}
	payment, e = loadPaymentTx(ctx, tx, pid, gid)
	if e != nil {
		return nil, nil, e
	}
	if e = completeIdempotency(ctx, tx, uid, op, keyHash, httpStatusOK, payment); e != nil {
		return nil, nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, nil, e
	}
	return payment, uuidStrings(ids), nil
}
func (r *postgresRepository) RemindDebt(ctx context.Context, in repository.RemindInput) (*domain.ReminderResult, error) {
	gid, e := uuid.Parse(in.GroupID)
	if e != nil {
		return nil, domain.ErrGroupNotFound
	}
	uid, e := uuid.Parse(in.CallerUserID)
	if e != nil {
		return nil, domain.ErrGroupNotFound
	}
	did, e := uuid.Parse(in.DebtID)
	if e != nil {
		return nil, domain.ErrDebtNotFound
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM groups WHERE id=$1 FOR UPDATE`, gid); e != nil {
		return nil, e
	}
	var memberID uuid.UUID
	var role string
	if e = tx.QueryRow(ctx, `SELECT id,role::text FROM group_members WHERE group_id=$1 AND user_id=$2 AND status='active'`, gid, uid).Scan(&memberID, &role); errors.Is(e, pgx.ErrNoRows) {
		return nil, domain.ErrGroupNotFound
	} else if e != nil {
		return nil, e
	}
	keyHash := hashString(in.IdempotencyKey)
	var storedHash, state string
	var body []byte
	tag, e := tx.Exec(ctx, `INSERT INTO payment_idempotency_keys(actor_user_id,operation,key_hash,canonical_request_hash,operation_id) VALUES($1,'remind_debt',$2,$3,$4) ON CONFLICT DO NOTHING`, uid, keyHash, in.RequestHash, uuid.Must(uuid.NewV7()))
	if e != nil {
		return nil, e
	}
	if tag.RowsAffected() == 0 {
		e = tx.QueryRow(ctx, `SELECT canonical_request_hash,state::text,response_body FROM payment_idempotency_keys WHERE actor_user_id=$1 AND operation='remind_debt' AND key_hash=$2 FOR UPDATE`, uid, keyHash).Scan(&storedHash, &state, &body)
		if e != nil {
			return nil, e
		}
		if storedHash != in.RequestHash {
			return nil, domain.ErrIdempotencyConflict
		}
		if state == "in_progress" {
			return nil, domain.ErrIdempotencyInProgress
		}
		var replay domain.ReminderResult
		if e = json.Unmarshal(body, &replay); e != nil {
			return nil, e
		}
		if e = tx.Commit(ctx); e != nil {
			return nil, e
		}
		return &replay, nil
	}
	var creditor, debtor uuid.UUID
	var amount int64
	var status string
	var count int32
	var last pgtype.Timestamptz
	e = tx.QueryRow(ctx, `SELECT creditor_member_id,debtor_member_id,amount,status::text,reminder_count,last_reminded_at FROM debts WHERE id=$1 AND group_id=$2 FOR UPDATE`, did, gid).Scan(&creditor, &debtor, &amount, &status, &count, &last)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, domain.ErrDebtNotFound
	} else if e != nil {
		return nil, e
	}
	if creditor != memberID && role != "captain" {
		return nil, domain.ErrForbidden
	}
	if status != "awaiting" {
		return nil, domain.ErrDebtNotAwaiting
	}
	if count >= 3 || last.Valid && last.Time.After(time.Now().Add(-24*time.Hour)) {
		return nil, domain.ErrReminderRateLimited
	}
	var remindedAt time.Time
	e = tx.QueryRow(ctx, `UPDATE debts SET reminder_count=reminder_count+1,last_reminded_at=now(),updated_at=now() WHERE id=$1 RETURNING reminder_count,last_reminded_at`, did).Scan(&count, &remindedAt)
	if e != nil {
		return nil, e
	}
	metadata, _ := json.Marshal(map[string]any{"debt_id": did, "debtor_member_id": debtor, "amount": amount, "reminder_count": count})
	_, e = tx.Exec(ctx, `INSERT INTO group_activities(group_id,actor_member_id,actor_kind,action_type,description,metadata) VALUES($1,$2,'member','debt_reminded','Debt reminder sent',$3)`, gid, memberID, metadata)
	if e != nil {
		return nil, e
	}
	var debtorUser uuid.UUID
	if e = tx.QueryRow(ctx, `SELECT user_id FROM group_members WHERE id=$1`, debtor).Scan(&debtorUser); e != nil {
		return nil, e
	}
	if in.BeforeCommit != nil {
		if e = in.BeforeCommit(ctx, tx, []string{debtorUser.String()}); e != nil {
			return nil, e
		}
	}
	result := &domain.ReminderResult{DebtID: did.String(), ReminderCount: count, RemindedAt: remindedAt}
	body, e = json.Marshal(result)
	if e != nil {
		return nil, e
	}
	_, e = tx.Exec(ctx, `UPDATE payment_idempotency_keys SET state='completed',response_code=200,response_body=$3 WHERE actor_user_id=$1 AND operation='remind_debt' AND key_hash=$2`, uid, keyHash, body)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return result, nil
}

func (r *postgresRepository) ProcessAutomatedReminders(ctx context.Context, staleBefore time.Time, maxCount int, before func(context.Context, repository.Executor, []string) error) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, `SELECT d.id,d.group_id,d.debtor_member_id,d.amount,d.reminder_count,gm.user_id FROM debts d JOIN group_members gm ON gm.id=d.debtor_member_id WHERE d.status='awaiting' AND d.created_at<=$1 AND d.reminder_count<$2 AND (d.last_reminded_at IS NULL OR d.last_reminded_at<=now()-interval '24 hours') ORDER BY d.id FOR UPDATE OF d SKIP LOCKED LIMIT 100`, staleBefore, maxCount)
	if e != nil {
		return e
	}
	type candidate struct {
		id, group, debtor, user uuid.UUID
		amount                  int64
		count                   int32
	}
	var items []candidate
	for rows.Next() {
		var c candidate
		if e = rows.Scan(&c.id, &c.group, &c.debtor, &c.amount, &c.count, &c.user); e != nil {
			rows.Close()
			return e
		}
		items = append(items, c)
	}
	rows.Close()
	targets := make([]string, 0, len(items))
	for _, c := range items {
		c.count++
		if _, e = tx.Exec(ctx, `UPDATE debts SET reminder_count=$2,last_reminded_at=now(),updated_at=now() WHERE id=$1`, c.id, c.count); e != nil {
			return e
		}
		metadata, _ := json.Marshal(map[string]any{"debt_id": c.id, "debtor_member_id": c.debtor, "amount": c.amount, "reminder_count": c.count})
		if _, e = tx.Exec(ctx, `INSERT INTO group_activities(group_id,actor_member_id,actor_kind,action_type,description,metadata) VALUES($1,NULL,'system','debt_reminded','Automated debt reminder sent',$2)`, c.group, metadata); e != nil {
			return e
		}
		targets = append(targets, c.user.String())
	}
	if before != nil && len(targets) > 0 {
		if e = before(ctx, tx, targets); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (r *postgresRepository) ProcessStalledPayments(ctx context.Context, submittedBefore time.Time, before func(context.Context, repository.Executor, []string) error) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, `SELECT p.id,p.group_id,p.creditor_member_id,p.debtor_member_id,p.submitted_at,gm.user_id FROM payments p JOIN group_members gm ON gm.id=p.creditor_member_id WHERE p.status='pending_confirmation' AND p.submitted_at<=$1 AND p.stalled_alerted_at IS NULL ORDER BY p.id FOR UPDATE OF p SKIP LOCKED LIMIT 100`, submittedBefore)
	if e != nil {
		return e
	}
	type candidate struct {
		id, group, creditor, debtor, user uuid.UUID
		submitted                         time.Time
	}
	var items []candidate
	for rows.Next() {
		var c candidate
		if e = rows.Scan(&c.id, &c.group, &c.creditor, &c.debtor, &c.submitted, &c.user); e != nil {
			rows.Close()
			return e
		}
		items = append(items, c)
	}
	rows.Close()
	targets := make([]string, 0, len(items))
	for _, c := range items {
		tag, e := tx.Exec(ctx, `UPDATE payments SET stalled_alerted_at=now(),updated_at=now() WHERE id=$1 AND stalled_alerted_at IS NULL`, c.id)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		hours := int64(time.Since(c.submitted).Hours())
		metadata, _ := json.Marshal(map[string]any{"payment_id": c.id, "creditor_member_id": c.creditor, "debtor_member_id": c.debtor, "hours_pending": hours})
		if _, e = tx.Exec(ctx, `INSERT INTO group_activities(group_id,actor_member_id,actor_kind,action_type,description,metadata) VALUES($1,NULL,'system','payment_stalled_confirmation','Payment confirmation is stalled',$2)`, c.group, metadata); e != nil {
			return e
		}
		targets = append(targets, c.user.String())
	}
	if before != nil && len(targets) > 0 {
		if e = before(ctx, tx, targets); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

func (r *postgresRepository) DeleteExpiredIdempotency(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM payment_idempotency_keys WHERE expires_at <= now()`)
	return err
}

func (r *postgresRepository) ProcessMediaCleanup(ctx context.Context, deleteObject func(context.Context, string) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `WITH due AS (
		SELECT id FROM media_cleanup_jobs
		WHERE completed_at IS NULL AND attempt_count < 10 AND next_attempt_at <= now()
		ORDER BY next_attempt_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 100
	)
	UPDATE media_cleanup_jobs j
	SET attempt_count=j.attempt_count+1, next_attempt_at=now()+interval '5 minutes'
	FROM due
	WHERE j.id=due.id
	RETURNING j.id,j.object_key`)
	if err != nil {
		return err
	}
	type cleanupJob struct {
		id  uuid.UUID
		key string
	}
	var jobs []cleanupJob
	for rows.Next() {
		var job cleanupJob
		if err = rows.Scan(&job.id, &job.key); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, job)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	for _, job := range jobs {
		if err = deleteObject(ctx, job.key); err != nil {
			_, _ = r.pool.Exec(ctx, `UPDATE media_cleanup_jobs SET last_error_code=$2,updated_at=now() WHERE id=$1 AND completed_at IS NULL`, job.id, err.Error())
			continue
		}
		if _, err = r.pool.Exec(ctx, `UPDATE media_cleanup_jobs SET completed_at=now(),last_error_code=NULL,updated_at=now() WHERE id=$1`, job.id); err != nil {
			return err
		}
	}
	return nil
}
