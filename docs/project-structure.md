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
│   ├── openapi.yaml                        # Hợp đồng REST API
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
│   │   └── auth/
│   │       ├── domain/
│   │       │   ├── errors.go               # Lỗi nghiệp vụ của auth
│   │       │   └── user.go                 # Entity User
│   │       ├── usecase/
│   │       │   └── service.go              # Luồng đăng ký và đăng nhập
│   │       ├── repository/
│   │       │   ├── repository.go           # Interface lưu trữ mà usecase sử dụng
│   │       │   └── postgres/
│   │       │       ├── repository.go       # Adapter PostgreSQL
│   │       │       ├── queries/
│   │       │       │   └── users.sql       # Query do module auth sở hữu
│   │       │       └── sqlc/
│   │       │           ├── db.go            # Code do sqlc sinh
│   │       │           ├── models.go        # Database model do sqlc sinh
│   │       │           ├── querier.go       # Query interface do sqlc sinh
│   │       │           └── users.sql.go     # Hàm query do sqlc sinh
│   │       └── delivery/
│   │           └── http/
│   │               ├── handler.go           # Nhận request và gọi usecase
│   │               ├── request.go           # DTO đầu vào
│   │               ├── response.go          # DTO đầu ra
│   │               └── routes.go            # Route register và login
│   │
│   ├── platform/
│   │   ├── auth/
│   │   │   └── jwt/
│   │   │       └── access_token_manager.go  # Phát hành và xác thực JWT access token
│   │   ├── database/
│   │   │   └── postgres.go                  # Khởi tạo pgx connection pool
│   │   └── security/
│   │       └── password/
│   │           └── bcrypt.go                # Băm và so sánh mật khẩu
│   │
│   └── transport/
│       └── http/
│           ├── helpers/
│           │   ├── error.go                 # Khuôn dạng lỗi JSON
│           │   ├── json.go                  # Đọc và ghi JSON
│           │   └── pagination.go            # Kiểu và helper phân trang
│           ├── middleware/
│           │   ├── auth.go                  # Xác thực request bằng access token
│           │   ├── cors.go                  # Chính sách CORS
│           │   ├── ratelimit.go             # Giới hạn tần suất request
│           │   └── timeout.go               # Giới hạn thời gian xử lý request
│           └── router/
│               └── router.go                # Router gốc, middleware và health endpoint
│
├── .env.example                             # Mẫu cấu hình chạy local
├── .gitignore                               # Các file Git bỏ qua
├── Dockerfile                               # Build image cho API
├── docker-compose.yaml                      # PostgreSQL 17 dùng khi phát triển local
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

Module `auth` hiện có code do sqlc sinh trong `repository/postgres/sqlc`, nhưng `repository/postgres/repository.go` đang gọi `pgxpool` trực tiếp. Khi chuyển sang dùng các hàm do sqlc sinh, repository adapter là nơi thực hiện việc chuyển đổi đó, không để kiểu dữ liệu của sqlc đi vào `domain` hoặc `usecase`.

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
5. Thêm một mục SQL mới vào `sqlc.yaml`, trỏ `queries` và `out` tới module mới.
6. Chạy `make sqlc` để sinh code. Không sửa tay thư mục `sqlc`.
7. Viết PostgreSQL adapter, usecase, HTTP DTO, handler và routes.
8. Khởi tạo repository, service và handler trong `internal/bootstrap/app.go`.
9. Mount route bằng prefix như `/api/v1/groups`.
10. Chạy `make test` và gọi endpoint để kiểm tra luồng hoàn chỉnh.

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
