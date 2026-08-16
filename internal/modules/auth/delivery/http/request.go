package http

// registerRequest mô tả JSON đầu vào của endpoint đăng ký.
type registerRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

// loginRequest mô tả JSON đầu vào của endpoint đăng nhập.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}
