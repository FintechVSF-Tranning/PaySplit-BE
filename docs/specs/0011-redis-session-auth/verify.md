# Verify: Redis session auth · spec 0011 · updated 2026-09-09

_Steps derived from spec 0011 acceptance criteria. `/check verify` runs these; `/test` locks the durable ones. Các ô đã đánh dấu là những bước đã chạy thật trên Postgres và Redis thật trong quá trình thực thi, không phải mock._

> ⚠️ Trước khi chạy: `TEST_DATABASE_URL` và `TEST_REDIS_URL` phải có trong `.env`. Mọi `*_integration_test.go` bị `t.Skip` im lặng khi thiếu, nên một lần `make test` xanh không có nghĩa là chúng đã chạy.

## Credential và kho phiên

- [x] Đăng nhập rồi gọi `GET /api/v1/users/me` bằng credential nhận được. Xác nhận response trả `session_id` dài bốn mươi ba ký tự cùng `expires_at` bằng trần tuyệt đối, và request bảo vệ trả 200 → AC-1
- [x] Duyệt Redis bằng `SCAN 0 MATCH 'session:*'`. Xác nhận key là SHA-256 và chuỗi client cầm không xuất hiện ở bất kỳ key hay value nào. Xác nhận hash có đủ bốn field và con trỏ `user_session:<user_id>` trỏ đúng vào nó → AC-2
- [x] Đọc TTL của bản ghi phiên, gọi lại API sau vài giây, rồi đọc lại TTL. Xác nhận TTL được nạp lại về `SESSION_IDLE_TTL_HOURS`, và TTL của con trỏ giữ nguyên mốc tuyệt đối → AC-4, AC-6, AC-8
- [x] Đặt `absolute_exp` về quá khứ trong khi key còn TTL. Xác nhận request kế tiếp trả 401 và key bị xóa khỏi Redis thay vì nằm lại tới hết TTL trượt → AC-5
- [x] Đăng nhập máy A và ghi lại hash, rồi đăng nhập lại ở máy B. Xác nhận số key `session:*` vẫn đúng một, credential của A trả 401, và không còn HASH nào không con trỏ nào trỏ tới → AC-7

## Đường xác thực và định danh phiên

- [x] Mở SSE `/api/v1/users/me/events`, rồi ở terminal khác đăng nhập lại cùng user. Xác nhận stream đầu nhận `event: close` với `{"reason":"session_ended"}`, chứng minh `pg_notify` và tầng realtime còn nguyên → AC-3
- [x] Đăng xuất rồi thử lại cùng credential, và thử đăng xuất không header cùng bearer sai định dạng. Xác nhận cả ba đều trả 204 và key đã biến mất → AC-13
- [x] Gọi `POST /api/v1/auth/refresh`. Xác nhận trả 404 và endpoint đã biến mất khỏi cả API lẫn `docs/openapi.yaml` → AC-1, AC-17
- [x] Đối chiếu response đăng nhập với schema trong `docs/openapi.yaml`. Xác nhận khớp hoàn toàn với `session_id` và `expires_at`, không còn `access_token` hay `refresh_token` → AC-17

## Thu hồi và backstop

- [x] Khóa tài khoản bằng `PUT /admin/accounts/{id}/status` với trạng thái `suspended`. Xác nhận phiên nạn nhân trả 401 ngay → AC-11
- [x] Đẩy thẳng một job `session_redis_purge` vào bảng `river_job`. Xác nhận phiên bị giết trong vài giây kèm log `session_purge_recovered`. Đây là cách kiểm chứng đúng, vì tạm dừng Redis khiến chính quản trị viên cũng không xác thực được → AC-11, AC-12
- [x] Enqueue job purge với danh sách sid cũ sau khi người dùng đã đăng nhập lại. Xác nhận phiên mới không bị giết vì `sid` không khớp → AC-12
- [x] Gửi OTP sai ba lần cho luồng đặt lại mật khẩu. Xác nhận phiên đang đăng nhập của nạn nhân vẫn trả 200. Sau đó gửi OTP đúng và xác nhận phiên chết → AC-10
- [x] Đổi mật khẩu rồi gọi lại API bằng credential hiện tại. Xác nhận phiên vẫn sống, đúng ý định của điều kiện `id<>$2` → AC-9

## Hạ tầng và cấu hình

- [x] Gọi `/health/ready` khi mọi thứ khỏe. Xác nhận trả 200 với `{"database":"ok","redis":"ok"}` → AC-14
- [x] Chạy `docker compose stop redis`. Xác nhận `/health/ready` trả 503 với `{"database":"ok","redis":"down","status":"degraded"}`, rồi khởi động lại Redis và xác nhận probe tự về 200 mà không cần khởi động lại ứng dụng → AC-14
- [x] Khởi động với `REDIS_URL` sai cổng, với `REDIS_URL` trống, và với idle TTL vượt absolute TTL. Xác nhận cả ba đều dừng ngay lúc bootstrap và nêu đúng tên biến → AC-14, AC-15
- [x] Kiểm cấu hình Redis đang chạy. Xác nhận `maxmemory-policy=noeviction`, `appendonly=yes`, `appendfsync=everysec`, và Redis từ chối kết nối không mật khẩu → AC-14
- [x] Đặt `AUTH_RECORD_RETENTION_DAYS` thấp hơn hiệu của absolute TTL và idle TTL. Xác nhận `Validate()` chặn với thông báo nêu đủ ba tên biến → AC-15

## Migration và dọn dẹp

- [x] Chạy migration `000019` tiến. Xác nhận `session_refresh_tokens` và `sessions.fcm_token` đã bị xóa → AC-17
- [x] Chạy `migrate-down` rồi `migrate-up` lại. Xác nhận cả hai được dựng lại đối xứng → AC-17
- [x] Khởi động ứng dụng không có `JWT_SECRET_KEY`. Xác nhận lên bình thường, không lỗi cấu hình → AC-17
- [x] Chạy `CleanupExpiredAuth` sau khi bảng bị drop. Xác nhận chạy sạch, không lỗi `relation does not exist` → AC-16
- [x] Gieo dữ liệu phủ ba ca rồi chạy migration `000018`: cùng cặp user và thiết bị có nhiều phiên thì giữ token của phiên mới nhất; phiên đã hết hạn thì token vẫn được backfill; chạy lùi thì token trả về `sessions` không mất mát → AC-18
- [x] Truy vấn FCM token cho một user không mở app tám ngày. Xác nhận query mới trả đúng token trong khi query cũ trả rỗng → AC-18

## Commands

- [x] `go test ./internal/platform/session/...` → mười một unit test qua `miniredis` xanh → AC-2, AC-4, AC-5, AC-6, AC-7
- [x] `TEST_REDIS_URL=<redis-url> go test -count=1 -run Integration ./internal/platform/session/...` → sáu integration test trên Redis 8 thật xanh → AC-2, AC-4, AC-7, AC-12
- [x] `go test -race ./...` → sạch, không có data race → AC-14
- [x] `grep` xác nhận không code production nào gọi kho phiên ở thời điểm cuối giai đoạn xây dựng, trước khi đường xác thực được chuyển → AC-8
- [x] `go mod tidy` rồi kiểm `go.mod` → `golang-jwt/jwt/v5` biến mất. Dòng `jwt/v4` còn lại là dependency gián tiếp của Firebase SDK, không liên quan tới auth → AC-17
- [x] `cd PaySplit-FE && flutter test && flutter analyze` → toàn bộ test và static analysis xanh sau khi gỡ bộ làm mới phiên → AC-19

## Acceptance criteria coverage

- AC-1 được phủ bởi bước credential 1 và hai bước đường xác thực về endpoint làm mới.
- AC-2 được phủ bởi bước credential 2 và cả hai lệnh test của kho phiên.
- AC-3 được phủ bởi bước SSE force logout.
- AC-4, AC-6, AC-8 được phủ bởi bước TTL trượt và lệnh test kho phiên.
- AC-5 được phủ bởi bước đặt `absolute_exp` về quá khứ, đã chạy tay ngày 2026-09-09.
- AC-7 được phủ bởi bước kiểm mồ côi.
- AC-9 được phủ bởi bước đổi mật khẩu.
- AC-10 được phủ bởi bước OTP sai.
- AC-11 được phủ bởi bước khóa tài khoản và bước đẩy job thẳng vào hàng đợi.
- AC-12 được phủ đầy đủ: cả đường hội tụ lẫn ca không giết nhầm phiên mới đều đã chạy tay end to end ngày 2026-09-09.
- AC-13 được phủ bởi bước đăng xuất ba ca.
- AC-14, AC-15 được phủ đầy đủ bởi nhóm hạ tầng, gồm cả bất biến retention đã chạy tay ngày 2026-09-09.
- AC-16 được phủ bởi bước `CleanupExpiredAuth`.
- AC-17 được phủ bởi nhóm migration và dọn dẹp.
- AC-18 được phủ bởi hai bước cuối nhóm migration.
- AC-19 được phủ bởi `flutter test` và `flutter analyze`, đã chạy lại ngày 2026-09-09 sau khi gỡ bộ làm mới phiên.

## Gaps

Bốn lỗ hổng kiểm chứng trước đây đã được đóng ngày 2026-09-09 bằng một lượt `/check verify` chạy trên Postgres và Redis thật. AC-5, AC-12, AC-15 và AC-19 nay đều có bằng chứng chạy tay, không còn chỉ dựa vào unit test.

Còn lại hai khoản chưa đóng, cả hai đều là phần thiết kế chưa cài chứ không phải bước kiểm chứng thiếu:

1. **`active_sessions_count` chưa chuyển sang Redis.** Xem Follow up mục 5 trong [index.md](index.md). Không có ô kiểm chứng nào ở trên tương ứng với nó.
2. **Bốn lỗ hổng trong code đang chạy, phát hiện khi rà soát spec.** Thu hồi lần hai không có tác dụng; mất Redis tạm thời đăng xuất vĩnh viễn mọi người dùng; job purge cạn lượt thử trong im lặng; hai tính chất vận hành của Redis không được cưỡng chế trong mã (chúng đúng trong `docker-compose.yaml` của môi trường phát triển, đã kiểm ngày 2026-09-09, nhưng ứng dụng không tự kiểm lúc bootstrap). Xem Risks mục 5 tới 8 và Follow up mục 1 tới 4 trong [index.md](index.md). Chúng cần bước kiểm chứng riêng sau khi được sửa, và các bước đó chưa tồn tại vì hành vi đúng chưa được cài.
