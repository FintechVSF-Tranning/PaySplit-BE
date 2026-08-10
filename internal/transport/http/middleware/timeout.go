package middleware

import (
	"net/http"
	"time"
)

func Timeout(duration time.Duration) func(http.Handler) http.Handler {
	panic("TODO: implement middleware.Timeout")
}
