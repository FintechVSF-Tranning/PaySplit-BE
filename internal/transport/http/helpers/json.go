package helpers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxJSONBodySize protects the server from clients sending unbounded JSON
// request bodies. Authentication and regular API payloads should fit in 1 MiB.
const maxJSONBodySize int64 = 64 << 10

// defaultSuccessMessage là thông điệp mặc định cho mọi response thành công khi
// handler không truyền thông điệp riêng.
const defaultSuccessMessage = "Thành công"

// successBody là vỏ bọc chuẩn cho mọi response thành công của API.
type successBody struct {
	Success bool   `json:"success"`
	Data    any    `json:"data"`
	Message string `json:"message"`
}

// WriteJSON writes data wrapped in the standard success envelope
// ({"success":true,"data":...,"message":...}) using the default message.
func WriteJSON(w http.ResponseWriter, status int, data any) error {
	return WriteJSONMessage(w, status, data, defaultSuccessMessage)
}

// WriteJSONMessage behaves like WriteJSON but lets the handler override the
// envelope message shown to the client.
func WriteJSONMessage(w http.ResponseWriter, status int, data any, message string) error {
	if strings.TrimSpace(message) == "" {
		message = defaultSuccessMessage
	}
	return WriteRawJSON(w, status, successBody{Success: true, Data: data, Message: message})
}

// WriteRawJSON writes a payload verbatim, without the success envelope. It is
// reserved for infrastructure endpoints (health probes) and for the error
// writer, which supplies its own envelope.
func WriteRawJSON(w http.ResponseWriter, status int, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal JSON response: %w", err)
	}

	// A trailing newline makes responses friendly to command-line clients while
	// remaining valid JSON whitespace.
	body = append(body, '\n')
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("write JSON response: %w", err)
	}

	return nil
}

// ReadJSON decodes exactly one JSON value from a request body. The body is
// limited to 1 MiB and unknown object fields are rejected.
func ReadJSON(w http.ResponseWriter, r *http.Request, data any) error {
	return readJSON(w, r, data, false)
}

// ReadOptionalJSON applies the same size, unknown-field, and single-value
// rules as ReadJSON while accepting a missing or empty body as the zero value.
func ReadOptionalJSON(w http.ResponseWriter, r *http.Request, data any) error {
	return readJSON(w, r, data, true)
}

func readJSON(w http.ResponseWriter, r *http.Request, data any, allowEmpty bool) error {
	if r.Body == nil {
		if allowEmpty {
			return nil
		}
		return errors.New("request body must not be empty")
	}

	// MaxBytesReader returns *http.MaxBytesError when the configured limit is
	// exceeded, allowing the handler to map it to an appropriate client error.
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodySize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(data); err != nil {
		if errors.Is(err, io.EOF) {
			if allowEmpty {
				return nil
			}
			return errors.New("request body must not be empty")
		}
		return fmt.Errorf("decode JSON request body: %w", err)
	}

	// A second decode must reach EOF. Otherwise the body contains a second JSON
	// value or malformed trailing data that should not be silently ignored.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain only one JSON value")
		}
		return fmt.Errorf("decode trailing JSON request data: %w", err)
	}

	return nil
}
