# Module notification & background queue v1

Tài liệu này giúp bạn hiểu cấu trúc và luồng xử lý của module Notification và nền tảng River Queue. Xem [spec 0006](specs/0006-notification-queue-v1/index.md) để biết đầy đủ các quyết định thiết kế và acceptance criteria.

## Tổng quan chức năng

Module Notification & Background Queue chịu trách nhiệm:
1. **In-App Notifications**: Lưu trữ và quản lý lịch sử thông báo trong ứng dụng cho từng người dùng, hỗ trợ đếm số lượng thông báo chưa đọc, đánh dấu đã đọc một thông báo hoặc toàn bộ thông báo.
2. **Push Notifications (FCM)**: Gửi thông báo đẩy tức thời tới thiết bị di động của người dùng qua Firebase Cloud Messaging với các nội dung tiếng Việt được định dạng số tiền VND (ví dụ `1.500.000đ`).
3. **Background Job Processing (River Queue)**: Đẩy công việc gửi push notification vào hàng đợi bền vững (durable queue) chạy trực tiếp trên PostgreSQL 18, tự động retry khi gặp lỗi tạm thời (exponential backoff).
4. **Dead Token Pruning**: Tự động dọn dẹp và xóa các FCM token không còn hợp lệ (user gỡ ứng dụng) khỏi bảng `sessions`.

## Luồng gửi và xử lý thông báo

```text
Sự kiện nghiệp vụ (ví dụ nhắc nợ / chốt bill)
    ↓
notification.usecase.Service.SendToUser()
    ├── 1. Ghi In-App notification vào bảng `notifications`
    └── 2. Enqueue `NotificationJobArgs` vào River Queue
             ↓
River Worker (`send_notification`) bốc job
    ├── 1. Lấy token FCM của session đang hoạt động (`GetActiveFCMTokenByUserID`)
    ├── 2. Gọi FCM API (`SendToDevice`)
    └── 3. Xử lý kết quả:
             ├── Thành công: hoàn thành job
             ├── Token chết / không hợp lệ: xóa `fcm_token` trong `sessions` và hoàn tất
             └── Lỗi mạng / timeout tạm thời: trả lỗi để River tự động retry
```

## Các bảng dữ liệu

### `notifications`
Lưu trữ các thông báo trong ứng dụng (In-App notifications):
* `id` (UUID v7, khóa chính)
* `user_id` (UUID, khóa ngoại tham chiếu `users(id)` ON DELETE CASCADE)
* `type` (TEXT, ví dụ `payment_reminder`, `bill_finalized`, `payment_confirmed`, `group_invitation`, v.v.)
* `title` (TEXT, tiêu đề hiển thị)
* `body` (TEXT, nội dung thông báo)
* `payload` (JSONB, dữ liệu định tuyến mở rộng cho Flutter app)
* `read_at` (TIMESTAMPTZ, null nghĩa là chưa đọc)
* `created_at` (TIMESTAMPTZ, thời gian tạo)

Index tối ưu:
* `idx_notifications_unread`: `(user_id) WHERE read_at IS NULL` — tối ưu đếm và lọc chưa đọc.
* `idx_notifications_user_created`: `(user_id, created_at DESC)` — tối ưu phân trang lịch sử thông báo.

### `sessions.fcm_token`
Cột `fcm_token` trong bảng `sessions` lưu token thiết bị gắn với phiên đăng nhập hiện tại. Theo quy tắc từ spec 0001 (mỗi user có tối đa 1 phiên hoạt động không bị thu hồi), mỗi user có tối đa 1 active FCM token tại một thời điểm.

### Các bảng `river_*`
Được quản lý tự động bởi River Migrator (`riverpkg.AutoMigrate`) trong PostgreSQL để theo dõi hàng đợi, trạng thái retry và lock worker.

## API Endpoints

| Method | Đường dẫn | Chức năng | Quyền |
|---|---|---|---|
| `GET` | `/api/v1/notifications` | Lấy danh sách thông báo (phân trang `page`, `page_size`) | Live Session |
| `GET` | `/api/v1/notifications/unread-count` | Lấy số lượng thông báo chưa đọc | Live Session |
| `PATCH` | `/api/v1/notifications/{id}/read` | Đánh dấu 1 thông báo là đã đọc | Live Session |
| `PATCH` | `/api/v1/notifications/read-all` | Đánh dấu tất cả thông báo là đã đọc | Live Session |
| `PUT` | `/api/v1/users/me/fcm-token` | Cập nhật FCM token cho session hiện tại | Live Session |

## Graceful Shutdown & Resiliency

* Khi server tắt (SIGTERM/SIGINT), `bootstrap.App.Shutdown`:
  1. Dừng tiếp nhận request HTTP mới (`server.Shutdown`).
  2. Dừng và drain các River workers đang chạy (`riverClient.Stop`).
  3. Dừng các background cleanup workers.
  4. Đóng kết nối database pool (`db.Close`).
