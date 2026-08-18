# Module admin v1

Tài liệu này giúp bạn đọc module admin theo đúng luồng chạy và nắm rõ các quyết định thiết kế, kiểm toán bảo mật cùng hệ thống giám sát vận hành. Xem [spec 0005](specs/0005-admin-v1/index.md) để biết đầy đủ quyết định thiết kế và acceptance criteria.

## Admin đang làm gì

Module admin cung cấp công cụ quản trị người dùng và giám sát nền tảng cho người vận hành hệ thống:

1. **Tìm kiếm và lọc danh sách tài khoản**: Cho phép tìm kiếm theo email, tên hiển thị hoặc số điện thoại, lọc theo trạng thái (`pending_verification`, `active`, `suspended`, `locked`) và vai trò (`user`, `admin`), sắp xếp linh hoạt và phân trang an toàn. Khi tham số `limit` vượt quá 100, hệ thống tự động giới hạn về 100 và phản hồi trong metadata phân trang.
2. **Tra cứu hồ sơ chi tiết và che dữ liệu nhạy cảm**: Trả về thông tin tài khoản đầy đủ, số lần đăng nhập thất bại, số session đang hoạt động, danh sách nhóm tham gia, tóm tắt công nợ (nợ phải trả và khoản phải thu), 10 bản ghi kiểm toán gần nhất. Số tài khoản ngân hàng luôn được che và chỉ hiển thị 4 chữ số cuối (ví dụ `******1234`).
3. **Thay đổi trạng thái tài khoản và thu hồi phiên tức thời**: Admin có thể đình chỉ (`suspended`), khóa (`locked`) hoặc kích hoạt lại (`active`) tài khoản người dùng kèm lý do ghi nhật ký kiểm toán. Khi chuyển sang `suspended` hoặc `locked`, toàn bộ session và refresh token của người dùng đó bị thu hồi ngay lập tức trong cùng database transaction.
4. **Thống kê tổng quan hệ thống**: Tổng hợp số lượng người dùng theo trạng thái, số nhóm, hóa đơn, công nợ, hàng đợi dọn dẹp file rác còn tồn đọng (`media_cleanup_jobs`), tiến độ xử lý tác vụ OCR (`ocr_jobs`), và số liệu tài nguyên Go runtime (goroutines, bộ nhớ phân bổ, thời gian uptime).
5. **Giám sát sức khỏe và Prometheus metrics**: Cung cấp health check phân tách giữa liveness và readiness (active ping tới PostgreSQL), cùng endpoint `/metrics` chuẩn hóa theo định dạng Prometheus.

## Nguyên tắc bất biến và mô hình bảo mật

- **Xác thực phiên thật và vai trò admin**: Toàn bộ endpoint `/api/v1/admin/*` được bảo vệ bởi middleware `transportmw.Auth` (xác thực access token JWT cùng session đang hoạt động trong database) và `transportmw.RequireRole("admin")`. Người dùng thường hoặc tài khoản chưa kích hoạt đều bị từ chối truy cập.
- **Tự bảo vệ (Self protection)**: Admin không thể tự đình chỉ, khóa hoặc thay đổi trạng thái tài khoản của chính mình (`403 CANNOT_MODIFY_SELF`).
- **Bảo vệ quyền admin (Admin protection)**: Admin không thể đình chỉ hoặc khóa tài khoản của một admin khác (`403 CANNOT_MODIFY_ADMIN`). Việc phân quyền hoặc hạ quyền admin do quản trị viên cơ sở dữ liệu thực hiện trực tiếp.
- **Bảo vệ luồng xác minh email**: Tài khoản ở trạng thái `pending_verification` không thể chuyển đổi trạng thái qua API này (`400 INVALID_STATUS_TRANSITION`), người dùng bắt buộc phải hoàn tất xác minh qua email.
- **Đóng cửa sổ TOCTOU (Time of check to time of use)**: Các điều kiện tự bảo vệ, bảo vệ admin và kiểm tra trạng thái mục tiêu được thực thi trực tiếp bên trong cùng database transaction với câu lệnh cập nhật và thu hồi session. Điều này ngăn chặn xung đột nếu hai admin cùng thao tác một tài khoản tại một thời điểm.
- **Không rò rỉ dữ liệu bí mật**: DTO và repository adapter loại bỏ hoàn toàn `password_hash`, token hash và số tài khoản ngân hàng đầy đủ khỏi phản hồi API.

## Một request đi qua code như thế nào

```text
HTTP request
    ↓
middleware (liveAuth, RequireRole("admin"))
    ↓
delivery/http (DTO decoding, UUID validation, error mapping)
    ↓
usecase.Service (business rules, default reason for reactivation)
    ↓
repository.Repository (interface)
    ↓
repository/postgres (adapter, sqlc, transaction, session revocation)
    ↓
PostgreSQL 18
```

- `delivery/http`: Đọc query params, giải mã JSON payload, kiểm tra định dạng UUID, gọi usecase và chuyển đổi domain error sang mã lỗi API chuẩn hóa (như `VALIDATION_FAILED`, `ACCOUNT_NOT_FOUND`, `CANNOT_MODIFY_SELF`, `CANNOT_MODIFY_ADMIN`).
- `usecase`: Giữ quy tắc nghiệp vụ tầng ứng dụng. Khi kích hoạt lại tài khoản (`active`) mà client không truyền lý do, usecase tự động gán lý do mặc định `reactivated by admin` để thỏa mãn ràng buộc database audit log.
- `repository/postgres`: Sở hữu các câu lệnh SQL sinh bởi sqlc và transaction. Khi thay đổi trạng thái sang suspended/locked, adapter cập nhật `users.status`, thu hồi `sessions` (`revoked_reason = 'admin_' || status`), thu hồi `session_refresh_tokens`, ánh xạ enum `admin_action` (`suspend`, `lock`, `reactivate`) và ghi `admin_audit_logs`.
- `bootstrap/app.go`: Khởi tạo admin repository, usecase service, HTTP handler và gắn route vào router chính.

## Prometheus Metrics và Health Probes

Hệ thống cung cấp khả năng quan sát vận hành tại tầng transport và platform:

- `HTTPMetricsMiddleware`: Ghi nhận counter `paysplit_http_requests_total` (gắn nhãn method, route pattern, status code) và histogram `paysplit_http_request_duration_seconds` cho từng request HTTP.
- **Database connection gauges**: Đo lường trạng thái kết nối PostgreSQL qua `paysplit_db_pool_acquired_conns`, `paysplit_db_pool_idle_conns`, và `paysplit_db_pool_total_conns`.
- `GET /metrics`: Phục vụ scraper Prometheus thu thập số liệu. Endpoint có thể bật tắt qua `METRICS_ENABLED` và bảo vệ bằng `METRICS_BEARER_TOKEN` trong cấu hình môi trường.
- `GET /health/live`: Liveness probe trả về `200 {"status": "ok"}` khi tiến trình HTTP server đang chạy.
- `GET /health/ready`: Readiness probe thực hiện ping kiểm tra kết nối database (`pgxpool.Ping`). Trả về `200 {"status": "ready", "database": "ok"}` khi sẵn sàng, hoặc `503 {"status": "degraded", "database": "down"}` nếu mất kết nối tới cơ sở dữ liệu.

## Các bảng dữ liệu liên quan

- `users`: Quản lý trạng thái tài khoản người dùng (`active`, `suspended`, `locked`, `pending_verification`) và vai trò (`user`, `admin`).
- `sessions`: Phiên làm việc của người dùng, bị thu hồi (`revoked_at = now()`) khi tài khoản bị đình chỉ hoặc khóa.
- `session_refresh_tokens`: Token làm mới phiên, bị thu hồi đồng thời với session.
- `admin_audit_logs`: Lưu vết toàn bộ tác động quản trị gồm `admin_id`, `target_user_id`, `action`, `reason`, `created_at`. Migration `000004_admin_v1.sql` bổ sung index `idx_admin_audit_admin(admin_id, created_at DESC)` kết hợp với `idx_admin_audit_target` có sẵn.
- `groups`, `group_members`, `debts`, `bills`, `media_cleanup_jobs`, `ocr_jobs`: Cung cấp số liệu tổng hợp cho endpoint overview và cảnh báo nghĩa vụ tài chính chưa tất toán (`unsettled_debts_count`, `unsettled_credits_count`).

## Danh sách API Endpoints

| Method | Đường dẫn | Chức năng | Quyền hạn |
|---|---|---|---|
| `GET` | `/api/v1/admin/accounts` | Tìm kiếm, lọc và phân trang danh sách tài khoản | Admin (Live Session) |
| `GET` | `/api/v1/admin/accounts/{id}` | Xem chi tiết hồ sơ tài khoản và dữ liệu kiểm toán | Admin (Live Session) |
| `PUT` | `/api/v1/admin/accounts/{id}/status` | Cập nhật trạng thái tài khoản và thu hồi phiên | Admin (Live Session) |
| `GET` | `/api/v1/admin/system/overview` | Thống kê số liệu tổng quan hệ thống và runtime | Admin (Live Session) |
| `GET` | `/health` | Kiểm tra trạng thái cơ bản của dịch vụ | Public |
| `GET` | `/health/live` | Liveness probe cho container orchestrator | Public |
| `GET` | `/health/ready` | Readiness probe kiểm tra kết nối database | Public / Internal |
| `GET` | `/metrics` | Xuất dữ liệu metrics định dạng Prometheus | Internal / Bearer Token |
