package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

func TestDelegatedReadOnlyCandidateConsumptionPrefersObjectiveSemanticAnchorsOverListing(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "Inspect how the idiom harness uses subagents (child packets, task tool, lane delegation, and orchestration layers).\nNamed workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal\nKey terms: idiom, subagents, child, packets, delegation, orchestration, layers, summarize",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output: `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[` +
			`{"name":"target_listing","summary":"Listed internal/runtime/agent.","output_excerpt":"internal/runtime/agent/errors.go\ninternal/runtime/agent/final_answer_acceptance.go\ninternal/runtime/agent/runner_review_repair.go"},` +
			`{"name":"text_anchor_search","summary":"Found delegated child orchestration implementation paths.","output_excerpt":"internal/runtime/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult\ninternal/runtime/agent/runner.go:574: result := r.runChildTask(ctx, req, opts)"}` +
			`]}`,
	}}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Results: results,
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "architecture_boundary", Path: "internal/runtime/agent", Pattern: "subagents|child|orchestration"},
	})

	if decision.Request.Tool != "read_file" {
		t.Fatalf("expected discovery candidate consumption read, got %#v", decision.Request)
	}

	if decision.Request.Path != "internal/runtime/agent/runner_child_tasks.go" && decision.Request.Path != "internal/runtime/agent/runner.go" {
		t.Fatalf("expected objective-relevant semantic anchor, got %#v", decision.Request)
	}

	if decision.Request.Path == "internal/runtime/agent/errors.go" {
		t.Fatalf("incidental listing path must not be consumed as source-body evidence: %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyCandidateConsumptionDoesNotForceListingOnlyPath(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "Explain how this harness uses subagents.\nNamed workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"evidence":[{"name":"target_listing","summary":"Listed internal/runtime/agent.","output_excerpt":"internal/runtime/agent/errors.go\ninternal/runtime/agent/final_answer_acceptance.go\ninternal/runtime/agent/runner_review_repair.go"}]}`,
	}}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Results: results,
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "architecture_boundary", Path: "internal/runtime/agent", Pattern: "subagent|task"},
	})

	if decision.Request.Tool == "read_file" {
		t.Fatalf("listing-only discovery should not force a source-body read, got %#v", decision.Request)
	}

	if decision.Request.Path == "internal/runtime/agent/errors.go" {
		t.Fatalf("listing-only discovery must not promote incidental path, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyCandidateConsumptionUsesObjectiveOverlapForWeakLayer(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "Explain child orchestration for delegated packets.\nNamed workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal\nKey terms: child, orchestration, packets",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"evidence":[{"name":"target_listing","summary":"Listed internal/runtime/agent.","output_excerpt":"internal/runtime/agent/errors.go:1: package agent\ninternal/runtime/agent/runner_child_tasks.go:16: child orchestration packet flow\ninternal/runtime/agent/final_answer_acceptance.go:1: package agent"}]}`,
	}}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Results: results,
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "architecture_boundary", Path: "internal/runtime/agent", Pattern: "child|orchestration"},
	})

	if decision.Request.Tool != "read_file" || decision.Request.Path != "internal/runtime/agent/runner_child_tasks.go" {
		t.Fatalf("expected weak-layer candidate with objective/path overlap to be consumed, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyPurposeIgnoresToolInventoryTerms(t *testing.T) {
	plan := newDelegatedDiscoveryPlan(strings.Join([]string{
		"Inspect the discovery tool surface in idiom (context_pack, discovery_query, history_*, blame_range).",
		"- history_*",
		"- blame_range",
		"Return gaps and adjacent capabilities.",
	}, "\n"))

	if plan.defaultPurpose != "architecture_boundary" {
		t.Fatalf("tool inventory metadata must not force history_context purpose, got %q", plan.defaultPurpose)
	}
}

func TestDelegatedDiscoveryPlanTreatsCodexAgentsAsSecondaryForSubagentMechanism(t *testing.T) {
	objective := strings.Join([]string{
		"Investigate how this harness (the idiom project) uses subagents.",
		"Named workspace scope: .codex/agents, internal/runtime/agent, internal/capabilities/workspace, internal/frontier, prompts",
		"Key terms: subagent, task tool, delegate, explorer, worker, reviewer, investigate, idiom",
	}, "\n")
	plan := newDelegatedDiscoveryPlan(objective)

	if plan.primaryScope != "internal/runtime/agent" {
		t.Fatalf("expected implementation scope to outrank .codex/agents, got %q", plan.primaryScope)
	}

	for _, scope := range plan.detailedScopes {
		if scope == ".codex/agents" {
			t.Fatalf(".codex/agents should be secondary evidence, not detailed source scope: %#v", plan.detailedScopes)
		}
	}

	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    objective,
		},
	}
	repaired, ok := runner.delegatedReadOnlyDiscoveryQueryRepairRequest(nil, workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "architecture_boundary",
		Path:    ".codex/agents",
		Pattern: "subagent|task|delegate",
	})

	if !ok {
		t.Fatal("expected child discovery query against .codex/agents to be repaired")
	}

	if repaired.Path != "internal/runtime/agent" {
		t.Fatalf("expected repaired discovery path to use implementation scope, got %#v", repaired)
	}

	bootstrap, ok := runner.delegatedReadOnlyBootstrapQueryRequest(nil, workspace.ToolRequest{
		Tool: workspace.ToolNameContextPack,
		Task: "how the idiom harness uses subagents/child-agents",
		Path: ".codex/agents",
		Mode: "brief",
	})
	if !ok {
		t.Fatal("expected subagent context_pack bootstrap to be rewritten")
	}

	if bootstrap.Tool != workspace.ToolNameDiscoveryQuery || bootstrap.Purpose != "architecture_boundary" || bootstrap.Path != "internal/runtime/agent" {
		t.Fatalf("expected subagent bootstrap to use architecture discovery under implementation scope, got %#v", bootstrap)
	}
}

func TestDelegatedReadOnlyBootstrapQueryPreservesExactSourceFileForSubagentPrompt(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_files", "read_file"},
			Objective:    "Inspect internal/capabilities/workspace/tool_manifest.go and explain how subagents are wired.",
		},
	}

	if redirected, ok := runner.delegatedReadOnlyBootstrapQueryRequest(nil, workspace.ToolRequest{
		Tool:    workspace.ToolNameSearchFiles,
		Pattern: "*tool_manifest*",
	}); ok {
		t.Fatalf("exact source-file prompt should not be forced into broad discovery query, got %#v", redirected)
	}
}

func TestDelegatedReadOnlyBootstrapQueryScopesSubagentMechanismAwayFromEntrypoint(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "context_pack", "read_file"},
			Objective:    "Investigate how the idiom harness uses subagents, child-agents, and subagent mechanisms.",
		},
	}

	redirected, ok := runner.delegatedReadOnlyBootstrapQueryRequest(nil, workspace.ToolRequest{
		Tool: workspace.ToolNameContextPack,
		Task: "how the idiom harness uses subagents, child-agents, subagent mechanisms",
		Path: "internal/frontier",
		Mode: "brief",
	})
	if !ok {
		t.Fatal("expected broad subagent mechanism bootstrap to be rewritten to composite discovery")
	}

	if redirected.Path != "internal/runtime/agent" {
		t.Fatalf("subagent mechanism discovery must stay on runtime agent source, got %#v", redirected)
	}
}

func TestDelegatedReadOnlyBootstrapQueryIgnoresConfigPatternForSubagentMechanism(t *testing.T) {
	objective := strings.Join([]string{
		"Investigate how the idiom harness uses subagents. I need to understand:",
		"1. What subagent/child-agent mechanisms exist (e.g., task tool, subagent delegation, lane-based subagents, parallel agents)",
		"2. Where subagents are defined/configured (look for .toml files, agent definitions, subagent configs)",
		"3. How the harness launches and communicates with subagents (look for subagent runners, dispatchers, supervisor loops)",
		"Search broadly:",
		"- Search for files matching *.toml in .codex/agents/ or similar agent config directories",
		"- Search for \"subagent\", \"child\", \"delegate\", \"worker\", \"explorer\", \"reviewer\" in Go source files",
		"- Look at the task tool implementation",
	}, "\n")
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_files", "read_file"},
			Objective:    objective,
		},
	}

	redirected, ok := runner.delegatedReadOnlyBootstrapQueryRequest(nil, workspace.ToolRequest{
		Tool:    workspace.ToolNameSearchFiles,
		Pattern: ".toml",
		Path:    ".",
	})
	if !ok {
		t.Fatal("expected config-pattern bootstrap to be rewritten to composite discovery")
	}

	if redirected.Tool != workspace.ToolNameDiscoveryQuery || redirected.Path != "internal/runtime/agent" {
		t.Fatalf("expected subagent mechanism bootstrap to use runtime implementation discovery, got %#v", redirected)
	}
}

func TestDelegatedDiscoveryPlanIgnoresIncidentalSlashPhraseForSubagentMechanism(t *testing.T) {
	objective := strings.Join([]string{
		"Investigate how the idiom harness uses subagents.",
		"Look at any subagent/child-agent/worker/explorer/reviewer patterns.",
		"Report back with specific file paths and function names.",
	}, "\n")
	plan := newDelegatedDiscoveryPlan(objective)

	if plan.primaryScope != "internal/runtime/agent" {
		t.Fatalf("expected subagent mechanism scope to outrank incidental slash phrase, got %q", plan.primaryScope)
	}
}

func TestDelegatedReadOnlyDiscoveryRepairReplacesBroadNamedScope(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective: strings.Join([]string{
				"Investigate how this harness uses subagents.",
				"Named workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal",
				"Key terms: subagent, child task, task tool",
			}, "\n"),
		},
	}
	repaired, ok := runner.delegatedReadOnlyDiscoveryQueryRepairRequest(nil, workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "architecture_boundary",
		Path:    "internal",
		Pattern: "subagent|child",
	})

	if !ok {
		t.Fatal("expected broad internal scope to be repaired")
	}

	if repaired.Path != "internal/runtime/agent" {
		t.Fatalf("expected broad internal scope to redirect to primary implementation scope, got %#v", repaired)
	}
}

func TestDelegatedReadOnlyDiscoveryRepairReplacesMissingNamedScope(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "capabilities", "workspace"), 0o755); err != nil {
		t.Fatalf("mkdir workspace scope: %v", err)
	}

	runner := &Runner{
		AgentName: "child:explorer",
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective: strings.Join([]string{
				"Inventory discovery tools and identify usefulness gaps.",
				"Named workspace scope: internal/capabilities/workspace, internal/frontier, internal",
				"Key terms: discovery tools, usefulness",
			}, "\n"),
		},
	}
	repaired, ok := runner.delegatedReadOnlyDiscoveryQueryRepairRequest(nil, workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "architecture_boundary",
		Path:    "internal/frontier",
		Pattern: "discovery|tools",
	})

	if !ok {
		t.Fatal("expected missing named scope to be repaired")
	}

	if repaired.Path != "internal/capabilities/workspace" {
		t.Fatalf("expected missing scope to redirect to existing primary implementation scope, got %#v", repaired)
	}
}

func TestDelegatedReadOnlyDiscoveryRepairUsesFirstExistingDetailedScope(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "capabilities", "workspace"), 0o755); err != nil {
		t.Fatalf("mkdir workspace scope: %v", err)
	}

	runner := &Runner{
		AgentName: "child:explorer",
		Workspace: workspace.NewManager(config.Workspace{
			Root: root,
			CWD:  root,
		}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective: strings.Join([]string{
				"Inventory discovery tools and identify usefulness gaps.",
				"Named workspace scope: internal/frontier, internal/capabilities/workspace, internal",
				"Key terms: discovery tools, usefulness",
			}, "\n"),
		},
	}

	repaired, ok := runner.delegatedReadOnlyDiscoveryQueryRepairRequest(nil, workspace.ToolRequest{
		Tool:    "discovery_query",
		Purpose: "architecture_boundary",
		Path:    "internal/frontier",
		Pattern: "discovery|tools",
	})
	if !ok {
		t.Fatal("expected missing first scope to be repaired")
	}

	if repaired.Path != "internal/capabilities/workspace" {
		t.Fatalf("expected repair to use first existing detailed scope, got %#v", repaired)
	}
}

func TestDelegatedReadOnlyPostDiscoveryInventoryConsumesCandidateSource(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "search_files", "read_file"},
			Objective: strings.Join([]string{
				"What can we add to our discovery tools in an effort at increasing their overall usefulness?",
				"Named workspace scope: internal/frontier, internal/capabilities/workspace, internal",
				"Key terms: discovery tools, usefulness, final answer",
			}, "\n"),
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output: `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[` +
			`{"name":"text_anchor_search","summary":"Found discovery tool implementation paths.","output_excerpt":"internal/runtime/agent/answer_composer.go:18: func (AnswerComposer) Compose(input AnswerComposerInput) (string, bool)\ninternal/capabilities/workspace/discovery_query.go:338: func (r *discoveryQueryRunner) run(ctx context.Context) {}"}` +
			`]}`,
	}}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Results: results,
		Request: workspace.ToolRequest{Tool: "search_files", Pattern: "internal/frontier"},
	})
	if decision.Request.Tool != "read_file" {
		t.Fatalf("expected post-discovery search to consume source candidate, got %#v", decision.Request)
	}

	if decision.Request.Path != "internal/capabilities/workspace/discovery_query.go" &&
		decision.Request.Path != "internal/runtime/agent/answer_composer.go" {

		t.Fatalf("expected discovered source candidate read, got %#v", decision.Request)
	}

	decision = runner.applyLanePreflight(runnerLanePreflightInput{
		Results: results,
		Request: workspace.ToolRequest{Tool: workspace.ToolNameFileStats, Path: ".", Recursive: true},
	})
	if decision.Request.Tool != workspace.ToolNameReadFile {
		t.Fatalf("expected post-discovery file_stats to consume source candidate, got %#v", decision.Request)
	}

	decision = runner.applyLanePreflight(runnerLanePreflightInput{
		Results: results,
		Request: workspace.ToolRequest{Tool: workspace.ToolNameCodeNavigate, Capability: "file_outline", Path: "internal/capabilities/workspace/discovery_query.go"},
	})
	if decision.Request.Tool != workspace.ToolNameReadFile || decision.Request.Path != "internal/capabilities/workspace/discovery_query.go" {
		t.Fatalf("expected post-discovery outline to become source-body read, got %#v", decision.Request)
	}
}

func TestMainReadOnlyBroadSearchBootstrapsContextPack(t *testing.T) {
	runner := &Runner{
		AgentName: "main",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyDirectAnswer),
			Contract:          string(workerruntime.ContractDirectAnswer),
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			AllowedTools:      []string{workspace.ToolNameContextPack, workspace.ToolNameSearchContent, workspace.ToolNameReadFile},
			Objective:         "What does the debug log redacted mode do?",
		},
	}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Request: workspace.ToolRequest{Tool: workspace.ToolNameSearchContent, Path: ".", Pattern: "redacted"},
	})
	if decision.Request.Tool != workspace.ToolNameContextPack {
		t.Fatalf("expected broad first-pass search to become context_pack, got %#v", decision.Request)
	}

	if decision.Request.Task != runner.RuntimeBrief.Objective || decision.Request.Mode != "plan" {
		t.Fatalf("expected context_pack to carry objective and plan mode, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyCandidateConsumptionPrefersDiscoveredMechanismEvidenceOverInventory(t *testing.T) {
	plan := newDelegatedDiscoveryPlan(strings.Join([]string{
		"What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		"Named workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal",
		"Key terms: discovery tools, usefulness, context graph, final answer",
	}, "\n"))
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output: `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[` +
			`{"name":"related_files","summary":"Found related Go files.","output_excerpt":"internal/capabilities/workspace/discovery_query.go\ninternal/runtime/agent/runner_child_tasks.go"},` +
			`{"name":"go_workspace_symbol","summary":"No Go workspace symbols matched \"discovery|tool|usefulness\".","output_excerpt":"Symbol index: no matching symbols. capability: file_outline. language: go."},` +
			`{"name":"text_anchor_search","summary":"Found discovery tool implementation paths.","output_excerpt":"internal/capabilities/workspace/discovery_query.go:338: func (r *discoveryQueryRunner) run(ctx context.Context) {}\ninternal/runtime/agent/answer_composer.go:18: func (AnswerComposer) Compose(input AnswerComposerInput) (string, bool)\ninternal/runtime/agent/runner_lane_policy.go:37: type delegatedDiscoveryPlan struct"}` +
			`]}`,
	}}

	paths := plan.consumableSourceBodyCandidatePaths(results, 4)
	if len(paths) == 0 {
		t.Fatal("expected discovered candidates")
	}

	if paths[0] != "internal/runtime/agent/answer_composer.go" {
		t.Fatalf("expected discovered answer-composition mechanism to outrank inventory paths, got %#v", paths)
	}

	for _, forbidden := range []string{".idiom/discovery/queries/query.json", "Symbol index"} {
		for _, path := range paths {
			if strings.Contains(path, forbidden) {
				t.Fatalf("inventory/artifact candidate %q leaked into consumable paths %#v", forbidden, paths)
			}
		}
	}
}

func TestDelegatedReadOnlyCandidateConsumptionDoesNotInjectUndiscoveredMechanismPath(t *testing.T) {
	plan := newDelegatedDiscoveryPlan(strings.Join([]string{
		"What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		"Named workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal",
		"Key terms: discovery tools, usefulness, context graph",
	}, "\n"))
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output: `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[` +
			`{"name":"text_anchor_search","summary":"Found discovery tool implementation paths.","output_excerpt":"internal/capabilities/workspace/discovery_query.go:338: func (r *discoveryQueryRunner) run(ctx context.Context) {}\ninternal/runtime/agent/runner.go:574: result := r.runChildTask(ctx, req, opts)"}` +
			`]}`,
	}}

	paths := plan.consumableSourceBodyCandidatePaths(results, 4)
	for _, path := range paths {
		if path == "internal/runtime/agent/answer_composer.go" {
			t.Fatalf("candidate ranking must not inject undiscovered mechanism path, got %#v", paths)
		}
	}

	if len(paths) == 0 || paths[0] != "internal/capabilities/workspace/discovery_query.go" {
		t.Fatalf("expected actual discovered discovery tool path to remain consumable, got %#v", paths)
	}
}

func TestDelegatedReadOnlyCandidateConsumptionDemotesInventoryLayer(t *testing.T) {
	plan := newDelegatedDiscoveryPlan(strings.Join([]string{
		"Explain how discovery tools are routed.",
		"Named workspace scope: internal/runtime/agent, internal/capabilities/workspace, internal",
		"Key terms: discovery tools, routing",
	}, "\n"))
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output: `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[` +
			`{"name":"related_files","summary":"Found related Go files.","output_excerpt":"internal/runtime/agent/nearby.go\ninternal/runtime/agent/runner_child_tasks.go"},` +
			`{"name":"text_anchor_search","summary":"Found discovery routing implementation anchor.","output_excerpt":"internal/runtime/agent/runner_lane_policy.go:37: type delegatedDiscoveryPlan struct\ninternal/capabilities/workspace/discovery_query.go:338: func (r *discoveryQueryRunner) run(ctx context.Context) {}"}` +
			`]}`,
	}}

	paths := plan.consumableSourceBodyCandidatePaths(results, 4)
	if len(paths) == 0 || paths[0] != "internal/runtime/agent/runner_lane_policy.go" {
		t.Fatalf("expected implementation anchor to outrank related-files inventory, got %#v", paths)
	}

	if paths[0] == "internal/runtime/agent/nearby.go" {
		t.Fatalf("related-files inventory path should not lead candidate consumption, got %#v", paths)
	}
}
