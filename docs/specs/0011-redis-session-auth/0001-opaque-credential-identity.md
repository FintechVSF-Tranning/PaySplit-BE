# 0001. Opaque credential and session identity

## Summary

Credential mà client cầm và định danh phiên mà hệ thống dùng nội bộ là hai thứ khác nhau, cố tình tách rời. Credential là chuỗi đục ngẫu nhiên; định danh phiên vẫn là UUID trong `sessions.id`. Nhờ tách như vậy, toàn bộ tầng realtime không phải sửa dòng nào.

## Requirements

1. Credential sinh bằng `domain.NewOpaqueToken()`: CSPRNG 32 byte, mã hóa base64url, dài bốn mươi ba ký tự. Dùng lại hàm sẵn có, không viết hàm sinh mới.
2. Không được dùng `uuidv7()` làm credential. Định danh và bí mật là hai vai trò khác nhau và không được gộp.
3. `sessions.id` kiểu UUID vẫn là định danh phiên nội bộ, không đổi.
4. Middleware đọc field `sid` trong hash phiên rồi đặt vào context, và không bao giờ đặt credential vào context.
5. Ba context key hiện có được giữ nguyên tên và ý nghĩa: user ID, role, session ID.
6. Bộ phân tích header bearer được export thành `middleware.BearerToken` để handler đăng xuất dùng chung đúng một bộ.

## Decision

Credential chỉ đóng vai trò bí mật, không đóng vai trò định danh. Hash SHA-256 của nó là key trên Redis, còn giá trị `sid` nằm bên trong hash.

Lý do ràng buộc này tồn tại: tầng realtime yêu cầu session ID phải là UUID. `realtime.UserEnvelope.TargetSIDs` khai báo `[]uuid.UUID`, `EncodeSessionEnded` nhận `[]uuid.UUID`, và SSE handler gọi `uuid.Parse` trên giá trị lấy từ context. Một chuỗi base64url không phải UUID và sẽ làm cả đường đó vỡ.

Nhờ giữ `sid` là UUID, `internal/platform/realtime/`, SSE hub, SSE handler, và toàn bộ đường `pg_notify` tới force logout đều không phải đổi.

Việc export bộ phân tích bearer là quyết định về an toàn, không phải về gọn code: đăng xuất là route duy nhất không đi qua middleware xác thực, nên nếu nó tự phân tích header theo cách riêng thì hệ thống có hai bộ phân tích bearer khác nhau. Đó là một lỗi auth kinh điển.

## Build plan

1. Dùng lại `domain.NewOpaqueToken()` và `domain.HashToken()` làm nguồn sinh credential và nguồn băm.
2. Đặt `SID` bằng `sessions.id` khi tạo bản ghi phiên trên Redis.
3. Rút middleware còn một biến thể `Auth(store)`, bỏ `TokenVerifier`, `SessionValidator`, `TokenAuth`, và nhánh chỉ verify chữ ký.
4. Export `BearerToken` và cho handler đăng xuất dùng chung.
5. Cập nhật `WithAuthContext` theo, vì test đang dùng nó.

## Rollback

Không có rollback riêng. Quyết định này là tiền đề của cả spec 0011 và không tách rời được.
