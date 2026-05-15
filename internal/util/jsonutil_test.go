package util

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMustJSON(t *testing.T) {
	t.Run("simple struct", func(t *testing.T) {
		type s struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}
		got := MustJSON(s{Name: "Alice", Age: 30})
		want := `{"name":"Alice","age":30}`
		if got != want {
			t.Errorf("MustJSON(simple) = %q, want %q", got, want)
		}
	})

	t.Run("nested struct", func(t *testing.T) {
		type inner struct {
			Value int `json:"value"`
		}
		type outer struct {
			Label string `json:"label"`
			Inner inner  `json:"inner"`
		}
		got := MustJSON(outer{Label: "x", Inner: inner{Value: 42}})
		want := `{"label":"x","inner":{"value":42}}`
		if got != want {
			t.Errorf("MustJSON(nested) = %q, want %q", got, want)
		}
	})

	t.Run("slice", func(t *testing.T) {
		got := MustJSON([]string{"a", "b", "c"})
		want := `["a","b","c"]`
		if got != want {
			t.Errorf("MustJSON(slice) = %q, want %q", got, want)
		}
	})

	t.Run("map", func(t *testing.T) {
		got := MustJSON(map[string]int{"x": 1, "y": 2})
		var m map[string]int
		if err := json.Unmarshal([]byte(got), &m); err != nil {
			t.Fatalf("MustJSON(map) produced invalid JSON: %v", err)
		}
		if m["x"] != 1 || m["y"] != 2 {
			t.Errorf("MustJSON(map) = %q, want x=1, y=2", got)
		}
	})

	t.Run("nil", func(t *testing.T) {
		got := MustJSON(nil)
		if got != "null" {
			t.Errorf("MustJSON(nil) = %q, want %q", got, "null")
		}
	})

	t.Run("empty struct", func(t *testing.T) {
		got := MustJSON(struct{}{})
		if got != "{}" {
			t.Errorf("MustJSON(empty struct) = %q, want %q", got, "{}")
		}
	})

	t.Run("special characters in string", func(t *testing.T) {
		got := MustJSON("line1\nline2\t\"quoted\"")
		want := `"line1\nline2\t\"quoted\""`
		if got != want {
			t.Errorf("MustJSON(special) = %q, want %q", got, want)
		}
	})
}

func TestMustJSONIndent(t *testing.T) {
	t.Run("indented output", func(t *testing.T) {
		type s struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}
		got := MustJSONIndent(s{Name: "Bob", Age: 25}, "", "  ")
		if !strings.Contains(got, "  \"name\"") {
			t.Errorf("MustJSONIndent missing indented field: %q", got)
		}
		if !strings.HasPrefix(got, "{\n") {
			t.Errorf("MustJSONIndent should start with {\\n, got %q", got)
		}
	})

	t.Run("empty prefix and indent", func(t *testing.T) {
		type s struct {
			A int `json:"a"`
		}
		got := MustJSONIndent(s{A: 1}, "", "")
		want := "{\n\"a\": 1\n}"
		if got != want {
			t.Errorf("MustJSONIndent with empty indent = %q, want %q", got, want)
		}
	})

	t.Run("round-trip with json.Unmarshal", func(t *testing.T) {
		type s struct {
			X int `json:"x"`
		}
		indented := MustJSONIndent(s{X: 99}, "", "  ")
		var parsed s
		if err := json.Unmarshal([]byte(indented), &parsed); err != nil {
			t.Fatalf("MustJSONIndent produced invalid JSON: %v\noutput: %q", err, indented)
		}
		if parsed.X != 99 {
			t.Errorf("round-trip x = %d, want 99", parsed.X)
		}
	})
}
