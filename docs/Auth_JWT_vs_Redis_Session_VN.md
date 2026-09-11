# Từ JWT + Session sang Opaque Credential + Redis

**Nhánh**: `lampt/auth-account-v2` · **Spec**: 0011, 0012 · **Ngày**: 2026-09-10

Tài liệu này giải thích hệ xác thực **cũ** (JWT + session trên Postgres) và hệ
**mới** (credential đục + session trên Redis): mỗi hệ làm gì, làm bằng cách nào,
được gì, mất gì, và vì sao lại đổi.

---

## 0. Bài toán gốc

HTTP là giao thức không nhớ. Mỗi request tới server là một tờ giấy trắng. Sau khi
người dùng đăng nhập một lần, mọi request sau đó phải tự chứng minh "tôi là ai",
và server phải trả lời được ba câu hỏi:

1. **Đây là ai?** (định danh)
2. **Người này còn được phép không?** (phiên còn sống, tài khoản chưa bị khóa)
3. **Trả lời hai câu trên tốn bao nhiêu?** (mỗi request đều phải trả giá này)

Có đúng hai họ giải pháp, và chúng đối lập nhau ở một điểm duy nhất:

| | **Token tự chứa** (JWT) | **Token tham chiếu** (opaque) |
|---|---|---|
| Token mang gì | dữ liệu + chữ ký | chỉ một chuỗi random vô nghĩa |
| Xác thực bằng | kiểm chữ ký, **không cần tra cứu** | **bắt buộc** tra kho phiên |
| Thu hồi | khó — token đã phát thì còn hiệu lực tới lúc hết hạn | dễ — xóa bản ghi là chết ngay |
| Chi phí | ~0, tính CPU | một lượt đi tới kho dữ liệu |

Toàn bộ câu chuyện dưới đây là chuyện dự án đã đi từ họ thứ nhất sang họ thứ hai,
và **vì sao**.

---

## 1. Hệ cũ: JWT + Session trên Postgres

### 1.1 Các thành phần

Hệ cũ có **hai** loại token, vai trò khác nhau:

**Access token** — JWT, ký HS256 bằng `JWT_SECRET_KEY`, TTL **15 phút**. Đây là
thứ gửi kèm mỗi request. Nội dung (payload) là JSON, **đọc được bằng mắt**, chỉ
không sửa được vì có chữ ký:

```json
{
  "iss": "paysplit-backend",                    // issuer
  "sub": "0192f3c1-...",                        // user ID
  "role": "user",                               // vai trò
  "sid": "0192f3c2-...",                        // session ID
  "iat": 1757400000, "exp": 1757400900          // phát hành / hết hạn
}
```

**Refresh token** — chuỗi random 32 byte **đục** (không mang thông tin), TTL
**7 ngày** (`AUTH_REFRESH_TOKEN_TTL_HOURS=168`). Không gửi kèm request thường;
chỉ dùng để xin access token mới khi cái cũ hết hạn.

Hai bảng Postgres đỡ phía sau:

```
sessions
  id, user_id, device_id, device_name, fcm_token
  expires_at, revoked_at, revoked_reason

session_refresh_tokens
  id, session_id, token_hash (BYTEA, SHA-256, UNIQUE)
  issued_at, expires_at, used_at, revoked_at
```

Lưu ý `token_hash` — refresh token được **băm** trước khi lưu, y như mật khẩu. Ai
đọc được database cũng không dùng được nó. Đây là tính chất tốt của hệ cũ, và hệ
mới giữ lại đúng nó.

### 1.2 Luồng đăng nhập

```
POST /api/v1/auth/sign-in
  │
  ├─ kiểm email + mật khẩu (bcrypt)
  ├─ INSERT sessions            → sid, expires_at = now + 7 ngày
  ├─ tạo refresh token random 32 byte
  ├─ INSERT session_refresh_tokens (token_hash = SHA256(raw))
  └─ ký JWT { sub, role, sid }, exp = now + 15 phút
      │
      └→ trả về { access_token, refresh_token, user }
```

Client lưu **cả hai** token vào `flutter_secure_storage`.

### 1.3 Luồng mỗi request có xác thực

Đây là chỗ quan trọng nhất, và cũng là chỗ hệ cũ có vấn đề. Middleware có **hai
biến thể**:

**`Auth` (gọi là `liveAuth`)** — dùng cho hầu hết route:

```
Authorization: Bearer <jwt>
  │
  ├─ [1] verifier.Verify(jwt)
  │       kiểm chữ ký HS256, alg, issuer, exp
  │       → userID, role, sessionID     (KHÔNG chạm database)
  │
  ├─ [2] sessions.ValidateSession(userID, sessionID, now)
  │       SELECT u.id, u.role, s.id
  │       FROM sessions s JOIN users u ON u.id = s.user_id
  │       WHERE s.id = $1 AND s.user_id = $2
  │         AND s.revoked_at IS NULL
  │         AND s.expires_at > $3
  │         AND u.status = 'active'          ← MỘT QUERY POSTGRES / REQUEST
  │
  ├─ [3] if identity.Role != role → 401
  │       (role trong JWT phải khớp role trong DB)
  │
  └─ đặt userID, role, sessionID vào context
```

**`TokenAuth`** — chỉ chạy bước [1], **không** tra database. Dùng cho các route
`/auth` (ví dụ `sign-out`).

Hãy nhìn kỹ bước [2]. **Đây là nghịch lý trung tâm của hệ cũ.**

Lý do duy nhất người ta chọn JWT là để **không phải tra cứu** — token tự chứng
minh được chính nó. Nhưng vì cần thu hồi phiên và cần khóa tài khoản có hiệu lực,
hệ cũ vẫn phải `JOIN users` **mỗi request**. Nghĩa là:

> Hệ cũ trả **cả hai** cái giá: sự phức tạp của JWT (secret, chữ ký, hết hạn,
> refresh, rotation, reuse detection) **và** một lượt đi Postgres mỗi request —
> nhưng chỉ nhận được **một nửa** lợi ích của mỗi bên.

Nó không nhanh như JWT thuần (vẫn query DB), và không thu hồi tức thì như opaque
token thuần (còn cửa sổ 15 phút ở `TokenAuth`, và JWT vẫn hợp lệ về mặt chữ ký).

### 1.4 Luồng refresh + rotation + reuse detection

Đây là phần phức tạp nhất của hệ cũ. Access token chết sau 15 phút, nên client
phải đổi lấy cái mới:

```
FE gọi API → 401
  │
  ├─ AuthInterceptor bắt 401, tạm giữ request lại
  ├─ POST /api/v1/auth/refresh { refresh_token }
  │     │
  │     │  BEGIN TRANSACTION
  │     ├─ SELECT t.session_id, s.user_id
  │     │    FROM session_refresh_tokens t JOIN sessions s ...
  │     │    WHERE t.token_hash = SHA256(raw)
  │     ├─ SELECT ... FROM users  FOR UPDATE          ← khóa hàng
  │     ├─ SELECT ... FROM sessions FOR UPDATE        ← khóa hàng
  │     ├─ SELECT ... FROM session_refresh_tokens FOR UPDATE  ← khóa hàng
  │     │
  │     ├─ NẾU used_at IS NOT NULL:        ★ REUSE DETECTION
  │     │     token này đã dùng rồi → có kẻ đang dùng token đánh cắp
  │     │     UPDATE sessions SET revoked_reason = 'refresh_reuse'
  │     │     UPDATE session_refresh_tokens SET revoked_at = now  (CẢ phiên)
  │     │     pg_notify → đóng SSE
  │     │     COMMIT rồi trả ErrSessionRevoked        ← giết cả phiên
  │     │
  │     ├─ kiểm: token revoked? session revoked? token hết hạn?
  │     │        session hết hạn? device_id khớp? user còn active?
  │     │
  │     ├─ UPDATE session_refresh_tokens SET used_at = now   ← đánh dấu đã dùng
  │     ├─ newExpiry = min(now + 7 ngày, session.expires_at)
│     │     ← BỊ KẸP bởi hạn phiên cố định: `sessions.expires_at` được đặt
│     │       một lần lúc đăng nhập và KHÔNG BAO GIỜ được nới ra. Nên phiên
│     │       cũ là 7 ngày CỨNG — dùng app đều đặn cũng phải đăng nhập lại.
  │     ├─ INSERT session_refresh_tokens (token_hash mới)     ← ROTATION
  │     └─ COMMIT
  │
  ├─ nhận { access_token mới, refresh_token mới }
  └─ chạy lại request bị giữ
```

**Rotation** nghĩa là mỗi lần refresh sinh ra một refresh token **mới** và đánh
dấu cái cũ `used_at`. **Reuse detection** là hệ quả: nếu một token đã có `used_at`
lại được đem dùng nữa, chỉ có hai khả năng — token bị đánh cắp, hoặc client lỗi.
Cả hai đều đáng giết cả phiên.

Cơ chế này **đúng và cần thiết** trong họ JWT. Nhưng hãy đếm chi phí: một
transaction với **ba** lần `FOR UPDATE`, sáu điều kiện kiểm tra, một nhánh xử lý
gian lận, một bảng riêng, một endpoint riêng, và phía FE là một interceptor phải
biết tạm giữ request, chống refresh đồng thời (nhiều request 401 cùng lúc không
được cùng gọi refresh), rồi chạy lại request.

**Toàn bộ sự phức tạp này tồn tại chỉ vì access token phải ngắn hạn.** Và access
token phải ngắn hạn chỉ vì nó **không thu hồi được** — cửa sổ 15 phút chính là
mức thiệt hại tối đa mà thiết kế chấp nhận khi một token bị đánh cắp.

### 1.5 Luồng thu hồi

```
sign-out / đổi mật khẩu / admin khóa tài khoản
  └─ UPDATE sessions SET revoked_at = now, revoked_reason = '...'
     UPDATE session_refresh_tokens SET revoked_at = now
     pg_notify('session.ended') → đóng stream SSE
```

Hiệu lực: **tức thì** với route dùng `liveAuth` (vì bước [2] đọc `revoked_at`),
nhưng **tối đa 15 phút** với route dùng `TokenAuth`.

### 1.6 Hệ cũ — lợi và hại

**Lợi**

| Lợi | Vì sao |
|---|---|
| Refresh token được băm trước khi lưu | dump DB không dùng lại được — tính chất bảo mật đúng |
| Reuse detection thật | token bị đánh cắp và dùng lại thì cả phiên bị giết |
| Chỉ cần Postgres | không thêm hạ tầng, không thêm thứ có thể sập |
| Ràng buộc `device_id` khi refresh | refresh token bị đánh cắp mà dùng ở thiết bị khác sẽ bị chặn |
| **Role luôn tươi** | bước [3] so `role` trong JWT với `role` trong DB mỗi request → đổi role có hiệu lực ngay |
| Bền vững | Postgres có WAL, backup, replication — mất phiên là chuyện rất khó xảy ra |

**Hại**

| Hại | Vì sao đây là vấn đề |
|---|---|
| **Trả giá hai lần, hưởng nửa lợi ích** | vẫn query Postgres mỗi request (mất lợi thế JWT) mà vẫn không thu hồi được tức thì ở mọi đường (mất lợi thế opaque) |
| Cửa sổ thu hồi 15 phút ở `TokenAuth` | route nào lỡ dùng biến thể này thì token đã thu hồi vẫn đi qua |
| **Hai đường xác thực khác nhau** | `Auth` và `TokenAuth` — chọn sai biến thể cho một route là một lỗ bảo mật im lặng, và không có gì trong kiểu dữ liệu ngăn việc chọn sai |
| Mỗi request một `JOIN` hai bảng | Postgres nằm trên đường đi của mọi request, và đây là query nặng hơn một lần đọc key |
| Bộ máy refresh cực phức tạp | 1 endpoint + 1 bảng + 1 transaction 3 khóa + logic reuse + interceptor FE biết tạm giữ và chống refresh đồng thời |
| Client giữ **hai** credential | phải lưu, phải làm mới, phải xử lý ca lệch nhau (access còn refresh chết, và ngược lại) |
| `JWT_SECRET_KEY` là bí mật toàn hệ thống | lộ secret = phát được token cho **bất kỳ** ai; đổi secret = đăng xuất **toàn bộ** người dùng |
| Payload JWT đọc được bằng mắt | `role`, `user_id`, `sid` lộ ra trong log, crash report, proxy |
| **Phiên hết hạn cứng sau 7 ngày** | `sessions.expires_at` đặt một lần lúc đăng nhập, `RotateRefresh` không nới ra. Người dùng mở app mỗi ngày vẫn bị đăng xuất vào ngày thứ 8 — không có lý do bảo mật nào bắt buộc điều đó |
| `sessions.fcm_token` gắn với phiên | phiên hết hạn là mất token push — user không mở app 8 ngày thì **không nhận được thông báo** nữa |

---

## 2. Hệ mới: Opaque Credential + Redis

### 2.1 Ý tưởng một câu

> Bỏ hẳn token tự chứa. Client giữ **một** chuỗi random vô nghĩa. Kho phiên trên
> Redis là **nguồn phán quyết duy nhất**. Vì mỗi request đều phải tra Redis, thu
> hồi có hiệu lực ngay, nên không cần token ngắn hạn, nên không cần refresh.

Đổi một lượt `JOIN` Postgres thành một lượt đọc key Redis — vừa **nhanh hơn**,
vừa **đơn giản hơn**, vừa **an toàn hơn**. Đây là lý do việc đổi là đáng làm.

### 2.2 Hai khái niệm bị tách rời

Điểm dễ hiểu sai nhất khi đọc code:

| | **credential** | **SID** |
|---|---|---|
| Là gì | 32 byte random → base64url → **43 ký tự** | UUID |
| Ai giữ | client, gửi kèm `Authorization: Bearer <...>` | server, khớp `sessions.id` |
| Vào context? | **không bao giờ** | có |

Vì sao phải tách: tầng realtime ràng buộc session ID **phải là UUID**
(`realtime.UserEnvelope.TargetSIDs`, `sse_handler` gọi `uuid.Parse`), nên chuỗi
đục không đóng được cả hai vai. Middleware đặt **SID** vào context và tuyệt đối
không đặt credential.

Credential sinh từ `domain.NewOpaqueToken()` — **đúng nguồn ngẫu nhiên 32 byte mà
refresh token cũ dùng**, nên độ mạnh không giảm. Cố tình **không** dùng uuidv7:
nó chứa timestamp và chỉ ~74 bit ngẫu nhiên, quá yếu cho một credential sống
7–30 ngày.

### 2.3 Sơ đồ khóa trên Redis

Chỉ hai loại key:

```
session:<sha256_hex(credential)>   → HASH { sid, user_id, role, absolute_exp }
                                     TTL = idle TTL  (7 ngày, TRƯỢT)

user_session:<user_id>             → STRING = sha256_hex đó   (con trỏ ngược)
                                     TTL = absolute TTL (30 ngày, CỐ ĐỊNH)
```

**Key là SHA-256 của credential, không phải credential trần.** Ai đọc được dump
Redis, file AOF, hay output `MONITOR` cũng **không** đăng nhập lại được. Đây đúng
là tính chất mà `session_refresh_tokens.token_hash` vốn có, và không được đánh mất
khi chuyển sang Redis.

**Con trỏ ngược** tồn tại để trả lời câu "**user này còn phiên nào không**" — câu
hỏi mà đường đổi mật khẩu và đường admin khóa tài khoản bắt buộc phải hỏi. Không
có nó thì phải `SCAN` toàn bộ keyspace. Kèm quy ước **một user một phiên sống**,
nên con trỏ là `STRING` đơn chứ không phải `SET`.

### 2.4 Hai TTL và cách chúng bị kẹp vào nhau

- `SESSION_IDLE_TTL_HOURS=168` (7 ngày) — **TTL trượt**, nạp lại mỗi lần đọc.
  Đây là thứ thay thế toàn bộ bộ máy refresh: dùng app thì phiên tự sống.
- `SESSION_ABSOLUTE_TTL_HOURS=720` (30 ngày) — **trần cứng**, không lần gia hạn
  nào vượt qua. Một credential bị đánh cắp không sống mãi chỉ vì kẻ cắp dùng đều.

Trần được cưỡng chế ngay trong Lua, không chỉ ở phía Go:

```lua
if absExp <= now then
  redis.call('DEL', KEYS[1])          -- xóa luôn, để bộ nhớ tự lành
  return nil
end
local remaining = absExp - now
if remaining < ttl then ttl = remaining end   -- KẸP
redis.call('EXPIRE', KEYS[1], ttl)
```

Không có đoạn kẹp thì mỗi request lại đẩy hạn ra thêm 7 ngày và key sẽ **sống lâu
hơn chính `absolute_exp` của nó** — trần thành vô nghĩa.

**Vì sao con trỏ mang TTL tuyệt đối chứ không trượt**: bản ghi phiên được gia hạn
mỗi request nên có thể sống quá 7 ngày; nếu con trỏ hết hạn trước thì **mất khả
năng thu hồi theo user** — admin khóa tài khoản sẽ không tìm ra phiên nào để giết.

Chọn TTL bất đối xứng cho một bất biến sạch: bản ghi hết hạn tại
`min(idle, absolute còn lại)`, con trỏ hết hạn đúng tại `absolute_exp`, nên **bản
ghi luôn chết trước hoặc cùng lúc với con trỏ**. Chỉ tồn tại được **con trỏ mồ
côi** (user không mở app 8 ngày), không bao giờ có bản ghi mồ côi — và cả hai
script thu hồi đều dọn đúng ca đó.

### 2.5 Vì sao phải là Lua script

Redis không có transaction kiểu Postgres, và **mỗi thao tác đụng tới hai key**.
Tách thành nhiều lệnh rời sẽ để lộ cửa sổ race.

| Script | Việc | Điểm tinh tế |
|---|---|---|
| `createScript` | tạo phiên + giết phiên cũ | `DEL` bản ghi cũ, không chỉ ghi đè con trỏ |
| `getScript` | xác thực + gia hạn | 1 round-trip; cưỡng chế trần + kẹp TTL |
| `peekAndDeleteScript` | đăng xuất | đọc trước khi xóa; xóa con trỏ **có điều kiện** |
| `revokeUserSIDsScript` | thu hồi **khớp SID** | dùng cho job retry chạy trễ |
| `revokeUserScript` | thu hồi **vô điều kiện** | chỉ cho đường khóa tài khoản |

Ba chỗ đáng chú ý nhất:

**`createScript` phải `DEL` bản ghi cũ.** Bản ghi phiên cũ giữ TTL riêng; chỉ ghi
đè con trỏ thì nó không còn chỉ mục nào trỏ tới nhưng **vẫn sống**, và thiết bị cũ
tiếp tục đăng nhập được tới hết TTL. Đó là một lỗ rò rỉ auth thật.

**`peekAndDeleteScript` xóa con trỏ có điều kiện** — `if GET ptr == ARGV[1]`. Nếu
user vừa đăng nhập lại ở nơi khác, con trỏ đã thuộc về phiên mới; xóa vô điều kiện
sẽ làm mất khả năng thu hồi của **phiên mới**.

**Phải `peek` trước khi `delete`** vì bên gọi cần `sid` + `user_id` để ghi audit
bên Postgres và phát sự kiện đóng SSE — sau `DEL` thì không lấy lại được.

### 2.6 Luồng đăng nhập

```
POST /api/v1/auth/sign-in
  │
  ├─ kiểm email + mật khẩu (bcrypt)
  ├─ repo.CreateSession()          → hàng audit `sessions`, expires_at = now + 30 ngày
  ├─ session.NewCredential()       → 32 byte random
  ├─ sessions.Create()             → Lua: ghi Redis, giết phiên cũ, nguyên tử
  │     ↳ nếu lỗi: RevokeSession(reason="session_store_failed")
  │                                  ← không để hàng audit treo
  └→ trả { session_id: "<43 ký tự>", expires_at, user }
```

Postgres **trước**, Redis **sau**, và có đường lùi. Không còn `access_token` /
`refresh_token`.

### 2.7 Luồng mỗi request có xác thực

```
Authorization: Bearer <credential 43 ký tự>
  │
  ├─ tách bearer
  ├─ getScript trên Redis:  HMGET → kiểm absolute_exp → EXPIRE (gia hạn)
  │     MỘT round-trip Redis. KHÔNG chạm Postgres.
  │
  ├─ lỗi?
  │   ├─ session.ErrNotFound → 401   (phiên chết thật)
  │   └─ lỗi khác            → 503   (hạ tầng — xem 2.10)
  │
  └─ đặt user_id, role, SID vào context
```

So với hệ cũ: **bỏ được `JOIN users` mỗi request**. `role` được cache trong hash
để đạt điều đó. Chỉ còn **một** đường xác thực — biến thể `TokenAuth` "chỉ kiểm
chữ ký" không còn tồn tại được nữa, vì credential đục không mang thông tin nào để
kiểm ngoại tuyến. Một lớp lỗi cấu hình bị **xóa khỏi thiết kế**, không phải được
sửa.

### 2.8 Luồng đăng xuất

Route `sign-out` **không** gắn middleware `Auth`, vì credential có thể đã chết và
đăng xuất vẫn phải thành công. Handler tự tách bearer bằng
`middleware.BearerToken` — **được export riêng cho mục đích này**, vì hai bộ parse
bearer khác nhau trong cùng một hệ thống là lỗi auth kinh điển.

`ErrNotFound` cũng trả **204**. Đăng xuất là idempotent: không header, bearer sai
định dạng, credential đã chết — cả ba đều 204.

### 2.9 Luồng thu hồi và backstop

Middleware **không còn đọc `users.status`**. Nên lệnh `DEL` trên Redis trở thành
**cơ chế cưỡng chế duy nhất** của việc khóa tài khoản — một lần ghi Redis lỡ nghĩa
là người bị khóa vẫn dùng app tới hết TTL. Đó là rủi ro mới, và nó được trả bằng
một backstop bền vững:

```
admin khóa tài khoản
  │  BEGIN TRANSACTION (Postgres)
  ├─ UPDATE users SET status = 'locked'
  ├─ UPDATE sessions SET revoked_at = now RETURNING id   → dùng cho pg_notify
  ├─ pg_notify('session.ended')                          → đóng SSE
  ├─ river.InsertTx(session_redis_purge)   ★ enqueue TRONG transaction
  │  COMMIT
  │
  ├─ RevokeUser(user_id) trên Redis        ← hiệu lực tức thì
  │
  └─ nếu lệnh trên lỡ: job River retry (MaxAttempts 10, nhịp phút)
        └─ cạn lượt → SessionPurgeExhaustedTotal.Inc() + log
```

`InsertTx` **trong** transaction nghĩa là job chỉ tồn tại khi commit thành công —
cùng ngữ nghĩa mà `pg_notify` đang dựa vào, nên **không cần bảng outbox riêng**.
Retry theo phút chứ không theo nhịp dọn dẹp 24 giờ, vì mỗi phút chậm trễ là một
phút tài khoản bị khóa vẫn gọi được API.

### 2.10 401 vs 503 — chi tiết nhỏ, hậu quả lớn

```go
if errors.Is(err, session.ErrNotFound) {
    writeAuthError(w)              // 401 — phiên chết thật
    return
}
writeSessionStoreUnavailable(w)    // 503 — lỗi hạ tầng
```

Gộp lỗi Redis thành 401 là **nói với ứng dụng rằng phiên đã chết** → client xóa
credential → người dùng phải đăng nhập lại bằng tay. Một cú chớp vài chục giây của
Redis khi đó sẽ **đăng xuất vĩnh viễn toàn bộ người dùng**.

Vì hệ mới đặt Redis lên đường đi của mọi request, việc phân biệt "phiên chết" với
"kho phiên không đọc được" trở thành yêu cầu bắt buộc — hệ cũ không cần nghĩ đến
nó vì Postgres đã là dependency bắt buộc từ trước.

### 2.11 Bất biến ở tầng cấu hình

- `REDIS_URL` bắt buộc có mật khẩu **ngoài development**; `APP_ENV` không set được
  coi là **không** phải development (fail-closed).
- `IdleTTL <= AbsoluteTTL`, cả hai dương.
- `AUTH_RECORD_RETENTION_DAYS >= AbsoluteTTL - IdleTTL` — hàng audit phải sống lâu
  hơn phiên sống.
- Redis phải là **`noeviction`**, cưỡng chế lúc bootstrap. `allkeys-lru` sẽ **âm
  thầm** xóa phiên của người ít hoạt động khi bộ nhớ đầy → những lần đăng xuất
  ngẫu nhiên không log, không tái hiện được. Managed Redis thường chặn
  `CONFIG GET`, nên đọc không được thì chỉ cảnh báo; **chỉ khi đọc được VÀ giá trị
  sai mới dừng**.
- `appendonly=yes`, `appendfsync=everysec` — Redis phải ghi xuống đĩa, vì nó đang
  giữ dữ liệu mà mất là mọi người bị đăng xuất.

### 2.12 Cái gì vẫn ở Postgres, và vì sao

Redis là **nguồn phán quyết**, nhưng Postgres không bị bỏ:

| Ở lại Postgres | Vì sao |
|---|---|
| `sessions` (hàng audit) | lịch sử đăng nhập, device, lý do thu hồi — dữ liệu cần bền, không phải dữ liệu cần nhanh |
| `pg_notify('session.ended')` | vẫn là đường đóng stream SSE. Đây là lý do `UpdateAccountStatusWithRevocation` **vẫn** trả danh sách SID — chỉ cho tín hiệu realtime, **không** cho việc thu hồi |
| `river_job` | backstop bền vững; Redis không đủ tin cậy để giữ hàng đợi thu hồi của chính nó |
| `device_tokens` (bảng mới) | token FCM **không** nên chết cùng phiên — user không mở app 8 ngày thì phiên hết hạn nhưng **vẫn phải nhận push**. Migration `000018` tách nó ra khỏi `sessions.fcm_token`, `000019` drop cột cũ |

### 2.13 Hệ mới — lợi và hại

**Lợi**

| Lợi | Vì sao |
|---|---|
| **Thu hồi tức thì, mọi đường** | Redis là nguồn phán quyết duy nhất; `DEL` là chết ngay, không có cửa sổ 15 phút |
| **Nhanh hơn hệ cũ** | một lượt đọc key Redis thay cho một `JOIN` hai bảng Postgres |
| **Bỏ hẳn bộ máy refresh** | không endpoint `/refresh`, không bảng `session_refresh_tokens`, không transaction 3 khóa, không reuse detection, không interceptor FE tạm giữ request |
| **Chỉ còn một đường xác thực** | `TokenAuth` không tồn tại được → xóa hẳn một lớp lỗi cấu hình |
| Client giữ **một** credential | không còn ca lệch giữa hai token |
| **Không còn secret toàn hệ thống** | không có `JWT_SECRET_KEY`; lộ một credential chỉ ảnh hưởng một phiên |
| Credential không mang thông tin | không lộ `role`/`user_id` qua log, crash report, proxy |
| Vẫn băm trước khi lưu | dump Redis / AOF / `MONITOR` không dùng lại được |
| Trần tuyệt đối 30 ngày | credential bị đánh cắp không sống mãi dù kẻ cắp dùng đều |
| **Người dùng thường xuyên không bị đăng xuất định kỳ** | hệ cũ hết 7 ngày là phải đăng nhập lại bất kể có dùng app hay không, vì `sessions.expires_at` cố định; hệ mới trượt trong 30 ngày |
| Push notification sống độc lập với phiên | `device_tokens` sửa một bug thật của hệ cũ |

**Hại**

| Hại | Mức độ / cách trả |
|---|---|
| **Redis thành SPOF** — mất Redis là mất xác thực | trả bằng: `noeviction` + AOF cưỡng chế, ping fail-fast lúc bootstrap, `/health/ready` báo 503, và **401 vs 503** để một cú chớp không đăng xuất ai. Nhưng Redis sập lâu = app không dùng được |
| **`role` bị cache tới 7 ngày** | ⚠️ **Đây là bước lùi so với hệ cũ.** Hệ cũ so `role` trong JWT với `role` trong DB **mỗi request** nên đổi role có hiệu lực ngay. Hệ mới đọc `role` từ Redis. Mọi thay đổi `users.role` **PHẢI** kéo theo thu hồi phiên, nếu không user bị hạ quyền vẫn giữ quyền cũ tối đa 7 ngày. Hiện **chưa có endpoint nào mutate `role`** nên chưa phải bug đang sống, nhưng ràng buộc này chỉ tồn tại dưới dạng comment, **không có test nào canh** |
| Mất reuse detection | credential đục không rotate nên không có khái niệm "dùng lại". Bù lại: credential không rotate thì cũng không có cửa sổ mà token cũ và mới cùng hợp lệ |
| Mất ràng buộc `device_id` khi refresh | không còn refresh nên không còn chỗ kiểm. Credential bị đánh cắp dùng được ở thiết bị khác cho tới khi bị thu hồi |
| Thêm một thành phần hạ tầng | thêm container, thêm mật khẩu phải quản, thêm thứ phải monitor, thêm thứ có thể cấu hình sai |
| Phiên nằm trong RAM | Redis restart mất AOF = mọi người đăng nhập lại. Đã trả bằng `appendfsync=everysec`, nhưng vẫn yếu hơn WAL của Postgres |
| Logic then chốt viết bằng Lua | 5 script, khó debug hơn Go, không có type checking. Đã trả bằng test trên `miniredis` **và** Redis 8 thật |
| Một user một phiên sống | đăng nhập máy B là máy A bị đăng xuất. Là **quyết định thiết kế**, không phải hạn chế — nhưng cần biết |
| Không thu hồi được từng phiên riêng khi có nhiều thiết bị | hệ quả trực tiếp của điều trên |

---

## 3. So sánh trực diện

| | **Cũ: JWT + Postgres** | **Mới: Opaque + Redis** |
|---|---|---|
| Client giữ | access token (15m) + refresh token (7d) | **một** credential (43 ký tự) |
| Token mang thông tin? | có — `sub`, `role`, `sid` đọc được | không, hoàn toàn đục |
| Xác thực mỗi request | verify chữ ký **+** `JOIN` 2 bảng Postgres | **1** lượt đọc key Redis |
| Thu hồi có hiệu lực | tức thì với `liveAuth`, **≤15 phút** với `TokenAuth` | **tức thì, mọi đường** |
| Làm mới phiên | endpoint `/refresh` + rotation + reuse detection | **TTL trượt tự động** |
| Số đường xác thực | **2** (`Auth`, `TokenAuth`) | **1** |
| Bảng phục vụ auth | `sessions` + `session_refresh_tokens` | `sessions` (chỉ audit) + `device_tokens` |
| Secret toàn hệ thống | `JWT_SECRET_KEY` | **không có** |
| Hạn phiên | **7 ngày cứng** từ lúc đăng nhập — dùng đều cũng phải đăng nhập lại | **7 ngày trượt** + trần cứng 30 ngày — dùng đều thì không phải đăng nhập lại |
| Điểm chết đơn | Postgres | Postgres **và Redis** |
| `role` tươi? | **có** — so với DB mỗi request | không — cache ≤7 ngày |
| Lỗi hạ tầng trả về | 401 (lẫn với phiên chết) | **503** (phân biệt rõ) |
| Push notification | gắn với phiên (**bug**) | bảng riêng, sống độc lập |
| Phức tạp phía FE | interceptor tạm giữ request, chống refresh đồng thời, chạy lại | gắn 1 header, hết |

---

## 4. Tác động phía Flutter

Trước:

```dart
// lưu 2 token, interceptor bắt 401 → gọi /refresh → chống refresh đồng thời
// → lưu cặp token mới → chạy lại request bị giữ
```

Sau:

```dart
// lưu 1 credential, gắn header Authorization: Bearer <session_id>
// 401 → đăng nhập lại. 503 → thử lại sau (ĐỪNG xóa credential).
```

Toàn bộ bộ làm mới phiên đã được gỡ; `flutter test` và `flutter analyze` xanh sau
khi gỡ.

⚠️ **FE phải phân biệt 401 với 503.** Xử lý 503 như 401 sẽ làm mất đi đúng cái lợi
ích mà mục 2.10 tạo ra.

---

## 5. Vì sao đổi là đáng làm

Rút lại thành một câu:

> Hệ cũ đã **trả** cái giá của opaque token (một lượt tra kho mỗi request) mà vẫn
> **gánh** toàn bộ sự phức tạp của JWT (secret, ngắn hạn, refresh, rotation, reuse
> detection, hai đường xác thực). Nó ở giữa hai họ giải pháp và nhận phần dở của
> cả hai.

Khi đã chấp nhận tra kho mỗi request — mà hệ cũ **đã** chấp nhận — thì JWT không
còn mua được gì cả. Bỏ nó đi thì:

- **Xóa** được: `/refresh`, `session_refresh_tokens`, rotation, reuse detection,
  `JWT_SECRET_KEY`, `TokenAuth`, và toàn bộ interceptor làm mới ở FE.
- **Thêm** được: thu hồi tức thì trên mọi đường, trần tuyệt đối 30 ngày, phân biệt
  401/503, push notification không chết cùng phiên.
- **Nhanh hơn**: đọc key Redis thay cho `JOIN` Postgres.

Cái giá thật phải trả là **Redis trở thành SPOF** và **`role` bị cache**. Khoản
thứ nhất đã được trả bằng `noeviction`, AOF, fail-fast, readiness probe, split
401/503 và backstop River. Khoản thứ hai **chưa được trả** — xem mục 6.

---

## 6. Rủi ro còn lại

1. **`role` cache mà không có gì canh.** Nếu ai đó thêm endpoint đổi role mà quên
   thu hồi phiên, người bị hạ quyền giữ quyền cũ tới 7 ngày. Cần một test tường
   minh cho bất biến này.
2. **`redis_eviction_policy_unverified` chỉ có log, không có counter.** Managed
   Redis chặn `CONFIG GET` → app boot bình thường, và không ai phân biệt được "đã
   kiểm, ổn" với "không kiểm được".
3. **`active_sessions_count` vẫn đếm từ Postgres** — đang đếm một bảng không còn là
   nguồn phán quyết, nên con số có thể lệch thực tế.
4. **Spec 0012 ở trạng thái `Assumed`**, chưa qua `/architect`. Job thu hồi vô điều
   kiện ở đường khóa tài khoản dựa vào bất biến "tài khoản không `active` thì không
   tạo được phiên" — bất biến này đúng trong `CreateSession` hiện tại nhưng **chưa
   được test tường minh**.
5. **`make test` không nạp `.env`**, nên mọi `*_integration_test.go` skip im lặng.
   Một lượt `make test` xanh **không** chứng minh gì về Redis. Chạy:
   `set -a; . ./.env; set +a; go test -count=1 ./...`

---

## 7. Đọc code theo thứ tự nào

1. `internal/platform/session/session.go` — hai khái niệm credential vs SID
2. `internal/platform/session/scripts.go` — 5 Lua script, phần lõi
3. `internal/platform/session/store.go` — bọc script thành API Go
4. `internal/transport/http/middleware/auth.go` — đường xác thực, 401 vs 503
5. `internal/modules/auth/usecase/service.go` (`SignIn`, `SignOut`) — vòng đời
6. `internal/modules/auth/jobs/session_purge.go` — backstop
7. `internal/platform/redis/client.go` — fail-fast, `noeviction`
8. `internal/config/config.go` (`Validate`) — các bất biến cấu hình
9. `db/migrations/000018`, `000019` — dọn dẹp schema

Tài liệu liên quan: `docs/specs/0011-redis-session-auth/` (index, plan, rationale,
verify + 3 child spec), `docs/specs/0012-lock-path-unconditional-purge.md`,
`docs/reviews/2026-09-10-lampt-auth-account-v2.md`.
