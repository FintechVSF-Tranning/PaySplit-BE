# 💸 PaySplit – Chia hóa đơn & thanh toán thông minh

PaySplit là giải pháp ưu tiên di động giúp loại bỏ sự ngại ngùng và những phép tính rối rắm khi chia tiền nhóm. Bằng cách kết hợp AI OCR với VietQR động, ứng dụng tự động đọc hóa đơn, chia tiền và nhắc nợ.

Repository này chứa **backend API** (Go). Ứng dụng Flutter nằm ở `PaySplit-FE`, tài liệu sản phẩm ở `PaySplit-RP`.

## 🚀 Tính năng cốt lõi (MVP)
* **OCR thông minh:** Quét hóa đơn để trích xuất món, giá tiền và tự động tính VAT/phí dịch vụ.
* **VietQR động:** Sinh mã QR riêng cho từng người với đúng số tiền cần trả.
* **Nhắc nợ thông minh:** Tự động gửi thông báo thân thiện tới người nợ qua các nền tảng chat.
* **Thanh toán 1 chạm:** Chạm để trả tiền ngay, không phải nhập tay dữ liệu.

## 🛠 Công nghệ sử dụng
* **Ngôn ngữ:** Go 1.26
* **HTTP router:** [chi/v5](https://github.com/go-chi/chi) (`RequestID`, `Logger`, `Recoverer`, middleware timeout tự viết)
* **Cơ sở dữ liệu:** PostgreSQL 18 qua connection pool [pgx/v5](https://github.com/jackc/pgx), UUID v7 do database sinh
* **Tầng truy vấn:** [sqlc](https://sqlc.dev) – sinh code Go type-safe từ các file `.sql`
* **Migration:** Các file SQL theo chuẩn Goose, chạy bằng các target `make migrate-*`
* **Xác thực:** Access JWT 15 phút có `sid`, session PostgreSQL, refresh rotation 7 ngày và bcrypt
* **Email và ảnh:** Gmail SMTP bằng App Password, avatar WebP lưu trên Cloudinary
* **Ứng dụng di động:** Flutter (Dart)

## 📁 Cấu trúc dự án

```text
PaySplit-BE/
├── cmd/                          # Điểm khởi chạy (main mỏng, không chứa nghiệp vụ)
│   └── api/main.go               # Khởi động HTTP API và cleanup workers
│
├── db/migrations/                # Migration SQL toàn hệ thống (định dạng goose)
│   └── 000001_init_schema.up.sql
│
├── docs/
│   ├── openapi.yaml              # Đặc tả REST API
│   └── project-structure.md      # Ghi chú chi tiết về cấu trúc (tiếng Việt)
│
├── internal/
│   ├── bootstrap/app.go          # Ghép config → DB → router → HTTP server; quản lý vòng đời
│   ├── config/                   # Cấu hình đọc từ biến môi trường + kiểm tra hợp lệ
│   │
│   ├── modules/                  # Mỗi thư mục là một domain nghiệp vụ, gồm bốn tầng
│   │   └── auth/
│   │       ├── domain/           # Entity và lỗi nghiệp vụ (không phụ thuộc bên ngoài)
│   │       ├── usecase/          # Service ứng dụng; chỉ phụ thuộc vào interface
│   │       ├── repository/       # repository.go = port; postgres/ = adapter
│   │       │   └── postgres/
│   │       │       ├── queries/  # SQL viết tay thuộc về module này
│   │       │       └── sqlc/     # Code sinh tự động — tuyệt đối không sửa tay
│   │       └── delivery/http/    # Handler, route, DTO request/response
│   │
│   ├── platform/                 # Hạ tầng kỹ thuật dùng chung
│   │   ├── database/             # Thiết lập pgx pool và health check
│   │   ├── auth/jwt/             # Access JWT có session ID
│   │   ├── banks/                # Snapshot VietQR được embed
│   │   ├── email/gmail/          # Gmail SMTP adapter
│   │   ├── image/avatar/         # EXIF, resize và WebP backend
│   │   ├── storage/cloudinary/   # Cloudinary avatar adapter
│   │   └── security/password/    # Bộ băm bcrypt
│   │
│   └── transport/http/           # Phần HTTP dùng chung giữa các module
│       ├── router/               # Dựng chi router, gắn route của từng module
│       ├── middleware/           # Timeout request, v.v.
│       └── helpers/              # Ghi JSON, khuôn dạng lỗi, phân trang
│
├── docker-compose.yaml           # PostgreSQL chạy local
├── Dockerfile                    # Build nhiều tầng cho binary API
├── Makefile                      # Các target run / build / test / fmt / sqlc / migrate
└── sqlc.yaml                     # Cấu hình sinh code của sqlc
```

### Ghi chú kiến trúc

Backend đi theo **clean architecture dạng module**. Mỗi module trong `internal/modules/` là độc lập và mọi phụ thuộc đều hướng vào trong:

```
delivery/http  →  usecase  →  repository (interface)  →  repository/postgres (adapter)
                     ↓
                  domain
```

* `domain` không import bất kỳ tầng nào khác — chỉ có entity thuần và lỗi nghiệp vụ.
* `usecase` tự định nghĩa các interface mà nó cần (`repository.Repository`, `PasswordManager`, `TokenIssuer`) và nhận chúng qua constructor injection. Nó không bao giờ import `pgx`, `chi` hay `net/http`.
* `repository/postgres` chuyển đổi qua lại giữa model do sqlc sinh ra và entity của domain.
* `bootstrap/app.go` là nơi duy nhất lắp ráp các implementation cụ thể.

Để thêm một module mới, hãy theo bố cục trong [docs/project-structure.md](docs/project-structure.md). Module sở hữu `domain`, `usecase`, repository port, adapter PostgreSQL và HTTP delivery. Dependency cụ thể chỉ được ghép tại `internal/bootstrap/app.go`.

## ⚙️ Yêu cầu môi trường

* Go **1.26+**
* Docker & Docker Compose (để chạy PostgreSQL local)
* [`sqlc`](https://docs.sqlc.dev/en/latest/overview/install.html) — chỉ cần khi bạn thay đổi các file query `.sql`
* [`goose`](https://github.com/pressly/goose) — quản lý migration (`make goose-install` nếu máy chưa có)

## ▶️ Bắt đầu

**1. Cấu hình biến môi trường**

```bash
cp .env.example .env
```

| Biến | Mặc định | Mô tả |
| --- | --- | --- |
| `APP_ENV` | `development` | Tên môi trường chạy |
| `HTTP_HOST` | `localhost` | Host/IP lắng nghe (dùng `0.0.0.0` khi deploy container/cloud) |
| `HTTP_PORT` | `8080` | Port lắng nghe |
| `HTTP_ADDRESS` | `localhost:8080` | Tùy chọn gộp `host:port` (ghi đè nếu có) |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5433/paysplit?sslmode=disable` | Chuỗi kết nối PostgreSQL (port 5433 khi dùng docker compose) |
| `DB_MAX_CONNS` / `DB_MIN_CONNS` | `10` / `2` | Giới hạn số kết nối của pgx pool |
| `DB_MAX_CONN_LIFETIME_MINUTES` | `60` | Thời gian sống tối đa của một kết nối trong pool |
| `DB_MAX_CONN_IDLE_MINUTES` | `15` | Thời gian nhàn rỗi tối đa trước khi đóng kết nối |
| `DB_HEALTH_CHECK_SECONDS` | `30` | Chu kỳ kiểm tra sức khỏe của pool |
| `JWT_SECRET_KEY` | — | **Bắt buộc.** Chuỗi bí mật ngẫu nhiên, đủ dài, dùng để ký token |
| `JWT_ISSUER` | `paysplit-backend` | Giá trị claim `iss` |
| `JWT_ACCESS_TOKEN_TTL_MINUTES` | `15` | Thời hạn access token, v1 yêu cầu đúng 15 phút |
| `AUTH_REFRESH_TOKEN_TTL_HOURS` | `168` | Session tuyệt đối 7 ngày, rotation không kéo dài session |
| `AUTH_EMAIL_VERIFICATION_TTL_MINUTES` / `AUTH_PASSWORD_RESET_TTL_MINUTES` | `10` | Thời hạn email token |
| `AUTH_EMAIL_VERIFICATION_URL` / `AUTH_PASSWORD_RESET_URL` | — | Deep link hoặc HTTPS callback, backend thêm query `token` |
| `APP_INVITE_BASE_URL` | `https://paysplit.app/join` | HTTPS base URL cho link mời nhóm; mã Base62 là path segment cuối |
| `SMTP_USERNAME` / `SMTP_APP_PASSWORD` | — | Gmail có bật xác minh hai bước và Google App Password 16 ký tự |
| `CLOUDINARY_CLOUD_NAME` / `CLOUDINARY_API_KEY` / `CLOUDINARY_API_SECRET` | — | Cloudinary lưu avatar WebP |
| `FIREBASE_CREDENTIALS_FILE` / `FIREBASE_CREDENTIALS_JSON` | — | Google Firebase Service Account credentials cho push notification FCM |
| `FCM_TIMEOUT_SECONDS` | `5` | Thời gian timeout khi gửi tin FCM |

**2. Khởi động PostgreSQL**

```bash
docker compose up -d postgres
```

**3. Chạy migration**

```bash
make migrate-up
```

**4. Chạy API**

```bash
make run          # go run ./cmd/api
```

Server lắng nghe tại `HTTP_ADDRESS` (mặc định <http://localhost:8080>). Kiểm tra bằng:

```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

## 📜 Các lệnh Make

| Lệnh | Chức năng |
| --- | --- |
| `make run` | Chạy API từ mã nguồn (`./cmd/api`) |
| `make build` | Build binary ra `bin/paysplit-api` |
| `make test` | Chạy toàn bộ test (`go test ./...`) |
| `make fmt` | `gofmt -w ./cmd ./internal` |
| `make tidy` | `go mod tidy` |
| `make sqlc` | Sinh lại code sqlc từ `queries/` + `db/migrations/` |
| `make goose-install` | Cài Goose CLI nếu máy chưa có |
| `make migrate-up` | Áp dụng các migration còn thiếu bằng Goose |
| `make migrate-down` | Quay lui migration gần nhất |
| `make migrate-status` | Xem trạng thái migration |

Chạy riêng một test hoặc một package:

```bash
go test ./internal/config/...
go test -run TestLoad ./internal/config
```

## 🐳 Chạy bằng Docker

```bash
docker build -t paysplit-api .
docker run --rm -p 8080:8080 --env-file .env paysplit-api
```

Image được build nhiều tầng: build binary tĩnh với `CGO_ENABLED=0` rồi đóng gói trên `alpine:3.22`, chạy bằng user `app` không phải root.

## 📱 Chạy ứng dụng Frontend (Flutter)

Yêu cầu [Flutter SDK](https://docs.flutter.dev/get-started/install) 3.x đã cài đặt và có device/emulator đang chạy.

```bash
cd ../PaySplit-FE
flutter pub get
dart run build_runner build --delete-conflicting-outputs   # sinh *.g.dart / *.freezed.dart trước khi chạy

flutter run --dart-define=API_BASE_URL=http://10.0.2.2:8080/v1   # trỏ về Go API chạy local
```

| Lệnh | Ý nghĩa |
| --- | --- |
| `flutter run` | Chạy flavor development (API mặc định `https://dev-api.paysplit.app/v1`) |
| `flutter run -t lib/main_staging.dart` | Chạy flavor staging |
| `--dart-define=API_BASE_URL=<url>` | Ghi đè endpoint API của BE |

> `10.0.2.2` là địa chỉ Android emulator dùng để gọi `localhost` của máy host — khớp với port `8080` mà backend đang lắng nghe. Khi chạy trên máy thật, thay bằng IP LAN của máy; iOS simulator dùng thẳng `http://localhost:8080/v1`.

Chi tiết kiến trúc Flutter xem [PaySplit-FE/README.md](../PaySplit-FE/README.md).

## 🔌 Các endpoint API

### Auth & User
| Method | Đường dẫn | Mô tả |
| --- | --- | --- |
| `GET` | `/` | Tên dịch vụ, trạng thái, phiên bản |
| `GET` | `/health` | Kiểm tra liveness |
| `POST` | `/api/v1/auth/sign-up` | Đăng ký bằng email, điện thoại, tên và password |
| `POST` | `/api/v1/auth/verify-email` | Xác minh email |
| `POST` | `/api/v1/auth/resend-verification` | Gửi lại email xác minh |
| `POST` | `/api/v1/auth/sign-in` | Đăng nhập bằng email và password |
| `POST` | `/api/v1/auth/refresh` | Rotate refresh token |
| `POST` | `/api/v1/auth/sign-out` | Thu hồi session hiện tại |
| `POST` | `/api/v1/auth/forgot-password` | Yêu cầu email đặt lại password |
| `POST` | `/api/v1/auth/reset-password` | Đặt lại password bằng token |
| `GET/PATCH` | `/api/v1/users/me` | Đọc hoặc cập nhật hồ sơ |
| `PUT` | `/api/v1/users/me/password` | Đổi password |
| `PUT/DELETE` | `/api/v1/users/me/avatar` | Tải lên hoặc xóa avatar |
| `PUT` | `/api/v1/users/me/fcm-token` | Cập nhật FCM registration token |

### Groups
| Method | Đường dẫn | Mô tả |
| --- | --- | --- |
| `POST` | `/api/v1/groups` | Tạo nhóm chi tiêu mới |
| `GET` | `/api/v1/groups` | Danh sách nhóm đã tham gia (cursor pagination) |
| `GET` | `/api/v1/groups/{id}` | Xem chi tiết nhóm và danh sách thành viên |
| `POST` | `/api/v1/groups/{id}/invites` | Tạo hoặc lấy lại mã mời nhóm (Captain) |
| `DELETE` | `/api/v1/groups/{id}/invites/{invite_id}` | Thu hồi mã mời nhóm (Captain) |
| `GET` | `/api/v1/groups/invites/{code}` | Xem trước thông tin nhóm từ mã mời |
| `POST` | `/api/v1/groups/join` | Tham gia nhóm bằng mã mời |
| `POST` | `/api/v1/groups/{id}/leave` | Rời khỏi nhóm |
| `DELETE` | `/api/v1/groups/{id}/members/{member_id}` | Loại thành viên khỏi nhóm (Captain) |
| `PUT` | `/api/v1/groups/{id}/members/{member_id}/role` | Chuyển vai trò Captain |
| `GET` | `/api/v1/groups/{id}/activities` | Xem lịch sử hoạt động nhóm (timeline) |

### Notifications
| Method | Đường dẫn | Mô tả |
| --- | --- | --- |
| `GET` | `/api/v1/notifications` | Danh sách thông báo in-app (phân trang `page`, `page_size`) |
| `GET` | `/api/v1/notifications/unread-count` | Số lượng thông báo chưa đọc |
| `PATCH` | `/api/v1/notifications/{id}/read` | Đánh dấu 1 thông báo đã đọc |
| `PATCH` | `/api/v1/notifications/read-all` | Đánh dấu tất cả thông báo đã đọc |

Hợp đồng API đầy đủ được mô tả trong [docs/openapi.yaml](docs/openapi.yaml). Route không tồn tại trả về JSON `404`; sai method trả về JSON `405`. Mọi request đều bị giới hạn bởi middleware timeout 15 giây.

## 🗄 Quy trình làm việc với cơ sở dữ liệu

1. Thêm migration trong `db/migrations/` theo quy ước đặt tên `NNNNNN_description.sql`. Mỗi version là **một file duy nhất**, gồm phần `-- +goose Up` và `-- +goose Down`; không tách thành hai file `.up.sql` / `.down.sql` vì Goose sẽ xem chúng là hai migration trùng version.
2. Viết hoặc cập nhật query trong `repository/postgres/queries/*.sql` của module sở hữu nó.
3. Chạy `make sqlc` để sinh lại code Go có kiểu.
4. Chạy `make migrate-up`.

Tuyệt đối không sửa tay bất cứ thứ gì trong thư mục `sqlc/` — chúng sẽ bị ghi đè mỗi lần sinh code.

## 📚 Tài liệu chi tiết các module

* [docs/auth-module.md](docs/auth-module.md) — Kiến trúc Auth v1, session management, OTP, token rotation, avatar pipeline.
* [docs/group-module.md](docs/group-module.md) — Quản lý Group v1, row-level locking, membership invariants, mã mời, activity timeline.
* [docs/notification-module.md](docs/notification-module.md) — Thông báo In-App, push FCM, River Queue background worker, dead token pruning.
