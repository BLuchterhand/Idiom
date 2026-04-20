package frontier

import (
	"slices"
	"strings"
	"testing"

	"qwen-bubbles/internal/llama"
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
