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

type record struct {
	Code      string `json:"code"`
	Supported bool   `json:"supported"`
}
type payload struct {
	Data []record `json:"data"`
}
type Directory struct{ supported map[string]struct{} }

func Load() (*Directory, error) {
	var data payload
	if err := json.Unmarshal(snapshot, &data); err != nil {
		return nil, fmt.Errorf("parse embedded banks snapshot: %w", err)
	}
	if len(data.Data) == 0 {
		return nil, errors.New("embedded banks snapshot is empty")
	}
	all := make(map[string]struct{}, len(data.Data))
	supported := make(map[string]struct{})
	for _, bank := range data.Data {
		code := strings.ToUpper(strings.TrimSpace(bank.Code))
		if code == "" {
			return nil, errors.New("embedded bank code is empty")
		}
		if _, exists := all[code]; exists {
			return nil, fmt.Errorf("duplicate embedded bank code %s", code)
		}
		all[code] = struct{}{}
		if bank.Supported {
			supported[code] = struct{}{}
		}
	}
	if len(supported) == 0 {
		return nil, errors.New("embedded banks snapshot has no supported banks")
	}
	return &Directory{supported: supported}, nil
}
func (d *Directory) Supported(code string) bool {
	if d == nil {
		return false
	}
	_, ok := d.supported[strings.ToUpper(strings.TrimSpace(code))]
	return ok
}
