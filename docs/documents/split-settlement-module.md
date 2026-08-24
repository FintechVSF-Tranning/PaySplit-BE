# Module Split & Settlement v1

Module `settlement` cung cấp các luồng tổng hợp khoản phải trả, tạo VietQR, gửi bằng chứng chuyển khoản và xác nhận thanh toán giữa các thành viên trong nhóm.

PaySplit chỉ điều phối giao dịch ngang hàng. Hệ thống không giữ tiền, không chuyển tiền thay người dùng và không đóng vai trò trung gian thanh toán.

## 1. Tổng quan chức năng

Module hỗ trợ các chức năng chính sau:

* Xem phần chi phí cá nhân đã được phân bổ theo từng hóa đơn
* Xem danh sách khoản nợ và ma trận nợ trong nhóm
* Gộp nhiều khoản nợ có cùng người trả và người nhận vào một lần thanh toán
* Sinh payload và ảnh VietQR từ hồ sơ ngân hàng hiện tại của chủ nợ
* Nhận bằng chứng chuyển khoản dưới dạng JPEG, PNG hoặc HEIC
* Lưu bằng chứng dưới dạng private WebP trên Cloudinary
* Cho chủ nợ xác nhận hoặc từ chối toàn bộ khoản thanh toán
* Cho chủ nợ hoặc trưởng nhóm gửi lời nhắc nợ thủ công
* Tự động nhắc các khoản nợ quá hạn và cảnh báo thanh toán chờ xác nhận
* Bảo vệ các thao tác ghi bằng idempotency và khóa dữ liệu theo thứ tự cố định

Mã nguồn chính nằm tại:

* `internal/modules/settlement/`
* `internal/platform/vietqr/`
* `internal/platform/storage/cloudinary/proof.go`
* `db/migrations/000009_split_settlement_v1.sql`
* `db/migrations/000010_media_cleanup_reason.sql`

## 2. Kiến trúc module

Luồng xử lý tuân theo Clean Architecture của backend:

```text
HTTP Request
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

Các thành phần hạ tầng được truyền qua interface:

* `ProofStorage` tải ảnh lên Cloudinary, tạo signed URL và xóa object
* `Notifier` ghi thông báo và enqueue push notification trong cùng transaction nghiệp vụ
* `BankInfoProvider` tra cứu tên ngân hàng và mã BIN
* VietQR generator mã hóa payload NAPAS ngay trong ứng dụng

Tầng `usecase` không phụ thuộc vào HTTP, PostgreSQL hoặc Cloudinary.

## 3. Trạng thái khoản nợ

Một khoản nợ dùng các trạng thái sau:

* `awaiting`: người nợ chưa gửi bằng chứng thanh toán
* `pending_confirmation`: bằng chứng đã được gửi và đang chờ chủ nợ xử lý
* `settled`: chủ nợ đã xác nhận thanh toán
* `voided`: hóa đơn nguồn đã bị hủy

Luồng thông thường:

```text
awaiting
    ↓ gửi bằng chứng
pending_confirmation
    ↓ xác nhận
settled
```

Khi chủ nợ từ chối, mọi khoản nợ thuộc thanh toán quay về `awaiting`.

Khi module bill hủy hóa đơn, các khoản nợ liên quan chuyển sang `voided`. Thanh toán VietQR đang chờ bằng chứng sẽ chuyển sang `superseded` để không thể tiếp tục sử dụng.

## 4. Trạng thái thanh toán

Một thanh toán dùng các trạng thái sau:

* `pending_proof`: VietQR đã được tạo và đang chờ người trả gửi bằng chứng
* `pending_confirmation`: bằng chứng đã được lưu và đang chờ chủ nợ xác nhận
* `confirmed`: chủ nợ đã xác nhận toàn bộ khoản thanh toán
* `rejected`: chủ nợ đã từ chối toàn bộ khoản thanh toán
* `superseded`: ý định thanh toán không còn hợp lệ do tập khoản nợ thay đổi hoặc hóa đơn bị hủy

Luồng chính:

```text
pending_proof
    ↓ gửi bằng chứng
pending_confirmation
    ├── xác nhận → confirmed
    └── từ chối  → rejected
```

Xác nhận và từ chối áp dụng cho toàn bộ `payment_debts`. Module không hỗ trợ xác nhận một phần trong v1.

## 5. Tổng hợp chi phí cá nhân

Endpoint chi phí cá nhân trả về các hóa đơn mà thành viên hiện tại có phần được phân bổ.

Mỗi hóa đơn gồm:

* Thông tin cơ bản của hóa đơn
* Phần tiền cá nhân phải chịu
* Trạng thái phần chi phí
* Thông tin người tạo và nhóm

Các khoản nợ `settled` và `voided` không được tính vào số tiền đang nợ. Tiền được xử lý bằng `int64` trong domain và repository, sau đó mới định dạng thành chuỗi ở HTTP response.

## 6. Danh sách và ma trận nợ

Danh sách nợ hỗ trợ cursor pagination với giới hạn mặc định 20 và tối đa 100 bản ghi.

Ma trận nợ tổng hợp số tiền giữa từng cặp thành viên. Số tiền được net theo hai chiều để người dùng nhìn thấy nghĩa vụ ròng.

Các query chỉ tính hai trạng thái đang hoạt động:

* `awaiting`
* `pending_confirmation`

Thành viên không còn hoạt động trong nhóm không thể đọc dữ liệu settlement.

## 7. Tạo VietQR

Người trả có thể chọn một hoặc nhiều khoản nợ nếu chúng đáp ứng toàn bộ điều kiện sau:

* Thuộc cùng một nhóm
* Có cùng người trả
* Có cùng chủ nợ
* Đang ở trạng thái `awaiting`
* Chưa thuộc thanh toán đang hoạt động khác

Repository khóa nhóm trước, sau đó khóa các khoản nợ theo thứ tự UUID và cuối cùng khóa thanh toán. Thứ tự này cũng được module bill sử dụng khi hủy hóa đơn.

Trước khi gửi bằng chứng, VietQR dùng hồ sơ ngân hàng hiện tại của chủ nợ. Nếu chủ nợ chưa có tài khoản ngân hàng hợp lệ, API trả về `BANK_ACCOUNT_REQUIRED`.

Payload VietQR được mã hóa cục bộ với các giá trị chính:

* NAPAS AID `A000000727`
* Dịch vụ `QRIBFTTA`
* Tiền tệ VND với mã `704`
* Nội dung chuyển khoản trong tag `62.08`
* Checksum CRC16 ở cuối payload

Hệ thống không gọi API ngoài để sinh payload thanh toán.

## 8. Snapshot ngân hàng

Hồ sơ ngân hàng vẫn có thể thay đổi khi thanh toán ở trạng thái `pending_proof`.

Khi người trả gửi bằng chứng thành công, repository lưu snapshot bất biến gồm:

* Tên ngân hàng
* Mã BIN
* Số tài khoản
* Tên chủ tài khoản
* Mã tham chiếu thanh toán

Các lần đọc sau đó dùng snapshot này. Việc chủ nợ thay đổi hồ sơ ngân hàng không làm thay đổi bằng chứng hoặc thông tin của giao dịch đã gửi.

## 9. Bằng chứng chuyển khoản

Endpoint proof nhận `multipart/form-data` với ảnh và ghi chú tùy chọn.

Các định dạng đầu vào được chấp nhận:

* JPEG
* PNG
* HEIC dựa trên ISOBMFF brand hợp lệ

Module kiểm tra magic bytes của nội dung thay vì chỉ tin `Content-Type` do client gửi. MP4 hoặc dữ liệu không phải ảnh sẽ bị từ chối với `INVALID_IMAGE`.

Ảnh được tải lên Cloudinary dưới dạng private WebP với mức chất lượng `q_100`. Signed URL chỉ có hiệu lực trong thời gian cấu hình, mặc định là năm phút.

Ghi chú được kiểm tra UTF 8 hợp lệ, tối đa 500 ký tự và tối đa 2000 byte.

## 10. Idempotency

Các thao tác ghi yêu cầu header `Idempotency-Key`:

* Tạo VietQR
* Gửi bằng chứng
* Xác nhận thanh toán
* Từ chối thanh toán
* Gửi lời nhắc nợ

Khóa gốc không được lưu trong database. Repository lưu hash của khóa cùng hash request, actor, operation và response đã hoàn thành.

Quy tắc xử lý:

* Cùng khóa và cùng request sẽ phát lại status code cùng response đã lưu
* Cùng khóa nhưng request khác trả về `IDEMPOTENCY_KEY_REUSED`
* Request đang được xử lý trả về `IDEMPOTENCY_IN_PROGRESS`
* Bản ghi idempotency hết hạn sau 24 giờ

Với proof, request hash gồm payment ID, hash SHA256 của ảnh và ghi chú đã chuẩn hóa.

Mỗi lần thử upload dùng một operation ID riêng trong storage key:

```text
payments/{paymentId}/proofs/{operationId}
```

Nếu upload thất bại, cùng operation có thể được tiếp tục. Nếu upload thành công nhưng commit database thất bại, service chỉ xóa object của chính operation đó rồi xoay operation ID. Cơ chế này ngăn một request thua cuộc xóa ảnh của request đã commit.

## 11. Xác nhận và từ chối

Chỉ chủ nợ của thanh toán được xác nhận hoặc từ chối. Trưởng nhóm không thể thay chủ nợ quyết định giao dịch.

Khi xác nhận:

* Payment chuyển thành `confirmed`
* Toàn bộ khoản nợ liên kết chuyển thành `settled`
* Activity được ghi với actor hiện tại
* Notification được lưu và enqueue trong cùng transaction

Khi từ chối:

* Payment chuyển thành `rejected`
* Toàn bộ khoản nợ liên kết quay về `awaiting`
* Lý do từ chối được kiểm tra và lưu
* Proof object được đưa vào cleanup sau khi transaction hoàn tất

Nếu một bước database thất bại, transaction rollback toàn bộ thay đổi.

## 12. Nhắc nợ

Chủ nợ hoặc trưởng nhóm có thể gửi lời nhắc thủ công cho khoản nợ ở trạng thái `awaiting`.

Giới hạn mặc định:

* Tối đa ba lần nhắc cho mỗi khoản nợ
* Khoảng cách tối thiểu 24 giờ giữa hai lần nhắc

Số lần nhắc tối đa có thể cấu hình từ một đến ba. Khi vượt giới hạn hoặc gửi quá sớm, API trả về `REMINDER_RATE_LIMITED`.

## 13. Background workers

Module đăng ký các River worker sau:

### Settlement scan

Worker chạy mỗi giờ và chạy ngay khi ứng dụng khởi động.

Nó xử lý:

* Khoản nợ `awaiting` chưa được nhắc trong thời gian cấu hình, mặc định 72 giờ
* Thanh toán `pending_confirmation` bị treo quá thời gian cấu hình, mặc định 48 giờ

### Settlement cleanup

Worker chạy hằng ngày và chạy ngay khi ứng dụng khởi động.

Nó xử lý:

* Xóa idempotency record đã hết hạn
* Xóa proof object còn sót lại sau lỗi hoặc từ chối
* Retry cleanup với exponential backoff có giới hạn
* Ghi metric khi xóa object tiếp tục thất bại

Cleanup job lưu đúng storage key của object cần xóa. Worker không tái tạo key từ payment ID.

## 14. Notification và activity

Notification được tạo trong cùng transaction với thay đổi nghiệp vụ. Payload luôn có `group_id` và một định danh điều hướng:

* `payment_id` cho sự kiện thanh toán
* `debt_id` cho sự kiện nhắc nợ

Các activity chính:

* `payment_created`
* `payment_submitted`
* `payment_confirmed`
* `payment_rejected`
* `debt_reminded`
* `payment_stalled_confirmation`

Actor có thể là `member` hoặc `system`. Worker tự động sử dụng actor `system`.

## 15. API endpoints

Tất cả endpoint đều nằm dưới `/api/v1/groups/{groupId}` và yêu cầu live authentication.

### Đọc dữ liệu

* `GET /expenses/me` trả về phần chi phí cá nhân
* `GET /debts` trả về danh sách nợ hoặc ma trận nợ
* `GET /payments/{paymentId}` trả về chi tiết thanh toán và signed proof URL khi có

### Thay đổi dữ liệu

* `POST /payments/qr` tạo hoặc phát lại ý định thanh toán VietQR
* `POST /payments/{paymentId}/proof` gửi bằng chứng chuyển khoản
* `POST /payments/{paymentId}/confirm` xác nhận toàn bộ thanh toán
* `POST /payments/{paymentId}/reject` từ chối toàn bộ thanh toán
* `POST /debts/{debtId}/remind` gửi lời nhắc nợ thủ công

Hợp đồng request và response đầy đủ nằm trong `docs/openapi.yaml`.

## 16. Mã lỗi chính

Module có thể trả về các mã lỗi sau:

* `VALIDATION_FAILED`
* `INVALID_IMAGE`
* `INVALID_CURSOR`
* `GROUP_NOT_FOUND`
* `PAYMENT_NOT_FOUND`
* `DEBT_NOT_FOUND`
* `CREDITOR_NOT_FOUND`
* `FORBIDDEN`
* `BANK_ACCOUNT_REQUIRED`
* `DEBTS_NOT_AWAITING`
* `DEBT_NOT_AWAITING`
* `PAYMENT_NOT_PENDING_PROOF`
* `PAYMENT_NOT_PENDING_CONFIRMATION`
* `IDEMPOTENCY_KEY_REUSED`
* `IDEMPOTENCY_IN_PROGRESS`
* `REMINDER_RATE_LIMITED`
* `STORAGE_UNAVAILABLE`

`IDEMPOTENCY_IN_PROGRESS` trả về HTTP 409 cùng header `Retry-After`.

## 17. Database

Các cấu trúc dữ liệu chính:

* `payments` lưu trạng thái, snapshot ngân hàng, reference code và proof metadata
* `debts` lưu nghĩa vụ giữa hai thành viên, trạng thái và thông tin nhắc nợ
* `payment_debts` liên kết bất biến một thanh toán với đúng tập khoản nợ
* `payment_idempotency_keys` lưu lease, request hash và response để replay
* `group_activities` lưu activity do thành viên hoặc hệ thống tạo
* `media_cleanup_jobs` lưu object cần cleanup và trạng thái retry
* `v_member_balances` tổng hợp số dư từ các khoản nợ còn hoạt động

Composite foreign key bảo đảm payment và debt có cùng group, debtor và creditor.

## 18. Cấu hình môi trường

Các biến cấu hình của module:

* `VIETQR_SERVICE_BASE_URL`, mặc định `https://img.vietqr.io/image`
* `VIETQR_TEMPLATE`, mặc định `compact`
* `PAYMENT_PROOF_MAX_BYTES`, mặc định `10485760`
* `PAYMENT_PROOF_SIGNED_URL_TTL`, mặc định `300` giây
* `PAYMENT_REMINDER_STALE_HOURS`, mặc định `72` giờ
* `PAYMENT_REMINDER_MAX_COUNT`, mặc định `3`
* `STALLED_CONFIRMATION_HOURS`, mặc định `48` giờ
* `TEST_DATABASE_URL`, database PostgreSQL riêng cho integration test

Integration test settlement sẽ skip nếu `TEST_DATABASE_URL` không được thiết lập. Nó không fallback sang `DATABASE_URL` để tránh chạy worker hoặc xóa fixture trên database phát triển.

## 19. Logging và metrics

Module không ghi các dữ liệu nhạy cảm sau vào log:

* Proof URL
* Reference code
* Số tài khoản ngân hàng
* Ghi chú chuyển khoản

Metrics chỉ dùng label có tập giá trị cố định:

* Kết quả operation của settlement
* Kết quả worker theo loại job
* Lỗi media cleanup theo reason đã chuẩn hóa

## 20. Kiểm thử và verify

Bạn có thể chạy unit test và integration test bằng các lệnh sau:

```bash
go test -count=1 ./...
go test -race -count=1 ./...
TEST_DATABASE_URL=postgres://... go test -count=1 ./internal/modules/settlement/repository/postgres
```

Verify hiện tại đã kiểm tra:

* Migration chạy mới, rollback về version 8 và chạy lại thành công
* Toàn bộ Go test và race test vượt qua
* Luồng API thật từ tạo QR, replay, gửi proof đến confirm
* PNG được lưu và tải lại dưới dạng WebP qua signed URL Cloudinary
* Idempotency replay giữ đúng HTTP 201 hoặc 200
* Request conflict trả về đúng mã lỗi
* OpenAPI parse thành công

Chi tiết bằng chứng nằm tại `docs/specs/0004-split-settlement-v1/verify.md`.

## 21. Các file logic chính

* `internal/modules/settlement/delivery/http/handler.go` xử lý HTTP, multipart và ánh xạ lỗi
* `internal/modules/settlement/delivery/http/response.go` định dạng response và số tiền
* `internal/modules/settlement/usecase/service.go` kiểm tra input, idempotency hash và điều phối proof storage
* `internal/modules/settlement/repository/postgres/repository.go` quản lý transaction, lock và cập nhật trạng thái
* `internal/modules/settlement/jobs/workers.go` đăng ký worker scan và cleanup
* `internal/modules/settlement/integration/notification.go` nối settlement với notification module
* `internal/platform/vietqr/generator.go` tạo payload VietQR và CRC16
* `internal/platform/storage/cloudinary/proof.go` lưu private proof dưới dạng WebP
* `internal/bootstrap/app.go` khởi tạo dependency và đăng ký route

## 22. Giới hạn của v1

* Hệ thống không giữ hoặc tự động chuyển tiền
* Chủ nợ xác nhận toàn bộ payment, không xác nhận từng debt riêng lẻ
* `Retry-After` của request đang xử lý hiện dùng giá trị cố định một giây
* `debt_count` trong ma trận đếm debt gốc ở hai chiều, còn `total_amount` biểu diễn số tiền đã net
* Automated reminder xử lý một batch tối đa 100 debt trong một transaction
* Notification payload có định danh điều hướng nhưng chưa có amount

