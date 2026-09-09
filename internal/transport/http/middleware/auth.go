package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"paysplit-backend/internal/platform/session"
	"paysplit-backend/internal/transport/http/helpers"
)

type contextKey string

const (
	userIDContextKey    contextKey = "authenticated-user-id"
	userRoleContextKey  contextKey = "authenticated-user-role"
	sessionIDContextKey contextKey = "authenticated-session-id"
)

// SessionStore là nguồn phán quyết duy nhất cho việc xác thực. Sau khi bỏ JWT,
// không còn gì verify được tại chỗ: mọi request đều phải tra kho phiên.
type SessionStore interface {
	Get(ctx context.Context, raw string, now time.Time) (*session.Session, error)
}

// Auth chặn mọi request không kèm credential còn hiệu lực.
//
// Không còn biến thể "chỉ verify chữ ký, không tra kho" như TokenAuth trước đây:
// credential giờ là chuỗi đục, không mang thông tin nào để kiểm ngoại tuyến.
func Auth(sessions SessionStore) func(http.Handler) http.Handler {
	if sessions == nil {
		panic("middleware: session store must not be nil")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, err := BearerToken(r.Header.Get("Authorization"))
			if err != nil {
				writeAuthError(w)
				return
			}
			sess, err := sessions.Get(r.Context(), raw, time.Now())
			if err != nil {
				// Chỉ credential không tồn tại mới là lỗi xác thực. Lỗi mạng hay
				// timeout của kho phiên là lỗi HẠ TẦNG, và gộp chung thành 401 sẽ
				// bảo ứng dụng rằng phiên đã chết: client xoá credential và người
				// dùng phải đăng nhập lại bằng tay, dù không phiên nào bị thu hồi.
				// Một cú chớp vài chục giây của Redis khi đó đăng xuất vĩnh viễn
				// toàn bộ người dùng.
				if errors.Is(err, session.ErrNotFound) {
					writeAuthError(w)
					return
				}
				writeSessionStoreUnavailable(w)
				return
			}
			// Đặt SID (UUID, khớp sessions.id) vào context, KHÔNG BAO GIỜ đặt
			// credential: tầng SSE parse giá trị này thành UUID và mọi bản ghi
			// audit đều tham chiếu tới nó.
			ctx := context.WithValue(r.Context(), userIDContextKey, sess.UserID)
			ctx = context.WithValue(ctx, userRoleContextKey, sess.Role)
			ctx = context.WithValue(ctx, sessionIDContextKey, sess.SID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeAuthError(w http.ResponseWriter) {
	_ = helpers.WriteAPIError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "authentication required", nil)
}

// writeSessionStoreUnavailable báo kho phiên không truy cập được. Mã 503 nói với
// client rằng credential vẫn còn giá trị và nên thử lại, khác hẳn 401 vốn có
// nghĩa "phiên đã chết, xoá credential đi".
func writeSessionStoreUnavailable(w http.ResponseWriter) {
	_ = helpers.WriteAPIError(w, http.StatusServiceUnavailable, "SESSION_STORE_UNAVAILABLE", "session store is unavailable", nil)
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

// BearerToken tách credential khỏi header Authorization. Được export để handler
// đăng xuất — route duy nhất không đi qua Auth — dùng chung đúng một bộ phân tích;
// hai bộ parse bearer khác nhau trong cùng một hệ thống là lỗi auth kinh điển.
func BearerToken(header string) (string, error) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", errors.New("invalid authorization header")
	}
	return parts[1], nil
}
