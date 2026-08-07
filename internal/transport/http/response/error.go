package response

import "net/http"

type ErrorBody struct {
	Error string `json:"error"`
}

func Error(w http.ResponseWriter, status int, message string) {
	panic("TODO: implement response.Error")
}
