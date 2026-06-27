package jsonl

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func writeJSONL(t *testing.T, lines ...string) string {
	t.Helper()

	path := t.TempDir() + "/test.jsonl"
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestScanFileMultipleRecords(t *testing.T) {
	path := writeJSONL(t,
		`{"id":1,"name":"alice"}`,
		`{"id":2,"name":"bob"}`,
		`{"id":3,"name":"carol"}`,
	)

	var records []json.RawMessage

	err := ScanFile(path, func(raw json.RawMessage) error {
		records = append(records, raw)
		return nil
	})
	if err != nil {
		t.Fatalf("ScanFile returned error: %v", err)
	}

	if len(records) != 3 {
		t.Fatalf("got %d records, want 3", len(records))
	}

	for i, want := range []string{`"alice"`, `"bob"`, `"carol"`} {
		var obj struct {
			Name string `json:"name"`
		}

		if err := json.Unmarshal(records[i], &obj); err != nil {
			t.Fatalf("record %d unmarshal error: %v", i, err)
		}

		if obj.Name != strings.Trim(want, `"`) {
			t.Errorf("record %d name = %q, want %q", i, obj.Name, want)
		}
	}
}

func TestScanFileEmpty(t *testing.T) {
	path := writeJSONL(t)
	called := false

	err := ScanFile(path, func(json.RawMessage) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("ScanFile returned error: %v", err)
	}

	if called {
		t.Error("callback called on empty file")
	}
}

func TestScanFileBlankLinesSkipped(t *testing.T) {
	path := writeJSONL(t,
		`{"a":1}`,
		"",
		`{"a":2}`,
		"",
		"",
		`{"a":3}`,
		"",
	)

	var count int

	err := ScanFile(path, func(json.RawMessage) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("ScanFile returned error: %v", err)
	}

	if count != 3 {
		t.Errorf("got %d calls, want 3", count)
	}
}

func TestScanFileNotFound(t *testing.T) {
	err := ScanFile("/nonexistent/path/file.jsonl", func(json.RawMessage) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestScanFileCallbackErrorStops(t *testing.T) {
	sentinel := errors.New("stop")
	path := writeJSONL(t,
		`{"i":1}`,
		`{"i":2}`,
		`{"i":3}`,
	)

	var count int

	err := ScanFile(path, func(raw json.RawMessage) error {
		count++

		var obj struct {
			I int `json:"i"`
		}

		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}

		if obj.I == 2 {
			return sentinel
		}

		return nil
	})

	if !errors.Is(err, sentinel) {
		t.Fatalf("got error %v, want sentinel", err)
	}

	if count != 2 {
		t.Errorf("got %d calls, want 2", count)
	}
}

func TestScanFileLargeRecord(t *testing.T) {
	big := strings.Repeat("x", 200*1024)
	path := writeJSONL(t, `{"data":"`+big+`"}`)

	var got json.RawMessage

	err := ScanFile(path, func(raw json.RawMessage) error {
		got = raw
		return nil
	})
	if err != nil {
		t.Fatalf("ScanFile returned error: %v", err)
	}

	var obj struct {
		Data string `json:"data"`
	}

	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(obj.Data) != len(big) {
		t.Errorf("data length = %d, want %d", len(obj.Data), len(big))
	}
}

func TestScanFileNestedJSON(t *testing.T) {
	path := writeJSONL(t,
		`{"outer":{"inner":{"value":42}},"arr":[1,2,3]}`,
		`{"meta":{"tags":["a","b"],"nested":{"deep":true}}}`,
	)

	var records []json.RawMessage

	err := ScanFile(path, func(raw json.RawMessage) error {
		records = append(records, raw)
		return nil
	})
	if err != nil {
		t.Fatalf("ScanFile returned error: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}

	var first struct {
		Outer struct {
			Inner struct {
				Value int `json:"value"`
			} `json:"inner"`
		} `json:"outer"`
		Arr []int `json:"arr"`
	}

	if err := json.Unmarshal(records[0], &first); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}

	if first.Outer.Inner.Value != 42 {
		t.Errorf("first.Outer.Inner.Value = %d, want 42", first.Outer.Inner.Value)
	}

	if len(first.Arr) != 3 {
		t.Errorf("len(first.Arr) = %d, want 3", len(first.Arr))
	}
}

func TestScanFileSingleRecord(t *testing.T) {
	path := writeJSONL(t, `{"only":true}`)

	var (
		count int
		got   json.RawMessage
	)

	err := ScanFile(path, func(raw json.RawMessage) error {
		count++

		got = raw

		return nil
	})
	if err != nil {
		t.Fatalf("ScanFile returned error: %v", err)
	}

	if count != 1 {
		t.Errorf("got %d calls, want 1", count)
	}

	var obj struct {
		Only bool `json:"only"`
	}

	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if !obj.Only {
		t.Error("obj.Only = false, want true")
	}
}

func TestScanFileWhitespaceOnlyLinesSkipped(t *testing.T) {
	path := writeJSONL(t,
		`{"a":1}`,
		"   ",
		"\t\t",
		`{"a":2}`,
		"  \t  ",
		`{"a":3}`,
	)

	var count int

	err := ScanFile(path, func(json.RawMessage) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("ScanFile returned error: %v", err)
	}

	if count != 3 {
		t.Errorf("got %d calls, want 3", count)
	}
}

func TestScanFileCustomMaxLineBytes(t *testing.T) {
	big := strings.Repeat("x", 5*1024*1024)
	path := writeJSONL(t, `{"data":"`+big+`"}`)

	err := ScanFile(path, func(json.RawMessage) error {
		return nil
	}, 1024*1024)
	if err == nil {
		t.Fatal("expected error for line exceeding custom maxLineBytes")
	}
}

func TestScanFileCustomMaxLineBytesLargeEnough(t *testing.T) {
	big := strings.Repeat("x", 512)
	path := writeJSONL(t, `{"data":"`+big+`"}`)

	var count int

	err := ScanFile(path, func(json.RawMessage) error {
		count++
		return nil
	}, 4*1024*1024)
	if err != nil {
		t.Fatalf("ScanFile returned error: %v", err)
	}

	if count != 1 {
		t.Errorf("got %d calls, want 1", count)
	}
}
