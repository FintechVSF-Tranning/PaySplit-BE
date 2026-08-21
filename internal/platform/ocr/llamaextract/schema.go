package llamaextract

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// RawReceiptPayload represents the direct unmarshaled structure returned from LlamaExtract OCR.
type RawReceiptPayload struct {
	MerchantName  *string          `json:"merchant_name"`
	BillDate      *string          `json:"bill_date"`
	Subtotal      *float64         `json:"subtotal"`
	ServiceCharge *float64         `json:"service_charge"`
	VAT           *float64         `json:"vat"`
	Discount      *float64         `json:"discount"`
	Total         *float64         `json:"total"`
	Items         []RawItemPayload `json:"items"`
}

// RawItemPayload represents a raw item line extracted by the OCR model.
type RawItemPayload struct {
	Name      string        `json:"name"`
	Quantity  *FlexQuantity `json:"quantity"`
	UnitPrice *float64      `json:"unit_price"`
	LineTotal *float64      `json:"line_total"`
}

// FlexQuantity allows parsing quantity whether it arrives as a string ("1", "0,950", "2.5"), float (1.5), int (2), or null.
type FlexQuantity struct {
	Value float64
	Valid bool
}

// UnmarshalJSON customizes deserialization for flexible quantity formats.
func (fq *FlexQuantity) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || len(data) == 0 {
		fq.Value = 0
		fq.Valid = false
		return nil
	}

	// 1. Try unmarshaling as float/int
	var num float64
	if err := json.Unmarshal(data, &num); err == nil {
		fq.Value = num
		fq.Valid = true
		return nil
	}

	// 2. Try unmarshaling as string
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		str = strings.TrimSpace(str)
		if str == "" || str == "null" {
			fq.Value = 0
			fq.Valid = false
			return nil
		}

		// Replace Vietnamese comma decimal with dot: "0,950" -> "0.950"
		normalized := strings.ReplaceAll(str, ",", ".")
		// Strip non-numeric suffix if present, e.g. "0.950 kg" -> "0.950"
		fields := strings.Fields(normalized)
		if len(fields) > 0 {
			normalized = fields[0]
		}

		parsed, err := strconv.ParseFloat(normalized, 64)
		if err != nil {
			return fmt.Errorf("invalid quantity string %q: %w", str, err)
		}
		fq.Value = parsed
		fq.Valid = true
		return nil
	}

	return fmt.Errorf("cannot unmarshal %s into FlexQuantity", string(data))
}

// ReceiptCandidate represents the normalized, validated candidate result ready to be stored in ocr_jobs.candidate.
type ReceiptCandidate struct {
	MerchantName      *string         `json:"merchant_name"`
	BillDate          *string         `json:"bill_date"` // ISO YYYY-MM-DD or DD/MM/YYYY
	Subtotal          int64           `json:"subtotal"`  // Gross items sum (VND)
	TotalItemDiscount int64           `json:"total_item_discount"` // Sum of item-specific discounts (VND)
	GeneralDiscount   int64           `json:"general_discount"`   // Bill-wide voucher discount (VND)
	TotalDiscount     int64           `json:"total_discount"`     // TotalItemDiscount + GeneralDiscount (VND)
	ServiceCharge     int64           `json:"service_charge"`     // Service fee (VND)
	VAT               int64           `json:"vat"`                // VAT (VND)
	ReportedTotal     int64           `json:"reported_total"`     // Total from OCR bill (VND)
	ComputedTotal     int64           `json:"computed_total"`     // Subtotal - TotalDiscount + ServiceCharge + VAT (VND)
	MismatchWarning   bool            `json:"mismatch_warning"`
	MismatchDelta     int64           `json:"mismatch_delta"` // ComputedTotal - ReportedTotal
	Warnings          []string        `json:"warnings,omitempty"`
	Items             []ItemCandidate `json:"items"`
}

// ItemCandidate represents a normalized line item ready for candidate review and bill creation.
type ItemCandidate struct {
	Position       int16   `json:"position"`
	Name           string  `json:"name"`
	Quantity       float64 `json:"quantity"`
	UnitPrice      int64   `json:"unit_price"`      // Unit price before discount (VND)
	LineTotal      int64   `json:"line_total"`      // Gross total = Quantity × UnitPrice (VND)
	DiscountAmount int64   `json:"discount_amount"` // Item-specific discount (VND)
	FinalPrice     int64   `json:"final_price"`     // Net price = LineTotal - DiscountAmount (VND)
}
// Schema cũ khi chưa có 0004. Item Discount OCR Parsing and Mapping
// // ReceiptSchema trả về cấu trúc JSON Schema định nghĩa dữ liệu cần trích xuất từ hóa đơn
// // gửi cho LlamaExtract API v2 (Spec 3 AC-3).
// func ReceiptSchema() map[string]any {
// 	return map[string]any{
// 		"type": "object",
// 		"properties": map[string]any{
// 			"merchant_name": map[string]any{
// 				"type":        "string",
// 				"description": "Tên nhà hàng, quán ăn, cửa hàng hoặc đơn vị xuất hóa đơn (nếu có)",
// 			},
// 			"bill_date": map[string]any{
// 				"type":        "string",
// 				"description": "Ngày lập hóa đơn theo định dạng YYYY-MM-DD hoặc DD/MM/YYYY",
// 			},
// 			"items": map[string]any{
// 				"type":        "array",
// 				"description": "Danh sách các món ăn, đồ uống hoặc dịch vụ trên hóa đơn",
// 				"items": map[string]any{
// 					"type": "object",
// 					"properties": map[string]any{
// 						"name": map[string]any{
// 							"type":        "string",
// 							"description": "Tên món hoặc sản phẩm",
// 						},
// 						"quantity": map[string]any{
// 							"type":        "string",
// 							"description": "Số lượng món (ví dụ: '1', '2', '0.5')",
// 						},
// 						"unit_price": map[string]any{
// 							"type":        "integer",
// 							"description": "Đơn giá bằng số nguyên VND",
// 						},
// 						"line_total": map[string]any{
// 							"type":        "integer",
// 							"description": "Thành tiền bằng số nguyên VND",
// 						},
// 					},
// 					"required": []string{"name", "line_total"},
// 				},
// 			},
// 			"subtotal": map[string]any{
// 				"type":        "integer",
// 				"description": "Tổng tiền hàng trước thuế phí hoặc giảm giá (VND)",
// 			},
// 			"service_charge": map[string]any{
// 				"type":        "integer",
// 				"description": "Phí dịch vụ nếu có ghi trên hóa đơn (VND), mặc định 0",
// 			},
// 			"vat": map[string]any{
// 				"type":        "integer",
// 				"description": "Tiền thuế GTGT / VAT nếu có ghi trên hóa đơn (VND), mặc định 0",
// 			},
// 			"discount": map[string]any{
// 				"type":        "integer",
// 				"description": "Tiền giảm giá, voucher, chiết khấu nếu có (VND), mặc định 0",
// 			},
// 			"total": map[string]any{
// 				"type":        "integer",
// 				"description": "Tổng số tiền thanh toán cuối cùng ghi trên hóa đơn (VND)",
// 			},
// 		},
// 		"required": []string{"items", "total"},
// 	}
// }