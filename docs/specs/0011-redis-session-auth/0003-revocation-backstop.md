# 0003. Revocation ordering and suspension backstop

## Summary

Thứ tự ghi Redis so với Postgres được quyết riêng cho từng call site, không đồng nhất, vì mỗi chỗ hỏng theo một hướng khác nhau. Ngoài ra, việc khóa tài khoản mất đi tuyến phòng thủ từng chạy miễn phí trên mọi request, nên phải bù bằng một job bền vững.

## Requirements

1. Thứ tự ghi được quyết theo từng call site và được ghi chú ngay tại chỗ.
2. Đặt lại mật khẩu tuyệt đối không được chạm Redis trước khi OTP được xác minh.
3. Đổi mật khẩu không ghi Redis.
4. Mọi lần khóa tài khoản đều enqueue job purge trong cùng transaction thu hồi.
5. Job purge phải idempotent và an toàn khi chạy trễ.
6. Job retry theo nhịp riêng của nó, không dùng nhịp dọn dẹp hai mươi bốn giờ của `AUTH_CLEANUP_INTERVAL_HOURS`.

## Decision

### Thứ tự ghi theo từng call site

| # | Call site | Thứ tự | Nếu hỏng giữa chừng | Hướng hỏng |
|---|---|---|---|---|
| 1 | Đăng xuất | Redis trước, Postgres sau | Hàng audit còn chưa đánh dấu revoke, tự lành ở lần tạo phiên sau | đóng |
| 2 | Đặt lại mật khẩu | Postgres trước, Redis sau | Phiên cũ còn sống sau khi đổi mật khẩu | mở, cần job |
| 3 | Đổi mật khẩu | không ghi Redis | không áp dụng | không áp dụng |
| 4 | Đăng nhập | Postgres trước, Redis sau | Thiết bị cũ còn sống | mở, cần job |
| 5 | Khóa tài khoản | Postgres trước, Redis sau | Người bị khóa vẫn dùng được app tới hết TTL | mở, job bắt buộc |

### Vì sao đặt lại mật khẩu không được ghi Redis trước

Đây là điểm phản trực giác nhất. Luồng đặt lại mật khẩu commit transaction rồi mới trả lỗi khi OTP sai hoặc hết lượt thử. Nếu xóa Redis trước khi biết OTP có đúng không, bất kỳ ai biết email của nạn nhân đều có thể gửi OTP sai để đá họ ra khỏi app. Đó là một lỗ DoS không cần xác thực.

### Vì sao đổi mật khẩu không ghi Redis

Câu lệnh hiện có là `UPDATE ... WHERE user_id=$1 AND id<>$2`, kết hợp với `uq_sessions_one_active_per_user` nên danh sách phiên bị đổi luôn rỗng. Phần thu hồi các phiên khác đã là code chết từ trước.

Nếu cài Redis theo kiểu chung cho mọi call site, nghĩa là đọc con trỏ theo user rồi xóa, thì chỗ này sẽ đá chính người vừa đổi mật khẩu ra khỏi app, ngược hẳn ý định của điều kiện `id<>$2`.

### Rò rỉ ở đường đăng xuất và cách bù

`NOTIFY` nằm trong transaction. Ghi Redis trước mà transaction sau đó hỏng thì không control nào phát ra, và SSE đang mở tiếp tục chạy tới hết tuổi kết nối tối đa. Bù bằng cách đọc `sid` trước khi xóa, và nếu transaction hỏng thì publish `session.ended` bù trên pool ngoài transaction. Việc publish bù phải được gate bằng việc thực sự xóa được một key Redis, không phải bằng số hàng Postgres.

### Tuyến phòng thủ bị gỡ

Câu truy vấn xác thực cũ `JOIN users` với `AND u.status='active'` trên mọi request. Nghĩa là quản trị viên khóa tài khoản thì request kế tiếp chết ngay, kể cả khi lệnh `UPDATE sessions` khớp không hàng nào.

Sau thay đổi, middleware chỉ đọc Redis và không bao giờ chạm `users.status` nữa. Lệnh `DEL` trên Redis trở thành cơ chế cưỡng chế duy nhất cho việc khóa tài khoản. Một lần ghi Redis hỏng nghĩa là người bị khóa vẫn dùng app bình thường cho tới khi TTL tự hết.

Vì vậy backstop bền vững là điều kiện để không tạo hồi quy bảo mật, không phải một tối ưu hóa.

### River purge job

Hệ thống đã có `*river.Client[pgx.Tx]` và `InsertTx` đã có test. Không cần bảng outbox mới: job được enqueue ngay trong chính transaction thu hồi, nên nó tồn tại khi và chỉ khi transaction commit, đúng ngữ nghĩa mà `pg_notify` đang dựa vào.

1. Job `session_redis_purge` mang `user_id` và danh sách sid, chạy script thu hồi theo user.
2. Bắt buộc ở call site khóa tài khoản. Nên có ở đặt lại mật khẩu và đăng nhập.
3. `InsertOpts` đặt `MaxAttempts: 10` và không đặt retry policy riêng, nên job dùng backoff mặc định của River. Backoff đó tăng theo lũy thừa bậc bốn của số lần thử, nên các lượt cuối cách nhau hàng giờ và toàn bộ cửa sổ retry kéo dài khoảng một ngày. Đây là khoảng cách so với ý định ban đầu, xem Ghi chú kiểm chứng bên dưới.
4. Nếu lệnh `DEL` trực tiếp hỏng thì API quản trị trả 500 để lỗi hiện ra. Trạng thái Postgres đã bền, job sẽ hội tụ.
5. Ghi log khi job thực sự xóa được, vì điều đó báo hiệu lần gọi trực tiếp đã lỡ và Redis đang chập chờn.

### Lỗ hổng đã biết: thu hồi lần hai không làm gì

Lệnh thu hồi là `UPDATE sessions SET revoked_at=now(), ... WHERE user_id=$1 AND revoked_at IS NULL RETURNING id`, và toàn bộ đường thu hồi Redis được lái bằng danh sách sid mà lệnh này trả về.

Hệ quả: nếu lần khóa đầu tiên đã đánh dấu hàng `sessions` là revoked nhưng lệnh `DEL` hỏng và job cũng cạn lượt thử, thì lần khóa thứ hai khớp không hàng nào. Danh sách sid rỗng làm `EnqueueTx` thoát sớm và làm script thu hồi lặp không vòng nào. Thao tác khóa lại không có tác dụng gì, và key Redis cũ sống tới hết TTL.

Đây đúng là tính chất tự lành mà điều kiện `AND u.status='active'` từng cung cấp miễn phí, và là lý do backstop hiện tại chưa bịt kín được lỗ hổng nó sinh ra để bịt.

Hướng sửa đề xuất: ở đường khóa tài khoản, thu hồi vô điều kiện theo user, nghĩa là đọc con trỏ `user_session:<user_id>` rồi xóa cả con trỏ lẫn bản ghi phiên mà nó trỏ tới, không phụ thuộc vào danh sách sid. Việc khớp `sid` vẫn giữ nguyên cho đường job retry, vì ở đó nó mới là thứ chống giết nhầm phiên mới.

## Build plan

1. Định nghĩa job args, worker, và enqueuer trong `internal/modules/auth/jobs/session_purge.go`.
2. Sửa transaction khóa tài khoản để trả thêm danh sách sid đã thu hồi, và enqueue job trong cùng transaction.
3. Khai báo port một method trong `admin/usecase` để admin không phải import `auth/usecase`.
4. Gọi thu hồi trực tiếp sau khi repository commit, trả 500 khi hỏng.
5. Đăng ký worker vào registry của River.
6. Sắp xếp lại thứ tự ghi ở từng call site theo bảng trên, kèm chú thích tại chỗ.

## Rollback

Không rollback riêng phần backstop được: bỏ nó đi là để lại hồi quy bảo mật đã mô tả ở trên. Rollback chỉ có nghĩa ở mức toàn bộ spec 0011.

## Ghi chú kiểm chứng

Không kiểm chứng được backstop bằng cách tạm dừng Redis. Redis chết thì chính quản trị viên cũng không xác thực được, request trả 401 ngay ở middleware và transaction không bao giờ chạy. Cửa sổ lỗi thật hẹp hơn dự tính: Redis phải sống lúc middleware chạy rồi chết đúng lúc `DEL`. Cách kiểm chứng đúng là đẩy thẳng job vào bảng `river_job` rồi quan sát nó hội tụ.
