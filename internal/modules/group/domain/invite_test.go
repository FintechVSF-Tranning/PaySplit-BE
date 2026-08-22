package domain

import (
	"bytes"
	"regexp"
	"testing"
)

func TestNewInviteCode_ReturnsExactlyEightBase62Characters_AC12(t *testing.T) {
	code, err := NewInviteCode()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9]{8}$`).MatchString(code) {
		t.Fatalf("code = %q, want exactly eight Base62 characters", code)
	}
}

func TestNewInviteCode_RejectsBiasedBytesBeforeModulo_AC12(t *testing.T) {
	code, err := newInviteCode(bytes.NewReader([]byte{
		248, 249, 250, 251, 252, 253, 254, 255,
		0, 61, 62, 123, 124, 185, 186, 247,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != "A9A9A9A9" {
		t.Fatalf("code = %q, want rejected high bytes followed by unbiased mapping A9A9A9A9", code)
	}
}

func TestNewInviteCode_IsUnpredictableAcrossCalls_AC12(t *testing.T) {
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
