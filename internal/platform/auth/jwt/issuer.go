package jwt

import (
	"errors"
	"fmt"
	"strings"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

type Issuer struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

type claims struct {
	Role string `json:"role"`
	jwtv5.RegisteredClaims
}

// NewIssuer creates an HS256 access-token issuer and verifier.
func NewIssuer(secret, issuer string, ttl time.Duration) (*Issuer, error) {
	secret = strings.TrimSpace(secret)
	issuer = strings.TrimSpace(issuer)
	if secret == "" {
		return nil, errors.New("JWT secret must not be empty")
	}
	if issuer == "" {
		return nil, errors.New("JWT issuer must not be empty")
	}
	if ttl <= 0 {
		return nil, errors.New("JWT access token TTL must be positive")
	}

	return &Issuer{secret: []byte(secret), issuer: issuer, ttl: ttl}, nil
}

// Issue signs a regular-user access token for userID and returns its lifetime
// in seconds. Use IssueWithRole when issuing an admin token.
func (i *Issuer) Issue(userID string) (string, int64, error) {
	return i.IssueWithRole(userID, RoleUser)
}

// IssueWithRole signs an access token containing an application role.
func (i *Issuer) IssueWithRole(userID, role string) (string, int64, error) {
	if strings.TrimSpace(userID) == "" {
		return "", 0, errors.New("user ID must not be empty")
	}
	if !validRole(role) {
		return "", 0, errors.New("invalid user role")
	}

	now := time.Now()
	token := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims{
		Role: role,
		RegisteredClaims: jwtv5.RegisteredClaims{
			Issuer:    i.issuer,
			Subject:   userID,
			IssuedAt:  jwtv5.NewNumericDate(now),
			ExpiresAt: jwtv5.NewNumericDate(now.Add(i.ttl)),
		},
	})

	signed, err := token.SignedString(i.secret)
	if err != nil {
		return "", 0, fmt.Errorf("sign access token: %w", err)
	}
	return signed, int64(i.ttl.Seconds()), nil
}

// Verify validates an HS256 access token and returns its authenticated user ID
// and role.
func (i *Issuer) Verify(token string) (string, string, error) {
	parsedClaims := &claims{}
	parsed, err := jwtv5.ParseWithClaims(
		token,
		parsedClaims,
		func(token *jwtv5.Token) (any, error) {
			if token.Method.Alg() != jwtv5.SigningMethodHS256.Alg() {
				return nil, errors.New("unexpected signing method")
			}
			return i.secret, nil
		},
		jwtv5.WithValidMethods([]string{jwtv5.SigningMethodHS256.Alg()}),
		jwtv5.WithIssuer(i.issuer),
		jwtv5.WithExpirationRequired(),
	)
	if err != nil || parsed == nil || !parsed.Valid {
		return "", "", errors.New("invalid access token")
	}

	userID := strings.TrimSpace(parsedClaims.Subject)
	if userID == "" {
		return "", "", errors.New("invalid access token subject")
	}
	if !validRole(parsedClaims.Role) {
		return "", "", errors.New("invalid access token role")
	}
	return userID, parsedClaims.Role, nil
}

func validRole(role string) bool {
	return role == RoleAdmin || role == RoleUser
}
