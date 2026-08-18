# Module Bill & OCR v1

Tài liệu này giúp bạn hiểu chi tiết kiến trúc, mô hình dữ liệu, cơ chế phân bổ tài chính, luồng xử lý nhận dạng hóa đơn OCR và kiểm toán bảo mật của module Bill & OCR trong PaySplit Backend API. Bạn cũng có thể xem thêm [spec 0003](specs/0003-bill-ocr-v1/index.md) để tra cứu các tiêu chí nghiệm thu (acceptance criteria) tương ứng.

---

## 1. Tổng quan chức năng

Module Bill & OCR v1 cung cấp toàn bộ nghiệp vụ quản lý vòng đời hóa đơn và phân bổ nợ trong nhóm:

1. **Tạo hóa đơn nháp (Manual Draft & Receipt Image Draft)**: Người dùng có thể tạo hóa đơn thủ công nhập tay hoặc tải lên từ 1 đến 5 ảnh hóa đơn thanh toán. Ảnh được lưu trữ bảo mật trên Cloudinary và sinh tác vụ nhận diện ngầm.
2. **Nhận dạng hóa đơn thông minh (OCR với LlamaExtract & River Queue)**: Tiến trình xử lý ngầm ghép nhiều ảnh (stitching) nếu có, gọi nhà cung cấp LlamaExtract, chuẩn hóa ngày tháng, tên cửa hàng, danh sách món ăn, tiền tệ VND và phát hiện các cảnh báo sai lệch số tiền.
3. **Cập nhật trạng thái thời gian thực qua SSE (Server Sent Events)**: Kênh phát sự kiện realtime (`/api/v1/bills/{id}/events`) thông báo cho client ngay khi tiến trình OCR bắt đầu xử lý, hoàn tất sinh candidate hoặc gặp lỗi cần thử lại.
4. **Xem trước và áp dụng kết quả OCR (Candidate Application & Stale Protection)**: Người dùng có thể xem danh sách món ăn do OCR đề xuất và áp dụng vào hóa đơn nháp. Hệ thống bảo vệ tính nhất quán bằng cách từ chối nếu phiên bản hóa đơn đã bị người khác chỉnh sửa trước đó.
5. **Chỉnh sửa hóa đơn nháp toàn diện (Full Draft Replacement)**: Trưởng nhóm (Captain) hoặc chủ nợ (Creditor) có thể cập nhật các thông tin tổng tiền, phụ phí, thuế VAT, chiết khấu và danh sách tối đa 100 món ăn kèm trọng số phân chia của từng thành viên.
6. **Giải thuật phân bổ nợ Hamilton (Deterministic VND Allocation)**: Tính toán số tiền từng người phải trả bằng phương pháp phần dư lớn nhất (Largest Remainder Method), bảo toàn số nguyên VND, không để xảy ra số dư âm khi có giảm giá lớn và xử lý hòa điểm (tie breaking) theo thứ tự UUID chuẩn hóa tăng dần.
7. **Rà soát hóa đơn (Review Bill)**: Kiểm tra đối soát chặt chẽ giữa tổng tiền khai báo và tổng tiền tính toán từ các món ăn, phụ phí, thuế và chiết khấu. Đánh dấu hóa đơn sang trạng thái `reviewed` khi tất cả số liệu khớp tuyệt đối.
8. **Chốt hóa đơn nguyên tử (Finalize Bill)**: Đóng băng dữ liệu hóa đơn, lưu bản snapshot phân bổ chi tiết (`bill_shares`), tự động sinh các khoản nợ (`debts`) cho từng con nợ, ghi nhật ký hoạt động nhóm (`group_activities`) và xếp hàng gửi thông báo đẩy (push notifications) cho các thành viên liên quan. Luồng này yêu cầu chủ nợ đã liên kết thông tin tài khoản ngân hàng.
9. **Hủy hóa đơn an toàn (Void Bill) và Hóa đơn thay thế (Replacement)**: Cho phép hủy hóa đơn đã chốt nếu chưa có bất kỳ khoản nợ nào bắt đầu thanh toán. Toàn bộ các khoản nợ chưa trả chuyển sang `voided`. Khi cần, người dùng có thể tạo một hóa đơn mới thay thế trỏ đến hóa đơn đã hủy qua `replaces_bill_id`.
10. **Xóa hóa đơn nháp và dọn dẹp file (Delete Draft & Media Cleanup)**: Cho phép xóa hóa đơn đang ở trạng thái nháp và tự động đưa các khóa ảnh Cloudinary vào hàng đợi dọn dẹp để xóa file triệt để.

---

## 2. Giải thuật phân bổ nợ Hamilton (Largest Remainder Method)

Để chia nhỏ tiền từng món ăn và các khoản phụ phí mà không làm lệch dù chỉ 1 đồng VND so với tổng tiền thực tế trên hóa đơn, PaySplit áp dụng giải thuật Hamilton số nguyên:

### Quy tắc toán học
1. **Tính tiền món ăn (Item Subtotal)**: Với mỗi món, tỷ lệ chia của thành viên $m$ dựa trên trọng số $w_m$ đã phân bổ:
   $$R_{m,i} = \frac{w_{m,i}}{\sum_{k} w_{k,i}}$$
   Số tiền cơ sở của thành viên $m$ nhận phần nguyên $\lfloor R_{m,i} \times \text{LineTotal}_i \rfloor$. Phần dư thừa được phân phối cho các thành viên có phần thập phân lớn nhất theo thứ tự giảm dần.
2. **Phân bổ phí dịch vụ (Service Charge) và thuế (VAT)**: Từng khoản phí được phân bổ theo tỷ lệ tiền món ăn của từng người trên tổng tiền món ăn (subtotal). Nếu hóa đơn không có món ăn (subtotal = 0), toàn bộ phí và thuế được quy cho chủ nợ (Creditor).
3. **Phân bổ chiết khấu (Discount)**: Chiết khấu được phân bổ theo tỷ trọng tiền món ăn và được chặn trên không vượt quá tổng số tiền món ăn cộng phụ phí của thành viên, đảm bảo `final_amount` không bao giờ bị âm.
4. **Phần bù làm tròn (Rounding Adjustment)**: Sau khi cộng tất cả các khoản, số tiền chênh lệch nhỏ (do làm tròn số nguyên) giữa tổng tiền thực tế của hóa đơn và tổng tiền tạm tính của tất cả thành viên được bù vào thành viên có phần dư lớn nhất.

### Tính xác định tuyệt đối (Deterministic Tie Breaking)
Khi hai hoặc nhiều thành viên có phần dư thập phân bằng nhau chính xác, quyền ưu tiên nhận 1 đồng làm tròn được phân xử bằng cách so sánh 16 byte nhị phân của `member_id` (UUID) theo thứ tự tăng dần. Điều này đảm bảo kết quả tính toán trên mọi máy chủ và môi trường luôn đồng nhất 100%.

---

## 3. Quy trình xử lý OCR và Server Sent Events (SSE)

```text
1. Client gửi ảnh (1-5 files)
        ↓
2. Lưu trữ Cloudinary (Private Asset, Type: Authenticated)
        ↓
3. Tạo bản ghi bills + ocr_jobs (Status: queued) trong 1 Transaction
        ↓
4. Enqueue River Job `bill_ocr`
        ↓
5. Background Worker (billjobs.OCRWorker):
   ├── Broadcast SSE: {status: "processing"}
   ├── Tải ảnh và ghép dọc (image stitching) nếu có nhiều ảnh
   ├── Gọi LlamaExtract API (trích xuất merchant, date, items, totals)
   ├── Chuẩn hóa Schema và kiểm tra đối soát số tiền
   ├── Lưu kết quả candidate vào bảng ocr_jobs (Status: completed)
   └── Broadcast SSE: {status: "completed", candidate: {...}}
        ↓
6. Client nhận sự kiện SSE qua GET /api/v1/bills/{id}/events
```

### Giới hạn thử lại và xử lý lỗi:
* Mỗi hóa đơn chỉ có tối đa 1 tác vụ OCR ở trạng thái hoạt động (`queued` hoặc `processing`) nhờ partial unique index trên database.
* Người dùng có thể yêu cầu thử lại nhận dạng (`POST /api/v1/bills/{id}/ocr-retry`) tối đa 5 lần trong vòng 24 giờ.
* Nếu người dùng chỉnh sửa bản nháp trong lúc OCR đang chạy, khi áp dụng candidate (`POST /api/v1/bills/{id}/apply-candidate`), hệ thống sẽ đối chiếu `version`. Nếu phiên bản không khớp, yêu cầu bị từ chối với lỗi `OCR_RESULT_STALE` để ngăn ghi đè mất dữ liệu người dùng.

---

## 4. Kiểm soát đồng thời, Idempotency và Bảo mật

### Khóa chống Deadlock theo thứ tự phân cấp (Lock Ordering Hierarchy)
Khi thực hiện các giao dịch nhạy cảm như chốt hóa đơn (`FinalizeBill`) hoặc hủy hóa đơn (`VoidBill`), hệ thống tuân thủ nghiêm ngặt thứ tự khóa dòng:
1. Luôn khóa bản ghi `bills` trước bằng `SELECT ... FOR UPDATE`.
2. Sau khi đã giữ khóa hóa đơn, tiến hành khóa các dòng công nợ `debts` liên quan theo **thứ tự UUID tăng dần**.
Quy tắc này loại bỏ hoàn toàn nguy cơ deadlock khi xảy ra thao tác đồng thời giữa module hóa đơn và module thanh toán công nợ.

### Tính lũy đẳng 24 giờ (Idempotency Key)
Mọi thao tác thay đổi dữ liệu (`CreateBill`, `UpdateDraftBill`, `ReviewBill`, `FinalizeBill`, `VoidBill`, `DeleteDraftBill`, `RetryOCR`, `ApplyCandidate`) đều hỗ trợ header `Idempotency-Key`. Trạng thái thực thi được lưu trong bảng `bill_idempotency_keys`. Nếu client gửi lại cùng một khóa trong vòng 24 giờ, hệ thống sẽ trả lại ngay kết quả đã lưu mà không thực hiện lại giao dịch.

### Bảo mật tài sản hình ảnh và che giấu dữ liệu
* Ảnh hóa đơn được lưu ở chế độ private trên Cloudinary, không công khai URL trực tiếp.
* API chi tiết hóa đơn sinh đường dẫn có chữ ký kèm thời hạn hiệu lực 5 phút (short lived signed URL) cho từng ảnh.
* Nhật ký hoạt động nhóm (`group_activities`) và Prometheus metrics chỉ lưu trữ định danh, số tiền tổng hợp và số lượng thành viên, tuyệt đối không lưu nội dung chi tiết món ăn hay thông tin nhạy cảm của tài khoản ngân hàng.

---

## 5. Kiến trúc phân lớp của một Request

```text
HTTP Request (chi Router)
    ↓
Middleware (RateLimit, Timeout, Live Session Auth)
    ↓
delivery/http (Handler & SSE Hub)
    ├── Đọc Multipart Form hoặc JSON Payload (giới hạn kích thước)
    ├── Xác thực quyền thành viên thông qua authmw.UserID
    └── Ánh xạ Domain Errors sang HTTP Error Responses chuẩn
    ↓
usecase.Service (Application Service)
    ├── Thực thi các Invariants, kiểm tra trạng thái và quyền hạn
    ├── Thực hiện giải thuật phân bổ nợ Hamilton (allocation.go)
    ├── Quản lý tính lũy đẳng (Idempotency Manager)
    └── Điều phối OCR Client, Storage Adapter, Image Processor và Enqueuer
    ↓
repository/postgres (Repository Adapter)
    ├── Quản lý Database Transactions nguyên tử
    ├── Áp dụng Row Locks (FOR UPDATE) và Optimistic Versioning
    └── Tương tác với cơ sở dữ liệu qua sqlc queries
    ↓
PostgreSQL 18 Pool (pgxpool)
```

---

## 6. Các bảng dữ liệu của Module Bill & OCR

### `bills`
Lưu trữ thông tin gốc của hóa đơn:
* `id` (UUID v7, khóa chính)
* `group_id` (UUID, tham chiếu `groups(id)` ON DELETE CASCADE)
* `creditor_member_id` (UUID, thành viên trả tiền trước, tham chiếu `group_members(id)`)
* `status` (`bill_status`: `'draft'`, `'reviewed'`, `'finalized'`, `'voided'`)
* `merchant_name` (TEXT, tên nhà hàng hoặc quán ăn)
* `bill_date` (DATE, ngày ghi trên hóa đơn)
* `subtotal`, `service_charge`, `vat`, `discount`, `total` (BIGINT, đơn vị VND)
* `split_method` (TEXT: `'even'`, `'item_ratio'`, `'exact'`, `'shares'`, `'percentage'`)
* `mismatch_codes` (TEXT[], danh sách cảnh báo sai lệch số tiền)
* `version` (INT, số phiên bản tăng dần dùng cho optimistic locking)
* `replaces_bill_id` (UUID, trỏ đến hóa đơn đã hủy mà hóa đơn này thay thế)
* `reviewed_by_member_id`, `reviewed_at` (Thông tin kiểm tra rà soát)
* `finalized_at`, `voided_at` (Thời điểm chốt hoặc hủy hóa đơn)

### `bill_images`
Lưu trữ danh sách 1 đến 5 ảnh chụp hóa đơn theo thứ tự:
* `id` (UUID v7, khóa chính)
* `bill_id`, `group_id` (Khóa ngoại tham chiếu `bills(id, group_id)` ON DELETE CASCADE)
* `image_key` (TEXT, public ID lưu trên Cloudinary)
* `position` (SMALLINT, thứ tự ảnh từ 0 đến 4)

### `bill_items`
Danh sách tối đa 100 món ăn hoặc dịch vụ trong hóa đơn:
* `id` (UUID v7, khóa chính)
* `bill_id`, `group_id` (Tham chiếu `bills(id, group_id)` ON DELETE CASCADE)
* `name` (TEXT, tên món ăn)
* `quantity` (NUMERIC, số lượng)
* `unit_price`, `line_total` (BIGINT, đơn vị VND)
* `position` (SMALLINT, thứ tự hiển thị món)

### `bill_item_assignments`
Phân bổ người tham gia ăn từng món:
* `id` (UUID v7, khóa chính)
* `bill_item_id`, `group_id` (Tham chiếu `bill_items(id, group_id)` ON DELETE CASCADE)
* `member_id` (UUID, tham chiếu `group_members(id)`)
* `weight` (NUMERIC, trọng số phân chia của thành viên cho món này)

### `ocr_jobs`
Theo dõi tiến trình nhận diện hình ảnh của hóa đơn:
* `id` (UUID v7, khóa chính)
* `bill_id` (UUID, tham chiếu `bills(id)` ON DELETE CASCADE)
* `status` (`ocr_job_status`: `'queued'`, `'processing'`, `'completed'`, `'failed'`)
* `provider` (TEXT, mặc định `'llamaextract'`)
* `attempts` (INT, số lần đã xử lý)
* `candidate` (JSONB, dữ liệu món ăn và số tiền trích xuất được)
* `version` (INT, phiên bản hóa đơn lúc tạo job để chống stale overwrite)

### `bill_shares`
Snapshot cố định số tiền phân bổ của từng thành viên sau khi chốt hóa đơn:
* `id` (UUID v7, khóa chính)
* `bill_id`, `group_id` (Tham chiếu `bills(id, group_id)` ON DELETE CASCADE)
* `member_id` (UUID, tham chiếu `group_members(id)`)
* `item_subtotal`, `service_charge_share`, `vat_share`, `discount_share`, `rounding_adjustment` (BIGINT)
* `final_amount` (BIGINT, số tiền cuối cùng thành viên phải trả)

### `bill_idempotency_keys`
Bảo vệ tính lũy đẳng trong 24 giờ cho các tác vụ thay đổi hóa đơn:
* `actor_user_id`, `operation`, `key_hash` (Khóa chính tổng hợp)
* `state` (`idempotency_state`: `'in_progress'`, `'completed'`)
* `response_code`, `response_body` (Lưu kết quả phản hồi để trả về ngay)
* `expires_at` (TIMESTAMPTZ, hết hạn sau 24 giờ)

---

## 7. Danh mục API Endpoints (`/api/v1/bills`)

Tất cả các endpoint dưới đây đều yêu cầu xác thực người dùng (Live Session Auth Bearer Token).

| Method | Endpoint | Quyền hạn | Mô tả |
|---|---|---|---|
| `POST` | `/api/v1/bills` | Thành viên nhóm | Tạo hóa đơn nháp mới (hỗ trợ nhập tay hoặc tải lên 1 đến 5 ảnh qua multipart) |
| `GET` | `/api/v1/bills?group_id={id}` | Thành viên nhóm | Lấy danh sách hóa đơn trong nhóm có phân trang cursor |
| `GET` | `/api/v1/bills/{id}?group_id={id}` | Thành viên nhóm | Lấy chi tiết hóa đơn kèm danh sách món, phân bổ Hamilton và signed URL ảnh |
| `GET` | `/api/v1/bills/{id}/events?group_id={id}` | Thành viên nhóm | Kết nối luồng Server Sent Events (SSE) theo dõi trạng thái tiến trình OCR |
| `PUT` | `/api/v1/bills/{id}?group_id={id}` | Chủ nợ / Captain | Cập nhật toàn bộ nội dung hóa đơn nháp (ghi đè danh sách món và trọng số) |
| `PATCH` | `/api/v1/bills/{id}?group_id={id}` | Chủ nợ / Captain | Bí danh (alias) của PUT để cập nhật toàn bộ nội dung hóa đơn nháp |
| `DELETE` | `/api/v1/bills/{id}?group_id={id}` | Chủ nợ / Captain | Xóa hóa đơn nháp và xếp hàng dọn dẹp ảnh trên lưu trữ đám mây |
| `POST` | `/api/v1/bills/{id}/ocr-retry?group_id={id}` | Chủ nợ / Captain | Yêu cầu tiến trình OCR nhận diện lại hình ảnh hóa đơn |
| `POST` | `/api/v1/bills/{id}/apply-candidate?group_id={id}` | Chủ nợ / Captain | Áp dụng danh sách món và số tiền do OCR đề xuất vào bản nháp |
| `POST` | `/api/v1/bills/{id}/review?group_id={id}` | Chủ nợ / Captain | Xác nhận đối soát hợp lệ và chuyển trạng thái hóa đơn sang `reviewed` |
| `POST` | `/api/v1/bills/{id}/finalize?group_id={id}` | Captain | Chốt hóa đơn, tạo snapshot chia tiền, phát sinh công nợ và gửi thông báo |
| `POST` | `/api/v1/bills/{id}/void?group_id={id}` | Captain | Hủy bỏ hóa đơn đã chốt và hủy các khoản nợ liên quan nếu chưa thanh toán |

---

## 8. Tác vụ ngầm (Background Jobs)

1. **River Queue OCR Worker (`billjobs.OCRWorker`)**:
   * Lắng nghe hàng đợi River Queue cho tác vụ `bill_ocr`.
   * Tải ảnh từ Cloudinary, ghép ảnh nếu có nhiều trang, gọi LlamaExtract, kiểm tra tính hợp lệ của schema, ghi nhận kết quả và phát broadcast SSE cho các client đang kết nối.
2. **Media Cleanup Worker (`internal/modules/auth/jobs`)**:
   * Định kỳ quét bảng `media_cleanup_jobs` để gọi Cloudinary API xóa vĩnh viễn các ảnh hóa đơn khi hóa đơn nháp bị xóa hoặc bị hủy bỏ.
