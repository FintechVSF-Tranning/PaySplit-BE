# Cấu trúc dự án PaySplit Backend

Tài liệu này mô tả cấu trúc hiện tại của backend PaySplit và cách tổ chức một module nghiệp vụ mới.

## Cây thư mục hiện tại

```text
PaySplit-BE/
├── .agents/                                # Skill và quy trình hỗ trợ agent phát triển
├── .claude/                                # Liên kết skill dành cho Claude Code
│
├── cmd/
│   └── api/
│       └── main.go                         # Điểm khởi chạy HTTP API và graceful shutdown
│
├── db/
│   └── migrations/
│       └── 000001_init_schema.up.sql       # Schema khởi tạo, gồm cả goose Up và Down
│
├── docs/
│   ├── Product_Requirement_Document.md     # Yêu cầu sản phẩm của PaySplit
│   ├── auth-module.md                      # Giải thích luồng code auth v1
│   ├── openapi.yaml                        # Hợp đồng REST API
│   ├── scope/scope.md                      # Phạm vi và tiến độ feature
│   ├── specs/0001-auth-account-v1/         # Quyết định đã chốt cho auth
│   └── project-structure.md                # Tài liệu cấu trúc dự án
│
├── internal/
│   ├── bootstrap/
│   │   └── app.go                          # Kết nối config, database, module, router và server
│   │
│   ├── config/
│   │   ├── config.go                       # Đọc và kiểm tra biến môi trường
│   │   └── config_test.go                  # Kiểm thử cấu hình
│   │
│   ├── modules/
│   │   ├── auth/                           # Module định danh, phiên đăng nhập, OTP và tài khoản
│   │   │   ├── domain/
│   │   │   ├── jobs/
│   │   │   ├── usecase/
│   │   │   ├── repository/ (interface + postgres adapter)
│   │   │   └── delivery/http/
│   │   ├── group/                          # Module quản lý nhóm, mã mời, thành viên và timeline
│   │   │   ├── domain/
│   │   │   ├── usecase/
│   │   │   ├── repository/ (interface + postgres adapter)
│   │   │   └── delivery/http/
│   │   └── notification/                   # Module thông báo in-app và push FCM
│   │       ├── domain/
│   │       ├── jobs/                       # River Queue worker xử lý gửi push
│   │       ├── usecase/
│   │       ├── repository/ (interface + postgres adapter)
│   │       └── delivery/http/
│   │
│   ├── platform/
│   │   ├── auth/jwt/                       # Phát hành và xác thực JWT access token
│   │   ├── banks/                          # Snapshot VietQR được embed và kiểm tra startup
│   │   ├── database/                       # Khởi tạo pgx connection pool
│   │   ├── email/gmail/                    # Gmail SMTP adapter
│   │   ├── image/avatar/                   # EXIF, resize và WebP converter
│   │   ├── notification/fcm/               # Firebase Cloud Messaging adapter & payload builders
│   │   ├── queue/river/                    # River Queue client & migrator trên PostgreSQL
│   │   ├── security/password/              # Bộ băm bcrypt
│   │   └── storage/cloudinary/             # Upload và xóa avatar Cloudinary
│   │
│   └── transport/
│       └── http/
│           ├── helpers/                    # Ghi JSON, khuôn dạng lỗi, phân trang
│           ├── middleware/                 # Xác thực JWT, session, CORS, ratelimit, timeout
│           └── router/                     # Router gốc, middleware và health endpoint
│
├── .env.example                             # Mẫu cấu hình chạy local
├── .gitignore                               # Các file Git bỏ qua
├── Dockerfile                               # Build image cho API
├── docker-compose.yaml                      # PostgreSQL 18 với volume mới dùng local
├── Makefile                                 # Lệnh run, build, test, sqlc và goose
├── README.md                                # Hướng dẫn sử dụng dự án
├── LICENSE                                  # Giấy phép của dự án
├── go.mod                                   # Module và dependency Go
├── go.sum                                   # Checksum dependency
├── skills-lock.json                         # Phiên bản các agent skill của dự án
└── sqlc.yaml                                # Cấu hình sinh code truy vấn PostgreSQL
```

File `.env` có thể tồn tại trên máy lập trình viên nhưng không được commit vì chứa cấu hình riêng và có thể chứa secret.

## Hướng phụ thuộc

Backend dùng clean architecture theo từng module. Một request thường đi qua các tầng sau:

```text
HTTP request
    ↓
delivery/http
    ↓
usecase
    ↓
repository interface
    ↓
repository/postgres
    ↓
PostgreSQL
```

Quy tắc chính:

1. `domain` chứa entity và quy tắc nghiệp vụ thuần. Tầng này không phụ thuộc HTTP, pgx hoặc sqlc.
2. `usecase` điều phối nghiệp vụ. Nó chỉ làm việc với interface và kiểu dữ liệu của domain.
3. `repository/repository.go` định nghĩa cổng dữ liệu mà usecase cần.
4. `repository/postgres` triển khai cổng dữ liệu bằng PostgreSQL.
5. `delivery/http` chuyển đổi giữa HTTP DTO và input hoặc output của usecase.
6. `bootstrap/app.go` là nơi tạo implementation cụ thể và lắp chúng lại với nhau.
7. `platform` chứa hạ tầng kỹ thuật dùng chung như database, JWT và password hashing.
8. `transport/http` chứa thành phần HTTP dùng chung giữa nhiều module.

Module `auth` dùng sqlc cho các query cơ bản và pgx transaction cho các luồng cần khóa nhiều dòng như sign in, refresh rotation và password reset. Cả hai đều chỉ tồn tại trong PostgreSQL adapter. Kiểu pgx và sqlc không đi vào `domain` hoặc `usecase`.

## Cấu trúc một module mới

Một module mới, ví dụ `groups`, nên theo bố cục sau:

```text
internal/modules/groups/
├── domain/
│   ├── group.go
│   └── errors.go
├── usecase/
│   └── service.go
├── repository/
│   ├── repository.go
│   └── postgres/
│       ├── repository.go
│       ├── queries/
│       │   └── groups.sql
│       └── sqlc/                          # Code sinh tự động, không sửa tay
└── delivery/
    └── http/
        ├── handler.go
        ├── request.go
        ├── response.go
        └── routes.go
```

Bạn có thể thêm module theo thứ tự sau:

1. Đọc use case tương ứng trong `docs/Product_Requirement_Document.md` và hợp đồng trong `docs/openapi.yaml`.
2. Tạo entity, lỗi và quy tắc nghiệp vụ trong `domain`.
3. Khai báo repository interface theo đúng nhu cầu của usecase.
4. Viết query trong `repository/postgres/queries`.
5. Thêm một mục SQL mới vào `sqlc.yaml`, trỏ `queries` và `out` tới module mới. Không dùng chung output package giữa hai module.
6. Chạy `make sqlc` để sinh code. Không sửa tay thư mục `sqlc`.
7. Viết PostgreSQL adapter, usecase, HTTP DTO, handler và routes.
8. Khởi tạo repository, service và handler trong `internal/bootstrap/app.go`.
9. Mount route bằng prefix như `/api/v1/groups`.
10. Chạy `make test`, áp migration trên PostgreSQL thật và gọi endpoint để kiểm tra luồng hoàn chỉnh.

## Database migration

Migration dùng Goose và nằm chung trong `db/migrations`. File `000001_init_schema.up.sql` hiện chứa cả phần `-- +goose Up` và `-- +goose Down`.

Sau khi version 1 đã được áp dụng, mọi thay đổi schema tiếp theo nên nằm trong migration mới, ví dụ:

```text
db/migrations/000002_add_group_description.sql
```

Không nên sửa migration cũ để thay đổi một database đã chạy, vì Goose không tự áp dụng lại version đã hoàn thành.

Các lệnh thường dùng:

```bash
make goose-install   # Chỉ cần khi máy chưa có Goose
make migrate-up      # Áp dụng các migration còn thiếu
make migrate-status  # Xem trạng thái migration
make migrate-down    # Hoàn tác migration gần nhất
```
