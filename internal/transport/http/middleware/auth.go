package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"paysplit-backend/internal/modules/auth/domain"
	"paysplit-backend/internal/transport/http/helpers"
)

type contextKey string

const (
	userIDContextKey    contextKey = "authenticated-user-id"
	userRoleContextKey  contextKey = "authenticated-user-role"
	sessionIDContextKey contextKey = "authenticated-session-id"
)

type TokenVerifier interface {
	Verify(token string) (userID, role, sessionID string, err error)
}

type SessionValidator interface {
	ValidateSession(context.Context, string, string, time.Time) (*domain.SessionIdentity, error)
}

func Auth(verifier TokenVerifier, sessions SessionValidator) func(http.Handler) http.Handler {
	return authenticate(verifier, sessions, true)
}

func TokenAuth(verifier TokenVerifier) func(http.Handler) http.Handler {
	return authenticate(verifier, nil, false)
}

func authenticate(verifier TokenVerifier, sessions SessionValidator, requireLive bool) func(http.Handler) http.Handler {
	if verifier == nil || (requireLive && sessions == nil) {
		panic("middleware: auth dependencies must not be nil")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, err := bearerToken(r.Header.Get("Authorization"))
			if err != nil {
				writeAuthError(w)
				return
			}
			userID, role, sessionID, err := verifier.Verify(raw)
			if err != nil {
				writeAuthError(w)
				return
			}
			if requireLive {
				identity, err := sessions.ValidateSession(r.Context(), userID, sessionID, time.Now())
				if err != nil || identity.Role != role {
					writeAuthError(w)
					return
				}
			}
			ctx := context.WithValue(r.Context(), userIDContextKey, userID)
			ctx = context.WithValue(ctx, userRoleContextKey, role)
			ctx = context.WithValue(ctx, sessionIDContextKey, sessionID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeAuthError(w http.ResponseWriter) {
	_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "authentication required", nil)
}

func UserID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(userIDContextKey).(string)
	return v, ok
}
func UserRole(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(userRoleContextKey).(string)
	return v, ok
}
func SessionID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(sessionIDContextKey).(string)
	return v, ok
}

// WithAuthContext injects authentication identity into context (useful for testing and internal calls)
func WithAuthContext(ctx context.Context, userID, sessionID, role string) context.Context {
	ctx = context.WithValue(ctx, userIDContextKey, userID)
	ctx = context.WithValue(ctx, sessionIDContextKey, sessionID)
	ctx = context.WithValue(ctx, userRoleContextKey, role)
	return ctx
}

func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, ok := UserRole(r.Context())
			if !ok {
				writeAuthError(w)
				return
			}
			if _, ok = allowed[role]; !ok {
				_ = helpers.WriteAPIError(w, http.StatusForbidden, "INSUFFICIENT_PERMISSIONS", "insufficient permissions", nil)
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
