# 0010. Connection efficient events

**Date**: 2026-09-03
**Status**: In Progress

## Summary

PaySplit giảm ba PostgreSQL `LISTEN` session xuống còn một session trên mỗi backend instance. Bill và Group dùng chung một listener cho hai channel. River chuyển sang poll only để bỏ notifier session dài hạn, với thời gian polling mặc định một giây và khả năng quay lại notifier bằng cấu hình.

Mức giảm tổng physical connection còn phụ thuộc `DB_MIN_CONNS` và tải thực tế. Vì vậy verification phải đo riêng số `LISTEN` session, số pool slot bị giữ lâu, và tổng physical session.

## Structure

1. [Shared PostgreSQL listener](0001-shared-postgres-listener.md) quy định một connection nhận cả `bill_events` và `group_events`.
2. [River poll only](0002-river-poll-only.md) quy định River không tạo notifier connection và tìm job mới bằng polling.

## Requirements

### User stories

1. Là người vận hành, tôi muốn mỗi backend instance dùng ít connection sống lâu hơn để giảm áp lực lên Supavisor.
2. Là người dùng, tôi muốn Bill SSE và Group SSE giữ nguyên hành vi khi hạ tầng listener được hợp nhất.
3. Là hệ thống, tôi muốn River vẫn xử lý job bền vững khi PostgreSQL `LISTEN/NOTIFY` không được dùng để đánh thức queue.

### Acceptance criteria

1. **AC-1**: Một backend instance chỉ giữ một PostgreSQL connection để `LISTEN bill_events` và `LISTEN group_events`.
2. **AC-2**: Notification của mỗi channel chỉ đến đúng Hub và giữ đúng thứ tự nhận. Event schema, SSE endpoint, snapshot, heartbeat, version fencing, và cơ chế bỏ event cho client chậm không thay đổi. Bill giữ subscriber buffer `16`; Group giữ subscriber buffer `32`.
3. **AC-3**: Startup chỉ báo ready sau khi listener acquire connection và đăng ký thành công cả hai channel. Khi connection bị đóng trong runtime, readiness chuyển sang degraded, listener kết nối lại với exponential backoff có jitter, đăng ký lại cả hai channel, rồi mới trở lại healthy.
4. **AC-4**: Bill payload chỉ hợp lệ khi decode thành công, `BillID != uuid.Nil`, và `Type` không rỗng. Group payload chỉ hợp lệ khi decode thành công, `GroupID != uuid.Nil`, `Version > 0`, và `Type` không rỗng. Payload lỗi chỉ bị bỏ cho notification đó, được ghi nhận bằng log và metric có cardinality giới hạn, không được log nguyên văn, và không làm reconnect listener.
5. **AC-5**: Khi `RIVER_POLL_ONLY=true`, River không tạo notifier connection và không phát câu lệnh `LISTEN`. River vẫn có thể gửi `NOTIFY` trong đường enqueue của thư viện, nhưng không giữ listener session. Job mới được phát hiện bằng polling với `RIVER_FETCH_POLL_INTERVAL_MS=1000` mặc định.
6. **AC-6**: Poll interval phải dương và không được nhỏ hơn `RIVER_FETCH_COOLDOWN_MS`. Cấu hình sai làm startup thất bại với tên biến rõ ràng.
7. **AC-7**: Khi queue đang rảnh, worker sẵn sàng, và database khỏe, thời gian từ lúc transaction enqueue commit đến lần fetch kế tiếp có miền lý thuyết từ `0 ms` đến dưới `1100 ms` với interval mặc định và jitter của River. Test dùng tolerance riêng cho thời gian chạy và database thay vì coi `1100 ms` là hard upper bound của end to end latency.
8. **AC-8**: `RIVER_POLL_ONLY=false` khôi phục notifier hiện tại sau khi restart process, không cần đổi code hoặc migration.
9. **AC-9**: Graceful shutdown dừng HTTP, River, shared listener, rồi pool trong thời gian chờ hữu hạn. Clean path chạy `UNLISTEN *` trước khi release connection. Session chưa đăng ký đủ channel hoặc cleanup lỗi phải bị đóng thay vì trả về pool.
10. **AC-10**: Ở trạng thái idle, số `LISTEN` session của ứng dụng giảm từ ba xuống một và số pool slot bị Bill hoặc Group listener giữ giảm từ hai xuống một. Tổng physical session được báo cáo riêng vì phụ thuộc `DB_MIN_CONNS` và tải. Với cấu hình hiện tại `DB_MIN_CONNS=0`, mục tiêu idle xấp xỉ từ ba xuống một physical session.
11. **AC-11**: Không có migration, thay đổi OpenAPI, hoặc thay đổi payload công khai.
12. **AC-12**: Khi shared listener mất connection, các local Bill và Group SSE subscription hiện có được đóng để client reconnect. Bill phục hồi bằng snapshot; Group phục hồi bằng version fencing và `/sync`, tránh giữ stream sống nhưng bỏ lỡ notification trong khoảng disconnect.

## Decision

**Chosen option**: Một shared listener tại platform layer và River poll only có feature flag.

Shared listener sở hữu một connection và một registry handler cố định được hoàn tất trước khi start. Handler nhận payload thô theo channel, giải mã trong module tương ứng, rồi publish đồng bộ để giữ thứ tự. River nhận `PollOnly` và `FetchPollInterval` qua wrapper hiện có. `RIVER_POLL_ONLY` mặc định `false` để hai phase rollout có thể được kiểm chứng độc lập trước khi bật poll only theo từng môi trường.

**Implementation skills**: `supabase` (`supabase`, `.agents/skills/supabase/`) · `supabase-postgres-best-practices` (`supabase/agent-skills`, `.agents/skills/supabase-postgres-best-practices/`)

## Feature design

### Components

1. `PostgresNotificationListener` nằm tại `internal/platform/database/notification_listener.go`. Nó sở hữu pool reference, một connection đang acquire, danh sách channel bất biến, vòng reconnect, readiness, và metrics.
2. Mỗi handler có chữ ký tương đương `func(context.Context, string) error`. Platform không import kiểu dữ liệu của Bill hoặc Group.
3. Bill Hub giữ subscriber buffer `16`, phần publish subscriber, và đường phát `pg_notify`, nhưng không còn sở hữu listener loop.
4. Group Hub giữ subscriber buffer `32`, phần publish subscriber, và giải mã event, nhưng không còn giữ `pgxpool.Pool`.
5. Bill và Group export `billhttp.NotificationChannel` và `grouphttp.NotificationChannel`. Bootstrap dùng hai constant này để tạo registry, rồi start đúng một shared listener goroutine.
6. River wrapper mở rộng config với `PollOnly` và `FetchPollInterval` rồi chuyển nguyên giá trị vào `river.Config`.

### Lifecycle

1. Bootstrap tạo pool và các Hub.
2. Bootstrap tạo registry listener cố định.
3. Shared listener acquire một connection và chạy đủ hai câu lệnh `LISTEN` trước khi application báo ready.
4. River start theo mode từ env.
5. Listener chờ notification và dispatch đồng bộ theo thứ tự nhận.
6. Khi listener disconnect, readiness degraded và local SSE subscription được đóng trước khi reconnect.
7. Shutdown dừng HTTP, River, và shared listener trong thời gian chờ hữu hạn trước khi đóng pool.

### Failure handling

1. Lỗi acquire, `LISTEN`, hoặc wait đưa listener về trạng thái unhealthy và kích hoạt reconnect. Backoff bắt đầu `500 ms`, nhân đôi tới tối đa `30 s`, áp dụng jitter từ `80%` đến `120%`, và reset sau `30 s` healthy liên tục.
2. Reconnect luôn tạo session mới và đăng ký lại toàn bộ channel.
3. Decode hoặc semantic validation lỗi chỉ loại một notification, không reconnect.
4. Handler chạy đồng bộ và phải hoàn tất nhanh. Publish tới SSE subscriber tiếp tục dùng channel buffer không blocking.
5. Clean shutdown chạy `UNLISTEN *` rồi release. Lỗi trong partial setup, wait, hoặc cleanup làm underlying connection bị đóng để session có trạng thái `LISTEN` không quay lại pool.
6. Khi listener disconnect, Hub đóng local SSE subscription. Bill client reconnect để lấy snapshot hiện tại. Group client reconnect với version fencing và gọi `/sync` khi có khoảng trống version.

### Value sourcing

1. Danh sách channel đến từ registry được bootstrap tạo từ `billhttp.NotificationChannel` và `grouphttp.NotificationChannel`.
2. Raw payload và channel đến từ `pgconn.Notification`.
3. Poll only đến từ `RIVER_POLL_ONLY`.
4. Poll interval đến từ `RIVER_FETCH_POLL_INTERVAL_MS`.
5. Fetch cooldown tiếp tục đến từ `RIVER_FETCH_COOLDOWN_MS`.
6. Application name đến từ `DB_APPLICATION_NAME` và được gắn vào PostgreSQL connection config.
7. Connection count đến từ Supabase Database Connections hoặc `pg_stat_activity`, lọc theo một `DB_APPLICATION_NAME` duy nhất cho instance thử nghiệm để bao gồm cả pooled session và River session đã hijack.

### Key invariants

1. Chỉ một goroutine được gọi `WaitForNotification` trên shared connection.
2. Registry không thay đổi sau khi listener start.
3. Notification của cùng một channel được dispatch theo đúng thứ tự nhận.
4. Platform layer không phụ thuộc Bill hoặc Group.
5. Bill subscriber buffer luôn là `16`; Group subscriber buffer luôn là `32`.
6. Connection chỉ được release về pool sau khi `UNLISTEN *` thành công. Mọi cleanup path không chắc chắn phải đóng underlying connection.
7. `FetchPollInterval >= FetchCooldown`.
8. Poll only không thay đổi transactional enqueue, retry, scheduling, periodic jobs, hoặc graceful shutdown của River.

### Security model

Không có surface công khai mới. Listener không log payload, thông tin hóa đơn, thông tin nhóm, hoặc dữ liệu ngân hàng. Database credential tiếp tục chỉ đến từ `DATABASE_URL`.

### Configuration required

1. `RIVER_POLL_ONLY`: bật poll only, mặc định `false`.
2. `RIVER_FETCH_POLL_INTERVAL_MS`: thời gian River tìm job khi queue đang rảnh, mặc định `1000`.
3. `RIVER_FETCH_COOLDOWN_MS`: thời gian nghỉ giữa các lần fetch khi queue đang có việc, giữ mặc định `100`.
4. `DB_APPLICATION_NAME`: tên ổn định để quan sát connection, mặc định `paysplit-api`.

### Observability

1. Gauge `paysplit_db_listener_connected` có giá trị `0` hoặc `1`.
2. Counter `paysplit_db_listener_reconnects_total{reason}` chỉ nhận `reason` thuộc allowlist `acquire`, `listen`, `wait`.
3. Counter `paysplit_db_listener_invalid_payloads_total{channel}` chỉ nhận `channel` thuộc allowlist `bill_events`, `group_events`.
4. Reconnect log có `channel`, `reason`, `attempt`, và `backoff`. Invalid payload log có `channel` và validation reason nhưng không có raw payload.
5. Startup log ghi River mode và poll interval, không ghi `DATABASE_URL`.
6. Verification ghi riêng `LISTEN` session, pinned pool slot, và total physical session trước và sau trên cùng một instance idle.

### Critical test scenarios

1. Gửi xen kẽ event Bill và Group, xác nhận đúng Hub, đúng thứ tự, và buffer giữ nguyên, kiểm chứng **AC-1**, **AC-2**.
2. Đóng connection listener, xác nhận readiness degraded, subscription hiện có bị đóng, cả hai channel được đăng ký lại, và client phục hồi qua snapshot hoặc `/sync`, kiểm chứng **AC-3**, **AC-12**.
3. Gửi payload lỗi cho từng rule semantic rồi gửi payload hợp lệ trên channel kia, xác nhận metric, log redaction, và listener không reconnect, kiểm chứng **AC-4**.
4. Làm lỗi câu lệnh `LISTEN` thứ hai và lỗi `UNLISTEN *`, xác nhận connection bị đóng và session có trạng thái `LISTEN` không quay lại pool, kiểm chứng **AC-9**.
5. Bật poll only, enqueue job trong transaction, và xác nhận worker fetch bằng polling với tolerance cho runtime, kiểm chứng **AC-5**, **AC-7**.
6. Tắt poll only và restart, xác nhận River notifier hoạt động lại, kiểm chứng **AC-8**.
7. Hủy context khi listener và River đang idle, xác nhận shutdown hoàn tất trong timeout và không còn goroutine hoặc connection, kiểm chứng **AC-9**.
8. Đo `LISTEN` session, pinned pool slot, và total physical session trước và sau bằng cùng application name, kiểm chứng **AC-10**.

## Build plan

1. Thêm `DB_APPLICATION_NAME` vào config và PostgreSQL connection setup. Ghi baseline theo ba phép đo trước khi thay đổi, kiểm chứng đầu vào cho **AC-10**.
2. Xây shared listener với initial readiness gate, runtime degraded state, backoff đã định nghĩa, bounded shutdown, và cleanup không trả session bẩn về pool, thỏa **AC-1**, **AC-3**, **AC-9**.
3. Tách Bill và Group handler với semantic validation, constant channel export, subscriber buffer giữ nguyên, metric, và log redaction, thỏa **AC-2**, **AC-4**, **AC-11**.
4. Wire một listener trong bootstrap, xóa hai listener loop cũ, và đóng local SSE subscription khi shared listener disconnect, thỏa **AC-1**, **AC-3**, **AC-12**.
5. Chạy unit test và PostgreSQL integration test cho routing, ordering, reconnect, partial `LISTEN`, cleanup, pool contamination, cancellation, và recovery của client, thỏa **AC-1** đến **AC-4**, **AC-9**, **AC-12**.
6. Thêm `RIVER_POLL_ONLY` mặc định `false` và `RIVER_FETCH_POLL_INTERVAL_MS` vào config, validation, `.env.example`, và README, thỏa **AC-5**, **AC-6**, **AC-8**.
7. Truyền poll config qua River wrapper và thêm test cho poll only, notifier rollback sau restart, pickup latency có tolerance, scheduled jobs, và periodic jobs, thỏa **AC-5** đến **AC-9**.
8. Thêm metrics và structured logs theo tên và allowlist đã định nghĩa, thỏa **AC-3**, **AC-4**, **AC-10**.
9. Chạy test liên quan, test toàn repo, rồi đo lại ba loại connection trên một instance idle và dưới queue load, thỏa **AC-2**, **AC-7**, **AC-9**, **AC-10**, **AC-11**.
10. Triển khai shared listener trước với River notifier giữ nguyên. Sau khi ổn định, bật `RIVER_POLL_ONLY=true` ở development, staging, rồi production. Quan sát reconnect, readiness, queue latency, queue depth, và ba phép đo connection tại mỗi bước, thỏa **AC-3**, **AC-7**, **AC-8**, **AC-10**, **AC-12**.

## Consequences

### Positive

1. Mỗi instance giải phóng hai `LISTEN` session dài hạn. Mức giảm total physical connection phụ thuộc `DB_MIN_CONNS` và tải.
2. Bill và Group dùng chung một cơ chế reconnect và observability.
3. River tương thích với môi trường không hỗ trợ `LISTEN/NOTIFY` để đánh thức queue.

### Negative

1. Job mới có thêm tối đa khoảng một polling window trước khi được nhận.
2. Polling tạo query đều đặn khi queue rảnh.
3. Shared listener là failure domain chung của hai channel. Disconnect buộc cả hai loại SSE client reconnect để phục hồi trạng thái.
4. Khi River leader dừng, poll only có thể mất tối đa khoảng năm giây để một client khác nhận leadership.

### Neutral

1. Không đổi schema hoặc API.
2. River vẫn dùng cùng pool cho query và worker transaction.
3. River có thể tiếp tục gửi `NOTIFY` khi enqueue dù poll only đã bỏ notifier listener session.

## Migration plan

**Strategy**: Hai phase độc lập với rollback bằng cấu hình cho River.

### Phases

1. Triển khai shared listener, giữ `RIVER_POLL_ONLY=false`, và đo mức giảm một `LISTEN` session.
2. Bật `RIVER_POLL_ONLY=true` theo từng môi trường và đo mức giảm notifier session cùng queue latency.

### Rollback

1. Shared listener rollback bằng revert phase một. Không chạy listener cũ và mới đồng thời vì sẽ phát trùng event.
2. River rollback bằng `RIVER_POLL_ONLY=false` rồi restart process, không cần đổi code hoặc migration.

### Risks

1. Handler chậm có thể trì hoãn channel còn lại. Thiết kế giới hạn handler ở decode và publish không blocking.
2. Poll interval quá nhỏ làm tăng database load. Validation giữ interval không thấp hơn cooldown và mặc định là một giây.
3. Poll interval quá lớn làm OCR và notification có cảm giác chậm. Metric queue wait phải được theo dõi trước khi tăng interval.
4. PostgreSQL không lưu notification trong thời gian listener disconnect. Đóng local SSE subscription buộc client dùng recovery flow thay vì tiếp tục trên stream đã mất event.

## Follow up

1. Sau khi production ổn định, đánh giá giảm `DB_MAX_CONNS` và `RIVER_WORKER_COUNT` dựa trên connection peak và queue depth thực tế.

## Rationale

Reasoning and options: see [rationale.md](rationale.md).
