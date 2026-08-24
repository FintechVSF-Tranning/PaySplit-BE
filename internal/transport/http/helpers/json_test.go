package helpers

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadOptionalJSON_AcceptsAnEmptyBody_AC3(t *testing.T) {
	req := httptest.NewRequest("POST", "/", nil)
	response := httptest.NewRecorder()
	var payload struct {
		Value string `json:"value"`
	}

	if err := ReadOptionalJSON(response, req, &payload); err != nil {
		t.Fatalf("ReadOptionalJSON() error = %v, want empty body accepted", err)
	}
	if payload.Value != "" {
		t.Fatalf("payload.Value = %q, want zero value", payload.Value)
	}
}

func TestReadOptionalJSON_StillRejectsUnknownFieldsAndTrailingValues_AC3(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"unknown":true}`},
		{name: "second value", body: `{}` + "\n" + `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", strings.NewReader(tt.body))
			response := httptest.NewRecorder()
			var payload struct {
				Value string `json:"value"`
			}
			if err := ReadOptionalJSON(response, req, &payload); err == nil {
				t.Fatal("ReadOptionalJSON() error = nil, want invalid JSON contract rejected")
			}
		})
	}
}
