package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

func TestUniqueDelegatedDiscoveryPathsNormalizesPacketArtifacts(t *testing.T) {
	paths := uniqueDelegatedDiscoveryPaths([]string{
		"internal/runtime/agent/runner.go``",
		"internal/runtime/agent/runner.go:648",
		"internal/runtime/agent/runner_child_tasks.go:93-94",
		"`internal/runtime/agent/answer_composer.go`",
	})
	want := []string{
		"internal/runtime/agent/runner.go",
		"internal/runtime/agent/runner_child_tasks.go",
		"internal/runtime/agent/answer_composer.go",
	}

	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("expected normalized delegated paths %#v, got %#v", want, paths)
	}
}

func TestDelegatedReadOnlyBootstrapRedirectsBroadInitialSearch(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "Explain how the harness uses sub-agents.\nNamed workspace scope: internal, cmd, prompts\nKey terms: harness, sub-agent, subagent",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Pattern: "*.go"},
	})

	if decision.Request.Tool != "discovery_query" || decision.Request.Purpose != "architecture_boundary" {
		t.Fatalf("expected bootstrap redirect to architecture discovery_query, got %#v", decision.Request)
	}

	if decision.Request.Path != "internal/runtime/agent" {
		t.Fatalf("expected bootstrap redirect to runtime agent implementation scope, got %#v", decision.Request)
	}

	for _, want := range []string{"sub[-_ ]?agent", "subagent"} {
		if !strings.Contains(decision.Request.Pattern, want) {
			t.Fatalf("expected bootstrap pattern to include %q, got %q", want, decision.Request.Pattern)
		}
	}
}

func TestDelegatedReadOnlyRepairsInvalidInitialDiscoveryPurpose(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "What can we add to our discovery tools in an effort at increasing their overall usefulness?\nNamed workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal\nKey terms: discovery tools, usefulness",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "tool_usefulness"},
	})

	if decision.Request.Tool != "discovery_query" || decision.Request.Purpose != "architecture_boundary" {
		t.Fatalf("expected invalid discovery purpose to be repaired to architecture_boundary, got %#v", decision.Request)
	}

	if decision.Request.Path != "internal/runtime/agent" || decision.Request.Pattern == "" || decision.Request.MaxDepth != 2 {
		t.Fatalf("expected repaired discovery query to carry scope/pattern/depth, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyPreservesValidDiscoveryUsefulnessPurposeAndPath(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "What can we add to our discovery tools in an effort at increasing their overall usefulness?\nNamed workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "history_context", Path: "internal/capabilities/workspace"},
	})

	if decision.Request.Tool != "discovery_query" || decision.Request.Purpose != "history_context" {
		t.Fatalf("expected valid discovery purpose to be preserved, got %#v", decision.Request)
	}

	if decision.Request.Path != "internal/capabilities/workspace" {
		t.Fatalf("expected valid discovery path to be preserved, got %#v", decision.Request)
	}

	for _, forbidden := range []string{"answer[-_ ]?composer", "synthesizeDelegatedReadOnlyAnswer", "readiness", "closeout"} {
		if strings.Contains(decision.Request.Pattern, forbidden) {
			t.Fatalf("expected usefulness pattern not to inject synthesis boundary %q, got %#v", forbidden, decision.Request)
		}
	}
}

func TestDelegatedReadOnlyRepairsSecurityPurposeWithoutSecurityObjective(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "What can we add to our discovery tools in an effort at increasing their overall usefulness?\nNamed workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "security_review", Path: "internal/runtime/agent"},
	})

	if decision.Request.Tool != "discovery_query" || decision.Request.Purpose != "architecture_boundary" {
		t.Fatalf("expected unsupported security_review to be repaired to architecture_boundary, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyRepairsDiscoveryPathOutsideDetailedNamedScope(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "What can we add to our discovery tools in an effort at increasing their overall usefulness?\nNamed workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "architecture_boundary", Path: "internal/frontier", Pattern: "discovery|tools"},
	})

	if decision.Request.Tool != "discovery_query" || decision.Request.Path != "internal/runtime/agent" {
		t.Fatalf("expected out-of-scope discovery path to use primary detailed named scope, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyRepeatedDiscoveryConsumesCandidateSource(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "Explain how this harness uses subagents.\nNamed workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal",
		},
	}
	results := []workspace.ToolResult{{
		Tool:   "discovery_query",
		Output: `{"evidence":[{"name":"text_anchor_search","summary":"implementation anchor: internal/runtime/agent/runner_child_tasks.go:42 runChildTask"},{"name":"text_anchor_search","summary":"implementation anchor: internal/runtime/agent/runner.go:300 task tool dispatch"}]}`,
	}}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Results: results,
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "architecture_boundary", Path: "internal/runtime/agent", Pattern: "subagent|task"},
	})

	if decision.Request.Tool != "read_file" {
		t.Fatalf("expected repeated discovery to consume a candidate source path, got %#v", decision.Request)
	}

	if !slices.Contains([]string{"internal/runtime/agent/runner_child_tasks.go", "internal/runtime/agent/runner.go"}, decision.Request.Path) ||
		decision.Request.StartLine != 1 || decision.Request.EndLine != 160 {

		t.Fatalf("expected bounded read_file on discovered source candidate, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyBootstrapFallsBackToSearchWithoutDiscoveryQuery(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"search_content", "read_file"},
			Objective:    "Explain how the harness uses sub-agents.\nNamed workspace scope: internal, cmd, prompts\nKey terms: harness, sub-agent, subagent",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Pattern: "*.go"},
	})

	if decision.Request.Tool != "search_content" {
		t.Fatalf("expected bootstrap redirect to search_content, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyBootstrapPrefersWorkspaceSymbolWhenAvailable(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"go_workspace_symbol", "search_content", "read_file"},
			Objective:    "Explain how `runChildTask` is used.\nNamed workspace scope: internal, cmd, prompts\nKey terms: runChildTask, child task",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Pattern: "*.go"},
	})

	if decision.Request.Tool != "go_workspace_symbol" || decision.Request.Pattern != "runChildTask" {
		t.Fatalf("expected bootstrap redirect to go_workspace_symbol for exact symbol, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyBootstrapPrefersWorkspaceSymbolOverDiscoveryForExactSymbolObjective(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "go_workspace_symbol", "search_content", "read_file"},
			Objective:    "Explain how `runChildTask` is used.\nNamed workspace scope: internal, cmd, prompts\nKey terms: runChildTask, child task",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Pattern: "*.go"},
	})

	if decision.Request.Tool != "go_workspace_symbol" || decision.Request.Pattern != "runChildTask" {
		t.Fatalf("expected bootstrap redirect to go_workspace_symbol for exact symbol even when discovery_query is available, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyBootstrapPrefersDiscoveryQueryOverWorkspaceSymbolForBroadObjective(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "go_workspace_symbol", "search_content", "read_file"},
			Objective:    "Explain how the harness uses sub-agents.\nNamed workspace scope: internal, cmd, prompts\nKey terms: harness, sub-agent, subagent",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Pattern: "*.go"},
	})

	if decision.Request.Tool != "discovery_query" || decision.Request.Purpose != "architecture_boundary" {
		t.Fatalf("expected broad bootstrap redirect to discovery_query, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyBootstrapDoesNotTreatPlainVerbAsExactSymbol(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "go_workspace_symbol", "search_content", "read_file"},
			Objective:    "How does this harness utilize sub-agents?\nNamed workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal\nKey terms: utilize, sub-agent, task, delegate",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_files", Path: "internal/runtime/agent", Pattern: "*.go"},
	})

	if decision.Request.Tool != "discovery_query" {
		t.Fatalf("expected broad verb/key-term objective to use discovery_query, got %#v", decision.Request)
	}

	if decision.Request.Pattern == "utilize" {
		t.Fatalf("plain verb should not be treated as an exact symbol probe: %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyBootstrapPatternIgnoresPromptGuidanceNoise(t *testing.T) {
	raw := "how does this harness utilize sub-agents\n" +
		"Named workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal\n" +
		"Start from source-of-truth implementation files before tests, README, or docs.\n" +
		"Return inspected paths, key findings, confidence, and named gaps."
	prompt := (&Runner{}).augmentReadOnlyChildPrompt(normalizeReadOnlyChildPrompt(raw))
	pattern := delegatedReadOnlyBootstrapPattern(prompt)

	if !strings.Contains(pattern, "sub[-_ ]?agents") {
		t.Fatalf("expected pattern to preserve the delegated question term, got %q", pattern)
	}

	for _, forbidden := range []string{"internal", "source", "truth", "inspected", "directly", "evidence"} {
		if strings.Contains(pattern, forbidden) {
			t.Fatalf("expected bootstrap pattern to ignore harness guidance term %q, got %q", forbidden, pattern)
		}
	}
}

func TestDelegatedReadOnlyTaskPromptUsesGenericImplementationScope(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"internal/evidence/transcript", "internal/surfaces/tui/app", "internal/runtime/agent"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	prompt := delegatedReadOnlyTaskPrompt("What does the debug log redacted mode do?", root)
	if strings.Contains(prompt, "internal/evidence/transcript, internal/surfaces/tui/app") {
		t.Fatalf("expected prompt scope not to special-case debug log features, got:\n%s", prompt)
	}

	if !strings.Contains(prompt, "Named workspace scope: internal/runtime/agent, internal") {
		t.Fatalf("expected generic implementation scope, got:\n%s", prompt)
	}
}

func TestPrepareReadOnlyChildPromptAddsDelegatedLikelySourceScope(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"internal/runtime/agent", "internal/capabilities/workspace"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	runner := &Runner{Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root})}
	prompt := runner.prepareReadOnlyChildPrompt("Inspect how this harness uses subagents. Focus on task tool implementation and child spawning.")

	if !strings.Contains(prompt, "Named workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal") {
		t.Fatalf("expected model-created child task to inherit delegated likely-source scope, got:\n%s", prompt)
	}
}

func TestPrepareReadOnlyChildPromptAddsPrimaryScopeWhenPromptNamesDocs(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"internal/runtime/agent", "internal/capabilities/workspace"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	runner := &Runner{Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root})}
	prompt := runner.prepareReadOnlyChildPrompt("Inspect docs/codebase-comprehension-memory-design.md and explain the discovery tool surface.")

	if !strings.Contains(prompt, "Named workspace scope: docs/codebase-comprehension-memory-design.md") {
		t.Fatalf("expected prompt to preserve named docs evidence, got:\n%s", prompt)
	}

	if !strings.Contains(prompt, "Named workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal") {
		t.Fatalf("expected docs-only prompt to inherit primary implementation scope, got:\n%s", prompt)
	}

	if got := delegatedReadOnlyBootstrapScope(prompt); got != "internal/runtime/agent" {
		t.Fatalf("expected bootstrap scope to prefer primary implementation scope over docs evidence, got %q from:\n%s", got, prompt)
	}
}

func TestDelegatedReadOnlyLikelySourceCandidatesSkipCmdWithoutEntrypointObjective(t *testing.T) {
	candidates := delegatedReadOnlyLikelySourceCandidates("Explain how this harness uses sub-agents.")
	for _, candidate := range candidates {
		if candidate == "cmd" {
			t.Fatalf("expected non-entrypoint architecture objective to skip cmd by default, got %#v", candidates)
		}
	}
}

func TestDelegatedReadOnlyLikelySourceCandidatesIncludeCmdForEntrypointObjective(t *testing.T) {
	candidates := delegatedReadOnlyLikelySourceCandidates("Explain which CLI entrypoints invoke delegated child tasks.")
	found := false

	for _, candidate := range candidates {
		if candidate == "cmd" {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("expected entrypoint objective to include cmd scope, got %#v", candidates)
	}
}

func TestDelegatedReadOnlyBootstrapPatternDoesNotInjectDelegateForDebugFeature(t *testing.T) {
	objective := strings.Join([]string{
		"What does the debug log redacted mode do?",
		"Scope guidance:",
		"- Answer the delegated question directly once the evidence is sufficient.",
		"Key terms: debug, redacted",
	}, "\n")
	pattern := delegatedReadOnlyBootstrapPattern(objective)

	if !strings.Contains(pattern, "debug") || !strings.Contains(pattern, "redacted") {
		t.Fatalf("expected debug/redacted pattern, got %q", pattern)
	}

	if strings.Contains(pattern, "delegate") || strings.Contains(pattern, "task") {
		t.Fatalf("debug log feature pattern should not inherit generic delegated-scope terms, got %q", pattern)
	}
}

func TestDelegatedReadOnlyBootstrapPatternForDiscoveryUsefulnessUsesQuestionTermsOnly(t *testing.T) {
	pattern := delegatedReadOnlyBootstrapPattern("What can we add to our discovery tools to increase their overall usefulness?")
	for _, want := range []string{"discovery", "tools", "increase"} {
		if !strings.Contains(pattern, want) {
			t.Fatalf("expected discovery usefulness pattern to preserve question term %q, got %q", want, pattern)
		}
	}

	for _, forbidden := range []string{"answer[-_ ]?composer", "synthesizeDelegatedReadOnlyAnswer", "readiness", "child[-_ ]?packet", "closeout", "comprehension", "frontier", "workspace|tool[-_ ]?surface"} {
		if strings.Contains(pattern, forbidden) {
			t.Fatalf("discovery usefulness pattern should not inject %q, got %q", forbidden, pattern)
		}
	}
}

func TestDelegatedReadOnlyDiscoveryUsefulnessBootstrapUsesDiscoveryQueryDespiteNamedFile(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			Objective:    "What can we add to our discovery tools to increase their overall usefulness? Also inspect internal/capabilities/workspace/tool_manifest.go.",
			AllowedTools: []string{"discovery_query", "read_file", "search_content"},
		},
	}
	redirected, ok := runner.delegatedReadOnlyBootstrapQueryRequest(nil, workspace.ToolRequest{
		Tool: "read_file",
		Path: "internal/capabilities/workspace/tool_manifest.go",
	})

	if !ok {
		t.Fatal("expected discovery usefulness bootstrap to prefer discovery_query")
	}

	if redirected.Tool != "discovery_query" {
		t.Fatalf("expected discovery_query redirect, got %#v", redirected)
	}
}

func TestDelegatedReadOnlyBootstrapPatternDoesNotInjectSubagentImplementationAliases(t *testing.T) {
	pattern := delegatedReadOnlyBootstrapPattern("Explain how this harness uses sub-agents.")
	for _, want := range []string{"explain", "sub[-_ ]?agents"} {
		if !strings.Contains(pattern, want) {
			t.Fatalf("expected subagent pattern to preserve question term %q, got %q", want, pattern)
		}
	}

	for _, want := range []string{"child[-_ ]?task", "task[-_ ]?tool", "delegate", "delegation"} {
		if !strings.Contains(pattern, want) {
			t.Fatalf("subagent pattern should include implementation alias %q, got %q", want, pattern)
		}
	}
}

func TestDelegatedReadOnlyDiscoveryRepairPreservesSubagentPurposeAndPath(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "delegated_read_only",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			Objective:         "Can you tell me how this harness uses subagents?",
		},
	}
	repaired, ok := runner.delegatedReadOnlyDiscoveryQueryRepairRequest(nil, workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "impact_analysis",
		Path:    ".",
	})

	if !ok {
		t.Fatal("expected delegated subagent discovery query to be repaired")
	}

	if repaired.Purpose != "impact_analysis" || repaired.Path != "." {
		t.Fatalf("expected valid discovery purpose/path to be preserved, got %#v", repaired)
	}

	for _, want := range []string{"tell", "subagents"} {
		if !strings.Contains(repaired.Pattern, want) {
			t.Fatalf("expected repaired query pattern to contain %q, got %q", want, repaired.Pattern)
		}
	}

	for _, want := range []string{"child[-_ ]?task", "task[-_ ]?tool", "delegate", "delegation"} {
		if !strings.Contains(repaired.Pattern, want) {
			t.Fatalf("subagent discovery repair should include implementation alias %q, got %q", want, repaired.Pattern)
		}
	}
}

func TestDelegatedReadOnlyDiscoveryRepairUsesNamedPrimaryScopeForDotPath(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "delegated_read_only",
			Contract:          "architecture_assessment",
			PermissionProfile: "read_only_workspace",
			Objective: strings.Join([]string{
				"Inspect how this harness uses subagents.",
				"Named workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal",
				"Key terms: subagents, child, task",
			}, "\n"),
		},
	}
	repaired, ok := runner.delegatedReadOnlyDiscoveryQueryRepairRequest([]workspace.ToolResult{{
		Tool:    "search_content",
		Summary: "Found candidate paths",
	}}, workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "investigate subagent orchestration",
		Path:    ".",
	})

	if !ok {
		t.Fatal("expected invalid delegated discovery query to be repaired after prior evidence")
	}

	if repaired.Purpose != "architecture_boundary" {
		t.Fatalf("expected invalid purpose repaired, got %#v", repaired)
	}

	if repaired.Path != "internal/runtime/agent" {
		t.Fatalf("expected dot path repaired to primary named scope, got %#v", repaired)
	}
}

func TestMainArchitectureBootstrapDelegatesBroadFirstRead(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how this harness uses sub-agents.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "list_files", "search_content", "read_file"},
			SearchPolicy:      []string{"delegate_broad_discovery_when_needed", "prefer_discovery_query_for_architecture_boundary", "trace_boundaries_from_named_components"},
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "list_files", Path: "."},
	})

	if decision.Request.Tool != "task" || decision.Request.Mode != "read_only" || decision.Request.Agent != "explorer" {
		t.Fatalf("expected architecture bootstrap to delegate broad first read, got %#v", decision.Request)
	}

	if !strings.Contains(decision.Request.Prompt, "Start from source-of-truth implementation files") {
		t.Fatalf("delegated prompt lost source-first guidance:\n%s", decision.Request.Prompt)
	}
}

func TestMainArchitectureBootstrapKeepsConcreteFeatureQuestionLocal(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "What does the debug log redacted mode do?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "context_pack", "discovery_query", "search_content", "read_file"},
			SearchPolicy:      []string{"delegate_broad_discovery_when_needed", "prefer_discovery_query_for_architecture_boundary"},
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "context_pack", Query: "debug log redacted mode"},
	})

	if decision.Request.Tool != "context_pack" {
		t.Fatalf("expected concrete feature question to stay local, got %#v", decision.Request)
	}
}

func TestMainArchitectureBootstrapDelegatesBroadBoundaryQuestion(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how this harness uses sub-agents.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "context_pack", "discovery_query", "search_content", "read_file"},
			SearchPolicy:      []string{"delegate_broad_discovery_when_needed", "prefer_discovery_query_for_architecture_boundary"},
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "context_pack", Query: "subagent architecture"},
	})

	if decision.Request.Tool != "task" || decision.Request.Mode != "read_only" || decision.Request.Agent != "explorer" {
		t.Fatalf("expected broad architecture context_pack bootstrap to delegate, got %#v", decision.Request)
	}
}

func TestMainArchitectureBootstrapIgnoresCompletionGateRejection(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "context_pack", "discovery_query", "search_content", "read_file"},
			SearchPolicy:      []string{"delegate_broad_discovery_when_needed", "prefer_discovery_query_for_architecture_boundary"},
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "context_pack", Task: "assess discovery tool gaps and propose new capabilities"},
		Results: []workspace.ToolResult{{
			Tool:    "completion_gate",
			Summary: "Read-only completion gate rejected final answer",
			Output:  "answer missing inspected paths and confidence",
		}},
	})

	if decision.Request.Tool != "task" || decision.Request.Mode != "read_only" || decision.Request.Agent != "explorer" {
		t.Fatalf("expected completion-gate rejection not to suppress architecture task bootstrap, got %#v", decision.Request)
	}
}

func TestMainArchitectureBootstrapDoesNotOverrideExactStructuredProbe(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		AgentName: "main",
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "architecture",
			Contract:          "architecture_assessment",
			Objective:         "Explain how this harness uses sub-agents.",
			PermissionProfile: "read_only_workspace",
			AllowedTools:      []string{"task", "go_read_symbol", "go_extract_body"},
			SearchPolicy:      []string{"prefer_discovery_query_for_architecture_boundary", "trace_boundaries_from_named_components"},
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "go_read_symbol", Path: "internal/runtime/agent/runner_child_tasks.go", Pattern: "runChildTask"},
	})

	if decision.Request.Tool != "go_read_symbol" || decision.Request.Path != "internal/runtime/agent/runner_child_tasks.go" || decision.Request.Pattern != "runChildTask" {
		t.Fatalf("expected exact structured probe to stay local, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyNamedGoReadRedirectsToScopedSearch(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how sub-agents are invoked.\nKey terms: task, sub-agent, subagent",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner.go"},
	})

	if decision.Request.Tool != "search_content" {
		t.Fatalf("expected named go-file read to redirect to search_content, got %#v", decision.Request)
	}

	if decision.Request.Path != "internal/runtime/agent/runner.go" {
		t.Fatalf("expected scoped search to stay on runner.go, got %#v", decision.Request)
	}

	for _, want := range []string{"task", "sub[-_ ]?agent", "subagent"} {
		if !strings.Contains(decision.Request.Pattern, want) {
			t.Fatalf("expected scoped search pattern to include %q, got %q", want, decision.Request.Pattern)
		}
	}
}

func TestDelegatedReadOnlyNamedGoReadPrefersOutlineWhenAvailable(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"go_file_outline", "search_content", "read_file"},
			Objective:    "Explain how sub-agents are invoked.\nKey terms: task, sub-agent, subagent",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner.go"},
	})

	if decision.Request.Tool != "go_file_outline" || decision.Request.Path != "internal/runtime/agent/runner.go" {
		t.Fatalf("expected named go-file read to redirect to go_file_outline, got %#v", decision.Request)
	}
}

func TestPostReadinessGoReadSymbolRedirectsToBodyAfterTruncatedRead(t *testing.T) {
	runner := &Runner{}
	results := []workspace.ToolResult{{
		Tool:    "go_read_symbol",
		Summary: "Read Runner.runChildTask in internal/runtime/agent/runner_child_tasks.go lines 16-135 (truncated from lines 16-516; full-body adapter: go_extract_body)",
		Output:  "func (r *Runner) runChildTask(...) {",
	}}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "go_read_symbol", Path: "internal/runtime/agent/runner_child_tasks.go", Pattern: "runChildTask"},
		Results: results,
	})

	if decision.Request.Tool != "go_extract_body" {
		t.Fatalf("expected truncated go_read_symbol follow-up to upgrade to go_extract_body, got %#v", decision.Request)
	}
}

func TestPostReadinessCodeNavigateReadSymbolRedirectsToExtractBodyAfterTruncatedRead(t *testing.T) {
	runner := &Runner{}
	results := []workspace.ToolResult{{
		Tool:    "code_navigate",
		Summary: "Code navigation routed read_symbol via go_read_symbol",
		Output:  "capability: read_symbol\nlanguage: go\nrouted_tool: go_read_symbol\nroute_summary: Read Model.handleKeyPress in internal/surfaces/tui/app/update_handlers.go lines 22-141 (truncated from lines 22-182; full-body adapter: go_extract_body)\n\n22:func (m *Model) handleKeyPress(...) {",
	}}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "code_navigate", Capability: "read_symbol", Path: "internal/surfaces/tui/app/update_handlers.go", Pattern: "handleKeyPress"},
		Results: results,
	})

	if decision.Request.Tool != "code_navigate" || decision.Request.Capability != "extract_body" {
		t.Fatalf("expected truncated code_navigate read_symbol follow-up to upgrade to code_navigate extract_body, got %#v", decision.Request)
	}

	if decision.Request.Pattern != "handleKeyPress" || decision.Request.Path != "internal/surfaces/tui/app/update_handlers.go" {
		t.Fatalf("expected upgrade to preserve target, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyIntegrationPivotRedirectsToSiblingImplementation(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how delegated child runners are created.\nKey terms: subagent, delegate, task",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:   "search_content",
			Output: "internal/runtime/agent/runner_child_tasks.go:16:func (r *Runner) runChildTask(...)\ninternal/runtime/agent/runner.go:574:result := r.runChildTask(ctx, req, opts)\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/runner_child_tasks.go lines 16-130",
			Output:  "16:func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {\n",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "read_file", Path: "internal/runtime/agent/runner_child_tasks.go", StartLine: 185, EndLine: 250},
		Results: results,
	})

	if decision.Request.Tool != "read_file" {
		t.Fatalf("expected pivot redirect to targeted read_file, got %#v", decision.Request)
	}

	if decision.Request.Path != "internal/runtime/agent/runner_child_tasks.go" {
		t.Fatalf("expected pivot redirect to targeted source-body window, got %#v", decision.Request)
	}

	if decision.Request.StartLine != 1 || decision.Request.EndLine != 96 {
		t.Fatalf("expected source-body window around runner_child_tasks.go, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyIntegrationPivotDoesNotOverrideStructuredProbe(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how delegated child runners are created.\nKey terms: subagent, delegate, task",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:   "search_content",
			Output: "internal/runtime/agent/runner_child_tasks.go:16:func (r *Runner) runChildTask(...)\ninternal/runtime/agent/runner.go:574:result := r.runChildTask(ctx, req, opts)\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/runner_child_tasks.go lines 16-130",
			Output:  "16:func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {\n",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "go_read_symbol", Path: "internal/runtime/agent/runner_child_tasks.go", Pattern: "runChildTask"},
		Results: results,
	})

	if decision.Request.Tool != "go_read_symbol" || decision.Request.Path != "internal/runtime/agent/runner_child_tasks.go" || decision.Request.Pattern != "runChildTask" {
		t.Fatalf("expected structured symbol probe to remain model-directed, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyAnswerReadyDoesNotForceSynthesisAfterTwoImplementationReads(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily: "delegated_read_only",
			Objective:  "Explain how delegated child runners are created.\nKey terms: subagent, delegate, task",
		},
	}
	results := []workspace.ToolResult{
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/runner_child_tasks.go lines 16-130",
			Output:  "16:func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/runner.go lines 554-654",
			Output:  "574:\t\tresult := r.runChildTask(ctx, req, opts)\n",
		},
	}
	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: "search_content", Path: "internal/runtime/agent", Pattern: "delegate|task"},
		Results: results,
	})

	if decision.RejectSummary != "" || decision.ForceAnswer != nil {
		t.Fatalf("expected delegated child evidence to remain model-directed, got %#v", decision)
	}
}

func TestSynthesizeDelegatedReadOnlyAnswerBuildsGroundedPacket(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "search_content",
			Output:  "internal/runtime/agent/runner.go:574:\t\tresult := r.runChildTask(ctx, req, opts)\ninternal/runtime/agent/runner_child_tasks.go:93:\t\t\t\"You are a read-only delegated child task.\"\n",
			Summary: "Found 2 matching line(s).",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/runner_child_tasks.go lines 16-130",
			Output:  "16:func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult {\n93:\t\t\t\"You are a read-only delegated child task.\"\n",
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/runner.go lines 554-654",
			Output:  "574:\t\tresult := r.runChildTask(ctx, req, opts)\n",
		},
	}

	answer, ok := synthesizeDelegatedReadOnlyAnswer("Can you tell me how this harness uses subagents?", results)
	if !ok {
		t.Fatal("expected delegated read-only synthesis to succeed")
	}

	for _, want := range []string{
		"The answer is grounded in",
		"What this establishes:",
		"`internal/runtime/agent/runner.go`",
		"`internal/runtime/agent/runner_child_tasks.go`",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected synthesized answer to contain %q, got %q", want, answer)
		}
	}

	for _, forbidden := range []string{
		"Partial findings from this turn",
		"Source: delegated subagent task packet.",
		"Source: delegated read-only evidence packet.",
		"Inspected paths:",
		"Key findings:",
		"Confidence:",
		"Discovery packet:",
		"Relevant source hits:",
	} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("expected composed user-facing synthesis without %q, got %q", forbidden, answer)
		}
	}
}

func TestDelegatedDiscoveryQueryEvidencePathsFeedSynthesizedAnswer(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query with 10 layer(s); artifact: .idiom/discovery/queries/query.json",
			Output:  `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"target_listing","summary":"Listed internal/runtime/agent.","output_excerpt":"internal/runtime/agent/final_answer_decorator.go\ninternal/runtime/agent/discovery_redirect.go\ninternal/runtime/agent/instructions.go"},{"name":"text_anchor_search","summary":"Found subagent handoff implementation paths.","output_excerpt":"internal/runtime/agent/runner_child_tasks.go:95: For broad architecture questions, prefer discovery_query first.\ninternal/runtime/agent/runner.go:574: result := r.runChildTask(ctx, req, opts)\n.idiom/discovery/queries/query.json"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/runtime/agent/runner_child_tasks.go lines 1-120",
			Output:  "package agent\nfunc (r *Runner) runChildTask() {}\n",
		},
	}

	paths := delegatedDiscoveryQueryEvidencePaths(results, 4)
	if len(paths) < 2 || paths[0] != "internal/runtime/agent/runner.go" || paths[1] != "internal/runtime/agent/runner_child_tasks.go" {
		t.Fatalf("expected text-anchor evidence paths to outrank target listing paths, got %#v", paths)
	}

	for _, want := range []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"} {
		if !slices.Contains(paths, want) {
			t.Fatalf("expected discovery paths to contain %q, got %#v", want, paths)
		}
	}

	if slices.Contains(paths, ".idiom/discovery/queries/query.json") {
		t.Fatalf("discovery artifact path should not displace implementation evidence, got %#v", paths)
	}

	answer, ok := synthesizeDelegatedReadOnlyAnswer("Explain how the harness uses subagents.", results)
	if !ok {
		t.Fatal("expected synthesized answer from discovery_query packet")
	}

	for _, want := range []string{"`internal/runtime/agent/runner.go`", "`internal/runtime/agent/runner_child_tasks.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected synthesized answer to cite %q, got:\n%s", want, answer)
		}
	}

	for _, want := range []string{
		"The strongest implementation evidence is in",
		"What this establishes:",
		"Text anchors:",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected human-readable discovery synthesis to contain %q, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{`"layers_run"`, `"evidence"`, "{\n", "Discovery packet:", "Relevant source hits:", "Evidence used:", "Confidence:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("synthesized answer should not dump raw discovery JSON %q, got:\n%s", forbidden, answer)
		}
	}
}

func TestDelegatedDiscoveryQueryEvidencePathsPreferCoreRunnerFiles(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query with 10 layer(s); artifact: .idiom/discovery/queries/query.json",
		Output:  `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[{"name":"text_anchor_search","summary":"Found discovery implementation paths.","output_excerpt":"internal/runtime/agent/modes.go:1: package agent\ninternal/runtime/agent/instructions.go:1: package agent\ninternal/runtime/agent/final_answer_decorator.go:1: package agent\ninternal/runtime/agent/answer_composer.go:18: func (AnswerComposer) Compose\ninternal/runtime/agent/runner.go:128: finish := func\ninternal/runtime/agent/runner_lane_policy.go:637: delegatedReadOnlyAnswerReadyAfterResult\ninternal/runtime/agent/discovery_redirect.go:1: package agent\ninternal/runtime/agent/runner_test.go:1: package agent"}]}`,
	}}

	paths := delegatedDiscoveryQueryEvidencePaths(results, 5)
	for _, want := range []string{"internal/runtime/agent/answer_composer.go", "internal/runtime/agent/runner.go", "internal/runtime/agent/runner_lane_policy.go"} {
		if !slices.Contains(paths, want) {
			t.Fatalf("expected core path %q in top evidence paths, got %#v", want, paths)
		}
	}

	if slices.Contains(paths, "internal/runtime/agent/modes.go") {
		t.Fatalf("incidental path displaced core runner evidence: %#v", paths)
	}
}

func TestAnswerComposerUsesProvidedDiscoveryUsefulnessEvidenceOnly(t *testing.T) {
	summaries := []delegatedDiscoverySummary{{
		Purpose:      "architecture_boundary",
		ArtifactPath: ".idiom/discovery/queries/query.json",
		Evidence: []struct {
			Name          string `json:"name"`
			Tool          string `json:"tool"`
			Summary       string `json:"summary"`
			Error         string `json:"error,omitempty"`
			OutputExcerpt string `json:"output_excerpt,omitempty"`
		}{{
			Name:          "text_anchor_search",
			Summary:       "Found discovery tool implementation paths.",
			OutputExcerpt: "internal/capabilities/workspace/discovery_query.go:1: package workspace",
		}},
	}}

	answer, ok := AnswerComposer{}.Compose(AnswerComposerInput{
		Question:      "What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		EvidencePaths: []string{"internal/capabilities/workspace/discovery_query.go"},
		Summaries:     summaries,
	})
	if !ok {
		t.Fatal("expected discovery-usefulness answer composition")
	}

	for _, want := range []string{"`internal/capabilities/workspace/discovery_query.go`"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected composed answer to cite %q, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{"`internal/runtime/agent/answer_composer.go`", "`internal/runtime/agent/runner.go`", "`internal/runtime/agent/runner_lane_policy.go`"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("composed answer should not inject question-class path %q, got:\n%s", forbidden, answer)
		}
	}
}

func TestDelegatedDiscoveryAnswerStaysGenericForDebugLogRedactedMode(t *testing.T) {
	results := []workspace.ToolResult{
		{
			Tool:    "discovery_query",
			Summary: "Ran architecture_boundary discovery query with 10 layer(s); artifact: .idiom/discovery/queries/debug.json",
			Output:  `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/debug.json","evidence":[{"name":"text_anchor_search","summary":"Found debug redaction implementation paths.","output_excerpt":"internal/evidence/transcript/debuglog.go:18: DebugLogModeRedacted DebugLogMode = \"redacted\"\ninternal/evidence/transcript/debuglog.go:528: return redactedBlob(typed)\ninternal/surfaces/tui/app/model_state.go:469: case \"redacted\", \"full\":"}]}`,
		},
		{
			Tool:    "read_file",
			Summary: "Read internal/evidence/transcript/debuglog.go lines 1-80",
			Output:  "package transcript\ntype DebugLogMode string\nconst DebugLogModeRedacted DebugLogMode = \"redacted\"\n",
		},
	}

	answer, ok := synthesizeDelegatedReadOnlyAnswer("What does the debug log redacted mode do?", results)
	if !ok {
		t.Fatal("expected synthesized answer from debug log evidence")
	}

	for _, want := range []string{
		"The strongest implementation evidence is in",
		"What this establishes:",
		"Text anchors:",
		"`internal/evidence/transcript/debuglog.go`",
		"`internal/surfaces/tui/app/model_state.go`",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected generic synthesized answer to contain %q, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{"## What Stays Useful", "## What Gets Redacted", "Found 400 matching", `"evidence"`, "Discovery packet:", "Relevant source hits:", "Evidence used:", "Confidence:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("generic synthesis should not use a topic template or dump metadata %q:\n%s", forbidden, answer)
		}
	}
}

func TestDelegatedTaskAnswerSynthesizesDebugLogRedactedMode(t *testing.T) {
	results := []workspace.ToolResult{{
		Tool:    "task",
		Summary: "Child task completed",
		Output: strings.Join([]string{
			"Source-body evidence paths:",
			"- internal/evidence/transcript/debuglog.go",
			"- internal/platform/config/config.go",
			"",
			"Verified context:",
			"- Found debug log redaction defaults in internal/evidence/transcript/debuglog.go and internal/platform/config/config.go.",
			"- Key findings: redacted mode keeps logs useful without exposing raw prompt or file body content.",
		}, "\n"),
	}}

	answer, ok := synthesizeDelegatedReadOnlyAnswer("What does the debug log redacted mode do?", results)
	if !ok {
		t.Fatal("expected synthesized answer from delegated debug-log packet")
	}

	for _, want := range []string{
		"The answer is grounded in",
		"What this establishes:",
		"redacted",
		"debug log",
		"`internal/evidence/transcript/debuglog.go`",
		"`internal/platform/config/config.go`",
	} {
		if !strings.Contains(answer, want) {
			t.Fatalf("expected synthesized answer to contain %q, got:\n%s", want, answer)
		}
	}

	for _, forbidden := range []string{"What Gets Redacted:", "Discovery packet:", "Relevant source hits:", "Evidence used:"} {
		if strings.Contains(answer, forbidden) {
			t.Fatalf("synthesized answer should not expose telemetry section %q:\n%s", forbidden, answer)
		}
	}

	runner := &Runner{}
	if blocker, reason := runner.finalAnswerBlockerKind(answer, results); blocker != "" || reason != "" {
		t.Fatalf("synthesized answer should pass final-answer blocker, got (%q, %q):\n%s", blocker, reason, answer)
	}
}

func TestCleanDelegatedReadOnlyFindingLineStripsPacketLabels(t *testing.T) {
	cases := map[string]string{
		"Key findings: ShortLabel returns Save changes.":              "ShortLabel returns Save changes.",
		"Verified context: redacted mode masks raw prompt text.":      "redacted mode masks raw prompt text.",
		"Inspected paths: internal/runtime/agent/runner.go was read.": "internal/runtime/agent/runner.go was read.",
		"Evidence used: internal/evidence/transcript/debuglog.go.":    "internal/evidence/transcript/debuglog.go.",
		"Core files: internal/runtime/agent/runner_child_tasks.go.":   "internal/runtime/agent/runner_child_tasks.go.",
		"Important flow: the task result returns a child packet.":     "the task result returns a child packet.",
		"Source body paths: internal/runtime/agent/runner.go.":        "internal/runtime/agent/runner.go.",
		"Grounded findings: child packet ready.":                      "child packet ready.",
	}
	for input, want := range cases {
		if got := cleanDelegatedReadOnlyFindingLine(input); got != want {
			t.Fatalf("cleanDelegatedReadOnlyFindingLine(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSourceEvidenceAnswerLinesStripSearchContextHandles(t *testing.T) {
	output := strings.Join([]string{
		"package workspace",
		"",
		"// task tool manifest entries wire subagents for delegated child work.",
		"context:",
		"f1 internal/capabilities/workspace/tool_manifest.go:3:// task tool manifest entries wire subagents for delegated child work.",
	}, "\n")

	lines := sourceEvidenceAnswerLinesForObjective(output, "Explain how subagents work in this harness.", 8)
	if len(lines) != 0 {
		t.Fatalf("expected comment-only context handles to be ignored, got %#v", lines)
	}
}
