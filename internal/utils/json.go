package utils

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func Write(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

func ReadData(r *http.Request, data any) error {
	// Khởi tạo decoder để đọc data từ r.body
	decoder := json.NewDecoder(r.Body)
	// Cấm các field ko xác định (các field ko có trong struct target)
	decoder.DisallowUnknownFields()

	// Giải mã sang struct target và gán vào data
	err := decoder.Decode(data)
	if err != nil {
		return err
	}

	// Check data dư thừa sau JSON đầu tiên
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("Request body must contain only 1 JSON value")
	}
	return nil
}
