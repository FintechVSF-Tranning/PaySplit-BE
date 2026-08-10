package router

import (
	"net/http"

	authhttp "paysplit-backend/internal/modules/auth/delivery/http"
)

func New(authHandler *authhttp.Handler) http.Handler {
	panic("TODO: implement router.New")
}
