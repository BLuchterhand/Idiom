package languagetools_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
)

func TestCodeNavigationSpecAcceptsDefinitionAliases(t *testing.T) {
	cases := []struct {
		name       string
		capability string
		wantName   string
		wantCap    language.Capability
	}{
		{name: "definition", capability: "definition", wantName: "definition", wantCap: language.CapDefinition},
		{name: "find definition", capability: "find_definition", wantName: "definition", wantCap: language.CapDefinition},
		{name: "find definitions", capability: "find_definitions", wantName: "definition", wantCap: language.CapDefinition},
		{name: "hyphen definition", capability: "find-definition", wantName: "definition", wantCap: language.CapDefinition},
		{name: "space definition", capability: "find definition", wantName: "definition", wantCap: language.CapDefinition},
		{name: "find references", capability: "find_references", wantName: "references", wantCap: language.CapReferences},
		{name: "hyphen references", capability: "find-references", wantName: "references", wantCap: language.CapReferences},
		{name: "space references", capability: "find references", wantName: "references", wantCap: language.CapReferences},
		{name: "extract body", capability: "extract_body", wantName: "extract_body", wantCap: language.CapExtractBody},
		{name: "full body", capability: "full_body", wantName: "extract_body", wantCap: language.CapExtractBody},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := languagetools.CodeNavigationSpecFor(tc.capability)
			if err != nil {
				t.Fatalf("languagetools.CodeNavigationSpecFor(%q): %v", tc.capability, err)
			}

			if spec.Name != tc.wantName || spec.Capability != tc.wantCap {
				t.Fatalf("languagetools.CodeNavigationSpecFor(%q) = %#v, want name=%q capability=%q", tc.capability, spec, tc.wantName, tc.wantCap)
			}
		})
	}
}

func TestCodeNavigationCapabilityListMatchesSpecs(t *testing.T) {
	names := languagetools.CodeNavigationCapabilityNames()
	if len(names) == 0 {
		t.Fatal("expected advertised code navigation capabilities")
	}

	for _, name := range names {
		spec, err := languagetools.CodeNavigationSpecFor(name)
		if err != nil {
			t.Fatalf("advertised capability %q did not resolve: %v", name, err)
		}

		if spec.Name != name {
			t.Fatalf("advertised capability %q resolved as %q", name, spec.Name)
		}
	}

	_, err := languagetools.CodeNavigationSpecFor("not_a_capability")
	if err == nil {
		t.Fatal("expected unknown capability to be rejected")
	}

	for _, name := range names {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("unknown-capability error omitted advertised capability %q: %s", name, err.Error())
		}
	}

	if strings.Join(names, ", ") != languagetools.CodeNavigationCapabilityList() {
		t.Fatalf("capability list helper drifted from names: names=%#v list=%q", names, languagetools.CodeNavigationCapabilityList())
	}
}

func TestCodeNavigateFindDefinitionAcceptsSymbolPattern(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/navalias\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(root, "internal", "transcript", "debuglog.go"), `package transcript

type DebugLogMode string

const DebugLogModeRedacted DebugLogMode = "redacted"
`)

	mgr := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	result := mgr.Execute(context.Background(), workspace.ToolRequest{
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
