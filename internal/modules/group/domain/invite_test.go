package domain

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewInviteCode_ReturnsThirtyTwoRawBytesBase64URLEncoded(t *testing.T) {
	code, err := NewInviteCode()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(code)
	if err != nil {
		t.Fatalf("code is not valid unpadded base64url: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("decoded length = %d, want 32", len(decoded))
	}
}

func TestNewInviteCode_HasNoPadding(t *testing.T) {
	code, err := NewInviteCode()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(code, "=") {
		t.Fatalf("code %q contains padding, want unpadded base64url", code)
	}
}

func TestNewInviteCode_IsUnpredictableAcrossCalls(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		code, err := NewInviteCode()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if seen[code] {
			t.Fatalf("code %q repeated across calls, generator is not producing fresh randomness", code)
		}
		seen[code] = true
	}
}
