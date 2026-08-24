package domain

import "errors"

var (
	ErrInvalidInput         = errors.New("invalid input")
	ErrNotificationNotFound = errors.New("notification not found")
)
