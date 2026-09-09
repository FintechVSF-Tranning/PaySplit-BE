# Rationale: 0011 Redis session auth

## Context

PaySplit dùng JWT access token HS256 sống mười lăm phút mang claim `sid`, kèm refresh token rotation bảy ngày có phát hiện tái sử dụng. Nhưng middleware xác thực gọi `ValidateSession` để truy vấn Postgres trên mọi request. Câu truy vấn đó là:

```sql
SELECT u.id, u.role, s.id FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.id=$1 AND s.user_id=$2 AND s.revoked_at IS NULL
  AND s.expires_at>$3 AND u.status='active'
```

Nghĩa là hệ thống đang trả đủ chi phí round trip của session based auth, trong khi vẫn mang toàn bộ độ phức tạp của JWT, mà chưa bao giờ hưởng lợi ích stateless. Hai cơ chế cùng tồn tại nhưng chỉ một cái thực sự phán quyết.

Độ phức tạp đó không nằm yên. Ở phía Flutter, bộ làm mới phiên mang sẵn một comment mô tả lớp lỗi của chính nó: hai luồng làm mới chạy song song bị máy chủ coi là tái sử dụng token, và người dùng bị đá ra khỏi app dù không làm gì sai. Đó là chi phí trực tiếp của việc giữ hai credential.

Câu truy vấn trên còn chứa một tính chất ít ai để ý. Điều kiện `AND u.status='active'` đọc lại trạng thái tài khoản trên mọi request, nên nó là một tuyến phòng thủ độc lập với `revoked_at`. Kể cả khi lệnh thu hồi phiên của quản trị viên khớp không hàng nào, request kế tiếp của người bị khóa vẫn chết. Bất kỳ thiết kế thay thế nào cũng phải trả lời được: cái gì thay thế tuyến phòng thủ đó.

Ràng buộc thứ hai đến từ tầng realtime. `realtime.UserEnvelope.TargetSIDs` là `[]uuid.UUID`, `EncodeSessionEnded` nhận `[]uuid.UUID`, và SSE handler gọi `uuid.Parse` trên giá trị session ID lấy từ context. Định danh phiên bắt buộc phải là UUID.

Bối cảnh cuối cùng quyết định chiến lược chuyển đổi: hệ thống chưa có người dùng production.

## Options considered

### Option 1. Giữ JWT nhưng bỏ `ValidateSession`

Đi stateless thật sự: tin chữ ký, không tra cơ sở dữ liệu trên mỗi request. Đây là cách rẻ nhất và đúng với ý định ban đầu của JWT.

**Pros**:
1. Bỏ được hoàn toàn một round trip cơ sở dữ liệu trên mọi request.
2. Không thêm hạ tầng nào.
3. Thay đổi code nhỏ nhất trong ba phương án.

**Cons**:
1. Mất khả năng thu hồi tức thì. Người bị khóa tài khoản vẫn dùng được app cho tới khi access token hết hạn.
2. Mất luôn tuyến phòng thủ `u.status='active'`, mà không có gì thay thế.
3. Vẫn giữ nguyên toàn bộ refresh rotation, tức là giữ nguyên lớp lỗi ở phía Flutter.
4. Force logout qua SSE mất ý nghĩa, vì đóng stream không làm token ngừng hợp lệ.

### Option 2. Session ID đục lưu thẳng trên Postgres

Bỏ JWT và refresh rotation, nhưng không thêm hạ tầng mới. Credential là chuỗi đục, tra thẳng bảng `sessions`.

**Pros**:
1. Bỏ được JWT và refresh rotation, tức là đạt phần lớn lợi ích về độ đơn giản.
2. Không thêm hạ tầng phải vận hành.
3. Giữ nguyên được tuyến phòng thủ `u.status='active'` vì vẫn `JOIN users`.
4. Thu hồi tức thì.

**Cons**:
1. Mỗi request vẫn là một query Postgres, đúng bằng chi phí đang trả hôm nay. Không giải quyết được phần chi phí.
2. Nếu muốn TTL trượt thì phải tự cài bằng một lệnh `UPDATE` trên đường đọc, tức là biến mọi request thành một lệnh ghi cơ sở dữ liệu. Với hạn cố định như hệ thống cũ thì không có nhược điểm này.
3. Phiên hết hạn phải dọn bằng worker thay vì để hạ tầng tự làm.

### Option 3. Session ID đục lưu trên Redis

Credential đục, Redis là nguồn phán quyết, `sessions` hạ vai trò xuống audit.

**Pros**:
1. Thu hồi tức thì bằng một lệnh `DEL`.
2. Giữ chi phí xác thực ở đúng một round trip, nhờ gộp đọc và gia hạn TTL vào một Lua script.
3. TTL trượt là cơ chế sẵn có của Redis, không cần lệnh ghi trên đường đọc và không cần worker dọn phiên.
4. Bỏ được toàn bộ refresh rotation cùng lớp lỗi của nó ở phía Flutter.

**Cons**:
1. Redis trở thành dependency cứng trên đường đi của mọi request có xác thực.
2. Mất tuyến phòng thủ `u.status='active'`, phải bù lại bằng một job bền vững chứ không được bỏ qua.
3. Thêm một hạ tầng phải vận hành và giám sát.
4. `role` bị cache, nên mọi thay đổi vai trò phải kéo theo thu hồi phiên.

### Option 4. Giữ nguyên hiện trạng

Chấp nhận trả chi phí session based mà vẫn mang độ phức tạp JWT.

**Pros**:
1. Không có rủi ro chuyển đổi.

**Cons**:
1. Không giải quyết vấn đề nào, và giữ nguyên lớp lỗi làm mới token song song ở phía người dùng.

## Rationale

Option 3 được chọn vì hai lý do, cả hai đều bám vào forces cụ thể trong Context.

Thứ nhất là thu hồi tức thì mà không tốn round trip Postgres. Đây là điểm Option 1 và Option 2 không cùng lúc đạt được: Option 1 có chi phí thấp nhưng mất thu hồi, Option 2 có thu hồi nhưng giữ nguyên chi phí. Redis đạt cả hai vì `DEL` là thu hồi, và vì script đọc gộp luôn lệnh gia hạn TTL nên vẫn đúng một round trip, bằng đúng số round trip đang trả cho Postgres hôm nay. Không hồi quy hiệu năng là điều kiện để việc chuyển đổi này không phải đánh đổi gì về tốc độ.

Thứ hai là bỏ được toàn bộ lớp refresh rotation. Lớp này không chỉ là code thừa, nó đang sinh lỗi thật cho người dùng: hai luồng làm mới song song bị coi là tái sử dụng token rồi đá người dùng ra. Cả Option 1 lẫn Option 4 đều giữ nguyên lớp đó. Việc giảm từ hai credential xuống một là thứ xóa lớp lỗi này tận gốc, chứ không phải vá nó.

Đánh đổi được chấp nhận một cách có ý thức. Redis thành dependency cứng, nên nó nằm trong `/health/ready` và bootstrap fail fast khi cấu hình sai, để lỗi hiện ra ở đúng chỗ thay vì biểu hiện thành việc người dùng không đăng nhập được. Việc thêm một hạ tầng phải vận hành là chi phí thật, được bù bằng việc bỏ đi một bảng, một endpoint, một package, và khoảng hai trăm dòng ở phía Flutter.

Đánh đổi nặng nhất là mất tuyến phòng thủ `u.status='active'`. Đây không phải một dòng bị xóa vô hại: nó từng khiến việc khóa tài khoản tự lành khi lệnh thu hồi phiên hỏng. Sau thay đổi, cơ chế cưỡng chế thu lại còn một lệnh ghi Redis, và một lần ghi lỡ để người bị khóa dùng app tới hết TTL. Vì vậy River purge job được coi là điều kiện để không tạo hồi quy bảo mật, không phải một tối ưu hóa. Job được enqueue bằng `InsertTx` trong chính transaction thu hồi nên nó tồn tại khi và chỉ khi transaction commit, đúng ngữ nghĩa mà `pg_notify` đang dựa vào, và không cần thêm bảng outbox.

Việc giữ `sessions.id` làm định danh phiên, tách khỏi credential, là thứ khiến toàn bộ tầng realtime không phải sửa dòng nào. Đây là quyết định về phạm vi ảnh hưởng nhiều hơn là về bảo mật, và nó thu hẹp đáng kể rủi ro của một thay đổi vốn chạm vào đường đi của mọi request.

Cần nói thẳng một điều để bản ghi này không tự tâng bốc: TTL trượt là một yêu cầu mới do chính thiết kế này đưa vào, không phải một yêu cầu có sẵn. Hệ thống cũ dùng hạn cố định. Nếu bỏ yêu cầu TTL trượt thì Option 2 với hạn cố định sẽ không thêm hạ tầng nào, giữ nguyên tuyến phòng thủ `u.status='active'` miễn phí, và vẫn bỏ được refresh rotation. Nói cách khác, TTL trượt và việc mất tuyến phòng thủ đó là hai thứ được chọn, không phải hai thứ bị hoàn cảnh ép.

Chiến lược cắt thẳng chỉ hợp lệ nhờ một fact trong Context: chưa có người dùng production. Nếu đã có, phải chọn strangler và middleware phải chấp nhận đồng thời hai loại credential trong thời gian chuyển đổi.

## Related decisions

1. `docs/specs/0001-auth-account-v1/` quy định danh tính, phiên, và refresh rotation ban đầu. Spec này thay thế phần cơ chế phiên và refresh của nó, còn phần đăng ký, xác minh email, và khôi phục mật khẩu vẫn giữ nguyên.
2. `docs/specs/0005-admin-v1/` quy định luồng khóa tài khoản và thu hồi phiên nguyên tử. Spec này đổi cơ chế cưỡng chế của luồng đó và thêm backstop bền vững.
3. `docs/specs/0006-notification-queue-v1/` quy định River, transactional enqueue, và graceful shutdown mà job purge dựa vào.
4. `docs/specs/0009-group-realtime-sync-v1/` quy định SSE theo phiên người dùng và force logout. Spec này giữ nguyên hợp đồng đó nhờ tách credential khỏi định danh phiên.
5. `docs/specs/0010-connection-efficient-events/` quy định shared listener và River poll only mà đường `session.ended` chạy qua.
