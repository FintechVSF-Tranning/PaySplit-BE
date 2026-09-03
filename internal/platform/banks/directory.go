package banks

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

//go:embed data/banks.json
var snapshot []byte

// Bank đại diện cho thông tin ngân hàng trong danh mục VietQR.
type Bank struct {
	ID        int    `json:"id,omitempty"`
	Name      string `json:"name"`
	Code      string `json:"code"`
	BIN       string `json:"bin"`
	ShortName string `json:"short_name"`
	Logo      string `json:"logo,omitempty"`
	Supported bool   `json:"supported"`
}

type payload struct {
	Source    string `json:"source,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	Data      []Bank `json:"data"`
}

// Directory quản lý danh mục các ngân hàng được tải từ snapshot.
type Directory struct {
	banks     []Bank
	byCode    map[string]Bank
	supported map[string]struct{}
}

// Load tải và kiểm tra tính hợp lệ của snapshot danh mục ngân hàng.
func Load() (*Directory, error) {
	return parse(snapshot)
}

// parse phân tích snapshot danh mục ngân hàng.
// Nó được gọi duy nhất một lần tại thời điểm build-time thông qua biến toàn cục snapshot.
func parse(raw []byte) (*Directory, error) {
	var data payload
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parse embedded banks snapshot: %w", err)
	}
	if len(data.Data) == 0 {
		return nil, errors.New("embedded banks snapshot is empty")
	}

	banks := make([]Bank, 0, len(data.Data))
	byCode := make(map[string]Bank, len(data.Data))
	supported := make(map[string]struct{})

	for _, bank := range data.Data {
		code := strings.ToUpper(strings.TrimSpace(bank.Code))
		if code == "" {
			return nil, errors.New("embedded bank code is empty")
		}
		if _, exists := byCode[code]; exists {
			return nil, fmt.Errorf("duplicate embedded bank code %s", code)
		}
		cleaned := Bank{
			ID:        bank.ID,
			Name:      strings.TrimSpace(bank.Name),
			Code:      code,
			BIN:       strings.TrimSpace(bank.BIN),
			ShortName: strings.TrimSpace(bank.ShortName),
			Logo:      strings.TrimSpace(bank.Logo),
			Supported: bank.Supported,
		}
		banks = append(banks, cleaned)
		byCode[code] = cleaned
		if bank.Supported {
			supported[code] = struct{}{}
		}
	}

	if len(supported) == 0 {
		return nil, errors.New("embedded banks snapshot has no supported banks")
	}

	return &Directory{
		banks:     banks,
		byCode:    byCode,
		supported: supported,
	}, nil
}

// Supported kiểm tra mã ngân hàng có được hệ thống PaySplit hỗ trợ hay không.
func (d *Directory) Supported(code string) bool {
	if d == nil {
		return false
	}
	_, ok := d.supported[strings.ToUpper(strings.TrimSpace(code))]
	return ok
}

// Get trả về thông tin chi tiết của ngân hàng theo mã code.
func (d *Directory) Get(code string) (Bank, bool) {
	if d == nil {
		return Bank{}, false
	}
	bank, ok := d.byCode[strings.ToUpper(strings.TrimSpace(code))]
	return bank, ok
}

// All trả về toàn bộ danh sách ngân hàng.
func (d *Directory) All() []Bank {
	if d == nil {
		return nil
	}
	out := make([]Bank, len(d.banks))
	copy(out, d.banks)
	return out
}

// List trả về danh sách ngân hàng theo bộ lọc supported.
// Nếu supportedOnly == nil, trả về tất cả ngân hàng.
// Nếu *supportedOnly == true, chỉ trả về các ngân hàng được hỗ trợ.
// Nếu *supportedOnly == false, chỉ trả về các ngân hàng không được hỗ trợ.
func (d *Directory) List(supportedOnly *bool) []Bank {
	if d == nil {
		return nil
	}
	if supportedOnly == nil {
		return d.All()
	}
	out := make([]Bank, 0, len(d.banks))
	for _, bank := range d.banks {
		if bank.Supported == *supportedOnly {
			out = append(out, bank)
		}
	}
	return out
}
