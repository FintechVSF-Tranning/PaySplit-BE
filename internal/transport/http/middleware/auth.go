package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"paysplit-backend/internal/transport/http/helpers"
)

type contextKey string

const userIDContextKey contextKey = "authenticated-user-id"

// TokenVerifier is implemented by the JWT platform service. Verify must
// validate the token signature and registered claims before returning its user ID.
type TokenVerifier interface {
	Verify(token string) (int64, error)
}

// Auth requires a valid Bearer token and stores its user ID in the request context.
func Auth(verifier TokenVerifier) func(http.Handler) http.Handler {
	if verifier == nil {
		panic("middleware: token verifier must not be nil")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := bearerToken(r.Header.Get("Authorization"))
			if err != nil {
				_ = helpers.WriteError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			userID, err := verifier.Verify(token)
			if err != nil || userID <= 0 {
				_ = helpers.WriteError(w, http.StatusUnauthorized, "invalid or expired access token")
				return
			}

			ctx := context.WithValue(r.Context(), userIDContextKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserID returns the authenticated user ID stored by Auth.
func UserID(ctx context.Context) (int64, bool) {
	userID, ok := ctx.Value(userIDContextKey).(int64)
	return userID, ok
}

func bearerToken(header string) (string, error) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", errors.New("invalid authorization header")
	}
	return parts[1], nil
}
