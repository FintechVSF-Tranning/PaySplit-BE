package helpers

import "net/http"

// errorBody is the public error shape returned by the HTTP API. Keeping this
// type private prevents handlers from depending on its implementation details.
type ErrorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"details,omitempty"`
}

type errorBody struct {
	Success bool        `json:"success"`
	Error   ErrorDetail `json:"error"`
}

// WriteError writes a consistent JSON error response using the shared JSON
// writer. The returned error only describes a failure to encode or send the
// response; message is the API error exposed to the client.
func WriteError(w http.ResponseWriter, status int, message string) error {
	return WriteAPIError(w, status, "REQUEST_FAILED", message, nil)
}

func WriteAPIError(w http.ResponseWriter, status int, code, message string, fields map[string]string) error {
	return WriteRawJSON(w, status, errorBody{Success: false, Error: ErrorDetail{Code: code, Message: message, Fields: fields}})
}
