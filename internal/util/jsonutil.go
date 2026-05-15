package util

import "encoding/json"

func MustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return `{"error":"json marshal failed"}`
	}
	return string(data)
}

func MustJSONIndent(v any, prefix, indent string) string {
	data, err := json.MarshalIndent(v, prefix, indent)
	if err != nil {
		return `{"error":"json marshal failed"}`
	}
	return string(data)
}
