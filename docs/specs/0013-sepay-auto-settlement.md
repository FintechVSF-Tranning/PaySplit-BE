# 0013 — Tự động đối soát thanh toán qua SePay

> Trạng thái: đã code xong ở cả `PaySplit-BE` và `PaySplit-FE`

---

## 1. Tóm tắt

PaySplit chuyển từ **xác nhận thanh toán bằng ảnh minh chứng** sang **tự động đối soát theo biến động số dư ngân hàng** qua SePay.

- Người trả quét mã VietQR và chuyển tiền. Không phải chụp ảnh, không phải gửi gì.
- Tiền vào tài khoản người nhận → SePay gọi webhook về PaySplit → hệ thống tự gạch nợ.
- Người nhận không phải duyệt gì cả. Chỉ khi ngân hàng không tự khớp được mới cần bấm **"Đã nhận tiền"**.

Nguyên tắc: **không giữ tiền hộ**. PaySplit không phải ví, không trung gian thanh toán. Tiền đi thẳng từ tài khoản người trả sang tài khoản người nhận; PaySplit chỉ đọc tín hiệu "tiền đã vào" để gạch nợ.

---

## 2. Luồng cũ đang bị thay thế

### 2.1 Cách nó hoạt động

```
Người trả: mở QR → chuyển tiền → chụp màn hình biên lai → tải ảnh lên (kèm lời nhắn)
           → payment: pending_proof → pending_confirmation, nợ: awaiting → pending_confirmation
Người nhận: mở app → xem ảnh biên lai → bấm "Xác nhận" hoặc "Từ chối"
           → xác nhận: payment confirmed, nợ settled
           → từ chối: payment rejected (kèm lý do), nợ quay lại awaiting
Nếu người nhận quên duyệt 48 giờ: job nền gửi thông báo nhắc
```

### 2.2 Thành phần của luồng cũ

| Lớp | Thành phần |
|---|---|
| API | `POST /groups/{groupId}/payments/{paymentId}/proof` (multipart ảnh + ghi chú), `.../confirm`, `.../reject` |
| Lưu trữ | Ảnh biên lai lưu Cloudinary dạng WebP riêng tư, đọc qua signed URL 5 phút |
| DB | `payments.image_object_key`, `payments.note`, `payments.rejection_reason`, `payments.stalled_alerted_at`, trạng thái `pending_confirmation` |
| Job nền | Quét payment treo quá 48 giờ để nhắc người nhận duyệt; dọn ảnh rác trên Cloudinary |
| Cấu hình | `PAYMENT_PROOF_MAX_BYTES`, `PAYMENT_PROOF_SIGNED_URL_TTL`, `STALLED_CONFIRMATION_HOURS` |
| Màn hình | Sheet QR có khu tải ảnh; tab "Cần thu" có thẻ "Chờ duyệt" với nút *Xem & xác nhận* / *Từ chối*; sheet duyệt biên lai; sheet xem biên lai đã gửi; dialog nhập lý do từ chối |

### 2.3 Vì sao bỏ

1. **Ảnh không chứng minh được gì.** Ảnh chụp màn hình chuyển khoản có thể chỉnh sửa; PaySplit không xác minh được. Nó chỉ là lời hứa có kèm hình.
2. **Tốn hai lượt thao tác thủ công** cho mỗi khoản nợ: một bên tải ảnh, một bên duyệt. Người nhận quên duyệt thì khoản nợ treo, phải có job nhắc.
3. **Tốn hạ tầng**: lưu ảnh riêng tư, ký URL, hàng đợi dọn ảnh rác.
4. Khi đã có tín hiệu thật từ ngân hàng thì mọi thứ trên đều thừa.

---

## 3. Luồng mới

### 3.1 Sơ đồ

```
Người trả (app)                PaySplit BE                SePay              Ngân hàng
     │ mở QR ─────────────────────▶│
     │◀── QR: SEVQR PAYxxxxxxxx ───│
     │ quét QR, chuyển tiền ─────────────────────────────────────────────────▶│
     │                              │                        │◀── biến động ──│
     │                              │◀── webhook (JSON) ─────│
     │                              │ đối soát: mã + số tiền + tài khoản
     │                              │ → payment confirmed, nợ settled
     │◀── realtime payment_changed ─│──── thông báo cho cả hai bên ───▶ Người nhận
```

### 3.2 Hai đường xác nhận

| Đường | Ai kích hoạt | Khi nào dùng | `confirmation_source` |
|---|---|---|---|
| Ngân hàng | SePay webhook | Mặc định, gần như mọi giao dịch | `bank_transfer` |
| Người nhận | Người nhận bấm "Đã nhận tiền" | Dự phòng: chuyển sai số tiền, mất mã trong nội dung, SePay lỗi | `manual` |

Không còn đường thứ ba nào. Ảnh minh chứng đã bị gỡ hoàn toàn.

---

## 4. Kỹ thuật — Backend

### 4.1 Module mới `internal/modules/sepay`

Theo đúng Clean Architecture của repo:

```
internal/modules/sepay/
├── domain/transaction.go          # Entity giao dịch + hằng match status + SettleRequest/SettleResult
├── repository/repository.go       # Port: SaveTransaction, MatchStatus, MarkProcessed
├── repository/postgres/           # Adapter pgx + sqlc (queries/sepay.sql)
├── usecase/service.go             # Chuẩn hóa payload, quyết định có đối soát không
├── integration/settlement.go      # Adapter gọi sang settlement, giữ hai module không phụ thuộc kiểu của nhau
└── delivery/http/handler.go       # POST /api/v1/webhooks/sepay
```

### 4.2 Endpoint webhook

`POST /api/v1/webhooks/sepay` — không dùng session, xác thực bằng API key.

- Header `Authorization: Apikey <SEPAY_WEBHOOK_API_KEY>`; so khớp bằng `subtle.ConstantTimeCompare` để không lộ thông tin qua thời gian phản hồi.
- Key rỗng → 503 `SEPAY_WEBHOOK_DISABLED`; key sai → 401.
- Body giới hạn 64 KiB. **Chấp nhận field lạ** (không dùng `helpers.ReadJSON` vốn từ chối field lạ) để SePay thêm field mới không làm rớt giao dịch.
- Trả `200` kèm envelope chuẩn có `"success": true` — đúng điều kiện SePay coi là thành công (200/201 + `success: true`, xong trong 30 giây).
- Lỗi lưu trữ → `500` để SePay tự gửi lại (tối đa 7 lần trong 5 giờ). Dữ liệu không khớp nghiệp vụ vẫn trả `200`, vì gửi lại cũng không đổi được kết quả.

Chuẩn hóa payload (`usecase/service.go`):

- `id <= 0`, `transferType` ngoài `in`/`out`, `transferAmount < 0`, `transactionDate` không đọc được → từ chối 400.
- `id = 0` là payload của nút **"Gửi thử"** trên SePay → trả `200` với `match_status=test_delivery`, **không lưu** (id=0 không chống trùng được).
- `gateway`, `accountNumber`, `transactionDate` để trống vẫn chấp nhận: thiếu tài khoản thì không thể gạch nợ nhầm, vì bước đối soát bắt buộc khớp tài khoản.
- Giờ giao dịch không kèm múi giờ được hiểu là giờ Việt Nam (UTC+7 cố định, không dùng `LoadLocation` để không phụ thuộc tzdata trong image).

### 4.3 Bảng `sepay_transactions` (migration 000020, 000021)

| Cột | Ý nghĩa |
|---|---|
| `id` BIGINT **PK** | ID giao dịch phía SePay — khóa chống trùng khi SePay gửi lại |
| `gateway`, `account_number`, `sub_account`, `code`, `content`, `transfer_type`, `transfer_amount`, `accumulated`, `reference_code`, `description` | Dữ liệu thô SePay gửi |
| `transaction_date` | Đã quy đổi về UTC+7 |
| `payment_reference` | Mã `PAYxxxxxxxx` trích được từ `code`/`content` |
| `raw_payload` JSONB | Nguyên văn payload, để truy vết |
| `match_status`, `payment_id`, `processed_at` | Kết quả đối soát (NULL = chưa xử lý xong) |

Chống trùng: `INSERT ... ON CONFLICT (id) DO NOTHING`. Nếu giao dịch đã có mà `match_status` vẫn NULL (lần trước lỗi giữa chừng) thì lần gửi lại sẽ xử lý tiếp. `MarkTransactionProcessed` chỉ ghi khi `match_status IS NULL`, nên hai lần gửi song song không ghi đè kết quả thật.

### 4.4 Các giá trị `match_status`

| Giá trị | Nghĩa |
|---|---|
| `confirmed` | Đã gạch nợ bằng giao dịch này |
| `already_confirmed` | Chính giao dịch này đã gạch trước đó (SePay gửi lại) |
| `payment_not_found` | Không có payment nào mang mã tham chiếu đó |
| `payment_closed` | Payment đã xác nhận/từ chối/bị thay thế, hoặc nhóm đã đóng |
| `amount_mismatch` | Số tiền nhận khác số tiền của payment |
| `account_mismatch` | Tiền vào tài khoản không phải của người nhận |
| `no_reference` | Nội dung không chứa mã `PAYxxxxxxxx` |
| `zero_amount` | Tiền vào 0đ |
| `ignored_outgoing` | Giao dịch tiền ra |
| `test_delivery` | Payload nút "Gửi thử" (`id=0`), không lưu |

Bảng này là công cụ chẩn đoán chính khi có người báo "đã chuyển mà không thấy gạch nợ".

### 4.5 Quy tắc đối soát (`settlement/repository/postgres/bank_transfer.go`)

`SettleBankTransfer` chạy trong **một transaction**, khóa theo thứ tự **nhóm → các khoản nợ → payment** (giống mọi luồng khác của module, để không deadlock với người dùng đang thao tác cùng payment).

Chỉ gạch nợ khi khớp **đủ bốn** điều kiện:

1. `transferType = in`.
2. Nội dung chứa mã `PAYxxxxxxxx` và tìm được payment mang mã đó.
3. Số tiền **bằng đúng** số tiền của payment.
4. Tài khoản nhận trùng tài khoản ngân hàng mặc định của người nhận (so cả tài khoản chính lẫn tài khoản ảo nếu SePay gửi `subAccount`).

Ngoài ra mọi khoản nợ của payment phải còn mở. Không khớp thì trả `Outcome` tương ứng và **không đổi một dòng dữ liệu nào**.

Regex trích mã: `PAY[A-HJ-NP-Z2-9]{8}` — bảng chữ không có `I`, `O`, `0`, `1` để tránh nhìn nhầm; tìm ở **bất kỳ vị trí nào** trong nội dung và không phân biệt hoa thường, vì ngân hàng hay thêm mã của họ vào đầu nội dung và có thể đổi hoa thường.

### 4.6 Đường dự phòng: "Đã nhận tiền"

`POST /api/v1/groups/{groupId}/debts/{debtId}/mark-received` — session + `Idempotency-Key` bắt buộc.

Ngữ nghĩa: **bấm trên khoản nợ nào thì gạch đúng khoản đó.** Trong một transaction:

1. Khóa nhóm, khóa khoản nợ. Người gọi phải là **người nhận** của khoản nợ (`403` nếu không), khoản nợ phải đang `awaiting` (`409 DEBT_NOT_AWAITING`).
2. Mọi payment `pending_proof` đang gom khoản nợ này chuyển sang `superseded` — mã QR cũ hết hiệu lực. Webhook của mã đó về sau chỉ ra `payment_closed`, **không thể gạch lần hai**.
3. Tạo payment mới `confirmed`, `confirmation_source='manual'`, chụp snapshot tài khoản người nhận; nếu người nhận chưa cài tài khoản ngân hàng → `422 BANK_ACCOUNT_REQUIRED`.
4. Gạch nợ, ghi hoạt động nhóm, phát sự kiện realtime, gửi thông báo cho người trả.

### 4.7 Phần dùng chung

`publishPaymentSettled` được tách ra và dùng chung cho cả hai đường xác nhận, để client luôn nhận cùng một bộ tín hiệu:

| Sự kiện realtime | Ai nghe |
|---|---|
| `settlement.payment_changed` (kèm `resource_id` = id payment) | Màn Công nợ, và sheet QR đang mở đúng payment đó |
| `group.debts_changed` | Tab công nợ của nhóm |
| `bill.settlement_changed` | Chi tiết hóa đơn liên quan |
| `group.activity_changed` | Dòng thời gian nhóm |
| `home.balance_changed` | Trang chủ của cả hai bên |

Thông báo (đều lưu `type = payment_confirmed` để client cũ vẫn điều hướng đúng):

| Kind nội bộ | Người nhận thông báo | Nội dung |
|---|---|---|
| `payment_bank_confirmed` | Người trả | "Ngân hàng đã ghi nhận chuyển khoản của bạn, khoản nợ đã được gạch." |
| `payment_bank_received` | Người nhận | "Một khoản chuyển khoản trong nhóm đã vào tài khoản của bạn và được tự động đối soát." |
| `payment_marked_received` | Người trả | "Người nhận đã xác nhận đã nhận đủ tiền, khoản nợ đã được gạch." |

### 4.8 Thay đổi schema

| Migration | Nội dung |
|---|---|
| `000020_sepay_transactions_v1` | Tạo bảng `sepay_transactions` |
| `000021_payment_bank_confirmation_v1` | `payments.confirmation_source` (`manual`/`bank_transfer`), `payments.bank_transaction_id` + unique index (một giao dịch ngân hàng chỉ gạch được một payment); nới `chk_payments_state_matrix` cho payment xác nhận qua ngân hàng không cần ảnh; thêm `match_status`, `payment_id`, `processed_at` cho `sepay_transactions` |
| `000022_manual_receipt_v1` | Nhánh `confirmed` của `chk_payments_state_matrix` bỏ hẳn yêu cầu ảnh, vì cả hai đường xác nhận mới đều không có ảnh |

Cột cũ (`image_object_key`, `note`, `rejection_reason`, trạng thái `pending_confirmation`) được **giữ nguyên** để không mất dữ liệu lịch sử.

### 4.9 Nội dung chuyển khoản và tiền tố ngân hàng

Phát hiện khi test thật: **VietinBank cá nhân kết nối qua SePay chỉ đẩy giao dịch có nội dung bắt đầu bằng `SEVQR`.** QR chỉ ghi `PAYxxxxxxxx` thì ngân hàng không báo cho SePay, và webhook không bao giờ tới.

Xử lý:

- Thêm `PAYMENT_TRANSFER_CONTENT_PREFIX` (đặt `SEVQR`). Bỏ trống nếu ngân hàng không yêu cầu tiền tố.
- `vietqr.Generator` sinh nội dung `SEVQR PAYxxxxxxxx` trong cả payload QR lẫn ảnh QR. Chuỗi dài 17 ký tự, vẫn dưới giới hạn 25 ký tự của trường nội dung VietQR. Cấu hình được kiểm tra lúc khởi động: chỉ chữ và số, tối đa 13 ký tự.
- API trả thêm `transfer_content` để app hiển thị và copy đúng chuỗi đầy đủ. Đối soát không đổi vì regex tìm mã `PAY...` ở bất kỳ đâu.

### 4.10 Bảo mật và log

- Webhook không dùng session; API key so khớp thời gian hằng định.
- Theo quy định trong `internal/modules/settlement/README.md`, log **không chứa** mã tham chiếu, số tài khoản hay nội dung chuyển khoản. Log webhook chỉ ghi `id`, loại, số tiền, `has_reference`, `match_status`, `payment_id`. Payload lỗi chỉ ghi lý do (tên field sai), không ghi nguyên văn.

### 4.11 Những gì bị gỡ khỏi backend

| Nhóm | Gỡ |
|---|---|
| Endpoint | `POST .../payments/{paymentId}/proof`, `.../confirm`, `.../reject` |
| Usecase/Repository | `SubmitProof`, `PrepareProof`, `ResetProofAttempt`, `ConfirmPayment`, `RejectPayment`, `finishPayment`, `ProcessStalledPayments`, `ProcessMediaCleanup`, `QueueMediaCleanup` |
| Job | Quét payment treo chờ duyệt; hàng đợi dọn ảnh của settlement (module `auth` vẫn có worker dọn `media_cleanup_jobs` dùng chung) |
| Hạ tầng | `internal/platform/storage/cloudinary/proof.go` (adapter lưu ảnh minh chứng) |
| Cấu hình | `PAYMENT_PROOF_MAX_BYTES`, `PAYMENT_PROOF_SIGNED_URL_TTL`, `STALLED_CONFIRMATION_HOURS` |
| Lỗi domain | `ErrInvalidImage`, `ErrPaymentNotPendingProof`, `ErrPaymentNotPendingConfirmation`, `ErrStorageUnavailable` |

---

## 5. Kỹ thuật — Frontend

### 5.1 Tầng dữ liệu

- Bỏ `submitProof` / `confirmPayment` / `rejectPayment` khỏi datasource, repository và mock.
- Thêm `markDebtReceived(groupId, debtId)` — khóa idempotency cố định theo khoản nợ, nên bấm lại sau timeout sẽ được BE replay thay vì báo "khoản nợ đã gạch".
- Thêm `getPaymentStatus(groupId, paymentId)` trả `PaymentStatusEntity {status, confirmationSource}`.
- Entity: bỏ `ProofUploadEntity`, `pendingProofs`, `submittedProofs`, `pendingProofCount`; đổi `ProofDetailEntity` → `PaymentDetailEntity` (thêm `confirmationSource`, giữ `legacyProofImageUrl` cho dữ liệu cũ); `PaymentQrEntity` thêm `transferContent`.

### 5.2 Theo dõi trạng thái payment bằng realtime

- Thêm surface realtime mới `settlement.payment`, khóa theo `(groupId, paymentId)`.
- `user_realtime_owner` định tuyến `settlement.payment_changed` tới đúng sheet đang mở theo `resource_id`. Không có `resource_id` thì không làm mới bừa mọi sheet.
- `paymentStatusProvider` (Riverpod `FutureProvider.autoDispose.family`) đăng ký interest và tự nạp lại khi có sự kiện. **Không polling**: lượt `ready` sau mỗi lần SSE kết nối lại đã làm mới mọi interest.

### 5.3 Màn hình — người trả

Sheet QR (`dynamic_vietqr_sheet.dart`) có ba trạng thái:

| Trạng thái | Hiển thị |
|---|---|
| Chờ | QR, số tiền, người nhận, ngân hàng, số tài khoản, **nội dung chuyển đầy đủ** kèm nút copy; dòng "Đang chờ ngân hàng xác nhận…" và nhắc giữ nguyên số tiền/nội dung; nút "Kiểm tra lại" |
| Thành công | Icon xanh, số tiền, câu ghi rõ ai xác nhận ("Ngân hàng đã xác nhận giao dịch" hoặc "[tên] đã xác nhận đã nhận tiền"), rung nhẹ, nút "Xong" |
| Hết hiệu lực | "Mã QR này không còn dùng được" — khi payment bị `superseded`/`rejected`, để người trả không chuyển thêm lần nữa |

Toàn bộ khu tải ảnh, ô lời nhắn và nút "Xác nhận đã chuyển tiền" đã bị bỏ.

### 5.4 Màn hình — người nhận

- Tab **"Cần thu"** (`receivable_debts_tab.dart`) thay cho tab duyệt minh chứng: mỗi khoản nợ có "Nhắc nợ" (giữ nguyên cooldown 24 giờ và trần 3 lần) và **"Đã nhận tiền"**.
- "Đã nhận tiền" mở dialog hỏi lại, nêu rõ số tiền và ghi chú *chỉ dùng khi ngân hàng không tự xác nhận*.
- Trang chủ: phần "Khoản nợ cần xử lý" bỏ nút duyệt proof, thêm "Đã nhận tiền".
- Tab **Lịch sử**: sheet chi tiết chỉ để xem (`payment_detail_sheet.dart`), có nhãn nguồn xác nhận; payment cũ còn ảnh thì vẫn xem được ảnh.
- Đã xóa: `receivable_proofs_tab.dart`, `proof_review_sheet.dart`, `submitted_proof_sheet.dart`, `reject_proof_dialog.dart`.

### 5.5 Điểm lệch so với kế hoạch

Trang **chi tiết nhóm** không có nút "Đã nhận tiền", chỉ bỏ nút "Duyệt proof". Lý do: bảng công nợ ở đó **gộp theo từng người** (một dòng có thể là tổng nhiều khoản nợ, thậm chí bù trừ hai chiều), nên không xác định được bấm thì gạch khoản nào. Nút này đặt ở tab "Cần thu" và trang chủ, nơi mỗi dòng đúng là một khoản nợ.

### 5.6 Lỗi có sẵn được phát hiện và sửa kèm

Màn Công nợ nạp chi tiết payment cho **mọi khoản nợ đã gạch trong nhóm**, kể cả khoản giữa hai người khác. Backend chỉ cho người trả, người nhận hoặc trưởng nhóm xem chi tiết payment → trả `403`, và app coi đó là lỗi của cả lượt nạp nên toàn màn hình trắng ("Bạn không có quyền thực hiện thao tác này").

Lỗi này có từ trước, chỉ lộ ra khi trong nhóm bắt đầu có nhiều khoản nợ đã gạch. Đã sửa: chỉ nạp payment của khoản nợ mà người dùng là một bên. Tab Lịch sử vốn chỉ nói về giao dịch của chính họ nên không mất dữ liệu, và còn giảm số request.

---

## 6. So sánh cũ ↔ mới

| Hạng mục | Cũ | Mới |
|---|---|---|
| Bằng chứng thanh toán | Ảnh chụp màn hình do người trả tải lên | Giao dịch ngân hàng thật, do SePay báo |
| Thao tác của người trả | Quét QR → chuyển tiền → chụp ảnh → tải lên | Quét QR → chuyển tiền |
| Thao tác của người nhận | Mở app → xem ảnh → duyệt hoặc từ chối | Không phải làm gì |
| Thời gian gạch nợ | Chờ người nhận rảnh, có thể vài ngày | Vài giây đến vài phút sau khi tiền vào |
| Trạng thái trung gian | `pending_confirmation` | Không còn; nợ đi thẳng `awaiting` → `settled` |
| Đường dự phòng | Từ chối minh chứng, gửi lại ảnh | Người nhận bấm "Đã nhận tiền" |
| Job nền | Nhắc duyệt minh chứng sau 48 giờ, dọn ảnh | Không cần |
| Hạ tầng | Cloudinary lưu ảnh riêng tư + signed URL | Không lưu gì thêm ngoài bản ghi giao dịch |
| Nội dung chuyển khoản | `PAYxxxxxxxx` | `SEVQR PAYxxxxxxxx` (tiền tố cấu hình được) |

---

## 7. Tương thích ngược

- Payment cũ ở trạng thái `pending_confirmation` và ảnh cũ vẫn nằm trong DB; sheet chi tiết vẫn xem được ảnh của chúng.
- Khoản nợ cũ đang `pending_confirmation` hiển thị nhãn "Đang xử lý", không có nút thao tác. Chúng vẫn có thể được gạch nếu webhook ngân hàng khớp.
- Thông báo mới vẫn lưu `type = payment_confirmed`, nên bản app cũ điều hướng đúng màn hình.
- `image_url` trong API vẫn còn nhưng luôn là `null` với payment mới, giữ để client cũ không vỡ.

---

## 8. Kiểm thử

**Backend** — `go test ./...` pass, gồm integration test chạy trên PostgreSQL thật:

- Webhook: thiếu/sai API key, payload lỗi, payload thưa field, tiền ra, tiền 0đ, nút "Gửi thử", SePay gửi lại, lỗi lưu trữ trả 500.
- Đối soát: không tìm thấy mã, sai số tiền, sai tài khoản, khớp đủ, gửi lại, chuyển lần hai vào QR đã thanh toán.
- "Đã nhận tiền": người trả gọi bị 403, gạch đúng một khoản, QR đang chờ bị `superseded`, webhook về muộn ra `payment_closed`, gọi lại cùng key thì replay.
- QR: tiền tố `SEVQR` có trong payload, trong ảnh QR, và trong cả nhánh trả lại mã QR đã tồn tại.
- 3 route cũ trả 404.

**Frontend** — `flutter analyze` sạch, `flutter test` 410 test pass:

- Sheet QR: trạng thái chờ, chuyển sang thành công khi có sự kiện realtime, phân biệt ngân hàng xác nhận và người nhận xác nhận, QR hết hiệu lực, nút "Kiểm tra lại", không tràn layout.
- Định tuyến realtime `settlement.payment_changed` đúng payment.
- Tab "Cần thu": cooldown nhắc nợ, nút "Đã nhận tiền" gọi đúng khoản nợ, khóa nút khi đang có thao tác khác.
- Repository: chỉ nạp payment của chính mình, ánh xạ `confirmation_source`, khóa idempotency ổn định.

---

## 9. Vận hành

### 9.1 Biến môi trường mới

```bash
SEPAY_WEBHOOK_API_KEY=<chuỗi ngẫu nhiên ≥32 ký tự, trùng với API Key khai trên SePay>
PAYMENT_TRANSFER_CONTENT_PREFIX=SEVQR   # để trống nếu ngân hàng không yêu cầu tiền tố
```

Sinh key: `openssl rand -hex 32`. Key rỗng thì endpoint webhook trả 503.

### 9.2 Cấu hình trên SePay

1. Liên kết tài khoản ngân hàng nhận tiền trong SePay (đây là điều kiện tiên quyết; điền số tài khoản trong PaySplit không liên quan gì tới SePay).
2. Tạo webhook: URL `https://<domain>/api/v1/webhooks/sepay`, loại giao dịch **Tiền vào**, định dạng **JSON**, bật **tự động gửi lại**, xác thực **API Key** bằng giá trị trong `.env`.
3. Môi trường dev dùng tunnel: `cloudflared tunnel --url http://localhost:8080`. **Mỗi lần chạy lại tunnel là một tên miền mới**, phải cập nhật URL trên SePay.
4. Muốn nghiệm thu mà không phụ thuộc ngân hàng: bật Test Mode của SePay và dùng "Mô phỏng giao dịch". Lưu ý cấu hình webhook của Test Mode tách riêng với Live.

### 9.3 Chẩn đoán khi "đã chuyển mà không gạch nợ"

Theo thứ tự:

```sql
-- 1. Webhook có tới không, và kết quả đối soát là gì?
SELECT id, transfer_amount, content, payment_reference, match_status, payment_id
FROM sepay_transactions ORDER BY received_at DESC LIMIT 10;
```

- **Bảng trống** → webhook chưa tới. Kiểm tra URL trên SePay còn sống không, SePay có thấy giao dịch không, nội dung chuyển khoản có tiền tố ngân hàng yêu cầu không.
- `no_reference` → nội dung mất mã `PAY...`.
- `amount_mismatch` / `account_mismatch` → xem lại số tiền và tài khoản mặc định của người nhận.
- `payment_closed` → khoản nợ đã được gạch bằng đường khác.

Thống kê HTTP có sẵn ở `/metrics` (`paysplit_http_requests_total`) rất hữu ích để tìm endpoint đang trả 4xx/5xx.

---

## 10. Sự cố gặp khi test thật và cách xử lý

| Sự cố | Nguyên nhân | Xử lý |
|---|---|---|
| Webhook không bao giờ tới | Tunnel `cloudflared` khởi động lại nên đổi tên miền, SePay vẫn gọi địa chỉ cũ | Cập nhật URL trên SePay sau mỗi lần chạy lại tunnel |
| SePay không thấy giao dịch dù tiền đã vào | VietinBank cá nhân chỉ đẩy giao dịch có nội dung bắt đầu bằng `SEVQR` | Thêm tiền tố vào nội dung QR (mục 4.9) |
| App vẫn hiện nội dung thiếu tiền tố | Nhánh "trả lại mã QR đã tồn tại" chưa được gán nội dung mới; bản vá đầu tiên không khớp chuỗi nên bị bỏ qua âm thầm | Đã sửa và thêm test khóa chính nhánh đó |
| Một cửa sổ báo "Bạn không có quyền thực hiện thao tác này" | App nạp payment của khoản nợ giữa hai người khác → BE trả 403 → hỏng cả lượt nạp | Chỉ nạp payment của chính mình (mục 5.6) |

---

## 11. Hạn chế và việc còn lại

1. **Mỗi người nhận tiền cần tài khoản ngân hàng liên kết SePay.** Hiện hệ thống dùng **một API key chung**, đủ cho môi trường dev với một tài khoản SePay. Muốn chạy thật cho nhiều người thì phải cấp key riêng cho từng người dùng, hoặc gom tiền qua một tài khoản chung. Đây là quyết định sản phẩm còn bỏ ngỏ.
2. Tiền chuyển sai số tiền không tự khớp; hiện chỉ ghi nhận `amount_mismatch` và để người nhận tự xử lý bằng "Đã nhận tiền". Chưa có màn hình nào liệt kê các giao dịch lệch.
3. Chưa hỗ trợ trả một phần khoản nợ.
4. Trang chi tiết nhóm chưa có nút "Đã nhận tiền" (mục 5.5).
5. Chưa hoàn tất nghiệm thu end-to-end bằng tiền thật; đang vướng phần liên kết ngân hàng phía SePay.
6. Toàn bộ thay đổi **chưa commit** ở cả hai repo.
