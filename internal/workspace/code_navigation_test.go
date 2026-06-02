package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
)

func TestCodeNavigationSpecAcceptsDefinitionAliases(t *testing.T) {
	cases := []struct {
		name       string
		capability string
		wantName   string
		wantCap    Capability
	}{
		{name: "definition", capability: "definition", wantName: "definition", wantCap: CapDefinition},
		{name: "find definition", capability: "find_definition", wantName: "definition", wantCap: CapDefinition},
		{name: "find definitions", capability: "find_definitions", wantName: "definition", wantCap: CapDefinition},
		{name: "hyphen definition", capability: "find-definition", wantName: "definition", wantCap: CapDefinition},
		{name: "space definition", capability: "find definition", wantName: "definition", wantCap: CapDefinition},
		{name: "find references", capability: "find_references", wantName: "references", wantCap: CapReferences},
		{name: "hyphen references", capability: "find-references", wantName: "references", wantCap: CapReferences},
		{name: "space references", capability: "find references", wantName: "references", wantCap: CapReferences},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := codeNavigationSpecFor(tc.capability)
			if err != nil {
				t.Fatalf("codeNavigationSpecFor(%q): %v", tc.capability, err)
			}
			if spec.Name != tc.wantName || spec.Capability != tc.wantCap {
				t.Fatalf("codeNavigationSpecFor(%q) = %#v, want name=%q capability=%q", tc.capability, spec, tc.wantName, tc.wantCap)
			}
		})
	}
}

func TestCodeNavigateFindDefinitionAcceptsSymbolPattern(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/navalias\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(root, "internal", "transcript", "debuglog.go"), `package transcript

type DebugLogMode string

const DebugLogModeRedacted DebugLogMode = "redacted"
`)

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:       "code_navigate",
		Capability: "find_definition",
		Symbol:     "DebugLogMode",
		Language:   "go",
	})

	if result.Error != "" {
		t.Fatalf("code_navigate find_definition should route from symbol, got error: %s\n%s", result.Error, result.Output)
	}
	for _, want := range []string{"capability: definition", "routed_tool: go_definition", "route_summary: Resolved Go definition"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("code_navigate output missing %q:\nsummary=%s\noutput=\n%s", want, result.Summary, result.Output)
		}
	}
}
