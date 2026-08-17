# Module group v1

Tài liệu này giúp bạn đọc module group theo đúng luồng chạy. Xem [spec 0002](specs/0002-group-management-v1/index.md) để biết đầy đủ quyết định thiết kế và acceptance criteria.

## Group đang làm gì

Một user tạo nhóm chi tiêu VND, tự động trở thành Captain đang hoạt động. Captain tạo mã mời (24 giờ mặc định, 1 đến 168 giờ, tối đa 50 lượt dùng), người khác dùng mã để xem trước hoặc tham gia nhóm. Một nhóm tối đa 50 thành viên đang hoạt động và luôn có đúng một Captain đang hoạt động.

Rời nhóm hoặc bị Captain loại chỉ thành công khi không còn khoản nợ chưa tất toán, dù là người nợ hay người cho nợ. Captain không thể tự rời hay bị loại; phải chuyển vai trò Captain cho người khác trước.

Mọi thay đổi quan trọng (tạo nhóm, tạo hoặc thu hồi mã mời, tham gia, rời, loại thành viên, chuyển Captain) đều ghi một dòng vào `group_activities` trong cùng transaction với thay đổi đó.

## Group row lock: điểm tựa cho toàn bộ tính đúng đắn

Mọi thao tác ghi vào một nhóm đều bắt đầu bằng `SELECT id FROM groups WHERE id=$1 FOR UPDATE` (chuyển Captain dùng thêm `NOWAIT` để trả lỗi ngay thay vì xếp hàng chờ). Khóa này tuần tự hóa mọi mutation trên cùng nhóm: tạo mời, redeem mời, rời/loại thành viên, chuyển Captain đều không thể chạy chồng lên nhau. Đây là lý do capacity 50 thành viên và giới hạn use_count của mã mời không bao giờ bị vượt dù nhiều request tới cùng lúc.

Module bill và payment sau này khi tạo hoặc đổi công nợ chưa tất toán cũng phải khóa group row trước, nếu không việc rời nhóm có thể chạy đua với việc phát sinh nợ mới.

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

`groups`, `group_members`, `group_invites`, `group_activities` được định nghĩa từ migration đầu (`000001_init_schema.up.sql`). Migration `000002_group_management_v1.sql` thêm ràng buộc tên nhóm và currency, tám giá trị `activity_type` cho các sự kiện group, và các index seek theo spec 0002 (`idx_group_members_user_active`, `idx_groups_cursor`, `idx_group_invites_candidate`, `idx_group_activities_timeline`, `idx_debts_group_debtor_unsettled`, `idx_debts_group_creditor_unsettled`).

`debts` và view `v_member_balances` đã có sẵn từ trước, group module chỉ dùng chúng để tính `payable_amount`/`receivable_amount` khi rời nhóm, không sở hữu logic tạo nợ.
