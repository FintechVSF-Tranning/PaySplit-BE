package domain

import (
	"errors"
	"time"
)

const (
	TransferIn  = "in"
	TransferOut = "out"
)

var (
	ErrInvalidInput = errors.New("invalid sepay transaction")
)

// Transaction là một biến động số dư SePay báo về qua webhook.
type Transaction struct {
	ID              int64
	Gateway         string
	TransactionDate time.Time
	AccountNumber   string
	SubAccount      *string
	Code            *string
	Content         string
	TransferType    string
	TransferAmount  int64
	Accumulated     int64
	ReferenceCode   *string
	Description     string
	// PaymentReference là mã PAYxxxxxxxx của PaySplit trích từ Code/Content,
	// nil khi giao dịch không mang mã nào (ví dụ chuyển khoản không qua QR).
	PaymentReference *string
	RawPayload       []byte
}

// Trạng thái đối soát do chính module SePay gán; các trạng thái còn lại đến từ
// settlement (confirmed, amount_mismatch, account_mismatch...).
const (
	MatchIgnoredOutgoing = "ignored_outgoing" // Tiền ra, không đối soát
	MatchNoReference     = "no_reference"     // Nội dung không có mã PAYxxxxxxxx
	MatchZeroAmount      = "zero_amount"      // Tiền vào 0đ, không có gì để gạch
	MatchTestDelivery    = "test_delivery"    // Nút "Gửi thử" của SePay (id=0), không lưu
)

// SettleRequest là phần của giao dịch mà bên gạch nợ cần.
type SettleRequest struct {
	TransactionID  int64
	ReferenceCode  string
	Amount         int64
	AccountNumbers []string
}

type SettleResult struct {
	Outcome   string
	PaymentID *string
}
