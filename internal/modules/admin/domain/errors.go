package domain

import "errors"

var (
	// ErrInvalidInput đánh dấu dữ liệu đầu vào không hợp lệ hoặc thiếu trường bắt buộc.
	ErrInvalidInput = errors.New("invalid input")

	// ErrAccountNotFound được trả về khi không tìm thấy tài khoản người dùng mục tiêu.
	ErrAccountNotFound = errors.New("account not found")

	// ErrCannotModifySelf ngăn admin tự đình chỉ, khóa hoặc thay đổi trạng thái tài khoản của chính mình.
	ErrCannotModifySelf = errors.New("cannot modify self account status")

	// ErrCannotModifyAdmin ngăn admin đình chỉ hoặc khóa tài khoản của một admin khác.
	ErrCannotModifyAdmin = errors.New("cannot modify another admin account status")

	// ErrInvalidStatusTransition đánh dấu hành động chuyển đổi trạng thái không hợp lệ (ví dụ từ pending_verification).
	ErrInvalidStatusTransition = errors.New("invalid status transition")

	// ErrReasonRequired đánh dấu trường lý do bắt buộc bị thiếu hoặc rỗng khi suspend/lock.
	ErrReasonRequired = errors.New("reason is required for status change")

	// ErrForbidden đánh dấu người gọi không có quyền thực hiện hành động.
	ErrForbidden = errors.New("forbidden")
)
