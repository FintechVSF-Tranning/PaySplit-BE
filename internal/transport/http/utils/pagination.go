package utils

import (
	"net/http"
	"strconv"

	"github.com/brpaz/lib-go/pagination"
)

// ParseOffsetPager reads "page" and "page_size" query params and returns a
// pagination.OffsetPager with both values clamped to valid ranges
// (page >= 1, 1 <= page_size <= pagination.MaxPageSize).
func ParseOffsetPager(r *http.Request) pagination.OffsetPager {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	return pagination.NewOffsetPager(page, pageSize)
}
