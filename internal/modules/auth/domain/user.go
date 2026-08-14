package domain

import "time"

type User struct {
	ID           string
	Email        string
	DisplayName  string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
}
