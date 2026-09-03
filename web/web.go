package web

import "embed"

// AdminFS nhúng toàn bộ tài nguyên tĩnh của Admin Portal vào Go binary.
//
//go:embed admin/*
var AdminFS embed.FS
