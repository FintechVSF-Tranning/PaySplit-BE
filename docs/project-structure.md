# Cấu trúc thư mục PaySplit Backend

```text
PaySplit-BE/
├── cmd/                                      # Các điểm khởi chạy của ứng dụng
│   ├── api/
│   │   └── main.go                          # Khởi chạy HTTP API
│   └── migrate/
│       └── main.go                          # Chạy các lệnh database migration
│
├── db/                                       # Tài nguyên database dùng toàn ứng dụng
│   └── migrations/
│       └── 00001_create_users.sql            # Migration tạo bảng users phục vụ xác thực
│
├── docs/                                     # Tài liệu kỹ thuật của dự án
│   ├── openapi.yaml                          # Đặc tả REST API
│   └── project-structure.md                  # Tài liệu cấu trúc thư mục này
│
├── internal/                                 # Mã nguồn chỉ được sử dụng trong backend
│   ├── bootstrap/                            # Lắp ráp dependency và quản lý vòng đời app
│   │   └── app.go                            # Tạo config, DB, router và HTTP server
│   │
│   ├── config/                               # Cấu hình toàn ứng dụng
│   │   ├── config.go                         # Định nghĩa và nạp cấu hình từ environment
│   │   └── config_test.go                    # Kiểm thử việc nạp và validate cấu hình
│   │
│   ├── modules/                              # Các module nghiệp vụ độc lập
│   │   └── auth/                             # Module đăng ký, đăng nhập và xác thực
│   │       ├── domain/                       # Mô hình và quy tắc nghiệp vụ cốt lõi
│   │       │   ├── user.go                   # Entity User và thông tin xác thực
│   │       │   └── errors.go                 # Lỗi nghiệp vụ của auth
│   │       │
│   │       ├── usecase/                      # Các ca sử dụng và application service
│   │       │   └── service.go                # Register, login, input, business error và validation
│   │       │
│   │       ├── repository/                   # Cổng truy cập dữ liệu của module
│   │       │   ├── repository.go             # Repository interface dùng bởi usecase
│   │       │   └── postgres/                 # Adapter triển khai repository bằng PostgreSQL
│   │       │       ├── repository.go         # Triển khai interface và mapping domain/sqlc
│   │       │       ├── queries/              # Câu lệnh SQL do module auth sở hữu
│   │       │       │   └── users.sql         # Query tạo và tìm user theo email/username
│   │       │       └── sqlc/                  # Go code được sqlc sinh tự động
│   │       │           ├── db.go              # DBTX và constructor của sqlc Queries
│   │       │           ├── models.go          # Database models được sinh từ schema
│   │       │           ├── querier.go         # Interface các query được sinh tự động
│   │       │           └── users.sql.go       # Hàm Go được sinh từ users.sql
│   │       │
│   │       └── delivery/                     # Adapter nhận yêu cầu từ bên ngoài
│   │           └── http/                     # HTTP delivery của module auth
│   │               ├── handler.go             # Xử lý HTTP request và gọi usecase
│   │               ├── routes.go              # Đăng ký endpoint register và login
│   │               ├── request.go             # DTO cho register và login request
│   │               └── response.go            # DTO cho user và access token response
│   │
│   ├── platform/                             # Hạ tầng kỹ thuật dùng chung giữa các module
│   │   └── database/
│   │       ├── postgres.go                    # Khởi tạo và kiểm tra pgx connection pool
│   │       └── postgres_test.go               # Kiểm thử cấu hình/kết nối PostgreSQL
│   │
│   └── transport/                            # Thành phần giao tiếp dùng chung
│       └── http/
│           ├── router/
│           │   └── router.go                  # Tạo Chi router và gắn route các module
│           ├── middleware/
│           │   ├── logging.go                 # Request logging và request ID
│           │   ├── recovery.go                # Khôi phục khi handler panic
│           │   └── timeout.go                 # Giới hạn thời gian xử lý request
│           ├── response/
│           │   ├── response.go                # Chuẩn hóa JSON success response
│           │   └── error.go                   # Chuẩn hóa JSON error response
│           └── utils/
│               └── validation.go              # Parse và validate dữ liệu HTTP dùng chung
│
├── .env.example                              # Mẫu biến môi trường cần thiết
├── .gitignore                                # Danh sách file Git bỏ qua
├── docker-compose.yaml                       # PostgreSQL và service dùng khi phát triển
├── Dockerfile                                # Build image cho backend
├── Makefile                                  # Lệnh build, run, test, sqlc và migration
├── go.mod                                    # Khai báo Go module và dependency
├── go.sum                                    # Checksum dependency
├── README.md                                 # Hướng dẫn cài đặt và chạy dự án
└── sqlc.yaml                                 # Cấu hình sinh PostgreSQL repository code
```
