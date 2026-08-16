package domain

import (
	"bytes"
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
