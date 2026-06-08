package util

import (
	"slices"
	"sort"
	"strings"
)

func AppendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

func LimitSlice(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return append([]string(nil), values...)
	}
	return append([]string(nil), values[:limit]...)
}

func Unique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func Merge(groups ...[]string) []string {
	var merged []string
	for _, group := range groups {
		merged = append(merged, group...)
	}
	return Unique(merged)
}

func Sorted(values []string) []string {
	out := Unique(values)
	sort.Strings(out)
	return out
}

func Remove(values []string, remove []string) []string {
	if len(values) == 0 || len(remove) == 0 {
		return Unique(values)
	}
	removed := make(map[string]struct{}, len(remove))
	for _, value := range remove {
		value = strings.TrimSpace(value)
		if value != "" {
			removed[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := removed[value]; ok {
			continue
		}
		out = append(out, value)
	}
	return Unique(out)
}

func UniqueLimit(values []string, max int) []string {
	values = Unique(values)
	if max > 0 && len(values) > max {
		return append([]string(nil), values[:max]...)
	}
	return values
}

func Prefix(values []string, max int) []string {
	if max <= 0 || len(values) <= max {
		return values
	}
	return values[:max]
}

// EqualSlice reports whether two slices have the same elements in the same order.
func EqualSlice[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
