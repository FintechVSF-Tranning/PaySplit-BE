package domain

import "errors"

var (
	ErrInvalidInput       = errors.New("invalid authentication input")
	ErrEmailAlreadyExists = errors.New("email already exists")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserNotFound       = errors.New("user not found")
)
