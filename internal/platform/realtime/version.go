package realtime

import (
	"strconv"
	"strings"
)

type AppVersion struct {
	Major   int
	Minor   int
	Patch   int
	Build   int
	Unknown bool
}

func ParseAppVersion(raw string) AppVersion {
	value := strings.TrimSpace(raw)
	if value == "" || strings.EqualFold(value, "unknown") {
		return AppVersion{Unknown: true}
	}
	plus := strings.Split(value, "+")
	if len(plus) != 2 {
		return AppVersion{Unknown: true}
	}
	parts := strings.Split(plus[0], ".")
	if len(parts) != 3 {
		return AppVersion{Unknown: true}
	}
	major, err1 := parseNonNegative(parts[0])
	minor, err2 := parseNonNegative(parts[1])
	patch, err3 := parseNonNegative(parts[2])
	build, err4 := parseNonNegative(plus[1])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return AppVersion{Unknown: true}
	}
	return AppVersion{Major: major, Minor: minor, Patch: patch, Build: build}
}

func (v AppVersion) Class(minimum AppVersion) string {
	if v.Unknown {
		return "unknown"
	}
	if minimum.Unknown {
		return "legacy"
	}
	if compareAppVersion(v, minimum) >= 0 {
		return "supported"
	}
	return "legacy"
}

func compareAppVersion(a, b AppVersion) int {
	switch {
	case a.Major != b.Major:
		return a.Major - b.Major
	case a.Minor != b.Minor:
		return a.Minor - b.Minor
	case a.Patch != b.Patch:
		return a.Patch - b.Patch
	default:
		return a.Build - b.Build
	}
}

func parseNonNegative(raw string) (int, error) {
	if raw == "" || strings.ContainsAny(raw, "+-") {
		return 0, strconv.ErrSyntax
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}
