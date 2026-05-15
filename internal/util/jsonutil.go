package util

import (
	"encoding/json"
)

func MarshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func JSONInt(v map[string]any, key string, fallback int) int {
	raw, ok := v[key]
	if !ok {
		return fallback
	}
	switch n := raw.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	}
	return fallback
}

func JSONString(v map[string]any, key string, fallback string) string {
	raw, ok := v[key]
	if !ok {
		return fallback
	}
	if s, ok := raw.(string); ok {
		return s
	}
	return fallback
}

func JSONBool(v map[string]any, key string, fallback bool) bool {
	raw, ok := v[key]
	if !ok {
		return fallback
	}
	if b, ok := raw.(bool); ok {
		return b
	}
	return fallback
}
