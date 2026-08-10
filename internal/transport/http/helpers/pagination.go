package helpers

import (
	"net/http"
	"strconv"

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

// WritePaginated writes a paginated list response using the shared JSON writer.
func WritePaginated[T any](w http.ResponseWriter, status int, page pagination.Page[T]) error {
	return WriteJSON(w, status, PaginatedData[T]{
		Items: page.Items,
		Meta: PaginationMeta{
			Page:       page.PageNumber,
			PageSize:   page.PageSize,
			TotalItems: page.Total,
			TotalPages: page.TotalPages(),
		},
	})
}

// ParseOffsetPager reads "page" and "page_size" query params and returns a
// pagination.OffsetPager with both values clamped to valid ranges
// (page >= 1, 1 <= page_size <= pagination.MaxPageSize).
func ParseOffsetPager(r *http.Request) pagination.OffsetPager {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	return pagination.NewOffsetPager(page, pageSize)
}
