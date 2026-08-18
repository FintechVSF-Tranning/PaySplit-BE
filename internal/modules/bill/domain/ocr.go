package domain

// Warning codes quy định trong Spec 3 cho kết quả OCR.
const (
	// WarningOCRDateAmbiguous cảnh báo ngày trên hóa đơn không rõ ràng hoặc nhập nhằng giữa ngày và tháng.
	WarningOCRDateAmbiguous = "OCR_DATE_AMBIGUOUS"

	// WarningSubtotalMismatch cảnh báo tổng tiền hàng (subtotal) không khớp với tổng các dòng line_total.
	WarningSubtotalMismatch = "SUBTOTAL_MISMATCH"

	// WarningTotalMismatch cảnh báo tổng thanh toán (total) không khớp với subtotal + service_charge + vat - discount.
	WarningTotalMismatch = "TOTAL_MISMATCH"

	// WarningLowConfidence cảnh báo độ tin cậy của OCR dưới ngưỡng tiêu chuẩn.
	WarningLowConfidence = "LOW_CONFIDENCE"
)

// OCRCandidate đại diện cho bản ghi trích xuất hóa đơn chuẩn hóa từ AI / Vision LLM.
// Dữ liệu này được lưu trữ dạng JSONB trong cột ocr_jobs.candidate và chỉ được áp dụng
// vào hóa đơn khi người dùng thực hiện apply một cách tường minh (Spec 3 AC-3, AC-4).
type OCRCandidate struct {
	MerchantName  *string            `json:"merchant_name,omitempty"`
	BillDate      *string            `json:"bill_date,omitempty"` // Định dạng ISO YYYY-MM-DD
	Items         []OCRCandidateItem `json:"items"`
	Subtotal      int64              `json:"subtotal"`
	ServiceCharge int64              `json:"service_charge"`
	VAT           int64              `json:"vat"`
	Discount      int64              `json:"discount"`
	Total         int64              `json:"total"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Warnings      []string           `json:"warnings"`
}

// OCRCandidateItem đại diện cho một món hoặc dịch vụ được bóc tách từ hóa đơn.
type OCRCandidateItem struct {
	Name      string `json:"name"`
	Quantity  string `json:"quantity"` // Chuỗi thập phân, ví dụ: "1", "2.5"
	UnitPrice int64  `json:"unit_price"`
	LineTotal int64  `json:"line_total"`
}
