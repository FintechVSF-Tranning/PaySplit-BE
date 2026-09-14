package repository

import (
	"context"

	"paysplit-backend/internal/modules/sepay/domain"
)

type Repository interface {
	// SaveTransaction ghi nhận giao dịch; inserted=false khi id đã tồn tại
	// (SePay gửi lại cùng một giao dịch) và không có gì bị ghi đè.
	SaveTransaction(ctx context.Context, tx domain.Transaction) (inserted bool, err error)
	// MatchStatus trả status nil khi giao dịch chưa được đối soát xong.
	MatchStatus(ctx context.Context, id int64) (status *string, paymentID *string, err error)
	MarkProcessed(ctx context.Context, id int64, status string, paymentID *string) error
}
