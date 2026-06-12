package productworkflow

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/platform/util"
)

func GenerateImplementationBrief(input BriefGenerationInput) BriefGenerationResult {
	taskID := strings.TrimSpace(input.TDD.TaskID)
	if taskID == "" {
		taskID = strings.TrimSpace(input.WorkflowState.TaskID)
	}

	out := BriefGenerationResult{
		Status:               ProductWorkflowStatusBlocked,
		FilesToModify:        []string{},
		FileActions:          map[string]string{},
		FilesToReadFirst:     []string{},
		VerifiedCurrentState: []string{},
		RequiredBehavior:     []string{},
		ImplementationShape:  []string{},
		Constraints:          []string{},
		Tests:                []string{},
		Acceptance:           []string{},
		Decisions:            []string{},
		Blockers:             []string{},
	}
	if !isAcceptedArtifact(input.PRD.FrontMatter.Status) {
		out.Blockers = append(out.Blockers, "prd.md must be accepted before generating a dispatch brief")
	}

	if CoverageGateHasBlockers(input.CoverageEvaluation) && !CoverageGateHasOnlyTDDEvidenceBlockers(input.CoverageEvaluation) {
		out.Blockers = append(out.Blockers, "coverage gate is blocked; run `idiom product tdd --effort-root <path> --evaluate-coverage --json` and resolve blockers")
	}

	plannedFiles := collectPlannedScopeFiles(input.ImplementationRecords, taskID)

	out.FilesToModify = briefUniqueSortedStrings(applyBriefPathAliases(plannedFiles, input.WorkflowState.Context.PathAliases))

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
		briefArtifactPath(input.ArtifactRootPath, WorkflowStateFileName),
		briefArtifactPath(input.ArtifactRootPath, "prd.md"),
		briefArtifactPath(input.ArtifactRootPath, "adrs/index.json"),
		briefArtifactPath(input.ArtifactRootPath, "tdd.json"),
		briefArtifactPath(input.ArtifactRootPath, "evidence/traceability_matrix.json"),
	}, append(collectAcceptedADRPaths(input.ArtifactRootPath, input.ADRs), collectTDDReadFirstPaths(input.TDD)...)...))

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
		if testability := strings.TrimSpace(req.Testability); testability != "" {
			out.Acceptance = append(out.Acceptance, fmt.Sprintf("%s testability: %s", reqID, testability))
		}

		if metric := strings.TrimSpace(req.Metric); metric != "" {
			out.Acceptance = append(out.Acceptance, fmt.Sprintf("%s metric: %s", reqID, metric))
		}
	}

	for _, finding := range ImplementationAffectingClaimFindings(input.PRD, input.ADRs) {
		out.Blockers = append(out.Blockers, finding.Message)
	}

	for _, finding := range ImplementationAffectingClaimEvidenceFindings(input.PRD, input.ADRs, TraceabilityEvidenceIDs(input.Traceability)) {
		out.Blockers = append(out.Blockers, finding.Message)
	}

	if len(out.RequiredBehavior) == 0 {
		out.Blockers = append(out.Blockers, "prd.md has no requirements to dispatch")
	}

	out.Constraints = append(out.Constraints,
		"Do not mutate durable workflow artifacts except the requested brief output path.",
		"Do not edit files outside the Files To Modify allowlist.",
		"Preserve privacy: do not store raw prompts, raw transcripts, file bodies, secrets, generated brief bodies, or customer-identifying data in durable reports.",
		"Stop and report a blocker if accepted PRD, accepted ADR, TDD, traceability, coverage, or exact source evidence contradicts the brief.",
	)

	for _, adr := range input.ADRs {
		if !isAcceptedArtifact(adr.Doc.FrontMatter.Status) {
			continue
		}

		adrID := strings.TrimSpace(adr.Doc.FrontMatter.ADRID)
		if adrID == "" {
			continue
		}

		out.Decisions = append(out.Decisions, fmt.Sprintf("%s is accepted and must be preserved unless superseded by a new accepted ADR.", adrID))
		if decision := markdownSectionExcerpt(adr.Doc.BodyMarkdown, "Decision"); strings.TrimSpace(decision) != "" {
			out.Decisions = append(out.Decisions, fmt.Sprintf("%s decision: %s", adrID, strings.TrimSpace(decision)))
		} else if decision := markdownSectionExcerpt(adr.RawMarkdown, "Decision"); strings.TrimSpace(decision) != "" {
			out.Decisions = append(out.Decisions, fmt.Sprintf("%s decision: %s", adrID, strings.TrimSpace(decision)))
		}
	}

	if len(out.Decisions) == 0 {
		out.Blockers = append(out.Blockers, "no accepted ADRs were found")
	}

	for _, obligation := range input.TDD.Obligations {
		obID := strings.TrimSpace(obligation.ID)
		if obID == "" {
			continue
		}

		if !util.EqualTrimFold(obligation.Status, ProductWorkflowStatusPassed) {
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

	shapes, missingNewFileShapes := collectAcceptedADRCodeBlocks(input.ADRs, out.FileActions)

	out.ImplementationShape = briefUniqueSortedStrings(shapes)

	for _, path := range missingNewFileShapes {
		out.Blockers = append(out.Blockers, fmt.Sprintf("accepted ADRs do not include a concrete path-labeled implementation code block for new file %s", path))
	}

	out.Decisions = briefUniqueSortedStrings(out.Decisions)
	out.Blockers = briefUniqueSortedStrings(out.Blockers)

	if len(out.Blockers) == 0 {
		out.Status = ProductWorkflowStatusPass
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

	if len(result.ImplementationShape) > 0 {
		b.WriteString("\n## Implementation Shape\n\n")

		for _, block := range result.ImplementationShape {
			b.WriteString(block)

			if !strings.HasSuffix(block, "\n") {
				b.WriteByte('\n')
			}

			b.WriteByte('\n')
		}
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
		if briefLooksLikeCommand(line) {
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

func briefLooksLikeCommand(line string) bool {
	command := strings.TrimSpace(line)
	for _, prefix := range []string{
		"go ",
		"git ",
		"idiom ",
		"make ",
		"npm ",
		"yarn ",
		"pnpm ",
		"node ",
		"python ",
		"python3 ",
		"pytest",
		"cargo ",
		"mvn ",
		"gradle ",
		"./",
	} {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}

	return false
}

func ImplementationAffectingClaimFindings(prd PRDDocument, adrs []ParsedADR) []ProfileFinding {
	var findings []ProfileFinding

	for _, req := range prd.Requirements {
		if !implementationAffectingClaim(req.Statement) {
			continue
		}

		id := strings.TrimSpace(req.ID)
		if id == "" {
			id = "unknown requirement"
		}

		if len(req.OpenQuestions) > 0 {
			findings = append(findings, ProfileFinding{
				Kind:     "ungrounded_implementation_claim",
				File:     "prd.md",
				Severity: ProductWorkflowSeverityError,
				Message:  fmt.Sprintf("implementation-affecting claim in PRD requirement %s is classified as open_question/advisory and cannot authorize dispatch", id),
			})

			continue
		}

		if len(req.EvidenceRefs) == 0 {
			findings = append(findings, ProfileFinding{
				Kind:     "ungrounded_implementation_claim",
				File:     "prd.md",
				Severity: ProductWorkflowSeverityError,
				Message:  fmt.Sprintf("implementation-affecting claim in PRD requirement %s lacks evidence_refs and cannot authorize dispatch", id),
			})
		}
	}

	for _, adr := range adrs {
		if !isAcceptedArtifact(adr.Doc.FrontMatter.Status) {
			continue
		}

		text := adrImplementationClaimText(adr)
		if !implementationAffectingClaim(text) {
			continue
		}

		id := strings.TrimSpace(adr.Doc.FrontMatter.ADRID)
		if id == "" {
			id = "unknown ADR"
		}

		path := strings.TrimSpace(adr.RelPath)
		if path == "" {
			path = "adrs"
		}

		if adrOpenQuestionsHasSubstance(firstNonEmptyBriefClaimText(adr.Doc.BodyMarkdown, adr.RawMarkdown)) {
			findings = append(findings, ProfileFinding{
				Kind:     "ungrounded_implementation_claim",
				File:     path,
				Severity: ProductWorkflowSeverityError,
				Message:  fmt.Sprintf("implementation-affecting claim in accepted ADR %s is classified as open_question/advisory and cannot authorize dispatch", id),
			})

			continue
		}

		if len(adr.Doc.FrontMatter.EvidenceRefs) > 0 {
			continue
		}

		findings = append(findings, ProfileFinding{
			Kind:     "ungrounded_implementation_claim",
			File:     path,
			Severity: ProductWorkflowSeverityError,
			Message:  fmt.Sprintf("implementation-affecting claim in accepted ADR %s lacks evidence_refs and cannot authorize dispatch", id),
		})
	}

	return findings
}

func firstNonEmptyBriefClaimText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}

func adrImplementationClaimText(adr ParsedADR) string {
	decision := markdownSectionExcerpt(adr.Doc.BodyMarkdown, "Decision")
	if strings.TrimSpace(decision) == "" {
		decision = markdownSectionExcerpt(adr.RawMarkdown, "Decision")
	}

	return firstNonEmptyBriefClaimText(decision, adr.Doc.BodyMarkdown, adr.RawMarkdown)
}

func implementationAffectingClaim(text string) bool {
	lower := strings.ToLower(text)
	if strings.TrimSpace(lower) == "" {
		return false
	}

	actionTerms := []string{"use ", "build with ", "implement with ", "adopt ", "depend on ", "require ", "run on ", "integrate "}
	subjectTerms := []string{"platform", "framework", "runtime", "tooling", "toolchain", "sdk", "library", "dependency", "database", "queue", "broker", "api", "service"}
	hasAction := false

	for _, term := range actionTerms {
		if strings.Contains(lower, term) {
			hasAction = true
			break
		}
	}

	if !hasAction {
		return false
	}

	for _, term := range subjectTerms {
		if strings.Contains(lower, term) {
			return true
		}
	}

	return false
}

func adrOpenQuestionsHasSubstance(markdown string) bool {
	section := strings.ToLower(markdownSectionExcerpt(markdown, "Open Questions"))

	section = strings.TrimSpace(section)

	if section == "" {
		return false
	}

	switch section {
	case "none.", "none", "n/a", "no open questions.", "no open questions":
		return false
	default:
		return true
	}
}

func markdownSectionExcerpt(markdown string, section string) string {
	markdown = strings.TrimSpace(markdown)
	section = strings.TrimSpace(section)

	if markdown == "" || section == "" {
		return ""
	}

	lines := strings.Split(markdown, "\n")
	inSection := false

	var parts []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if inSection {
				break
			}

			inSection = util.EqualTrimFold(strings.TrimPrefix(trimmed, "## "), section)

			continue
		}

		if inSection && trimmed != "" {
			parts = append(parts, trimmed)
		}
	}

	return strings.Join(parts, " ")
}

func ImplementationAffectingClaimEvidenceFindings(prd PRDDocument, adrs []ParsedADR, evidenceIDs map[string]bool) []ProfileFinding {
	var findings []ProfileFinding

	for _, req := range prd.Requirements {
		if !implementationAffectingClaim(req.Statement) {
			continue
		}

		id := strings.TrimSpace(req.ID)
		if id == "" {
			id = "unknown requirement"
		}

		for _, ref := range req.EvidenceRefs {
			ref = strings.TrimSpace(ref)
			if ref == "" || evidenceIDs[ref] {
				continue
			}

			findings = append(findings, ProfileFinding{
				Kind:     "ungrounded_implementation_claim",
				File:     "prd.md",
				Severity: ProductWorkflowSeverityError,
				Message:  fmt.Sprintf("implementation-affecting claim in PRD requirement %s references unresolved evidence %q and cannot authorize dispatch", id, ref),
			})
		}
	}

	for _, adr := range adrs {
		if !isAcceptedArtifact(adr.Doc.FrontMatter.Status) {
			continue
		}

		text := adrImplementationClaimText(adr)
		if !implementationAffectingClaim(text) {
			continue
		}

		for _, ref := range adr.Doc.FrontMatter.EvidenceRefs {
			ref = strings.TrimSpace(ref)
			if ref == "" || evidenceIDs[ref] {
				continue
			}

			id := strings.TrimSpace(adr.Doc.FrontMatter.ADRID)
			if id == "" {
				id = "unknown ADR"
			}

			path := strings.TrimSpace(adr.RelPath)
			if path == "" {
				path = "adrs"
			}

			findings = append(findings, ProfileFinding{
				Kind:     "ungrounded_implementation_claim",
				File:     path,
				Severity: ProductWorkflowSeverityError,
				Message:  fmt.Sprintf("implementation-affecting claim in accepted ADR %s references unresolved evidence %q and cannot authorize dispatch", id, ref),
			})
		}
	}

	return findings
}

func TraceabilityEvidenceIDs(matrix TraceabilityMatrix) map[string]bool {
	ids := make(map[string]bool)

	for _, row := range matrix.Matrix {
		ref := strings.TrimSpace(row.EvidenceCard)
		if ref != "" {
			ids[ref] = true
		}
	}

	return ids
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

func collectAcceptedADRCodeBlocks(adrs []ParsedADR, actions map[string]string) ([]string, []string) {
	blocks := []string{}
	requiredPaths := briefActionPaths(actions)
	newFilePaths := briefNewFileActionPaths(actions)
	found := map[string]bool{}

	for _, adr := range adrs {
		if !isAcceptedArtifact(adr.Doc.FrontMatter.Status) {
			continue
		}

		for _, block := range markdownCodeBlocks(adr.RawMarkdown, requiredPaths) {
			if strings.TrimSpace(block.Body) != "" {
				found[block.Path] = true
				blocks = append(blocks, fmt.Sprintf("`%s`\n\n%s", block.Path, normalizeBriefCodeFence(block.Body)))
			}
		}
	}

	missing := []string{}

	for _, path := range newFilePaths {
		if !found[path] {
			missing = append(missing, path)
		}
	}

	return blocks, missing
}

func briefActionPaths(actions map[string]string) []string {
	paths := make([]string, 0, len(actions))
	for path := range actions {
		paths = append(paths, path)
	}

	sort.Strings(paths)

	return paths
}

func briefNewFileActionPaths(actions map[string]string) []string {
	paths := []string{}

	for path, action := range actions {
		if strings.Contains(strings.ToLower(action), "create new file") {
			paths = append(paths, path)
		}
	}

	sort.Strings(paths)

	return paths
}

func normalizeBriefCodeFence(block string) string {
	lines := strings.Split(block, "\n")
	if len(lines) == 0 {
		return block
	}

	if isMarkdownFenceLine(lines[0]) {
		lines[0] = "```text"
	}

	return strings.Join(lines, "\n")
}

type markdownCodeBlock struct {
	Path string
	Body string
}

func markdownCodeBlocks(markdown string, requiredPaths []string) []markdownCodeBlock {
	lines := strings.Split(markdown, "\n")
	blocks := []markdownCodeBlock{}

	var current []string

	currentPath := ""
	inFence := false
	previousNonEmpty := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			if inFence {
				current = append(current, line)
				if currentPath != "" {
					blocks = append(blocks, markdownCodeBlock{Path: currentPath, Body: strings.Join(current, "\n")})
				}

				current = nil
				currentPath = ""
				inFence = false

				continue
			}

			currentPath = pathMentionedInLine(previousNonEmpty, requiredPaths)
			inFence = true
			current = []string{line}

			continue
		}

		if inFence {
			current = append(current, line)
			continue
		}

		if trimmed != "" {
			previousNonEmpty = line
		}
	}

	return blocks
}

func pathMentionedInLine(line string, requiredPaths []string) string {
	for _, path := range requiredPaths {
		if strings.Contains(line, path) {
			return path
		}
	}

	return ""
}

func isMarkdownFenceLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func collectTDDReadFirstPaths(ledger TDDLedger) []string {
	paths := []string{}
	needsPackageManifest := false

	for _, obligation := range ledger.Obligations {
		owner := strings.TrimSpace(filepath.ToSlash(obligation.Owner))
		if owner != "" && looksLikeWorkspaceFile(owner) {
			paths = append(paths, owner)
		}

		command := strings.TrimSpace(obligation.Command)
		if strings.HasPrefix(command, "npm ") || strings.HasPrefix(command, "yarn ") || strings.HasPrefix(command, "pnpm ") {
			needsPackageManifest = true
		}
	}

	if needsPackageManifest {
		paths = append(paths, "package.json")
	}

	return briefUniqueSortedStrings(paths)
}

func looksLikeWorkspaceFile(path string) bool {
	path = strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
	if path == "" || filepath.IsAbs(path) {
		return false
	}

	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return false
		}
	}

	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}

	if strings.Contains(clean, "/") || filepath.Ext(clean) != "" {
		return true
	}

	switch clean {
	case "Makefile", "Dockerfile", "Containerfile", "Brewfile", "Rakefile", "Gemfile":
		return true
	default:
		return false
	}
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

func applyBriefPathAliases(paths []string, aliases map[string]string) []string {
	if len(aliases) == 0 {
		return paths
	}

	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if alias := strings.TrimSpace(aliases[normalizeBriefPathAliasKey(p)]); alias != "" {
			out = append(out, filepath.ToSlash(alias))
			continue
		}

		out = append(out, p)
	}

	return out
}

func normalizeBriefPathAliasKey(path string) string {
	return strings.TrimSpace(filepath.ToSlash(path))
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
