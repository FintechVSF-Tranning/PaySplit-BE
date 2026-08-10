package helpers

import "net/http"

// errorBody is the public error shape returned by the HTTP API. Keeping this
// type private prevents handlers from depending on its implementation details.
type errorBody struct {
	Error string `json:"error"`
}

// WriteError writes a consistent JSON error response using the shared JSON
// writer. The returned error only describes a failure to encode or send the
// response; message is the API error exposed to the client.
func WriteError(w http.ResponseWriter, status int, message string) error {
	return WriteJSON(w, status, errorBody{Error: message})
}
