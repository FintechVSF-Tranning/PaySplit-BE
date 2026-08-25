package llamaextract

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"paysplit-backend/internal/modules/bill/domain"
)

var (
	dateRegexDMY = regexp.MustCompile(`^(\d{1,2})[/\-\.](\d{1,2})[/\-\.](\d{4})$`)
	dateRegexYMD = regexp.MustCompile(`^(\d{4})[/\-\.](\d{1,2})[/\-\.](\d{1,2})$`)
)

// RawExtractResponse đại diện cho cấu trúc JSON thô nhận từ LlamaExtract API.
type RawExtractResponse struct {
	MerchantName  any   `json:"merchant_name"`
	BillDate      any   `json:"bill_date"`
	Items         []any `json:"items"`
	Subtotal      any   `json:"subtotal"`
	ServiceCharge any   `json:"service_charge"`
	VAT           any   `json:"vat"`
	Discount      any   `json:"discount"`
	Total         any   `json:"total"`
	Confidence    any   `json:"confidence"`
}

// Normalize chuyển đổi raw JSON payload từ LlamaExtract thành domain.OCRCandidate chuẩn hóa.
func Normalize(rawBytes []byte) (*domain.OCRCandidate, error) {
	if len(rawBytes) == 0 {
		return nil, domain.ErrOcrSchemaInvalid
	}

	// Trường hợp rawBytes là wrapper payload từ LlamaExtract: {"data": {...}} hoặc trực tiếp {...}
	var rawWrapper struct {
		Data          json.RawMessage `json:"data"`
		ExtractResult json.RawMessage `json:"extract_result"`
		Result        json.RawMessage `json:"result"`
	}

	payloadToParse := rawBytes
	if err := json.Unmarshal(rawBytes, &rawWrapper); err == nil {
		if len(rawWrapper.ExtractResult) > 0 {
			payloadToParse = rawWrapper.ExtractResult
		} else if len(rawWrapper.Data) > 0 {
			payloadToParse = rawWrapper.Data
		} else if len(rawWrapper.Result) > 0 {
			payloadToParse = rawWrapper.Result
		}
	}

	var raw RawExtractResponse
	if err := json.Unmarshal(payloadToParse, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrOcrSchemaInvalid, err)
	}

	var warnings []string

	// 1. Chuẩn hóa merchant_name
	var merchantName *string
	if raw.MerchantName != nil {
		trimmed := strings.TrimSpace(fmt.Sprint(raw.MerchantName))
		if trimmed != "" && trimmed != "<nil>" {
			merchantName = &trimmed
		}
	}

	// 2. Chuẩn hóa bill_date
	var billDate *string
	if raw.BillDate != nil {
		dateStr := strings.TrimSpace(fmt.Sprint(raw.BillDate))
		if dateStr != "" && dateStr != "<nil>" {
			parsedDate, isAmbiguous := ParseDate(dateStr)
			if parsedDate != nil {
				billDate = parsedDate
			}
			if isAmbiguous {
				warnings = append(warnings, domain.WarningOCRDateAmbiguous)
			}
		}
	}

	// 3. Chuẩn hóa items và gộp các dòng khuyến mãi theo món liền trước (Spec 3 AC-15, AC-16)
	items := make([]domain.OCRCandidateItem, 0, len(raw.Items))
	var calculatedSubtotal int64
	var orphanItemDiscount int64     // Dòng khuyến mãi không có món liền trước (Case B)
	var exceededExcessDiscount int64 // Phần giảm giá vượt quá giá gốc của chính món đó
	for _, rawItem := range raw.Items {
		itemMap, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}

		name := strings.TrimSpace(fmt.Sprint(itemMap["name"]))
		if name == "" || name == "<nil>" {
			name = "Món chưa đặt tên"
		}

		rawLineTotal := parseVNDAmountSigned(itemMap["line_total"])
		isPromotionLine := isPromotionMarker(name) ||
			rawLineTotal < 0 ||
			(isNullOrZeroQuantity(itemMap["quantity"]) && rawLineTotal <= 0)

		if isPromotionLine {
			promoValue := absInt64(rawLineTotal)
			if promoValue == 0 {
				// Dòng khuyến mãi không mang giá trị nào để gộp, bỏ qua an toàn.
				continue
			}

			if len(items) == 0 {
				// Không có món liền trước để gộp vào, coi như giảm giá chung (Spec 3 AC-16).
				orphanItemDiscount += promoValue
				warnings = append(warnings, domain.WarningOCROrphanItemDiscount)
				continue
			}

			preceding := &items[len(items)-1]
			preceding.DiscountAmount += promoValue
			if preceding.DiscountAmount > preceding.LineTotal {
				// Giảm giá vượt quá giá gốc của chính món đó: chặn về 0, phần dư chuyển sang
				// giảm giá chung (Spec 3 AC-16).
				excess := preceding.DiscountAmount - preceding.LineTotal
				preceding.DiscountAmount = preceding.LineTotal
				preceding.FinalPrice = 0
				exceededExcessDiscount += excess
				warnings = append(warnings, domain.WarningOCRItemDiscountExceeded)
			} else {
				preceding.FinalPrice = preceding.LineTotal - preceding.DiscountAmount
			}
			continue
		}

		quantity := NormalizeQuantity(itemMap["quantity"])
		unitPrice := ParseVNDAmount(itemMap["unit_price"])
		lineTotal := ParseVNDAmount(itemMap["line_total"])

		// Tự động suy luận line_total hoặc unit_price nếu 1 trong 2 bằng 0
		if lineTotal == 0 && unitPrice > 0 {
			if q, err := strconv.ParseFloat(quantity, 64); err == nil && q > 0 {
				lineTotal = int64(math.Round(float64(unitPrice) * q))
			}
		} else if unitPrice == 0 && lineTotal > 0 {
			if q, err := strconv.ParseFloat(quantity, 64); err == nil && q > 0 {
				unitPrice = int64(math.Round(float64(lineTotal) / q))
			} else {
				unitPrice = lineTotal
			}
		}

		items = append(items, domain.OCRCandidateItem{
			Name:       name,
			Quantity:   quantity,
			UnitPrice:  unitPrice,
			LineTotal:  lineTotal,
			FinalPrice: lineTotal, // Không có khuyến mãi riêng: giá cuối bằng giá gốc
		})

		calculatedSubtotal += lineTotal
		if len(items) >= 100 { // Giới hạn tối đa 100 món theo Spec 3 AC-5
			break
		}
	}

	// 3b. Tách giảm giá theo món ra khỏi giảm giá chung của hóa đơn (Spec 3 AC-17)
	var totalItemDiscount int64
	for _, it := range items {
		totalItemDiscount += it.DiscountAmount
	}

	// 4. Chuẩn hóa các trường tiền tệ
	subtotal := ParseVNDAmount(raw.Subtotal)
	serviceCharge := ParseVNDAmount(raw.ServiceCharge)
	vat := ParseVNDAmount(raw.VAT)
	rawDiscount := ParseVNDAmount(raw.Discount) + orphanItemDiscount
	total := ParseVNDAmount(raw.Total)

	generalDiscount := rawDiscount - totalItemDiscount
	if generalDiscount < 0 {
		generalDiscount = 0
	}
	// Phần vượt trần của một món (đã bị chặn về LineTotal ở trên) chuyển thẳng vào giảm giá chung,
	// độc lập với phép trừ total_item_discount ở trên vì total_item_discount đã tính trên giá trị
	// bị chặn, không phải giá trị gốc trước khi chặn (Spec 3 AC-16).
	generalDiscount += exceededExcessDiscount
	totalDiscount := totalItemDiscount + generalDiscount

	// Nếu subtotal không quét được nhưng có items, gán bằng tổng line_total
	if subtotal == 0 && calculatedSubtotal > 0 {
		subtotal = calculatedSubtotal
	}

	// Nếu total không quét được, gán theo công thức
	if total == 0 && (subtotal > 0 || serviceCharge > 0 || vat > 0) {
		total = subtotal + serviceCharge + vat - totalDiscount
		if total < 0 {
			total = 0
		}
	}

	// 5. Kiểm tra đối soát để sinh Warning (Spec 3 AC-3, AC-5, AC-18)
	if subtotal > 0 && calculatedSubtotal > 0 && subtotal != calculatedSubtotal {
		warnings = append(warnings, domain.WarningSubtotalMismatch)
	}

	expectedTotal := subtotal + serviceCharge + vat - totalDiscount
	if expectedTotal < 0 {
		expectedTotal = 0
	}
	if total > 0 && total != expectedTotal {
		warnings = append(warnings, domain.WarningTotalMismatch)
	}

	// 6. Chuẩn hóa Confidence
	var confidence *float64
	if raw.Confidence != nil {
		if confVal, ok := raw.Confidence.(float64); ok && confVal >= 0 && confVal <= 1.0 {
			confidence = &confVal
			if confVal < 0.70 {
				warnings = append(warnings, domain.WarningLowConfidence)
			}
		}
	}

	return &domain.OCRCandidate{
		MerchantName:      merchantName,
		BillDate:          billDate,
		Items:             items,
		Subtotal:          subtotal,
		TotalItemDiscount: totalItemDiscount,
		GeneralDiscount:   generalDiscount,
		ServiceCharge:     serviceCharge,
		VAT:               vat,
		Discount:          totalDiscount,
		Total:             total,
		Confidence:        confidence,
		Warnings:          warnings,
	}, nil
}

// ParseVNDAmount chuẩn hóa các dạng số và chuỗi tiền tệ VND về int64.
// Hỗ trợ xử lý dấu phân cách hàng nghìn (50.000, 50,000, 50k, 50K, 50 000).
// Giá trị âm luôn được kẹp về 0 (dùng cho các trường tiền không thể âm).
func ParseVNDAmount(val any) int64 {
	n := parseVNDAmountSigned(val)
	if n < 0 {
		return 0
	}
	return n
}

// parseVNDAmountSigned chuẩn hóa tương tự ParseVNDAmount nhưng giữ nguyên dấu âm, dùng để nhận
// diện các dòng khuyến mãi có line_total âm trong hóa đơn thô (Spec 3 AC-15).
func parseVNDAmountSigned(val any) int64 {
	if val == nil {
		return 0
	}

	switch v := val.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0
		}
		return int64(math.Round(v))
	case string:
		s := strings.TrimSpace(v)
		if s == "" || s == "<nil>" {
			return 0
		}

		// Xử lý dạng 50k / 50K
		lower := strings.ToLower(s)
		isThousandK := strings.HasSuffix(lower, "k")
		if isThousandK {
			lower = strings.TrimSuffix(lower, "k")
		}

		// Loại bỏ các ký tự tiền tệ và khoảng trắng: "đ", "vnd", "vnđ", " "
		cleaned := strings.NewReplacer("vnd", "", "vnđ", "", "đ", "", " ", "").Replace(lower)
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			return 0
		}

		dotCount := strings.Count(cleaned, ".")
		commaCount := strings.Count(cleaned, ",")

		if dotCount > 0 && commaCount > 0 {
			// Ví dụ 1,000,000.50 hoặc 1.000.000,50
			lastDot := strings.LastIndex(cleaned, ".")
			lastComma := strings.LastIndex(cleaned, ",")
			if lastComma > lastDot {
				// Dấu chấm là phân cách nghìn, dấu phẩy là thập phân
				cleaned = strings.ReplaceAll(cleaned, ".", "")
				cleaned = strings.ReplaceAll(cleaned, ",", ".")
			} else {
				// Dấu phẩy là phân cách nghìn, dấu chấm là thập phân
				cleaned = strings.ReplaceAll(cleaned, ",", "")
			}
		} else if dotCount > 0 && commaCount == 0 {
			parts := strings.Split(cleaned, ".")
			if len(parts) > 2 {
				// Nhiều dấu chấm (ví dụ 1.250.000) -> phân cách hàng nghìn
				cleaned = strings.ReplaceAll(cleaned, ".", "")
			} else if len(parts) == 2 {
				if len(parts[1]) == 3 {
					// 50.000 -> phân cách hàng nghìn trong VND
					cleaned = strings.ReplaceAll(cleaned, ".", "")
				}
				// Nếu len(parts[1]) != 3 (ví dụ 50.5 hay 12.75) thì giữ nguyên dấu chấm thập phân
			}
		} else if commaCount > 0 && dotCount == 0 {
			parts := strings.Split(cleaned, ",")
			if len(parts) > 2 {
				// Nhiều dấu phẩy (ví dụ 1,250,000) -> phân cách hàng nghìn
				cleaned = strings.ReplaceAll(cleaned, ",", "")
			} else if len(parts) == 2 {
				if len(parts[1]) == 3 {
					// 50,000 -> phân cách hàng nghìn
					cleaned = strings.ReplaceAll(cleaned, ",", "")
				} else {
					// 50,5 -> dấu phẩy thập phân -> đổi sang dấu chấm
					cleaned = strings.ReplaceAll(cleaned, ",", ".")
				}
			}
		}

		f, err := strconv.ParseFloat(cleaned, 64)
		if err != nil {
			return 0
		}

		if isThousandK {
			f *= 1000
		}

		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0
		}

		return int64(math.Round(f))
	default:
		return 0
	}
}

// absInt64 trả về giá trị tuyệt đối của một số nguyên có dấu.
func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// isNullOrZeroQuantity báo giá trị quantity thô là null, rỗng, hoặc bằng 0 (Spec 3 AC-15).
func isNullOrZeroQuantity(val any) bool {
	if val == nil {
		return true
	}
	switch v := val.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" || s == "<nil>" {
			return true
		}
		if f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64); err == nil {
			return f == 0
		}
		return false
	case float64:
		return v == 0
	case int:
		return v == 0
	case int64:
		return v == 0
	default:
		return false
	}
}

// vnDiacriticsFolder loại bỏ dấu tiếng Việt để so khớp nhãn khuyến mãi không phân biệt dấu.
var vnDiacriticsFolder = strings.NewReplacer(
	"à", "a", "á", "a", "ả", "a", "ã", "a", "ạ", "a",
	"ă", "a", "ằ", "a", "ắ", "a", "ẳ", "a", "ẵ", "a", "ặ", "a",
	"â", "a", "ầ", "a", "ấ", "a", "ẩ", "a", "ẫ", "a", "ậ", "a",
	"è", "e", "é", "e", "ẻ", "e", "ẽ", "e", "ẹ", "e",
	"ê", "e", "ề", "e", "ế", "e", "ể", "e", "ễ", "e", "ệ", "e",
	"ì", "i", "í", "i", "ỉ", "i", "ĩ", "i", "ị", "i",
	"ò", "o", "ó", "o", "ỏ", "o", "õ", "o", "ọ", "o",
	"ô", "o", "ồ", "o", "ố", "o", "ổ", "o", "ỗ", "o", "ộ", "o",
	"ơ", "o", "ờ", "o", "ớ", "o", "ở", "o", "ỡ", "o", "ợ", "o",
	"ù", "u", "ú", "u", "ủ", "u", "ũ", "u", "ụ", "u",
	"ư", "u", "ừ", "u", "ứ", "u", "ử", "u", "ữ", "u", "ự", "u",
	"ỳ", "y", "ý", "y", "ỷ", "y", "ỹ", "y", "ỵ", "y",
	"đ", "d",
)

// promotionMarkers là các nhãn khuyến mãi thường gặp trên hóa đơn siêu thị và quán ăn (Spec 3 AC-15).
var promotionMarkers = []string{"km", "khuyen mai", "chiet khau", "giam gia"}

// isPromotionMarker báo tên món có chứa nhãn khuyến mãi, không phân biệt hoa thường và dấu.
func isPromotionMarker(name string) bool {
	folded := vnDiacriticsFolder.Replace(strings.ToLower(name))
	for _, marker := range promotionMarkers {
		if strings.Contains(folded, marker) {
			return true
		}
	}
	return false
}

// NormalizeQuantity chuẩn hóa số lượng món thành chuỗi thập phân chuẩn (ví dụ "1", "2.5").
func NormalizeQuantity(val any) string {
	if val == nil {
		return "1"
	}

	switch v := val.(type) {
	case int, int64:
		return fmt.Sprintf("%d", v)
	case float64:
		if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return "1"
		}
		// Bỏ phần thập phân .0 nếu là số nguyên
		if v == math.Trunc(v) {
			return fmt.Sprintf("%.0f", v)
		}
		return fmt.Sprintf("%.2f", v)
	case string:
		s := strings.TrimSpace(v)
		if s == "" || s == "<nil>" {
			return "1"
		}
		// Đổi dấu phẩy thành dấu chấm nếu là "1,5"
		s = strings.ReplaceAll(s, ",", ".")
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return s
		}
		return "1"
	default:
		return "1"
	}
}

// ParseDate chuẩn hóa ngày thành chuỗi ISO YYYY-MM-DD.
// Trả về (dateString, isAmbiguous).
func ParseDate(dateStr string) (*string, bool) {
	s := strings.TrimSpace(dateStr)
	if s == "" {
		return nil, false
	}

	// Thử parse chuẩn ISO YYYY-MM-DD
	if t, err := time.Parse("2006-01-02", s); err == nil {
		iso := t.Format("2006-01-02")
		return &iso, false
	}

	// Thử khớp regex YYYY/MM/DD hoặc YYYY.MM.DD
	if matches := dateRegexYMD.FindStringSubmatch(s); len(matches) == 4 {
		year, _ := strconv.Atoi(matches[1])
		month, _ := strconv.Atoi(matches[2])
		day, _ := strconv.Atoi(matches[3])
		if isValidDate(year, month, day) {
			iso := fmt.Sprintf("%04d-%02d-%02d", year, month, day)
			return &iso, false
		}
	}

	// Thử khớp regex DD/MM/YYYY hoặc DD-MM-YYYY hoặc DD.MM.YYYY
	if matches := dateRegexDMY.FindStringSubmatch(s); len(matches) == 4 {
		part1, _ := strconv.Atoi(matches[1])
		part2, _ := strconv.Atoi(matches[2])
		year, _ := strconv.Atoi(matches[3])

		// Kiểm tra tính hợp lệ nếu part1 = Ngày, part2 = Tháng
		validDM := isValidDate(year, part2, part1)
		// Kiểm tra tính hợp lệ nếu part1 = Tháng, part2 = Ngày (định dạng US MM/DD/YYYY)
		validMD := isValidDate(year, part1, part2)

		if validDM && validMD && part1 != part2 && part1 <= 12 && part2 <= 12 {
			// Nhập nhằng giữa Ngày và Tháng (ví dụ 05/06/2026):
			// Theo Spec 3, ngày nhập nhằng sẽ trở thành null với warning OCR_DATE_AMBIGUOUS
			return nil, true
		}

		if validDM {
			iso := fmt.Sprintf("%04d-%02d-%02d", year, part2, part1)
			return &iso, false
		}
		if validMD {
			iso := fmt.Sprintf("%04d-%02d-%02d", year, part1, part2)
			return &iso, false
		}
	}

	// Không thể nhận diện ngày
	return nil, true
}

func isValidDate(year, month, day int) bool {
	if year < 2000 || year > 2100 || month < 1 || month > 12 || day < 1 || day > 31 {
		return false
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return t.Year() == year && int(t.Month()) == month && t.Day() == day
}
