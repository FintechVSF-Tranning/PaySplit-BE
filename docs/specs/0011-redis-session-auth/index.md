# 0011. Redis session auth

**Date**: 2026-09-09
**Status**: Accepted

## Summary

PaySplit bỏ JWT access token và refresh token rotation, thay bằng một session ID đục (chuỗi ngẫu nhiên không mang thông tin, chỉ máy chủ giải nghĩa được) lưu trên Redis. Client giữ đúng một credential thay vì hai, và không còn endpoint làm mới token.

Hệ thống trước đây trả chi phí của session based auth (tra cơ sở dữ liệu trên mọi request) nhưng vẫn mang toàn bộ độ phức tạp của JWT, mà chưa bao giờ hưởng lợi ích stateless. Quyết định này gộp hai thứ đó lại thành một cơ chế duy nhất: Redis là nguồn phán quyết, thu hồi tức thì bằng một lệnh `DEL`.

Đánh đổi lớn nhất phải trả: middleware không còn đọc `users.status` nữa, nên một lệnh ghi Redis lỡ có thể để người bị khóa tài khoản tiếp tục dùng app. River purge job là điều kiện bắt buộc để bù lại tuyến phòng thủ đó, không phải một tối ưu hóa tùy chọn.

## Structure

1. [Opaque credential and session identity](0001-opaque-credential-identity.md) quy định vì sao credential và định danh phiên là hai thứ tách rời, nhờ đó tầng realtime không phải sửa dòng nào.
2. [Redis session store](0002-redis-session-store.md) quy định sơ đồ key, mô hình TTL hai tầng, và bốn Lua script bảo đảm tính nguyên tử.
3. [Revocation ordering and suspension backstop](0003-revocation-backstop.md) quy định thứ tự ghi Redis so với Postgres theo từng call site, và River purge job bù cho tuyến phòng thủ đã bị gỡ.

## Requirements

### User stories

1. Là người dùng, tôi muốn giữ đúng một credential và không bị đăng xuất ngẫu nhiên vì lỗi làm mới token chạy song song.
2. Là quản trị viên, tôi muốn khóa một tài khoản và phiên của người đó chết ngay lập tức, kể cả khi hạ tầng chập chờn tại đúng thời điểm đó.
3. Là người vận hành, tôi muốn biết ngay khi kho phiên hỏng thay vì phát hiện qua việc người dùng không đăng nhập được.
4. Là kỹ sư, tôi muốn một cơ chế xác thực duy nhất để đọc, thay vì JWT và bảng refresh token cùng tồn tại mà chỉ một cái thực sự phán quyết.

### Acceptance criteria

1. **AC-1**: Credential là chuỗi đục 32 byte sinh từ CSPRNG, mã hóa base64url, cấp bởi `POST /api/v1/auth/sign-in` và gửi kèm mọi request dưới dạng `Authorization: Bearer <session_id>`. Nó không mang claim, không tự xác minh được ngoại tuyến, và không có endpoint làm mới.
2. **AC-2**: Redis chỉ lưu SHA-256 của credential, không bao giờ lưu chuỗi trần. Một bản dump Redis không thể dùng để đăng nhập lại. Hai key cho mỗi phiên: `session:<sha256_hex>` là HASH chứa `sid`, `user_id`, `role`, `absolute_exp`; `user_session:<user_id>` là STRING chứa hash, đóng vai trò chỉ mục ngược để thu hồi theo user.
3. **AC-3**: `sessions.id` kiểu UUID vẫn là định danh phiên nội bộ. Middleware đặt field `sid` trong hash vào context và không bao giờ đặt credential. Toàn bộ `internal/platform/realtime/`, SSE hub, SSE handler, và đường `pg_notify` tới force logout không đổi hành vi.
4. **AC-4**: TTL trượt bằng `SESSION_IDLE_TTL_HOURS` được gia hạn trên mọi request có xác thực, và bị kẹp vào phần thời gian tuyệt đối còn lại nên key không bao giờ sống lâu hơn `absolute_exp` của chính nó.
5. **AC-5**: Trần tuyệt đối được cưỡng chế ngay trong Lua: khi `absolute_exp <= now`, script xóa key và trả về không tìm thấy, thay vì để bản ghi chết nằm lại chiếm bộ nhớ tới hết TTL trượt.
6. **AC-6**: Con trỏ `user_session:<user_id>` mang TTL tuyệt đối, không dùng chung TTL trượt với bản ghi phiên. Con trỏ hết hạn trước bản ghi sẽ làm mất khả năng thu hồi theo user, tức là đổi mật khẩu và khóa tài khoản im lặng không có tác dụng.
7. **AC-7**: Mọi thao tác chạm nhiều hơn một key chạy bằng Lua script nên nguyên tử. Việc tạo phiên mới ghi đè con trỏ và xóa bản ghi phiên cũ trong cùng một lượt, không bao giờ để lại HASH mồ côi mà không chỉ mục nào trỏ tới.
8. **AC-8**: Ở trạng thái ổn định, xác thực một request tốn đúng một round trip Redis, vì lệnh đọc và lệnh gia hạn TTL được gộp trong một script. Ngoại lệ đã biết: client gửi `EVALSHA` và chỉ gửi lại toàn bộ script khi Redis trả `NOSCRIPT`, nên request đầu tiên sau mỗi lần Redis khởi động lại tốn hai round trip. Đây là cùng bậc chi phí với một lần tra Postgres trước đây, chưa có số đo p50 và p99 để so sánh trực tiếp.
9. **AC-9**: Thứ tự ghi Redis so với Postgres được quyết theo từng call site, không đồng nhất: đăng xuất ghi Redis trước để fail closed; đăng nhập, đặt lại mật khẩu, và khóa tài khoản commit Postgres trước; đổi mật khẩu không ghi Redis.
10. **AC-10**: Đặt lại mật khẩu không được chạm Redis trước khi OTP được xác minh. Nếu chạm trước, bất kỳ ai biết email của nạn nhân đều có thể gửi OTP sai để đá họ ra khỏi app, tạo một lỗ DoS không cần xác thực.
11. **AC-11**: Khi lệnh thu hồi khớp ít nhất một hàng `sessions`, việc khóa tài khoản enqueue job `session_redis_purge` bằng `InsertTx` trong chính transaction thu hồi, nên job tồn tại khi và chỉ khi transaction commit. Lệnh `DEL` trực tiếp hỏng thì trả 500 để lỗi hiện ra, và job hội tụ sau đó trong cửa sổ retry mặc định của River. Điều kiện "khớp ít nhất một hàng" là một lỗ hổng đã biết, xem Risks mục 5 và Follow up mục 1.
12. **AC-12**: Job thu hồi khớp theo `sid` trước khi xóa, nên một lần chạy trễ không giết nhầm phiên mới mà người dùng vừa tạo lại.
13. **AC-13**: Đăng xuất là idempotent và luôn trả 204, kể cả khi thiếu header, bearer sai định dạng, hoặc credential đã chết. Khi không tìm thấy phiên trên Redis, đường xử lý không chạm Postgres.
14. **AC-14**: Redis là dependency bắt buộc chứ không phải cache tùy chọn. `/health/ready` trả 503 kèm tên đúng thành phần đang hỏng khi Redis mất, và tự trở lại 200 khi Redis sống lại mà không cần khởi động lại ứng dụng. Cấu hình sai làm bootstrap dừng ngay.
15. **AC-15**: Validation cấu hình chặn hai bất biến: `SESSION_IDLE_TTL_HOURS` không vượt `SESSION_ABSOLUTE_TTL_HOURS`, và `AUTH_RECORD_RETENTION_DAYS` phải phủ được hiệu của hai giá trị đó để hàng audit sống lâu hơn phiên còn hiệu lực.
16. **AC-16**: Bảng `sessions` chỉ còn là bản ghi audit. Không gì đọc nó để quyết định một request có được phép hay không. `expires_at` được đặt bằng `now + AbsoluteTTL` để worker dọn rác không xóa bản ghi của phiên vẫn đang sống.
17. **AC-17**: JWT và refresh rotation bị gỡ hoàn toàn: package `internal/platform/auth/jwt/`, bảng `session_refresh_tokens`, endpoint `POST /auth/refresh`, và dependency `golang-jwt/jwt/v5` đều không còn. Response đăng nhập trả `session_id` và `expires_at` thay cho cặp access và refresh token.
18. **AC-18**: FCM registration token chuyển sang bảng `device_tokens` khóa theo `(user_id, device_id)` và bỏ mọi điều kiện liveness, vì token mô tả một thiết bị và phải sống lâu hơn phiên.
19. **AC-19**: Ứng dụng Flutter giữ đúng một credential, không còn interceptor làm mới token và không còn nhánh 401 rồi thử lại. Backend và frontend phải phát hành cùng lúc.

## Decision

**Chosen option**: Session ID đục lưu trên Redis, cắt thẳng, giữ bảng `sessions` làm audit.

Credential là chuỗi đục sinh bằng CSPRNG và chỉ tồn tại dưới dạng SHA-256 trên Redis. Redis là nguồn phán quyết duy nhất cho câu hỏi một credential còn hiệu lực hay không. Định danh phiên vẫn là UUID trong `sessions.id` để tầng realtime không phải đổi. Vì hệ thống chưa có người dùng production, việc chuyển đổi được cắt thẳng thay vì chạy middleware hỗ trợ đồng thời hai chế độ.

**Implementation skills**: không có community skill nào chi phối quyết định này.

## Feature design

### Components

1. `internal/platform/redis/client.go` dựng kết nối Redis theo khuôn `database.NewPostgresPool`, ping ngay để fail fast, và cung cấp `Close` cùng `Ping` để readiness dùng.
2. `internal/platform/session/` là kho phiên: `session.go` định nghĩa entity và sentinel `ErrNotFound`, `scripts.go` chứa bốn Lua script, `store.go` là adapter Redis.
3. `internal/transport/http/middleware/auth.go` chỉ còn một biến thể `Auth(store)`. Không còn `TokenVerifier`, `SessionValidator`, hay `TokenAuth`, vì credential đục không mang thông tin nào để kiểm ngoại tuyến.
4. `internal/modules/auth/usecase/service.go` điều phối cả repository Postgres (audit) lẫn store Redis (phán quyết). Store là một port khai báo trong usecase, đúng khuôn `PasswordManager` sẵn có. Repository Postgres không được biết tới Redis.
5. `internal/modules/auth/jobs/session_purge.go` chứa job args, worker, và enqueuer cho backstop thu hồi.
6. `internal/modules/admin/` khai báo port một method riêng cho việc thu hồi, để admin không phải import `auth/usecase`.

### Lifecycle

1. Bootstrap dựng Redis ngay sau pgx pool, ping thất bại thì dừng ứng dụng.
2. Đăng nhập commit Postgres trước để có hàng audit, bất biến một phiên trên mỗi user, và `pg_notify` trong transaction. Sau đó sinh credential và gọi script tạo phiên với `SID` bằng `sessions.id`.
3. Mọi request có xác thực chạy một script đọc và gia hạn, rồi đặt `user_id`, `role`, `sid` vào context.
4. Đăng xuất đọc rồi xóa phiên trên Redis trước, lấy được `sid` và `user_id`, sau đó ghi audit và phát `session.ended` trong transaction Postgres.
5. Thu hồi theo user chạy script khớp `sid`, được gọi trực tiếp và được bảo hiểm bằng River job.
6. Shutdown đóng Redis cùng nhóm với pool, lỗi đóng được gộp vào `errors.Join`.

### Failure handling

1. Redis mất kết nối làm mọi request có xác thực trả 401 và `/health/ready` trả 503 kèm tên thành phần hỏng. Đây là hành vi fail closed có chủ đích.
2. Bootstrap không có closer list, nên Redis được đóng bằng một `defer` với cờ bàn giao, chạy trên mọi đường thoát sớm kể cả nhánh thêm về sau.
3. Transaction Postgres hỏng sau khi đã xóa key Redis ở đường đăng xuất thì `session.ended` được publish bù ngoài transaction, và việc publish bù được gate bằng việc thực sự xóa được một key Redis, không phải bằng số hàng Postgres.
4. Lệnh `DEL` trực tiếp lúc khóa tài khoản hỏng thì API trả 500, trạng thái Postgres đã bền, và job purge hội tụ sau.
5. Con trỏ mồ côi (bản ghi phiên đã hết hạn nhưng con trỏ còn) được script thu hồi dọn luôn khi gặp.

### Value sourcing

| Action | Value produced | Source |
|---|---|---|
| Đăng nhập | `session_id` trả cho client | `domain.NewOpaqueToken()`, CSPRNG 32 byte |
| Đăng nhập | Key trên Redis | `domain.HashToken()`, SHA-256 của credential |
| Đăng nhập | `sid` trong hash | `sessions.id`, uuidv7 do Postgres sinh |
| Đăng nhập | `expires_at` trả cho client | `now + SESSION_ABSOLUTE_TTL_HOURS` |
| Đăng nhập | `expires_at` của hàng audit | `now + SESSION_ABSOLUTE_TTL_HOURS`, không phải idle TTL |
| Mọi request | `user_id` và `role` trong context | Field trong hash phiên, không phải JOIN `users` |
| Mọi request | TTL mới của key | `SESSION_IDLE_TTL_HOURS`, kẹp vào `absolute_exp - now` |
| Đăng xuất | `sid` và `user_id` để ghi audit | Đọc từ hash trước khi xóa |
| Khóa tài khoản | Danh sách sid cần thu hồi | `RETURNING id` của lệnh `UPDATE sessions` trong transaction |
| Đặt lại mật khẩu | `user_id` cho thu hồi theo user | Trả về từ repository cùng danh sách sid, vì chỉ mục ngược khóa theo user |
| Chi tiết tài khoản | `active_sessions_count` | Đếm hàng `sessions` chưa revoke và chưa quá `expires_at`, xem Follow up |

### Key invariants

1. Credential trần không bao giờ xuất hiện trong bất kỳ key hay value nào trên Redis, cũng không nằm trong log.
2. Middleware đặt `sid` vào context, không bao giờ đặt credential.
3. Mỗi user có tối đa một bản ghi phiên sống và đúng một con trỏ trỏ tới nó. Không tồn tại HASH mà không con trỏ nào trỏ tới.
4. TTL trượt luôn nhỏ hơn hoặc bằng phần thời gian tuyệt đối còn lại.
5. TTL của con trỏ luôn là tuyệt đối, không bao giờ là TTL trượt.
6. Mọi thay đổi `users.role` hoặc `users.status` phải kéo theo thu hồi phiên, vì `role` được cache trong hash và sống tới hết TTL nếu không thu hồi.
7. `uq_sessions_one_active_per_user` từ nay có nghĩa là một hàng chưa revoke, không còn có nghĩa là một phiên còn sống. Không được đọc index này như một bất biến về liveness ở bất kỳ đâu.
8. Không bao giờ ghi Postgres trên đường đọc của middleware để sửa độ lệch, vì làm vậy biến mọi request thành một lệnh ghi cơ sở dữ liệu.
9. Đường thu hồi luôn khớp `sid` trước khi xóa.

### Security model

Credential là bí mật duy nhất, và ai cầm nó thì dùng được tài khoản trong thời hạn TTL. Vì vậy Redis chỉ lưu bản băm, và `internal/transport/http/middleware/logging.go` không log bất kỳ header nào, tính chất phải được giữ nguyên.

Tuyến phòng thủ `AND u.status='active'` trong câu truy vấn xác thực cũ đã biến mất cùng câu truy vấn đó. Middleware không còn chạm `users.status`, nên `DEL` trên Redis là cơ chế cưỡng chế duy nhất cho việc khóa tài khoản. River purge job là điều kiện để không tạo hồi quy bảo mật, chi tiết tại [child spec 0003](0003-revocation-backstop.md).

Redis phải có mật khẩu và phải dùng `maxmemory-policy noeviction`. Chính sách `allkeys-lru` là dành cho cache: với kho phiên, nó âm thầm xóa phiên của người ít hoạt động khi đầy bộ nhớ, tạo ra việc đăng xuất ngẫu nhiên không log và không tái hiện được.

Hai tính chất này hiện chỉ được bảo đảm bằng cấu hình trong `docker-compose.yaml` của môi trường phát triển. Ứng dụng không kiểm tra `maxmemory-policy` lúc bootstrap và không từ chối một `REDIS_URL` không mật khẩu, nên ở môi trường production chúng là trách nhiệm của người vận hành chứ chưa phải bất biến được cưỡng chế. Xem Follow up mục 4.

### Configuration required

1. `REDIS_URL`: chuỗi kết nối, phải là `redis://` hoặc `rediss://` và có host. Mật khẩu đi vào qua phần userinfo của URL, ví dụ `redis://:matkhau@host:6380/0`. `RedisConfig` không có field mật khẩu riêng và client chỉ gọi `ParseURL`, nên đây là đường duy nhất.
2. `REDIS_PASSWORD`: chỉ được `docker-compose.yaml` đọc để đặt `--requirepass` cho container. Mã Go không đọc biến này. Hiện `Validate()` chấp nhận một `REDIS_URL` không có userinfo, xem Follow up mục 4.
3. `REDIS_POOL_SIZE`: mặc định `20`, phải dương.
4. `REDIS_DIAL_TIMEOUT_SECONDS`: mặc định `5`, phải dương.
5. `REDIS_READ_TIMEOUT_SECONDS`: mặc định `2`, phải dương.
6. `SESSION_IDLE_TTL_HOURS`: TTL trượt, mặc định `168` tức bảy ngày.
7. `SESSION_ABSOLUTE_TTL_HOURS`: trần cứng, mặc định `720` tức ba mươi ngày.
8. `TEST_REDIS_URL`: dùng cho integration test, trỏ sang database Redis khác.

### Observability

1. Log `event=session_purge_recovered` kèm `user_id` và số lượng sid khi job purge thực sự xóa được, vì điều đó báo hiệu lần gọi trực tiếp đã lỡ và Redis đang chập chờn.
2. `/health/ready` trả về trạng thái từng dependency có tên, ví dụ `{"status":"degraded","database":"ok","redis":"down"}`, thay vì một chữ degraded không cho biết nên nhìn đâu.
3. Không log credential, không log header.
4. Vận hành cần alert `used_memory` ở khoảng bảy mươi phần trăm `maxmemory`, và `rdb_last_bgsave_status`.

### Critical test scenarios

1. Đăng nhập rồi gọi API bằng credential nhận được, xác nhận key trên Redis là SHA-256 và chuỗi client cầm không xuất hiện ở bất kỳ key hay value nào, kiểm chứng **AC-1**, **AC-2**.
2. Gọi API sau vài giây và xác nhận TTL được nạp lại, kiểm chứng **AC-4**, **AC-8**.
3. Đặt `absolute_exp` đã qua trong khi key còn sống, xác nhận script từ chối và xóa key, kiểm chứng **AC-5**.
4. Đăng nhập trên máy A rồi đăng nhập lại trên máy B, xác nhận số key `session:*` vẫn đúng một và credential của A trả 401, kiểm chứng **AC-7**.
5. Mở SSE rồi đăng nhập lại cùng user ở nơi khác, xác nhận stream đầu nhận `event: close` với lý do `session_ended`, kiểm chứng **AC-3**.
6. Khóa tài khoản bằng API quản trị, xác nhận phiên nạn nhân chết ngay, kiểm chứng **AC-11**.
7. Đẩy thẳng job purge vào hàng đợi rồi xác nhận phiên bị giết kèm log recovered, kiểm chứng **AC-11**, **AC-12**.
8. Gửi OTP sai nhiều lần cho luồng đặt lại mật khẩu, xác nhận phiên đang đăng nhập vẫn sống, rồi gửi OTP đúng và xác nhận phiên chết, kiểm chứng **AC-10**.
9. Đổi mật khẩu và xác nhận phiên hiện tại vẫn sống, kiểm chứng **AC-9**.
10. Đăng xuất hai lần cùng credential, và đăng xuất không header, xác nhận cả ba đều 204, kiểm chứng **AC-13**.
11. Dừng Redis rồi khởi động lại, xác nhận `/health/ready` chuyển 503 rồi tự về 200, kiểm chứng **AC-14**.
12. Nạp cấu hình với idle vượt absolute, và với retention không phủ được hiệu, xác nhận bootstrap dừng và nêu đúng tên biến, kiểm chứng **AC-15**.
13. Chạy migration tiến rồi lùi, xác nhận `session_refresh_tokens` và `sessions.fcm_token` được dựng lại đối xứng, kiểm chứng **AC-17**.
14. Truy vấn FCM token cho một user không mở app tám ngày, xác nhận query mới trả đúng token trong khi query cũ trả rỗng, kiểm chứng **AC-18**.

## Build plan

Kế hoạch được chia theo hạ tầng trước, đường xác thực sau, dọn dẹp sau cùng. Cách chia này lệch khỏi Tracer Bullet mặc định của dự án một cách có chủ đích: đây là việc thay thế một cơ chế đang chạy, nên mỗi giai đoạn phải tự đứng vững và tự kiểm chứng được trước khi giai đoạn sau chạm vào đường xác thực thật.

1. Tách `device_tokens` thành bảng riêng khóa theo `(user_id, device_id)`, bỏ điều kiện liveness trong hai query notification, và gỡ `fcm_token` khỏi đường ghi của `sessions`. Làm trước vì TTL trượt sẽ làm lỗi cũ lộ ra nặng hơn, thỏa **AC-18**.
2. Dựng hạ tầng Redis: service trong compose, `RedisConfig` cùng validation, client có ping fail fast, readiness nhiều dependency có tên, và wiring trong bootstrap có đóng an toàn trên mọi đường thoát, thỏa **AC-14**, **AC-15**.
3. Xây kho phiên thuần với bốn Lua script và test đầy đủ, chưa nối vào đường xác thực, thỏa **AC-2**, **AC-4**, **AC-5**, **AC-6**, **AC-7**, **AC-8**.
4. Chuyển usecase auth sang kho phiên: đăng nhập, đăng xuất, đặt lại mật khẩu, đổi mật khẩu, cùng thứ tự ghi riêng cho từng call site, thỏa **AC-1**, **AC-9**, **AC-10**, **AC-13**, **AC-16**.
5. Rút gọn middleware còn một biến thể, export bộ phân tích bearer dùng chung, và giữ nguyên ba context key với `sid` là UUID, thỏa **AC-3**.
6. Nối admin: trả thêm danh sách sid từ transaction thu hồi, enqueue job purge trong cùng transaction, đăng ký worker, và trả 500 khi `DEL` trực tiếp hỏng, thỏa **AC-11**, **AC-12**.
7. Cập nhật tầng delivery: bỏ route và handler làm mới, đổi DTO response sang `session_id` và `expires_at`, và cho đăng xuất chạy không middleware, thỏa **AC-1**, **AC-13**.
8. Dọn code cũ: xóa package jwt, `RotateRefresh`, migration drop `session_refresh_tokens`, `go mod tidy`, và cập nhật `docs/openapi.yaml` trước khi sửa frontend, thỏa **AC-17**.
9. Chuyển frontend sang một credential: một storage key, model response mới, xóa bộ làm mới phiên, rút gọn interceptor còn gắn header và kết thúc phiên khi 401, thỏa **AC-19**.

## Consequences

### Positive

1. Một cơ chế xác thực duy nhất để đọc và suy luận, thay vì JWT và bảng refresh token cùng tồn tại mà chỉ một cái thực sự phán quyết.
2. Thu hồi tức thì bằng một lệnh `DEL`, không còn cửa sổ mười lăm phút của access token.
3. Frontend giảm khoảng hai trăm dòng và mất hẳn lớp lỗi hai luồng làm mới song song bị coi là tái sử dụng token rồi đá người dùng ra.
4. Không còn một lượt gọi HTTP phụ mỗi mười lăm phút cho mỗi client.
5. Chi phí xác thực giữ nguyên một round trip, chuyển từ Postgres sang Redis.

### Negative

1. Redis trở thành dependency cứng trên đường đi của mọi request có xác thực. Redis chết là toàn bộ hệ thống trả 401.
2. Mất tuyến phòng thủ `u.status='active'` từng chạy miễn phí trên mọi request. Cơ chế cưỡng chế khóa tài khoản thu lại còn một lệnh ghi Redis, phải bù bằng job bền vững.
3. `role` bị cache trong hash phiên, nên mọi luồng đổi vai trò trong tương lai bắt buộc phải kéo theo thu hồi phiên.
4. Thêm một hạ tầng phải vận hành: AOF, `noeviction`, mật khẩu, giám sát bộ nhớ, và TLS khi Redis không cùng mạng riêng với API.
5. `active_sessions_count` hiển thị cho quản trị viên có thể lệch, vì nó vẫn đếm từ Postgres trong khi Redis mới là nguồn phán quyết.

### Neutral

1. Bảng `sessions` vẫn tồn tại nhưng đổi vai trò thành audit, và `expires_at` của nó đổi ý nghĩa theo.
2. `CleanupExpiredAuth` được giữ lại vì hàng audit vẫn cần dọn. Phiên còn sống thì do TTL của Redis dọn, không do worker.
3. Cột `sessions.fcm_token` được giữ qua giai đoạn một để rollback an toàn, rồi mới gỡ ở migration sau.
4. Dòng `golang-jwt/jwt/v4` còn lại trong `go.mod` là dependency gián tiếp của Firebase SDK, không liên quan tới auth.

## Migration plan

**Strategy**: Cắt thẳng theo giai đoạn, không chạy song song hai chế độ.

Cắt thẳng là lựa chọn được phép ở đây vì hệ thống chưa có người dùng production. Với hệ thống đã có người dùng thì phải chọn strangler thay vì cách này, và middleware sẽ phải chấp nhận đồng thời cả hai loại credential trong thời gian chuyển đổi.

### Phases

1. Tách `device_tokens` thành pull request riêng, không dính Redis, có thể phát hành độc lập.
2. Dựng hạ tầng Redis mà chưa đụng đường xác thực. Kiểm chứng bằng `/health/ready`.
3. Xây kho phiên có test đầy đủ mà chưa code production nào gọi tới.
4. Chuyển đường xác thực, admin, và job purge trong một lần.
5. Dọn code cũ và cập nhật `docs/openapi.yaml`.
6. Phát hành frontend cùng lúc với backend.

### Rollback

1. Giai đoạn một và hai revert độc lập được, vì chưa có gì phụ thuộc.
2. Migration `000018` và `000019` đều có `-- +goose Down` đối xứng và đã được kiểm chứng bằng cách chạy lùi rồi tiến lại.
3. Từ giai đoạn bốn trở đi không còn rollback từng phần: backend và frontend phải revert cùng nhau, vì model response của frontend khai báo access token và refresh token là bắt buộc và không cho phép null.

### Risks

1. Phát hành backend trước frontend làm ứng dụng vỡ ngay lần đăng nhập đầu tiên. Hai bên phải lên cùng lúc.
2. Đặt `expires_at` của hàng audit bằng idle TTL thay vì absolute TTL sẽ khiến worker dọn rác xóa bản ghi của phiên vẫn đang sống. Đây là lý do có bất biến trong **AC-15**.
3. Cài thu hồi theo kiểu chung cho mọi call site sẽ đá chính người vừa đổi mật khẩu ra khỏi app, ngược hẳn ý định của điều kiện `id<>$2` trong câu lệnh hiện có.
4. Không kiểm chứng được backstop bằng cách tạm dừng Redis, vì Redis chết thì chính quản trị viên cũng không xác thực được và transaction không bao giờ chạy. Cửa sổ lỗi thật hẹp hơn dự tính: Redis phải sống lúc middleware chạy rồi chết đúng lúc `DEL`. Phải kiểm chứng bằng cách đẩy thẳng job vào hàng đợi.
5. **Backstop chưa bịt kín lỗ hổng nó sinh ra để bịt.** Đường thu hồi được lái bằng danh sách sid mà `UPDATE ... WHERE revoked_at IS NULL RETURNING id` trả về, nên một lần khóa lại sau khi hàng đã revoked sẽ khớp không hàng nào và không làm gì cả. Chi tiết và hướng sửa nằm ở [child spec 0003](0003-revocation-backstop.md).
6. **Mất Redis tạm thời đăng xuất vĩnh viễn mọi người dùng.** Middleware gộp mọi lỗi từ kho phiên thành 401, kể cả lỗi mạng và timeout, còn ứng dụng Flutter coi 401 là kết thúc phiên và xóa credential. Một cú chớp vài chục giây của Redis buộc toàn bộ người dùng đăng nhập lại bằng tay, dù không phiên nào thực sự bị thu hồi.
7. **Backstop cạn lượt thử thì im lặng.** Job chỉ ghi log khi xóa thành công. Sau mười lần thử thất bại nó bị loại bỏ mà không có log, metric, hay cảnh báo nào, nên một sự cố Redis dài hơn cửa sổ retry để lại tài khoản bị khóa vẫn dùng được mà không ai biết.
8. **Mất dữ liệu Redis là đăng xuất toàn hệ thống.** `appendfsync everysec` chấp nhận mất tối đa một giây, nhưng một lần restart mất AOF, một lần failover, hoặc một lệnh `FLUSHALL` sẽ xóa mọi phiên cùng lúc, và không có đường phục hồi từ bảng `sessions`.
9. **Đổi mật khẩu không xoay credential.** Quyết định không ghi Redis ở call site này là đúng với ý định của điều kiện `id<>$2`, nhưng kéo theo một tính chất chưa từng được nêu ra: một credential bị đánh cắp vẫn dùng được sau khi nạn nhân đổi mật khẩu, cho tới hết TTL.

## Follow up

Bốn mục đầu là hồi quy bảo mật hoặc lỗi vận hành trong code đang chạy, phát hiện khi rà soát spec này. Chúng nên được xử lý trước các mục còn lại.

1. **Thu hồi vô điều kiện theo user ở đường khóa tài khoản.** Đường hiện tại chỉ hoạt động khi lệnh `UPDATE sessions` khớp ít nhất một hàng, nên khóa lại lần hai không có tác dụng. Sửa bằng cách đọc con trỏ `user_session:<user_id>` rồi xóa cả con trỏ lẫn bản ghi phiên, không phụ thuộc danh sách sid. Giữ nguyên việc khớp `sid` cho đường job retry. Xem Risks mục 5.
2. **Tách lỗi hạ tầng khỏi lỗi xác thực trong middleware.** Chỉ `session.ErrNotFound` mới được ánh xạ thành 401; lỗi mạng và timeout phải trả 503. Kèm theo, phía Flutter chỉ được xóa credential khi nhận 401, không được xóa khi nhận 503. Xem Risks mục 6.
3. **Cảnh báo khi job purge cạn lượt thử.** Thêm log và metric ở đường job bị loại bỏ, ví dụ `session_purge_exhausted`, và xác định ai nhận cảnh báo đó. Không có nó thì backstop hỏng trong im lặng. Xem Risks mục 7.
4. **Cưỡng chế hai tính chất vận hành của Redis thay vì tin vào cấu hình.** Từ chối một `REDIS_URL` không có userinfo ở ngoài môi trường phát triển, và kiểm `maxmemory-policy` lúc bootstrap để dừng hoặc cảnh báo khi nó không phải `noeviction`.
5. `CountActiveSessionsByUserID` vẫn đếm hàng Postgres với điều kiện `revoked_at IS NULL AND expires_at > now()`. Vì `expires_at` nay là `now + AbsoluteTTL` còn Redis mới là nguồn phán quyết, một phiên hết hạn vì không hoạt động ở ngày thứ tám vẫn được báo là đang hoạt động cho tới ngày thứ ba mươi. Thiết kế ban đầu định trả lời con số này từ Redis bằng `EXISTS user_session:<user_id>` nhưng phần đó chưa được thực hiện. Nếu không sửa thì phải đổi tên hoặc chú thích lại field trong `docs/openapi.yaml` để nó không được đọc như một con số về phiên còn sống.
6. **Quyết định xem đổi mật khẩu có nên xoay credential không.** Hành vi hiện tại để credential bị đánh cắp sống sót qua lần đổi mật khẩu của nạn nhân. Đây là một quyết định chưa từng được đưa ra một cách tường minh, xem Risks mục 9.
7. **Nêu cơ chế cưỡng chế cho bất biến thu hồi khi đổi vai trò.** Key invariant 6 hiện chỉ là văn xuôi. Không có test hay chokepoint nào chặn người viết endpoint đổi vai trò trong tương lai quên nối vào đường thu hồi.
8. Gỡ cột `sessions.fcm_token` bằng một migration riêng sau khi giai đoạn một đã chạy ổn định.
9. Cân nhắc thêm metric cho tỉ lệ hit và miss của kho phiên, để biết Redis chập chờn tới mức nào trước khi người dùng phàn nàn.
10. `plan.md` trong thư mục này là tài liệu thực thi đã hoàn thành. Spec này thay thế nó làm bản ghi quyết định, nên có thể lưu trữ hoặc xóa `plan.md` khi thấy hợp lý.

## Rationale

Reasoning and options: see [rationale.md](rationale.md).
