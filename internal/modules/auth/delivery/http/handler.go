package http

import (
	"errors"
	"net/http"

	"paysplit-backend/internal/modules/auth/domain"
	"paysplit-backend/internal/modules/auth/usecase"
	"paysplit-backend/internal/transport/http/helpers"
)

// Handler là HTTP adapter của module auth: đọc request, gọi usecase và chuyển
// kết quả hoặc lỗi nghiệp vụ thành HTTP response.
type Handler struct {
	service *usecase.Service
}

// NewHandler tạo auth handler từ service đã được bootstrap khởi tạo.
func NewHandler(service *usecase.Service) *Handler {
	if service == nil {
		panic("auth handler service must not be nil")
	}
	return &Handler{service: service}
}

// Register tiếp nhận HTTP request đăng ký và chuyển dữ liệu sang auth usecase.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	panic("TODO: implement Handler.Register")
}

// Login tiếp nhận thông tin đăng nhập và trả access token khi xác thực thành công.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := helpers.ReadJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	output, err := h.service.Login(r.Context(), usecase.LoginInput{
		Email:    request.Email,
		Password: request.Password,
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			writeError(w, http.StatusBadRequest, "email and password are required")
		case errors.Is(err, domain.ErrInvalidCredentials):
			// Dùng cùng một response cho email không tồn tại và mật khẩu sai để
			// không làm lộ tài khoản đã đăng ký.
			writeError(w, http.StatusUnauthorized, "invalid email or password")
		default:
			writeError(w, http.StatusInternalServerError, "unable to log in")
		}
		return
	}

	// Ánh xạ sang response DTO để PasswordHash không bao giờ xuất hiện trong API.
	writeJSON(w, http.StatusOK, authResponse{
		User: userResponse{
			ID:          output.User.ID,
			Email:       output.User.Email,
			DisplayName: output.User.DisplayName,
			Role:        output.User.Role,
			CreatedAt:   output.User.CreatedAt,
		},
		AccessToken: output.AccessToken,
		TokenType:   output.TokenType,
		ExpiresIn:   output.ExpiresIn,
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	if err := helpers.WriteJSON(w, status, data); err != nil {
		http.Error(w, "failed to write response", http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	if err := helpers.WriteError(w, status, message); err != nil {
		http.Error(w, "failed to write response", http.StatusInternalServerError)
	}
}
