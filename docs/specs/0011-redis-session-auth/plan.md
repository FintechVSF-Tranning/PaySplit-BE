# Chuyển Auth sang Session ID đục + Redis

## Context

PaySplit-BE hiện dùng JWT access token (HS256, 15 phút, mang claim `sid`) kèm refresh token
rotation có reuse detection. Nhưng `internal/transport/http/middleware/auth.go:55` vẫn gọi
`ValidateSession` query Postgres trên **mọi** request — hệ thống đang trả chi phí round-trip của
session-based auth trong khi vẫn mang toàn bộ độ phức tạp của JWT, mà **chưa bao giờ hưởng lợi ích
stateless**.

Mục tiêu: bỏ JWT và refresh rotation, thay bằng session ID đục lưu trên Redis.

Kết quả mong đợi:
- Xoá `internal/platform/auth/jwt/` (124 dòng), `Service.Refresh`, `RotateRefresh`,
  bảng `session_refresh_tokens`, endpoint `POST /auth/refresh`, dependency `golang-jwt/jwt/v5`.
- Xoá `session_refresher.dart` và nhánh 401→refresh→retry ở FE (~200 dòng), loại bỏ lớp bug
  "hai luồng refresh song song bị coi là reuse rồi đá user ra" (comment `session_refresher.dart:11-16`).
- FE giữ 1 credential thay vì 2; không còn HTTP call phụ mỗi 15 phút.

### Quyết định đã chốt với người dùng

| Câu hỏi | Chốt |
|---|---|
| Đã có user production? | **Chưa** — cắt thẳng, không cần middleware dual-mode |
| Bảng `sessions` | **Giữ**, hạ vai trò xuống bản ghi audit; Redis là nguồn phán quyết |
| Tách `device_tokens` | **PR riêng, làm TRƯỚC** (Giai đoạn 0) |
| Multi-device | **Giữ** 1 phiên/user (`uq_sessions_one_active_per_user`) |

---

## Hai quyết định kiến trúc then chốt

### 1. Tách *credential* khỏi *định danh phiên*

Tầng realtime ràng buộc session ID phải là UUID:
- `realtime.UserEnvelope.TargetSIDs` là `[]uuid.UUID` — `internal/platform/realtime/envelope.go:79`
- `EncodeSessionEnded(sids []uuid.UUID)` — `envelope.go:305`
- `uuid.Parse(sessionIDRaw)` — `internal/modules/auth/delivery/http/sse_handler.go:93`

> **`sessions.id` (uuidv7) vẫn là định danh phiên nội bộ.** Chuỗi đục chỉ là *credential*.
> Middleware đọc field `sid` **trong hash Redis** rồi đặt vào context — không bao giờ đặt token.

Nhờ vậy **không phải sửa dòng nào** ở `internal/platform/realtime/*`, `sse_hub.go`,
`sse_handler.go`, và toàn bộ đường `pg_notify` → SSE force-logout.

### 2. ⚠️ Một tuyến phòng thủ đang bị gỡ mất — phải bù lại

`ValidateSession` (`repository.go:357`) hiện JOIN `users` với **`AND u.status='active'`** trên mọi
request. Nghĩa là hôm nay, admin khoá tài khoản là request kế tiếp chết ngay, **kể cả khi lệnh
`UPDATE sessions` không khớp hàng nào**.

Sau khi đổi, middleware chỉ đọc Redis và **không bao giờ chạm `users.status`** nữa. Lệnh `DEL`
Redis trở thành **cơ chế cưỡng chế duy nhất** cho việc khoá tài khoản. Một lần ghi Redis hỏng ⇒
user bị ban vẫn dùng app bình thường suốt 7 ngày.

⇒ Bắt buộc có backstop bền vững, xem "River purge job" bên dưới. Đây không phải tối ưu, đây là
điều kiện để không tạo hồi quy bảo mật.

---

## Thiết kế Redis

### Sinh credential
Dùng lại `domain.NewOpaqueToken()` (`internal/modules/auth/domain/token.go:11`) — CSPRNG 32 byte
→ base64url + SHA-256, và `domain.HashToken()` (`:35`). **Không** dùng `uuidv7()` làm credential.

### Sơ đồ key
```
session:<sha256_hex>     HASH  { sid, user_id, role, absolute_exp }
                         TTL   = SESSION_IDLE_TTL   (trượt, gia hạn mỗi request)

user_session:<user_id>   STRING = <sha256_hex>       ← chỉ mục ngược, dùng để thu hồi theo user
                         TTL    = SESSION_ABSOLUTE_TTL
```

- Key là **hash** của token, không phải token trần → dump Redis không đăng nhập lại được.
- `absolute_exp` là **field trong hash**, không phải TTL. TTL trượt vô hạn sẽ khiến phiên sống mãi;
  middleware phải so `now > absolute_exp` và từ chối kể cả khi key còn sống.
- Hash cache `role`. **Bất biến cần ghi nhớ**: mọi thay đổi `users.role` hoặc `users.status` phải
  kéo theo thu hồi phiên. Hôm nay chỉ `status` bị đổi (admin), nhưng ghi chú cạnh schema.

### Ba Lua script (bắt buộc atomic)

**`create.lua`** — tạo phiên mới và **giết phiên cũ**, một round-trip:
```
KEYS[1]=user_session:<uid>
ARGV  = new_hash, sid, user_id, role, absolute_exp, idle_ttl, abs_ttl
  old = GETSET KEYS[1] new_hash      -- lấy hash cũ và ghi đè trong một lệnh
  if old and old ~= new_hash then DEL 'session:'..old end
  HSET 'session:'..new_hash sid ... ; EXPIRE 'session:'..new_hash idle_ttl
  EXPIRE KEYS[1] abs_ttl
```
> **Bug phải tránh**: chỉ ghi đè `user_session:<uid>` mà không `DEL session:<old_hash>` sẽ để lại
> một HASH mồ côi giữ nguyên TTL riêng — thiết bị cũ tiếp tục đăng nhập được tới 7 ngày, và
> **không chỉ mục ngược nào trỏ tới nó nữa** nên không cơ chế dọn nào tìm ra.

**`get.lua`** — validate + gia hạn TTL trượt trong **1 round-trip**:
```
KEYS[1]=session:<hash>   ARGV[1]=idle_ttl
  d = HGETALL KEYS[1]; if #d == 0 then return nil end
  EXPIRE KEYS[1] idle_ttl
  return d
```
`HGETALL` rồi `EXPIRE` rời nhau tốn 2 RTT/request; gộp Lua giữ đúng 1 RTT — bằng số RTT Postgres
hiện tại, không hồi quy hiệu năng.

**`revoke_user.lua`** — thu hồi theo user, idempotent, khớp `sid` để không giết nhầm phiên mới:
```
KEYS[1]=user_session:<uid>   ARGV=danh sách sid bị thu hồi
  h = GET KEYS[1]; if not h then return 0 end
  sid = HGET 'session:'..h 'sid'
  if sid nằm trong ARGV then DEL 'session:'..h; DEL KEYS[1]; return 1 end
  return 0
```
Khớp theo `sid` (đã có sẵn trong hash) giúp job chạy trễ **không xoá nhầm phiên mới** mà user vừa
tạo sau đó — và nhờ vậy **không cần thêm cột `sessions.token_hash`**.

---

## Thứ tự ghi Redis vs Postgres — quyết theo từng site, KHÔNG đồng nhất

| # | Site | Thứ tự | Nếu crash giữa chừng | Hướng hỏng |
|---|---|---|---|---|
| 1 | `RevokeSession` sign-out `repository.go:367` | **Redis → PG** | Hàng audit còn `revoked_at IS NULL`; tự lành ở lần `CreateSession` sau | **đóng** |
| 2 | `ResetPassword` `repository.go:448` | **PG → Redis** | Phiên cũ còn sống sau khi đổi mật khẩu | mở → cần job |
| 3 | `ChangePassword` `repository.go:477` | **không ghi Redis** | — | — |
| 4 | `CreateSession` `repository.go:252` | **PG → Redis** (Lua replace) | Thiết bị cũ còn sống | mở → cần job |
| 5 | Admin suspend `admin/…/repository.go:292` | **PG → Redis** | User bị ban vẫn dùng được ≤7 ngày | mở → **job bắt buộc** |

**Vì sao site 2 KHÔNG được Redis-trước** — đây là điểm phản trực giác nhất:
`repository.go:426-428` và `:436-437` **commit transaction rồi mới trả `ErrInvalidOrExpiredToken`**
khi OTP sai hoặc hết lượt thử. Nếu xoá Redis trước khi biết OTP có đúng không, **bất kỳ ai biết
email của nạn nhân đều có thể gửi OTP sai để đá họ ra khỏi app** — DoS không cần xác thực.

**Vì sao site 3 không ghi Redis**: `UPDATE ... WHERE user_id=$1 AND id<>$2` kết hợp
`uq_sessions_one_active_per_user` ⇒ `changedIDs` **luôn rỗng**, "thu hồi các phiên khác" đã là code
chết từ trước. Nếu cài Redis theo kiểu chung "GET `user_session:<uid>` → DEL", site này sẽ
**đá chính người vừa đổi mật khẩu ra** — ngược hẳn ý định `id<>$2`.

**Site 1 — rò rỉ cần bù**: NOTIFY nằm trong tx (`realtime.go:36-38`). Redis-trước mà tx sau đó hỏng
thì không control nào phát ra, SSE đang mở tiếp tục chạy tới `maxConnectionAge`. Bù bằng cách đọc
`sid` **trước** khi DEL, và nếu tx hỏng thì publish `session.ended` bù trên pool ngoài tx.

### River purge job — backstop bền vững

`internal/platform/queue/river/client.go:42` đã dựng `*river.Client[pgx.Tx]`, và `InsertTx` đã có
test (`client_integration_test.go:110`). **Không cần bảng outbox mới**: enqueue job ngay trong
chính tx thu hồi, nên nó chỉ tồn tại khi commit thành công — cùng ngữ nghĩa `pg_notify` đang dựa vào.

- Job `session_redis_purge{user_id, sids []uuid}` → chạy `revoke_user.lua`. Idempotent, an toàn khi
  chạy trễ.
- **Bắt buộc** ở site 5 (admin) — vì lý do ở phần "tuyến phòng thủ bị gỡ". Nên có ở site 2 và 4.
- Retry dưới 1 phút, **không** dùng nhịp `AUTH_CLEANUP_INTERVAL_HOURS` (24h, `config.go:284`).
- Site 5: nếu DEL inline hỏng thì trả **500** cho admin để lỗi hiện ra — trạng thái PG đã bền,
  job sẽ hội tụ.

---

## Giai đoạn thực thi

### ✅ Giai đoạn 0 — Tách `device_tokens` (PR riêng, không dính Redis) — ĐÃ XONG

Sửa dứt điểm bug: `GetActiveFCMTokenByUserID`
(`internal/modules/notification/repository/postgres/queries/notification.sql:38-43`) lọc
`expires_at > now()`, nên **không gửi được nhắc nợ cho đúng nhóm user cần nhắc nhất** — người lâu
không mở app. Phải làm **trước** giai đoạn 3, nếu không TTL trượt sẽ khiến bug lộ ra nặng hơn.

1. Migration goose `db/migrations/000018_device_tokens_v1.sql` (1 file, `-- +goose Up`/`Down`):
   `device_tokens(user_id, device_id, fcm_token, updated_at)`, `UNIQUE (user_id, device_id)`,
   FK `user_id → users(id) ON DELETE CASCADE`; backfill từ `sessions`.
2. `notification.sql`: hai query trỏ sang `device_tokens`, bỏ mọi điều kiện liveness. `make sqlc`.
3. `Service.UpdateFCMToken` (`auth/usecase/service.go:445`) và `UpdateSessionFCMToken`
   (`repository.go:741`) ghi theo `(user_id, device_id)`. `device_id` FE đã gửi sẵn lúc sign-in.
4. Bỏ `fcm_token` khỏi `INSERT INTO sessions` (`repository.go:260`) và khỏi `domain.Session`.

**Đã kiểm chứng trên DB thật** (gieo dữ liệu phủ 3 ca rồi chạy migration):
cùng `(user, device)` nhiều phiên → giữ token phiên mới nhất; phiên **đã hết hạn** → token vẫn
được backfill (chính là bug được sửa); rollback `migrate-down` trả token về `sessions` không mất mát.
Đối chiếu trực tiếp: với user idle 8 ngày, query cũ trả rỗng, query mới trả đúng token.

**Hai điều chỉnh so với thiết kế ban đầu, phát hiện khi chạy thật:**
- Bản đầu tạo `UNIQUE INDEX` trên `fcm_token` **trước** backfill nên migration đổ khi dữ liệu có
  token dùng chung; và câu `DELETE` khử trùng lặp dựa vào `created_at DEFAULT now()` — vốn giống hệt
  nhau trong cùng transaction nên không bao giờ khớp.
- Bỏ hẳn unique index trên `fcm_token`. `TestPostgresRepository_ClearFCMToken_ScopedToUser` ghim một
  quyết định có chủ đích: hai bản cài **được phép** dùng chung một token, và ràng buộc đúng là giới
  hạn `ClearFCMToken` theo user. Thêm index đó sẽ âm thầm đảo ngược quyết định ấy — nằm ngoài phạm vi
  "chuyển cột sang bảng riêng".

**Còn lại**: cột `sessions.fcm_token` được **giữ nguyên** (không còn ai đọc/ghi) để rollback an toàn.
Gỡ hẳn ở migration sau khi giai đoạn 0 chạy ổn định.

---

### ✅ Giai đoạn 1 — Hạ tầng Redis (chưa đụng auth) — ĐÃ XONG

5. `docker-compose.yaml`: service `redis` (Phụ lục A).
6. `internal/config/config.go`: `RedisConfig` cạnh `RiverConfig` (~`:137`), field trong `Config`
   (~`:26`), parse trong `Load()` bằng helper sẵn có (`stringEnv`, `intEnv`,
   `durationEnv(name, fallbackInt, unit)`), block trong `Validate()` theo khuôn
   `if <bad> { return errors.New("REDIS_URL must be ...") }`.
7. `internal/platform/redis/client.go`: `New(ctx, cfg) (*Client, error)` theo khuôn
   `database.NewPostgresPool` (`platform/database/postgres.go:45-62`) — **ping ngay để fail fast**,
   đóng client rồi trả lỗi nếu ping hỏng. Có `Close() error` và `Ping(ctx) error` (bọc
   `*redis.StatusCmd` về `error`; `go-redis` không tự thoả `DBPingChecker`).
8. `router.go`: readiness hiện chỉ nhận **một** `DBPingChecker` (`:21-23`, `healthReady` `:90-110`).
   Mở rộng thành danh sách checker có tên → `{"status":"degraded","database":"ok","redis":"down"}`.
   Cập nhật `router_test.go:34-80`.
9. `bootstrap/app.go`: dựng Redis ngay sau pgx pool (~`:94`); field trong `App` (`:63-73`); đóng
   trong goroutine đóng pool ở `Shutdown` (`:426-435`) + `wrapShutdownError("Redis client", err)`
   vào `errors.Join` (`:442-448`).
   ⚠️ `New()` **không có closer list** — mọi điểm `return nil, err` **sau** bước dựng Redis
   (`:99, 107, 114, 121, 139, 154, 175, 180, 227`) phải thêm `redisClient.Close()`.
10. `.env.example` + `.env`: nhóm `# Redis session store`. `go get github.com/redis/go-redis/v9`.

**Đã kiểm chứng trên hạ tầng thật**: `/health/ready` trả `{"database":"ok","redis":"ok"}` 200;
`docker compose stop redis` → `{"database":"ok","redis":"down","status":"degraded"}` 503; khởi động
lại Redis thì probe tự về 200 sau 1s **mà không cần restart app**. Fail-fast đúng ba ca: URL sai
cổng, URL trống, và `IDLE > ABSOLUTE` đều dừng ngay lúc bootstrap. Cấu hình sống đã xác nhận
`maxmemory-policy=noeviction`, `appendonly=yes`, `appendfsync=everysec`, và Redis từ chối kết nối
không mật khẩu.

**Hai điều chỉnh so với thiết kế ban đầu:**
- `router.New` nhận `...Dependency{Name, Checker}` thay vì một `DBPingChecker` đơn: readiness giờ
  probe **hết** mọi dependency và gọi đúng tên cái hỏng. "degraded" trống rỗng không cho người trực
  biết nên nhìn Postgres hay Redis, và dừng ở lỗi đầu tiên sẽ giấu mất lỗi thứ hai.
- Không sửa 9 điểm `return nil, err` trong `New()` như plan dự tính. Dùng một `defer` với cờ
  `redisHandedOver` — chạy trên mọi đường thoát sớm kể cả nhánh mới thêm sau này, nên không thể sót.
  `Shutdown` trả kết quả đóng Redis qua channel chứ không qua biến dùng chung, vì nhánh `ctx.Done()`
  chạy song song với goroutine đóng pool (đã xác nhận bằng `go test -race`).

**Bổ sung ngoài plan**: hai bất biến TTL được ghim bằng test trong `config_test.go` —
`IDLE <= ABSOLUTE`, và `AUTH_RECORD_RETENTION_DAYS >= ABSOLUTE - IDLE` (bất biến ngầm mà plan chỉ
ghi trong bảng Rủi ro; giờ `Validate()` chặn thẳng thay vì để nó vỡ sau nhiều tuần chạy).

---

### ✅ Giai đoạn 2 — Session store (thuần, có test, chưa ai gọi) — ĐÃ XONG

11. `internal/platform/session/` — interface + adapter `redis`:
```go
type Session struct { SID, UserID, Role string; AbsoluteExp time.Time }

type Store interface {
    Create(ctx, raw string, s Session) error            // create.lua
    Get(ctx, raw string, now time.Time) (*Session, error) // get.lua
    PeekAndDelete(ctx, raw string) (*Session, error)    // sign-out: trả phiên vừa xoá, nil nếu miss
    RevokeUserSIDs(ctx, userID string, sids []uuid.UUID) error // revoke_user.lua
}
```
    Băm bằng `domain.HashToken` — dùng lại, không viết hàm băm mới.
    `Get` trả `domain.ErrSessionRevoked` khi miss **hoặc** khi `now > AbsoluteExp`.
12. Ba Lua script ở trên, nhúng bằng `redis.NewScript`.
13. Test:
    - Unit với `miniredis` (`github.com/alicebob/miniredis/v2`).
    - `store_integration_test.go` theo **house style**: env var + `t.Skip`, **không build tag**,
      tên file `*_integration_test.go`. Khuôn tốt nhất để chép:
      `internal/modules/settlement/repository/postgres/repository_integration_test.go:24-34`
      (có `t.Skipf` khi server không kết nối được). Dùng `TEST_REDIS_URL`; `Makefile:3-4` đã
      `include .env` + `export` nên biến tự chảy vào `make test`.
    - Ca bắt buộc phủ: TTL trượt được gia hạn; `absolute_exp` chặn dù key còn sống;
      **`Create` xoá sạch hash cũ, không để mồ côi**; `RevokeUserSIDs` không xoá nhầm phiên mới.

**Đã kiểm chứng**: 17 test xanh (11 unit qua `miniredis`, 6 integration trên Redis 8 thật),
`go test -race` sạch, và `grep` xác nhận chưa code production nào gọi store. Quan sát trực tiếp trên
Redis: credential `SIlAsJ…` (43 ký tự) client cầm, còn key trên Redis là SHA-256 của nó; TTL phiên
604800s, TTL con trỏ 2592000s.

**Bốn script, không phải ba như plan dự tính** — đăng xuất cần `peekAndDelete` riêng: nó phải đọc
`sid` và `user_id` *trước* khi xoá, vì bên gọi cần hai giá trị đó để ghi audit và phát `session.ended`.

**Ba điều chỉnh so với thiết kế ban đầu:**
- `getScript` cưỡng chế trần tuyệt đối **ngay trong Lua** và xoá key luôn, thay vì chỉ so
  `absolute_exp` ở Go. Cách cũ để bản ghi chết nằm lại chiếm bộ nhớ tới hết TTL trượt.
- TTL trượt được **kẹp** vào phần thời gian tuyệt đối còn lại. Không kẹp thì mỗi request lại đẩy hạn
  ra 7 ngày và key sống lâu hơn chính mốc `absolute_exp` của nó.
- Con trỏ `user_session:<uid>` mang **TTL tuyệt đối**, không dùng chung TTL với bản ghi phiên (bản
  đầu tôi viết dùng chung, sai). Bản ghi phiên trượt nên sống quá 7 ngày; con trỏ hết hạn trước sẽ
  làm mất khả năng thu hồi theo user — tức đổi mật khẩu và admin khoá tài khoản im lặng không có tác dụng.
- `revokeUserSIDs` khớp theo `sid` chứ không xoá vô điều kiện, để River purge job chạy trễ không giết
  nhầm phiên user vừa tạo lại.

**Ghi chú layering**: gói dùng sentinel `session.ErrNotFound` riêng thay vì `domain.ErrSessionRevoked`;
việc dịch sang ngôn ngữ nghiệp vụ thuộc về tầng gọi ở Giai đoạn 3.

---

### ✅ Giai đoạn 3 — Chuyển auth sang session store — ĐÃ XONG

**Nơi đặt lời gọi Redis: tầng `usecase` điều phối** cả `repository` (Postgres, audit) lẫn `store`
(Redis, phán quyết). Store là một port khai báo trong `usecase`, đúng khuôn `TokenIssuer`/
`PasswordManager` hiện có.
Repo `repository/postgres` **không** được biết tới Redis — và tuyệt đối không lặp lại kiểu
`SetRealtimePublisher` (`realtime.go:13-17`), vốn đã lén nhét một mối quan tâm ngoài-Postgres vào
repo bằng type-assertion setter.

14. `usecase/service.go`: bỏ port `TokenIssuer` (`:27`), thêm `SessionStore`; đổi `NewService`
    (`:64`). `Options`: thêm `IdleTTL`, `AbsoluteTTL`.
15. `SignIn` (`:190-241`): giữ `repo.CreateSession` (vẫn cần hàng audit + bất biến 1-phiên/user +
    `replaced_by_sign_in` + NOTIFY trong tx). **Commit PG trước**, rồi `domain.NewOpaqueToken()` +
    `store.Create(...)` với `SID = session.ID`. Nhánh lỗi `access_issue_failed` (`:237`) giữ, đổi
    lý do thành `"session_store_failed"`.
    **`expires_at` của hàng audit đặt = `now + AbsoluteTTL` (30 ngày)**, không phải IdleTTL —
    xem "Vì sao" ở phần Rủi ro.
16. **Xoá** `Service.Refresh` (`:243-264`), `TokenOutput.RefreshToken/RefreshExpiresAt`.
17. `SignOut` (`:266`): nhận **raw token**. `store.PeekAndDelete` trước → lấy `sid`+`user_id` →
    `repo.RevokeSession` ghi audit + NOTIFY trong tx. Tx hỏng ⇒ publish `session.ended` bù ngoài tx.
    **Miss ở Redis ⇒ trả `nil` (204), không chạm DB** — không có `user_id` để ghi, và tránh tạo
    "unauthenticated write amplifier".
18. `ResetPassword` (`:270`): gọi `repo` **trước** (vì lý do DoS ở trên), rồi `store.RevokeUserSIDs`.
    `ChangePassword` (`:285`): **không** ghi Redis.
19. `middleware/auth.go`: xoá `TokenVerifier` (`:22`), `SessionValidator` (`:26`), `TokenAuth`
    (`:34`), nhánh `requireLive` (`:37-66`). `Auth(store)` chỉ còn hash → `get.lua`.
    - **Giữ nguyên cả ba context key** (`:60-63`); `sessionIDContextKey` phải nhận **field `sid`
      trong hash**, không phải token — `sse_handler.go:93` `uuid.Parse` nó.
    - **Export `bearerToken`** (`:110-117`, đang unexported) thành `middleware.BearerToken` để
      handler sign-out dùng chung. Hai bộ parse bearer khác nhau là lỗi auth kinh điển.
    - `WithAuthContext` (`:87-92`) dùng trong test — cập nhật theo.
20. `delivery/http`:
    - `routes.go:8,16` — `RegisterAuthRoutes(r)` bỏ tham số middleware; sign-out không middleware.
    - `handler.go:101 SignOut` — đọc bearer bằng `middleware.BearerToken`, luôn 204 (kể cả header
      thiếu/hỏng, để giữ idempotent cho client đã xoá token).
    - `handler.go:89 Refresh` + route `/auth/refresh` (`routes.go:13`) — **xoá**.
    - `response.go:23-30 tokenResponse` — thay `token_type`/`access_token`/
      `access_token_expires_at`/`refresh_token`/`refresh_token_expires_at` bằng
      `session_id` + `expires_at`.
    - `request.go:25 refreshRequest` — xoá.
21. **Admin** (`internal/modules/admin/`):
    - `repository.go:292-318` giữ tx nguyên (vẫn cần `RETURNING id` để NOTIFY), nhưng **trả thêm**
      danh sách sid đã thu hồi, và `InsertTx` job purge trong cùng tx.
      `UpdateAccountStatusWithRevocation` đổi từ 3 → 4 giá trị trả về; kéo theo
      `admin/repository/repository.go` và `usecase/service.go:172`.
    - `usecase/service.go:145-173`: sau khi repo commit, gọi `sessions.RevokeUserSIDs(...)`.
      Khai báo port một-method **trong `admin/usecase`** để admin không import `auth/usecase`.
      DEL hỏng ⇒ log + 500.
    - `CountActiveSessionsByUserID` (`admin.sql:56-59`): **trả lời từ Redis**
      (`EXISTS user_session:<uid>`) thay vì đếm Postgres — xem Rủi ro.
22. `bootstrap/app.go`: bỏ `jwt.NewAccessTokenManager` (`:97`); tiêm store vào auth service và admin
    service; `:304-305` chỉ còn `liveAuth := transportmw.Auth(sessionStore)`; bỏ `tokenAuth` ở `:307`.
23. Đăng ký worker `session_redis_purge` vào River workers registry (`app.go:163`).

**Đã kiểm chứng end-to-end trên app thật** (Postgres + Redis thật, không mock):

| Ca | Kết quả |
|---|---|
| Đăng nhập | Trả `session_id` 43 ký tự + `expires_at` (30 ngày = trần tuyệt đối) |
| Credential trên Redis | Key là SHA-256; chuỗi client cầm **không xuất hiện** ở bất kỳ key/value nào |
| TTL trượt | 604784 → **604800** ngay sau một lần gọi API |
| `POST /auth/refresh` | **404** — endpoint đã biến mất |
| Đăng nhập lại | Credential cũ 401, số key `session:*` vẫn **đúng 1** (không mồ côi) |
| Đăng xuất | 204; lần hai bằng credential đã chết vẫn **204**; không header cũng **204** |
| SSE force-logout | Stream đang mở nhận `event: close {"reason":"session_ended"}` — `pg_notify` còn nguyên |
| Admin khoá tài khoản | Phiên nạn nhân chết ngay (401) |
| **River purge backstop** | Đẩy job vào hàng đợi → phiên bị giết sau **2 giây**, kèm log `session_purge_recovered` |
| **DoS qua reset-password** | 3 lần OTP sai → phiên nạn nhân **vẫn 200**; OTP đúng → **401** |

**Bốn phát hiện khi thực thi:**

1. **`ResetPassword` phải trả cả `userID`, không chỉ danh sách SID.** Bản đầu tôi chỉ trả SID rồi
   gọi `RevokeUserSIDs(ctx, "", sids)` — chỉ mục ngược khoá theo user nên userID rỗng khiến hàm trả
   về ngay mà **không thu hồi gì**. Thu hồi phiên sẽ im lặng không hoạt động.

2. **`r.sessionPurge != nil` phải kiểm trên INTERFACE.** `EnqueueTx` đã guard con trỏ nil bên trong,
   nhưng repo dựng qua `New()` có trường interface nil, và gọi method trên interface nil thì panic —
   test admin nổ SIGSEGV. Bug này sẽ phát nổ trong production nếu bootstrap quên nối dây.

3. **Không kiểm chứng được backstop bằng cách pause Redis.** Redis chết thì chính admin cũng không
   xác thực được, request 401 ngay ở middleware và transaction không bao giờ chạy. Cửa sổ lỗi thật
   hẹp hơn tôi tưởng: Redis phải sống lúc middleware chạy rồi chết đúng lúc `DEL`. Đã kiểm chứng
   bằng cách đẩy thẳng job vào `river_job`.

4. **`SessionTTL` của hàng audit đổi từ `cfg.Auth.RefreshTokenTTL` (7 ngày) sang
   `cfg.Redis.AbsoluteTTL` (30 ngày)**, đúng như phần Rủi ro đã nêu — nếu để 7 ngày, worker dọn rác
   sẽ xoá bản ghi audit của phiên vẫn đang sống.

**Còn nợ sang Giai đoạn 4**: bảng `session_refresh_tokens` vẫn tồn tại (chưa ai ghi vào),
`cfg.Auth.JWTSecret`/`RefreshTokenTTL` vẫn nằm trong config, và `docs/openapi.yaml` chưa cập nhật.
`golang-jwt/jwt/v5` **đã** biến mất khỏi `go.mod`.

---

### ✅ Giai đoạn 4 — Dọn code cũ — ĐÃ XONG

24. Xoá `internal/platform/auth/jwt/` (2 file) + `access_token_manager_test.go`.
25. Xoá `RotateRefresh`, `RotateRefreshResult`, `RefreshTokenHash` khỏi
    `repository/repository.go:33-56` và `repository/postgres/repository.go:279-352`.
26. Migration `000019_drop_session_refresh_tokens.sql`. Gỡ `RevokeRefreshTokensByUserID`
    (`admin.sql:125`) và mọi `UPDATE session_refresh_tokens` trong auth repo.
    Kiểm tra `RevokeSessionsByUserID` (`sqlc/admin.sql.go:518`) — **sinh ra nhưng không ai gọi**
    (raw SQL ở `:294` thay thế nó); xoá khỏi `admin.sql`.
27. `CleanupExpiredAuth` (`repository.go:650-682`): **giữ** — hàng `sessions` vẫn là audit và vẫn
    cần dọn. `cleanupMedia` không đụng tới. Nếu bỏ `CleanupExpiredAuth` thì phải sửa
    `jobs/workers.go:13` và validation trong `New` (`:28`).
28. `go mod tidy` bỏ `golang-jwt/jwt/v5`.
29. `docs/openapi.yaml`: bỏ `/auth/refresh`, đổi schema response `sign-in`.
    **Làm trước khi sửa FE**, theo quy ước AGENTS.md mục 3.

---

**Đã kiểm chứng trên hạ tầng thật** (Postgres + Redis, không mock):

| Ca | Kết quả |
|---|---|
| Migration 000019 tiến | `session_refresh_tokens` xoá, `sessions.fcm_token` xoá |
| Migration 000019 lùi | Cả hai được dựng lại đối xứng — kiểm bằng `migrate-down` rồi `up` lại |
| App khởi động không `JWT_SECRET_KEY` | Lên bình thường, không lỗi cấu hình |
| Response `sign-in` đối chiếu OpenAPI | `{expires_at, session_id, token_type, user}` khớp 100% với schema `Session`/`SignInResponse` mới |
| `/auth/refresh` | Đã xoá khỏi cả API lẫn spec |
| `sign-out` không header / bearer rác / bearer đã chết | Cả ba đều 204, đúng thiết kế idempotent |
| Đăng nhập → đổi mật khẩu → đăng nhập lại → reset password | Toàn bộ luồng vẫn chạy đúng sau khi bảng bị drop |
| `CleanupExpiredAuth` sau khi drop bảng | Chạy sạch, không lỗi `relation does not exist` (gọi trực tiếp qua test, vì chu kỳ worker tính bằng giờ không tiện chờ) |
| `golang-jwt/jwt/v5` | Biến mất khỏi `go.mod`. Dòng `jwt/v4` còn lại là dependency gián tiếp của Firebase SDK (`internal/platform/notification/fcm`), không liên quan tới auth |

**Một phát hiện ngoài dự tính**: `AGENTS.md` mục Security mô tả sai từ trước (nói phiên lưu ở bảng
`user_sessions`, thực tế là `sessions`) và giờ mô tả sai nặng hơn nếu không sửa — đã viết lại toàn bộ
mục đó theo đúng cơ chế hiện tại, kèm liên kết tới các file thật (`internal/platform/session`,
`internal/modules/auth/jobs/session_purge.go`). Đây là tài liệu mọi AI agent sau này đọc đầu tiên,
nên để nó sai sẽ khiến agent kế tiếp đề xuất "sửa" JWT không còn tồn tại.

---

### ✅ Giai đoạn 5 — Frontend (phát hành **cùng lúc** với BE) — ĐÃ XONG

`AuthResponseModel.accessToken/refreshToken` là `required` non-nullable
(`auth_response_model.dart:11-12`) ⇒ FE **vỡ cứng ngay lần sign-in đầu tiên** nếu BE lên trước.

30. `storage_keys.dart:3-4` → một key `sessionId`. `token_storage.dart` → `sessionId` getter,
    `saveSession()`, `clear()`. `getOrCreateDeviceId()` giữ nguyên.
31. `auth_response_model.dart:11-12` → `@JsonKey(name:'session_id')` + `expires_at`.
32. Xoá `session_refresher.dart`; chuyển `endSession()` (phần duy nhất còn giá trị) sang helper nhỏ.
    `session_events.dart` giữ nguyên — nó không phụ thuộc transport.
33. `auth_interceptor.dart` (122 → ~40 dòng): xoá `_skipRefreshPaths`, `_retriedFlag`, `_retry`,
    toàn bộ nhánh refresh. Còn: gắn header, 401 ⇒ `endSession()`.
34. `sse_transport.dart:86` bỏ `await refresher.refresh()` giữa stream;
    `realtime_ports.dart:26-27` bỏ `realtimeSessionRefresherProvider`;
    `user_realtime_owner.dart:183,220` chuyển sang `endSession` mới.
35. `dio_client.dart:13-17` bỏ provider `sessionRefresher`; `api_endpoints.dart:10` xoá
    `refreshToken`; `auth_repository_impl.dart:43-46` lưu `session_id`.
36. `dart run build_runner build --delete-conflicting-outputs`.
37. Sửa test: `test/core/network/auth_interceptor_test.dart`,
    `test/core/realtime/sse_transport_test.dart`, `test/core/realtime/user_realtime_owner_test.dart`
    — cả ba dựng `SessionRefresher` thật và assert envelope refresh.

---

## Files quan trọng

**Backend** — `internal/platform/redis/client.go` *(mới)*, `internal/platform/session/` *(mới)*,
`internal/config/config.go`, `internal/bootstrap/app.go`, `internal/transport/http/router/router.go`,
`internal/transport/http/middleware/auth.go`, `internal/modules/auth/usecase/service.go`,
`internal/modules/auth/delivery/http/{routes,handler,request,response}.go`,
`internal/modules/auth/repository/postgres/repository.go`,
`internal/modules/admin/{usecase/service.go,repository/postgres/repository.go}`,
`db/migrations/000018_device_tokens_v1.sql`, `000019_drop_session_refresh_tokens.sql`,
`docs/openapi.yaml`, `docker-compose.yaml`, `.env.example`

**Frontend** — `lib/core/network/{token_storage,dio_client}.dart`,
`lib/core/network/interceptors/auth_interceptor.dart`,
`lib/core/realtime/{sse_transport,realtime_ports,user_realtime_owner}.dart`,
`lib/features/auth/data/{models/auth_response_model.dart,repositories/auth_repository_impl.dart}`,
`lib/core/constants/{api_endpoints,storage_keys}.dart`

**Tái sử dụng, không viết mới**: `domain.NewOpaqueToken()`/`domain.HashToken()`
(`auth/domain/token.go:11,35`), `database.NewPostgresPool` làm khuôn constructor
(`platform/database/postgres.go:45`), `fcm.New` làm khuôn adapter tuỳ chọn
(`platform/notification/fcm/client.go:41`), `river.Client.InsertTx` làm outbox
(`platform/queue/river/client.go:42`), `realtime.NotifySessionEnded` (`notify.go:64`) giữ nguyên.

`docs/specs/0011-redis-session-auth/plan.md` là bản nháp viết **trước** khảo sát, đã lỗi thời
(đề xuất bỏ bảng `sessions`, chưa biết ràng buộc UUID của realtime). Ghi đè bằng plan này.

---

## Verification

> ⚠️ **Trước khi bắt đầu**: `TEST_DATABASE_URL` có trong `.env.example` nhưng **không có trong
> `.env`**. Mọi `*_integration_test.go` đang bị `t.Skip` im lặng — `make test` xanh **không**
> nghĩa là chúng đã chạy. Thêm `TEST_DATABASE_URL` (rồi `TEST_REDIS_URL`) vào `.env`, chạy
> `make test` một lần để có mốc so sánh trước khi sửa gì.

**Tự động**
1. `make test`. Các test chắc chắn phải sửa:
   - `auth/usecase/service_test.go:46` (`mockTokenIssuer`), `:331` (`TestSignOut_RevokesSession`)
   - `auth/delivery/http/handler_integration_test.go:51` `TestAuthHTTPJourneyAndRefreshReplay` —
     viết lại thành journey không refresh; **giữ** assert single-device (`:112-120`)
   - `auth/repository/postgres/repository_integration_test.go:16` — bỏ phần `RotateRefresh`
   - `auth/repository/postgres/realtime_integration_test.go:163,211` — **phải vẫn xanh**. Đổi tên/
     comment cho đúng hợp đồng mới: "không phát control cho SID chưa từng sống trên Redis".
     Gate việc publish bù bằng *"đã thực sự xoá được một key Redis"*, không phải số hàng PG.
   - `group/delivery/http/handler_integration_test.go:35-46`,
     `notification/delivery/http/handler_integration_test.go:38` — fake verifier/sessions
   - `admin/delivery/http/handler_test.go:138,158` — `ActiveSessionsCount`
   - `platform/auth/jwt/access_token_manager_test.go` — xoá
2. `cd PaySplit-FE && flutter test && flutter analyze`

**Thủ công, chạy thật** (`docker compose up -d postgres redis && make run`)
3. Sign-in → nhận `session_id`; `GET /api/v1/users/me` bằng nó → 200.
4. `redis-cli -a … KEYS 'session:*'` thấy đúng 1 key; `TTL` ~604800. Gọi lại API sau vài giây →
   `TTL` được nạp lại (chứng minh TTL trượt).
5. Sign-out → key biến mất, request kế tiếp 401. Sign-out lần hai cùng token → **vẫn 204**.
6. **Kiểm mồ côi**: đăng nhập máy A, ghi lại hash. Đăng nhập lại (máy B) → `KEYS 'session:*'`
   phải còn **đúng 1** key. Token của A phải trả 401.
7. Mở SSE `/api/v1/users/me/events`; terminal khác đăng nhập lại cùng user → stream đầu nhận
   `event: close` với `{"reason":"session_ended"}` (chứng minh `pg_notify` còn nguyên).
8. **Kiểm hồi quy bảo mật**: admin `PUT /admin/accounts/{id}/status` → `suspended`. Phiên user chết
   ngay. Rồi mô phỏng Redis hỏng: `docker compose pause redis`, ban một user khác, `unpause` →
   River purge job phải hội tụ và giết phiên đó.
9. Đổi mật khẩu → phiên hiện tại **vẫn sống** (đúng ý `id<>$2`).
10. Reset password bằng OTP **sai** → phiên đang đăng nhập **không được** bị đá ra (kiểm DoS).
11. `docker compose stop redis` → `/health/ready` 503; `start` → 200.

---

## Phụ lục A — Setup Redis

### `docker-compose.yaml`
```yaml
  redis:
    image: redis:8-alpine
    command: >
      redis-server
      --requirepass ${REDIS_PASSWORD:-devpassword}
      --appendonly yes
      --appendfsync everysec
      --maxmemory 256mb
      --maxmemory-policy noeviction
    ports: ["6380:6379"]
    healthcheck:
      test: ["CMD-SHELL", "redis-cli -a ${REDIS_PASSWORD:-devpassword} ping | grep PONG"]
      interval: 5s
      timeout: 5s
      retries: 10
    volumes: [paysplit_redis_data:/data]

volumes:
  paysplit_redis_data:
```

| Cờ | Lý do |
|---|---|
| `--maxmemory-policy noeviction` | **Tuyệt đối không dùng `allkeys-lru`** — đó là policy cho *cache*. Với session store, LRU âm thầm xoá phiên của user ít hoạt động khi đầy RAM ⇒ user bị đăng xuất ngẫu nhiên, không log, không tái hiện được. `noeviction` cho lỗi + alert thay vì ticket bí ẩn. |
| `--appendonly yes --appendfsync everysec` | Không bật thì Redis restart = **mọi user đăng nhập lại**. `everysec`: xấu nhất mất 1 giây. |
| `--requirepass` | Redis mặc định không auth. Lộ Redis = lộ mọi phiên. |
| Port `6380` | Khớp quy ước sẵn có (Postgres đang map `5433:5432`). |

### Biến môi trường (`.env.example`, nhóm `# Redis session store`)
```dotenv
REDIS_URL=redis://:devpassword@localhost:6380/0
REDIS_PASSWORD=devpassword
REDIS_POOL_SIZE=20
REDIS_DIAL_TIMEOUT_SECONDS=5
REDIS_READ_TIMEOUT_SECONDS=2
SESSION_IDLE_TTL_HOURS=168      # 7 ngày, TTL trượt
SESSION_ABSOLUTE_TTL_HOURS=720  # 30 ngày, trần cứng
TEST_REDIS_URL=redis://:devpassword@localhost:6380/1
```
Đơn vị nằm trong tên biến — đúng quy ước `.env.example` hiện tại.

### Làm quen bằng `redis-cli`
```bash
docker compose up -d redis
docker compose exec redis redis-cli -a devpassword

> PING
> HSET session:abc sid "…" user_id "u-1" role "user"
> EXPIRE session:abc 604800
> HGETALL session:abc
> TTL session:abc          # còn bao nhiêu giây
> DEL session:abc          # đây chính là "đăng xuất"
> INFO memory              # theo dõi used_memory
> INFO persistence         # xác nhận AOF đang bật
```
- `SCAN 0 MATCH session:* COUNT 100` để duyệt key. **Không bao giờ `KEYS *`** trên production —
  Redis đơn luồng, lệnh đó block cả server.
- `MONITOR` xem realtime mọi lệnh; hữu ích khi debug, chỉ dùng lúc dev.

### Production
- Ưu tiên managed (Upstash / Redis Cloud / ElastiCache).
- Bắt buộc TLS (`rediss://`) nếu Redis không cùng private network với API.
- Alert `used_memory` ở ~70% `maxmemory`, và `rdb_last_bgsave_status`.

---

## Rủi ro

| Rủi ro | Biểu hiện | Giảm thiểu |
|---|---|---|
| **Mất tuyến `u.status='active'`** | User bị ban vẫn dùng app ≤7 ngày nếu DEL hỏng | River purge job **bắt buộc** ở admin; 500 khi DEL inline hỏng |
| Redis chết | 100% request 401 | Redis trong `/health/ready` (bước 8) |
| `maxmemory-policy` sai | User bị đăng xuất ngẫu nhiên, không tái hiện | `noeviction`, ghi rõ trong compose |
| **Hash mồ côi khi đăng nhập lại** | Thiết bị cũ còn sống 7 ngày, không chỉ mục nào trỏ tới | `create.lua` dùng `GETSET` + `DEL session:<old>` (bước 12) |
| `expires_at` đặt sai | `CleanupExpiredAuth` xoá hàng audit của phiên còn sống | Đặt `= now + AbsoluteTTL`. Nếu đặt IdleTTL sẽ tạo bất biến ngầm `AUTH_RECORD_RETENTION_DAYS ≥ 23` — hôm nay mặc định 30 nên an toàn, nhưng vỡ ngay khi ai đó chỉnh xuống |
| `active_sessions_count` sai lệch | Phiên idle-out ngày 8 vẫn báo "active" tới ngày 30 | Trả lời từ Redis (`EXISTS user_session:<uid>`) |
| Đổi `users.role` | Redis giữ role cũ tới hết TTL | Ghi bất biến cạnh schema hash; hiện chưa có luồng đổi role |
| Lộ session ID qua log | Chiếm tài khoản 7–30 ngày | `middleware/logging.go:34` hiện **không** log header nào — an toàn sẵn, giữ nguyên tính chất đó |
| `New()` không có closer list | Rò kết nối Redis khi bootstrap lỗi | Thêm `Close()` vào 9 điểm `return nil, err` (bước 9) |

**Ghi chú**: sau thay đổi này `uq_sessions_one_active_per_user` không còn nghĩa "một phiên *sống*"
mà là "một *hàng* chưa revoke". Phiên idle-out để lại `revoked_at IS NULL` cho tới khi
`CreateSession` quét. Vô hại với unique index, nhưng **đừng đọc index này như một bất biến về
liveness** ở bất kỳ đâu nữa. Và **không bao giờ ghi Postgres trên đường đọc của middleware** để
"sửa" độ lệch — làm vậy biến mọi request thành một lệnh ghi DB.

---

## Ước lượng

| Giai đoạn | Ước lượng |
|---|---|
| 0 — `device_tokens` (PR riêng) | 1 ngày |
| 1 — Hạ tầng Redis | 0.5 ngày |
| 2 — Session store + 3 Lua + test | 1 ngày |
| 3 — Chuyển auth + admin + purge job | 2 ngày |
| 4 — Dọn code cũ + openapi | 0.5 ngày |
| 5 — Frontend | 1 ngày |
| **Tổng** | **~6 ngày** |
