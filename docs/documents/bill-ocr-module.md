# Module Bill & OCR v1

Tài liệu này mô tả chi tiết kiến trúc, mô hình dữ liệu, cơ chế xử lý OCR và luồng nghiệp vụ của module Bill & OCR (Hóa đơn & Trích xuất OCR) trong PaySplit Backend API.

---

## 1. Tổng quan chức năng

Module Bill & OCR v1 chịu trách nhiệm:
1. **Tạo hóa đơn (Bill Draft)**: Tạo hóa đơn thủ công (`201`) hoặc tải lên tối đa 5 ảnh biên lai (`202`, xử lý OCR bất đồng bộ). Người tạo hóa đơn luôn là Creditor (người đã ứng tiền chi trả).
2. **Trích xuất dữ liệu bằng OCR (LlamaExtract)**: Xử lý ảnh biên lai qua River Queue, gửi tới provider LlamaExtract để trích xuất merchant, ngày, danh sách món, phí dịch vụ, VAT, giảm giá và tổng tiền. Kết quả OCR (candidate) không tự động ghi đè bản nháp; người dùng phải chủ động "áp dụng" (apply) kết quả.
3. **Phân bổ theo tỷ lệ (Item Ratio Allocation)**: Mỗi món hàng được gán cho một hoặc nhiều thành viên nhóm với tỷ lệ (`weight`/`share_ratio`) dương, tổng bằng 1. Preview cộng phần chính xác của từng thành viên qua mọi món bằng phân số, chỉ làm tròn một lần, rồi phân phối VND còn dư theo Largest Remainder.
4. **Xét duyệt tường minh (Review)**: Creditor hoặc Captain xác nhận một phiên bản (`version`) cụ thể của hóa đơn đã sẵn sàng để chốt sổ. Bất kỳ thay đổi nào sau đó (sửa món, sửa gán, áp dụng OCR mới) đều xóa trạng thái đã duyệt.
5. **Chốt sổ hóa đơn (Finalize)**: Captain chốt hóa đơn trong một transaction PostgreSQL ngắn, tạo snapshot bất biến `bill_shares`, tạo `debts` cho từng thành viên không phải Creditor, ghi activity và job thông báo.
6. **Hủy và thay thế (Void & Replace)**: Captain có thể hủy một hóa đơn đã chốt sổ nhưng chưa có khoản nợ nào bắt đầu thanh toán. Lịch sử được giữ nguyên; một hóa đơn nháp mới có thể tham chiếu `replaces_bill_id` tới hóa đơn đã hủy.

---

## 2. Cơ chế xử lý OCR (LlamaExtract) qua River Queue

Hệ thống dùng River Queue (PostgreSQL-backed job queue) để xử lý OCR bất đồng bộ và bền vững:

```text
1. Tạo job:
   POST /api/v1/bills (ảnh) hoặc POST /bills/{id}/ocr-retry
        ↓
   jobs.Enqueuer.EnqueueOCRJobTx (trong cùng transaction tạo bill/ảnh)
        ↓
   River job OCRJobArgs{kind: "bill_ocr", MaxAttempts: BILL_OCR_MAX_ATTEMPTS}
        ↓
   ocr_jobs (status = queued), partial unique index đảm bảo tối đa 1 job queued/processing mỗi bill

2. Xử lý job (OCRWorker.Work):
   fetch job → status = processing → broadcast SSE
        ↓
   Tải ảnh từ Cloudinary theo `bill_images.position`, ghép nếu nhiều trang
        ↓
   Gọi LlamaExtract (internal/platform/ocr/llamaextract) với timeout BILL_OCR_PROVIDER_TIMEOUT_SECONDS
        ├── Thành công: chuẩn hóa candidate (schema.go, normalizer.go), lưu raw_response + candidate (JSONB), status = succeeded
        └── Thất bại: retry theo backoff tùy biến (không dùng backoff mặc định của River)
             NextRetry = BILL_OCR_RETRY_BASE_DELAY_SECONDS × 2^(attempt-1)
             Hết số lần thử (BILL_OCR_MAX_ATTEMPTS) → status = failed, draft vẫn khả dụng để nhập thủ công

3. Áp dụng kết quả (Apply Candidate):
   POST /bills/{id}/apply-candidate kèm version hiện tại
        ↓
   Hai lần kiểm tra version, khác nhau và trả lỗi khác nhau:
        ├── Client gửi version không phải version hiện tại của bill → 409 VERSION_CONFLICT
        └── Version hiện tại khác version bill lúc job bắt đầu → 409 OCR_RESULT_STALE
             (hóa đơn đã bị sửa trong lúc OCR chạy, kết quả này đã lỗi thời)
        ↓
   Ghi đè fields + items của bill, xóa toàn bộ assignments và trạng thái review, tăng version
```

### Đặc điểm của luồng OCR:
* **Không tự động ghi đè**: Kết quả OCR thành công chỉ là "candidate" gắn với `ocr_jobs.id`; không có gì thay đổi trên bill cho tới khi người dùng gọi apply-candidate.
* **Xử lý khuyến mãi theo món (Item Promotions Folding)**: Các dòng khuyến mãi (`KM`, `Chiết khấu`, `Giảm giá` hoặc `line_total < 0`) nằm dưới từng món ăn được tự động gộp vào món đứng trước nó dưới dạng `discount_amount` dương. Món được tính lại `final_price = line_total - discount_amount`, dòng `KM` bị xóa khỏi danh sách món.
* **Tách biệt KM món và KM chung**: Hệ thống tính `total_item_discount = Σ item.discount_amount` và tách khuyến mãi chung áp dụng cho toàn bill: `general_discount = max(0, payload.discount - total_item_discount)`. Khi chia tiền nhóm, chỉ `general_discount` mới được chia tỷ lệ, còn KM món thuộc về người gánh món đó.
* **Chống stale apply**: Mỗi `ocr_jobs` row lưu `version` (phiên bản bill tại thời điểm job bắt đầu). Áp dụng candidate khi bill đã đổi version từ lúc đó sẽ bị từ chối bằng `409 OCR_RESULT_STALE`, giữ nguyên các sửa đổi thủ công đã có.
* **Giữ mọi candidate cũ**: Chạy lại OCR tạo một `ocr_jobs` row mới với candidate riêng. Không row nào bị ghi đè, nên lịch sử mọi lần trích xuất đều truy vết được.
* **Giới hạn retry thủ công**: Người dùng chủ động chạy lại OCR (`ocr-retry`) tối đa `BILL_OCR_MANUAL_LIMIT` lần trong cửa sổ `BILL_OCR_MANUAL_WINDOW_HOURS`.
* **Một job hoạt động tại một thời điểm**: Partial unique index `uq_ocr_jobs_active_bill` đảm bảo mỗi bill chỉ có tối đa một job ở trạng thái `queued` hoặc `processing`.
* **Dọn dữ liệu nhạy cảm**: `raw_response` (phản hồi thô từ provider) bị xóa sau `BILL_OCR_RAW_RETENTION_DAYS` (mặc định 30 ngày) qua `OCRRetentionWorker` (`ocr_worker.go`), chạy mỗi 24 giờ và chạy luôn khi khởi động. `candidate` đã chuẩn hóa được giữ lại. Phần nối dây nằm ở `jobs.RegisterRetentionJobs` (`internal/modules/bill/jobs/retention.go`), gọi từ `internal/bootstrap/app.go`.

---

## 3. Cơ chế cập nhật realtime qua SSE (Server-Sent Events)

* **Endpoint**: `GET /api/v1/bills/{id}/events`, yêu cầu thành viên nhóm đang hoạt động (active membership).
* **Luồng sự kiện**: Khi kết nối, server gửi ngay một sự kiện `snapshot` chứa version và trạng thái OCR hiện tại. Sau đó server phát các sự kiện cập nhật (`ocr.updated`, `bill.updated`) khi có thay đổi, thông qua PostgreSQL `LISTEN/NOTIFY` (`billHub.StartPostgresListener`) chuyển tiếp qua `sse_hub.go`.
* **Heartbeat**: Server gửi sự kiện `heartbeat` mỗi `BILL_SSE_HEARTBEAT_INTERVAL_SECONDS` (mặc định 15 giây) để giữ kết nối và cho client biết stream còn sống.
* **Đóng kết nối chủ động**: Sau `BILL_SSE_MAX_CONNECTION_AGE_MINUTES` (mặc định 15 phút), server gửi sự kiện `close` và ngắt kết nối, buộc client tự reconnect (lấy `snapshot` mới nhất). Route SSE được miễn khỏi timeout request 15s chung của toàn hệ thống.
* **Không có replay**: SSE chỉ phát trạng thái hiện tại (snapshot + updates), không lưu event ID hay hỗ trợ replay lịch sử.

---

## 4. Phân bổ tỷ lệ và làm tròn sau khi cộng

* Mỗi `bill_items` có một hoặc nhiều `bill_item_assignments`, với `weight` (tỷ lệ, `numeric`) dương, tổng theo từng món phải bằng đúng 1.
* **Đọc chi tiết, Review và Finalize dùng chung một hàm** `evaluateAllocation` (`internal/modules/bill/usecase/reconciliation.go`), hàm này chạy toàn bộ phần đối soát rồi gọi `CalculateAllocation` (`internal/modules/bill/usecase/allocation.go`). Nhờ vậy số hiển thị trước khi chốt sổ, điều kiện chặn ở bước review, và số ghi nhận sau khi chốt sổ luôn nói cùng một điều.
* Mỗi phần món được biểu diễn bằng `math/big.Rat` từ VND nguyên và trọng số nguyên. Hệ thống cộng mọi phần chính xác của một thành viên trước, sau đó mới lấy phần nguyên một lần. Không dùng `float32` hoặc `float64` cho phép tính tiền.
* Sau khi có tổng chính xác, hệ thống sắp xếp phần lẻ giảm dần và phát từng 1 VND còn lại. Nếu phần lẻ bằng nhau, UUID byte chuẩn tăng dần quyết định người nhận. Creditor không có ưu tiên làm tròn.
* Nếu phần giảm giá chia cho một thành viên lớn hơn số tiền họ phải trả, phần đó bị chặn trần để `final_amount` của họ đúng bằng 0, và phần bị cắt rơi về Creditor. Không bao giờ kẹp kết quả cuối về 0, vì chính việc kẹp đó từng làm tổng vượt quá hóa đơn.
* `rounding_adjustment = final_amount - (item_subtotal + service_charge_share + vat_share - discount_share)`. Giá trị này có thể thuộc bất kỳ thành viên nào và chỉ được tính đúng một lần.
* `split_method = even` vẫn chỉ là tiện ích ghi trọng số đều cho các món đã chọn. Nó không biến chia theo món thành chia đều toàn hóa đơn.
* Phí dịch vụ, VAT và giảm giá dùng tỷ trọng subtotal chính xác của từng thành viên trên tổng tiền món. Nếu subtotal bằng 0, toàn bộ phí dịch vụ, VAT và giảm giá thuộc về Creditor, và nhánh này không đi qua vòng chặn trần.
* Phân bổ dùng tổng **tính được** từ chính các thành phần, không dùng `total` khai báo trên bản nháp. Bản nháp có thể lưu `total` lệch (OCR đọc sai, người dùng nhập thiếu), và khoản lệch đó không được phép rơi lên đầu Creditor. Tầng đối soát báo lệch bằng mã `TOTAL_MISMATCH` riêng.
* Khi đọc chi tiết một hóa đơn `draft` hoặc `reviewed`, các mã chặn được tính lại tại thời điểm đọc và trả về trong `mismatch_codes`, gộp với cảnh báo OCR đã lưu. `breakdown` chỉ có mặt khi danh sách đó rỗng, nên một breakdown vắng mặt luôn kèm lý do. Bảy mã: `ITEM_UNASSIGNED`, `INACTIVE_MEMBER_ASSIGNED`, `DISCOUNT_EXCEEDS_BILL`, `SUBTOTAL_MISMATCH`, `TOTAL_MISMATCH`, `DISCOUNT_NOT_ALLOCATABLE`, `CREDITOR_REQUIRED`.

---

## 5. Kiến trúc phân lớp của một Request

```text
HTTP Request (chi Router)
    ↓
Middleware: RateLimit → Timeout (15s, SSE được miễn) → Auth (Live Session Verification)
    ↓
delivery/http (Handler & DTOs)
    ├── Đọc JSON/multipart, kiểm tra Idempotency-Key
    ├── Trích xuất User ID & Group ID, xác định vai trò (Creditor/Captain/Member)
    └── Giao tiếp với usecase và chuyển đổi domain error sang HTTP JSON error
    ↓
usecase.Service (Application Service)
    ├── Kiểm tra hình dạng request thuần túy trước transaction (ratios, replacement eligibility...)
    ├── Gọi OCR Provider, Cloudinary Storage, River Enqueuer
    └── Gọi repository.Repository (Port Interface), khóa bill row (SELECT FOR UPDATE) khi mutate
    ↓
repository/postgres (Adapter Layer)
    ├── Quản lý Database Transactions & Row Locks theo thứ tự UUID byte tăng dần
    ├── Thực thi SQL Queries / sqlc-generated queries
    └── Chuyển đổi giữa database models và domain entities
    ↓
PostgreSQL 18 Pool (pgxpool) + River Queue (bảng river_job)
```

* **Nguyên tắc Clean Architecture**: giống module Auth — `domain/` chỉ chứa struct thuần và sentinel errors; `usecase/` định nghĩa interface phụ thuộc (`OCRProvider`, `BillStorage`, `Enqueuer`) và không import `pgx`/`chi`/`net/http`; lời gọi mạng ra ngoài (LlamaExtract, Cloudinary) không bao giờ chạy trong lúc giữ khóa transaction.
* **Nhóm đã giải tán chặn mọi write bill**: kể từ group governance (spec 0002 AC-9), `CreateBill`, `UpdateDraftBill`, `ReviewBill`, `FinalizeBill` đều gọi `database.LockActiveGroup` (`internal/platform/database/group_lock.go`) trước khi ghi bất cứ gì; nếu `groups.status = 'archived'` thao tác bị chặn ngay trong transaction. Các câu SQL đọc trạng thái OCR job (`UpdateOCRJobProcessing`, `UpdateOCRJobSuccess`, `UpdateOCRJobFailed`) và gauge `paysplit_ocr_queue_depth` cũng lọc theo `groups.status = 'active'`, nên một job của nhóm đã bị giải tán không còn được tính là job đang chạy.
* **Idempotency**: Các mutation quan trọng (tạo bill, xóa draft, apply-candidate, review, finalize, void) yêu cầu header `Idempotency-Key`, được kiểm tra qua bảng `bill_idempotency_keys` với khóa `(actor_user_id, operation, key_hash)` và một dòng "in_progress" được reserve trước khi gọi ra ngoài (Cloudinary, LlamaExtract). Câu lệnh reserve dùng `ON CONFLICT ... DO UPDATE ... WHERE expires_at <= now()`, nên một dòng đã hết hạn được chiếm lại nguyên tử trong đúng một lượt. Dòng còn hạn không bị đụng và được đọc lại để quyết định replay hay báo xung đột.

---

## 6. Các bảng dữ liệu của Module Bill

### `bills`
Hóa đơn, là bảng trung tâm của module:
* `id` (UUID v7), `group_id`, `creditor_member_id` (người đã ứng tiền, không thể đổi sau khi tạo)
* `status` (`bill_status`: `'draft'`, `'reviewed'`, `'finalized'`, `'voided'`)
* `merchant_name`, `bill_date`, `image_object_key` (cột ảnh đơn cũ, giữ tương thích)
* `subtotal`, `total_item_discount`, `general_discount`, `discount`, `service_charge`, `vat`, `total` (BIGINT, VND)
* `version` (INT, optimistic lock — mọi mutation phải kiểm tra và tăng cột này)
* `mismatch_warning` (BOOL), `mismatch_codes` (TEXT[]) — cột lưu cảnh báo OCR. Khi đọc chi tiết một hóa đơn `draft` hoặc `reviewed`, giá trị trả về cho client là danh sách mã chặn **tính lại tại thời điểm đọc** gộp với cảnh báo OCR đã lưu, không phải giá trị thô trong cột (xem mục 4). Luôn là mảng, rỗng khi hóa đơn sạch, không bao giờ `null`
* `split_method` (TEXT: `even`, `item_ratio`, `exact`, `shares`, `percentage`)
* `reviewed_at`, `reviewed_by_member_id` — bị xóa (`NULL`) mỗi khi có mutation thay đổi ý nghĩa hóa đơn
* `replaces_bill_id` (self FK, unique — chỉ được trỏ tới một bill đã `voided` trong cùng nhóm), `voided_at`
* `finalized_at`, `created_at`, `updated_at`
* *Constraint*: `bills_finalized_check` (đã finalize/voided thì `finalized_at` phải có giá trị), `bills_voided_check`, `uq_bills_replacement`
* *Index*: `idx_bills_group_created`/`idx_bills_group_cursor` phục vụ cursor pagination `(created_at DESC, id DESC)`

### `bill_images`
Ảnh biên lai riêng tư, bất biến sau khi tạo:
* `id`, `bill_id`, `group_id`
* `image_key` (object key riêng tư trên Cloudinary)
* `position` (SMALLINT, 0 đến 4 — tối đa 5 ảnh)
* *Unique*: `(bill_id, position)`

### `bill_items`
Danh sách món hàng của hóa đơn (tối đa 100 dòng):
* `id`, `bill_id`, `group_id`, `name`, `position` (SMALLINT)
* `quantity` (NUMERIC(10,2)), `unit_price` (BIGINT), `line_total` (BIGINT — tổng giá gốc trước giảm giá món)
* `discount_amount` (BIGINT — tiền khuyến mãi riêng của món), `final_price` (BIGINT — giá thực tế sau giảm giá: `line_total - discount_amount`)

### `bill_item_assignments`
Gán món hàng cho thành viên theo tỷ lệ:
* `id`, `bill_item_id`, `group_id`, `member_id`
* `weight` (NUMERIC(10,4), mặc định 1, phải > 0)
* *Unique*: `(bill_item_id, member_id)`

### `bill_shares`
Snapshot bất biến số tiền cuối cùng mỗi thành viên phải trả, chỉ được ghi khi finalize:
* `id`, `bill_id`, `group_id`, `member_id`
* `item_subtotal`, `service_charge_share`, `vat_share`, `discount_share`, `rounding_adjustment`, `final_amount` (BIGINT)
* `rounding_adjustment` giữ phương trình thành phần bằng `final_amount` và có thể thuộc bất kỳ thành viên nào
* `Σ final_amount` luôn bằng đúng `bills.total`; `Σ` các `final_amount` dương của thành viên không phải Creditor bằng tổng `debts` của hóa đơn
* *Unique*: `(bill_id, member_id)` — ghi cho tất cả thành viên có gán, kể cả khi `final_amount = 0`

### `ocr_jobs`
Lịch sử các lần chạy OCR:
* `id`, `bill_id`, `status` (`ocr_job_status`: `queued`, `processing`, `succeeded`, `failed`)
* `provider` (mặc định `llamaextract`), `attempts` (INT)
* `raw_response` (JSONB, dọn sau `BILL_OCR_RAW_RETENTION_DAYS`), `candidate` (JSONB, kết quả đã chuẩn hóa)
* `error_message`, `version` (phiên bản bill tại thời điểm job bắt đầu, dùng để phát hiện stale apply)
* *Index*: partial unique `uq_ocr_jobs_active_bill` — tối đa 1 job `queued`/`processing` mỗi bill

### `bill_idempotency_keys`
Đảm bảo các mutation không bị lặp khi client retry:
* *Primary key*: `(actor_user_id, operation, key_hash)`
* `canonical_request_hash`, `operation_id`, `state` (`idempotency_state`: `in_progress`, `completed`)
* `response_code`, `response_body`, `resource_id`, `retry_after`
* `expires_at` (mặc định `now() + 24h`)
* *Index*: theo `expires_at` phục vụ dọn dẹp định kỳ
* Dòng đã quá `expires_at` được **chiếm lại** (reclaim) ngay trong câu lệnh reserve, và bị xóa hẳn bởi `IdempotencyRetentionWorker` chạy hằng ngày (xem mục 8)

### `debts` (mở rộng từ module Split & Settlement)
* Thêm trạng thái `voided` và cột `voided_at` — khi một bill bị hủy, mọi `debts` liên quan chuyển sang `voided` (yêu cầu chưa có `payment_id`)

---

## 7. Danh mục API Endpoints

Toàn bộ endpoint yêu cầu live bearer session (spec 0001) và được đăng ký phẳng dưới `/api/v1/bills`, nhóm được truyền qua `group_id` (query hoặc body) chứ không phải path `{groupId}` như mô tả sơ bộ trong spec.

| Method | Endpoint | Yêu cầu Auth | Mô tả |
|---|---|---|---|
| `POST` | `/api/v1/bills` | Active member | Tạo hóa đơn thủ công (`201`) hoặc kèm 1-5 ảnh biên lai (`202`, kèm OCR job). Cần `Idempotency-Key` |
| `GET` | `/api/v1/bills` | Active member | Liệt kê hóa đơn theo cursor pagination `(created_at, id)` |
| `GET` | `/api/v1/bills/{id}` | Active member | Chi tiết hóa đơn: draft/preview hoặc breakdown đã finalize, kèm URL ảnh ký 5 phút |
| `PUT`/`PATCH` | `/api/v1/bills/{id}` | Creditor hoặc Captain | Thay thế toàn bộ bản nháp (fields, items, assignments) theo version |
| `DELETE` | `/api/v1/bills/{id}` | Creditor hoặc Captain | Xóa bản nháp (idempotent), dọn ảnh qua hàng đợi dọn dẹp bền vững |
| `POST` | `/api/v1/bills/{id}/ocr-retry` | Creditor hoặc Captain | Chạy lại OCR (giới hạn `BILL_OCR_MANUAL_LIMIT` lần/`BILL_OCR_MANUAL_WINDOW_HOURS`) |
| `POST` | `/api/v1/bills/{id}/apply-candidate` | Creditor hoặc Captain | Áp dụng kết quả OCR vào bản nháp, yêu cầu version hiện tại |
| `GET` | `/api/v1/bills/{id}/events` | Active member | SSE: `snapshot`, `ocr.updated`, `bill.updated`, `heartbeat` |
| `POST` | `/api/v1/bills/{id}/review` | Creditor hoặc Captain | Xét duyệt tường minh phiên bản hiện tại |
| `POST` | `/api/v1/bills/{id}/finalize` | Captain | Chốt sổ: tạo `bill_shares`, `debts`, activity, job thông báo trong 1 transaction |
| `POST` | `/api/v1/bills/{id}/void` | Captain | Hủy hóa đơn đã chốt sổ (chỉ khi mọi debt còn `awaiting`, chưa có payment) |

### Mã lỗi hay gặp

| Mã | HTTP | Khi nào |
|---|---|---|
| `VERSION_CONFLICT` | 409 | Version client gửi không phải version hiện tại của bill |
| `OCR_RESULT_STALE` | 409 | Bill đã đổi version kể từ lúc job OCR bắt đầu |
| `BILL_NOT_READY` | 422 | Còn mã chặn khi review hoặc chốt sổ, hoặc hóa đơn chưa có Creditor |
| `DISCOUNT_NOT_ALLOCATABLE` | 422 | Giảm giá hợp lệ so với tổng nhưng dồn vào những thành viên không hấp thụ hết, khiến phần bị chặn trần đẩy Creditor xuống âm |
| `BANK_ACCOUNT_REQUIRED` | 422 | Creditor chưa có tài khoản ngân hàng đầy đủ khi chốt sổ |
| `IDEMPOTENCY_KEY_REUSED` | 409 | Cùng `Idempotency-Key` nhưng body khác |
| `IDEMPOTENCY_IN_PROGRESS` | 409 | Cùng `Idempotency-Key` đang được xử lý |
| `BILL_IMMUTABLE` / `BILL_ALREADY_VOIDED` | 409 | Sửa hóa đơn đã chốt sổ hoặc đã hủy |

Chi tiết request/response đầy đủ xem [docs/openapi.yaml](openapi.yaml) (từ dòng 548).

---

## 8. Tác vụ ngầm định kỳ (Background Workers)

Module Bill sử dụng River Queue (bảng `river_job`, PostgreSQL-backed) thay vì cron nội bộ như module Auth:

1. **OCR Worker** (`internal/modules/bill/jobs/ocr_worker.go`, kind `bill_ocr`): xử lý từng job OCR theo hàng đợi, tự quản lý backoff theo `BILL_OCR_RETRY_BASE_DELAY_SECONDS`.
2. **OCR Retention Worker** (kind `ocr_raw_retention_cleanup`): dọn `raw_response` sau `BILL_OCR_RAW_RETENTION_DAYS`, giữ nguyên `candidate`.
3. **Idempotency Retention Worker** (kind `bill_idempotency_key_cleanup`): xóa các dòng `bill_idempotency_keys` đã quá `expires_at`.
4. **Queue Depth Poller** (`app.go`, không phải worker River): quét độ sâu hàng đợi mỗi 15 giây, chỉ phục vụ metric `paysplit_ocr_queue_depth`, không xử lý job.

Hai worker dọn dẹp ở mục 2 và 3 được đăng ký qua `jobs.RegisterRetentionJobs` (`internal/modules/bill/jobs/retention.go`), chạy mỗi 24 giờ với `RunOnStart` bật nên chúng chạy luôn khi khởi động. Phần nối dây tách riêng khỏi `app.go` để kiểm thử được: lỗi quên đăng ký worker chỉ có test ở tầng nối dây mới bắt được, và đó đúng là lỗi đã từng xảy ra với `OCRRetentionWorker`.

**Khoảng trống vận hành cần lưu ý**: worker dọn media (`internal/modules/auth/jobs`) hiện chỉ được nối dây (wire) với `avatarStore` của module Auth, chưa bao gồm `billStorage` — nghĩa là ảnh biên lai bị xóa (qua thao tác xóa draft) chưa chắc được dọn tự động khỏi Cloudinary bởi cùng một worker chung; cần xác nhận cơ chế dọn ảnh bill đang chạy qua đường nào trước khi coi đây là đã hoàn thiện.

---

## 9. Cấu hình môi trường (Environment Variables)

| Biến | Mặc định | Mô tả |
|---|---|---|
| `LLAMAINDEX_API_KEY` | — | API key của LlamaExtract |
| `LLAMAINDEX_EXTRACT_ENDPOINT` | `https://api.cloud.llamaindex.ai` | Endpoint trích xuất biên lai |
| `BILL_OCR_PROVIDER_TIMEOUT_SECONDS` | 8 | Timeout gọi provider OCR |
| `BILL_OCR_MAX_ATTEMPTS` | 3 | Số lần thử tự động của River |
| `BILL_OCR_RETRY_BASE_DELAY_SECONDS` | 1 | Delay khởi điểm cho backoff mũ 2 |
| `BILL_OCR_MANUAL_LIMIT` | 5 | Số lần người dùng chủ động retry OCR mỗi cửa sổ |
| `BILL_OCR_MANUAL_WINDOW_HOURS` | 24 | Cửa sổ tính giới hạn retry thủ công |
| `BILL_OCR_RAW_RETENTION_DAYS` | 30 | Thời gian lưu `raw_response` trước khi dọn |
| `BILL_IMAGE_MAX_COUNT` | 5 | Số ảnh tối đa mỗi hóa đơn |
| `BILL_IMAGE_MAX_BYTES` | 10 MB | Dung lượng tối đa mỗi ảnh |
| `BILL_IMAGE_UPLOAD_TIMEOUT_SECONDS` | 15 | Timeout upload ảnh lên Cloudinary |
| `BILL_IMAGE_PROCESSING_TIMEOUT_SECONDS` | 10 | Timeout xử lý/chuẩn hóa ảnh |
| `BILL_IMAGE_SIGNED_URL_TTL_MINUTES` | 5 | Thời hạn URL ảnh đã ký cho mobile |
| `BILL_SSE_HEARTBEAT_INTERVAL_SECONDS` | 15 | Chu kỳ heartbeat SSE |
| `BILL_SSE_MAX_CONNECTION_AGE_MINUTES` | 15 | Thời gian tối đa một kết nối SSE trước khi buộc reconnect |

---

## 10. Vị trí logic nghiệp vụ chính

* **Đối soát dùng chung**: `internal/modules/bill/usecase/reconciliation.go:43` (`evaluateAllocation`), dùng bởi đọc chi tiết, review và chốt sổ
* **Phân bổ chính xác**: `internal/modules/bill/usecase/allocation.go` (`CalculateAllocation`), dùng `math/big.Rat` và Largest Remainder
* **Chốt sổ (Finalize)**: `internal/modules/bill/usecase/service.go:724` (`FinalizeBill`, wrapper đo metric) gọi `finalizeBillImpl:735` — kiểm tra vai trò Captain, version, trạng thái, yêu cầu tài khoản ngân hàng, chạy `evaluateAllocation`, ghi `bill_shares` + `debts` + notification, commit qua `repo.FinalizeBill`
* **Hủy hóa đơn (Void)**: `internal/modules/bill/usecase/service.go:877` (`VoidBill`) — chỉ Captain, lý do 1-500 ký tự, yêu cầu `status = finalized` và mọi debt còn `awaiting`
* **OCR Worker**: `internal/modules/bill/jobs/ocr_worker.go` — `Work` (dòng 121-257), `NextRetry` (dòng 85)
* **Worker dọn dẹp**: `ocr_worker.go` — `OCRRetentionWorker` (dòng 282), `IdempotencyRetentionWorker` (dòng 347); phần đăng ký ở `internal/modules/bill/jobs/retention.go` (`RegisterRetentionJobs`)
* **Enqueue OCR job**: `internal/modules/bill/jobs/ocr_worker.go:392` (`EnqueueOCRJobTx`, chạy trong cùng transaction tạo bill/ảnh)
* **Chiếm lại khóa idempotency hết hạn**: `internal/modules/bill/repository/postgres/repository.go:1353` (`ReserveIdempotencyKey`)
* **Adapter LlamaExtract**: `internal/platform/ocr/llamaextract/client.go`, `normalizer.go`, `schema.go`
