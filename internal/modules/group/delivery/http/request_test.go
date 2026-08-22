package http

import (
	"encoding/json"
	"testing"
)

func TestCreateInviteRequest_TracksExplicitFalseAndNullPolicyFields_AC3(t *testing.T) {
	var explicitFalse createInviteRequest
	if err := json.Unmarshal([]byte(`{"regenerate":false}`), &explicitFalse); err != nil {
		t.Fatal(err)
	}
	if err := explicitFalse.decodePolicy(); err != nil {
		t.Fatal(err)
	}
	if !explicitFalse.Regenerate.Set || explicitFalse.Regenerate.Null || explicitFalse.Regenerate.Value {
		t.Fatalf("explicit false was not preserved: %+v", explicitFalse.Regenerate)
	}

	var explicitNull createInviteRequest
	if err := json.Unmarshal([]byte(`{"max_uses":null}`), &explicitNull); err != nil {
		t.Fatal(err)
	}
	if !explicitNull.MaxUses.Set || !explicitNull.MaxUses.Null {
		t.Fatalf("explicit null was not preserved: %+v", explicitNull.MaxUses)
	}
}

func TestCreateInviteRequest_RejectsSuppliedNonObjectBodies_AC3(t *testing.T) {
	for _, body := range []string{"null", `"value"`, `[]`} {
		var req createInviteRequest
		if err := json.Unmarshal([]byte(body), &req); err == nil {
			t.Fatalf("json.Unmarshal(%s) error = nil, want non-object rejected", body)
		}
	}
}

func TestCreateInviteRequest_DefersPolicyTypeValidationUntilExplicitDecode_AC3(t *testing.T) {
	var req createInviteRequest
	if err := json.Unmarshal([]byte(`{"regenerate":"false"}`), &req); err != nil {
		t.Fatalf("presence decode failed before authorization: %v", err)
	}
	if !req.hasConfiguration() || !req.Regenerate.Set {
		t.Fatalf("malformed policy presence was lost: %+v", req)
	}
	if err := req.decodePolicy(); err == nil {
		t.Fatal("decodePolicy() error = nil, want malformed bool rejected after authorization")
	}
}
