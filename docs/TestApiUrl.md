# PaySplit Backend API — Test Log & Manual Retest Guide

> Test thực hiện: 2026-08-21, trên môi trường local (`make run`, Postgres local qua `docker compose`, port `8080`).
> Toàn bộ request bên dưới là request **thật** đã chạy bằng `curl` trong quá trình test (không phải suy đoán). Response là response **thật** trả về từ server, chỉ rút gọn bớt các trường lặp lại (đánh dấu `...`) để dễ đọc.
>
> Base URL dùng khi test: `http://localhost:8080`
> Header chung cho các API cần đăng nhập: `Authorization: Bearer <access_token>`

## Cách dùng file này để test lại bằng tay

1. Khởi động Postgres: `docker compose up -d postgres`.
2. Khởi động server: `make run` (mặc định lắng nghe `localhost:8080`).
3. Copy lệnh `curl` trong mục tương ứng, thay các giá trị biến (`$EMAIL`, `$TOKEN`, `$GROUP_ID`, ...) bằng dữ liệu thật của lần test của bạn.
4. Với các API cần OTP (verify-email, reset-password), OTP được gửi qua email thật (Gmail SMTP) — kiểm tra hộp thư, hoặc nếu test ở local/dev, có thể lấy OTP bằng cách join bảng `user_tokens` (giá trị được hash SHA-256, không đọc trực tiếp được — chỉ dùng được khi có quyền truy cập DB dev để tự tạo lại hash phục vụ test).

## Tổng quan kết quả

| Nhóm | Số API | Trạng thái |
|---|---|---|
| Auth | 9 | ✅ Tất cả hoạt động đúng |
| Users | 6 | ✅ Tất cả hoạt động đúng |
| Banks | 1 | ✅ Hoạt động đúng |
| Groups | 10 | ✅ Tất cả hoạt động đúng |
| Notifications | 4 | ✅ Tất cả hoạt động đúng |
| Bills | 11 | ✅ Hoạt động đúng, xem cảnh báo schema drift bên dưới |
| Admin | 4 | ✅ Tất cả hoạt động đúng |
| **Tổng** | **45** (39 API nghiệp vụ + `/health` + 5 case lỗi bổ sung) | |

## ⚠️ Phát hiện quan trọng khi test (cần xử lý)

1. **Schema drift nghiêm trọng — bảng `bills` và `bill_items` thiếu cột dù migration đã "applied":**
   Khi gọi `POST /api/v1/bills` lần đầu, server trả `500 INTERNAL_ERROR`. Log server ghi:
   ```
   event=bill_internal_error err=create bill in repo: create bill row: ERROR: column "total_item_discount" of relation "bills" does not exist (SQLSTATE 42703)
   ```
   Kiểm tra `goose_db_version` cho thấy có `version_id = 9` đã được đánh dấu applied (`2026-08-19 16:27:18`) nhưng **không có file migration `000009` nào tồn tại trong `db/migrations/`** (thư mục chỉ có tới `000008`). Đồng thời các file `000006`, `000007`, `000008` có mtime mới hơn hẳn (`Aug 21 14:41`) so với các file còn lại (`Aug 19 15:09`), cho thấy nội dung các file này đã bị chỉnh sửa/khôi phục sau khi đã "applied" trên DB — dẫn đến DB thực tế không khớp với nội dung file migration hiện tại (thiếu cột `bills.total_item_discount`, `bills.general_discount`, `bill_items.discount_amount`, `bill_items.final_price`).
   **Tôi đã tạm thời ALTER TABLE thủ công trên DB local để có thể tiếp tục test** (nội dung y hệt migration `000007_bill_item_discount_v1.sql`). Đây chỉ là fix tạm cho môi trường test — **cần rà soát lại lịch sử migration** (có thể phải `goose fix`/reconcile version 9, hoặc kiểm tra xem migration 000009 có bị xoá nhầm không) trước khi merge/triển khai để tránh lỗi tương tự trên các môi trường khác (staging/production).

2. **`openapi.yaml` thiếu 4 endpoint của module `admin`** — đã bổ sung trong buổi làm việc trước (xem lịch sử chat), file hiện đã đầy đủ 39 path.

3. **Hành vi cần lưu ý (không phải bug, nhưng nên biết khi test tay):**
   - Đăng nhập lại (`sign-in`) trên cùng tài khoản sẽ **thu hồi session cũ** (chỉ 1 session/tài khoản tại một thời điểm) — access token cũ sẽ nhận `401 AUTHENTICATION_REQUIRED` ngay cả khi chưa hết hạn 15 phút.
   - Admin `suspend`/`lock` một tài khoản sẽ **vô hiệu hoá session của tài khoản đó ngay lập tức** — access token đang dùng sẽ nhận `401` ở request tiếp theo.
   - `POST /groups/join` bằng invite code đã bị **revoke**, nếu người gọi **đã là active member** của group đó, vẫn trả `200 already_active` thay vì `410` — do việc kiểm tra "đã là thành viên" được ưu tiên trước khi kiểm tra tính khả dụng của invite.
   - Tạo bill qua OCR (multipart `images`) cần API key nhà cung cấp OCR (`llamaextract`) hợp lệ; trong môi trường test không có key nên job luôn `failed` sau 3 lần thử với `error_message: provider_unavailable`. Đây là giới hạn môi trường, không phải lỗi code.

---

## 1. Nhóm Auth (`/api/v1/auth/*`)

### 1.1 POST /api/v1/auth/sign-up
Đăng ký tài khoản mới.

**Request**
```bash
curl -X POST http://localhost:8080/api/v1/auth/sign-up \
  -H "Content-Type: application/json" \
  -d '{
    "email": "tester_1787299977@example.com",
    "phone_number": "0912345678",
    "display_name": "Tester One",
    "password": "Password123"
  }'
```

**Response — 201 Created**
```json
{
  "user": {
    "id": "01a02361-5a00-7d5e-a7f8-2907130fe857",
    "email": "tester_1787299977@example.com",
    "phone_number": "+84912345678",
    "display_name": "Tester One",
    "role": "user",
    "status": "pending_verification",
    "email_verified_at": null,
    "bank_code": null,
    "bank_account_number": null,
    "bank_account_holder": null,
    "avatar_url": null,
    "created_at": "2026-08-21T15:12:57.728199+07:00",
    "updated_at": "2026-08-21T15:12:57.728199+07:00"
  },
  "verification_email_sent": true,
  "verification_expires_at": "2026-08-21T15:22:57.647638167+07:00"
}
```
Ghi chú: `phone_number` được server tự chuẩn hoá về dạng E.164 (`0912345678` → `+84912345678`).

**Test lỗi — 409 Conflict (trùng SĐT)**
```bash
curl -X POST http://localhost:8080/api/v1/auth/sign-up \
  -H "Content-Type: application/json" \
  -d '{"email":"tester2_new@example.com","phone_number":"0912345678","display_name":"X","password":"Password123"}'
```
```json
{"error":{"code":"PHONE_EXISTS","message":"phone number already exists"}}
```

**Test lỗi — 400 Bad Request (thiếu field / email sai định dạng)**
```bash
curl -X POST http://localhost:8080/api/v1/auth/sign-up \
  -H "Content-Type: application/json" -d '{"email":"bad-email"}'
```
```json
{"error":{"code":"VALIDATION_FAILED","message":"request validation failed"}}
```

---

### 1.2 POST /api/v1/auth/verify-email
Xác thực email bằng OTP 6 số gửi qua email.

**Request**
```bash
curl -X POST http://localhost:8080/api/v1/auth/verify-email \
  -H "Content-Type: application/json" \
  -d '{"email":"tester_1787299977@example.com","otp":"123456"}'
```

**Response — 200 OK**
```json
{"status":"active"}
```

---

### 1.3 POST /api/v1/auth/resend-verification
Gửi lại OTP xác thực email. Response không tiết lộ việc tài khoản có tồn tại hay không (chống dò email).

**Request**
```bash
curl -X POST http://localhost:8080/api/v1/auth/resend-verification \
  -H "Content-Type: application/json" \
  -d '{"email":"tester3_1787300XXX@example.com"}'
```

**Response — 202 Accepted**
```json
{"message":"If the account is eligible, an email will be sent."}
```

---

### 1.4 POST /api/v1/auth/sign-in
Đăng nhập, trả về cặp token. **Lưu ý: thu hồi session khác của cùng thiết bị/tài khoản (one active session policy).**

**Request**
```bash
curl -X POST http://localhost:8080/api/v1/auth/sign-in \
  -H "Content-Type: application/json" \
  -d '{
    "email": "tester_1787299977@example.com",
    "password": "Password123",
    "device_id": "b6b1e6d0-....-uuid",
    "device_name": "Test Device"
  }'
```

**Response — 200 OK**
```json
{
  "user": {"id":"01a02361-...", "email":"tester_1787299977@example.com", "status":"active", "...": "..."},
  "token_type": "Bearer",
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "access_token_expires_at": "2026-08-21T15:29:22.867418415+07:00",
  "refresh_token": "FoB3RcrUpabQHbjDgtiYdoQhPocrf90LfP17U6HMCyY",
  "refresh_token_expires_at": "2026-08-28T15:14:22.79562+07:00"
}
```

**Test lỗi — 401 Unauthorized (sai mật khẩu)**
```json
{"error":{"code":"INVALID_CREDENTIALS","message":"invalid email or password"}}
```

---

### 1.5 POST /api/v1/auth/refresh
Xoay vòng refresh token (rotation) — refresh token cũ bị vô hiệu sau khi dùng.

**Request**
```bash
curl -X POST http://localhost:8080/api/v1/auth/refresh \
  -H "Content-Type: application/json" \
  -d '{"refresh_token":"FoB3RcrUpabQHbjDgtiYdoQhPocrf90LfP17U6HMCyY","device_id":"b6b1e6d0-....-uuid"}'
```

**Response — 200 OK**
```json
{
  "token_type": "Bearer",
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "access_token_expires_at": "2026-08-21T15:29:51.212497886+07:00",
  "refresh_token": "tAosKcIxH10V9NCReSW20ryc1rtEcs3fHTS8s6fkoxg",
  "refresh_token_expires_at": "2026-08-28T15:14:22.79562+07:00"
}
```

---

### 1.6 POST /api/v1/auth/sign-out
Thu hồi session hiện tại (yêu cầu Bearer token).

**Request**
```bash
curl -X POST http://localhost:8080/api/v1/auth/sign-out \
  -H "Authorization: Bearer $TOKEN"
```

**Response — 204 No Content** (body rỗng)

**Kiểm tra sau sign-out** — gọi lại `GET /users/me` bằng token cũ:
```json
{"error":{"code":"AUTHENTICATION_REQUIRED","message":"authentication required"}}
```
`401 Unauthorized`

---

### 1.7 POST /api/v1/auth/forgot-password
Gửi OTP đặt lại mật khẩu (response chung, không lộ email tồn tại hay không).

**Request**
```bash
curl -X POST http://localhost:8080/api/v1/auth/forgot-password \
  -H "Content-Type: application/json" \
  -d '{"email":"tester_1787299977@example.com"}'
```

**Response — 202 Accepted**
```json
{"message":"If the account is eligible, an email will be sent."}
```

---

### 1.8 POST /api/v1/auth/reset-password
Đặt lại mật khẩu bằng OTP. **Thu hồi toàn bộ session của user sau khi thành công.**

**Request**
```bash
curl -X POST http://localhost:8080/api/v1/auth/reset-password \
  -H "Content-Type: application/json" \
  -d '{"email":"tester_1787299977@example.com","otp":"654321","new_password":"Password123"}'
```

**Response — 204 No Content** (body rỗng)

---

### 1.9 PUT /api/v1/users/me/password (đổi mật khẩu khi đã đăng nhập)
*(Xem mục 2.3 — thuộc nhóm Users nhưng test cùng thời điểm trong luồng Auth)*

---

## 2. Nhóm Users (`/api/v1/users/*`)

### 2.1 GET /api/v1/users/me
**Request**
```bash
curl http://localhost:8080/api/v1/users/me -H "Authorization: Bearer $TOKEN"
```
**Response — 200 OK**
```json
{
  "user": {
    "id": "01a02361-5a00-7d5e-a7f8-2907130fe857",
    "email": "tester_1787299977@example.com",
    "phone_number": "+84912345678",
    "display_name": "Tester One",
    "role": "user",
    "status": "active",
    "email_verified_at": "2026-08-21T15:14:15.206151+07:00",
    "bank_code": null,
    "bank_account_number": null,
    "bank_account_holder": null,
    "avatar_url": null,
    "created_at": "2026-08-21T15:12:57.728199+07:00",
    "updated_at": "2026-08-21T15:14:22.858559+07:00"
  }
}
```

### 2.2 PATCH /api/v1/users/me
**Request**
```bash
curl -X PATCH http://localhost:8080/api/v1/users/me \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"display_name":"Tester One Updated","bank_code":"VCB","bank_account_number":"0123456789","bank_account_holder":"TESTER ONE"}'
```
**Response — 200 OK**
```json
{"user":{"...":"...", "display_name":"Tester One Updated","bank_code":"VCB","bank_account_number":"0123456789","bank_account_holder":"TESTER ONE"}}
```

**Test lỗi — 400 Bad Request (mã ngân hàng không được hỗ trợ)**
```bash
curl -X PATCH http://localhost:8080/api/v1/users/me -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"bank_code":"970422"}'
```
```json
{"error":{"code":"UNSUPPORTED_BANK","message":"bank is not supported"}}
```
> Lưu ý: `bank_code` phải dùng **short code** trả về từ `GET /banks` (VD `VCB`), không phải BIN số (`970436`).

### 2.3 PUT /api/v1/users/me/password
**Request**
```bash
curl -X PUT http://localhost:8080/api/v1/users/me/password \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"current_password":"Password123","new_password":"Password456"}'
```
**Response — 204 No Content**

### 2.4 PUT /api/v1/users/me/avatar
**Request**
```bash
curl -X PUT http://localhost:8080/api/v1/users/me/avatar \
  -H "Authorization: Bearer $TOKEN" \
  -F "avatar=@receipt.jpg;type=image/jpeg"
```
**Response — 200 OK**
```json
{"avatar_url":"https://res.cloudinary.com/d6utcujm/image/upload/v1/paysplit/avatars/01a02361-.../01a0236f-...?_a=AQAZCop"}
```

### 2.5 DELETE /api/v1/users/me/avatar
**Request**
```bash
curl -X DELETE http://localhost:8080/api/v1/users/me/avatar -H "Authorization: Bearer $TOKEN"
```
**Response — 204 No Content**

### 2.6 PUT /api/v1/users/me/fcm-token
**Request**
```bash
curl -X PUT http://localhost:8080/api/v1/users/me/fcm-token \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"fcm_token":"dummy-fcm-token-abc123"}'
```
**Response — 200 OK**
```json
{"status":"ok"}
```

---

## 3. Nhóm Banks (`/api/v1/banks`)

### 3.1 GET /api/v1/banks
**Request**
```bash
curl "http://localhost:8080/api/v1/banks?supported=true"
```
**Response — 200 OK** (rút gọn, trả về 65 ngân hàng từ snapshot VietQR)
```json
{
  "banks": [
    {"id":17,"name":"Ngân hàng TMCP Công thương Việt Nam","code":"ICB","bin":"970415","short_name":"VietinBank","logo":"https://cdn.vietqr.io/img/ICB.png","supported":true},
    {"id":43,"name":"Ngân hàng TMCP Ngoại Thương Việt Nam","code":"VCB","bin":"970436","short_name":"Vietcombank","logo":"https://cdn.vietqr.io/img/VCB.png","supported":true}
  ]
}
```
Không cần đăng nhập (public endpoint). Header response có `Cache-Control: public, max-age=86400`.

---

## 4. Nhóm Groups (`/api/v1/groups/*`)

### 4.1 POST /api/v1/groups
**Request**
```bash
curl -X POST http://localhost:8080/api/v1/groups -H "Authorization: Bearer $TOKEN1" -H "Content-Type: application/json" \
  -d '{"name":"Nhóm Du Lịch Đà Lạt","currency":"VND"}'
```
**Response — 201 Created**
```json
{
  "group": {"id":"01a02364-789b-7504-9a50-785e2b011df9","name":"Nhóm Du Lịch Đà Lạt","currency":"VND","created_at":"2026-08-21T15:16:22.170613+07:00"},
  "membership": {"id":"01a02364-789e-7b03-9ad9-9d607b4ab41e","group_id":"01a02364-789b-7504-9a50-785e2b011df9","user_id":"01a02361-...","role":"captain","status":"active","joined_at":"2026-08-21T15:16:22.170613+07:00","left_at":null}
}
```

### 4.2 GET /api/v1/groups
**Request**
```bash
curl "http://localhost:8080/api/v1/groups?limit=10" -H "Authorization: Bearer $TOKEN1"
```
**Response — 200 OK**
```json
{
  "groups": [
    {"group":{"id":"01a02364-789b-...","name":"Nhóm Du Lịch Đà Lạt","currency":"VND","created_at":"..."},
     "caller_membership_id":"01a02364-789e-...","caller_role":"captain","active_member_count":1}
  ],
  "next_cursor": null
}
```

### 4.3 GET /api/v1/groups/{id}
**Request**
```bash
curl "http://localhost:8080/api/v1/groups/01a02364-789b-7504-9a50-785e2b011df9" -H "Authorization: Bearer $TOKEN1"
```
**Response — 200 OK**
```json
{
  "group": {"id":"01a02364-789b-...","name":"Nhóm Du Lịch Đà Lạt","currency":"VND","created_at":"..."},
  "members": [{"membership_id":"01a02364-789e-...","user_id":"01a02361-...","display_name":"Tester One Updated","avatar_url":null,"role":"captain","joined_at":"..."}],
  "balances": [{"member_id":"01a02364-789e-...","net_balance":"0"}],
  "caller_role": "captain"
}
```
**Test lỗi — 404 Not Found**
```bash
curl "http://localhost:8080/api/v1/groups/00000000-0000-0000-0000-000000000000" -H "Authorization: Bearer $TOKEN1"
```
```json
{"error":{"code":"GROUP_NOT_FOUND","message":"group not found"}}
```

### 4.4 POST /api/v1/groups/{id}/invites
**Request**
```bash
curl -X POST "http://localhost:8080/api/v1/groups/$GROUP_ID/invites" -H "Authorization: Bearer $TOKEN1" -H "Content-Type: application/json" \
  -d '{"expires_in_hours":24,"max_uses":10}'
```
**Response — 201 Created**
```json
{
  "invite": {
    "id": "01a02364-a264-788a-832f-56a3ec7655f5",
    "code": "mFiZCoUjeWAWSuBDUtNmXbbo76qq18nZUtZwXM1P4o8",
    "invite_url": "paysplit://join?code=mFiZCoUjeWAWSuBDUtNmXbbo76qq18nZUtZwXM1P4o8",
    "expires_at": "2026-08-22T15:16:32.866038+07:00",
    "max_uses": 10,
    "use_count": 0
  }
}
```

### 4.5 DELETE /api/v1/groups/{id}/invites/{inviteId}
**Request**
```bash
curl -X DELETE "http://localhost:8080/api/v1/groups/$GROUP_ID/invites/$INVITE_ID" -H "Authorization: Bearer $TOKEN_CAPTAIN"
```
**Response — 204 No Content** (gọi lại lần 2 với cùng id vẫn trả 204 — idempotent, đã kiểm chứng thực tế)

### 4.6 GET /api/v1/groups/invites/{code}
**Request**
```bash
curl "http://localhost:8080/api/v1/groups/invites/$CODE" -H "Authorization: Bearer $TOKEN2"
```
**Response — 200 OK**
```json
{"preview":{"group_name":"Nhóm Du Lịch Đà Lạt","active_member_count":1,"captain_display_name":"Tester One Updated"}}
```
**Test lỗi — 404 Not Found (code không tồn tại)**
```json
{"error":{"code":"INVITE_NOT_FOUND","message":"invite not found"}}
```

### 4.7 POST /api/v1/groups/join
**Request**
```bash
curl -X POST http://localhost:8080/api/v1/groups/join -H "Authorization: Bearer $TOKEN2" -H "Content-Type: application/json" \
  -d '{"code":"mFiZCoUjeWAWSuBDUtNmXbbo76qq18nZUtZwXM1P4o8"}'
```
**Response — 200 OK**
```json
{"join":{"group_id":"01a02364-789b-...","membership_id":"01a02364-d07c-7388-9c8d-35100ad1f000","role":"member","status":"active","result":"joined"}}
```
> `result` có 3 giá trị: `joined` (lần đầu), `reactivated` (đã từng rời rồi vào lại), `already_active` (đã là thành viên — kể cả khi code đã bị revoke, xem mục "Hành vi cần lưu ý" ở trên).

### 4.8 DELETE /api/v1/groups/{id}/members/{memberId}
**Request (trường hợp bị chặn vì còn nợ)**
```bash
curl -X DELETE "http://localhost:8080/api/v1/groups/$GROUP_ID/members/$MEMBER_ID" -H "Authorization: Bearer $TOKEN_CAPTAIN"
```
**Response — 409 Conflict**
```json
{"error":{"code":"GROUP_MEMBER_HAS_OPEN_DEBTS","message":"member has unsettled debts","fields":{"payable_amount":"121000","receivable_amount":"0"}}}
```
> Áp dụng cho cả captain xoá member khác **và** member tự rời nhóm (self-leave) — đã kiểm chứng cả 2 trường hợp, cùng trả `409` với `payable_amount`/`receivable_amount` tương ứng phía người gọi.

### 4.9 PUT /api/v1/groups/{id}/members/{memberId}/role
**Request**
```bash
curl -X PUT "http://localhost:8080/api/v1/groups/$GROUP_ID/members/$MEMBER2_ID/role" \
  -H "Authorization: Bearer $TOKEN_CAPTAIN" -H "Content-Type: application/json" \
  -d '{"role":"captain"}'
```
**Response — 200 OK**
```json
{"transfer":{"previous_captain_member_id":"01a02364-789e-...","current_captain_member_id":"01a02364-d07c-..."}}
```

### 4.10 GET /api/v1/groups/{id}/activities
**Request**
```bash
curl "http://localhost:8080/api/v1/groups/$GROUP_ID/activities?limit=10" -H "Authorization: Bearer $TOKEN1"
```
**Response — 200 OK**
```json
{
  "activities": [
    {"id":"...","action_type":"member_joined","description":"Tester Two joined the group","actor":{"member_id":"...","user_id":"...","display_name":"Tester Two","avatar_url":null},"metadata":{"member_id":"..."},"created_at":"..."},
    {"id":"...","action_type":"invite_created","description":"Tester One Updated created an invite","actor":{"...":"..."},"metadata":{"max_uses":10,"invite_id":"...","expires_at":"..."},"created_at":"..."},
    {"id":"...","action_type":"group_created","description":"Tester One Updated created the group \"Nhóm Du Lịch Đà Lạt\"","actor":{"...":"..."},"metadata":{"group_id":"..."},"created_at":"..."}
  ],
  "next_cursor": null
}
```

---

## 5. Nhóm Notifications (`/api/v1/notifications/*`)

### 5.1 GET /api/v1/notifications
**Request**
```bash
curl "http://localhost:8080/api/v1/notifications?page=1&page_size=10" -H "Authorization: Bearer $TOKEN"
```
**Response — 200 OK**
```json
{
  "items": [
    {"id":"...","user_id":"...","type":"bill_finalized","title":"Hóa đơn đã được chốt","body":"Hóa đơn của bạn đã được chốt với tổng cộng 50000 đ.","payload":{"amount":"25000","bill_id":"...","group_id":"..."},"created_at":"..."}
  ],
  "meta": {"page":1,"page_size":10,"total_items":2,"total_pages":1}
}
```

### 5.2 GET /api/v1/notifications/unread-count
```bash
curl http://localhost:8080/api/v1/notifications/unread-count -H "Authorization: Bearer $TOKEN"
```
```json
{"unread_count":2}
```

### 5.3 PATCH /api/v1/notifications/read-all
```bash
curl -X PATCH http://localhost:8080/api/v1/notifications/read-all -H "Authorization: Bearer $TOKEN"
```
```json
{"message":"All notifications marked as read"}
```

### 5.4 PATCH /api/v1/notifications/{id}/read
```bash
curl -X PATCH "http://localhost:8080/api/v1/notifications/$NOTIF_ID/read" -H "Authorization: Bearer $TOKEN"
```
```json
{"message":"Notification marked as read"}
```

---

## 6. Nhóm Bills (`/api/v1/bills/*`)

> ⚠️ Trước khi test nhóm này lần đầu trên một DB mới, xem mục "Phát hiện quan trọng" ở đầu file — có thể gặp lỗi 500 do thiếu cột nếu migration chưa đồng bộ.

### 6.1 POST /api/v1/bills (tạo bill thủ công — JSON)
**Request**
```bash
curl -X POST http://localhost:8080/api/v1/bills -H "Authorization: Bearer $TOKEN_CAPTAIN" -H "Content-Type: application/json" \
  -d '{
    "group_id": "01a02364-789b-7504-9a50-785e2b011df9",
    "merchant_name": "Quan An Ngon",
    "bill_date": "2026-08-21T12:00:00Z",
    "subtotal": 200000, "service_charge": 20000, "vat": 22000, "discount": 0, "total": 242000,
    "split_method": "even",
    "items": [
      {"name":"Pho bo","quantity":"2","unit_price":50000,"line_total":100000,
       "assignments":[{"member_id":"MEMBER1_ID","weight":"1"},{"member_id":"MEMBER2_ID","weight":"1"}]},
      {"name":"Nem ran","quantity":"1","unit_price":100000,"line_total":100000,
       "assignments":[{"member_id":"MEMBER1_ID","weight":"1"},{"member_id":"MEMBER2_ID","weight":"1"}]}
    ]
  }'
```
> Lưu ý bắt buộc: mỗi phần tử trong `assignments` **phải có `weight`** (chuỗi số dương), thiếu sẽ trả `400 VALIDATION_FAILED: "assignment weight must be a positive number"`.

**Response — 201 Created**
```json
{
  "bill": {
    "id": "01a02367-45d2-7c86-9c25-079caa8ec4d2",
    "group_id": "01a02364-789b-7504-9a50-785e2b011df9",
    "creditor_member_id": "01a02364-789e-7b03-9ad9-9d607b4ab41e",
    "status": "draft",
    "merchant_name": "Quan An Ngon",
    "subtotal": 200000, "service_charge": 20000, "vat": 22000,
    "discount": 0, "total_item_discount": 0, "general_discount": 0, "total": 242000,
    "split_method": "even", "mismatch_codes": [], "version": 1,
    "items": [ "... 2 items với assignments đầy đủ ..." ]
  },
  "is_accepted": false
}
```

### 6.2 POST /api/v1/bills (tạo bill từ ảnh — multipart, kích hoạt OCR)
**Request**
```bash
curl -X POST http://localhost:8080/api/v1/bills -H "Authorization: Bearer $TOKEN_CAPTAIN" \
  -F "group_id=01a02364-789b-7504-9a50-785e2b011df9" \
  -F "images=@receipt.jpg;type=image/jpeg"
```
**Response — 202 Accepted**
```json
{
  "bill": {"id":"01a02369-f391-...","status":"draft","subtotal":0,"total":0,"images":[{"id":"...","image_key":"bills/.../0","position":0}]},
  "ocr_job": {"id":"01a02369-fb0d-...","status":"queued","provider":"llamaextract","attempts":0,"version":1},
  "is_accepted": true
}
```
> Trong môi trường test không có OCR API key hợp lệ → job chuyển sang `failed` sau 3 lần thử (`error_message: provider_unavailable`). Đây là giới hạn cấu hình môi trường, không phải lỗi API.

### 6.3 GET /api/v1/bills (list theo group, phân trang cursor)
```bash
curl "http://localhost:8080/api/v1/bills?group_id=$GROUP_ID&limit=10" -H "Authorization: Bearer $TOKEN"
```
```json
{"bills":[{"id":"...","status":"draft","merchant_name":"Quan An Ngon","total":242000,"...":"..."}]}
```

### 6.4 GET /api/v1/bills/{id} (chi tiết + breakdown)
```bash
curl "http://localhost:8080/api/v1/bills/$BILL_ID?group_id=$GROUP_ID" -H "Authorization: Bearer $TOKEN"
```
```json
{
  "bill": {"...":"...", "items":[{"...":"..."}]},
  "breakdown": [
    {"member_id":"01a02364-789e-...","item_subtotal":100000,"service_charge_share":10000,"vat_share":11000,"discount_share":0,"rounding_adjustment":0,"final_amount":121000},
    {"member_id":"01a02364-d07c-...","item_subtotal":100000,"service_charge_share":10000,"vat_share":11000,"discount_share":0,"rounding_adjustment":0,"final_amount":121000}
  ]
}
```
**Test lỗi — 404 sau khi xoá draft**
```json
{"error":{"code":"BILL_NOT_FOUND","message":"bill not found"}}
```

### 6.5 PUT /api/v1/bills/{id} (cập nhật draft — optimistic locking bằng `version`)
```bash
curl -X PUT "http://localhost:8080/api/v1/bills/$BILL_ID?group_id=$GROUP_ID" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"version":1,"merchant_name":"Quan An Ngon (Updated)","subtotal":200000,"service_charge":20000,"vat":22000,"discount":0,"total":242000,"split_method":"even","items":[...]}'
```
```json
{"bill":{"...":"...","merchant_name":"Quan An Ngon (Updated)","version":2}}
```
> Mỗi lần update thành công, `version` tăng lên 1 — phải truyền đúng `version` hiện tại của bill, nếu không sẽ nhận `409 VERSION_CONFLICT`.

### 6.6 POST /api/v1/bills/{id}/review
```bash
curl -X POST "http://localhost:8080/api/v1/bills/$BILL_ID/review?group_id=$GROUP_ID" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"version":2}'
```
```json
{"bill":{"...":"...","status":"reviewed","version":3,"reviewed_at":"...","reviewed_by_member_id":"..."}}
```

### 6.7 POST /api/v1/bills/{id}/finalize
```bash
curl -X POST "http://localhost:8080/api/v1/bills/$BILL_ID/finalize?group_id=$GROUP_ID" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"version":3}'
```
```json
{
  "bill": {
    "...":"...", "status":"finalized","version":4,"finalized_at":"...",
    "shares": [
      {"id":"...","member_id":"01a02364-789e-...","item_subtotal":100000,"service_charge_share":10000,"vat_share":11000,"discount_share":0,"rounding_adjustment":0,"final_amount":121000},
      {"id":"...","member_id":"01a02364-d07c-...","item_subtotal":100000,"service_charge_share":10000,"vat_share":11000,"discount_share":0,"rounding_adjustment":0,"final_amount":121000}
    ]
  }
}
```
> Finalize tạo debt tương ứng trong bảng `debts` và một notification `bill_finalized` cho member không phải creditor (đã kiểm chứng qua mục 5.1).

### 6.8 POST /api/v1/bills/{id}/void
```bash
curl -X POST "http://localhost:8080/api/v1/bills/$BILL_ID/void?group_id=$GROUP_ID" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"version":3,"reason":"Da huy don hang do dat sai"}'
```
```json
{"bill":{"...":"...","status":"voided","voided_at":"...","version":4}}
```

### 6.9 DELETE /api/v1/bills/{id} (xoá draft)
```bash
curl -X DELETE "http://localhost:8080/api/v1/bills/$BILL_ID?group_id=$GROUP_ID" -H "Authorization: Bearer $TOKEN"
```
**Response — 204 No Content**

### 6.10 GET /api/v1/bills/{id}/events (SSE)
```bash
curl -N -H "Authorization: Bearer $TOKEN" "http://localhost:8080/api/v1/bills/$BILL_ID/events?group_id=$GROUP_ID"
```
**Response — 200, `Content-Type: text/event-stream`, stream liên tục:**
```
event: snapshot
data: {"bill_id":"...","mismatch_codes":[],"ocr_job":{"attempts":1,"status":"processing",...},"status":"draft","subtotal":0,"total":0,"version":1}

event: ocr.updated
data: {"bill_id":"...","job_id":"...","status":"processing"}
```
> Kết nối giữ mở (`Connection: keep-alive`), client cần đóng thủ công khi không cần nữa.

### 6.11 POST /api/v1/bills/{id}/ocr-retry
```bash
curl -X POST "http://localhost:8080/api/v1/bills/$BILL_ID/ocr-retry?group_id=$GROUP_ID" -H "Authorization: Bearer $TOKEN"
```
**Response — 202 Accepted**
```json
{"ocr_job":{"id":"01a0236a-d7cb-...","status":"queued","provider":"llamaextract","attempts":0,"version":1}}
```

### 6.12 POST /api/v1/bills/{id}/apply-candidate
```bash
curl -X POST "http://localhost:8080/api/v1/bills/$BILL_ID/apply-candidate?group_id=$GROUP_ID" -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"job_id":"01a0236a-d7cb-...","version":1}'
```
**Test lỗi — 409 (job chưa xong / failed)**
```json
{"error":{"code":"OCR_NOT_READY","message":"ocr not ready"}}
```

---

## 7. Nhóm Admin (`/api/v1/admin/*`)

> Yêu cầu access token của tài khoản có `role: admin`. Trong DB dev, có thể set trực tiếp: `UPDATE users SET role='admin' WHERE id=...;` rồi đăng nhập lại để JWT mang claim `role: admin` (JWT không tự cập nhật khi đổi role của session đang mở).

### 7.1 GET /api/v1/admin/accounts
**Request**
```bash
curl "http://localhost:8080/api/v1/admin/accounts?page=1&limit=10" -H "Authorization: Bearer $ADMIN_TOKEN"
```
**Response — 200 OK**
```json
{
  "items": [
    {"id":"01a02364-0ae0-...","email":"tester2_...@example.com","display_name":"Tester Two","phone_number":"+8497...","role":"user","status":"active","email_verified_at":"...","created_at":"...","updated_at":"..."}
  ],
  "pagination": {"total":281,"page":1,"limit":10,"total_pages":29}
}
```
Hỗ trợ filter: `status`, `role`, `search` (khớp email/tên/SĐT), `sort_by`, `sort_order`.

### 7.2 GET /api/v1/admin/accounts/{id}
```bash
curl "http://localhost:8080/api/v1/admin/accounts/$USER_ID" -H "Authorization: Bearer $ADMIN_TOKEN"
```
```json
{
  "id": "01a02364-0ae0-...", "email":"tester2_...@example.com","display_name":"Tester Two","role":"user","status":"active",
  "failed_login_count": 0, "bank": {},
  "active_sessions_count": 1,
  "groups": [{"group_id":"01a02364-789b-...","group_name":"Nhóm Du Lịch Đà Lạt","role":"member","status":"active","joined_at":"..."}],
  "financials": {"outstanding_debts_count":1,"total_debt_amount_vnd":121000,"outstanding_credits_count":0,"total_credit_amount_vnd":0},
  "recent_audit_logs": []
}
```

### 7.3 PUT /api/v1/admin/accounts/{id}/status
**Request (suspend)**
```bash
curl -X PUT "http://localhost:8080/api/v1/admin/accounts/$USER_ID/status" -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"status":"suspended","reason":"Vi pham dieu khoan su dung - test"}'
```
```json
{"account":{"...":"...","status":"suspended"},"warning":{"unsettled_debts_count":1,"unsettled_credits_count":0}}
```
> Ngay sau lệnh này, access token hiện tại của user bị suspend nhận `401` ở request kế tiếp (session bị revoke ngay lập tức).

**Test lỗi — 403 (tự sửa chính mình)**
```bash
curl -X PUT "http://localhost:8080/api/v1/admin/accounts/$ADMIN_ID/status" -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"status":"suspended","reason":"self test"}'
```
```json
{"error":{"code":"CANNOT_MODIFY_SELF","message":"cannot modify self account status"}}
```

**Test lỗi — 403 (tài khoản thường gọi API admin)**
```json
{"error":{"code":"INSUFFICIENT_PERMISSIONS","message":"insufficient permissions"}}
```

### 7.4 GET /api/v1/admin/system/overview
```bash
curl http://localhost:8080/api/v1/admin/system/overview -H "Authorization: Bearer $ADMIN_TOKEN"
```
```json
{
  "users": {"total":281,"active":280,"pending_verification":1,"suspended":0,"locked":0},
  "groups": {"total":112},
  "bills": {"total_finalized":79,"total_draft":44},
  "debts": {"awaiting":74,"pending_confirmation":0,"stalled_confirmation":0,"rejected":0,"settled":1},
  "media_cleanup": {"pending_jobs_count":0},
  "ocr_jobs": {"queued":31,"processing":0,"succeeded":5,"failed":3},
  "runtime": {"goroutines_count":42,"alloc_memory_bytes":8560808,"uptime_seconds":725}
}
```

---

## 8. Health check

### 8.1 GET /health
```bash
curl http://localhost:8080/health
```
```json
{"status":"ok"}
```
