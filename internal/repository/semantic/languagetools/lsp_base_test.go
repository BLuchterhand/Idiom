package languagetools_test

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseLSPLocations(t *testing.T) {
	selectionRange := LSPRange{
		Start: LSPPosition{Line: 2, Character: 4},
		End:   LSPPosition{Line: 2, Character: 9},
	}
	targetRange := LSPRange{
		Start: LSPPosition{Line: 1, Character: 0},
		End:   LSPPosition{Line: 3, Character: 0},
	}

	tests := []struct {
		name    string
		raw     string
		want    []LSPLocation
		wantErr bool
	}{
		{
			name: "null",
			raw:  "null",
		},
		{
			name: "single location",
			raw:  `{"uri":"file:///tmp/a.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}}`,
			want: []LSPLocation{{
				URI: "file:///tmp/a.go",
				Range: LSPRange{
					Start: LSPPosition{Line: 1, Character: 2},
					End:   LSPPosition{Line: 1, Character: 5},
				},
			}},
		},
		{
			name: "location list",
			raw:  `[{"uri":"file:///tmp/a.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}},{"uri":"file:///tmp/b.go","range":{"start":{"line":6,"character":7},"end":{"line":6,"character":8}}}]`,
			want: []LSPLocation{
				{
					URI: "file:///tmp/a.go",
					Range: LSPRange{
						Start: LSPPosition{Line: 1, Character: 2},
						End:   LSPPosition{Line: 1, Character: 5},
					},
				},
				{
					URI: "file:///tmp/b.go",
					Range: LSPRange{
						Start: LSPPosition{Line: 6, Character: 7},
						End:   LSPPosition{Line: 6, Character: 8},
					},
				},
			},
		},
		{
			name: "single link falls back to target range",
			raw:  marshalTestJSON(t, LSPLocationLink{TargetURI: "file:///tmp/target.go", TargetRange: targetRange}),
			want: []LSPLocation{{URI: "file:///tmp/target.go", Range: targetRange}},
		},
		{
			name: "link list uses selection range",
			raw: marshalTestJSON(t, []LSPLocationLink{
				{TargetURI: "file:///tmp/target.go", TargetRange: targetRange, TargetSelectionRange: selectionRange},
				{TargetURI: "file:///tmp/other.go", TargetRange: targetRange, TargetSelectionRange: selectionRange},
			}),
			want: []LSPLocation{
				{URI: "file:///tmp/target.go", Range: selectionRange},
				{URI: "file:///tmp/other.go", Range: selectionRange},
			},
		},
		{
			name:    "unsupported payload",
			raw:     `{"targetRange":{"start":{"line":1,"character":0},"end":{"line":1,"character":1}}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLSPLocations(json.RawMessage(tt.raw), "test-server")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("parseLSPLocations error: %v", err)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseLSPLocations() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func marshalTestJSON(t *testing.T, value any) string {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test json: %v", err)
	}

	return string(raw)
}
