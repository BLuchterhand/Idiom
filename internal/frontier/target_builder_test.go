package frontier

import (
	"slices"
	"strings"
	"testing"

	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/workspace"
)

func TestPromptMentionedWorkspacePathsParsesAnchorsAndToolFields(t *testing.T) {
	got := PromptMentionedWorkspacePaths(`Update @internal/app/app.go and {"tool":"read_file","path":"internal/app/analysis_agents.go:12:3"}.`)
	want := []string{"internal/app/app.go", "internal/app/analysis_agents.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
}

func TestBuildTargetPacketDerivesAnchorsAndArchitectureHints(t *testing.T) {
	history := []llama.ChatMessage{{
		Role: "system",
		Content: "[architecture subagent summary]\n" +
			"Use this as initial orientation.\n\n" +
			"- Direction: split routing policy into internal/frontier/routing.go from internal/app/prompt_routing.go.\n" +
			"- Best target: internal/app/prompt_routing.go:1968",
	}}
	packet := BuildTargetPacket("go ahead and start", history)
	if packet.SourceLane != "architecture" {
		t.Fatalf("SourceLane = %q, want architecture", packet.SourceLane)
	}
	if !slices.Contains(packet.PrimaryPaths, "internal/app/prompt_routing.go") {
		t.Fatalf("PrimaryPaths = %#v, want architecture target", packet.PrimaryPaths)
	}
	if len(packet.Anchors) != 1 || packet.Anchors[0].Path != "internal/app/prompt_routing.go" || packet.Anchors[0].Line != 1968 {
		t.Fatalf("Anchors = %#v, want prompt_routing.go:1968", packet.Anchors)
	}
	if len(packet.StrategyHints) == 0 || !strings.Contains(packet.StrategyHints[0], "Direction:") {
		t.Fatalf("StrategyHints = %#v, want architecture direction", packet.StrategyHints)
	}
}

func TestTargetGuardTreatsGoFindFunctionAsScopedInspection(t *testing.T) {
	guard := NewTargetGuard(nil, TargetPacket{
		PrimaryPaths: []string{"internal/app/app.go"},
	}, nil)
	blocked := guard.PreExecute(workspace.ToolRequest{
		Tool:    "go_find_function",
		Path:    "internal/app/app.go",
		Pattern: "runImplementAgent",
	})
	if blocked != nil {
		t.Fatalf("expected go_find_function scoped to a primary path to pass, got %#v", blocked)
	}
	if !guard.targetConsumed {
		t.Fatal("expected go_find_function to consume the target packet")
	}
}

func TestStructuralAnchorGuardRequiresConcreteEvidenceBeforeWrites(t *testing.T) {
	packet := TargetPacket{
		SourceLane:    string(LaneArchitecture),
		PrimaryPaths:  []string{"internal/app/app.go"},
		StrategyHints: []string{"extract routing responsibility"},
	}
	guard := NewStructuralAnchorGuard(packet)
	if !guard.Required() {
		t.Fatal("expected structural guard to require an anchor for file-level architecture target")
	}
	if blocked := guard.PreExecute(workspace.ToolRequest{Tool: "edit_file", Path: "internal/app/app.go"}); blocked == nil {
		t.Fatal("expected write to be blocked before concrete anchor evidence")
	}
	guard.Observe(
		workspace.ToolRequest{Tool: "read_file", Path: "internal/app/app.go"},
		workspace.ToolResult{Tool: "read_file", Summary: "Read internal/app/app.go lines 220-260", Output: "func run() {}"},
	)
	if guard.Required() {
		t.Fatal("expected narrow read window to satisfy concrete anchor requirement")
	}
	if blocked := guard.PreExecute(workspace.ToolRequest{Tool: "edit_file", Path: "internal/app/app.go"}); blocked != nil {
		t.Fatalf("expected write to pass after anchor evidence, got %#v", blocked)
	}
}
