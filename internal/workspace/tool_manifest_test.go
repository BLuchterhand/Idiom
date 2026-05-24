package workspace

import (
	"strings"
	"testing"
)

func TestBuildToolManifestSuppressesPlaceholderRequests(t *testing.T) {
	manifest := BuildToolManifest([]string{"submit_plan", "edit_file", "pwd"})

	byTool := map[string][]ToolManifestEntry{}
	for _, entry := range manifest {
		byTool[entry.Tool] = append(byTool[entry.Tool], entry)
	}

	if got := byTool["submit_plan"][0].Request; got != "" {
		t.Fatalf("submit_plan placeholder request should be suppressed, got %q", got)
	}
	for _, entry := range byTool["edit_file"] {
		if entry.Request != "" {
			t.Fatalf("edit_file placeholder request should be suppressed, got %q", entry.Request)
		}
	}
	if got := byTool["pwd"][0].Request; got == "" || strings.Contains(got, "relative/file") {
		t.Fatalf("concrete request should remain available, got %q", got)
	}
}

func TestRenderToolManifestInstructionsOmitsBlankRequestSlot(t *testing.T) {
	rendered := RenderToolManifestInstructions([]string{"submit_plan"})

	if strings.Contains(rendered, "submit_plan  (") {
		t.Fatalf("blank request should not render as a double-spaced slot:\n%s", rendered)
	}
	if strings.Contains(rendered, "relative/file") || strings.Contains(rendered, "one paragraph") {
		t.Fatalf("placeholder request text leaked into manifest instructions:\n%s", rendered)
	}
	if !strings.Contains(rendered, "- submit_plan (") {
		t.Fatalf("sanitized manifest should still name the tool and purpose:\n%s", rendered)
	}
}

func TestRenderToolManifestInstructionsSuppressesManifestWidePlaceholderRequests(t *testing.T) {
	rendered := strings.ToLower(RenderToolManifestInstructions(nil))
	requestText := renderedManifestRequestText(rendered)
	for _, forbidden := range []string{
		"symbol|text",
		`"pattern":"name"`,
		`"pattern":"funcname"`,
		`"pattern":"functionname"`,
		`"pattern":"typename"`,
		`"pattern":"constname"`,
		`"pattern":"interfacename"`,
		`"pattern":"classname`,
		`"pattern":"methodname"`,
		`"path":"file.go"`,
		`"path":"source.go"`,
		`"destination":"target.go"`,
		`"path":"src/file`,
		`"path":"src/pkg/file.py"`,
		`"path":"src/main/java/com/example/file.java"`,
		`"path":"include/file.h"`,
		`"path":"relative/dir"`,
		`"path":"optional/path"`,
		"session-001",
		"session.jsonl",
		"relative/file",
		"exact substring",
		"replacement text",
		"complete replacement",
		"one paragraph",
	} {
		if strings.Contains(requestText, forbidden) {
			t.Fatalf("placeholder request fragment %q leaked into rendered manifest requests:\n%s", forbidden, requestText)
		}
	}
	if !strings.Contains(requestText, `{"tool":"pwd"}`) {
		t.Fatalf("concrete non-placeholder request should still render:\n%s", rendered)
	}
}

func renderedManifestRequestText(rendered string) string {
	var b strings.Builder
	for _, line := range strings.Split(rendered, "\n") {
		idx := strings.Index(line, " {")
		if idx < 0 {
			continue
		}
		request := line[idx+1:]
		if end := strings.Index(request, " ("); end >= 0 {
			request = request[:end]
		}
		b.WriteString(request)
		b.WriteByte('\n')
	}
	return b.String()
}
