package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

func NewOpaqueToken() (raw string, hash []byte, err error) {
	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(material)
	sum := sha256.Sum256([]byte(raw))
	return raw, sum[:], nil
}

func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
