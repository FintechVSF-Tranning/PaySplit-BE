# 0012 · Lock path unconditional purge

**Status**: Assumed
**Date**: 2026-09-10
**Authorized by**: Lam Pham, during /develop

## Owed decision

Spec 0011 ghi một luật rõ: đường job retry phải khớp `sid` trước khi xóa (AC-12,
key invariant 9, và [child spec 0003](0011-redis-session-auth/0003-revocation-backstop.md)
mục "Hướng sửa đề xuất"). Lý do là chống giết nhầm một phiên mà người dùng vừa
tạo lại.

Luật đó không giải quyết được ca khóa lại. Khi admin khóa một tài khoản mà hàng
`sessions` đã revoked từ trước, lệnh `UPDATE sessions ... WHERE revoked_at IS NULL
RETURNING id` khớp không hàng nào, danh sách sid rỗng, và `EnqueueTx` tự thoát
sớm. Không job nào được đặt vào hàng đợi. Dưới luật khớp `sid`, không job nào có
thể tồn tại ở ca này, vì không có sid để mang theo.

Quyết định chưa từng được đưa ra: đường khóa tài khoản có được phép dùng một job
thu hồi vô điều kiện hay không, và nếu có thì vì sao điều đó không phá tính chất
mà AC-12 bảo vệ.

Lỗ hổng do `/check review session revocation hardening` ngày 2026-09-10 tìm ra,
ghi tại [docs/reviews/2026-09-10-lampt-auth-account-v2.md](../reviews/2026-09-10-lampt-auth-account-v2.md).

## Assumption built on

Đường khóa và đình chỉ tài khoản được phép enqueue một job thu hồi **vô điều
kiện**, nghĩa là job gọi `RevokeUser(user_id)` thay vì `RevokeUserSIDs(user_id, sids)`.

Giả định làm việc đó an toàn: một tài khoản không ở trạng thái `active` không thể
tạo phiên mới. `CreateSession` từ chối với `ErrAccountUnavailable` khi
`user.Status != domain.StatusActive`
(`internal/modules/auth/repository/postgres/repository.go`). Nên ở đường này
không tồn tại "phiên mới mà người dùng vừa tạo lại" để giết nhầm, và tính chất mà
AC-12 bảo vệ không bị mất đi thứ gì.

Phạm vi nới lỏng chỉ đúng một call site. Mọi đường thu hồi khác (đặt lại mật khẩu,
thay thế phiên khi đăng nhập lại, đăng xuất) giữ nguyên việc khớp `sid`, vì ở đó
tài khoản vẫn `active` và người dùng thực sự có thể đã đăng nhập lại.

Hệ quả nếu giả định sai: nếu sau này có đường nào cho phép một tài khoản không
`active` tạo phiên, job vô điều kiện có thể giết một phiên hợp lệ. Ràng buộc cần
giữ nằm ở `CreateSession`, không ở đây.

## Code area

- `internal/modules/auth/jobs/session_purge.go`, thêm chế độ vô điều kiện cho job
  và cho enqueuer
- `internal/modules/admin/repository/postgres/repository.go`, enqueue job ở đường
  khóa và đình chỉ bất kể `UPDATE sessions` khớp bao nhiêu hàng
- `internal/modules/admin/repository/repository.go`, sửa comment đã trôi dạt trên
  interface

## Requirements

Kế thừa từ mệnh đề "Done when" của mục 11 trong `docs/scope/scope.md`, phần liên
quan tới backstop:

1. Một lần khóa tài khoản luôn để lại một job `session_redis_purge` trong cùng
   transaction, kể cả khi không hàng `sessions` nào khớp. Đây là bất biến mà
   `AGENTS.md` đang phát biểu, và hiện code không giữ đúng ở ca khóa lại.
2. Job của đường khóa thu hồi theo user, không khớp `sid`.
3. Mọi đường thu hồi khác vẫn enqueue job khớp `sid`, giữ nguyên AC-12.
4. Job của đường mở lại tài khoản (`active`) không được tồn tại, vì mở lại không
   phải là thu hồi.

## Ratify

This decision was recorded by /develop, not deliberated. Run
`/architect session revocation hardening` to deliberate and ratify it. Until then
it stays flagged as an owed decision; it does not block marking the feature `done`.

Điểm cần bàn khi phê duyệt: có nên nâng "tài khoản không active thì không tạo được
phiên" thành một bất biến được test tường minh hay không, vì job vô điều kiện ở
đây đang dựa vào nó.
