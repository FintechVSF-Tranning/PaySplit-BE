package llamaextract

import (
	"encoding/json"
	"testing"
)

// TestReceiptSchema_MarshalsToValidJSON đảm bảo schema gửi cho LlamaExtract API (client.go dùng
// trực tiếp trong createExtractionJob) luôn là JSON hợp lệ. Một map chứa giá trị không thể
// marshal (ví dụ channel, func) sẽ làm request tới provider lỗi âm thầm ở tầng http, nên đây là
// bất biến cần khóa lại bằng test, không chỉ nhìn bằng mắt.
func TestReceiptSchema_MarshalsToValidJSON(t *testing.T) {
	raw, err := json.Marshal(ReceiptSchema())
	if err != nil {
		t.Fatalf("ReceiptSchema() phải marshal được sang JSON, lỗi: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("JSON của ReceiptSchema() phải unmarshal lại được, lỗi: %v", err)
	}
}

// TestReceiptSchema_RequiresItemsAndTotal khớp Spec 3 AC-3: normalizer.go (Normalize) giả định
// payload luôn có items và total để tính toán, nên schema gửi cho provider phải bắt buộc đúng
// hai trường này, nếu không provider có thể trả về thiếu và làm sập bước chuẩn hóa phía sau.
func TestReceiptSchema_RequiresItemsAndTotal(t *testing.T) {
	schema := ReceiptSchema()

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("mong đợi schema['required'] là []string, nhận %T", schema["required"])
	}

	requiredSet := make(map[string]bool, len(required))
	for _, r := range required {
		requiredSet[r] = true
	}

	for _, want := range []string{"items", "total"} {
		if !requiredSet[want] {
			t.Errorf("mong đợi '%s' nằm trong required của schema gốc, nhận %v", want, required)
		}
	}
}

// TestReceiptSchema_ItemRequiresNameAndLineTotal khớp Spec 3 AC-15: normalizer.go phân loại dòng
// khuyến mãi dựa trên name và line_total của từng item (isPromotionMarker, rawLineTotal), nên cả
// hai trường này phải luôn được provider trả về.
func TestReceiptSchema_ItemRequiresNameAndLineTotal(t *testing.T) {
	schema := ReceiptSchema()

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("mong đợi schema['properties'] là map[string]any, nhận %T", schema["properties"])
	}
	itemsField, ok := properties["items"].(map[string]any)
	if !ok {
		t.Fatalf("mong đợi properties['items'] là map[string]any, nhận %T", properties["items"])
	}
	itemSchema, ok := itemsField["items"].(map[string]any)
	if !ok {
		t.Fatalf("mong đợi items['items'] là map[string]any, nhận %T", itemsField["items"])
	}
	itemRequired, ok := itemSchema["required"].([]string)
	if !ok {
		t.Fatalf("mong đợi item['required'] là []string, nhận %T", itemSchema["required"])
	}

	requiredSet := make(map[string]bool, len(itemRequired))
	for _, r := range itemRequired {
		requiredSet[r] = true
	}

	for _, want := range []string{"name", "line_total"} {
		if !requiredSet[want] {
			t.Errorf("mong đợi '%s' nằm trong required của item, nhận %v", want, itemRequired)
		}
	}
}
