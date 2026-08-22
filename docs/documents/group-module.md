# Module group v1

Tài liệu này giúp bạn đọc module group theo đúng luồng chạy. Xem [spec 0002](specs/0002-group-management-v1/index.md) để biết đầy đủ quyết định thiết kế và acceptance criteria.

## Group đang làm gì

Một user tạo nhóm chi tiêu VND, tự động trở thành Captain đang hoạt động. Bất kỳ thành viên đang hoạt động nào cũng có thể gọi tạo mã mời: mặc định là dùng lại mã còn hiệu lực (24 giờ, không giới hạn lượt dùng nếu tạo mới), còn việc cấu hình `expires_in_hours` (1 đến 168), `max_uses` (1 đến 50) hay `regenerate` chỉ dành riêng cho Captain — thành viên thường gửi kèm bất kỳ field cấu hình nào đều bị từ chối `403 CAPTAIN_REQUIRED`. Người khác dùng mã để xem trước hoặc tham gia nhóm. Một nhóm tối đa 50 thành viên đang hoạt động và luôn có đúng một Captain đang hoạt động.

Rời nhóm hoặc bị Captain loại chỉ thành công khi không còn khoản nợ chưa tất toán, dù là người nợ hay người cho nợ. Captain không thể tự rời hay bị loại; phải chuyển vai trò Captain cho người khác trước.

Captain còn có thể đổi tên nhóm (`PATCH /groups/{id}`) và giải tán nhóm (`DELETE /groups/{id}`). Giải tán từ chối bằng `409 GROUP_HAS_UNSETTLED_OBLIGATIONS` nếu còn bill chưa `finalized`/`voided` hoặc debt chưa `settled`/`voided`; khi thành công, nó chuyển `groups.status` sang `archived`, vô hiệu hóa mọi membership đang hoạt động, thu hồi mọi invite còn khả dụng, và ghi activity `group_archived` — không xóa lịch sử bill/debt/payment. Một nhóm đã `archived` không còn view riêng: mọi route phạm vi nhóm đó, kể cả đọc, trả về y hệt như với người không phải thành viên (`404 GROUP_NOT_FOUND` hoặc `403 FORBIDDEN`).

Mọi thay đổi quan trọng (tạo nhóm, tạo hoặc thu hồi mã mời, tham gia, rời, loại thành viên, chuyển Captain, đổi tên, giải tán) đều ghi một dòng vào `group_activities` trong cùng transaction với thay đổi đó.

## Mã mời Base62 và chia sẻ theo thành viên

Mã mời là đúng tám ký tự Base62 phân biệt hoa thường (`^[A-Za-z0-9]{8}$`), sinh bằng lấy mẫu không lệch từ `crypto/rand` (loại bỏ byte 248-255 trước khi lấy modulo 62 vì 248 là bội số lớn nhất của 62 biểu diễn được trong một byte — xem `domain.NewInviteCode`). Trùng mã (khó xảy ra nhưng ràng buộc unique DB vẫn bắt) khiến usecase thử lại ở một transaction boundary mới, tối đa `maxInviteCodeAttempts` lần.

`invite_url` là `APP_INVITE_BASE_URL` cộng thẳng mã làm path segment cuối (`url.JoinPath`), dùng chung cho cả liên kết dán vào app lẫn nhập tay. Route xem trước và tham gia (`GET /groups/invites/{code}`, `POST /groups/join`) dùng chung một giới hạn tần suất theo phút UTC cố định (`HTTP_RATE_LIMIT_REQUESTS_PER_MINUTE`), khóa độc lập theo cả tài khoản đăng nhập lẫn địa chỉ IP TCP trực tiếp — middleware này không tin header forwarding trong v1.

## Group row lock: điểm tựa cho toàn bộ tính đúng đắn

Mọi thao tác ghi vào một nhóm đều bắt đầu bằng khóa row của `groups` (chuyển Captain dùng thêm `NOWAIT` để trả lỗi ngay thay vì xếp hàng chờ). Khóa này tuần tự hóa mọi mutation trên cùng nhóm: tạo mời, redeem mời, rời/loại thành viên, chuyển Captain, đổi tên, giải tán đều không thể chạy chồng lên nhau. Đây là lý do capacity 50 thành viên và giới hạn use_count của mã mời không bao giờ bị vượt dù nhiều request tới cùng lúc.

Từ khi có group governance (spec 0002 AC-9 mở rộng), khóa này được rút thành helper dùng chung `database.LockActiveGroup` / `LockActiveGroupNowait` (`internal/platform/database/group_lock.go`): `SELECT id FROM groups WHERE id=$1 AND status='active' FOR UPDATE [NOWAIT]`, trả `database.ErrGroupNotActive` nếu nhóm không tồn tại hoặc đã `archived`. Module bill (`internal/modules/bill/repository/postgres/repository.go`, các hàm tạo/sửa/review/finalize bill) và module settlement đều gọi helper này trước khi ghi, nên một nhóm đã giải tán ngay lập tức chặn mọi write bill/settlement mới, không chỉ write group.

## Membership là anchor, không phải user

`group_members` đóng vai trò tương tự user trong các bảng phạm vi nhóm: bill, debt, payment, activity đều trỏ về `group_members.id` chứ không trỏ thẳng `users.id`. Nhờ vậy khi một người rời rồi tham gia lại, `UPDATE group_members SET status='active', left_at=NULL WHERE id=...` giữ nguyên `id` cũ, lịch sử nợ không bị đứt gãy. Unique `(group_id, user_id)` khiến việc tham gia lại luôn phải là UPDATE, không được INSERT dòng mới.

## Mã mời là dữ liệu nhạy cảm

Mã mời (`group_invites.code`) chỉ xuất hiện trong response khi tạo hoặc dùng lại mã, và trong query string của endpoint xem trước (`GET /groups/invites/{code}`). Nó không bao giờ được ghi vào `group_activities.metadata`, log lỗi, hay access log. Router dùng `internal/transport/http/middleware/logging.go` thay cho logger mặc định của chi để tự động che giá trị này trong access log; xem file đó nếu bạn thêm một endpoint khác cũng nhận giá trị nhạy cảm qua path.

## Một request đi qua code như thế nào

```text
HTTP request
    ↓
delivery/http
    ↓
usecase.Service
    ↓
repository.Repository (postgres adapter, sqlc)
    ↓
PostgreSQL
```

Giống module auth: `delivery/http` chỉ đọc request, lấy user từ middleware, gọi usecase rồi map domain error sang HTTP. `usecase` giữ validation và thứ tự nghiệp vụ (ví dụ tạo mời kiểm tra range trước khi sinh code). `repository/postgres` sở hữu toàn bộ SQL, transaction và activity insert.

Enum Postgres như `group_role`, `member_status`, `activity_type` được tạo bằng `DO $$ ... EXCEPTION WHEN duplicate_object ... $$` nên sqlc không nhận diện được kiểu, luôn sinh ra `interface{}`. Repository chuyển các cột này bằng `fmt.Sprint(...)`; không parameterize trực tiếp một cột enum, luôn viết literal `'member'`, `'captain'` ngay trong câu SQL.

## Cursor pagination

List groups và activity timeline dùng chung một dạng cursor: base64url của `"<created_at RFC3339Nano>|<id>"`, seek bằng so sánh tuple `(created_at, id) < (cursor_created_at, cursor_id)`. Hàm `encodeCursor`/`decodeCursor` nằm trong `repository/postgres/repository.go`, dùng lại cho cả hai endpoint.

## Các bảng group

`groups`, `group_members`, `group_invites`, `group_activities` được định nghĩa từ migration đầu (`000001_init_schema.up.sql`). Migration `000002_group_management_v1.sql` thêm ràng buộc tên nhóm và currency, tám giá trị `activity_type` cho các sự kiện group, và các index seek theo spec 0002 (`idx_group_members_user_active`, `idx_groups_cursor`, `idx_group_invites_candidate`, `idx_group_activities_timeline`, `idx_debts_group_debtor_unsettled`, `idx_debts_group_creditor_unsettled`). Migration `000008_group_exit_voided_debt_fix.sql` sửa các query loại trừ debt `voided` khỏi điều kiện chặn rời nhóm.

Migration `000011_group_governance_invites_v1.sql` (governance + invite chia sẻ theo thành viên, spec 0002 AC-3 và AC-9 đến AC-12) thêm enum `group_status` (`active`/`archived`) và cột `groups.status`, hai giá trị `activity_type` mới `group_renamed`/`group_archived`, và xoay toàn bộ `group_invites.code` cũ sang namespace `legacy-*` đã thu hồi trước khi đổi định dạng (migration này yêu cầu dừng traffic/worker trong lúc chạy vì code cũ bị vô hiệu hóa). Migration này cũng có một preflight kiểm tra mọi nhóm đang có đúng một Captain đang hoạt động trước khi cho phép chạy tiếp.

`debts` và view `v_member_balances` đã có sẵn từ trước, group module chỉ dùng chúng để tính `payable_amount`/`receivable_amount` khi rời nhóm, không sở hữu logic tạo nợ.
