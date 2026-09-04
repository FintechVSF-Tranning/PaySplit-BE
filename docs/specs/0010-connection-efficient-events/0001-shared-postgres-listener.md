# 0001. Shared PostgreSQL listener

## Summary

Bill và Group dùng một PostgreSQL connection để nhận hai channel. Platform dispatch payload thô theo channel, còn mỗi module giữ kiểu event, validation, subscriber buffer, và quy tắc publish riêng.

## Requirements

1. Một connection chạy cả `LISTEN bill_events` và `LISTEN group_events`.
2. Event cùng channel giữ đúng thứ tự nhận.
3. Startup chỉ ready sau khi đăng ký đủ hai channel. Runtime disconnect chuyển readiness sang degraded và reconnect đăng ký lại đủ hai channel.
4. Bill payload yêu cầu `BillID != uuid.Nil` và `Type` không rỗng. Group payload yêu cầu `GroupID != uuid.Nil`, `Version > 0`, và `Type` không rỗng.
5. Lỗi payload chỉ bỏ một notification, không log raw payload, và không làm reconnect listener.
6. Khi listener disconnect, local SSE subscription được đóng để Bill phục hồi bằng snapshot và Group phục hồi bằng version fencing cùng `/sync`.
7. Bill giữ subscriber buffer `16`; Group giữ subscriber buffer `32`.
8. API và SSE contract không đổi.

## Decision

Tạo listener dùng chung tại `internal/platform/database/notification_listener.go`, với registry handler bất biến. Dispatch chạy đồng bộ vì handler chỉ decode và publish không blocking. Cách này giữ thứ tự Group version và không tạo dependency từ platform vào module.

Bill export `billhttp.NotificationChannel`, tiếp tục dùng pool cho outbound `pg_notify`, và không còn listener loop. Group export `grouphttp.NotificationChannel`, giữ decode và publish, và không còn giữ `pgxpool.Pool`. Bootstrap sở hữu lifecycle của listener chung.

Reconnect backoff bắt đầu `500 ms`, nhân đôi tới tối đa `30 s`, có jitter từ `80%` đến `120%`, và reset sau `30 s` healthy liên tục. Clean shutdown chạy `UNLISTEN *` trước khi release. Nếu setup chỉ hoàn tất một phần, wait lỗi, hoặc cleanup lỗi, underlying connection bị đóng để session có trạng thái `LISTEN` không trở lại pool.

## Build plan

1. Định nghĩa listener, handler registry, initial readiness gate, runtime degraded state, reconnect backoff, bounded shutdown, và metrics.
2. Tách Bill decode, semantic validation, và publish thành handler, giữ subscriber buffer `16`.
3. Tách Group decode, semantic validation, và publish thành handler, giữ subscriber buffer `32`.
4. Export channel constant từ hai module, wire một listener trong bootstrap, và xóa hai loop cũ.
5. Đóng local SSE subscription khi listener disconnect để kích hoạt recovery flow hiện có.
6. Thêm unit test và PostgreSQL integration test cho routing, ordering, invalid payload, reconnect, partial `LISTEN`, `UNLISTEN *`, pool contamination, readiness, recovery, và shutdown timeout.
7. Đo riêng số `LISTEN` session, pinned pool slot, và total physical session trước và sau.

## Rollback

Revert wiring về hai listener cũ. Không chạy hai kiến trúc cùng lúc vì cùng event có thể được publish hai lần trong một instance.

