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
const userRoleContextKey contextKey = "authenticated-user-role"

// TokenVerifier is implemented by the JWT platform service. Verify must
// validate the token signature and registered claims before returning its user ID.
type TokenVerifier interface {
	Verify(token string) (userID string, role string, err error)
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

			userID, role, err := verifier.Verify(token)
			if err != nil || userID == "" {
				_ = helpers.WriteError(w, http.StatusUnauthorized, "invalid or expired access token")
				return
			}

			ctx := context.WithValue(r.Context(), userIDContextKey, userID)
			ctx = context.WithValue(ctx, userRoleContextKey, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserID returns the authenticated user ID stored by Auth.
func UserID(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(userIDContextKey).(string)
	return userID, ok
}

// UserRole returns the authenticated user's role stored by Auth.
func UserRole(ctx context.Context) (string, bool) {
	role, ok := ctx.Value(userRoleContextKey).(string)
	return role, ok
}

// RequireRole permits only authenticated users with one of the supplied roles.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, ok := UserRole(r.Context())
			if !ok {
				_ = helpers.WriteError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			if _, ok := allowed[role]; !ok {
				_ = helpers.WriteError(w, http.StatusForbidden, "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(header string) (string, error) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", errors.New("invalid authorization header")
	}
	return parts[1], nil
}
