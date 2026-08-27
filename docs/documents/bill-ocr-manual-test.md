# Hướng dẫn test tay module Bill và OCR

Toàn bộ lệnh dưới đây tôi đã chạy thật trên máy này ngày 2026 08 20 và chúng đều hoạt động. Bạn chạy tuần tự từ trên xuống là được.

Bạn có thể bỏ qua bất kỳ mục nào không quan tâm. Các mục 6 tới 9 là phần vừa sửa, đáng chạy nhất.

---

## 0. Chuẩn bị

```bash
cd PaySplit-BE
docker compose up -d postgres          # PostgreSQL 18 ở cổng 5433
cp .env.example .env                   # nếu chưa có, rồi điền key
make migrate-up
```

Chạy server. Đừng dùng `make run` nếu bạn muốn thấy log ở một cửa sổ riêng:

```bash
set -a; . ./.env; set +a
go run ./cmd/api
```

Mở một cửa sổ terminal khác cho phần còn lại.

> **Lưu ý khi cần tắt server.** `go run` biên dịch ra một binary tên `api`, nên `pkill -f 'cmd/api'` **không** giết được nó. Dùng `ss -ltnp | grep 8080` để tìm đúng pid. Tôi đã mất một lượt verify vì chuyện này, tưởng đã khởi động lại nhưng server cũ vẫn chạy.

Kiểm tra server sống:

```bash
curl -s http://localhost:8080/health
```

---

## 1. Danh sách endpoint

### Công khai, không cần token

| Method | Endpoint | Mô tả |
|---|---|---|
| `GET` | `/health` | Kiểm tra server sống |
| `POST` | `/api/v1/auth/sign-up` | Đăng ký, tài khoản ở trạng thái `pending_verification` |
| `POST` | `/api/v1/auth/verify-email` | Xác thực email bằng mã 6 số |
| `POST` | `/api/v1/auth/resend-verification` | Gửi lại mã xác thực |
| `POST` | `/api/v1/auth/sign-in` | Đăng nhập, cần `device_id` |
| `POST` | `/api/v1/auth/refresh` | Xoay vòng refresh token |
| `POST` | `/api/v1/auth/forgot-password` | Gửi mã đặt lại mật khẩu |
| `POST` | `/api/v1/auth/reset-password` | Đặt lại mật khẩu |
| `GET` | `/api/v1/banks` | Danh bạ ngân hàng VietQR |

### Tài khoản và nhóm, cần `Authorization: Bearer <token>`

| Method | Endpoint | Mô tả |
|---|---|---|
| `POST` | `/api/v1/auth/sign-out` | Đăng xuất, thu hồi phiên |
| `GET` `PATCH` | `/api/v1/users/me` | Xem và sửa hồ sơ, gồm tài khoản ngân hàng |
| `PUT` | `/api/v1/users/me/password` | Đổi mật khẩu |
| `PUT` `DELETE` | `/api/v1/users/me/avatar` | Ảnh đại diện |
| `PUT` | `/api/v1/users/me/fcm-token` | Đăng ký token push |
| `POST` `GET` | `/api/v1/groups` | Tạo và liệt kê nhóm |
| `GET` | `/api/v1/groups/{id}` | Chi tiết nhóm |
| `POST` | `/api/v1/groups/{id}/invites` | Tạo link mời |
| `DELETE` | `/api/v1/groups/{id}/invites/{inviteId}` | Thu hồi link mời |
| `GET` | `/api/v1/groups/invites/{code}` | Xem trước lời mời |
| `POST` | `/api/v1/groups/join` | Vào nhóm bằng mã mời |
| `DELETE` | `/api/v1/groups/{id}/members/{memberId}` | Rời nhóm hoặc xóa thành viên |
| `PUT` | `/api/v1/groups/{id}/members/{memberId}/role` | Chuyển quyền Captain |
| `GET` | `/api/v1/groups/{id}/activities` | Nhật ký hoạt động nhóm |
| `GET` `PATCH` | `/api/v1/notifications...` | Thông báo trong ứng dụng |

### Hóa đơn và OCR, cần token và `group_id`

| Method | Endpoint | Ai được gọi | Mô tả |
|---|---|---|---|
| `POST` | `/api/v1/bills` | Thành viên | Tạo hóa đơn thủ công (`201`) hoặc kèm 1 tới 5 ảnh (`202`) |
| `GET` | `/api/v1/bills?group_id=` | Thành viên | Liệt kê theo cursor |
| `GET` | `/api/v1/bills/{id}?group_id=` | Thành viên | Chi tiết, breakdown, URL ảnh ký 5 phút |
| `PUT` `PATCH` | `/api/v1/bills/{id}?group_id=` | Creditor hoặc Captain | Thay toàn bộ bản nháp theo version |
| `DELETE` | `/api/v1/bills/{id}?group_id=` | Creditor hoặc Captain | Xóa bản nháp |
| `GET` | `/api/v1/bills/{id}/events?group_id=` | Thành viên | SSE |
| `POST` | `/api/v1/bills/{id}/ocr-retry?group_id=` | Creditor hoặc Captain | Chạy lại OCR |
| `POST` | `/api/v1/bills/{id}/apply-candidate?group_id=` | Creditor hoặc Captain | Áp dụng kết quả OCR |
| `POST` | `/api/v1/bills/{id}/review?group_id=` | Creditor hoặc Captain | Xét duyệt |
| `POST` | `/api/v1/bills/{id}/finalize?group_id=` | Captain | Chốt sổ |
| `POST` | `/api/v1/bills/{id}/void?group_id=` | Captain | Hủy hóa đơn đã chốt |

---

## 2. Tạo hai người dùng và một nhóm

Chép nguyên khối này. Nó lưu mọi biến vào `/tmp/paysplit-test.env` để các bước sau dùng lại.

```bash
API=http://localhost:8080
PW='Passw0rd!23'
E1="test$(date +%s)a@test.com"
E2="test$(date +%s)b@test.com"

curl -s -X POST $API/api/v1/auth/sign-up -H 'Content-Type: application/json' \
  -d "{\"email\":\"$E1\",\"password\":\"$PW\",\"display_name\":\"Nguoi A\",\"phone_number\":\"+8490$(date +%H%M%S)0\"}" -o /dev/null
curl -s -X POST $API/api/v1/auth/sign-up -H 'Content-Type: application/json' \
  -d "{\"email\":\"$E2\",\"password\":\"$PW\",\"display_name\":\"Nguoi B\",\"phone_number\":\"+8491$(date +%H%M%S)1\"}" -o /dev/null
```

Đăng ký xong tài khoản ở trạng thái `pending_verification`. Bạn có thể lấy mã 6 số từ email thật, hoặc kích hoạt thẳng trong database cho nhanh:

```bash
docker compose exec -T postgres psql -U postgres -d paysplit \
  -c "UPDATE users SET status='active', email_verified_at=now() WHERE email IN ('$E1','$E2');"
```

Đăng nhập. **`device_id` là bắt buộc**, thiếu nó sẽ nhận `VALIDATION_FAILED` chứ không phải lỗi rõ ràng:

```bash
signin() {
  curl -s -X POST $API/api/v1/auth/sign-in -H 'Content-Type: application/json' \
    -d "{\"email\":\"$1\",\"password\":\"$PW\",\"device_id\":\"$(python3 -c 'import uuid;print(uuid.uuid4())')\",\"device_name\":\"manual-test\"}"
}
R1=$(signin "$E1"); R2=$(signin "$E2")
T1=$(echo "$R1" | python3 -c 'import sys,json;print(json.load(sys.stdin)["access_token"])')
T2=$(echo "$R2" | python3 -c 'import sys,json;print(json.load(sys.stdin)["access_token"])')
U1=$(echo "$R1" | python3 -c 'import sys,json;print(json.load(sys.stdin)["user"]["id"])')
```

Tạo nhóm và cho người B vào:

```bash
GID=$(curl -s -X POST $API/api/v1/groups -H "Authorization: Bearer $T1" -H 'Content-Type: application/json' \
  -d '{"name":"Nhom test","currency":"VND"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["group"]["id"])')

CODE=$(curl -s -X POST "$API/api/v1/groups/$GID/invites" -H "Authorization: Bearer $T1" \
  -H 'Content-Type: application/json' -d '{}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["invite"]["code"])')

curl -s -X POST "$API/api/v1/groups/join" -H "Authorization: Bearer $T2" \
  -H 'Content-Type: application/json' -d "{\"code\":\"$CODE\"}" -o /dev/null
```

Lấy `member_id` của hai người. Chú ý đây là **member id chứ không phải user id**, và các API hóa đơn dùng member id:

```bash
M1=$(docker compose exec -T postgres psql -U postgres -d paysplit -t -A \
  -c "SELECT id FROM group_members WHERE group_id='$GID' AND role='captain';")
M2=$(docker compose exec -T postgres psql -U postgres -d paysplit -t -A \
  -c "SELECT id FROM group_members WHERE group_id='$GID' AND role='member';")

cat > /tmp/paysplit-test.env <<EOF
API=$API
PW='$PW'
E1=$E1
E2=$E2
T1=$T1
T2=$T2
U1=$U1
GID=$GID
M1=$M1
M2=$M2
EOF
echo "đã lưu vào /tmp/paysplit-test.env"
```

Từ đây, mỗi khi mở terminal mới chỉ cần `. /tmp/paysplit-test.env`.

> Access token sống 15 phút. Hết hạn thì chạy lại phần `signin` ở trên và cập nhật `T1`.

---

## 3. Tạo hóa đơn thủ công và xem phần chia tiền

Dùng tổng **lẻ** (100001) để thấy phần dư đi về đâu. Đây là điểm cốt lõi của phần vừa sửa.

```bash
. /tmp/paysplit-test.env

BID=$(curl -s -X POST $API/api/v1/bills -H "Authorization: Bearer $T1" \
  -H 'Content-Type: application/json' -H "Idempotency-Key: test-$(date +%s)" -d "{
  \"group_id\":\"$GID\",\"merchant_name\":\"Quan test\",
  \"subtotal\":100001,\"service_charge\":0,\"vat\":0,\"discount\":0,\"total\":100001,
  \"split_method\":\"item_ratio\",
  \"items\":[{\"name\":\"Lau\",\"quantity\":\"1\",\"unit_price\":100001,\"line_total\":100001,
    \"assignments\":[{\"member_id\":\"$M1\",\"weight\":\"1\"},{\"member_id\":\"$M2\",\"weight\":\"1\"}]}]
 }" | python3 -c 'import sys,json;print(json.load(sys.stdin)["bill"]["id"])')
echo "BID=$BID"
```

Xem breakdown:

```bash
curl -s "$API/api/v1/bills/$BID?group_id=$GID" -H "Authorization: Bearer $T1" | python3 -c '
import sys,json
d=json.load(sys.stdin); b=d["bill"]
print("total:",b["total"],"| creditor:",b["creditor_member_id"])
print("mismatch_codes:",b["mismatch_codes"])
s=0
for a in d.get("breakdown") or []:
    role="CREDITOR" if a["member_id"]==b["creditor_member_id"] else "thanh vien"
    print(" ",role,"final=",a["final_amount"],"adj=",a["rounding_adjustment"])
    s+=a["final_amount"]
print("TONG:",s,"| khop total?",s==b["total"])'
```

**Kết quả đúng:**

```
total: 100001 | creditor: <M1>
mismatch_codes: []
  <member UUID nhỏ hơn> final= 50001 adj= 1
  <member UUID lớn hơn> final= 50000 adj= 0
TONG: 100001 | khop total? True
```

Đây là điều cần nhìn: 100001 chia đôi có 1 đồng dư. Đồng đó đi theo phần lẻ lớn nhất, và khi phần lẻ hòa thì UUID byte tăng dần quyết định người nhận. Creditor không được ưu tiên. Tổng vẫn khớp tuyệt đối.

---

## 4. Chốt sổ và xem khoản nợ

Chốt sổ đòi Creditor phải có tài khoản ngân hàng. Cột trong database tên là `default_bank_*`:

```bash
docker compose exec -T postgres psql -U postgres -d paysplit -c \
  "UPDATE users SET default_bank_code='970415', default_bank_account_number='0123456789', default_bank_account_holder='NGUOI A' WHERE id='$U1';"

curl -s -o /dev/null -w 'review: %{http_code}\n' -X POST "$API/api/v1/bills/$BID/review?group_id=$GID" \
  -H "Authorization: Bearer $T1" -H 'Content-Type: application/json' -d '{"version":1}'

curl -s -o /dev/null -w 'finalize: %{http_code}\n' -X POST "$API/api/v1/bills/$BID/finalize?group_id=$GID" \
  -H "Authorization: Bearer $T1" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: fin-$(date +%s)" -d '{"version":2}'
```

Kiểm tra dữ liệu đã ghi:

```bash
docker compose exec -T postgres psql -U postgres -d paysplit -c \
"SELECT member_id, final_amount, rounding_adjustment FROM bill_shares WHERE bill_id='$BID';
 SELECT sum(final_amount) AS tong_shares, (SELECT total FROM bills WHERE id='$BID') AS bill_total FROM bill_shares WHERE bill_id='$BID';
 SELECT debtor_member_id, amount, status FROM debts WHERE bill_id='$BID';"
```

**Kết quả đúng:** `tong_shares` bằng `bill_total`, và bảng `debts` chỉ có **một** dòng 50000 trạng thái `awaiting` cho người không phải Creditor. Creditor không tự nợ mình.

---

## 5. Hủy hóa đơn

Void cần đúng `version` hiện tại, thiếu nó sẽ nhận 409:

```bash
V=$(docker compose exec -T postgres psql -U postgres -d paysplit -t -A -c "SELECT version FROM bills WHERE id='$BID';")
curl -s -o /dev/null -w 'void: %{http_code}\n' -X POST "$API/api/v1/bills/$BID/void?group_id=$GID" \
  -H "Authorization: Bearer $T1" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: void-$(date +%s)" -d "{\"reason\":\"test tay\",\"version\":$V}"

docker compose exec -T postgres psql -U postgres -d paysplit -c \
"SELECT (SELECT status FROM bills WHERE id='$BID') AS bill,
        (SELECT string_agg(status::text,',') FROM debts WHERE bill_id='$BID') AS debts,
        (SELECT count(*) FROM bill_shares WHERE bill_id='$BID') AS shares_con_lai;"
```

**Kết quả đúng:** cả hóa đơn lẫn khoản nợ đều `voided`, nhưng hai dòng `bill_shares` **vẫn còn**. Lịch sử tài chính không bị xóa.

---

## 6. Mã chặn khi hóa đơn có vấn đề

Đây là phần vừa được thêm. Trước đây mọi trường hợp dưới đây đều trả về HTTP 200 với `mismatch_codes` rỗng và không có `breakdown`, nên client không phân biệt được hóa đơn hỏng với hóa đơn trống.

```bash
. /tmp/paysplit-test.env

mk() { curl -s -X POST $API/api/v1/bills -H "Authorization: Bearer $T1" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: rc-$(date +%s%N)" -d "$1" | python3 -c 'import sys,json;print(json.load(sys.stdin)["bill"]["id"])'; }

show() { curl -s "$API/api/v1/bills/$1?group_id=$GID" -H "Authorization: Bearer $T1" | python3 -c '
import sys,json
d=json.load(sys.stdin); bd=d.get("breakdown")
print("  codes:",json.dumps(d["bill"]["mismatch_codes"]),"| breakdown:",("%d dong"%len(bd)) if bd else "khong co")'; }

echo "A) mon chua gan cho ai"
show $(mk "{\"group_id\":\"$GID\",\"merchant_name\":\"A\",\"subtotal\":5000,\"total\":5000,\"split_method\":\"item_ratio\",\"items\":[{\"name\":\"X\",\"quantity\":\"1\",\"unit_price\":5000,\"line_total\":5000,\"assignments\":[]}]}")

echo "B) subtotal khai bao lech voi tong cac mon"
show $(mk "{\"group_id\":\"$GID\",\"merchant_name\":\"B\",\"subtotal\":99999,\"total\":99999,\"split_method\":\"item_ratio\",\"items\":[{\"name\":\"X\",\"quantity\":\"1\",\"unit_price\":5000,\"line_total\":5000,\"assignments\":[{\"member_id\":\"$M1\",\"weight\":\"1\"}]}]}")

echo "C) giam gia lon hon tien hang"
show $(mk "{\"group_id\":\"$GID\",\"merchant_name\":\"C\",\"subtotal\":100000,\"discount\":200000,\"total\":0,\"split_method\":\"item_ratio\",\"items\":[{\"name\":\"X\",\"quantity\":\"1\",\"unit_price\":100000,\"line_total\":100000,\"assignments\":[{\"member_id\":\"$M1\",\"weight\":\"1\"},{\"member_id\":\"$M2\",\"weight\":\"1\"}]}]}")
```

**Kết quả đúng:**

```
A) codes: ["ITEM_UNASSIGNED"]                              | breakdown: khong co
B) codes: ["SUBTOTAL_MISMATCH"]                            | breakdown: khong co
C) codes: ["DISCOUNT_EXCEEDS_BILL", "TOTAL_MISMATCH"]      | breakdown: khong co
```

Thử review một trong số đó, phải nhận `422 BILL_NOT_READY`. Người dùng đã thấy lý do từ bước xem trước nên không bị bất ngờ ở bước chốt sổ.

Bảy mã có thể xuất hiện: `ITEM_UNASSIGNED`, `INACTIVE_MEMBER_ASSIGNED`, `DISCOUNT_EXCEEDS_BILL`, `SUBTOTAL_MISMATCH`, `TOTAL_MISMATCH`, `DISCOUNT_NOT_ALLOCATABLE`, `CREDITOR_REQUIRED`.

> `mismatch_codes` luôn là mảng, hóa đơn sạch trả `[]` chứ không phải `null`. Nếu bạn thấy `null` ở đâu đó thì đó là lỗi, báo cho tôi.

---

## 7. Idempotency

```bash
. /tmp/paysplit-test.env
K="idem-$(date +%s)"
BODY="{\"group_id\":\"$GID\",\"merchant_name\":\"Idem\",\"subtotal\":1000,\"total\":1000,\"split_method\":\"item_ratio\",\"items\":[{\"name\":\"X\",\"quantity\":\"1\",\"unit_price\":1000,\"line_total\":1000,\"assignments\":[{\"member_id\":\"$M1\",\"weight\":\"1\"}]}]}"

echo "lan 1 va lan 2 voi cung key, cung body, phai ra CUNG mot bill id"
for i in 1 2; do
  curl -s -X POST $API/api/v1/bills -H "Authorization: Bearer $T1" -H 'Content-Type: application/json' \
    -H "Idempotency-Key: $K" -d "$BODY" | python3 -c 'import sys,json;print(" ",json.load(sys.stdin)["bill"]["id"])'
done

echo "cung key nhung body khac, phai ra 409 IDEMPOTENCY_KEY_REUSED"
curl -s -w ' HTTP %{http_code}\n' -X POST $API/api/v1/bills -H "Authorization: Bearer $T1" \
  -H 'Content-Type: application/json' -H "Idempotency-Key: $K" \
  -d "{\"group_id\":\"$GID\",\"merchant_name\":\"Khac\",\"subtotal\":2000,\"total\":2000}"
```

### Ca quan trọng nhất: khóa đã hết hạn

Đây chính là ca từng làm **sập tiến trình server**. Trước khi sửa, câu lệnh reserve không trả dòng nào, hàm đọc dự phòng lọc bỏ dòng hết hạn, và tầng usecase dereference con trỏ nil.

```bash
docker compose exec -T postgres psql -U postgres -d paysplit \
  -c "UPDATE bill_idempotency_keys SET expires_at = now() - interval '1 hour' WHERE actor_user_id='$U1';"

curl -s -o /dev/null -w 'goi lai sau khi khoa het han: %{http_code}\n' -X POST $API/api/v1/bills \
  -H "Authorization: Bearer $T1" -H 'Content-Type: application/json' -H "Idempotency-Key: $K" -d "$BODY"

curl -s -o /dev/null -w 'server con song? %{http_code}\n' $API/health
```

**Kết quả đúng:** `201` và `200`. Server vẫn sống, không có dòng `panic` nào trong log.

---

## 8. Luồng OCR đầy đủ

Cần `LLAMAINDEX_API_KEY` và `CLOUDINARY_*` trong `.env`, cùng một ảnh hóa đơn ở `testdata/bills/`.

```bash
. /tmp/paysplit-test.env

RESP=$(curl -s -X POST $API/api/v1/bills -H "Authorization: Bearer $T1" \
  -H "Idempotency-Key: ocr-$(date +%s)" \
  -F "group_id=$GID" -F "merchant_name=Quan OCR" \
  -F "images=@testdata/bills/image.png;type=image/png")
OBID=$(echo "$RESP" | python3 -c 'import sys,json;print(json.load(sys.stdin)["bill"]["id"])')
JOB=$(echo "$RESP" | python3 -c 'import sys,json;print(json.load(sys.stdin)["ocr_job"]["id"])')
echo "OBID=$OBID JOB=$JOB"
```

Phải trả `202` với một `ocr_job` ở trạng thái `queued`.

### Xem SSE trong lúc OCR chạy

```bash
timeout 60 curl -sN "$API/api/v1/bills/$OBID/events?group_id=$GID" -H "Authorization: Bearer $T1"
```

**Kết quả đúng:** một sự kiện `snapshot` ngay khi kết nối, rồi các `ocr.updated` chuyển từ `processing` sang `succeeded`, rồi `heartbeat` mỗi 15 giây. Nhấn `Ctrl+C` để thoát.

### Kiểm tra ảnh thật sự riêng tư

```bash
URL=$(curl -s "$API/api/v1/bills/$OBID?group_id=$GID" -H "Authorization: Bearer $T1" \
  | python3 -c 'import sys,json;print(list(json.load(sys.stdin)["signed_urls"].values())[0])')

curl -s -o /dev/null -w 'URL da ky:      %{http_code}\n' "$URL"
curl -s -o /dev/null -w 'URL cong khai:  %{http_code}\n' "$(echo "$URL" | sed -E 's|/image/private/s--.+--/v1/|/image/upload/|')"
```

**Kết quả đúng:** `200` cho URL đã ký, `404` cho URL công khai. Nếu URL công khai trả 200 thì ảnh hóa đơn của người dùng đang lộ ra internet, báo ngay.

### Kiểm tra OCR không tự ghi đè bản nháp

```bash
docker compose exec -T postgres psql -U postgres -d paysplit -c \
"SELECT status, attempts, (candidate->>'total') AS candidate_total, (candidate->>'bill_date') AS ngay,
        jsonb_array_length(candidate->'items') AS so_mon FROM ocr_jobs WHERE bill_id='$OBID';
 SELECT subtotal, total, version, (SELECT count(*) FROM bill_items WHERE bill_id='$OBID') AS so_mon_tren_bill
 FROM bills WHERE id='$OBID';"
```

**Kết quả đúng:** job `succeeded` với candidate đầy đủ, nhưng bản nháp vẫn `subtotal` 0, `total` 0, `version` 1, **0 món**. Kết quả OCR chỉ nằm trên dòng job cho tới khi bạn chủ động áp dụng.

Cũng kiểm tra `raw_response` không lọt ra API:

```bash
curl -s "$API/api/v1/bills/$OBID?group_id=$GID" -H "Authorization: Bearer $T1" | grep -c raw_response
```

Phải ra `0`.

### Áp dụng kết quả

```bash
echo "version sai, phai ra 409"
curl -s -w ' HTTP %{http_code}\n' -X POST "$API/api/v1/bills/$OBID/apply-candidate?group_id=$GID" \
  -H "Authorization: Bearer $T1" -H 'Content-Type: application/json' \
  -d "{\"job_id\":\"$JOB\",\"version\":99}" | tail -c 100

echo "version dung"
curl -s -X POST "$API/api/v1/bills/$OBID/apply-candidate?group_id=$GID" \
  -H "Authorization: Bearer $T1" -H 'Content-Type: application/json' \
  -d "{\"job_id\":\"$JOB\",\"version\":1}" | python3 -c '
import sys,json
b=json.load(sys.stdin)["bill"]
print("subtotal:",b["subtotal"],"version:",b["version"])
for it in b.get("items") or []: print("  -",it["name"],it["line_total"])'
```

### Chạy lại OCR không phá sửa đổi thủ công

Sửa tay bản nháp trước, rồi chạy lại OCR, rồi kiểm tra sửa đổi còn nguyên:

```bash
curl -s -o /dev/null -w 'ocr-retry: %{http_code}\n' -X POST "$API/api/v1/bills/$OBID/ocr-retry?group_id=$GID" \
  -H "Authorization: Bearer $T1" -H 'Content-Type: application/json' -H "Idempotency-Key: retry-$(date +%s)" -d '{}'

sleep 45
docker compose exec -T postgres psql -U postgres -d paysplit -c \
  "SELECT id, status, (candidate->>'total') AS total FROM ocr_jobs WHERE bill_id='$OBID' ORDER BY created_at;"
```

**Kết quả đúng:** hai dòng job, cả hai `succeeded`, mỗi dòng giữ candidate riêng. Bản nháp giữ nguyên những gì bạn đã sửa tay.

---

## 9. Hai worker dọn dẹp

Trước đây `OCRRetentionWorker` được viết đầy đủ nhưng không ai đăng ký nó, nên cam kết xóa dữ liệu OCR thô sau 30 ngày chưa bao giờ chạy.

Kiểm tra chúng chạy khi khởi động:

```bash
docker compose exec -T postgres psql -U postgres -d paysplit -c \
"SELECT kind, state, args, finalized_at FROM river_job
 WHERE kind IN ('ocr_raw_retention_cleanup','bill_idempotency_key_cleanup')
 ORDER BY id DESC LIMIT 4;"
```

Phải thấy hai dòng `completed`, và `ocr_raw_retention_cleanup` mang `{"older_than_hours": 720}` đọc từ config.

Kiểm tra chúng **thật sự xóa**. Làm dữ liệu cũ đi rồi khởi động lại server:

```bash
docker compose exec -T postgres psql -U postgres -d paysplit -c \
"UPDATE ocr_jobs SET completed_at = now() - interval '40 days' WHERE bill_id='$OBID';
 UPDATE bill_idempotency_keys SET expires_at = now() - interval '1 day' WHERE actor_user_id='$U1';"
```

Tắt server rồi bật lại (nhớ dùng `ss -ltnp | grep 8080` để tìm đúng pid), rồi:

```bash
docker compose exec -T postgres psql -U postgres -d paysplit -c \
"SELECT id, raw_response IS NOT NULL AS con_raw, candidate IS NOT NULL AS con_candidate FROM ocr_jobs WHERE bill_id='$OBID';
 SELECT count(*) AS khoa_het_han_con_lai FROM bill_idempotency_keys WHERE expires_at <= now();"
```

**Kết quả đúng:** `con_raw` là `f` trên mọi dòng nhưng `con_candidate` vẫn `t`. Chỉ dữ liệu thô nhạy cảm bị xóa, kết quả đã chuẩn hóa giữ lại. Số khóa hết hạn còn lại bằng `0`.

---

## 10. Phân quyền

```bash
. /tmp/paysplit-test.env

echo "khong co token"
curl -s -o /dev/null -w '  %{http_code} (mong doi 401)\n' $API/api/v1/bills

echo "nguoi B khong phai Captain, thu chot so"
curl -s -w ' HTTP %{http_code}\n' -X POST "$API/api/v1/bills/$BID/finalize?group_id=$GID" \
  -H "Authorization: Bearer $T2" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: forbid-$(date +%s)" -d '{"version":1}' | tail -c 120
```

**Kết quả đúng:** `401 AUTHENTICATION_REQUIRED` và `403 FORBIDDEN`.

> Chỗ này có một điểm lệch giữa spec và code mà tôi phát hiện khi viết hướng dẫn này. Spec [`0003-finalize-void.md`](specs/0003-bill-ocr-v1/0003-finalize-void.md) dòng 61 ghi thành viên thường chốt sổ hoặc hủy sẽ nhận `403 CAPTAIN_REQUIRED`, nhưng module Bill trả `403 FORBIDDEN`. Module Group thì lại dùng đúng `CAPTAIN_REQUIRED`. Mã HTTP đúng, chỉ mã lỗi trong body khác spec và khác module bên cạnh. Chưa sửa, xem ghi chú ở cuối file.

---

## 11. Chạy bộ test tự động

Nếu bạn chỉ muốn biết mọi thứ còn xanh:

```bash
go test ./...

# riêng phần kiểm tra bất biến chia tiền trên 4260 tổ hợp đầu vào
go test -run BruteForce -v ./internal/modules/bill/usecase/

# phần cần database thật
TEST_DATABASE_URL="$DATABASE_URL" go test -run Idempotency -v ./internal/modules/bill/repository/postgres/
```

---

## Dọn dẹp

```bash
. /tmp/paysplit-test.env
docker compose exec -T postgres psql -U postgres -d paysplit \
  -c "DELETE FROM groups WHERE id='$GID'; DELETE FROM users WHERE email IN ('$E1','$E2');"
rm /tmp/paysplit-test.env
```

---

## Ghi chú, những điểm chưa khớp đã biết

Ghi lại để bạn không tưởng là mình test sai.

1. **`FORBIDDEN` thay vì `CAPTAIN_REQUIRED`** ở mục 10. Spec `0003-finalize-void.md` dòng 61 ghi một đằng, code trả một nẻo, và module Group ngay bên cạnh lại dùng đúng mã trong spec. Sửa thì chỉ là thêm một nhánh vào bảng ánh xạ lỗi trong `internal/modules/bill/delivery/http/handler.go`, nhưng nó đổi hợp đồng API nên cần quyết định trước.

2. **`attempts` bằng 3 trên job OCR đầu tiên** dù job thành công, trong khi job chạy lại thủ công chỉ có 1. Không có dòng log nào giải thích hai lần tăng kia. Có thể là retry thật của nhà cung cấp, cũng có thể là bộ đếm tăng nhầm chỗ. Nếu bạn thấy `attempts` lớn hơn 1 ở mục 8 thì đó là hiện tượng này, không phải bạn làm sai.

3. **`DISCOUNT_NOT_ALLOCATABLE` gần như không kích hoạt được bằng tay.** Nó là lớp phòng vệ cho trường hợp giảm giá hợp lệ về tổng nhưng dồn vào những người không hấp thụ nổi. Cả lượt chạy thật lẫn bộ quét 4260 tổ hợp đều không chạm tới nó. Đừng mất thời gian cố tạo ca đó.

4. **Worker dọn ảnh trên Cloudinary** hiện chỉ được nối dây với kho ảnh đại diện của module Auth, chưa bao gồm kho ảnh hóa đơn. Nghĩa là xóa bản nháp có ảnh thì ảnh trên Cloudinary chưa chắc được dọn tự động. Chưa xác minh, xem mục 8 của `bill-ocr-module.md`.
