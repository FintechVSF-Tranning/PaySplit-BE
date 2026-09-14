# 0002. Redis session store

## Summary

Kho phiên nằm ở `internal/platform/session/`. Nó lưu hai key cho mỗi phiên, dùng mô hình TTL hai tầng gồm một hạn trượt và một trần cứng, và chạy mọi thao tác chạm nhiều key bằng Lua script để bảo đảm nguyên tử.

## Requirements

1. Key là SHA-256 của credential, không bao giờ là credential trần. Một bản dump Redis không đăng nhập lại được.
2. Sơ đồ key:

   ```text
   session:<sha256_hex>     HASH  { sid, user_id, role, absolute_exp }
                            TTL   = SESSION_IDLE_TTL   (trượt, gia hạn mỗi request)

   user_session:<user_id>   STRING = <sha256_hex>      (chỉ mục ngược để thu hồi theo user)
                            TTL    = SESSION_ABSOLUTE_TTL
   ```

3. `absolute_exp` là field trong hash, không phải TTL của key. TTL trượt vô hạn sẽ khiến phiên sống mãi, nên phải có một mốc cứng nằm ngoài cơ chế TTL.
4. TTL trượt phải được kẹp vào phần thời gian tuyệt đối còn lại.
5. Con trỏ mang TTL tuyệt đối, không dùng chung TTL với bản ghi phiên.
6. Xác thực một request tốn đúng một round trip.
7. `Get` trả lỗi không tìm thấy khi key không tồn tại hoặc khi đã quá hạn tuyệt đối.
8. Gói dùng sentinel `session.ErrNotFound` riêng, không dùng lỗi domain của module auth. Việc dịch sang ngôn ngữ nghiệp vụ thuộc tầng gọi.

## Decision

Bốn Lua script, không phải ba như thiết kế ban đầu dự tính.

**`createScript`** tạo phiên mới và giết phiên cũ trong một lượt. Nó đọc con trỏ cũ, xóa bản ghi phiên cũ nếu khác, rồi ghi bản ghi mới và đặt lại con trỏ.

> Lỗi phải tránh: chỉ ghi đè con trỏ mà không xóa bản ghi phiên cũ sẽ để lại một HASH mồ côi giữ nguyên TTL riêng của nó. Thiết bị cũ tiếp tục đăng nhập được, và vì không chỉ mục ngược nào trỏ tới nó nữa nên không cơ chế dọn nào tìm ra.

**`getScript`** xác thực và gia hạn TTL trượt trong một round trip. Nó đọc bốn field bằng `HMGET`, cưỡng chế trần tuyệt đối ngay trong Lua bằng cách xóa key khi `absolute_exp <= now`, rồi đặt lại TTL đã kẹp.

Cưỡng chế trần trong Lua thay vì chỉ so sánh ở phía Go là quyết định về bộ nhớ: cách chỉ so ở Go để bản ghi chết nằm lại chiếm chỗ cho tới hết TTL trượt.

Tách `HGETALL` và `EXPIRE` thành hai lệnh rời sẽ tốn hai round trip cho mỗi request có xác thực. Gộp vào Lua giữ đúng một, bằng số round trip đang trả cho Postgres, nên không hồi quy hiệu năng.

**`peekAndDeleteScript`** đọc rồi xóa, dùng cho đăng xuất. Nó phải đọc `sid` và `user_id` trước khi xóa, vì bên gọi cần hai giá trị đó để ghi audit và phát `session.ended`. Nó chỉ xóa con trỏ khi con trỏ còn trỏ đúng vào phiên này: nếu người dùng vừa đăng nhập lại ở nơi khác, con trỏ đã thuộc về phiên mới và xóa đi sẽ làm mất khả năng thu hồi theo user của phiên đó.

**`revokeUserSIDsScript`** thu hồi theo user và chỉ xóa khi `sid` của phiên đang sống nằm trong danh sách được yêu cầu. Việc khớp `sid` là thứ khiến script an toàn khi chạy trễ: River purge job có thể retry nhiều phút sau, và nếu trong lúc đó người dùng đã đăng nhập lại thì phiên mới mang `sid` khác nên không bị giết oan. Nhờ khớp theo `sid`, không cần thêm cột `sessions.token_hash`. Script cũng dọn con trỏ mồ côi khi gặp trường hợp bản ghi phiên đã hết hạn nhưng con trỏ còn.

Giá trị trả về của `getScript` và `peekAndDeleteScript` luôn theo đúng thứ tự `[sid, user_id, role, absolute_exp]` để phía Go đọc bằng chỉ số, thay vì `HGETALL` trả mảng phẳng phải tự ghép cặp.

## Build plan

1. Định nghĩa entity `Session` và sentinel `ErrNotFound`.
2. Viết bốn Lua script, nhúng bằng `redis.NewScript`.
3. Viết adapter Redis với `Create`, `Get`, `PeekAndDelete`, `RevokeUserSIDs`, băm bằng `domain.HashToken`.
4. Test unit bằng `miniredis`.
5. Test integration trên Redis thật theo house style: biến môi trường cộng `t.Skip`, không dùng build tag, tên file kết thúc bằng `_integration_test.go`, dùng `TEST_REDIS_URL`.
6. Các ca bắt buộc phủ: TTL trượt được gia hạn; trần tuyệt đối chặn dù key còn sống; tạo phiên mới xóa sạch bản ghi cũ và không để mồ côi; thu hồi không xóa nhầm phiên mới.

## Rollback

Gói này thuần và không có ai gọi cho tới khi đường xác thực được chuyển. Ở giai đoạn xây dựng, xóa gói là rollback đủ.
