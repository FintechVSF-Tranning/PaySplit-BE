package helpers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// maxJSONBodySize protects the server from clients sending unbounded JSON
// request bodies. Authentication and regular API payloads should fit in 1 MiB.
const maxJSONBodySize int64 = 1 << 20

// WriteJSON writes data as a JSON response. The payload is marshaled before
// the status code is committed so the caller can handle encoding failures.
func WriteJSON(w http.ResponseWriter, status int, data any) error {
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
	if r.Body == nil {
		return errors.New("request body must not be empty")
	}

	// MaxBytesReader returns *http.MaxBytesError when the configured limit is
	// exceeded, allowing the handler to map it to an appropriate client error.
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodySize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(data); err != nil {
		if errors.Is(err, io.EOF) {
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
