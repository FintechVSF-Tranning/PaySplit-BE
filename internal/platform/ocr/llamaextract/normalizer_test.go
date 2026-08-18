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
		{"String with currency (50.000 VNĐ)", "50.000 VNĐ", 50000},
		{"String with currency (75.000đ)", "75.000đ", 750000 / 10},
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
