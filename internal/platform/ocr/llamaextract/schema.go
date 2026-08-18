package llamaextract

// ReceiptSchema trả về cấu trúc JSON Schema định nghĩa dữ liệu cần trích xuất từ hóa đơn
// gửi cho LlamaExtract API v2 (Spec 3 AC-3).
func ReceiptSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"merchant_name": map[string]any{
				"type":        "string",
				"description": "Tên nhà hàng, quán ăn, cửa hàng hoặc đơn vị xuất hóa đơn (nếu có)",
			},
			"bill_date": map[string]any{
				"type":        "string",
				"description": "Ngày lập hóa đơn theo định dạng YYYY-MM-DD hoặc DD/MM/YYYY",
			},
			"items": map[string]any{
				"type":        "array",
				"description": "Danh sách các món ăn, đồ uống hoặc dịch vụ trên hóa đơn",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Tên món hoặc sản phẩm",
						},
						"quantity": map[string]any{
							"type":        "string",
							"description": "Số lượng món (ví dụ: '1', '2', '0.5')",
						},
						"unit_price": map[string]any{
							"type":        "integer",
							"description": "Đơn giá bằng số nguyên VND",
						},
						"line_total": map[string]any{
							"type":        "integer",
							"description": "Thành tiền bằng số nguyên VND",
						},
					},
					"required": []string{"name", "line_total"},
				},
			},
			"subtotal": map[string]any{
				"type":        "integer",
				"description": "Tổng tiền hàng trước thuế phí hoặc giảm giá (VND)",
			},
			"service_charge": map[string]any{
				"type":        "integer",
				"description": "Phí dịch vụ nếu có ghi trên hóa đơn (VND), mặc định 0",
			},
			"vat": map[string]any{
				"type":        "integer",
				"description": "Tiền thuế GTGT / VAT nếu có ghi trên hóa đơn (VND), mặc định 0",
			},
			"discount": map[string]any{
				"type":        "integer",
				"description": "Tiền giảm giá, voucher, chiết khấu nếu có (VND), mặc định 0",
			},
			"total": map[string]any{
				"type":        "integer",
				"description": "Tổng số tiền thanh toán cuối cùng ghi trên hóa đơn (VND)",
			},
		},
		"required": []string{"items", "total"},
	}
}
