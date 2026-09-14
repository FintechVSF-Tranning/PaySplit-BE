package integration

import (
	"context"

	sepaydomain "paysplit-backend/internal/modules/sepay/domain"
	settlementdomain "paysplit-backend/internal/modules/settlement/domain"
)

type BankTransferSettler interface {
	SettleBankTransfer(context.Context, settlementdomain.BankTransfer) (settlementdomain.BankMatchResult, error)
}

// Settler chuyển giao dịch SePay sang settlement để gạch nợ, giữ module SePay
// không phụ thuộc trực tiếp vào kiểu dữ liệu của settlement.
type Settler struct {
	settlement BankTransferSettler
}

func NewSettler(settlement BankTransferSettler) *Settler {
	if settlement == nil {
		panic("sepay settler settlement service must not be nil")
	}
	return &Settler{settlement: settlement}
}

func (s *Settler) Settle(ctx context.Context, req sepaydomain.SettleRequest) (sepaydomain.SettleResult, error) {
	res, err := s.settlement.SettleBankTransfer(ctx, settlementdomain.BankTransfer{
		TransactionID:  req.TransactionID,
		ReferenceCode:  req.ReferenceCode,
		Amount:         req.Amount,
		AccountNumbers: req.AccountNumbers,
	})
	if err != nil {
		return sepaydomain.SettleResult{}, err
	}
	return sepaydomain.SettleResult{Outcome: res.Outcome, PaymentID: res.PaymentID}, nil
}
