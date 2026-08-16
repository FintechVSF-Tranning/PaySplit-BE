package domain

import "errors"

var (
	// ErrInvalidInput cho biết dữ liệu đầu vào không đủ hoặc không hợp lệ.
	ErrInvalidInput = errors.New("invalid authentication input")
	// ErrEmailAlreadyExists cho biết email đăng ký đã thuộc về tài khoản khác.
	ErrEmailAlreadyExists = errors.New("email already exists")
	// ErrInvalidCredentials không tiết lộ email hay mật khẩu sai để tránh dò tài khoản.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrUserNotFound cho biết repository không tìm thấy người dùng được yêu cầu.
	ErrUserNotFound = errors.New("user not found")
)
