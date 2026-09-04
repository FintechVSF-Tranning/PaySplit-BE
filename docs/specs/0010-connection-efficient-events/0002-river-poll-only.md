# 0002. River poll only

## Summary

River bỏ notifier listener connection và tìm job mới bằng polling. Mặc định một giây cân bằng giữa connection budget, database load, và độ trễ OCR hoặc notification.

## Requirements

1. `RIVER_POLL_ONLY=true` không tạo River notifier connection hoặc chạy `LISTEN`. River vẫn có thể gửi `NOTIFY` khi enqueue.
2. `RIVER_POLL_ONLY=false` là mặc định để rollout hai phase độc lập.
3. `RIVER_FETCH_POLL_INTERVAL_MS=1000` là mặc định.
4. Poll interval phải dương và không nhỏ hơn fetch cooldown mặc định `100 ms`.
5. Transactional enqueue, retry, scheduled jobs, periodic jobs, và graceful shutdown không đổi.
6. `RIVER_POLL_ONLY=false` khôi phục notifier sau khi restart.
7. Với queue idle, worker sẵn sàng, và database khỏe, commit đến lần fetch tiếp theo có miền lý thuyết từ `0 ms` đến dưới `1100 ms`. Test end to end phải thêm tolerance cho runtime và database.

## Decision

Mở rộng config nội bộ của River wrapper với `PollOnly` và `FetchPollInterval`. Giá trị được chuyển thẳng vào `river.Config`. Giữ `FetchCooldown` riêng vì nó điều khiển tốc độ fetch khi queue đang có việc, không phải nhịp polling khi queue rảnh.

Thêm `DB_APPLICATION_NAME`, mặc định `paysplit-api`, vào PostgreSQL connection config. Verification dùng một application name duy nhất cho instance thử nghiệm để `pg_stat_activity` đếm được cả pooled session và River session đã hijack.

## Build plan

1. Thêm `RIVER_POLL_ONLY` mặc định `false`, `RIVER_FETCH_POLL_INTERVAL_MS`, và `DB_APPLICATION_NAME` vào config loader, validation, `.env.example`, và README.
2. Mở rộng River wrapper và bootstrap wiring.
3. Thêm unit test cho default, override, và validation.
4. Thêm integration test chứng minh job được nhận bằng polling với timing tolerance và các loại job định kỳ vẫn chạy.
5. Log mode và poll interval khi startup.
6. Bật `RIVER_POLL_ONLY=true` theo từng môi trường và đo notifier session, total physical session, queue wait, và queue depth.

## Rollback

Đặt `RIVER_POLL_ONLY=false` rồi restart process. Không cần đổi code hoặc migration.

