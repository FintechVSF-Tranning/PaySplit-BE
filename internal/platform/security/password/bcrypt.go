package password

import (
	"errors"
	"fmt"
	"strings"

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
	if strings.TrimSpace(plain) == "" {
		return "", errors.New("password must not be empty")
	}
	if len(plain) > 72 {
		return "", errors.New("password must not exceed 72 bytes")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
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
