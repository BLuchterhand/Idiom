package mcpidentity

import "testing"

func TestNormalizeServerName(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "trimmed", value: " docs ", want: "docs"},
		{name: "empty", value: "  ", wantErr: true},
		{name: "reserved separator", value: "team/docs", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeServerName(test.value)
			if (err != nil) != test.wantErr {
				t.Fatalf("NormalizeServerName(%q) error = %v, wantErr %v", test.value, err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("NormalizeServerName(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestQualifiedToolNameRoundTrip(t *testing.T) {
	qualified := QualifiedToolName(" docs ", " summarize ")
	if qualified != "mcp:docs/summarize" {
		t.Fatalf("QualifiedToolName = %q", qualified)
	}

	server, tool, ok := ParseQualifiedToolName(qualified)
	if !ok || server != "docs" || tool != "summarize" {
		t.Fatalf("ParseQualifiedToolName = server:%q tool:%q ok:%v", server, tool, ok)
	}

	for _, invalid := range []string{"plain_tool", "mcp:only-server", "mcp:/tool", "mcp:server/"} {
		if _, _, ok := ParseQualifiedToolName(invalid); ok {
			t.Fatalf("ParseQualifiedToolName(%q) unexpectedly succeeded", invalid)
		}
	}
}
