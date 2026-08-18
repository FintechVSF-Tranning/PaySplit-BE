# Module auth v1

Tài liệu này giúp bạn đọc module auth theo đúng luồng chạy và dùng nó làm mẫu khi viết module mới.

## Auth đang làm gì

Đăng ký cần email, số điện thoại, tên hiển thị và password. Số điện thoại được chuẩn hóa thành E.164 nhưng chưa dùng để đăng nhập hoặc xác minh trong v1. Đăng nhập chỉ nhận email và password.

Access token có hiệu lực 15 phút và chứa `sid`, là ID của session. Session tồn tại tối đa 7 ngày. Refresh token được rotate sau mỗi lần dùng. Database chỉ lưu SHA 256 của refresh token. Nếu token đã dùng xuất hiện lại, toàn bộ session bị thu hồi.

Mỗi user chỉ có một session chưa bị thu hồi. Database bảo vệ điều này bằng partial unique index. Khi thiết bị mới đăng nhập, session cũ mất hiệu lực ngay cả khi access JWT cũ chưa hết 15 phút.

## `device_id` và `device_name`

App Flutter tự sinh một UUID khi app được cài lần đầu, rồi lưu UUID đó trong secure storage. Giá trị này là `device_id` và được gửi khi sign in hoặc refresh. Không dùng IMEI, MAC address hoặc advertising ID.

`device_name` là thông tin tùy chọn để con người nhận biết thiết bị, ví dụ `iPhone 16 Pro, iOS 20`. App lấy từ API hệ điều hành. Backend chỉ trim và giới hạn 120 ký tự.

Nếu người dùng xóa rồi cài lại app, app sinh `device_id` mới. Lần đăng nhập tiếp theo sẽ thu hồi session mang ID cài đặt cũ.

## Một request đi qua code như thế nào

```text
HTTP request
    ↓
delivery/http
    ↓
usecase.Service
    ↓
repository.Repository và các provider interface
    ↓
PostgreSQL, Gmail hoặc Cloudinary adapter
```

`delivery/http` chỉ đọc request, lấy user và session từ middleware, gọi usecase rồi map domain error sang HTTP.

`usecase` chứa thứ tự nghiệp vụ. Ví dụ sign up kiểm tra input, rate limit, hash password, sinh verification token, commit user cùng token, rồi mới gửi Gmail.

`repository/postgres` sở hữu SQL và transaction. Những luồng cạnh tranh như tạo session hoặc refresh luôn khóa theo thứ tự ổn định.

`platform` chứa chi tiết kỹ thuật có thể thay thế. Gmail, Cloudinary, WebP, JWT, phone parsing và bcrypt không được import trực tiếp vào domain.

`bootstrap/app.go` là nơi duy nhất chọn implementation thật và nối các interface với nhau.

## Các bảng auth

`users` giữ identity, profile và trạng thái block đăng nhập.

`sessions` giữ một lần đăng nhập trên một app installation.

`session_refresh_tokens` giữ lịch sử hash để rotate và phát hiện reuse.

`user_tokens` giữ email verification và password reset token. `used_at` nghĩa là action đã thành công. `superseded_at` nghĩa là token mới đã thay token cũ.

`auth_rate_limit_events` giữ các request được chấp nhận để tính rolling window theo email và IP.

`media_cleanup_jobs` giữ Cloudinary object cần xóa lại sau lỗi mạng hoặc provider.

## Avatar

Backend nhận tối đa 10 MB. JPEG, PNG, GIF và WebP được backend đọc EXIF orientation, resize cạnh dài còn tối đa 1024 px, loại metadata và encode WebP quality 82.

Nếu backend không hỗ trợ định dạng như HEIC, file gốc được gửi sang Cloudinary với `format=webp`. Database chỉ đổi `avatar_object_key` sau khi upload mới thành công. Mỗi upload dùng public ID mới nên asset cũ vẫn an toàn cho tới lúc database commit.

V1 không đặt trần pixel nguồn. Conversion bị giới hạn 10 giây và mặc định chỉ hai tác vụ chạy đồng thời. Đây là rủi ro prototype đã được ghi trong spec và cần xem lại trước production.

## Bank Directory và Endpoint GET /api/v1/banks

Backend nhúng snapshot danh mục ngân hàng từ VietQR (`internal/platform/banks/data/banks.json`) và khởi tạo `Directory` khi khởi động hệ thống.

- `GET /api/v1/banks` là endpoint công khai (public) trả về danh sách toàn bộ ngân hàng (bao gồm `id`, `name`, `code`, `bin`, `short_name`, `logo`, `supported`).
- Cho phép truyền query parameter `?supported=true` (hoặc `?supported=false`) để lọc danh sách ngân hàng được hỗ trợ.
- Header phản hồi đính kèm `Cache-Control: public, max-age=86400` cho phép app Flutter / proxy cache dữ liệu cục bộ trong 24 giờ.
- Khi người dùng cập nhật thông tin tài khoản ngân hàng (`PATCH /api/v1/users/me`), backend dùng `Directory.Supported(code)` để đảm bảo mã ngân hàng gửi lên là hợp lệ và được hỗ trợ; nếu không sẽ trả về `400 UNSUPPORTED_BANK`.

## Khi thêm một module mới

Bạn nên bắt đầu bằng một luồng nhỏ đi xuyên suốt database, usecase và HTTP.

1. Viết entity và domain error. Domain không import pgx, chi hoặc provider SDK.
2. Viết repository interface đúng theo nhu cầu usecase, không thiết kế một repository chung cho mọi module.
3. Viết migration và query SQL. Chạy Goose trên database thật, sau đó chạy sqlc.
4. Viết PostgreSQL adapter. Transaction và mapping lỗi constraint nằm tại đây.
5. Viết usecase bằng interface. External network call luôn chạy ngoài database transaction.
6. Viết request, response, handler và routes. Không trả database model trực tiếp.
7. Ghép dependency trong `internal/bootstrap/app.go`.
8. Thêm OpenAPI, unit test và integration test trên PostgreSQL.

Một cấu trúc module tối thiểu:

```text
internal/modules/example/
├── domain/
├── usecase/
├── repository/
│   ├── repository.go
│   └── postgres/
│       ├── queries/
│       ├── sqlc/
│       └── repository.go
└── delivery/http/
```

Bạn có thể dùng module auth làm ví dụ về ranh giới, nhưng không nên sao chép toàn bộ độ phức tạp của session hoặc token vào module không cần chúng.
