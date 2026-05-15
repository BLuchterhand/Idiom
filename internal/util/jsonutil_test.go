package util

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarshalIndent(t *testing.T) {
	got, err := MarshalIndent(map[string]int{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": 1\n}"
	if string(got) != want {
		t.Errorf("MarshalIndent = %q, want %q", got, want)
	}
}

func TestMarshalIndentNested(t *testing.T) {
	v := map[string]any{
		"outer": map[string]any{
			"inner": []int{1, 2},
		},
	}
	got, err := MarshalIndent(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Error("expected non-empty output")
	}
}

func TestMarshalIndentNil(t *testing.T) {
	got, err := MarshalIndent(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "null" {
		t.Errorf("MarshalIndent(nil) = %q, want %q", got, "null")
	}
}

func TestMarshalIndentEmptySlice(t *testing.T) {
	got, err := MarshalIndent([]int{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "[]" {
		t.Errorf("MarshalIndent([]) = %q, want %q", got, "[]")
	}
}

func TestMarshalIndentUsesTwoSpaces(t *testing.T) {
	got, err := MarshalIndent(map[string]string{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(got), "  ") {
		t.Error("expected two-space indentation")
	}
	if contains(string(got), "    ") {
		t.Error("should not have four-space indentation for single nesting level")
	}
}

func TestJSONInt(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]any
		key      string
		fallback int
		want     int
	}{
		{"missing key", map[string]any{}, "x", -1, -1},
		{"nil map", nil, "x", -1, -1},
		{"float64 from json", map[string]any{"n": float64(42)}, "n", 0, 42},
		{"int type", map[string]any{"n": int(42)}, "n", 0, 42},
		{"json.Number", map[string]any{"n": json.Number("42")}, "n", 0, 42},
		{"string value", map[string]any{"n": "42"}, "n", -1, -1},
		{"bool value", map[string]any{"n": true}, "n", -1, -1},
		{"nil value", map[string]any{"n": nil}, "n", -1, -1},
		{"negative float64", map[string]any{"n": float64(-7)}, "n", 0, -7},
		{"zero float64", map[string]any{"n": float64(0)}, "n", -1, 0},
		{"large float64", map[string]any{"n": float64(999999)}, "n", 0, 999999},
		{"fractional truncation", map[string]any{"n": float64(3.7)}, "n", 0, 3},
		{"invalid json.Number", map[string]any{"n": json.Number("abc")}, "n", -1, -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := JSONInt(tc.data, tc.key, tc.fallback)
			if got != tc.want {
				t.Errorf("JSONInt(%v, %q, %d) = %d, want %d", tc.data, tc.key, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestJSONIntRoundTripsFromJSON(t *testing.T) {
	raw := []byte(`{"count": 15}`)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	got := JSONInt(m, "count", -1)
	if got != 15 {
		t.Errorf("JSONInt after json.Unmarshal = %d, want 15", got)
	}
}

func TestJSONString(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]any
		key      string
		fallback string
		want     string
	}{
		{"missing key", map[string]any{}, "x", "fb", "fb"},
		{"nil map", nil, "x", "fb", "fb"},
		{"string present", map[string]any{"s": "hello"}, "s", "", "hello"},
		{"empty string", map[string]any{"s": ""}, "s", "fb", ""},
		{"int value", map[string]any{"s": 42}, "s", "fb", "fb"},
		{"bool value", map[string]any{"s": true}, "s", "fb", "fb"},
		{"nil value", map[string]any{"s": nil}, "s", "fb", "fb"},
		{"slice value", map[string]any{"s": []any{1, 2}}, "s", "fb", "fb"},
		{"unicode string", map[string]any{"s": "日本語"}, "s", "", "日本語"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := JSONString(tc.data, tc.key, tc.fallback)
			if got != tc.want {
				t.Errorf("JSONString(%v, %q, %q) = %q, want %q", tc.data, tc.key, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestJSONBool(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]any
		key      string
		fallback bool
		want     bool
	}{
		{"missing key", map[string]any{}, "x", true, true},
		{"nil map", nil, "x", false, false},
		{"true value", map[string]any{"b": true}, "b", false, true},
		{"false value", map[string]any{"b": false}, "b", true, false},
		{"int value", map[string]any{"b": 1}, "b", false, false},
		{"string value", map[string]any{"b": "true"}, "b", false, false},
		{"nil value", map[string]any{"b": nil}, "b", true, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := JSONBool(tc.data, tc.key, tc.fallback)
			if got != tc.want {
				t.Errorf("JSONBool(%v, %q, %v) = %v, want %v", tc.data, tc.key, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestJSONExtractorsRoundTripFromJSON(t *testing.T) {
	raw := []byte(`{"name":"test","count":10,"active":true}`)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}

	if got := JSONString(m, "name", ""); got != "test" {
		t.Errorf("JSONString name = %q, want %q", got, "test")
	}
	if got := JSONInt(m, "count", -1); got != 10 {
		t.Errorf("JSONInt count = %d, want 10", got)
	}
	if got := JSONBool(m, "active", false); got != true {
		t.Errorf("JSONBool active = %v, want true", got)
	}
}

func TestJSONExtractorsIgnoreWrongKey(t *testing.T) {
	m := map[string]any{"name": "alice"}
	if got := JSONInt(m, "name", -1); got != -1 {
		t.Errorf("JSONInt on string key = %d, want fallback", got)
	}
	if got := JSONBool(m, "name", false); got != false {
		t.Errorf("JSONBool on string key = %v, want fallback", got)
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

var _ = contains

func BenchmarkMarshalIndent(b *testing.B) {
	v := map[string]any{
		"name":   "test",
		"values": []int{1, 2, 3},
		"nested": map[string]string{"k": "v"},
	}
	for b.Loop() {
		_, err := MarshalIndent(v)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONInt(b *testing.B) {
	m := map[string]any{"n": float64(42)}
	for b.Loop() {
		JSONInt(m, "n", 0)
	}
}
