package response

import (
	"net/http"

	"github.com/brpaz/lib-go/pagination"
)

type PaginationMeta struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	TotalItems int64 `json:"total_items"`
	TotalPages int   `json:"total_pages"`
}

type PaginatedData[T any] struct {
	Items []T            `json:"items"`
	Meta  PaginationMeta `json:"meta"`
}

func Paginated[T any](w http.ResponseWriter, status int, page pagination.Page[T]) {
	JSON(w, status, PaginatedData[T]{
		Items: page.Items,
		Meta: PaginationMeta{
			Page:       page.PageNumber,
			PageSize:   page.PageSize,
			TotalItems: page.Total,
			TotalPages: page.TotalPages(),
		},
	})
}
