// Package session lưu phiên đăng nhập trên Redis và là nguồn phán quyết duy nhất
// cho câu hỏi "credential này còn hiệu lực không".
//
// Hai khái niệm bị tách rời có chủ đích:
//
//   - credential: chuỗi đục 32 byte client cầm và gửi kèm mỗi request.
//   - SID: UUID định danh phiên, khớp với `sessions.id` bên Postgres.
//
// Tách như vậy vì tầng realtime ràng buộc session ID phải là UUID
// (realtime.UserEnvelope.TargetSIDs, sse_handler dùng uuid.Parse), nên chuỗi đục
// không thể đóng cả hai vai. Middleware đặt SID vào context, không bao giờ đặt
// credential.
package session

import (
	"errors"
	"time"
)

// ErrNotFound báo credential không tồn tại, đã bị thu hồi, hoặc đã quá hạn tuyệt
// đối. Gói này cố tình không dùng lỗi domain của module auth: nó là hạ tầng, còn
// việc dịch sang ngôn ngữ nghiệp vụ (ErrSessionRevoked) thuộc về tầng gọi.
var ErrNotFound = errors.New("session not found")

// Session là dữ liệu phiên nằm trên Redis.
//
// Role được cache ở đây để middleware không phải JOIN `users` mỗi request. Đánh
// đổi: mọi thay đổi `users.role` hoặc `users.status` PHẢI kéo theo thu hồi phiên,
// nếu không giá trị cũ sống tới hết TTL.
type Session struct {
	SID         string
	UserID      string
	Role        string
	AbsoluteExp time.Time
}
