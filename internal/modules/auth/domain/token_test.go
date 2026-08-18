package domain

import (
	"bytes"
	"strconv"
	"testing"
)

func TestOpaqueTokenStoresOnlyStableHash(t *testing.T) {
	raw, hash, err := NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 43 {
		t.Fatalf("unexpected token length %d", len(raw))
	}
	if len(hash) != 32 {
		t.Fatalf("unexpected hash length %d", len(hash))
	}
	if bytes.Contains(hash, []byte(raw)) {
		t.Fatal("hash contains raw token")
	}
	if !bytes.Equal(hash, HashToken(raw)) {
		t.Fatal("token hash is not stable")
	}
	raw2, _, err := NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	if raw == raw2 {
		t.Fatal("tokens must be unique")
	}
}

func TestNewOTPGeneratesValidSixDigitCode(t *testing.T) {
	for i := 0; i < 100; i++ {
		otp, hash, err := NewOTP()
		if err != nil {
			t.Fatalf("NewOTP failed: %v", err)
		}
		if len(otp) != 6 {
			t.Fatalf("expected 6 digits OTP, got %q (len %d)", otp, len(otp))
		}
		val, err := strconv.Atoi(otp)
		if err != nil || val < 0 || val > 999999 {
			t.Fatalf("expected integer between 0 and 999999, got %q", otp)
		}
		if len(hash) != 32 {
			t.Fatalf("expected 32 bytes SHA-256 hash, got %d", len(hash))
		}
		if !bytes.Equal(hash, HashToken(otp)) {
			t.Fatalf("hash mismatch for OTP %q", otp)
		}
	}
}
