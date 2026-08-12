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
* **Cơ sở dữ liệu:** PostgreSQL 17 qua connection pool [pgx/v5](https://github.com/jackc/pgx)
* **Tầng truy vấn:** [sqlc](https://sqlc.dev) – sinh code Go type-safe từ các file `.sql`
* **Migration:** Các file SQL theo chuẩn goose, chạy qua `cmd/migrate`
* **Xác thực:** JWT access token + băm mật khẩu bằng bcrypt
* **Ứng dụng di động:** Flutter (Dart)

## 📁 Cấu trúc dự án

```text
PaySplit-BE/
├── cmd/                          # Điểm khởi chạy (main mỏng, không chứa nghiệp vụ)
│   ├── api/main.go               # Khởi động HTTP API
│   └── migrate/main.go           # Chạy migration up/down/status
│
├── db/migrations/                # Migration SQL toàn hệ thống (định dạng goose)
│   └── 00001_create_users.sql
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
│   │   ├── auth/jwt/             # Bộ phát hành token
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

Để thêm một module mới, hãy làm theo đúng bố cục thư mục `auth/`, đăng ký route của nó trong [internal/transport/http/router/router.go](internal/transport/http/router/router.go), và khai báo thư mục queries của nó trong [sqlc.yaml](sqlc.yaml).

## ⚙️ Yêu cầu môi trường

* Go **1.26+**
* Docker & Docker Compose (để chạy PostgreSQL local)
* [`sqlc`](https://docs.sqlc.dev/en/latest/overview/install.html) — chỉ cần khi bạn thay đổi các file `.sql`

## ▶️ Bắt đầu

**1. Cấu hình biến môi trường**

```bash
cp .env.example .env
```

| Biến | Mặc định | Mô tả |
| --- | --- | --- |
| `APP_ENV` | `development` | Tên môi trường chạy |
| `HTTP_ADDRESS` | `:8080` | Địa chỉ API lắng nghe |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/paysplit?sslmode=disable` | Chuỗi kết nối PostgreSQL |
| `DB_MAX_CONNS` / `DB_MIN_CONNS` | `10` / `2` | Giới hạn số kết nối của pgx pool |
| `DB_MAX_CONN_LIFETIME_MINUTES` | `60` | Thời gian sống tối đa của một kết nối trong pool |
| `DB_MAX_CONN_IDLE_MINUTES` | `15` | Thời gian nhàn rỗi tối đa trước khi đóng kết nối |
| `DB_HEALTH_CHECK_SECONDS` | `30` | Chu kỳ kiểm tra sức khỏe của pool |
| `JWT_SECRET_KEY` | — | **Bắt buộc.** Chuỗi bí mật ngẫu nhiên, đủ dài, dùng để ký token |
| `JWT_ISSUER` | `paysplit-backend` | Giá trị claim `iss` |
| `JWT_ACCESS_TOKEN_TTL_MINUTES` | `60` | Thời hạn của access token |

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
| `make migrate-up` | Áp dụng các migration còn thiếu |
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

## 🔌 Các endpoint API

| Method | Đường dẫn | Mô tả |
| --- | --- | --- |
| `GET` | `/` | Tên dịch vụ, trạng thái, phiên bản |
| `GET` | `/health` | Kiểm tra liveness |
| `POST` | `/api/v1/auth/register` | Tạo tài khoản, trả về access token |
| `POST` | `/api/v1/auth/login` | Đăng nhập, trả về access token |

Hợp đồng API đầy đủ được mô tả trong [docs/openapi.yaml](docs/openapi.yaml). Route không tồn tại trả về JSON `404`; sai method trả về JSON `405`. Mọi request đều bị giới hạn bởi middleware timeout 15 giây.

## 🗄 Quy trình làm việc với cơ sở dữ liệu

1. Thêm migration trong `db/migrations/` theo quy ước đặt tên `NNNNN_description.sql` và có đủ marker goose `-- +goose Up` / `-- +goose Down`.
2. Viết hoặc cập nhật query trong `repository/postgres/queries/*.sql` của module sở hữu nó.
3. Chạy `make sqlc` để sinh lại code Go có kiểu.
4. Chạy `make migrate-up`.

Tuyệt đối không sửa tay bất cứ thứ gì trong thư mục `sqlc/` — chúng sẽ bị ghi đè mỗi lần sinh code.

## 🚧 Trạng thái hiện tại

Bố cục, routing và các interface đã sẵn sàng; tuy nhiên một số phần cài đặt vẫn chỉ là khung và sẽ `panic("TODO: ...")` khi được gọi — cụ thể là `bootstrap.New`, `config.Load`, usecase của auth và các HTTP handler của auth. Vì vậy `make run` sẽ chưa phục vụ được request cho đến khi những chỗ đó được hoàn thiện. Dùng grep tìm `TODO:` để biết phần việc còn lại.
