# Test module Bill và OCR bằng Postman

Import [`PaySplit-Bill-OCR.postman_collection.json`](PaySplit-Bill-OCR.postman_collection.json) vào Postman, tạo một environment rỗng, rồi chạy lần lượt từ thư mục 0 xuống.

Mỗi request tự lưu ID nó nhận được vào environment, nên bạn không phải copy paste ID nào bằng tay, và **không cần chạm database** cho luồng chính.

---

## Chuẩn bị

1. `docker compose up -d postgres` rồi `make migrate-up`
2. Chạy server: `set -a; . ./.env; set +a; go run ./cmd/api`
3. Trong Postman tạo environment mới, thêm một biến duy nhất: `base_url` = `http://localhost:8080`
4. Chọn environment đó ở góc trên bên phải, rồi chạy thư mục 0

---

## Ba API bạn hỏi tới

### Tạo hóa đơn và xem chia tiền

| Bước | Request |
|---|---|
| Tạo | `POST /api/v1/bills` với body JSON |
| Xem chia tiền | `GET /api/v1/bills/{id}?group_id=...` rồi đọc mảng `breakdown` |

Không có endpoint `preview` riêng. Phần chia tiền nằm ngay trong response chi tiết hóa đơn, ở trường `breakdown`, và chỉ xuất hiện khi hóa đơn còn `draft` hoặc `reviewed`.

Collection dùng tổng **lẻ** 100001 có chủ ý. Chia đôi ra 50000 mỗi người, dư 1 đồng, và 1 đồng đó phải về Creditor. Tab Test Results tự kiểm tra ba điều: tổng khớp `bill.total`, Creditor được 50001 với `rounding_adjustment` 1, người kia được 50000 với 0.

### OCR

| Bước | Request |
|---|---|
| Tải ảnh, tạo job | `POST /api/v1/bills` dạng **multipart form-data** |
| Theo dõi tiến độ | `GET /api/v1/bills/{id}/events` (SSE) |
| Áp dụng kết quả | `POST /api/v1/bills/{id}/apply-candidate` |
| Chạy lại | `POST /api/v1/bills/{id}/ocr-retry` |

---

## Bốn cái bẫy trong Postman

### 1. `member_id` không phải `user_id`

Các API hóa đơn dùng **member id** (id của một dòng trong `group_members`), không phải user id. Đây là chỗ nhầm dễ nhất.

Lấy nó từ response, không cần query database:

- Người tạo nhóm: `POST /api/v1/groups` trả về `membership.id`
- Người vào sau: `POST /api/v1/groups/join` trả về `join.membership_id`
- Quên rồi: `GET /api/v1/groups/{id}` trả về `members[]`, mỗi phần tử có `membership_id`

Collection tự bắt cả hai vào `memberA_id` và `memberB_id`.

### 2. Upload ảnh phải là form-data, và đừng đặt Content-Type

Ở request `2.1`, sang tab **Body → form-data**. Dòng `images` có kiểu **File**, bấm **Select Files** và chọn ảnh của bạn. Postman không cho collection mang sẵn đường dẫn file vì lý do bảo mật, nên bạn phải tự chọn.

**Đừng tự đặt header `Content-Type`.** Postman cần tự sinh chuỗi `boundary`; đặt tay sẽ làm hỏng request và bạn nhận lỗi phân tích multipart khó hiểu.

Muốn nhiều ảnh thì thêm nhiều dòng cùng tên `images`, tối đa 5. Mỗi ảnh tối đa 10 MB, định dạng JPEG, PNG hoặc HEIC.

Request này trả **202** chứ không phải 201, vì OCR chạy nền.

### 3. Chi tiết hóa đơn không cho biết OCR xong chưa

Tôi đã kiểm chứng trên server thật: `GET /api/v1/bills/{id}` chỉ trả về `bill`, `breakdown`, `signed_urls`. Trong `bill` **không có trường nào** về job OCR. Đường duy nhất lộ trạng thái job là SSE.

Trong Postman, cách gọn nhất là bỏ qua việc theo dõi và cứ gọi thẳng `apply-candidate` sau khoảng 30 giây:

- `409 OCR_NOT_READY` nghĩa là chưa xong, đợi thêm rồi bấm Send lại
- `200` nghĩa là xong

Muốn nhìn thấy tiến độ thật thì mở terminal bên cạnh:

```bash
curl -N -H "Authorization: Bearer <tokenA>" \
  "http://localhost:8080/api/v1/bills/<ocr_bill_id>/events?group_id=<group_id>"
```

Postman xử lý SSE rất kém, nhiều bản sẽ treo cho tới khi hết timeout rồi mới đổ hết một cục ra thay vì hiện từng sự kiện.

### 4. `bank_code` là mã ngắn, không phải số BIN

Khi gán tài khoản ngân hàng cho Creditor trước lúc chốt sổ, dùng `"ICB"` chứ không phải `"970415"`. Đưa số BIN vào sẽ nhận `400 UNSUPPORTED_BANK`, và thông báo lỗi không nói rõ là bạn đưa sai định dạng.

`GET /api/v1/banks` trả về cả `code` (ICB) lẫn `bin` (970415). Dùng `code`.

Ba trường `bank_code`, `bank_account_number`, `bank_account_holder` phải gửi cùng lúc, database có ràng buộc bắt cả ba cùng rỗng hoặc cùng có giá trị.

---

## Không nhận được email xác thực

Đăng ký xong tài khoản ở trạng thái `pending_verification` và cần mã 6 số gửi qua email. Nếu SMTP chưa cấu hình hoặc email không tới, kích hoạt thẳng trong database rồi bỏ qua request `0.2` và `0.7`:

```bash
docker compose exec -T postgres psql -U postgres -d paysplit \
  -c "UPDATE users SET status='active', email_verified_at=now() WHERE email LIKE 'test%@example.com';"
```

---

## Token hết hạn

Access token sống 15 phút. Khi bắt đầu nhận `401`, chạy lại request `0.3` (và `0.8` cho người B). Các biến khác vẫn giữ nguyên.

---

## Muốn test bằng dòng lệnh thay vì Postman

Xem [`../bill-ocr-manual-test.md`](../bill-ocr-manual-test.md), phủ rộng hơn: worker dọn dẹp, phân quyền, và các bất biến về tiền kiểm tra thẳng trong database.
