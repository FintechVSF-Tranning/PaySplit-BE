package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"paysplit-backend/internal/modules/sepay/domain"
	"paysplit-backend/internal/modules/sepay/repository"
	dbgen "paysplit-backend/internal/modules/sepay/repository/postgres/sqlc"
)

type postgresRepository struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) repository.Repository {
	if db == nil {
		panic("sepay repository pool must not be nil")
	}
	return &postgresRepository{db: db}
}

func (r *postgresRepository) SaveTransaction(ctx context.Context, tx domain.Transaction) (bool, error) {
	rows, err := dbgen.New(r.db).InsertTransaction(ctx, dbgen.InsertTransactionParams{
		ID:               tx.ID,
		Gateway:          tx.Gateway,
		TransactionDate:  pgtype.Timestamptz{Time: tx.TransactionDate, Valid: true},
		AccountNumber:    tx.AccountNumber,
		SubAccount:       text(tx.SubAccount),
		Code:             text(tx.Code),
		Content:          tx.Content,
		TransferType:     tx.TransferType,
		TransferAmount:   tx.TransferAmount,
		Accumulated:      tx.Accumulated,
		ReferenceCode:    text(tx.ReferenceCode),
		Description:      tx.Description,
		PaymentReference: text(tx.PaymentReference),
		RawPayload:       tx.RawPayload,
	})
	if err != nil {
		return false, fmt.Errorf("insert sepay transaction %d: %w", tx.ID, err)
	}
	return rows == 1, nil
}

func (r *postgresRepository) MatchStatus(ctx context.Context, id int64) (*string, *string, error) {
	row, err := dbgen.New(r.db).GetTransactionMatchStatus(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("load sepay match status %d: %w", id, err)
	}
	if !row.MatchStatus.Valid {
		return nil, nil, nil
	}
	var paymentID *string
	if row.PaymentID.Valid {
		v := uuid.UUID(row.PaymentID.Bytes).String()
		paymentID = &v
	}
	return &row.MatchStatus.String, paymentID, nil
}

func (r *postgresRepository) MarkProcessed(ctx context.Context, id int64, status string, paymentID *string) error {
	var pid pgtype.UUID
	if paymentID != nil {
		parsed, err := uuid.Parse(*paymentID)
		if err != nil {
			return fmt.Errorf("mark sepay transaction %d: invalid payment id: %w", id, err)
		}
		pid = pgtype.UUID{Bytes: parsed, Valid: true}
	}
	if _, err := dbgen.New(r.db).MarkTransactionProcessed(ctx, dbgen.MarkTransactionProcessedParams{
		ID: id, MatchStatus: pgtype.Text{String: status, Valid: true}, PaymentID: pid,
	}); err != nil {
		return fmt.Errorf("mark sepay transaction %d: %w", id, err)
	}
	return nil
}

func text(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}
