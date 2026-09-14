package session

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Bất biến được canh ở đây (spec 0011, Key invariant 6):
//
//	Mọi thay đổi `users.role` hoặc `users.status` PHẢI kéo theo thu hồi phiên.
//
// Vì sao nó mong manh: bản ghi phiên trên Redis cache sẵn `role` để middleware
// không phải JOIN `users` mỗi request, và middleware cũng không còn đọc
// `users.status` nữa. Đổi một trong hai cột đó trong Postgres mà không chạm Redis
// nghĩa là giá trị cũ sống tiếp tới hết TTL: người vừa bị hạ quyền giữ quyền cũ,
// người vừa bị khoá vẫn dùng app, tới bảy ngày.
//
// Trước bản vá này bất biến chỉ là một dòng văn xuôi trong spec. Không có gì chặn
// người viết endpoint đổi vai trò trong tương lai quên nối vào đường thu hồi, và
// lỗi đó không làm đỏ một test nào. Test này là cái chốt đó: nó quét mã nguồn và
// đỏ lên ngay khi xuất hiện một chỗ ghi mới vào hai cột ấy.
//
// Nó cố tình KHÔNG cố chứng minh rằng chỗ ghi mới có thu hồi hay không, vì việc đó
// cần phân tích luồng gọi. Nó chỉ bắt người thêm code phải dừng lại, đọc, và tự
// khai báo. Một danh sách cho phép phải sửa bằng tay là đủ để không ai lỡ tay.
var usersColumnWriteAllowlist = map[string]string{
	// VerifyEmail nâng pending_verification lên active. Đây là đường CẤP quyền cho
	// một tài khoản chưa từng có phiên nào, nên không có gì để thu hồi.
	"internal/modules/auth/repository/postgres/repository.go": "VerifyEmail: pending_verification -> active, chưa có phiên nào tồn tại",

	// UpdateUserStatus của admin. Đường gọi UpdateAccountStatus thu hồi VÔ ĐIỀU
	// KIỆN qua SessionRevoker.RevokeUser và enqueue job session_redis_purge trong
	// cùng transaction. Xem TestUpdateAccountStatus... trong admin/usecase.
	"internal/modules/admin/repository/postgres/queries/admin.sql": "UpdateUserStatus: admin usecase thu hồi vô điều kiện sau khi commit",
	"internal/modules/admin/repository/postgres/sqlc/admin.sql.go": "bản sinh tự động của admin.sql ở trên",
}

// updateUsersPattern bắt phần SET của một lệnh UPDATE users, dừng ở WHERE, dấu
// chấm phẩy, hoặc backtick đóng chuỗi Go. (?is) cho phép khớp qua nhiều dòng.
var updateUsersPattern = regexp.MustCompile("(?is)UPDATE\\s+users\\s+SET\\s+(.*?)(?:\\s+WHERE\\b|;|`)")

// roleOrStatusColumn tìm `role` hoặc `status` đứng ở vế trái của một phép gán.
// Ràng buộc "theo sau là dấu bằng" loại bỏ `RETURNING role, status` và các cột
// khác có chứa hai từ này như `failed_login_count`.
var roleOrStatusColumn = regexp.MustCompile(`(?i)\b(role|status)\s*=`)

func TestRoleAndStatusWritesStayOnTheRevocationPath(t *testing.T) {
	root := repoRoot(t)

	var offenders []string
	for _, dir := range []string{"internal", "db"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".sql") {
				return nil
			}
			if strings.HasSuffix(name, "_test.go") {
				return nil
			}

			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)

			for _, match := range updateUsersPattern.FindAllStringSubmatch(string(body), -1) {
				if !roleOrStatusColumn.MatchString(match[1]) {
					continue
				}
				if _, allowed := usersColumnWriteAllowlist[rel]; allowed {
					continue
				}
				offenders = append(offenders, rel+": UPDATE users SET "+strings.TrimSpace(collapse(match[1])))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("quét %s: %v", dir, err)
		}
	}

	if len(offenders) > 0 {
		t.Fatalf(`phát hiện chỗ ghi mới vào users.role hoặc users.status:

  %s

Bản ghi phiên trên Redis cache sẵn `+"`role`"+`, và middleware không đọc `+"`users.status`"+`
nữa. Ghi vào hai cột đó mà không thu hồi phiên nghĩa là giá trị cũ còn hiệu lực tới
hết TTL trượt (mặc định bảy ngày).

Cách xử lý, chọn một:

  1. Nối chỗ ghi này vào đường thu hồi: gọi SessionRevoker.RevokeUser sau khi
     transaction commit, và enqueue job session_redis_purge trong chính transaction
     đó. Xem admin/usecase.UpdateAccountStatus làm mẫu.
  2. Nếu chỗ ghi này thật sự không cần thu hồi (ví dụ nó CẤP quyền cho tài khoản
     chưa có phiên nào), thêm file vào usersColumnWriteAllowlist trong file này
     kèm một câu giải thích vì sao.

Spec 0011, Key invariant 6.`, strings.Join(offenders, "\n  "))
	}
}

// TestAllowlistStaysHonest: một mục trong danh sách cho phép mà file đã biến mất
// là danh sách đang nói về quá khứ. Xoá nó đi, không thì lần sửa sau sẽ tin vào
// một lời bảo đảm không còn ai giữ.
func TestAllowlistStaysHonest(t *testing.T) {
	root := repoRoot(t)
	for rel := range usersColumnWriteAllowlist {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("usersColumnWriteAllowlist còn giữ %q nhưng file không tồn tại: %v", rel, err)
		}
	}
}

// repoRoot đi ngược lên tới thư mục có go.mod, thay vì hardcode "../../..", để
// test không vỡ nếu package được chuyển chỗ.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("không tìm thấy go.mod ở bất kỳ thư mục cha nào")
		}
		dir = parent
	}
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
