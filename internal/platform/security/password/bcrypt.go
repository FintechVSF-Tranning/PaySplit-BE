package password

import (
	"errors"
	"fmt"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = bcrypt.DefaultCost

type Manager struct{}

func New() *Manager {
	return &Manager{}
}

// Hash creates a bcrypt password hash. Passwords must be non-empty and no
// longer than bcrypt's 72-byte input limit.
func (m *Manager) Hash(plain string) (string, error) {
	if err := m.Validate(plain); err != nil {
		return "", err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func (m *Manager) Validate(plain string) error {
	if len(plain) < 8 || len(plain) > 72 {
		return errors.New("password must contain between 8 and 72 bytes")
	}
	var lower, upper, digit bool
	for _, r := range plain {
		lower = lower || unicode.IsLower(r)
		upper = upper || unicode.IsUpper(r)
		digit = digit || unicode.IsDigit(r)
	}
	if !lower || !upper || !digit {
		return errors.New("password must contain lowercase, uppercase, and a digit")
	}
	return nil
}

// Compare verifies a plaintext password against its bcrypt hash.
func (m *Manager) Compare(hash, plain string) error {
	if hash == "" || plain == "" {
		return errors.New("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)); err != nil {
		return errors.New("invalid credentials")
	}
	return nil
}
