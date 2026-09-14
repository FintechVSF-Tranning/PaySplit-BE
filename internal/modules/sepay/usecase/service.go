package usecase

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"paysplit-backend/internal/modules/sepay/domain"
	"paysplit-backend/internal/modules/sepay/repository"
)

// sepayDateLayouts: định dạng tài liệu SePay mô tả ("2023-03-25 14:02:37") đứng
// đầu, các định dạng sau phòng dữ liệu gửi thử hoặc phiên bản API khác.
var sepayDateLayouts = []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", time.RFC3339, "02/01/2006 15:04:05", "2006-01-02"}

// vietnamTime cố định UTC+7 thay vì LoadLocation: Việt Nam không có giờ mùa hè
// và image runtime có thể không kèm tzdata.
var vietnamTime = time.FixedZone("ICT", 7*60*60)

// paymentReferencePattern khớp mã do settlement sinh ra: "PAY" + 8 ký tự từ
// bảng chữ không có I, O, 0, 1. Ngân hàng có thể đổi hoa/thường nội dung CK.
var paymentReferencePattern = regexp.MustCompile(`PAY[A-HJ-NP-Z2-9]{8}`)

// PaymentSettler gạch nợ theo giao dịch tiền vào (cài đặt ở settlement).
type PaymentSettler interface {
	Settle(context.Context, domain.SettleRequest) (domain.SettleResult, error)
}

type Service struct {
	repo    repository.Repository
	settler PaymentSettler
}

func NewService(repo repository.Repository, settler PaymentSettler) *Service {
	if repo == nil {
		panic("sepay service repository must not be nil")
	}
	if settler == nil {
		panic("sepay service settler must not be nil")
	}
	return &Service{repo: repo, settler: settler}
}

// ReceiveInput giữ nguyên các trường thô của webhook; Service chịu trách nhiệm
// kiểm tra và chuẩn hóa để handler không phải biết quy tắc nghiệp vụ.
type ReceiveInput struct {
	ID              int64
	Gateway         string
	TransactionDate string
	AccountNumber   string
	SubAccount      *string
	Code            *string
	Content         string
	TransferType    string
	TransferAmount  int64
	Accumulated     int64
	ReferenceCode   *string
	Description     string
	RawPayload      []byte
}

type ReceiveResult struct {
	Transaction domain.Transaction
	Duplicate   bool
	MatchStatus string
	PaymentID   *string
}

// Receive lưu giao dịch rồi đối soát. Lỗi ở bước đối soát được trả ra để
// handler trả 500 và SePay gửi lại; match_status còn NULL nên lần sau xử lý tiếp.
func (s *Service) Receive(ctx context.Context, in ReceiveInput) (ReceiveResult, error) {
	// Nút "Gửi thử" của SePay gửi id=0 (giao dịch thật luôn có id dương). Trả
	// thành công để bước kiểm tra kết nối qua được, nhưng không lưu: id=0 không
	// chống trùng được và đây không phải biến động số dư thật.
	if in.ID == 0 {
		return ReceiveResult{MatchStatus: domain.MatchTestDelivery}, nil
	}
	tx, err := normalize(in)
	if err != nil {
		return ReceiveResult{}, err
	}
	inserted, err := s.repo.SaveTransaction(ctx, tx)
	if err != nil {
		return ReceiveResult{}, fmt.Errorf("save sepay transaction: %w", err)
	}
	result := ReceiveResult{Transaction: tx, Duplicate: !inserted}
	if !inserted {
		status, paymentID, err := s.repo.MatchStatus(ctx, tx.ID)
		if err != nil {
			return ReceiveResult{}, err
		}
		if status != nil {
			result.MatchStatus, result.PaymentID = *status, paymentID
			return result, nil
		}
	}

	settled, err := s.match(ctx, tx)
	if err != nil {
		return ReceiveResult{}, fmt.Errorf("settle sepay transaction %d: %w", tx.ID, err)
	}
	if err = s.repo.MarkProcessed(ctx, tx.ID, settled.Outcome, settled.PaymentID); err != nil {
		return ReceiveResult{}, err
	}
	result.MatchStatus, result.PaymentID = settled.Outcome, settled.PaymentID
	return result, nil
}

func (s *Service) match(ctx context.Context, tx domain.Transaction) (domain.SettleResult, error) {
	if tx.TransferType != domain.TransferIn {
		return domain.SettleResult{Outcome: domain.MatchIgnoredOutgoing}, nil
	}
	if tx.PaymentReference == nil {
		return domain.SettleResult{Outcome: domain.MatchNoReference}, nil
	}
	if tx.TransferAmount <= 0 {
		return domain.SettleResult{Outcome: domain.MatchZeroAmount}, nil
	}
	accounts := []string{tx.AccountNumber}
	if tx.SubAccount != nil {
		accounts = append(accounts, *tx.SubAccount)
	}
	return s.settler.Settle(ctx, domain.SettleRequest{
		TransactionID:  tx.ID,
		ReferenceCode:  *tx.PaymentReference,
		Amount:         tx.TransferAmount,
		AccountNumbers: accounts,
	})
}

// normalize chỉ bắt buộc những gì cần để lưu và chống trùng (id, loại, số
// tiền). Thiếu gateway/tài khoản vẫn lưu được: đối soát sẽ ra account_mismatch
// nên không thể gạch nợ nhầm.
func normalize(in ReceiveInput) (domain.Transaction, error) {
	transferType := strings.ToLower(strings.TrimSpace(in.TransferType))
	switch {
	case in.ID <= 0:
		return domain.Transaction{}, fmt.Errorf("%w: id must be positive, got %d", domain.ErrInvalidInput, in.ID)
	case transferType != domain.TransferIn && transferType != domain.TransferOut:
		return domain.Transaction{}, fmt.Errorf("%w: transferType must be in/out, got %q", domain.ErrInvalidInput, in.TransferType)
	case in.TransferAmount < 0:
		return domain.Transaction{}, fmt.Errorf("%w: transferAmount must not be negative, got %d", domain.ErrInvalidInput, in.TransferAmount)
	case len(in.RawPayload) == 0:
		return domain.Transaction{}, fmt.Errorf("%w: empty payload", domain.ErrInvalidInput)
	}
	date, err := parseTransactionDate(in.TransactionDate)
	if err != nil {
		return domain.Transaction{}, err
	}
	return domain.Transaction{
		ID:               in.ID,
		Gateway:          strings.TrimSpace(in.Gateway),
		TransactionDate:  date,
		AccountNumber:    strings.TrimSpace(in.AccountNumber),
		SubAccount:       optional(in.SubAccount),
		Code:             optional(in.Code),
		Content:          in.Content,
		TransferType:     transferType,
		TransferAmount:   in.TransferAmount,
		Accumulated:      in.Accumulated,
		ReferenceCode:    optional(in.ReferenceCode),
		Description:      in.Description,
		PaymentReference: extractPaymentReference(in.Code, in.Content),
		RawPayload:       in.RawPayload,
	}, nil
}

// parseTransactionDate hiểu giờ không kèm múi giờ là giờ Việt Nam. Thiếu hẳn
// ngày thì lấy thời điểm nhận: đây chỉ là thông tin tham khảo, không dùng để đối soát.
func parseTransactionDate(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now(), nil
	}
	for _, layout := range sepayDateLayouts {
		if date, err := time.ParseInLocation(layout, raw, vietnamTime); err == nil {
			return date, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: unrecognized transactionDate %q", domain.ErrInvalidInput, raw)
}

// extractPaymentReference ưu tiên trường code (SePay đã tự nhận diện theo tiền
// tố cấu hình), sau đó mới quét nội dung chuyển khoản.
func extractPaymentReference(code *string, content string) *string {
	for _, candidate := range []string{deref(code), content} {
		if match := paymentReferencePattern.FindString(strings.ToUpper(candidate)); match != "" {
			return &match
		}
	}
	return nil
}

func optional(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
