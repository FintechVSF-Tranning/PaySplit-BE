package helpers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWriteJSON_WrapsSuccessEnvelope verifies the standard success shape
// {"success":true,"data":...,"message":...} with the default message.
func TestWriteJSON_WrapsSuccessEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteJSON(rec, http.StatusOK, map[string]string{"foo": "bar"}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	var body struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
		Message string         `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, rec.Body.String())
	}
	if !body.Success {
		t.Errorf("success = false, want true")
	}
	if body.Data["foo"] != "bar" {
		t.Errorf("data.foo = %v, want bar", body.Data["foo"])
	}
	if body.Message != defaultSuccessMessage {
		t.Errorf("message = %q, want default %q", body.Message, defaultSuccessMessage)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
}

// TestWriteJSONMessage_OverridesMessage verifies handlers can supply a
// message and that a blank message falls back to the default.
func TestWriteJSONMessage_OverridesMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteJSONMessage(rec, http.StatusCreated, nil, "Đăng ký thành công."); err != nil {
		t.Fatalf("WriteJSONMessage() error = %v", err)
	}
	var body struct {
		Success bool   `json:"success"`
		Data    any    `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Message != "Đăng ký thành công." {
		t.Errorf("message = %q, want override", body.Message)
	}
	if body.Data != nil {
		t.Errorf("data = %v, want nil", body.Data)
	}

	rec = httptest.NewRecorder()
	if err := WriteJSONMessage(rec, http.StatusOK, nil, "   "); err != nil {
		t.Fatalf("WriteJSONMessage() error = %v", err)
	}
	json.Unmarshal(rec.Body.Bytes(), &body) //nolint:errcheck
	if body.Message != defaultSuccessMessage {
		t.Errorf("blank message = %q, want default %q", body.Message, defaultSuccessMessage)
	}
}

// TestWriteRawJSON_DoesNotWrap verifies infrastructure endpoints (health
// probes) are exempt from the success envelope.
func TestWriteRawJSON_DoesNotWrap(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteRawJSON(rec, http.StatusOK, map[string]string{"status": "ok"}); err != nil {
		t.Fatalf("WriteRawJSON() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := body["success"]; ok {
		t.Errorf("body has success key, want raw passthrough: %v", body)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
}

// TestWriteAPIError_WrapsErrorEnvelope verifies the standard error shape
// {"success":false,"error":{"code","message","details"}}.
func TestWriteAPIError_WrapsErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	fields := map[string]string{"email": "invalid"}
	if err := WriteAPIError(rec, http.StatusBadRequest, "VALIDATION_FAILED", "request validation failed", fields); err != nil {
		t.Fatalf("WriteAPIError() error = %v", err)
	}

	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, rec.Body.String())
	}
	if body.Success {
		t.Errorf("success = true, want false")
	}
	if body.Error.Code != "VALIDATION_FAILED" {
		t.Errorf("error.code = %q", body.Error.Code)
	}
	if body.Error.Details["email"] != "invalid" {
		t.Errorf("error.details = %v, want key 'email' under 'details' (not 'fields')", body.Error.Details)
	}

	// Confirm the literal key on the wire is "details", not the old "fields".
	if !json.Valid(rec.Body.Bytes()) {
		t.Fatal("invalid JSON body")
	}
	var raw map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	errObj := raw["error"].(map[string]any)
	if _, hasOldKey := errObj["fields"]; hasOldKey {
		t.Errorf("response still carries legacy 'fields' key: %v", errObj)
	}
	if _, hasNewKey := errObj["details"]; !hasNewKey {
		t.Errorf("response missing 'details' key: %v", errObj)
	}
}

// TestWriteError_UsesRequestFailedCode verifies the convenience wrapper still
// produces an enveloped error.
func TestWriteError_UsesRequestFailedCode(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteError(rec, http.StatusInternalServerError, "boom"); err != nil {
		t.Fatalf("WriteError() error = %v", err)
	}
	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Success {
		t.Errorf("success = true, want false")
	}
	if body.Error.Code != "REQUEST_FAILED" || body.Error.Message != "boom" {
		t.Errorf("error = %+v", body.Error)
	}
}
