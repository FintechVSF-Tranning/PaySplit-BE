package llamaextract

import (
	"testing"

	"paysplit-backend/internal/modules/bill/domain"
)

func TestParseVNDAmount(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected int64
	}{
		{"Integer input", 50000, 50000},
		{"Int64 input", int64(120000), 120000},
		{"Float input", 45000.75, 45001},
		{"String plain number", "300000", 300000},
		{"String with dots (50.000)", "50.000", 50000},
		{"String with commas (50,000)", "50,000", 50000},
		{"String with million dots (1.250.000)", "1.250.000", 1250000},
		{"String with 'k' suffix (50k)", "50k", 50000},
		{"String with 'K' suffix (120K)", "120K", 120000},
		{"String with decimal and 'k' suffix (50.5k)", "50.5k", 50500},
		{"String with decimal and 'K' suffix (12.75K)", "12.75K", 12750},
		{"String with decimal and comma 'k' suffix (50,5k)", "50,5k", 50500},
		{"String with decimal (50.5)", "50.5", 51},
		{"String with comma decimal (50,5)", "50,5", 51},
		{"String with million and decimal comma (1.250.000,50)", "1.250.000,50", 1250001},
		{"String with million and decimal dot (1,250,000.50)", "1,250,000.50", 1250001},
		{"String with currency (50.000 VNĐ)", "50.000 VNĐ", 50000},
		{"String with currency (75.000đ)", "75.000đ", 75000},
		{"Nil input", nil, 0},
		{"Empty string", "", 0},
		{"Negative input", -5000, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseVNDAmount(tt.input)
			if got != tt.expected {
				t.Errorf("ParseVNDAmount(%v) = %v, expected %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectedDate string
		ambiguous    bool
	}{
		{"ISO Date YYYY-MM-DD", "2026-08-17", "2026-08-17", false},
		{"Vietnamese date DD/MM/YYYY unambiguous (day > 12)", "25/08/2026", "2026-08-25", false},
		{"Vietnamese date DD-MM-YYYY unambiguous", "18-09-2026", "2026-09-18", false},
		{"Ambiguous date (05/06/2026)", "05/06/2026", "", true},
		{"Invalid date", "not-a-date", "", true},
		{"Empty date", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, isAmb := ParseDate(tt.input)
			if tt.ambiguous != isAmb {
				t.Errorf("ParseDate(%q) ambiguous = %v, expected %v", tt.input, isAmb, tt.ambiguous)
			}
			if tt.expectedDate != "" {
				if got == nil || *got != tt.expectedDate {
					t.Errorf("ParseDate(%q) = %v, expected %v", tt.input, got, tt.expectedDate)
				}
			} else if !tt.ambiguous && got != nil {
				t.Errorf("ParseDate(%q) expected nil, got %v", tt.input, *got)
			}
		})
	}
}

func TestNormalizeReceiptJSON(t *testing.T) {
	rawJSON := []byte(`{
		"merchant_name": "  Pizza 4P's Bến Thành  ",
		"bill_date": "17/08/2026",
		"items": [
			{"name": "Pizza 4 Cheese", "quantity": "1", "unit_price": 250000, "line_total": 250000},
			{"name": "Trà Đào Cam Sả", "quantity": "2", "unit_price": "45.000", "line_total": "90.000"}
		],
		"subtotal": 340000,
		"service_charge": "17.000",
		"vat": "28.560",
		"discount": 0,
		"total": 385560,
		"confidence": 0.95
	}`)

	candidate, err := Normalize(rawJSON)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}

	if candidate.MerchantName == nil || *candidate.MerchantName != "Pizza 4P's Bến Thành" {
		t.Errorf("unexpected merchant name: %v", candidate.MerchantName)
	}
	if candidate.BillDate == nil || *candidate.BillDate != "2026-08-17" {
		t.Errorf("unexpected bill date: %v", candidate.BillDate)
	}
	if len(candidate.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(candidate.Items))
	}
	if candidate.Items[1].LineTotal != 90000 || candidate.Items[1].UnitPrice != 45000 {
		t.Errorf("unexpected item line total or unit price: %+v", candidate.Items[1])
	}
	if candidate.Subtotal != 340000 || candidate.Total != 385560 {
		t.Errorf("unexpected subtotal/total: subtotal=%d, total=%d", candidate.Subtotal, candidate.Total)
	}
	if len(candidate.Warnings) != 0 {
		t.Errorf("expected 0 warnings, got %v", candidate.Warnings)
	}
}

func TestNormalizeWithMismatchesAndAmbiguousDate(t *testing.T) {
	rawJSON := []byte(`{
		"merchant_name": "Quán Cafe",
		"bill_date": "04/05/2026",
		"items": [
			{"name": "Cà phê đen", "quantity": "1", "unit_price": 20000, "line_total": 20000}
		],
		"subtotal": 50000,
		"total": 100000,
		"confidence": 0.60
	}`)

	candidate, err := Normalize(rawJSON)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}

	// Should have warnings: OCR_DATE_AMBIGUOUS, SUBTOTAL_MISMATCH, TOTAL_MISMATCH, LOW_CONFIDENCE
	warningSet := make(map[string]bool)
	for _, w := range candidate.Warnings {
		warningSet[w] = true
	}

	expectedWarnings := []string{
		domain.WarningOCRDateAmbiguous,
		domain.WarningSubtotalMismatch,
		domain.WarningTotalMismatch,
		domain.WarningLowConfidence,
	}

	for _, ew := range expectedWarnings {
		if !warningSet[ew] {
			t.Errorf("expected warning %s was not generated in %v", ew, candidate.Warnings)
		}
	}
}

// TestNormalize_VMRoyalCityBenchmark khớp Spec 3 AC-15, AC-17, AC-18: 5 món với 3 dòng khuyến mãi
// KM xen kẽ phải gộp đúng vào món liền trước và tổng phải khớp tuyệt đối.
func TestNormalize_VMRoyalCityBenchmark(t *testing.T) {
	rawJSON := []byte(`{
		"merchant_name": "VM Royal City",
		"items": [
			{"name": "Lẩu bò", "quantity": "1", "unit_price": 189500, "line_total": 189500},
			{"name": "KM", "quantity": null, "unit_price": null, "line_total": -64125},
			{"name": "Rau muống", "quantity": "2", "unit_price": 25000, "line_total": 50000},
			{"name": "Bia Tiger", "quantity": "6", "unit_price": 20000, "line_total": 120000},
			{"name": "Khuyến mãi", "quantity": null, "unit_price": null, "line_total": -68175},
			{"name": "Nước ngọt", "quantity": "4", "unit_price": 15000, "line_total": 60000},
			{"name": "Tôm hấp", "quantity": "1", "unit_price": 125900, "line_total": 125900},
			{"name": "KM", "quantity": null, "unit_price": null, "line_total": -59900}
		],
		"discount": 0
	}`)

	candidate, err := Normalize(rawJSON)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}

	if len(candidate.Items) != 5 {
		t.Fatalf("expected 5 clean items, got %d: %+v", len(candidate.Items), candidate.Items)
	}
	if candidate.Subtotal != 545400 {
		t.Errorf("expected gross subtotal 545400, got %d", candidate.Subtotal)
	}
	if candidate.TotalItemDiscount != 192200 {
		t.Errorf("expected total_item_discount 192200, got %d", candidate.TotalItemDiscount)
	}
	if candidate.GeneralDiscount != 0 {
		t.Errorf("expected general_discount 0, got %d", candidate.GeneralDiscount)
	}
	if candidate.Discount != 192200 {
		t.Errorf("expected total discount 192200, got %d", candidate.Discount)
	}
	if candidate.Total != 545400-192200 {
		t.Errorf("expected total %d, got %d", 545400-192200, candidate.Total)
	}
	for _, w := range candidate.Warnings {
		if w == domain.WarningTotalMismatch || w == domain.WarningSubtotalMismatch {
			t.Errorf("did not expect mismatch warning, got %v", candidate.Warnings)
		}
	}

	// Lẩu bò hấp thụ khuyến mãi -64.125 liền sau nó.
	if candidate.Items[0].DiscountAmount != 64125 || candidate.Items[0].FinalPrice != 189500-64125 {
		t.Errorf("unexpected folded discount on item 0: %+v", candidate.Items[0])
	}
	// Bia Tiger hấp thụ khuyến mãi -68.175 liền sau nó.
	if candidate.Items[2].DiscountAmount != 68175 || candidate.Items[2].FinalPrice != 120000-68175 {
		t.Errorf("unexpected folded discount on item 2 (Bia Tiger): %+v", candidate.Items[2])
	}
	// Tôm hấp hấp thụ khuyến mãi -59.900 liền sau nó.
	if candidate.Items[4].DiscountAmount != 59900 || candidate.Items[4].FinalPrice != 125900-59900 {
		t.Errorf("unexpected folded discount on item 4 (Tôm hấp): %+v", candidate.Items[4])
	}
}

// TestNormalize_ItemDiscountPlusVoucher khớp Spec 3 AC-17: giảm giá theo món và voucher chung
// phải được tách riêng, không cộng dồn sai.
func TestNormalize_ItemDiscountPlusVoucher(t *testing.T) {
	rawJSON := []byte(`{
		"items": [
			{"name": "Bò bít tết", "quantity": "1", "unit_price": 250000, "line_total": 250000},
			{"name": "KM", "quantity": null, "unit_price": null, "line_total": -50000}
		],
		"subtotal": 250000,
		"discount": 80000,
		"total": 170000
	}`)

	candidate, err := Normalize(rawJSON)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}

	if candidate.TotalItemDiscount != 50000 {
		t.Errorf("expected total_item_discount 50000, got %d", candidate.TotalItemDiscount)
	}
	if candidate.GeneralDiscount != 30000 {
		t.Errorf("expected general_discount 30000, got %d", candidate.GeneralDiscount)
	}
	if candidate.Discount != 80000 {
		t.Errorf("expected total discount 80000, got %d", candidate.Discount)
	}
}

// TestNormalize_OrphanPromotionLine khớp Spec 3 AC-16: dòng khuyến mãi không có món liền trước
// phải chuyển thành giảm giá chung kèm cảnh báo OCR_ORPHAN_ITEM_DISCOUNT.
func TestNormalize_OrphanPromotionLine(t *testing.T) {
	rawJSON := []byte(`{
		"items": [
			{"name": "Voucher giảm giá", "quantity": null, "unit_price": null, "line_total": -30000},
			{"name": "Cà phê sữa", "quantity": "2", "unit_price": 25000, "line_total": 50000}
		],
		"subtotal": 50000,
		"discount": 0,
		"total": 20000
	}`)

	candidate, err := Normalize(rawJSON)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}

	if len(candidate.Items) != 1 {
		t.Fatalf("expected 1 clean item, got %d: %+v", len(candidate.Items), candidate.Items)
	}
	if candidate.Items[0].DiscountAmount != 0 {
		t.Errorf("expected preserved item to carry no discount, got %+v", candidate.Items[0])
	}
	if candidate.TotalItemDiscount != 0 {
		t.Errorf("expected total_item_discount 0, got %d", candidate.TotalItemDiscount)
	}
	if candidate.GeneralDiscount != 30000 {
		t.Errorf("expected general_discount 30000 from orphan line, got %d", candidate.GeneralDiscount)
	}

	found := false
	for _, w := range candidate.Warnings {
		if w == domain.WarningOCROrphanItemDiscount {
			found = true
		}
	}
	if !found {
		t.Errorf("expected OCR_ORPHAN_ITEM_DISCOUNT warning, got %v", candidate.Warnings)
	}
}

// TestNormalize_ItemDiscountExceedsLinePrice khớp Spec 3 AC-16: giảm giá vượt quá giá gốc của
// chính món đó phải bị chặn về 0 và phần dư chuyển sang giảm giá chung.
func TestNormalize_ItemDiscountExceedsLinePrice(t *testing.T) {
	rawJSON := []byte(`{
		"items": [
			{"name": "Trà sữa", "quantity": "1", "unit_price": 100000, "line_total": 100000},
			{"name": "KM", "quantity": null, "unit_price": null, "line_total": -150000}
		],
		"subtotal": 100000,
		"discount": 0,
		"total": -50000
	}`)

	candidate, err := Normalize(rawJSON)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}

	if len(candidate.Items) != 1 {
		t.Fatalf("expected 1 clean item, got %d: %+v", len(candidate.Items), candidate.Items)
	}
	if candidate.Items[0].DiscountAmount != 100000 || candidate.Items[0].FinalPrice != 0 {
		t.Errorf("expected discount clamped to line total with final_price 0, got %+v", candidate.Items[0])
	}
	if candidate.GeneralDiscount != 50000 {
		t.Errorf("expected excess 50000 moved to general_discount, got %d", candidate.GeneralDiscount)
	}

	found := false
	for _, w := range candidate.Warnings {
		if w == domain.WarningOCRItemDiscountExceeded {
			found = true
		}
	}
	if !found {
		t.Errorf("expected OCR_ITEM_DISCOUNT_EXCEEDED warning, got %v", candidate.Warnings)
	}
}
