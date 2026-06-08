package stringset

import (
	"idiom/internal/platform/util"
	"strings"
)

func Unique(values []string) []string {
	return util.Unique(values)
}

func Merge(groups ...[]string) []string {
	return util.Merge(groups...)
}

func Sorted(values []string) []string {
	return util.Sorted(values)
}

func Remove(values []string, remove []string) []string {
	return util.Remove(values, remove)
}

func Limit(values []string, max int) []string {
	return util.UniqueLimit(values, max)
}

func Cap(values []string, max int) []string {
	return util.LimitSlice(values, max)
}

func Prefix(values []string, max int) []string {
	return util.Prefix(values, max)
}

func AppendUnique(values []string, additions ...string) []string {
	// Inline implementation retained since util.AppendUnique has a different
	// signature (single value, uses slices.Contains). This multi-value variant
	// builds a seen map once and appends in place.
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		values = append(values, value)
		seen[value] = struct{}{}
	}
	return values
}
