package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
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

// NewOTP sinh mã số ngẫu nhiên bảo mật 6 chữ số ("000000" đến "999999") và mã băm SHA-256 tương ứng.
func NewOTP() (otp string, hash []byte, err error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", nil, fmt.Errorf("generate otp: %w", err)
	}
	otp = fmt.Sprintf("%06d", n.Int64())
	sum := sha256.Sum256([]byte(otp))
	return otp, sum[:], nil
}

func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
