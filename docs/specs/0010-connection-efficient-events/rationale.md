# Rationale: 0010 Connection efficient events

## Context

Mỗi backend instance hiện có hai listener riêng cho Bill và Group. River tạo thêm notifier bằng một connection được tách khỏi pgx pool sau khi acquire. Ba session này sống lâu trên Supavisor session mode. Tổng physical session còn phụ thuộc `DB_MIN_CONNS`, `DB_MAX_CONNS`, tải HTTP, và tải worker nên không được suy ra chỉ từ pool limit.

Bill và Group đều cần PostgreSQL `LISTEN/NOTIFY`, nhưng cơ chế acquire, wait, reconnect, và shutdown đang bị lặp. River đã hỗ trợ poll only chính thức, nên notifier listener riêng không còn bắt buộc. Poll only có thể vẫn gửi `NOTIFY` khi enqueue, nhưng không giữ session để nhận notification.

Mục tiêu là giảm connection mà không đổi transactional enqueue, SSE contract, Group version fencing, hoặc tính bền vững của queue.

## Options considered

### Option 1. Giữ nguyên kiến trúc và chỉ giảm pool limit

Pool cap thấp hơn không loại các session sống lâu. Cách này còn tăng nguy cơ HTTP và worker chờ connection.

### Option 2. Chỉ bật River poll only

Cách này là thay đổi nhỏ nhất và bỏ một notifier session, nhưng Bill và Group vẫn giữ hai listener riêng cùng code lifecycle bị lặp.

### Option 3. Chỉ gộp Bill và Group

Cách này giảm một `LISTEN` session và loại code lặp, nhưng River vẫn giữ notifier listener session.

### Option 4. Shared listener và River poll only

Cách này giảm hai `LISTEN` session trên mỗi instance, giữ toàn bộ dữ liệu trong PostgreSQL, và có rollback riêng cho River. Đổi lại, job mới chịu thêm tối đa khoảng một polling window và database nhận query polling đều đặn.

Transaction pooling không phải một option thay thế trực tiếp vì `LISTEN/NOTIFY` cần session state. PaySplit vẫn cần direct hoặc session connection cho realtime ngay cả khi các query khác dùng transaction pooler.

## Rationale

Option 4 giải quyết đúng hai listener session dư thừa mà không thêm hạ tầng. Shared listener giữ nguyên module event types và public contract. River poll only là khả năng sẵn có của thư viện đang dùng, nên rủi ro thấp hơn việc tự xây queue wakeup.

Hai phần được triển khai riêng để mỗi bước có baseline, verification, và rollback độc lập. Shared listener không dùng feature flag vì chạy song song sẽ tạo event trùng. River dùng env flag, mặc định `false`, vì hai mode có thể thay thế nhau an toàn sau restart.

## Related decisions

1. `docs/specs/0003-bill-ocr-v1/` quy định Bill SSE và snapshot recovery.
2. `docs/specs/0006-notification-queue-v1/` quy định River, transactional enqueue, và graceful shutdown.
3. `docs/specs/0009-group-realtime-sync-v1/` quy định Group SSE, version fencing, và `/sync` recovery.

