package productworkflow

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func GenerateImplementationBrief(input BriefGenerationInput) BriefGenerationResult {
	taskID := strings.TrimSpace(input.TDD.TaskID)
	if taskID == "" {
		taskID = strings.TrimSpace(input.WorkflowState.TaskID)
	}
	out := BriefGenerationResult{
		Status:               "blocked",
		FilesToModify:        []string{},
		FileActions:          map[string]string{},
		FilesToReadFirst:     []string{},
		VerifiedCurrentState: []string{},
		RequiredBehavior:     []string{},
		Constraints:          []string{},
		Tests:                []string{},
		Acceptance:           []string{},
		Decisions:            []string{},
		Blockers:             []string{},
	}
	if !isAcceptedArtifact(input.PRD.FrontMatter.Status) {
		out.Blockers = append(out.Blockers, "prd.md must be accepted before generating a dispatch brief")
	}
	if CoverageGateHasBlockers(input.CoverageEvaluation) {
		out.Blockers = append(out.Blockers, "coverage gate is blocked; run `idiom product tdd --effort-root <path> --evaluate-coverage --json` and resolve blockers")
	}

	out.FilesToModify = briefUniqueSortedStrings(collectPlannedScopeFiles(input.ImplementationRecords, taskID))
	if len(out.FilesToModify) == 0 {
		out.Blockers = append(out.Blockers, "implementation ledger is missing atom_planned scope files for "+taskID)
	}
	for _, path := range out.FilesToModify {
		if input.ExistingWorkspacePath != nil && !input.ExistingWorkspacePath[path] {
			out.FileActions[path] = "Create new file: implement this atom's behavior in the new source file."
			continue
		}
		out.FileActions[path] = "Modify existing file at line 1 for this atom's bounded behavior."
	}
	out.FilesToReadFirst = briefUniqueSortedStrings(append([]string{
		"docs/design-prd-adr-brief-research-workflow.md",
		briefArtifactPath(input.ArtifactRootPath, "README.md"),
		briefArtifactPath(input.ArtifactRootPath, "workflow_state.json"),
		briefArtifactPath(input.ArtifactRootPath, "prd.md"),
		briefArtifactPath(input.ArtifactRootPath, "adrs/index.json"),
		briefArtifactPath(input.ArtifactRootPath, "tdd.json"),
		briefArtifactPath(input.ArtifactRootPath, "evidence/traceability_matrix.json"),
	}, collectAcceptedADRPaths(input.ArtifactRootPath, input.ADRs)...))

	out.VerifiedCurrentState = append(out.VerifiedCurrentState,
		fmt.Sprintf("workflow_state: %s/%s (task_id=%s)", input.WorkflowState.CurrentPhase, input.WorkflowState.CurrentState, input.WorkflowState.TaskID),
		fmt.Sprintf("prd: %s (status=%s)", defaultIfBlank(input.PRD.FrontMatter.PRDID, "unknown"), input.PRD.FrontMatter.Status),
		fmt.Sprintf("accepted_adrs: %d", len(collectAcceptedADRIDs(input.ADRs))),
		fmt.Sprintf("coverage_gate: %s (tier=%s)", input.CoverageEvaluation.Status, input.CoverageEvaluation.CoverageTier),
	)

	for _, req := range input.PRD.Requirements {
		reqID := strings.TrimSpace(req.ID)
		if reqID == "" {
			continue
		}
		out.RequiredBehavior = append(out.RequiredBehavior, fmt.Sprintf("%s: %s", reqID, strings.TrimSpace(req.Statement)))
	}
	if len(out.RequiredBehavior) == 0 {
		out.Blockers = append(out.Blockers, "prd.md has no requirements to dispatch")
	}

	out.Constraints = append(out.Constraints,
		"Do not dispatch work, execute models, or start lane execution from this brief.",
		"Do not mutate durable workflow artifacts except the requested brief output path.",
		"Do not edit files outside the Files To Modify allowlist.",
		"Preserve privacy: do not store raw prompts, raw transcripts, file bodies, secrets, generated brief bodies, or customer-identifying data in durable reports.",
		"Stop and report a blocker if accepted PRD, accepted ADR, TDD, traceability, coverage, or exact source evidence contradicts the brief.",
	)
	for _, adrID := range collectAcceptedADRIDs(input.ADRs) {
		out.Decisions = append(out.Decisions, fmt.Sprintf("%s is accepted and must be preserved unless superseded by a new accepted ADR.", adrID))
	}
	if len(out.Decisions) == 0 {
		out.Blockers = append(out.Blockers, "no accepted ADRs were found")
	}

	for _, obligation := range input.TDD.Obligations {
		obID := strings.TrimSpace(obligation.ID)
		if obID == "" {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(obligation.Status), "passed") {
			if strings.TrimSpace(obligation.Behavior) != "" {
				out.RequiredBehavior = append(out.RequiredBehavior, fmt.Sprintf("%s: %s", obID, strings.TrimSpace(obligation.Behavior)))
			}
			out.Acceptance = append(out.Acceptance, fmt.Sprintf("%s satisfied and traceable before dispatch-ready claim.", obID))
		}
		if strings.TrimSpace(obligation.Command) != "" {
			out.Tests = append(out.Tests, strings.TrimSpace(obligation.Command))
		}
	}
	out.Tests = briefUniqueSortedStrings(append(out.Tests, collectPlannedVerification(input.ImplementationRecords, taskID)...))
	out.Acceptance = briefUniqueSortedStrings(append(out.Acceptance, out.Tests...))
	if len(out.Tests) == 0 {
		out.Blockers = append(out.Blockers, "tdd.json contains no obligation commands or atom verification commands")
	}

	out.RequiredBehavior = briefUniqueSortedStrings(out.RequiredBehavior)
	out.Decisions = briefUniqueSortedStrings(out.Decisions)
	out.Blockers = briefUniqueSortedStrings(out.Blockers)
	if len(out.Blockers) == 0 {
		out.Status = "pass"
	}
	out.RenderedBrief = renderImplementationBriefMarkdown(out)
	return out
}

func renderImplementationBriefMarkdown(result BriefGenerationResult) string {
	var b strings.Builder
	b.WriteString("# Brief: Product Workflow Dispatch\n\n")
	b.WriteString("## Files To Modify\n\n")
	if len(result.FilesToModify) == 0 {
		b.WriteString("- none\n")
	} else {
		b.WriteString("| File | Action |\n| --- | --- |\n")
		for _, path := range result.FilesToModify {
			b.WriteString("| `")
			b.WriteString(path)
			b.WriteString("` | ")
			b.WriteString(result.FileActions[path])
			b.WriteString(" |\n")
		}
	}
	b.WriteString("\n## Files to Read First\n\n")
	if len(result.FilesToReadFirst) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, path := range result.FilesToReadFirst {
			b.WriteString("- `")
			b.WriteString(path)
			b.WriteString("`\n")
		}
	}
	b.WriteString("\n## Verified Current State\n\n")
	for _, line := range result.VerifiedCurrentState {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n## Required Behavior\n\n")
	for i, line := range result.RequiredBehavior {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, line))
	}
	b.WriteString("\n## Constraints\n\n")
	for _, line := range result.Constraints {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n## Tests\n\n")
	for _, line := range result.Tests {
		b.WriteString("- `")
		b.WriteString(line)
		b.WriteString("`\n")
	}
	b.WriteString("\n## Acceptance\n\n")
	for _, line := range result.Acceptance {
		if strings.HasPrefix(line, "go ") || strings.HasPrefix(line, "git ") || strings.HasPrefix(line, "idiom ") {
			b.WriteString("- `")
			b.WriteString(line)
			b.WriteString("` passes.\n")
		} else {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n## Decisions\n\n")
	for _, line := range result.Decisions {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n## Stop Rule\n\n")
	b.WriteString("Stop and report a blocker if required facts are missing, stale, contradicted, or outside the allowed scope.\n")
	return b.String()
}

func briefArtifactPath(artifactRoot, relPath string) string {
	root := strings.TrimRight(strings.TrimSpace(filepath.ToSlash(artifactRoot)), "/")
	rel := strings.Trim(strings.TrimSpace(filepath.ToSlash(relPath)), "/")
	if root == "" {
		return rel
	}
	if rel == "" {
		return root
	}
	return root + "/" + rel
}

func collectAcceptedADRPaths(artifactRoot string, adrs []ParsedADR) []string {
	paths := make([]string, 0, len(adrs))
	for _, adr := range adrs {
		if !isAcceptedArtifact(adr.Doc.FrontMatter.Status) {
			continue
		}
		if strings.TrimSpace(adr.RelPath) != "" {
			paths = append(paths, briefArtifactPath(artifactRoot, adr.RelPath))
		}
	}
	return paths
}

func collectAcceptedADRIDs(adrs []ParsedADR) []string {
	ids := []string{}
	for _, adr := range adrs {
		if !isAcceptedArtifact(adr.Doc.FrontMatter.Status) {
			continue
		}
		id := strings.TrimSpace(adr.Doc.FrontMatter.ADRID)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return briefUniqueSortedStrings(ids)
}

func collectPlannedScopeFiles(records []ImplementationLedgerRecord, taskID string) []string {
	files := []string{}
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec.RecordType != "atom_planned" || rec.Scope == nil || strings.TrimSpace(rec.TaskID) != strings.TrimSpace(taskID) {
			continue
		}
		files = append(files, dispatchImplementationPaths(rec.Scope.ExpectedFiles)...)
		files = append(files, dispatchImplementationPaths(rec.Scope.AllowedFiles)...)
		break
	}
	return files
}

func dispatchImplementationPaths(paths []string) []string {
	out := []string{}
	for _, path := range paths {
		clean := strings.TrimSpace(filepath.ToSlash(path))
		if clean == "" {
			continue
		}
		if strings.HasPrefix(clean, "docs/product-workflows/efforts/") ||
			strings.HasPrefix(clean, "docs/obsidian/") {
			continue
		}
		out = append(out, clean)
	}
	return out
}

func collectPlannedVerification(records []ImplementationLedgerRecord, taskID string) []string {
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec.RecordType == "atom_planned" && strings.TrimSpace(rec.TaskID) == strings.TrimSpace(taskID) {
			return rec.RequiredVerification
		}
	}
	return nil
}

func briefUniqueSortedStrings(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		trimmed := strings.TrimSpace(filepath.ToSlash(value))
		if trimmed == "" {
			continue
		}
		set[trimmed] = true
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
