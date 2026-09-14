package middleware

// Middleware Auth là đường đọc nóng nhất của hệ thống: sau khi bỏ JWT, nó là nơi
// duy nhất quyết định một request có được đi tiếp hay không. Trước spec 0011 nó
// chỉ được kiểm gián tiếp qua handler_integration_test.go — mà file đó tự bỏ qua
// trong im lặng khi thiếu TEST_DATABASE_URL, nên một lần `make test` xanh không
// chứng minh được gì về nó. File này kiểm thẳng, không cần Postgres lẫn Redis.
//
// covers: AC-1 (credential đục, không tự xác minh ngoại tuyến), AC-3 (đặt SID vào
// context, không bao giờ đặt credential), AC-13 (parse bearer dùng chung một bộ).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"paysplit-backend/internal/platform/session"
)

// stubStore thay cho kho phiên Redis. Chỉ ghi lại đầu vào và trả về kết quả đã
// dựng sẵn, vì thứ đang được kiểm là middleware chứ không phải Redis.
type stubStore struct {
	sess *session.Session
	err  error

	calls   int
	gotRaw  string
	gotNow  time.Time
	gotCtxV any
}

func (s *stubStore) Get(ctx context.Context, raw string, now time.Time) (*session.Session, error) {
	s.calls++
	s.gotRaw = raw
	s.gotNow = now
	s.gotCtxV = ctx.Value(contextKey("probe"))
	if s.err != nil {
		return nil, s.err
	}
	return s.sess, nil
}

func validSession() *session.Session {
	return &session.Session{
		SID:         "01a0843c-1526-73f7-a6fc-711bdbdc778e",
		UserID:      "01a0843a-1ed2-7e45-b0ef-5448b0703526",
		Role:        "user",
		AbsoluteExp: time.Now().Add(720 * time.Hour),
	}
}

// okHandler ghi lại danh tính mà middleware đã đặt vào context.
type captured struct {
	reached bool
	userID  string
	role    string
	sid     string
	okUser  bool
	okRole  bool
	okSID   bool
}

func okHandler(c *captured) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.reached = true
		c.userID, c.okUser = UserID(r.Context())
		c.role, c.okRole = UserRole(r.Context())
		c.sid, c.okSID = SessionID(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func TestBearerToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		header  string
		want    string
		wantErr bool
	}{
		{name: "bearer hợp lệ", header: "Bearer abc123", want: "abc123"},
		{name: "scheme không phân biệt hoa thường", header: "bearer abc123", want: "abc123"},
		{name: "scheme viết hoa toàn bộ", header: "BEARER abc123", want: "abc123"},
		{name: "nhiều khoảng trắng giữa hai phần", header: "Bearer    abc123", want: "abc123"},
		{name: "tab thay cho khoảng trắng", header: "Bearer\tabc123", want: "abc123"},
		{name: "có khoảng trắng thừa hai đầu", header: "  Bearer abc123  ", want: "abc123"},
		{name: "header rỗng", header: "", wantErr: true},
		{name: "chỉ có scheme", header: "Bearer", wantErr: true},
		{name: "scheme kèm khoảng trắng nhưng không có token", header: "Bearer ", wantErr: true},
		{name: "sai scheme", header: "Basic abc123", wantErr: true},
		{name: "thiếu scheme", header: "abc123", wantErr: true},
		{name: "thừa phần thứ ba", header: "Bearer abc123 extra", wantErr: true},
		{name: "chỉ toàn khoảng trắng", header: "   ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := BearerToken(tt.header)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("BearerToken(%q) = %q, muốn lỗi", tt.header, got)
				}
				if got != "" {
					t.Fatalf("BearerToken(%q) trả token %q kèm lỗi, muốn chuỗi rỗng", tt.header, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("BearerToken(%q) lỗi bất ngờ: %v", tt.header, err)
			}
			if got != tt.want {
				t.Fatalf("BearerToken(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestAuth_ValidCredentialReachesHandler(t *testing.T) {
	t.Parallel()

	store := &stubStore{sess: validSession()}
	var c captured
	handler := Auth(store)(okHandler(&c))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	req.Header.Set("Authorization", "Bearer YSe0MW1ErH0cxUwVtwi_NiurXpWImzpMEEBEJP3bCNk")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !c.reached {
		t.Fatal("handler phía sau không được gọi")
	}
	if store.calls != 1 {
		t.Fatalf("kho phiên được gọi %d lần, want đúng 1 (một round trip mỗi request)", store.calls)
	}
	if store.gotRaw != "YSe0MW1ErH0cxUwVtwi_NiurXpWImzpMEEBEJP3bCNk" {
		t.Fatalf("credential chuyển tới kho phiên = %q, không khớp header", store.gotRaw)
	}
}

// AC-3: đây là bất biến quan trọng nhất của file này. Tầng SSE parse giá trị
// context thành UUID, nên đặt nhầm credential vào đó vừa làm hỏng realtime vừa
// rò bí mật xuống mọi tầng phía dưới.
func TestAuth_PutsSIDInContextNeverTheCredential(t *testing.T) {
	t.Parallel()

	const credential = "YSe0MW1ErH0cxUwVtwi_NiurXpWImzpMEEBEJP3bCNk"
	sess := validSession()
	store := &stubStore{sess: sess}
	var c captured
	handler := Auth(store)(okHandler(&c))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+credential)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !c.okUser || !c.okRole || !c.okSID {
		t.Fatalf("thiếu giá trị trong context: user=%v role=%v sid=%v", c.okUser, c.okRole, c.okSID)
	}
	if c.sid != sess.SID {
		t.Fatalf("SID trong context = %q, want %q", c.sid, sess.SID)
	}
	if c.sid == credential {
		t.Fatal("credential bị đặt vào context thay cho SID: rò bí mật và làm hỏng uuid.Parse ở tầng SSE")
	}
	if c.userID != sess.UserID {
		t.Fatalf("user_id = %q, want %q", c.userID, sess.UserID)
	}
	if c.role != sess.Role {
		t.Fatalf("role = %q, want %q", c.role, sess.Role)
	}
}

func TestAuth_RejectsBadOrMissingCredential(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		header     string
		storeErr   error
		wantStore  bool // có nên chạm tới kho phiên hay không
		wantStatus int
		wantCode   string
	}{
		{name: "không có header", header: "", wantStore: false, wantStatus: http.StatusUnauthorized, wantCode: "AUTHENTICATION_REQUIRED"},
		{name: "bearer sai định dạng", header: "NotBearer xyz", wantStore: false, wantStatus: http.StatusUnauthorized, wantCode: "AUTHENTICATION_REQUIRED"},
		{name: "scheme không có token", header: "Bearer", wantStore: false, wantStatus: http.StatusUnauthorized, wantCode: "AUTHENTICATION_REQUIRED"},
		{name: "credential không tồn tại", header: "Bearer deadbeef", storeErr: session.ErrNotFound, wantStore: true, wantStatus: http.StatusUnauthorized, wantCode: "AUTHENTICATION_REQUIRED"},
		{name: "credential đã hết hạn tuyệt đối", header: "Bearer deadbeef", storeErr: session.ErrNotFound, wantStore: true, wantStatus: http.StatusUnauthorized, wantCode: "AUTHENTICATION_REQUIRED"},
		// Redis hỏng KHÔNG phải lỗi xác thực. Trả 401 ở đây bảo client xoá
		// credential, nên một cú chớp của Redis đăng xuất vĩnh viễn mọi người dùng.
		{name: "Redis hỏng trả 503 chứ không phải 401", header: "Bearer deadbeef", storeErr: errors.New("dial tcp: connection refused"), wantStore: true, wantStatus: http.StatusServiceUnavailable, wantCode: "SESSION_STORE_UNAVAILABLE"},
		{name: "timeout của kho phiên cũng là lỗi hạ tầng", header: "Bearer deadbeef", storeErr: context.DeadlineExceeded, wantStore: true, wantStatus: http.StatusServiceUnavailable, wantCode: "SESSION_STORE_UNAVAILABLE"},
		{name: "lỗi bọc quanh ErrNotFound vẫn là 401", header: "Bearer deadbeef", storeErr: fmt.Errorf("get session: %w", session.ErrNotFound), wantStore: true, wantStatus: http.StatusUnauthorized, wantCode: "AUTHENTICATION_REQUIRED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &stubStore{sess: validSession(), err: tt.storeErr}
			var c captured
			handler := Auth(store)(okHandler(&c))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if c.reached {
				t.Fatal("handler phía sau được gọi dù xác thực thất bại")
			}
			if got := store.calls > 0; got != tt.wantStore {
				t.Fatalf("chạm kho phiên = %v, want %v (header hỏng thì không nên tốn một round trip)", got, tt.wantStore)
			}

			var body struct {
				Success bool `json:"success"`
				Error   struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body không phải JSON hợp lệ: %v", err)
			}
			if body.Success {
				t.Fatal("success = true trong phản hồi lỗi")
			}
			if body.Error.Code != tt.wantCode {
				t.Fatalf("error.code = %q, want %q", body.Error.Code, tt.wantCode)
			}
		})
	}
}

// Bảo mật: body lỗi không được chứa credential (nó sẽ đi vào log của proxy) và
// không được chứa chi tiết hạ tầng như host hay cổng.
//
// Chú ý một điều đã đổi: 401 và 503 CỐ Ý khác nhau. Bản đầu của test này ép hai
// phản hồi phải giống hệt nhau, với lý do là chống dò trạng thái hạ tầng. Đó là
// một đánh đổi sai: nó khoá lại đúng cái lỗi khiến một cú chớp Redis đăng xuất
// vĩnh viễn mọi người dùng, trong khi trạng thái Redis vốn đã công khai qua
// `/health/ready`. Client cần phân biệt được "phiên chết" với "hạ tầng lỗi".
func TestAuth_ErrorResponseLeaksNothing(t *testing.T) {
	t.Parallel()

	const credential = "YSe0MW1ErH0cxUwVtwi_NiurXpWImzpMEEBEJP3bCNk"

	for _, storeErr := range []error{session.ErrNotFound, errors.New("dial tcp 10.0.0.1:6379: connection refused")} {
		store := &stubStore{err: storeErr}
		handler := Auth(store)(okHandler(&captured{}))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
		req.Header.Set("Authorization", "Bearer "+credential)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		body := rec.Body.String()
		if strings.Contains(body, credential) {
			t.Fatalf("credential xuất hiện trong body lỗi: %s", body)
		}
		if strings.Contains(body, "connection refused") || strings.Contains(body, "10.0.0.1") || strings.Contains(body, "6379") {
			t.Fatalf("chi tiết hạ tầng rò ra body lỗi: %s", body)
		}
	}
}

func TestAuth_PassesRequestContextAndCurrentTimeToStore(t *testing.T) {
	t.Parallel()

	store := &stubStore{sess: validSession()}
	handler := Auth(store)(okHandler(&captured{}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	req.Header.Set("Authorization", "Bearer abc")
	req = req.WithContext(context.WithValue(req.Context(), contextKey("probe"), "carried"))

	before := time.Now()
	handler.ServeHTTP(httptest.NewRecorder(), req)
	after := time.Now()

	if store.gotCtxV != "carried" {
		// Context của request mang deadline và giá trị tracing; thay bằng
		// context.Background() sẽ làm request treo qua cả lúc client đã bỏ đi.
		t.Fatalf("context của request không được chuyển xuống kho phiên, nhận %v", store.gotCtxV)
	}
	if store.gotNow.Before(before) || store.gotNow.After(after) {
		t.Fatalf("now = %v, muốn nằm trong [%v, %v]", store.gotNow, before, after)
	}
}

func TestAuth_PanicsOnNilStore(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("Auth(nil) không panic: một kho phiên nil sẽ để mọi request đi qua mà không ai kiểm")
		}
	}()
	_ = Auth(nil)
}

func TestRequireRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		allowed    []string
		ctxRole    string
		hasRole    bool
		wantStatus int
		wantCode   string
	}{
		{name: "đúng vai trò thì đi tiếp", allowed: []string{"admin"}, ctxRole: "admin", hasRole: true, wantStatus: http.StatusOK},
		{name: "một trong nhiều vai trò được phép", allowed: []string{"admin", "staff"}, ctxRole: "staff", hasRole: true, wantStatus: http.StatusOK},
		{name: "sai vai trò trả 403", allowed: []string{"admin"}, ctxRole: "user", hasRole: true, wantStatus: http.StatusForbidden, wantCode: "INSUFFICIENT_PERMISSIONS"},
		{name: "không có vai trò trong context trả 401", allowed: []string{"admin"}, hasRole: false, wantStatus: http.StatusUnauthorized, wantCode: "AUTHENTICATION_REQUIRED"},
		{name: "vai trò rỗng không khớp gì", allowed: []string{"admin"}, ctxRole: "", hasRole: true, wantStatus: http.StatusForbidden, wantCode: "INSUFFICIENT_PERMISSIONS"},
		{name: "danh sách cho phép rỗng chặn tất cả", allowed: nil, ctxRole: "admin", hasRole: true, wantStatus: http.StatusForbidden, wantCode: "INSUFFICIENT_PERMISSIONS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var c captured
			handler := RequireRole(tt.allowed...)(okHandler(&c))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts", nil)
			if tt.hasRole {
				req = req.WithContext(context.WithValue(req.Context(), userRoleContextKey, tt.ctxRole))
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusOK {
				if !c.reached {
					t.Fatal("handler phía sau không được gọi")
				}
				return
			}
			if c.reached {
				t.Fatal("handler phía sau được gọi dù bị chặn")
			}

			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body không phải JSON hợp lệ: %v", err)
			}
			if body.Error.Code != tt.wantCode {
				t.Fatalf("error.code = %q, want %q", body.Error.Code, tt.wantCode)
			}
		})
	}
}

// RequireRole đứng sau Auth trong chuỗi middleware. Ghép hai lớp lại để chắc
// rằng vai trò Auth đặt vào context đúng là vai trò RequireRole đọc ra.
func TestAuthThenRequireRole_AdminSessionReachesAdminRoute(t *testing.T) {
	t.Parallel()

	sess := validSession()
	sess.Role = "admin"
	store := &stubStore{sess: sess}

	var c captured
	handler := Auth(store)(RequireRole("admin")(okHandler(&c)))

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/accounts/x/status", nil)
	req.Header.Set("Authorization", "Bearer admin-credential")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if c.role != "admin" {
		t.Fatalf("role = %q, want admin", c.role)
	}
}

func TestAuthThenRequireRole_UserSessionIsForbiddenOnAdminRoute(t *testing.T) {
	t.Parallel()

	store := &stubStore{sess: validSession()} // role "user"
	var c captured
	handler := Auth(store)(RequireRole("admin")(okHandler(&c)))

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/accounts/x/status", nil)
	req.Header.Set("Authorization", "Bearer user-credential")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if c.reached {
		t.Fatal("phiên thường vào được route admin")
	}
}

func TestWithAuthContext_RoundTrips(t *testing.T) {
	t.Parallel()

	ctx := WithAuthContext(context.Background(), "user-1", "sid-1", "admin")

	userID, ok := UserID(ctx)
	if !ok || userID != "user-1" {
		t.Fatalf("UserID = %q, %v; want user-1, true", userID, ok)
	}
	sid, ok := SessionID(ctx)
	if !ok || sid != "sid-1" {
		t.Fatalf("SessionID = %q, %v; want sid-1, true", sid, ok)
	}
	role, ok := UserRole(ctx)
	if !ok || role != "admin" {
		t.Fatalf("UserRole = %q, %v; want admin, true", role, ok)
	}
}

func TestAuthAccessors_ReportMissingOnBareContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if v, ok := UserID(ctx); ok || v != "" {
		t.Fatalf("UserID trên context trống = %q, %v; want \"\", false", v, ok)
	}
	if v, ok := UserRole(ctx); ok || v != "" {
		t.Fatalf("UserRole trên context trống = %q, %v; want \"\", false", v, ok)
	}
	if v, ok := SessionID(ctx); ok || v != "" {
		t.Fatalf("SessionID trên context trống = %q, %v; want \"\", false", v, ok)
	}
}
