# Module Auth & Account v1

Tài liệu này mô tả chi tiết kiến trúc, mô hình dữ liệu, cơ chế bảo mật và luồng xử lý của module Auth & Account (Xác thực & Quản lý tài khoản) trong PaySplit Backend API.

---

## 1. Tổng quan chức năng

Module Auth & Account v1 chịu trách nhiệm:
1. **Đăng ký tài khoản (Sign Up)**: Tiếp nhận email, số điện thoại Việt Nam (chuẩn hóa E.164), tên hiển thị và mật khẩu (8-72 bytes, gồm chữ hoa, chữ thường và chữ số). Tạo tài khoản ở trạng thái `pending_verification` và phát hành mã OTP 6 chữ số gửi qua Gmail SMTP.
2. **Xác thực Email (Email Verification)**: Xác thực bằng mã OTP 6 chữ số trong vòng 10 phút, kích hoạt tài khoản sang trạng thái `active`.
3. **Đăng nhập & Phiên duy nhất (Sign In & Single Device Enforcement)**: Đăng nhập bằng email và mật khẩu kèm `device_id`. Khi đăng nhập thành công trên thiết bị mới, mọi phiên (`sessions`) cũ của người dùng tự động bị thu hồi ngay lập tức.
4. **Token JWT & Refresh Token Rotation**:
   * **Access Token**: JWT ký bằng HS256, có hiệu lực 15 phút, mang claim `sid` (Session ID), `sub` (User ID) và `role`.
   * **Refresh Token**: Chuỗi ngẫu nhiên bảo mật (opaque token 32 bytes), chỉ lưu mã băm SHA-256 trong database, có thời hạn tối đa 7 ngày kể từ lúc đăng nhập. Refresh token được rotate (đổi mới) sau mỗi lần sử dụng; nếu phát hiện refresh token cũ đã dùng được gửi lại (replay attack), toàn bộ session sẽ bị thu hồi ngay lập tức (`refresh_reuse`).
5. **Khôi phục mật khẩu (Forgot/Reset Password)**: Gửi mã OTP 6 chữ số qua email để đặt lại mật khẩu. Khi reset mật khẩu thành công, toàn bộ session và refresh token của người dùng bị thu hồi ngay trong cùng một transaction.
6. **Đổi mật khẩu (Change Password)**: Yêu cầu mật khẩu hiện tại và mật khẩu mới. Đổi mật khẩu thành công sẽ giữ lại phiên hiện tại và thu hồi tất cả các phiên khác.
7. **Hồ sơ & Tài khoản ngân hàng (Profile & Bank Info)**: Xem và cập nhật thông tin cá nhân (`display_name`, `phone_number`, tài khoản ngân hàng mặc định nhận tiền). Kiểm tra tính hợp lệ của ngân hàng qua danh mục VietQR tích hợp sẵn.
8. **Ảnh đại diện (Avatar Processing & Storage)**: Tiếp nhận ảnh tải lên (tối đa 10 MB), backend tự động xoay theo EXIF, resize cạnh dài tối đa 1024 px và chuyển đổi sang định dạng WebP (quality 82). Hỗ trợ fallback sang Cloudinary nếu định dạng không được backend hỗ trợ cục bộ (ví dụ HEIC). Thu hồi và xóa ảnh cũ qua hàng đợi dọn dẹp bền vững (`media_cleanup_jobs`).

---

## 2. Cơ chế xác thực Email & Quên mật khẩu bằng mã OTP 6 chữ số

Hệ thống sử dụng mã OTP 6 chữ số dạng số (`000000` đến `999999`) cho cả hai luồng xác thực email và đặt lại mật khẩu:

```text
1. Phát sinh OTP:
   crypto/rand.Int (đồng đều tuyệt đối, không có modulo bias)
        ↓
   OTP hiển thị (6 chữ số) ──(Gửi qua Gmail SMTP)──> Email người dùng
        ↓
   SHA-256 (Hash) ──(Lưu trữ bảo mật)──> Bảng `user_tokens` (expires_at = now + 10m, attempt_count = 0)

2. Xác thực OTP:
   Người dùng gửi email + OTP
        ↓
   subtle.ConstantTimeCompare (so sánh an toàn chống side-channel)
   ├── Khớp OTP:
   │    ├── pending_verification → chuyển status sang active, đánh dấu token `used_at = now()`
   │    └── active (idempotent) → kiểm tra khớp với token đã dùng gần nhất, trả về thành công
   └── Không khớp OTP:
        ├── attempt_count++
        ├── Nếu attempt_count >= 5: đánh dấu token `superseded_at = now()` (vô hiệu hóa hoàn toàn)
        └── Trả về lỗi 400 INVALID_OR_EXPIRED_TOKEN
```

### Đặc điểm bảo mật của OTP:
* **Chống Brute Force**: Cột `attempt_count` trong bảng `user_tokens` đếm số lần thử sai. Sau 5 lần nhập sai, token lập tức bị vô hiệu hóa (`superseded_at = now()`).
* **Chống Email Enumeration**: Các endpoint `resend-verification` và `forgot-password` luôn trả về HTTP 202 Accepted với cùng một thông điệp chung dù tài khoản có tồn tại trong hệ thống hay không. Khi gọi `verify-email` với mã OTP sai trên tài khoản đã active, hệ thống trả về lỗi 400 như thông thường thay vì tiết lộ trạng thái tài khoản.
* **Tính Idempotent (Idempotency)**: Nếu người dùng nhấn xác thực nhiều lần với cùng một mã OTP hợp lệ khi tài khoản đã kích hoạt, backend kiểm tra mã OTP với token đã tiêu thụ (`used_at IS NOT NULL`) trong cửa sổ lưu trữ để trả về 200 OK một cách an toàn.
* **Giới hạn tần suất gửi (Rate Limiting)**:
  * Đăng ký (`sign_up`): Tối đa 10 request/giờ trên mỗi IP.
  * Gửi lại OTP (`resend_verification`, `forgot_password`): Tối đa 1 request/phút và 10 request/giờ trên từng chiều độc lập: địa chỉ email và địa chỉ IP. Dữ liệu rate limit được lưu trữ bền vững trong bảng `auth_rate_limit_events` với khóa băm SHA-256 và khóa giao dịch bằng PostgreSQL advisory locks.

---

## 3. Quản lý `device_id`, `device_name` và `fcm_token`

* **`device_id`**: Ứng dụng Flutter tự sinh một chuỗi UUID chuẩn khi ứng dụng được cài đặt lần đầu và lưu trong Flutter Secure Storage. `device_id` được gửi kèm trong request `sign-in` và `refresh`. Hệ thống không sử dụng các định danh phần cứng nhạy cảm (như IMEI hay MAC address).
* **`device_name`**: Tên hiển thị của thiết bị do hệ điều hành cung cấp (ví dụ `iPhone 16 Pro, iOS 18.2`), hỗ trợ tối đa 120 ký tự.
* **`fcm_token`**: Token đăng ký nhận thông báo đẩy Firebase Cloud Messaging. Có thể gửi kèm ngay lúc đăng nhập (`sign-in`) hoặc cập nhật sau qua endpoint `PUT /api/v1/users/me/fcm-token`.
* **Thu hồi phiên khi đăng nhập mới**: Mỗi user chỉ có tối đa 1 session hoạt động (`revoked_at IS NULL`). Khi người dùng đăng nhập trên thiết bị mới hoặc cài lại ứng dụng (sinh `device_id` mới), session cũ bị đánh dấu `revoked_reason = 'replaced_by_sign_in'`.

---

## 4. Kiến trúc phân lớp của một Request

```text
HTTP Request (chi Router)
    ↓
Middleware: RateLimit → Timeout (15s) → Auth (Token / Live Session Verification)
    ↓
delivery/http (Handler & DTOs)
    ├── Đọc JSON (giới hạn 64 KB, cấm trường lạ)
    ├── Trích xuất User ID & Session ID từ Context
    └── Giao tiếp với usecase và chuyển đổi domain error sang HTTP JSON error
    ↓
usecase.Service (Application Service)
    ├── Thực thi business logic theo thứ tự nghiêm ngặt
    ├── Gọi PasswordManager, TokenIssuer, Mailer, BankDirectory, ImageProcessor, AvatarStorage
    └── Gọi repository.Repository (Port Interface)
    ↓
repository/postgres (Adapter Layer)
    ├── Quản lý Database Transactions & Row Locks (SELECT FOR UPDATE)
    ├── Thực thi SQL Queries / sqlc-generated queries
    └── Chuyển đổi giữa database models và domain entities
    ↓
PostgreSQL 18 Pool (pgxpool)
```

* **Nguyên tắc Clean Architecture**:
  * `domain/`: Chỉ chứa plain Go structs (`User`, `Session`, `BankProfile`), constants và các sentinel errors (`ErrInvalidCredentials`, `ErrInvalidOrExpiredToken`, v.v.). Hoàn toàn không import bất kỳ package bên ngoài nào.
  * `usecase/`: Chứa nghiệp vụ ứng dụng và định nghĩa các interface phụ thuộc (`PasswordManager`, `TokenIssuer`, `Mailer`, `AvatarStorage`, `BankDirectory`, `ImageProcessor`). Các lời gọi mạng ra bên ngoài (Gmail SMTP, Cloudinary API) **không bao giờ** được chạy bên trong database transaction.
  * `repository/postgres/`: Chịu trách nhiệm thực thi câu lệnh SQL, đảm bảo tính toàn vẹn dữ liệu và xử lý tranh chấp (concurrency locks).
  * `internal/bootstrap/app.go`: Nơi duy nhất khởi tạo các implementation cụ thể và tiêm phụ thuộc (dependency injection) cho toàn bộ ứng dụng.

---

## 5. Các bảng dữ liệu của Module Auth

### `users`
Lưu trữ thông tin danh tính, mật khẩu băm, thông tin ngân hàng và trạng thái khóa đăng nhập:
* `id` (UUID v7, khóa chính mặc định `uuidv7()`)
* `email` (CITEXT, duy nhất, không phân biệt hoa thường)
* `password_hash` (TEXT, bcrypt hash)
* `display_name` (TEXT, 1-100 ký tự)
* `phone_number` (TEXT, duy nhất, định dạng E.164 Việt Nam)
* `avatar_object_key` (TEXT, public ID lưu trên Cloudinary)
* `default_bank_code`, `default_bank_account_number`, `default_bank_account_holder` (Thông tin tài khoản ngân hàng mặc định)
* `role` (`user_role`: `'user'`, `'admin'`)
* `status` (`account_status`: `'pending_verification'`, `'active'`, `'suspended'`, `'locked'`)
* `email_verified_at` (TIMESTAMPTZ, thời điểm xác thực email thành công)
* `failed_login_count`, `failed_login_window_started_at`, `login_blocked_until` (Theo dõi và khóa tạm thời 15 phút khi đăng nhập sai 5 lần liên tiếp)

### `sessions`
Quản lý phiên làm việc của từng thiết bị:
* `id` (UUID v7, khóa chính)
* `user_id` (UUID, tham chiếu `users(id)` ON DELETE CASCADE)
* `device_id` (UUID của thiết bị)
* `device_name` (TEXT, tên thiết bị)
* `fcm_token` (TEXT, token push notification của thiết bị)
* `issued_at`, `expires_at` (Thời điểm tạo và thời điểm hết hạn 7 ngày)
* `revoked_at`, `revoked_reason` (Thời điểm và lý do thu hồi: `'replaced_by_sign_in'`, `'sign_out'`, `'refresh_reuse'`, `'password_reset'`, v.v.)
* *Index*: Partial Unique Index `uq_sessions_one_active_per_user` trên `(user_id) WHERE revoked_at IS NULL` đảm bảo mỗi user chỉ có đúng 1 session hoạt động tại một thời điểm.

### `session_refresh_tokens`
Lưu trữ lịch sử băm của refresh token phục vụ rotation và chống replay attack:
* `id` (UUID v7, khóa chính)
* `session_id` (UUID, tham chiếu `sessions(id)` ON DELETE CASCADE)
* `token_hash` (BYTEA 32 bytes, SHA-256 của refresh token, UNIQUE)
* `issued_at`, `expires_at` (Thời hạn refresh token)
* `used_at` (Thời điểm đã dùng để đổi token mới)
* `revoked_at` (Thời điểm bị thu hồi)

### `user_tokens`
Lưu trữ các mã OTP xác thực email và đặt lại mật khẩu:
* `id` (UUID v7, khóa chính)
* `user_id` (UUID, tham chiếu `users(id)` ON DELETE CASCADE)
* `type` (`token_type`: `'email_verification'`, `'password_reset'`)
* `token_hash` (BYTEA 32 bytes, SHA-256 của mã OTP 6 chữ số)
* `attempt_count` (INT, số lần nhập sai, tối đa 5 lần)
* `expires_at` (TIMESTAMPTZ, thời hạn OTP 10 phút)
* `used_at` (TIMESTAMPTZ, thời điểm sử dụng thành công)
* `superseded_at` (TIMESTAMPTZ, thời điểm bị thay thế hoặc bị khóa do sai quá 5 lần)

### `auth_rate_limit_events`
Ghi nhận các sự kiện xác thực để kiểm soát tần suất gửi request:
* `id` (UUID v7, khóa chính)
* `action` (`'sign_up'`, `'resend_verification'`, `'forgot_password'`)
* `dimension` (`'email'`, `'ip'`)
* `key_hash` (BYTEA 32 bytes, SHA-256 của email hoặc IP)
* `occurred_at` (TIMESTAMPTZ, thời điểm phát sinh request)

### `media_cleanup_jobs`
Hàng đợi xử lý xóa ảnh đại diện cũ trên Cloudinary khi cập nhật hoặc xóa avatar:
* `id` (UUID v7, khóa chính)
* `provider` (`'cloudinary'`)
* `object_key` (TEXT, public ID cần xóa)
* `attempt_count` (INT, số lần đã thử xóa, tối đa 10 lần)
* `next_attempt_at` (TIMESTAMPTZ, thời điểm thử lại kế tiếp với exponential backoff)
* `completed_at` (TIMESTAMPTZ, thời điểm hoàn tất)

---

## 6. Danh mục API Endpoints

### Auth Endpoints (`/api/v1/auth`)

| Method | Endpoint | Yêu cầu Auth | Mô tả |
|---|---|---|---|
| `POST` | `/api/v1/auth/sign-up` | Public | Đăng ký tài khoản mới & gửi OTP xác thực qua email |
| `POST` | `/api/v1/auth/verify-email` | Public | Xác thực email bằng OTP 6 chữ số |
| `POST` | `/api/v1/auth/resend-verification` | Public | Gửi lại OTP xác thực email |
| `POST` | `/api/v1/auth/sign-in` | Public | Đăng nhập bằng email, mật khẩu & device_id |
| `POST` | `/api/v1/auth/refresh` | Public / Body | Rotate refresh token lấy cặp access & refresh token mới |
| `POST` | `/api/v1/auth/forgot-password` | Public | Yêu cầu OTP đặt lại mật khẩu qua email |
| `POST` | `/api/v1/auth/reset-password` | Public | Đặt lại mật khẩu bằng email, OTP 6 chữ số & mật khẩu mới |
| `POST` | `/api/v1/auth/sign-out` | Bearer Token | Đăng xuất và thu hồi session hiện tại |

### User Profile Endpoints (`/api/v1/users`)

| Method | Endpoint | Yêu cầu Auth | Mô tả |
|---|---|---|---|
| `GET` | `/api/v1/users/me` | Live Session | Lấy thông tin hồ sơ của tài khoản đang đăng nhập |
| `PATCH` | `/api/v1/users/me` | Live Session | Cập nhật thông tin cá nhân (`display_name`, `phone_number`, ngân hàng) |
| `PUT` | `/api/v1/users/me/password` | Live Session | Đổi mật khẩu tài khoản (thu hồi tất cả phiên khác) |
| `PUT` | `/api/v1/users/me/avatar` | Live Session | Tải lên ảnh đại diện mới (multipart/form-data) |
| `DELETE` | `/api/v1/users/me/avatar` | Live Session | Xóa ảnh đại diện hiện tại |
| `PUT` | `/api/v1/users/me/fcm-token` | Live Session | Cập nhật FCM token cho session hiện tại |

### Bank Directory Endpoints (`/api/v1/banks`)

| Method | Endpoint | Yêu cầu Auth | Mô tả |
|---|---|---|---|
| `GET` | `/api/v1/banks` | Public | Lấy danh mục ngân hàng VietQR (hỗ trợ lọc `?supported=true`) |

---

## 7. Tác vụ ngầm định kỳ (Background Cleanup Workers)

Hệ thống chạy 2 tác vụ dọn dẹp định kỳ độc lập trong package `internal/modules/auth/jobs`:
1. **Auth Cleanup Worker (chạy mỗi 24 giờ)**:
   * Sử dụng PostgreSQL advisory lock `pg_try_advisory_xact_lock` để đảm bảo chỉ có đúng 1 instance API chạy cleanup trong môi trường multi-instance.
   * Xóa các bản ghi `auth_rate_limit_events` cũ hơn 24 giờ.
   * Xóa các bản ghi `user_tokens`, `sessions` và `media_cleanup_jobs` đã hết hạn hoặc đã xử lý quá 30 ngày (retention period) theo từng lô (batch limit 500 dòng).
2. **Media Cleanup Worker (chạy mỗi 60 giây)**:
   * Quét và claim tối đa 50 job xóa ảnh Cloudinary chưa hoàn tất trong bảng `media_cleanup_jobs` bằng cú pháp `FOR UPDATE SKIP LOCKED`.
   * Gọi Cloudinary API để hủy asset. Nếu thành công thì đánh dấu `completed_at = now()`; nếu thất bại thì tính toán thời gian thử lại kế tiếp theo công thức exponential backoff (tối đa 10 lần thử).
