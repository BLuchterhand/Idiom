package agent

import (
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func TestDelegatedReadOnlyCandidateConsumptionPrefersObjectiveSemanticAnchorsOverListing(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "Inspect how the idiom harness uses subagents (child packets, task tool, lane delegation, and orchestration layers).\nNamed workspace scope: internal/agent, internal/workspace, internal\nKey terms: idiom, subagents, child, packets, delegation, orchestration, layers, summarize",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output: `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[` +
			`{"name":"target_listing","summary":"Listed internal/agent.","output_excerpt":"internal/agent/errors.go\ninternal/agent/final_answer_acceptance.go\ninternal/agent/runner_review_repair.go"},` +
			`{"name":"text_anchor_search","summary":"Found delegated child orchestration implementation paths.","output_excerpt":"internal/agent/runner_child_tasks.go:16: func (r *Runner) runChildTask(ctx context.Context, req workspace.ToolRequest, opts llama.ChatOptions) workspace.ToolResult\ninternal/agent/runner.go:574: result := r.runChildTask(ctx, req, opts)"}` +
			`]}`,
	}}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Results: results,
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "architecture_boundary", Path: "internal/agent", Pattern: "subagents|child|orchestration"},
	})

	if decision.Request.Tool != "read_file" {
		t.Fatalf("expected discovery candidate consumption read, got %#v", decision.Request)
	}
	if decision.Request.Path != "internal/agent/runner_child_tasks.go" && decision.Request.Path != "internal/agent/runner.go" {
		t.Fatalf("expected objective-relevant semantic anchor, got %#v", decision.Request)
	}
	if decision.Request.Path == "internal/agent/errors.go" {
		t.Fatalf("incidental listing path must not be consumed as source-body evidence: %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyCandidateConsumptionDoesNotForceListingOnlyPath(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "Explain how this harness uses subagents.\nNamed workspace scope: internal/agent, internal/workspace, internal",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"evidence":[{"name":"target_listing","summary":"Listed internal/agent.","output_excerpt":"internal/agent/errors.go\ninternal/agent/final_answer_acceptance.go\ninternal/agent/runner_review_repair.go"}]}`,
	}}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Results: results,
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "architecture_boundary", Path: "internal/agent", Pattern: "subagent|task"},
	})

	if decision.Request.Tool == "read_file" {
		t.Fatalf("listing-only discovery should not force a source-body read, got %#v", decision.Request)
	}
	if decision.Request.Path == "internal/agent/errors.go" {
		t.Fatalf("listing-only discovery must not promote incidental path, got %#v", decision.Request)
	}
}

func TestDelegatedReadOnlyCandidateConsumptionUsesObjectiveOverlapForWeakLayer(t *testing.T) {
	runner := &Runner{
		AgentName: "child:explorer",
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:   "delegated_read_only",
			AllowedTools: []string{"discovery_query", "search_content", "read_file"},
			Objective:    "Explain child orchestration for delegated packets.\nNamed workspace scope: internal/agent, internal/workspace, internal\nKey terms: child, orchestration, packets",
		},
	}
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output:  `{"evidence":[{"name":"target_listing","summary":"Listed internal/agent.","output_excerpt":"internal/agent/errors.go:1: package agent\ninternal/agent/runner_child_tasks.go:16: child orchestration packet flow\ninternal/agent/final_answer_acceptance.go:1: package agent"}]}`,
	}}

	decision := runner.applyLanePreflight(runnerLanePreflightInput{
		Results: results,
		Request: workspace.ToolRequest{Tool: "discovery_query", Purpose: "architecture_boundary", Path: "internal/agent", Pattern: "child|orchestration"},
	})

	if decision.Request.Tool != "read_file" || decision.Request.Path != "internal/agent/runner_child_tasks.go" {
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

func TestDelegatedReadOnlyCandidateConsumptionPrefersDiscoveredMechanismEvidenceOverInventory(t *testing.T) {
	plan := newDelegatedDiscoveryPlan(strings.Join([]string{
		"What can we add to our discovery tools in an effort at increasing their overall usefulness?",
		"Named workspace scope: internal/agent, internal/workspace, internal",
		"Key terms: discovery tools, usefulness, context graph, final answer",
	}, "\n"))
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output: `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[` +
			`{"name":"related_files","summary":"Found related Go files.","output_excerpt":"internal/workspace/discovery_query.go\ninternal/agent/runner_child_tasks.go"},` +
			`{"name":"go_workspace_symbol","summary":"No Go workspace symbols matched \"discovery|tool|usefulness\".","output_excerpt":"Symbol index: no matching symbols. capability: file_outline. language: go."},` +
			`{"name":"text_anchor_search","summary":"Found discovery tool implementation paths.","output_excerpt":"internal/workspace/discovery_query.go:338: func (r *discoveryQueryRunner) run(ctx context.Context) {}\ninternal/agent/answer_composer.go:18: func (AnswerComposer) Compose(input AnswerComposerInput) (string, bool)\ninternal/agent/runner_lane_policy.go:37: type delegatedDiscoveryPlan struct"}` +
			`]}`,
	}}

	paths := plan.consumableSourceBodyCandidatePaths(results, 4)
	if len(paths) == 0 {
		t.Fatal("expected discovered candidates")
	}
	if paths[0] != "internal/agent/answer_composer.go" {
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
		"Named workspace scope: internal/agent, internal/workspace, internal",
		"Key terms: discovery tools, usefulness, context graph",
	}, "\n"))
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output: `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[` +
			`{"name":"text_anchor_search","summary":"Found discovery tool implementation paths.","output_excerpt":"internal/workspace/discovery_query.go:338: func (r *discoveryQueryRunner) run(ctx context.Context) {}\ninternal/agent/runner.go:574: result := r.runChildTask(ctx, req, opts)"}` +
			`]}`,
	}}

	paths := plan.consumableSourceBodyCandidatePaths(results, 4)
	for _, path := range paths {
		if path == "internal/agent/answer_composer.go" {
			t.Fatalf("candidate ranking must not inject undiscovered mechanism path, got %#v", paths)
		}
	}
	if len(paths) == 0 || paths[0] != "internal/workspace/discovery_query.go" {
		t.Fatalf("expected actual discovered discovery tool path to remain consumable, got %#v", paths)
	}
}

func TestDelegatedReadOnlyCandidateConsumptionDemotesInventoryLayer(t *testing.T) {
	plan := newDelegatedDiscoveryPlan(strings.Join([]string{
		"Explain how discovery tools are routed.",
		"Named workspace scope: internal/agent, internal/workspace, internal",
		"Key terms: discovery tools, routing",
	}, "\n"))
	results := []workspace.ToolResult{{
		Tool:    "discovery_query",
		Summary: "Ran architecture_boundary discovery query",
		Output: `{"purpose":"architecture_boundary","artifact_path":".idiom/discovery/queries/query.json","evidence":[` +
			`{"name":"related_files","summary":"Found related Go files.","output_excerpt":"internal/agent/nearby.go\ninternal/agent/runner_child_tasks.go"},` +
			`{"name":"text_anchor_search","summary":"Found discovery routing implementation anchor.","output_excerpt":"internal/agent/runner_lane_policy.go:37: type delegatedDiscoveryPlan struct\ninternal/workspace/discovery_query.go:338: func (r *discoveryQueryRunner) run(ctx context.Context) {}"}` +
			`]}`,
	}}

	paths := plan.consumableSourceBodyCandidatePaths(results, 4)
	if len(paths) == 0 || paths[0] != "internal/agent/runner_lane_policy.go" {
		t.Fatalf("expected implementation anchor to outrank related-files inventory, got %#v", paths)
	}
	if paths[0] == "internal/agent/nearby.go" {
		t.Fatalf("related-files inventory path should not lead candidate consumption, got %#v", paths)
	}
}
