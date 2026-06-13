package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/config"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/surfaces/tui/implementationdispatch"
	"idiom/internal/workflows/featureplanner"
)

type implementationDispatchPlanDocument = implementationdispatch.PlanDocument

type implementationDispatchPlanDocumentPreflightResult struct {
	Path         string
	OK           bool
	AtomState    string
	Blockers     []string
	LintBlockers []string
	Warnings     []string
}

type implementationDispatchPlanDocumentRefineResult struct {
	OriginalPath          string
	OutputPath            string
	OK                    bool
	ExitCode              int
	Stdout                string
	Stderr                string
	Err                   error
	Document              implementationDispatchPlanDocument
	PostPreflightBlockers []implementationDispatchPlanDocumentPreflightResult
}

type implementationDispatchPlanDocumentResolutionBlocker struct {
	Ref        string
	Reason     string
	Candidates []string
}

type implementationDispatchPlanDocumentAtom struct {
	Path            string
	Change          string
	Excerpt         string
	Anchors         []string
	TestObligations []string
}

type implementationDispatchRecoveryDecision struct {
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
	Message       string `json:"message"`
	RetryAddendum string `json:"retry_addendum"`
}

var implementationDispatchPlanDocumentPreflightRunner = defaultImplementationDispatchPlanDocumentPreflight
var implementationDispatchPlanDocumentRefineRunner = defaultImplementationDispatchPlanDocumentRefine

func isPlanExecuteInternalFamily(family string) bool {
	return workerruntime.TaskFamilyIsPlanThenExecutePlanner(family) || workerruntime.TaskFamilyIsPlanThenExecuteReview(family)
}

func implementationDispatchHasPlanExecuteHandoff(prompt string, plan promptExecutionPlan, cfg config.Config) bool {
	requiredPaths, allowedPaths, planDocuments, blockers := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, plan, cfg)

	return len(requiredPaths) > 0 ||
		len(allowedPaths) > 0 ||
		len(planDocuments) > 0 ||
		len(blockers) > 0
}

func composeImplementationBrief(prompt string, plan promptExecutionPlan, cfg config.Config) string {
	requiredPaths, allowedPaths, planDocuments := implementationDispatchEditScopeAndPlanDocuments(prompt, plan, cfg)
	return composeImplementationBriefWithEditScopeAndPlanDocuments(prompt, plan, cfg, requiredPaths, allowedPaths, planDocuments)
}

func composeImplementationBriefWithEditScopeAndPlanDocuments(prompt string, plan promptExecutionPlan, cfg config.Config, requiredPaths []string, allowedPaths []string, planDocuments []implementationDispatchPlanDocument) string {
	var b strings.Builder

	b.WriteString("# Objective\n\n")
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n")

	brief := plan.runtimeBrief

	if len(requiredPaths) > 0 {
		b.WriteString("\n## Files to Modify\n\n")

		for _, p := range requiredPaths {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(p))
			b.WriteString("\n")
		}
	}

	if len(allowedPaths) > 0 {
		b.WriteString("\n## Candidate Edit Scope From Target References\n\n")

		for _, p := range allowedPaths {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(p))
			b.WriteString("\n")
		}
	}

	if len(planDocuments) > 0 {
		appendImplementationDispatchPlanDocuments(&b, prompt, planDocuments)
	}

	if len(brief.EvidencePaths) > 0 {
		b.WriteString("\n## Evidence Paths\n\n")

		for _, p := range brief.EvidencePaths {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(p))
			b.WriteString("\n")
		}
	}

	if len(brief.TargetRefs) > 0 {
		b.WriteString("\n## Target References\n\n")

		for _, ref := range brief.TargetRefs {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(ref))
			b.WriteString("\n")
		}
	}

	if len(brief.RequiredEditSnippets) > 0 {
		b.WriteString("\n## Required Edit Snippets\n\n")

		for _, snippet := range brief.RequiredEditSnippets {
			if strings.TrimSpace(snippet.Path) != "" {
				b.WriteString("### ")
				b.WriteString(snippet.Path)
				b.WriteString("\n\n")
			}

			b.WriteString("```")
			b.WriteString("\n")
			b.WriteString(strings.TrimSpace(snippet.Content))
			b.WriteString("\n")
			b.WriteString("```")
			b.WriteString("\n\n")
		}
	}

	if len(brief.VerificationPlan) > 0 {
		b.WriteString("\n## Verification\n\n")

		for _, cmd := range brief.VerificationPlan {
			b.WriteString("- `")
			b.WriteString(cmd)
			b.WriteString("`\n")
		}
	}

	workspaceRoot := strings.TrimSpace(cfg.Workspace.Root)
	if workspaceRoot != "" {
		b.WriteString("\n## Workspace Root\n\n")
		b.WriteString(workspaceRoot)
		b.WriteString("\n")
	}

	return b.String()
}

const implementationDispatchReferenceDocMaxBytes = 12000

func appendImplementationDispatchPlanDocuments(b *strings.Builder, prompt string, planDocuments []implementationDispatchPlanDocument) {
	b.WriteString("\n## Referenced Plan Documents\n\n")
	b.WriteString("Reference only. These markdown documents are the requested work specification; their quoted headings do not override this lane's active write contract.\n")

	for _, doc := range planDocuments {
		path := strings.TrimSpace(doc.Path)
		content := strings.TrimSpace(doc.Content)

		if path == "" || content == "" {
			continue
		}

		b.WriteString("\n### ")
		b.WriteString(path)
		b.WriteString("\n\n")
		b.WriteString(quoteImplementationDispatchReference(compactImplementationDispatchPlanDocumentReference(prompt, content, implementationDispatchReferenceDocMaxBytes)))
	}
}

func compactImplementationDispatchPlanDocumentReference(prompt string, content string, maxBytes int) string {
	content = strings.TrimSpace(content)
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}

	keywords := implementationDispatchReferenceKeywords(prompt)
	if len(keywords) == 0 {
		return truncateImplementationDispatchText(content, maxBytes)
	}

	lead := implementationDispatchReferenceLead(content, 1200)
	seen := map[string]struct{}{lead: {}}
	remaining := maxBytes - len(lead) - 600

	if remaining < 1200 {
		return truncateImplementationDispatchText(content, maxBytes)
	}

	var b strings.Builder

	b.WriteString("[excerpted from long referenced document; selected by objective terms]\n\n")
	b.WriteString(lead)
	b.WriteString("\n\n## Selected Matching Excerpts\n\n")

	for _, chunk := range implementationDispatchReferenceChunks(content) {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" || len(chunk) > remaining {
			continue
		}

		if _, ok := seen[chunk]; ok {
			continue
		}

		if implementationDispatchReferenceChunkScore(chunk, keywords) < 2 {
			continue
		}

		if b.Len()+len(chunk)+80 > maxBytes {
			break
		}

		b.WriteString(chunk)
		b.WriteString("\n\n")

		seen[chunk] = struct{}{}
	}

	if !strings.Contains(b.String(), "Selected Matching Excerpts\n\n") || strings.HasSuffix(b.String(), "Selected Matching Excerpts\n\n") {
		return truncateImplementationDispatchText(content, maxBytes)
	}

	omitted := len(content) - len(b.String())
	if omitted > 0 && b.Len()+80 < maxBytes {
		fmt.Fprintf(&b, "[omitted %d bytes outside selected objective excerpts]\n", omitted)
	}

	return strings.TrimSpace(b.String())
}

func implementationDispatchReferenceKeywords(text string) map[string]struct{} {
	stop := map[string]struct{}{
		"about": {}, "against": {}, "already": {}, "also": {}, "bounded": {}, "change": {}, "changes": {}, "code": {}, "complete": {}, "continue": {}, "continuing": {}, "current": {}, "docs": {}, "existing": {}, "file": {}, "files": {}, "first": {}, "focus": {}, "from": {}, "implementation": {}, "implement": {}, "make": {}, "minimal": {}, "only": {}, "path": {}, "paths": {}, "please": {}, "proving": {}, "request": {}, "requested": {}, "source": {}, "tests": {}, "that": {}, "this": {}, "through": {}, "work": {},
	}
	keywords := map[string]struct{}{}

	for _, raw := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) {
		token := strings.Trim(raw, "_-")
		if len(token) < 4 {
			continue
		}

		if _, ignored := stop[token]; ignored {
			continue
		}

		keywords[token] = struct{}{}
	}

	return keywords
}

func implementationDispatchReferenceLead(content string, maxBytes int) string {
	chunks := implementationDispatchReferenceChunks(content)
	if len(chunks) == 0 {
		return truncateImplementationDispatchText(content, maxBytes)
	}

	var b strings.Builder

	for i, chunk := range chunks {
		if i >= 2 {
			break
		}

		if b.Len()+len(chunk)+2 > maxBytes {
			break
		}

		if b.Len() > 0 {
			b.WriteString("\n\n")
		}

		b.WriteString(chunk)
	}

	if strings.TrimSpace(b.String()) == "" {
		return truncateImplementationDispatchText(content, maxBytes)
	}

	return strings.TrimSpace(b.String())
}

func implementationDispatchReferenceChunks(content string) []string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")

	var chunks []string

	for _, chunk := range strings.Split(normalized, "\n\n") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}

		chunks = append(chunks, chunk)
	}

	return chunks
}

func implementationDispatchReferenceChunkScore(chunk string, keywords map[string]struct{}) int {
	lower := strings.ToLower(chunk)
	score := 0

	for keyword := range keywords {
		if strings.Contains(lower, keyword) {
			score++
		}
	}

	return score
}

func quoteImplementationDispatchReference(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "> (empty)\n"
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ">"
			continue
		}

		lines[i] = "> " + line
	}

	return strings.Join(lines, "\n") + "\n"
}

func truncateImplementationDispatchText(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}

	return text[:max] + fmt.Sprintf("\n\n[truncated %d bytes]\n", len(text)-max)
}

func preflightImplementationDispatchPlanDocuments(ctx context.Context, cfg config.Config, planDocuments []implementationDispatchPlanDocument) []implementationDispatchPlanDocumentPreflightResult {
	var blocked []implementationDispatchPlanDocumentPreflightResult

	for _, doc := range planDocuments {
		if strings.TrimSpace(doc.Path) == "" {
			continue
		}

		if doc.ReferenceOnly {
			continue
		}

		result := implementationDispatchPlanDocumentPreflightRunner(ctx, cfg, doc)
		if !result.OK || len(result.Blockers) > 0 {
			if strings.TrimSpace(result.Path) == "" {
				result.Path = doc.Path
			}

			blocked = append(blocked, result)
		}
	}

	return blocked
}

func defaultImplementationDispatchPlanDocumentPreflight(ctx context.Context, cfg config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentPreflightResult {
	result := implementationDispatchPlanDocumentPreflightResult{Path: doc.Path, OK: true}
	root := implementationDispatchWorkspaceRoot(cfg)

	if strings.TrimSpace(root) == "" {
		result.OK = false
		result.Blockers = append(result.Blockers, "workspace root is unavailable; cannot run dispatch preflight")

		return result
	}

	abs, ok := implementationDispatchWorkspaceFile(root, doc.Path)
	if !ok {
		result.OK = false
		result.Blockers = append(result.Blockers, "referenced plan document path is outside the workspace")

		return result
	}

	binary, err := implementationDispatchIdiomBinary()
	if err != nil {
		result.OK = false
		result.Blockers = append(result.Blockers, "dispatch preflight unavailable: "+err.Error())

		return result
	}

	atomArgs := []string{"atom", "intake", "--spec", abs, "--project-root", root, "--json"}
	if doc.AllowDirtyAtomScope {
		atomArgs = append(atomArgs, "--allow-dirty")
	}

	atom := runImplementationDispatchPreflightCommand(ctx, root, binary, atomArgs...)
	if atom.ExitCode != 0 {
		atomState, blockers := implementationDispatchAtomIntakeBlocker(atom)

		result.AtomState = atomState
		result.Blockers = append(result.Blockers, blockers...)

		if implementationDispatchAtomStateIsSourceSatisfied(atomState) {
			result.OK = false
			return result
		}
	}

	lint := runImplementationDispatchPreflightCommand(ctx, root, binary, "brief", "lint", abs, "--json")
	if lint.ExitCode != 0 {
		blockers := implementationDispatchBriefLintBlockers(lint)

		result.LintBlockers = append(result.LintBlockers, blockers...)
		result.Blockers = append(result.Blockers, blockers...)
	} else {
		result.Warnings = append(result.Warnings, implementationDispatchBriefLintWarnings(lint)...)
	}

	result.OK = len(result.Blockers) == 0

	return result
}

func defaultImplementationDispatchPlanDocumentRefine(ctx context.Context, cfg config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentRefineResult {
	result := implementationDispatchPlanDocumentRefineResult{OriginalPath: doc.Path}
	root := implementationDispatchWorkspaceRoot(cfg)

	if strings.TrimSpace(root) == "" {
		result.Err = fmt.Errorf("workspace root is unavailable")
		result.ExitCode = 2

		return result
	}

	absSpec, ok := implementationDispatchWorkspaceFile(root, doc.Path)
	if !ok {
		result.Err = fmt.Errorf("referenced plan document path is outside the workspace")
		result.ExitCode = 2

		return result
	}

	binary, err := implementationDispatchIdiomBinary()
	if err != nil {
		result.Err = err
		result.ExitCode = 2

		return result
	}

	title := implementationDispatchRefineTitle(doc.Path)
	outRel := filepath.Join(".idiom", "refine", title, "accepted.md")
	outAbs, ok := implementationDispatchWorkspaceFile(root, outRel)

	if !ok {
		result.Err = fmt.Errorf("refine output path is outside the workspace")
		result.ExitCode = 2

		return result
	}

	result.OutputPath = outRel
	if err := os.MkdirAll(filepath.Dir(outAbs), 0o755); err != nil {
		result.Err = err
		result.ExitCode = 1

		return result
	}

	args := []string{"lane", "refine", "--spec", absSpec, "--output", outAbs, "--project-root", root, "--title", title}
	if configPath := filepath.Join(root, ".idiom", "config.json"); implementationDispatchFileExists(configPath) {
		args = append([]string{"lane", "refine", "--config", configPath}, args[2:]...)
	}

	cmdResult := runImplementationDispatchCommand(ctx, root, 30*time.Minute, binary, args...)

	result.ExitCode = cmdResult.ExitCode
	result.Stdout = cmdResult.Stdout
	result.Stderr = cmdResult.Stderr
	result.Err = cmdResult.Err

	if cmdResult.ExitCode != 0 {
		candidate, candidatePath, blockers, ok := materializeImplementationDispatchNonzeroRefineAcceptedCandidate(ctx, cfg, outRel, doc.AllowDirtyAtomScope)
		if strings.TrimSpace(candidatePath) != "" {
			result.OutputPath = candidatePath
			result.Document = candidate
		}

		if len(blockers) > 0 {
			result.PostPreflightBlockers = blockers
		}

		if ok {
			result.OK = true
			return result
		}

		if strings.TrimSpace(result.Document.Path) == "" {
			if refined, ok := readImplementationDispatchPlanDocument(root, outRel); ok {
				refined.AllowDirtyAtomScope = doc.AllowDirtyAtomScope
				result.Document = refined
			}
		}

		if strings.TrimSpace(result.OutputPath) == "" {
			result.OutputPath = outRel
		}

		return result
	}

	refined, ok := readImplementationDispatchPlanDocument(root, outRel)
	if !ok {
		result.ExitCode = 1
		result.Err = fmt.Errorf("refine completed but output brief was not readable: %s", outRel)

		return result
	}

	refined.AllowDirtyAtomScope = doc.AllowDirtyAtomScope
	result.Document = refined
	result.OK = true

	return result
}

func materializeImplementationDispatchNonzeroRefineAcceptedCandidate(ctx context.Context, cfg config.Config, acceptedRel string, allowDirtyAtomScope ...bool) (implementationDispatchPlanDocument, string, []implementationDispatchPlanDocumentPreflightResult, bool) {
	root := implementationDispatchWorkspaceRoot(cfg)
	if strings.TrimSpace(root) == "" {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	acceptedAbs, ok := implementationDispatchWorkspaceFile(root, acceptedRel)
	if !ok {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	body, err := os.ReadFile(acceptedAbs)
	if err != nil {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	sanitized, changed := sanitizeImplementationDispatchNonzeroRefineAcceptedContent(string(body))
	if !changed {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	candidateRel := strings.TrimSuffix(acceptedRel, filepath.Ext(acceptedRel)) + ".dispatch" + filepath.Ext(acceptedRel)
	candidateAbs, ok := implementationDispatchWorkspaceFile(root, candidateRel)

	if !ok {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	if err := os.WriteFile(candidateAbs, []byte(sanitized), 0o644); err != nil {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	candidate, ok := readImplementationDispatchPlanDocument(root, candidateRel)
	if !ok {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	candidate.AllowDirtyAtomScope = len(allowDirtyAtomScope) > 0 && allowDirtyAtomScope[0]
	if blocked := preflightImplementationDispatchPlanDocuments(ctx, cfg, []implementationDispatchPlanDocument{candidate}); len(blocked) > 0 {
		return candidate, candidateRel, blocked, false
	}

	return candidate, candidateRel, nil, true
}

func sanitizeImplementationDispatchNonzeroRefineAcceptedContent(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	changed := false
	skippingLeadingWarning := true

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if skippingLeadingWarning {
			if trimmed == "" {
				if changed {
					continue
				}

				out = append(out, line)

				continue
			}

			if strings.HasPrefix(trimmed, "> WARNING: refine exited non-zero") {
				changed = true
				continue
			}

			skippingLeadingWarning = false
		}

		if strings.HasPrefix(trimmed, "<!-- idiom:refine-drafted") && !strings.Contains(trimmed, "reason=ship-as-is") {
			out = append(out, "<!-- idiom:tui-refine-accepted-after-structural-preflight -->")
			changed = true

			continue
		}

		out = append(out, line)
	}

	return strings.TrimLeft(strings.Join(out, "\n"), "\n"), changed
}

func implementationDispatchIdiomBinary() (string, error) {
	if path := strings.TrimSpace(os.Getenv("IDIOM_BIN")); path != "" {
		return path, nil
	}

	path, err := os.Executable()
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("current executable path is empty")
	}

	return path, nil
}

type implementationDispatchPreflightCommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error
}

func runImplementationDispatchPreflightCommand(ctx context.Context, root string, binary string, args ...string) implementationDispatchPreflightCommandResult {
	return runImplementationDispatchCommand(ctx, root, 45*time.Second, binary, args...)
}

func runImplementationDispatchCommand(ctx context.Context, root string, timeout time.Duration, binary string, args ...string) implementationDispatchPreflightCommandResult {
	if timeout <= 0 {
		timeout = 45 * time.Second
	}

	preflightCtx, cancel := context.WithTimeout(ctx, timeout)

	defer cancel()

	cmd := exec.CommandContext(preflightCtx, binary, args...)

	cmd.Dir = root

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := implementationDispatchPreflightCommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err == nil {
		return result
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		result.Err = err

		return result
	}

	if preflightCtx.Err() != nil {
		result.ExitCode = 124
		result.Err = preflightCtx.Err()

		return result
	}

	result.ExitCode = 2
	result.Err = err

	return result
}

func implementationDispatchFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func implementationDispatchAtomIntakeBlocker(cmd implementationDispatchPreflightCommandResult) (string, []string) {
	type atomReport struct {
		State        string   `json:"state"`
		Dispatchable bool     `json:"dispatchable"`
		Reasons      []string `json:"reasons"`
		NextActions  []string `json:"next_actions"`
		Dirty        []string `json:"dirty_paths"`
		Invalid      []string `json:"invalid_paths"`
	}

	var report atomReport

	if err := json.Unmarshal([]byte(cmd.Stdout), &report); err == nil && strings.TrimSpace(report.State) != "" {
		state := strings.TrimSpace(report.State)

		var parts []string

		parts = append(parts, "atom intake state="+state)

		if len(report.Reasons) > 0 {
			parts = append(parts, strings.Join(report.Reasons, "; "))
		}

		if len(report.Dirty) > 0 {
			parts = append(parts, "dirty paths: "+strings.Join(report.Dirty, ", "))
		}

		if len(report.Invalid) > 0 {
			parts = append(parts, "invalid paths: "+strings.Join(report.Invalid, ", "))
		}

		if len(report.NextActions) > 0 {
			parts = append(parts, "next: "+strings.Join(report.NextActions, "; "))
		}

		return state, []string{strings.Join(parts, " - ")}
	}

	return "", []string{"atom intake failed: " + implementationDispatchCompactCommandOutput(cmd)}
}

func implementationDispatchAtomStateIsSourceSatisfied(state string) bool {
	normalized := strings.ToLower(strings.TrimSpace(state))
	return normalized == "source-satisfied" || normalized == "source satisfied"
}

type implementationDispatchBriefLintReport struct {
	Exit     int `json:"exit"`
	Findings []struct {
		Severity string `json:"severity"`
		Category string `json:"category"`
		Message  string `json:"message"`
	} `json:"findings"`
}

func implementationDispatchBriefLintBlockers(cmd implementationDispatchPreflightCommandResult) []string {
	report, ok := parseImplementationDispatchBriefLintReport(cmd)
	if !ok {
		return []string{"brief lint failed: " + implementationDispatchCompactCommandOutput(cmd)}
	}

	var blockers []string

	for _, finding := range report.Findings {
		if util.EqualTrimFold(finding.Severity, "WARN") || util.EqualTrimFold(finding.Severity, "INFO") {
			continue
		}

		message := strings.TrimSpace(finding.Message)
		if message == "" {
			continue
		}

		blockers = append(blockers, "brief lint: "+message)
		if len(blockers) >= 4 {
			break
		}
	}

	if len(blockers) == 0 {
		blockers = append(blockers, fmt.Sprintf("brief lint failed with exit %d", report.Exit))
	}

	return blockers
}

func implementationDispatchBriefLintWarnings(cmd implementationDispatchPreflightCommandResult) []string {
	report, ok := parseImplementationDispatchBriefLintReport(cmd)
	if !ok {
		return nil
	}

	var warnings []string

	for _, finding := range report.Findings {
		if !util.EqualTrimFold(finding.Severity, "WARN") {
			continue
		}

		message := strings.TrimSpace(finding.Message)
		if message == "" {
			continue
		}

		warnings = append(warnings, "brief lint: "+message)
		if len(warnings) >= 3 {
			break
		}
	}

	return warnings
}

func parseImplementationDispatchBriefLintReport(cmd implementationDispatchPreflightCommandResult) (implementationDispatchBriefLintReport, bool) {
	var report implementationDispatchBriefLintReport

	if err := json.Unmarshal([]byte(cmd.Stdout), &report); err != nil {
		return report, false
	}

	return report, true
}

func implementationDispatchCompactCommandOutput(cmd implementationDispatchPreflightCommandResult) string {
	parts := []string{}
	if cmd.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit=%d", cmd.ExitCode))
	}

	if cmd.Err != nil {
		parts = append(parts, cmd.Err.Error())
	}

	combined := strings.TrimSpace(cmd.Stdout + "\n" + cmd.Stderr)
	if combined != "" {
		parts = append(parts, collapseWhitespace(combined))
	}

	if len(parts) == 0 {
		return "no diagnostic output"
	}

	return strings.Join(parts, "; ")
}

func humanPlanDocumentPreflightBlockedMessage(results []implementationDispatchPlanDocumentPreflightResult) string {
	var b strings.Builder

	b.WriteString("Plan-execute preflight blocked dispatch before the planner ran.\n\n")
	b.WriteString("The referenced document is not a dispatch-ready plan-execute brief yet. Convert it through refine or rewrite it with the canonical `## Files To Modify`, verified current state, exact `old_text`/`new_text` for existing files or missing/new-file `replace_all` edit shape, and acceptance commands before retrying.\n")

	for _, result := range results {
		path := strings.TrimSpace(result.Path)
		if path == "" {
			path = "(unknown plan document)"
		}

		b.WriteString("\n")
		b.WriteString("- `")
		b.WriteString(path)
		b.WriteString("`")

		if len(result.Blockers) == 0 {
			b.WriteString(": preflight failed without a structured blocker")

			continue
		}

		b.WriteString(":\n")

		for _, blocker := range result.Blockers {
			blocker = strings.TrimSpace(blocker)
			if blocker == "" {
				continue
			}

			b.WriteString("  - ")
			b.WriteString(blocker)
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func humanPlanDocumentResolutionBlockedMessage(blockers []implementationDispatchPlanDocumentResolutionBlocker) string {
	var b strings.Builder

	b.WriteString("Plan-execute dispatch blocked before the planner ran.\n\n")
	b.WriteString("The TUI must resolve referenced design documents into a concrete brief and edit scope before plan-execute. Resolve the document path, then retry.\n")

	for _, blocker := range blockers {
		ref := strings.TrimSpace(blocker.Ref)
		if ref == "" {
			ref = "(unknown design document)"
		}

		b.WriteString("\n")
		b.WriteString("- `")
		b.WriteString(ref)
		b.WriteString("`")

		switch strings.TrimSpace(blocker.Reason) {
		case "ambiguous":
			b.WriteString(": ambiguous shorthand matched multiple markdown files:")

			for _, candidate := range blocker.Candidates {
				candidate = strings.TrimSpace(candidate)
				if candidate == "" {
					continue
				}

				b.WriteString("\n  - `")
				b.WriteString(candidate)
				b.WriteString("`")
			}
		default:
			b.WriteString(": no readable markdown design document matched this reference")
		}
	}

	return strings.TrimSpace(b.String())
}

func implementationDispatchPreflightCanRefine(results []implementationDispatchPlanDocumentPreflightResult) bool {
	for _, result := range results {
		if len(result.LintBlockers) > 0 {
			return true
		}

		switch strings.TrimSpace(result.AtomState) {
		case "missing-scope":
			return true
		}
	}

	return false
}

func implementationDispatchRefineTitle(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "plan-document"
	}

	path = strings.TrimSuffix(path, filepath.Ext(path))

	var b strings.Builder

	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}

	title := strings.Trim(b.String(), "-")
	if title == "" {
		title = "plan-document"
	}

	title = limitImplementationDispatchRefineTitle(title)

	return "tui-" + title
}

func limitImplementationDispatchRefineTitle(title string) string {
	const maxTitleBytes = 80

	if len(title) <= maxTitleBytes {
		return title
	}

	h := fnv.New64a()

	_, _ = h.Write([]byte(title))

	suffix := fmt.Sprintf("%016x", h.Sum64())
	prefixLen := maxTitleBytes - len(suffix) - 1

	if prefixLen < 1 {
		return suffix
	}

	prefix := strings.Trim(title[:prefixLen], "-._")
	if prefix == "" {
		return suffix
	}

	return prefix + "-" + suffix
}

func implementationDispatchEditScopeFromPlanDocuments(prompt string, plan promptExecutionPlan, cfg config.Config, planDocuments []implementationDispatchPlanDocument) ([]string, []string) {
	requiredPaths := appendUniqueBriefStrings(nil, plan.runtimeBrief.RequiredEditPaths...)

	requiredPaths = appendUniqueBriefStrings(requiredPaths, requiredEditPathsFromPrompt(prompt)...)

	allowedPaths := allowedEditPathsFromPrompt(prompt)
	root := implementationDispatchWorkspaceRoot(cfg)

	for _, doc := range planDocuments {
		if implementationDispatchPlanDocumentScopeIsRequired(doc, root) {
			requiredPaths = appendUniqueBriefStrings(requiredPaths, doc.EditPaths...)
			continue
		}

		allowedPaths = appendUniqueBriefStrings(allowedPaths, doc.EditPaths...)
	}

	return requiredPaths, allowedPaths
}

func implementationDispatchPlanDocumentScopeIsRequired(doc implementationDispatchPlanDocument, root string) bool {
	return implementationDispatchIsAtomizedPlanDocumentPath(doc.Path) ||
		implementationDispatchIsRefinedAcceptedPlanDocumentPath(doc.Path) ||
		implementationDispatchPlanDocumentScopeIsAllMissing(root, doc.EditPaths)
}

func implementationDispatchPlanDocumentScopeIsAllMissing(root string, paths []string) bool {
	root = strings.TrimSpace(root)
	if root == "" || len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		abs, ok := implementationDispatchWorkspaceFile(root, path)
		if !ok {
			return false
		}

		if _, err := os.Stat(abs); err == nil || !os.IsNotExist(err) {
			return false
		}
	}

	return true
}

func implementationDispatchIsRefinedAcceptedPlanDocumentPath(path string) bool {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" {
		return false
	}

	if !strings.HasPrefix(path, ".idiom/refine/") {
		return false
	}

	base := filepath.Base(path)

	return base == "accepted.md" || strings.HasSuffix(base, ".dispatch.md")
}

func replaceImplementationDispatchPlanDocument(planDocuments []implementationDispatchPlanDocument, originalPath string, refined implementationDispatchPlanDocument) []implementationDispatchPlanDocument {
	var out []implementationDispatchPlanDocument

	replaced := false

	for _, doc := range planDocuments {
		if doc.Path == originalPath {
			if len(refined.AuthorizedNewFiles) == 0 {
				refined.AuthorizedNewFiles = append([]string{}, doc.AuthorizedNewFiles...)
			}

			out = append(out, refined)
			replaced = true

			continue
		}

		out = append(out, doc)
	}

	if !replaced {
		out = append(out, refined)
	}

	return out
}

func humanPlanDocumentRefineFailedMessage(preflight []implementationDispatchPlanDocumentPreflightResult, refineResults []implementationDispatchPlanDocumentRefineResult) string {
	var b strings.Builder

	b.WriteString("Plan-execute preflight blocked dispatch, and automatic refine did not produce an accepted brief.\n\n")
	b.WriteString("The TUI tried the Colony-style refine step, but the refined artifact still needs human review before plan-execute dispatch.\n")

	for _, result := range refineResults {
		path := strings.TrimSpace(result.OriginalPath)
		if path == "" {
			path = "(unknown plan document)"
		}

		b.WriteString("\n")
		b.WriteString("- `")
		b.WriteString(path)
		b.WriteString("`")

		if strings.TrimSpace(result.OutputPath) != "" {
			b.WriteString(" -> `")
			b.WriteString(result.OutputPath)
			b.WriteString("`")
		}

		if result.ExitCode != 0 {
			b.WriteString(fmt.Sprintf(": refine exited %d", result.ExitCode))
		} else {
			b.WriteString(": refine did not return a readable accepted brief")
		}

		if result.Err != nil {
			b.WriteString(" (")
			b.WriteString(result.Err.Error())
			b.WriteString(")")
		}

		for _, diag := range implementationDispatchRefineDiagnostics(result) {
			diag = strings.TrimSpace(diag)
			if diag == "" {
				continue
			}

			b.WriteString("\n  - ")
			b.WriteString(diag)
		}
	}

	if len(preflight) > 0 {
		b.WriteString("\n\nOriginal preflight blockers:")

		for _, result := range preflight {
			for _, blocker := range result.Blockers {
				blocker = strings.TrimSpace(blocker)
				if blocker == "" {
					continue
				}

				b.WriteString("\n- ")
				b.WriteString(blocker)
			}
		}
	}

	return strings.TrimSpace(b.String())
}

func implementationDispatchRefineDiagnostics(result implementationDispatchPlanDocumentRefineResult) []string {
	var diagnostics []string

	for _, text := range []string{result.Stderr, result.Stdout} {
		for _, line := range strings.Split(text, "\n") {
			line = collapseWhitespace(strings.TrimSpace(line))
			if line == "" {
				continue
			}

			lower := strings.ToLower(line)
			switch {
			case strings.Contains(lower, "refine:"):
				diagnostics = append(diagnostics, line)
			case strings.Contains(lower, "brief lint:"):
				diagnostics = append(diagnostics, line)
			case strings.Contains(lower, "single-purpose decomposition failed"):
				diagnostics = append(diagnostics, line)
			case strings.Contains(lower, "exact-anchor specificity failed"):
				diagnostics = append(diagnostics, line)
			case strings.Contains(lower, "plan-execute new-file body missing"):
				diagnostics = append(diagnostics, line)
			case strings.Contains(lower, "accepted.md"):
				diagnostics = append(diagnostics, line)
			}
		}
	}

	return appendUniqueBriefStrings(nil, diagnostics...)
}

func humanPlanDocumentRefinePostPreflightBlockedMessage(refineResults []implementationDispatchPlanDocumentRefineResult, blocked []implementationDispatchPlanDocumentPreflightResult) string {
	var b strings.Builder

	b.WriteString("Automatic refine produced a brief, but intake/lint still blocked plan-execute dispatch.\n")

	for _, result := range refineResults {
		if strings.TrimSpace(result.OutputPath) != "" {
			b.WriteString("\nRefined brief: `")
			b.WriteString(result.OutputPath)
			b.WriteString("`")
		}
	}

	b.WriteString("\n\nRemaining blockers:")

	for _, result := range blocked {
		path := strings.TrimSpace(result.Path)
		if path == "" {
			path = "(unknown brief)"
		}

		b.WriteString("\n- `")
		b.WriteString(path)
		b.WriteString("`")

		for _, blocker := range result.Blockers {
			blocker = strings.TrimSpace(blocker)
			if blocker == "" {
				continue
			}

			b.WriteString("\n  - ")
			b.WriteString(blocker)
		}
	}

	return strings.TrimSpace(b.String())
}

func implementationDispatchEditScopeAndPlanDocuments(prompt string, plan promptExecutionPlan, cfg config.Config) ([]string, []string, []implementationDispatchPlanDocument) {
	requiredPaths, allowedPaths, planDocuments, _ := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, plan, cfg)
	return requiredPaths, allowedPaths, planDocuments
}

func implementationDispatchEditScopePlanDocumentsAndResolution(prompt string, plan promptExecutionPlan, cfg config.Config) ([]string, []string, []implementationDispatchPlanDocument, []implementationDispatchPlanDocumentResolutionBlocker) {
	requiredPaths := appendUniqueBriefStrings(nil, plan.runtimeBrief.RequiredEditPaths...)

	requiredPaths = appendUniqueBriefStrings(requiredPaths, requiredEditPathsFromPrompt(prompt)...)

	allowedPaths := allowedEditPathsFromPrompt(prompt)
	planDocuments, blockers := implementationDispatchPlanDocumentsAndResolutionBlockers(prompt, plan, cfg)
	root := implementationDispatchWorkspaceRoot(cfg)

	for _, doc := range planDocuments {
		if implementationDispatchPlanDocumentScopeIsRequired(doc, root) {
			requiredPaths = appendUniqueBriefStrings(requiredPaths, doc.EditPaths...)
			continue
		}

		allowedPaths = appendUniqueBriefStrings(allowedPaths, doc.EditPaths...)
	}

	return requiredPaths, allowedPaths, planDocuments, blockers
}

func implementationDispatchPlanDocumentsAndResolutionBlockers(prompt string, plan promptExecutionPlan, cfg config.Config) ([]implementationDispatchPlanDocument, []implementationDispatchPlanDocumentResolutionBlocker) {
	root := implementationDispatchWorkspaceRoot(cfg)
	if strings.TrimSpace(root) == "" {
		return nil, nil
	}

	refs := implementationDispatchPlanDocumentRefs(prompt, plan)
	explicitEditPaths := appendUniqueBriefStrings(nil, plan.runtimeBrief.RequiredEditPaths...)

	explicitEditPaths = appendUniqueBriefStrings(explicitEditPaths, requiredEditPathsFromPrompt(prompt)...)
	explicitEditPaths = appendUniqueBriefStrings(explicitEditPaths, allowedEditPathsFromPrompt(prompt)...)

	var (
		docs     []implementationDispatchPlanDocument
		blockers []implementationDispatchPlanDocumentResolutionBlocker
	)

	blockUnresolvedPlanDocuments := implementationDispatchPromptRequiresResolvedPlanDocument(prompt, plan, refs)

	for _, ref := range refs {
		if implementationDispatchRefIsExplicitEditPath(ref, explicitEditPaths) {
			continue
		}

		doc, candidates, ok := resolveImplementationDispatchPlanDocument(root, ref)
		if !ok {
			if blocker, blocked := implementationDispatchPlanDocumentResolutionBlockerForRef(ref, candidates, blockUnresolvedPlanDocuments); blocked {
				blockers = append(blockers, blocker)
			}

			continue
		}

		duplicate := false

		for _, existing := range docs {
			if existing.Path == doc.Path {
				duplicate = true
				break
			}
		}

		if duplicate {
			continue
		}

		docs = append(docs, doc)
	}

	return docs, blockers
}

func implementationDispatchRefIsExplicitEditPath(ref string, editPaths []string) bool {
	ref, ok := pathparse.NormalizeWorkspacePathToken(ref)
	if !ok {
		return false
	}

	for _, path := range editPaths {
		path, ok := pathparse.NormalizeWorkspacePathToken(path)
		if ok && path == ref {
			return true
		}
	}

	return false
}

func implementationDispatchPlanDocumentRefs(prompt string, plan promptExecutionPlan) []string {
	refs := appendUniqueBriefStrings(nil, plan.runtimeBrief.TargetRefs...)
	return appendUniqueBriefStrings(refs, pathparse.MentionedWorkspacePaths(prompt)...)
}

func resolveImplementationDispatchPlanDocument(root string, ref string) (implementationDispatchPlanDocument, []string, bool) {
	normalized, ok := pathparse.NormalizeWorkspacePathToken(ref)
	if !ok || !implementationDispatchLooksLikePlanDocument(normalized) {
		return implementationDispatchPlanDocument{}, nil, false
	}

	if doc, ok := readImplementationDispatchPlanDocument(root, normalized); ok {
		return doc, []string{doc.Path}, true
	}

	if !implementationDispatchCanResolvePlanDocumentBasename(normalized) {
		return implementationDispatchPlanDocument{}, nil, false
	}

	candidates := implementationDispatchPlanDocumentBasenameMatches(root, filepath.Base(normalized))
	if len(candidates) != 1 {
		return implementationDispatchPlanDocument{}, candidates, false
	}

	doc, ok := readImplementationDispatchPlanDocument(root, candidates[0])

	return doc, candidates, ok
}

func implementationDispatchCanResolvePlanDocumentBasename(ref string) bool {
	ref = strings.TrimSpace(filepath.ToSlash(filepath.Clean(ref)))
	if ref == "" || ref == "." || strings.Contains(ref, "/") {
		return false
	}

	return implementationDispatchLooksLikePlanDocument(ref)
}

func implementationDispatchPlanDocumentBasenameMatches(root string, basename string) []string {
	root = strings.TrimSpace(root)
	basename = strings.TrimSpace(basename)

	if root == "" || basename == "" || !implementationDispatchLooksLikePlanDocument(basename) {
		return nil
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}

	var matches []string

	_ = filepath.WalkDir(rootAbs, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		rel, relErr := filepath.Rel(rootAbs, path)
		if relErr != nil {
			return nil
		}

		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel != "." && implementationDispatchSkipPlanDocumentSearchDir(rel, entry.Name()) {
				return filepath.SkipDir
			}

			return nil
		}

		if entry.Name() != basename || !entry.Type().IsRegular() {
			return nil
		}

		if implementationDispatchIsRefineScratchpadPath(rel) {
			return nil
		}

		if _, ok := readImplementationDispatchPlanDocument(root, rel); !ok {
			return nil
		}

		matches = append(matches, rel)

		return nil
	})

	sort.Strings(matches)

	return matches
}

func implementationDispatchSkipPlanDocumentSearchDir(rel string, name string) bool {
	switch strings.TrimSpace(name) {
	case ".git", ".idiom", "node_modules", "vendor", "dist", "build", ".cache", ".direnv":
		return true
	default:
		return false
	}
}

func implementationDispatchPromptRequiresResolvedPlanDocument(prompt string, plan promptExecutionPlan, refs []string) bool {
	if !isImplementationLikeFamily(plan.route.Family) {
		return false
	}

	hasPlanDocumentRef := false

	for _, ref := range refs {
		normalized, ok := pathparse.NormalizeWorkspacePathToken(ref)
		if ok && implementationDispatchLooksLikePlanDocument(normalized) {
			hasPlanDocumentRef = true
			break
		}
	}

	if !hasPlanDocumentRef {
		return false
	}

	if strings.TrimSpace(plan.route.Reason) == "typed_plan_execute_command" {
		return true
	}

	words := plannerWordText(prompt)
	for _, phrase := range []string{
		" start working on ",
		" start woring on ",
		" start implementing ",
		" begin working on ",
		" begin implementing ",
		" implement ",
		" implementing ",
		" implement from ",
		" implement the plan ",
		" implement this plan ",
		" work from ",
		" design doc ",
		" design document ",
		" plan doc ",
		" plan document ",
		" implementation handoff ",
	} {
		if strings.Contains(words, phrase) {
			return true
		}
	}

	return false
}

func implementationDispatchPlanDocumentResolutionBlockerForRef(ref string, candidates []string, blockUnresolved bool) (implementationDispatchPlanDocumentResolutionBlocker, bool) {
	normalized, ok := pathparse.NormalizeWorkspacePathToken(ref)
	if !ok || !implementationDispatchLooksLikePlanDocument(normalized) || !blockUnresolved {
		return implementationDispatchPlanDocumentResolutionBlocker{}, false
	}

	if len(candidates) > 1 {
		return implementationDispatchPlanDocumentResolutionBlocker{
			Ref:        normalized,
			Reason:     "ambiguous",
			Candidates: candidates,
		}, true
	}

	return implementationDispatchPlanDocumentResolutionBlocker{
		Ref:    normalized,
		Reason: "missing",
	}, true
}

func readImplementationDispatchPlanDocument(root string, ref string) (implementationDispatchPlanDocument, bool) {
	ref, ok := pathparse.NormalizeWorkspacePathToken(ref)
	if !ok || !implementationDispatchLooksLikePlanDocument(ref) {
		return implementationDispatchPlanDocument{}, false
	}

	if implementationDispatchIsRefineScratchpadPath(ref) {
		return implementationDispatchPlanDocument{}, false
	}

	abs, ok := implementationDispatchWorkspaceFile(root, ref)
	if !ok {
		return implementationDispatchPlanDocument{}, false
	}

	info, err := os.Stat(abs)
	if err != nil || info.IsDir() || info.Size() > 512*1024 {
		return implementationDispatchPlanDocument{}, false
	}

	body, err := os.ReadFile(abs)
	if err != nil {
		return implementationDispatchPlanDocument{}, false
	}

	content := string(body)
	obligationContent := content

	if implementationDispatchIsAtomizedPlanDocumentPath(ref) {
		obligationContent = implementationDispatchTextBeforeHeading(content, "source design document")
	}

	editPaths := implementationDispatchPlanDocumentPathsFromText(content, ref)

	return implementationDispatchPlanDocument{
		Path:               ref,
		Content:            content,
		EditPaths:          editPaths,
		AuthorizedNewFiles: featureplanner.PlanExecuteAuthorizedNewFilesFromImplementorPrompt(content),
		TestObligations:    implementationDispatchDesignDocTestObligations(obligationContent),
		ReferenceOnly:      len(editPaths) == 0 && !implementationDispatchIsAtomizedPlanDocumentPath(ref),
	}, true
}

func implementationDispatchPlanDocumentPathsFromText(text string, sourceRef string) []string {
	found := pathparse.MentionedWorkspacePathsUnderHeadings(text, implementationDispatchPlanDocumentScopeHeadings()...)

	var paths []string

	for _, candidate := range found {
		path, ok := pathparse.NormalizeWorkspacePathToken(candidate)
		if !ok || path == sourceRef || !implementationDispatchLooksLikeConcreteFile(path) {
			continue
		}

		paths = appendUniqueBriefStrings(paths, path)
	}

	return paths
}

func implementationDispatchPlanDocumentScopeHeadings() []string {
	return []string{
		"expected file scope",
		"implementation file scope",
		"file scope",
		"files to modify",
		"files to edit",
		"write scope",
		"edit scope",
	}
}

func implementationDispatchPlanDocumentAtoms(doc implementationDispatchPlanDocument) []implementationDispatchPlanDocumentAtom {
	if implementationDispatchIsAtomizedPlanDocumentPath(doc.Path) {
		return nil
	}

	atoms := implementationDispatchPlanDocumentAtomsFromText(doc.Content, doc.Path)
	if len(atoms) > 0 {
		return implementationDispatchAttachDesignDocTestObligations(atoms, doc.TestObligations, doc.Path)
	}

	for _, path := range doc.EditPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		atoms = append(atoms, implementationDispatchPlanDocumentAtom{
			Path:    path,
			Change:  "Implement the source design document slice for this path.",
			Excerpt: path,
		})
	}

	return implementationDispatchAttachDesignDocTestObligations(atoms, doc.TestObligations, doc.Path)
}

func implementationDispatchPlanDocumentAtomsFromText(text string, sourceRef string) []implementationDispatchPlanDocumentAtom {
	headingSet := map[string]struct{}{}

	for _, heading := range implementationDispatchPlanDocumentScopeHeadings() {
		normalized := normalizeImplementationDispatchMarkdownHeading(heading)
		if normalized != "" {
			headingSet[normalized] = struct{}{}
		}
	}

	var atoms []implementationDispatchPlanDocumentAtom

	active := false
	sawScopeLine := false

	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		normalized := normalizeImplementationDispatchMarkdownHeading(line)

		if _, ok := headingSet[normalized]; ok {
			active = true
			sawScopeLine = false

			continue
		}

		if !active {
			continue
		}

		if line == "" {
			if sawScopeLine {
				break
			}

			continue
		}

		if sawScopeLine && implementationDispatchLooksLikeMarkdownHeading(line) {
			break
		}

		if cells, ok := implementationDispatchMarkdownTableCells(line); ok {
			if len(cells) == 0 || implementationDispatchMarkdownTableSeparatorCell(cells[0]) {
				continue
			}

			paths := pathparse.MentionedWorkspacePathsInMarkdownTableFirstCell(line)
			if len(paths) == 0 {
				continue
			}

			change := ""
			if len(cells) > 1 {
				change = strings.TrimSpace(cells[1])
			}

			sawScopeLine = true

			for _, path := range paths {
				path, ok := pathparse.NormalizeWorkspacePathToken(path)
				if !ok || path == sourceRef || !implementationDispatchLooksLikeConcreteFile(path) {
					continue
				}

				atoms = append(atoms, implementationDispatchPlanDocumentAtom{
					Path:    path,
					Change:  change,
					Excerpt: strings.TrimSpace(line),
				})
			}

			continue
		}

		paths := pathparse.MentionedWorkspacePaths(line)
		if len(paths) == 0 {
			continue
		}

		sawScopeLine = true

		for _, path := range paths {
			path, ok := pathparse.NormalizeWorkspacePathToken(path)
			if !ok || path == sourceRef || !implementationDispatchLooksLikeConcreteFile(path) {
				continue
			}

			atoms = append(atoms, implementationDispatchPlanDocumentAtom{
				Path:    path,
				Change:  strings.TrimSpace(line),
				Excerpt: strings.TrimSpace(line),
			})
		}
	}

	return atoms
}

func implementationDispatchDesignDocTestObligations(text string) []string {
	var obligations []string

	obligations = append(obligations, implementationDispatchMinimumTestObligations(text)...)

	for _, heading := range []string{"design doc test obligations", "verification", "acceptance", "acceptance criteria"} {
		obligations = append(obligations, implementationDispatchTestObligationsFromLines(implementationDispatchMarkdownSectionLines(text, heading))...)
	}

	return appendUniqueBriefStrings(nil, obligations...)
}

func implementationDispatchMinimumTestObligations(text string) []string {
	var obligations []string

	active := false
	sawItem := false

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), ">"))
		normalized := normalizeImplementationDispatchMarkdownHeading(line)

		if strings.Contains(normalized, "minimum tests") {
			active = true
			sawItem = false

			continue
		}

		if !active {
			continue
		}

		if line == "" {
			continue
		}

		if sawItem && (implementationDispatchLooksLikeMarkdownHeading(line) || implementationDispatchLooksLikeNumberedStep(line)) {
			break
		}

		obligation := implementationDispatchCleanObligationLine(line)
		if obligation == "" {
			continue
		}

		if implementationDispatchLineLooksLikeTestObligation(obligation) {
			obligations = append(obligations, obligation)
			sawItem = true
		}
	}

	return obligations
}

func implementationDispatchMarkdownSectionLines(text string, heading string) []string {
	want := normalizeImplementationDispatchMarkdownHeading(heading)

	var lines []string

	active := false

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), ">"))
		normalized := normalizeImplementationDispatchMarkdownHeading(line)

		if active && implementationDispatchLooksLikeMarkdownHeading(line) {
			break
		}

		if normalized == want {
			active = true
			continue
		}

		if active {
			lines = append(lines, raw)
		}
	}

	return lines
}

func implementationDispatchTextBeforeHeading(text string, heading string) string {
	want := normalizeImplementationDispatchMarkdownHeading(heading)

	var kept []string

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), ">"))
		if normalizeImplementationDispatchMarkdownHeading(line) == want {
			break
		}

		kept = append(kept, raw)
	}

	return strings.Join(kept, "\n")
}

func implementationDispatchTestObligationsFromLines(lines []string) []string {
	var obligations []string

	inFence := false

	for _, raw := range lines {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), ">"))
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inFence = !inFence
			continue
		}

		isListItem := strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") || implementationDispatchLooksLikeNumberedStep(line)
		obligation := implementationDispatchCleanObligationLine(line)

		if obligation == "" {
			continue
		}

		if inFence {
			if implementationDispatchLineLooksLikeVerificationCommand(obligation) {
				obligations = append(obligations, obligation)
			}

			continue
		}

		if !isListItem && !implementationDispatchLineLooksLikeVerificationCommand(obligation) {
			continue
		}

		if implementationDispatchLineLooksLikeTestObligation(obligation) {
			obligations = append(obligations, obligation)
		}
	}

	return obligations
}

func implementationDispatchCleanObligationLine(line string) string {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), ">"))
	line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
	line = strings.TrimSpace(strings.TrimPrefix(line, "*"))

	for i, r := range line {
		if i == 0 && r >= '0' && r <= '9' {
			continue
		}

		if i > 0 && (r == '.' || r == ')') {
			prefix := line[:i]
			if strings.Trim(prefix, "0123456789") == "" {
				return strings.TrimSpace(line[i+1:])
			}
		}

		break
	}

	return strings.TrimSpace(line)
}

func implementationDispatchLineLooksLikeTestObligation(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}

	if strings.Contains(lower, "design doc test obligations") && strings.Contains(lower, "represented in source") {
		return false
	}

	if implementationDispatchLineLooksLikeVerificationCommand(line) {
		return true
	}

	return strings.Contains(lower, "test") ||
		strings.Contains(lower, "_test.") ||
		strings.Contains(lower, "coverage") ||
		strings.Contains(lower, "verification")
}

func implementationDispatchLineLooksLikeVerificationCommand(line string) bool {
	lower := strings.ToLower(strings.Trim(strings.TrimSpace(line), "`"))
	for _, prefix := range []string{
		"go test",
		"go build",
		"make test",
		"make build",
		"git diff --check",
		"pytest",
		"npm test",
		"pnpm test",
		"yarn test",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	return false
}

func implementationDispatchLooksLikeNumberedStep(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}

	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}

	return i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' '
}

func implementationDispatchAttachDesignDocTestObligations(atoms []implementationDispatchPlanDocumentAtom, obligations []string, sourceRef string) []implementationDispatchPlanDocumentAtom {
	if len(atoms) == 0 || len(obligations) == 0 {
		return atoms
	}

	for _, obligation := range obligations {
		obligation = strings.TrimSpace(obligation)
		if obligation == "" {
			continue
		}

		assigned := false

		for _, path := range implementationDispatchObligationConcretePaths(obligation, sourceRef) {
			matched := false

			for i := range atoms {
				if atoms[i].Path == path {
					atoms[i].TestObligations = appendUniqueBriefStrings(atoms[i].TestObligations, obligation)
					matched = true
					assigned = true
				}
			}

			if !matched {
				atoms = append(atoms, implementationDispatchPlanDocumentAtom{
					Path:            path,
					Change:          "Implement the source design document test obligation.",
					Excerpt:         obligation,
					TestObligations: []string{obligation},
				})
				assigned = true
			}
		}

		if assigned {
			continue
		}

		for i := range atoms {
			if implementationDispatchAtomMatchesTestObligation(atoms[i], obligation) {
				atoms[i].TestObligations = appendUniqueBriefStrings(atoms[i].TestObligations, obligation)
				assigned = true
			}
		}

		if !assigned {
			for i := range atoms {
				if implementationDispatchAtomPathLooksTest(atoms[i].Path) {
					atoms[i].TestObligations = appendUniqueBriefStrings(atoms[i].TestObligations, obligation)
				}
			}
		}
	}

	return atoms
}

func implementationDispatchObligationConcretePaths(obligation string, sourceRef string) []string {
	var paths []string

	for _, candidate := range pathparse.MentionedWorkspacePaths(obligation) {
		path, ok := pathparse.NormalizeWorkspacePathToken(candidate)
		if !ok || path == sourceRef || !implementationDispatchLooksLikeConcreteFile(path) {
			continue
		}

		paths = appendUniqueBriefStrings(paths, path)
	}

	return paths
}

func implementationDispatchAtomMatchesTestObligation(atom implementationDispatchPlanDocumentAtom, obligation string) bool {
	path := strings.ToLower(strings.TrimSpace(atom.Path))
	if path == "" {
		return false
	}

	lower := strings.ToLower(obligation)
	dir := strings.Trim(filepath.ToSlash(filepath.Dir(path)), ".")

	if dir != "" && strings.Contains(lower, "./"+dir) {
		return true
	}

	if strings.Contains(lower, path) {
		return true
	}

	return implementationDispatchAtomPathLooksTest(path) && implementationDispatchLineLooksLikeTestObligation(obligation)
}

func implementationDispatchAtomPathLooksTest(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))

	return strings.Contains(lower, "_test.") ||
		strings.Contains(lower, "/test/") ||
		strings.Contains(lower, "/tests/") ||
		strings.HasSuffix(lower, "_test.go")
}

func implementationDispatchMarkdownTableCells(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") || strings.Count(trimmed, "|") < 2 {
		return nil, false
	}

	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")

	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))

	for _, part := range parts {
		cells = append(cells, strings.TrimSpace(part))
	}

	return cells, len(cells) > 0
}

func implementationDispatchMarkdownTableSeparatorCell(cell string) bool {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return false
	}

	for _, r := range cell {
		if r != '-' && r != ':' && r != ' ' {
			return false
		}
	}

	return strings.Contains(cell, "-")
}

func normalizeImplementationDispatchMarkdownHeading(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "#*: `\"'")
	value = strings.TrimSuffix(value, ":")
	value = strings.ToLower(value)
	value = strings.NewReplacer("-", " ", "_", " ", "/", " ").Replace(value)

	return strings.Join(strings.Fields(value), " ")
}

func implementationDispatchLooksLikeMarkdownHeading(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}

	return strings.HasPrefix(trimmed, "#") ||
		(strings.HasPrefix(trimmed, "**") && strings.Contains(trimmed, "**")) ||
		strings.HasSuffix(trimmed, ":")
}

func implementationDispatchCanAtomizePlanDocuments(planDocuments []implementationDispatchPlanDocument) bool {
	for _, doc := range planDocuments {
		if implementationDispatchIsAtomizedPlanDocumentPath(doc.Path) {
			continue
		}

		if len(implementationDispatchPlanDocumentAtoms(doc)) > 0 {
			return true
		}
	}

	return false
}

func implementationDispatchPlanDocumentsShouldPreAtomize(planDocuments []implementationDispatchPlanDocument) bool {
	for _, doc := range planDocuments {
		if implementationDispatchIsAtomizedPlanDocumentPath(doc.Path) {
			continue
		}

		atoms := implementationDispatchPlanDocumentAtoms(doc)
		if len(atoms) < 8 {
			continue
		}

		distinctFiles := map[string]struct{}{}

		for _, atom := range atoms {
			path := strings.TrimSpace(atom.Path)
			if path != "" {
				distinctFiles[path] = struct{}{}
			}
		}

		if len(distinctFiles) >= 6 {
			return true
		}
	}

	return false
}

func implementationDispatchPreflightNeedsAtomization(results []implementationDispatchPlanDocumentPreflightResult) bool {
	for _, result := range results {
		diagnostics := strings.ToLower(strings.Join(append(append([]string{}, result.Blockers...), result.LintBlockers...), "\n"))
		if strings.Contains(diagnostics, "single-purpose decomposition failed") ||
			(strings.Contains(diagnostics, "decompose into") && strings.Contains(diagnostics, "sub-atoms")) {

			return true
		}
	}

	return false
}

type implementationDispatchSourceSatisfiedRecord struct {
	DocumentPath    string
	EditPath        string
	Evidence        []string
	TestObligations []string
	TestEvidence    []string
}

type implementationDispatchTerminalAtomRecord struct {
	DocumentPath string
	Status       string
}

func implementationDispatchPlanDocumentsSourceSatisfied(cfg config.Config, planDocuments []implementationDispatchPlanDocument) []implementationDispatchSourceSatisfiedRecord {
	root := implementationDispatchWorkspaceRoot(cfg)
	if strings.TrimSpace(root) == "" {
		return nil
	}

	var records []implementationDispatchSourceSatisfiedRecord

	for _, doc := range planDocuments {
		record, ok := implementationDispatchPlanDocumentSourceSatisfied(root, doc)
		if !ok {
			return nil
		}

		records = append(records, record)
	}

	return records
}

func implementationDispatchPlanDocumentsTerminalAtoms(planDocuments []implementationDispatchPlanDocument) []implementationDispatchTerminalAtomRecord {
	var records []implementationDispatchTerminalAtomRecord

	for _, doc := range planDocuments {
		status, ok := implementationDispatchPlanDocumentTerminalAtomStatus(doc)
		if !ok {
			return nil
		}

		records = append(records, implementationDispatchTerminalAtomRecord{
			DocumentPath: doc.Path,
			Status:       status,
		})
	}

	return records
}

func implementationDispatchPlanDocumentTerminalAtomStatus(doc implementationDispatchPlanDocument) (string, bool) {
	if !implementationDispatchIsAtomizedPlanDocumentPath(doc.Path) {
		return "", false
	}

	status, ok := implementationDispatchAtomDocumentStatus(doc.Content)
	if !ok {
		return "", false
	}

	if implementationDispatchAtomStatusIsTerminal(status) {
		return status, true
	}

	return "", false
}

func implementationDispatchAtomDocumentStatus(content string) (string, bool) {
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		line = strings.Trim(line, "* ")

		parts := strings.SplitN(line, ":", 2)

		if len(parts) != 2 || !util.EqualTrimFold(parts[0], "status") {
			continue
		}

		status := strings.Trim(strings.TrimSpace(parts[1]), "* ")

		return status, strings.TrimSpace(status) != ""
	}

	return "", false
}

func implementationDispatchAtomStatusIsTerminal(status string) bool {
	lower := strings.ToLower(strings.TrimSpace(status))
	if lower == "" {
		return false
	}

	if strings.Contains(lower, "do not dispatch") ||
		strings.Contains(lower, "source-satisfied") ||
		strings.Contains(lower, "source satisfied") {

		return true
	}

	switch implementationDispatchFirstStatusToken(lower) {
	case "accepted", "complete", "completed", "closed":
		return true
	default:
		return false
	}
}

func implementationDispatchFirstStatusToken(status string) string {
	for _, token := range strings.FieldsFunc(status, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '-' || r == '_' || r == ':' || r == ';' || r == ','
	}) {
		token = strings.TrimSpace(token)
		if token != "" {
			return token
		}
	}

	return ""
}

func implementationDispatchPlanDocumentSourceSatisfied(root string, doc implementationDispatchPlanDocument) (implementationDispatchSourceSatisfiedRecord, bool) {
	if !implementationDispatchIsAtomizedPlanDocumentPath(doc.Path) {
		return implementationDispatchSourceSatisfiedRecord{}, false
	}

	atoms := implementationDispatchPlanDocumentAtomsFromText(doc.Content, doc.Path)
	if len(atoms) != 1 {
		return implementationDispatchSourceSatisfiedRecord{}, false
	}

	atom := atoms[0]

	atom.TestObligations = appendUniqueBriefStrings(atom.TestObligations, doc.TestObligations...)

	path := strings.TrimSpace(atom.Path)

	if path == "" {
		return implementationDispatchSourceSatisfiedRecord{}, false
	}

	abs, ok := implementationDispatchWorkspaceFile(root, path)
	if !ok {
		return implementationDispatchSourceSatisfiedRecord{}, false
	}

	body, err := os.ReadFile(abs)
	if err != nil {
		return implementationDispatchSourceSatisfiedRecord{}, false
	}

	content := string(body)
	scopeText := implementationDispatchSourceScopeRowText(doc.Content)

	if strings.TrimSpace(scopeText) == "" {
		scopeText = strings.Join([]string{atom.Change, atom.Excerpt}, " ")
	}

	if evidence := implementationDispatchSourceSatisfiedLiteralEvidence(path, content, scopeText, doc.Path); len(evidence) > 0 {
		testEvidence, ok := implementationDispatchSourceSatisfiedTestEvidence(root, doc, atom)
		if !ok {
			return implementationDispatchSourceSatisfiedRecord{}, false
		}

		return implementationDispatchSourceSatisfiedRecord{
			DocumentPath:    doc.Path,
			EditPath:        path,
			Evidence:        evidence,
			TestObligations: atom.TestObligations,
			TestEvidence:    testEvidence,
		}, true
	}

	if evidence := implementationDispatchSourceSatisfiedAnchorEvidence(path, content, scopeText, doc.Content); len(evidence) > 0 {
		testEvidence, ok := implementationDispatchSourceSatisfiedTestEvidence(root, doc, atom)
		if !ok {
			return implementationDispatchSourceSatisfiedRecord{}, false
		}

		return implementationDispatchSourceSatisfiedRecord{
			DocumentPath:    doc.Path,
			EditPath:        path,
			Evidence:        evidence,
			TestObligations: atom.TestObligations,
			TestEvidence:    testEvidence,
		}, true
	}

	if !implementationDispatchScopeRowIsOptional(scopeText) {
		evidence := implementationDispatchSourceSatisfiedSourceBodyEvidence(path, content, scopeText)
		if len(evidence) == 0 {
			return implementationDispatchSourceSatisfiedRecord{}, false
		}

		testEvidence, ok := implementationDispatchSourceSatisfiedTestEvidence(root, doc, atom)
		if !ok {
			return implementationDispatchSourceSatisfiedRecord{}, false
		}

		return implementationDispatchSourceSatisfiedRecord{
			DocumentPath:    doc.Path,
			EditPath:        path,
			Evidence:        evidence,
			TestObligations: atom.TestObligations,
			TestEvidence:    testEvidence,
		}, true
	}

	return implementationDispatchSourceSatisfiedRecord{}, false
}

func implementationDispatchSourceScopeRowText(content string) string {
	lines := strings.Split(content, "\n")
	for i, raw := range lines {
		if normalizeImplementationDispatchMarkdownHeading(raw) != "source scope row" {
			continue
		}

		var out []string

		for _, line := range lines[i+1:] {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				if len(out) > 0 {
					break
				}

				continue
			}

			if implementationDispatchLooksLikeMarkdownHeading(trimmed) {
				break
			}

			out = append(out, strings.TrimSpace(strings.TrimPrefix(trimmed, ">")))
		}

		return strings.Join(out, "\n")
	}

	return ""
}

func implementationDispatchSourceSatisfiedLiteralEvidence(path string, content string, sourceText string, skipPaths ...string) []string {
	literals := implementationDispatchBacktickLiterals(sourceText)
	for _, literal := range literals {
		literal = strings.TrimSpace(literal)
		if literal == "" || implementationDispatchLiteralIsSkippedPath(literal, skipPaths...) {
			continue
		}

		if evidence, ok := implementationDispatchLiteralSatisfiedEvidence(path, content, literal); ok {
			return []string{evidence}
		}
	}

	return nil
}

func implementationDispatchBacktickLiterals(text string) []string {
	var literals []string

	for {
		start := strings.Index(text, "`")
		if start < 0 {
			break
		}

		text = text[start+1:]

		end := strings.Index(text, "`")

		if end < 0 {
			break
		}

		literals = appendUniqueBriefStrings(literals, strings.TrimSpace(text[:end]))
		text = text[end+1:]
	}

	return literals
}

func implementationDispatchLiteralIsSkippedPath(literal string, skipPaths ...string) bool {
	normalizedLiteral, ok := pathparse.NormalizeWorkspacePathToken(literal)
	if !ok {
		return false
	}

	for _, skip := range skipPaths {
		normalizedSkip, ok := pathparse.NormalizeWorkspacePathToken(skip)
		if ok && normalizedLiteral == normalizedSkip {
			return true
		}
	}

	return implementationDispatchLooksLikeConcreteFile(normalizedLiteral)
}

func implementationDispatchLiteralSatisfiedEvidence(path string, content string, literal string) (string, bool) {
	literal = strings.TrimSpace(literal)
	if literal == "" {
		return "", false
	}

	lowerContent := strings.ToLower(content)
	lowerLiteral := strings.ToLower(literal)

	if strings.Contains(lowerContent, lowerLiteral) {
		return implementationDispatchLineEvidence(path, content, literal, fmt.Sprintf("current source contains `%s`", literal)), true
	}

	if strings.HasPrefix(lowerLiteral, "idiom.") && strings.Count(lowerLiteral, ".") >= 2 {
		parts := strings.Split(lowerLiteral, ".")
		prefix := strings.Join(parts[:len(parts)-1], ".") + "."
		leaf := parts[len(parts)-1]

		if leaf != "" && strings.Contains(lowerContent, leaf) && strings.Contains(lowerContent, prefix) {
			return implementationDispatchLineEvidence(path, content, leaf, fmt.Sprintf("current source contains event leaf `%s` and namespace prefix `%s`", leaf, prefix)), true
		}
	}

	return "", false
}

func implementationDispatchLineEvidence(path string, content string, needle string, description string) string {
	needle = strings.ToLower(strings.TrimSpace(needle))
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(strings.ToLower(line), needle) {
			return fmt.Sprintf("`%s:%d`: %s (`%s`)", path, i+1, description, strings.ReplaceAll(strings.TrimSpace(line), "`", "'"))
		}
	}

	return fmt.Sprintf("`%s`: %s", path, description)
}

type implementationDispatchCurrentStateAnchor struct {
	Path string
	Line int
	Text string
}

func implementationDispatchSourceSatisfiedAnchorEvidence(path string, content string, scopeText string, docContent string) []string {
	anchors := implementationDispatchCurrentStateAnchorsForPath(docContent, path, content)
	if len(anchors) == 0 {
		return nil
	}

	if implementationDispatchScopeRowIsOptional(scopeText) {
		return []string{fmt.Sprintf("`%s`: source scope is optional (`If needed`) and current-state anchors are present; no required edit shape remains", path)}
	}

	sourceTerms := implementationDispatchSatisfactionTermSet(implementationDispatchStripWorkspacePathLiterals(scopeText))
	if len(sourceTerms) == 0 {
		return nil
	}

	covered := map[string]struct{}{}

	var evidence []string

	for _, anchor := range anchors {
		anchorTerms := implementationDispatchSatisfactionTermSet(anchor.Text)
		matched := false

		for term := range sourceTerms {
			if _, ok := anchorTerms[term]; ok {
				covered[term] = struct{}{}
				matched = true
			}
		}

		if matched {
			evidence = append(evidence, fmt.Sprintf("`%s:%d`: current-state anchor covers requested source-scope term(s) (`%s`)", anchor.Path, anchor.Line, strings.ReplaceAll(anchor.Text, "`", "'")))
		}
	}

	if !implementationDispatchAnchorCoverageSatisfies(sourceTerms, covered) {
		return nil
	}

	if len(evidence) > 3 {
		evidence = evidence[:3]
	}

	return evidence
}

func implementationDispatchSourceSatisfiedSourceBodyEvidence(path string, content string, scopeText string) []string {
	sourceTerms := implementationDispatchSatisfactionTermSet(implementationDispatchStripWorkspacePathLiterals(scopeText))
	if len(sourceTerms) == 0 {
		return nil
	}

	covered := map[string]struct{}{}

	var evidence []string

	for i, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		lineTerms := implementationDispatchSatisfactionTermSet(trimmed)
		matched := false

		for term := range sourceTerms {
			if _, ok := lineTerms[term]; ok {
				covered[term] = struct{}{}
				matched = true
			}
		}

		if matched {
			evidence = append(evidence, fmt.Sprintf("`%s:%d`: current source covers requested source-scope term(s) (`%s`)", path, i+1, strings.ReplaceAll(trimmed, "`", "'")))
		}
	}

	if !implementationDispatchAnchorCoverageSatisfies(sourceTerms, covered) {
		return nil
	}

	if len(evidence) > 3 {
		evidence = evidence[:3]
	}

	return evidence
}

func implementationDispatchCurrentStateAnchorsForPath(docContent string, path string, currentContent string) []implementationDispatchCurrentStateAnchor {
	var anchors []implementationDispatchCurrentStateAnchor

	for _, line := range strings.Split(docContent, "\n") {
		literals := implementationDispatchBacktickLiterals(line)
		if len(literals) < 2 {
			continue
		}

		anchorPath, anchorLine, ok := implementationDispatchParseCurrentStateAnchorRef(literals[0])
		if !ok || anchorPath != path {
			continue
		}

		text := strings.TrimSpace(literals[1])
		if text == "" || !implementationDispatchAnchorTextStillCurrent(currentContent, anchorLine, text) {
			continue
		}

		anchors = append(anchors, implementationDispatchCurrentStateAnchor{
			Path: anchorPath,
			Line: anchorLine,
			Text: text,
		})
	}

	return anchors
}

func implementationDispatchStripWorkspacePathLiterals(text string) string {
	for _, literal := range implementationDispatchBacktickLiterals(text) {
		normalized, ok := pathparse.NormalizeWorkspacePathToken(literal)
		if !ok || !implementationDispatchLooksLikeConcreteFile(normalized) {
			continue
		}

		text = strings.ReplaceAll(text, "`"+literal+"`", "")
	}

	return text
}

func implementationDispatchParseCurrentStateAnchorRef(ref string) (string, int, bool) {
	ref = strings.TrimSpace(ref)

	idx := strings.LastIndex(ref, ":")

	if idx <= 0 || idx == len(ref)-1 {
		return "", 0, false
	}

	path := strings.TrimSpace(ref[:idx])
	lineText := strings.TrimSpace(ref[idx+1:])
	line := 0

	for _, r := range lineText {
		if r < '0' || r > '9' {
			return "", 0, false
		}

		line = line*10 + int(r-'0')
	}

	if line <= 0 {
		return "", 0, false
	}

	normalized, ok := pathparse.NormalizeWorkspacePathToken(path)
	if !ok {
		return "", 0, false
	}

	return normalized, line, true
}

func implementationDispatchAnchorTextStillCurrent(content string, line int, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	lines := strings.Split(content, "\n")
	if line > 0 && line <= len(lines) && strings.TrimSpace(lines[line-1]) == text {
		return true
	}

	return strings.Contains(content, text)
}

func implementationDispatchScopeRowIsOptional(scopeText string) bool {
	lower := strings.ToLower(scopeText)
	return strings.Contains(lower, "if needed") || strings.Contains(lower, "if necessary")
}

func implementationDispatchAnchorCoverageSatisfies(sourceTerms map[string]struct{}, covered map[string]struct{}) bool {
	if len(covered) == 0 {
		return false
	}

	requiredCoverage := 3
	if len(sourceTerms) < requiredCoverage {
		requiredCoverage = len(sourceTerms)
	}

	if requiredCoverage < 2 {
		requiredCoverage = 2
	}

	return len(covered) >= requiredCoverage
}

func implementationDispatchSatisfactionTermSet(text string) map[string]struct{} {
	terms := map[string]struct{}{}

	for _, token := range implementationDispatchSatisfactionTokens(text) {
		if implementationDispatchIgnoredSatisfactionTerm(token) {
			continue
		}

		terms[token] = struct{}{}
	}

	return terms
}

func implementationDispatchSatisfactionTokens(text string) []string {
	var (
		tokens  []string
		current []rune
	)

	flush := func() {
		if len(current) == 0 {
			return
		}

		token := strings.ToLower(string(current))

		tokens = append(tokens, token)
		current = current[:0]
	}

	for _, r := range text {
		switch {
		case r >= 'A' && r <= 'Z':
			if len(current) > 0 {
				last := current[len(current)-1]
				if last >= 'a' && last <= 'z' {
					flush()
				}
			}

			current = append(current, r)
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			current = append(current, r)
		default:
			flush()
		}
	}

	flush()

	return tokens
}

func implementationDispatchIgnoredSatisfactionTerm(term string) bool {
	if len(term) < 3 && term != "id" {
		return true
	}

	if implementationDispatchIgnoredAnchorTerm(term) {
		return true
	}

	switch term {
	case "add", "already", "around", "after", "before", "build", "current", "existing", "needed", "needs", "new", "only", "preserving", "provided", "requested", "runs", "row", "state", "without":
		return true
	default:
		return false
	}
}

func implementationDispatchSourceSatisfiedTestEvidence(root string, doc implementationDispatchPlanDocument, atom implementationDispatchPlanDocumentAtom) ([]string, bool) {
	obligations := appendUniqueBriefStrings(append([]string(nil), atom.TestObligations...), doc.TestObligations...)
	if len(obligations) == 0 {
		return nil, true
	}

	var evidence []string

	for _, obligation := range obligations {
		obligation = strings.TrimSpace(obligation)
		if obligation == "" {
			continue
		}

		itemEvidence, ok := implementationDispatchTestObligationEvidence(root, atom.Path, obligation)
		if !ok {
			return nil, false
		}

		evidence = append(evidence, itemEvidence)
	}

	return appendUniqueBriefStrings(nil, evidence...), true
}

func implementationDispatchTestObligationEvidence(root string, atomPath string, obligation string) (string, bool) {
	if implementationDispatchLineLooksLikeVerificationCommand(obligation) {
		return "", false
	}

	paths := implementationDispatchObligationConcretePaths(obligation, "")
	if len(paths) == 0 {
		paths = []string{strings.TrimSpace(atomPath)}
	}

	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		abs, ok := implementationDispatchWorkspaceFile(root, path)
		if !ok {
			continue
		}

		body, err := os.ReadFile(abs)
		if err != nil {
			continue
		}

		content := string(body)
		testNames := implementationDispatchTestNamesFromText(obligation)

		for _, testName := range testNames {
			if evidence, ok := implementationDispatchLiteralSatisfiedEvidence(path, content, testName); ok {
				return strings.Replace(evidence, "current source contains", "current test source contains", 1), true
			}
		}

		if len(testNames) > 0 {
			continue
		}

		if evidence, ok := implementationDispatchTestObligationTermEvidence(path, content, obligation); ok {
			return evidence, true
		}
	}

	return "", false
}

func implementationDispatchTestNamesFromText(text string) []string {
	var names []string

	for _, token := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_')
	}) {
		if len(token) <= len("Test") || !strings.HasPrefix(token, "Test") {
			continue
		}

		next := token[len("Test")]
		if !((next >= 'A' && next <= 'Z') || (next >= '0' && next <= '9') || next == '_') {
			continue
		}

		names = appendUniqueBriefStrings(names, token)
	}

	return names
}

func implementationDispatchTestObligationTermEvidence(path string, content string, obligation string) (string, bool) {
	terms := implementationDispatchSatisfactionTermSet(implementationDispatchStripWorkspacePathLiterals(obligation))
	if len(terms) == 0 {
		return "", false
	}

	required := 2
	if len(terms) < required {
		required = len(terms)
	}

	for i, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		lineTerms := implementationDispatchSatisfactionTermSet(trimmed)
		matches := 0

		for term := range terms {
			if _, ok := lineTerms[term]; ok {
				matches++
			}
		}

		if matches >= required {
			return fmt.Sprintf("`%s:%d`: current test source covers design-doc test obligation (`%s`)", path, i+1, strings.ReplaceAll(trimmed, "`", "'")), true
		}
	}

	return "", false
}

func implementationDispatchPreflightAllSourceSatisfied(results []implementationDispatchPlanDocumentPreflightResult) bool {
	if len(results) == 0 {
		return false
	}

	for _, result := range results {
		if !implementationDispatchAtomStateIsSourceSatisfied(result.AtomState) {
			return false
		}
	}

	return true
}

func humanImplementationDispatchSourceSatisfiedMessage(records []implementationDispatchSourceSatisfiedRecord) string {
	var b strings.Builder

	b.WriteString("Decision: source-satisfied\n\n")
	b.WriteString("The referenced design-doc atom is already present in current source; plan-execute was not dispatched.")

	for _, record := range records {
		b.WriteString("\n- `")
		b.WriteString(record.EditPath)
		b.WriteString("`")

		if strings.TrimSpace(record.DocumentPath) != "" {
			b.WriteString(" from `")
			b.WriteString(record.DocumentPath)
			b.WriteString("`")
		}

		for _, evidence := range record.Evidence {
			evidence = strings.TrimSpace(evidence)
			if evidence == "" {
				continue
			}

			b.WriteString("\n  - ")
			b.WriteString(evidence)
		}

		if len(record.TestObligations) > 0 {
			b.WriteString("\n  - Test obligations:")

			for _, obligation := range record.TestObligations {
				obligation = strings.TrimSpace(obligation)
				if obligation == "" {
					continue
				}

				b.WriteString("\n    - ")
				b.WriteString(obligation)
			}
		}

		for _, evidence := range record.TestEvidence {
			evidence = strings.TrimSpace(evidence)
			if evidence == "" {
				continue
			}

			b.WriteString("\n  - ")
			b.WriteString(evidence)
		}
	}

	return strings.TrimSpace(b.String())
}

func humanImplementationDispatchTerminalAtomMessage(records []implementationDispatchTerminalAtomRecord) string {
	var b strings.Builder

	b.WriteString("Decision: source-satisfied\n\n")
	b.WriteString("The referenced design-doc atom is already closed in the atom tracker; plan-execute was not dispatched.")

	for _, record := range records {
		path := strings.TrimSpace(record.DocumentPath)
		if path == "" {
			path = "(unknown atom)"
		}

		status := strings.TrimSpace(record.Status)
		if status == "" {
			status = "closed"
		}

		b.WriteString("\n- `")
		b.WriteString(path)
		b.WriteString("`: ")
		b.WriteString(status)
	}

	return strings.TrimSpace(b.String())
}

func humanImplementationDispatchPreflightSourceSatisfiedMessage(results []implementationDispatchPlanDocumentPreflightResult) string {
	var b strings.Builder

	b.WriteString("Decision: source-satisfied\n\n")
	b.WriteString("Atom intake classified the referenced brief as already satisfied; plan-execute was not dispatched.")

	for _, result := range results {
		path := strings.TrimSpace(result.Path)
		if path == "" {
			path = "(unknown brief)"
		}

		b.WriteString("\n- `")
		b.WriteString(path)
		b.WriteString("`")

		for _, blocker := range result.Blockers {
			blocker = strings.TrimSpace(blocker)
			if blocker == "" {
				continue
			}

			b.WriteString("\n  - ")
			b.WriteString(blocker)
		}
	}

	return strings.TrimSpace(b.String())
}

func implementationDispatchMarkSourceSatisfiedAtomDocuments(cfg config.Config, records []implementationDispatchSourceSatisfiedRecord) {
	root := implementationDispatchWorkspaceRoot(cfg)
	if strings.TrimSpace(root) == "" {
		return
	}

	for _, record := range records {
		var evidence []string

		evidence = append(evidence, record.Evidence...)
		evidence = append(evidence, record.TestEvidence...)
		implementationDispatchCloseAtomDocument(root, record.DocumentPath, "source-satisfied - do not dispatch", "Source-satisfied closeout recorded by TUI design-doc dispatch.", evidence)
	}
}

func implementationDispatchMarkPreflightSourceSatisfiedAtomDocuments(cfg config.Config, results []implementationDispatchPlanDocumentPreflightResult) {
	root := implementationDispatchWorkspaceRoot(cfg)
	if strings.TrimSpace(root) == "" {
		return
	}

	for _, result := range results {
		evidence := append([]string(nil), result.Blockers...)

		evidence = append(evidence, result.LintBlockers...)
		implementationDispatchCloseAtomDocument(root, result.Path, "source-satisfied - do not dispatch", "Atom intake reported this atom as source-satisfied before plan-execute dispatch.", evidence)
	}
}

func implementationDispatchMarkCompletedAtomDocument(cfg config.Config, doc implementationDispatchPlanDocument, answer string) {
	root := implementationDispatchWorkspaceRoot(cfg)
	if strings.TrimSpace(root) == "" {
		return
	}

	if implementationDispatchAnswerLooksBlocked(answer) {
		return
	}

	if len(implementationDispatchUnprovenVerificationCommands(doc, answer)) > 0 {
		return
	}

	status := "completed"
	summary := "Completed by plan-execute design-doc atom dispatch."

	if strings.Contains(strings.ToLower(answer), "decision: source-satisfied") {
		status = "source-satisfied - do not dispatch"
		summary = "Plan-execute reported source-satisfied closeout for this design-doc atom."
	}

	implementationDispatchCloseAtomDocument(root, doc.Path, status, summary, implementationDispatchCloseoutEvidenceFromAnswer(answer))
}

func implementationDispatchCloseAtomDocument(root string, relPath string, status string, summary string, evidence []string) {
	relPath = strings.TrimSpace(relPath)
	if !implementationDispatchIsAtomizedPlanDocumentPath(relPath) {
		return
	}

	abs, ok := implementationDispatchWorkspaceFile(root, relPath)
	if !ok {
		return
	}

	body, err := os.ReadFile(abs)
	if err != nil {
		return
	}

	content := implementationDispatchSetAtomStatusLine(string(body), status)

	content = implementationDispatchSetAtomResultSection(content, status, summary, evidence)

	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return
	}
}

func implementationDispatchSetAtomStatusLine(content string, status string) string {
	statusLine := "Status: " + strings.TrimSpace(status)
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		trimmed := strings.Trim(strings.TrimSpace(line), "* ")
		parts := strings.SplitN(trimmed, ":", 2)

		if len(parts) == 2 && util.EqualTrimFold(parts[0], "status") {
			lines[i] = statusLine
			return strings.Join(lines, "\n")
		}
	}

	if len(lines) > 0 {
		return lines[0] + "\n\n" + statusLine + "\n" + strings.Join(lines[1:], "\n")
	}

	return statusLine + "\n"
}

func implementationDispatchSetAtomResultSection(content string, status string, summary string, evidence []string) string {
	sourceIdx := strings.Index(content, "\n## Source Design Document\n")
	prefix := content
	suffix := ""

	if sourceIdx >= 0 {
		prefix = content[:sourceIdx]
		suffix = content[sourceIdx:]
	}

	if resultIdx := strings.Index(prefix, "\n## Result\n"); resultIdx >= 0 {
		prefix = prefix[:resultIdx]
	}

	var b strings.Builder

	b.WriteString(strings.TrimRight(prefix, "\n"))
	b.WriteString("\n\n## Result\n\n")
	b.WriteString("Status: ")
	b.WriteString(strings.TrimSpace(status))
	b.WriteString("\n\n")

	if strings.TrimSpace(summary) != "" {
		b.WriteString(strings.TrimSpace(summary))
		b.WriteString("\n\n")
	}

	if len(evidence) > 0 {
		b.WriteString("Evidence:\n")

		for _, item := range evidence {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}

			b.WriteString("- ")
			b.WriteString(truncateImplementationDispatchText(item, 1000))
			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	b.WriteString(strings.TrimLeft(suffix, "\n"))

	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}

	return b.String()
}

func implementationDispatchCloseoutEvidenceFromAnswer(answer string) []string {
	var evidence []string

	for _, line := range strings.Split(answer, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if line == "" {
			continue
		}

		if strings.Contains(line, "`") || strings.Contains(strings.ToLower(line), "test obligation") || strings.Contains(strings.ToLower(line), "verification") {
			evidence = append(evidence, line)
		}

		if len(evidence) >= 12 {
			break
		}
	}

	return evidence
}

func implementationDispatchUnprovenVerificationCommands(doc implementationDispatchPlanDocument, answer string) []string {
	var pending []string

	for _, obligation := range doc.TestObligations {
		obligation = strings.TrimSpace(obligation)
		if obligation == "" || !implementationDispatchLineLooksLikeVerificationCommand(obligation) {
			continue
		}

		if implementationDispatchAnswerHasVerificationCommandEvidence(answer, obligation) {
			continue
		}

		pending = appendUniqueBriefStrings(pending, strings.Trim(strings.TrimSpace(obligation), "`"))
	}

	return pending
}

func implementationDispatchAnswerHasVerificationCommandEvidence(answer string, command string) bool {
	command = strings.ToLower(strings.Trim(strings.TrimSpace(command), "`"))
	if command == "" {
		return false
	}

	for _, raw := range strings.Split(answer, "\n") {
		line := strings.ToLower(strings.TrimSpace(raw))
		if !strings.Contains(line, command) {
			continue
		}

		for _, marker := range []string{"pass", "passed", "success", "succeeded", "exit 0", "ok "} {
			if strings.Contains(line, marker) {
				return true
			}
		}
	}

	return false
}

func humanImplementationDispatchPendingVerificationCommandMessage(doc implementationDispatchPlanDocument, commands []string) string {
	var b strings.Builder

	b.WriteString("<<QUESTION>>: Design-doc verification command obligations remain unproven, so this atom was not marked complete.")

	if strings.TrimSpace(doc.Path) != "" {
		b.WriteString("\n\nAtom: `")
		b.WriteString(strings.TrimSpace(doc.Path))
		b.WriteString("`")
	}

	b.WriteString("\n\nPending verification commands:")

	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}

		b.WriteString("\n- `")
		b.WriteString(command)
		b.WriteString("`")
	}

	return strings.TrimSpace(b.String())
}

func materializeImplementationDispatchPlanDocumentAtoms(cfg config.Config, planDocuments []implementationDispatchPlanDocument) ([]implementationDispatchPlanDocument, error) {
	root := implementationDispatchWorkspaceRoot(cfg)
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("workspace root is unavailable")
	}

	var atomDocuments []implementationDispatchPlanDocument

	for _, doc := range planDocuments {
		if implementationDispatchIsAtomizedPlanDocumentPath(doc.Path) {
			atomDocuments = append(atomDocuments, doc)
			continue
		}

		atoms := implementationDispatchPlanDocumentAtoms(doc)
		if len(atoms) == 0 {
			atomDocuments = append(atomDocuments, doc)
			continue
		}

		title := implementationDispatchRefineTitle(doc.Path)
		dirRel := filepath.ToSlash(filepath.Join(".idiom", "atoms", title))
		dirAbs, ok := implementationDispatchWorkspaceFile(root, dirRel)

		if !ok {
			return nil, fmt.Errorf("atom output path is outside the workspace: %s", dirRel)
		}

		if err := os.MkdirAll(dirAbs, 0o755); err != nil {
			return nil, err
		}

		for i, atom := range atoms {
			atom = implementationDispatchPlanDocumentAtomWithWorkspaceShape(root, atom)

			rel := filepath.ToSlash(filepath.Join(dirRel, fmt.Sprintf("atom-%03d.md", i+1)))
			abs, ok := implementationDispatchWorkspaceFile(root, rel)

			if !ok {
				return nil, fmt.Errorf("atom output path is outside the workspace: %s", rel)
			}

			if existing, ok := readImplementationDispatchPlanDocument(root, rel); ok &&
				implementationDispatchPlanDocumentMatchesAtomPath(existing, atom.Path) &&
				implementationDispatchPlanDocumentIsTerminalAtom(existing) {

				atomDocuments = append(atomDocuments, existing)
				continue
			}

			body := composeImplementationDispatchPlanDocumentAtom(doc, atom, i+1, len(atoms))
			if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
				return nil, err
			}

			atomDoc, ok := readImplementationDispatchPlanDocument(root, rel)
			if !ok {
				return nil, fmt.Errorf("materialized atom was not readable: %s", rel)
			}

			atomDoc.AuthorizedNewFiles = implementationDispatchAtomAuthorizedNewFiles(doc, atom.Path)
			atomDocuments = append(atomDocuments, atomDoc)
		}
	}

	return atomDocuments, nil
}

func implementationDispatchPlanDocumentMatchesAtomPath(doc implementationDispatchPlanDocument, atomPath string) bool {
	atomPath = strings.TrimSpace(atomPath)
	if atomPath == "" {
		return false
	}

	for _, path := range doc.EditPaths {
		if strings.TrimSpace(path) == atomPath {
			return true
		}
	}

	return false
}

func implementationDispatchPlanDocumentIsTerminalAtom(doc implementationDispatchPlanDocument) bool {
	_, ok := implementationDispatchPlanDocumentTerminalAtomStatus(doc)
	return ok
}

func implementationDispatchPlanDocumentAtomWithWorkspaceShape(root string, atom implementationDispatchPlanDocumentAtom) implementationDispatchPlanDocumentAtom {
	path := strings.TrimSpace(atom.Path)
	if path == "" || implementationDispatchAtomChangeLooksLikeNewFile(atom.Change) {
		return atom
	}

	abs, ok := implementationDispatchWorkspaceFile(root, path)
	if !ok {
		return atom
	}

	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		atom.Anchors = implementationDispatchPlanDocumentAtomCurrentStateAnchors(root, atom)
		return atom
	}

	change := strings.TrimSpace(atom.Change)
	if change == "" {
		change = "create this file from the source design document slice"
	}

	atom.Change = "NEW FILE: " + change

	return atom
}

func implementationDispatchPlanDocumentAtomCurrentStateAnchors(root string, atom implementationDispatchPlanDocumentAtom) []string {
	path := strings.TrimSpace(atom.Path)
	abs, ok := implementationDispatchWorkspaceFile(root, path)

	if !ok {
		return nil
	}

	body, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(body), "\n")
	terms := implementationDispatchPlanDocumentAtomAnchorTerms(atom)
	candidates := implementationDispatchPlanDocumentAtomAnchorCandidates(lines, terms)

	if len(candidates) < 3 {
		candidates = implementationDispatchAppendAtomAnchorFallbacks(candidates, implementationDispatchPlanDocumentAtomFallbackAnchorCandidates(lines), 3)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}

		return candidates[i].Line < candidates[j].Line
	})

	if len(candidates) > 3 {
		candidates = candidates[:3]
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Line < candidates[j].Line
	})

	anchors := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		anchors = append(anchors, fmt.Sprintf("- `%s:%d`: `%s`", path, candidate.Line, strings.ReplaceAll(candidate.Text, "`", "'")))
	}

	return anchors
}

type implementationDispatchPlanDocumentAtomAnchorCandidate struct {
	Line  int
	Text  string
	Score int
}

func implementationDispatchAppendAtomAnchorFallbacks(candidates []implementationDispatchPlanDocumentAtomAnchorCandidate, fallbacks []implementationDispatchPlanDocumentAtomAnchorCandidate, limit int) []implementationDispatchPlanDocumentAtomAnchorCandidate {
	seen := map[int]struct{}{}
	for _, candidate := range candidates {
		seen[candidate.Line] = struct{}{}
	}

	for _, fallback := range fallbacks {
		if len(candidates) >= limit {
			break
		}

		if _, ok := seen[fallback.Line]; ok {
			continue
		}

		candidates = append(candidates, fallback)
		seen[fallback.Line] = struct{}{}
	}

	return candidates
}

func implementationDispatchPlanDocumentAtomAnchorCandidates(lines []string, terms map[string]struct{}) []implementationDispatchPlanDocumentAtomAnchorCandidate {
	if len(terms) == 0 {
		return nil
	}

	var candidates []implementationDispatchPlanDocumentAtomAnchorCandidate

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		score := 0
		lower := strings.ToLower(trimmed)

		for term := range terms {
			if strings.Contains(lower, term) {
				score++
			}
		}

		if score == 0 {
			continue
		}

		candidates = append(candidates, implementationDispatchPlanDocumentAtomAnchorCandidate{
			Line:  i + 1,
			Text:  implementationDispatchAnchorSnippet(trimmed),
			Score: score,
		})
	}

	return candidates
}

func implementationDispatchPlanDocumentAtomFallbackAnchorCandidates(lines []string) []implementationDispatchPlanDocumentAtomAnchorCandidate {
	var candidates []implementationDispatchPlanDocumentAtomAnchorCandidate

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		candidates = append(candidates, implementationDispatchPlanDocumentAtomAnchorCandidate{
			Line:  i + 1,
			Text:  implementationDispatchAnchorSnippet(trimmed),
			Score: 1,
		})
		if len(candidates) >= 3 {
			return candidates
		}
	}

	return candidates
}

func implementationDispatchPlanDocumentAtomAnchorTerms(atom implementationDispatchPlanDocumentAtom) map[string]struct{} {
	text := strings.ToLower(strings.Join(append([]string{atom.Change, atom.Excerpt}, atom.TestObligations...), " "))
	terms := map[string]struct{}{}

	for _, raw := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_')
	}) {
		term := strings.Trim(raw, "_")
		if len(term) < 3 || implementationDispatchIgnoredAnchorTerm(term) {
			continue
		}

		terms[term] = struct{}{}
	}

	return terms
}

func implementationDispatchIgnoredAnchorTerm(term string) bool {
	switch term {
	case "the", "and", "for", "with", "this", "that", "from", "into", "file", "files", "path", "paths", "source", "design", "document", "doc", "scope", "slice", "atom", "edit", "edits", "modify", "change", "changes", "implementation", "implement":
		return true
	default:
		return false
	}
}

func implementationDispatchAnchorSnippet(text string) string {
	return strings.TrimSpace(text)
}

func implementationDispatchAtomChangeLooksLikeNewFile(change string) bool {
	lower := strings.ToLower(strings.TrimSpace(change))
	if lower == "" {
		return false
	}

	for _, prefix := range []string{
		"new file:",
		"new file target:",
		"new-file target:",
		"create file:",
		"create new file:",
		"add new file:",
		"write a new",
		"write new",
		"produce a new",
		"generate a new",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	return false
}

func composeImplementationDispatchPlanDocumentAtom(source implementationDispatchPlanDocument, atom implementationDispatchPlanDocumentAtom, index int, total int) string {
	path := strings.TrimSpace(atom.Path)
	change := strings.TrimSpace(atom.Change)

	if change == "" {
		change = "Implement this file's bounded slice from the source design document."
	}

	excerpt := strings.TrimSpace(atom.Excerpt)
	if excerpt == "" {
		excerpt = path
	}

	title := strings.TrimSuffix(filepath.Base(source.Path), filepath.Ext(source.Path))
	if title == "" || title == "." {
		title = "design-document"
	}

	var b strings.Builder

	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString(fmt.Sprintf(" atom %03d\n\n", index))
	b.WriteString("Status: pending\n\n")
	b.WriteString(fmt.Sprintf("<!-- idiom:design-doc-atom source=%q index=%d total=%d -->\n\n", source.Path, index, total))
	b.WriteString("## Files To Modify\n\n")
	b.WriteString("| File | Change |\n")
	b.WriteString("|---|---|\n")
	b.WriteString("| ")
	b.WriteString(path)
	b.WriteString(" | ")
	b.WriteString(strings.ReplaceAll(change, "\n", " "))
	b.WriteString(" |\n\n")
	b.WriteString("## Verified Current State\n\n")
	b.WriteString("- Source design document: `")
	b.WriteString(source.Path)
	b.WriteString("`.\n")
	b.WriteString("- The source design document lists `")
	b.WriteString(path)
	b.WriteString("` in its implementation scope.\n")

	if len(atom.Anchors) > 0 {
		b.WriteString("- Host current-state anchors captured before dispatch:\n")

		for _, anchor := range atom.Anchors {
			b.WriteString("  ")
			b.WriteString(anchor)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString("## Required Behavior\n\n")
	b.WriteString("Implement only this atom's bounded slice for `")
	b.WriteString(path)
	b.WriteString("`, preserving the source design document's intended behavior.\n\n")
	b.WriteString("Source scope row:\n\n")
	b.WriteString("> ")
	b.WriteString(strings.ReplaceAll(excerpt, "\n", "\n> "))
	b.WriteString("\n\n")

	if implementationDispatchPathAuthorizedByDocument(source, path) {
		b.WriteString("Typed plan execution contract:\n")
		b.WriteString(fmt.Sprintf("{\"authorized_new_files\":[{\"path\":%q}]}\n", path))
		b.WriteString("Typed plan authority projection:\n")
		b.WriteString(fmt.Sprintf("{\"atom_id\":\"atom-%03d\"}\n\n", index))
	}

	b.WriteString("Dispatch contract:\n\n")
	b.WriteString("- If the Host current-state anchors already show this atom's requested final state, close the refined brief as `Status: source-satisfied - do not dispatch` and cite the exact path evidence; do not invent a no-op edit.\n")
	b.WriteString("- If this atom has Design Doc Test Obligations, source-satisfied closeout must cite matching test evidence as well as source evidence.\n")
	b.WriteString("- If work remains for an existing file, state executable edit shape only: exact `old_text`/`new_text` blocks or `start_line`/`end_line` plus replacement `content`. Do not use prose-only mutation sites such as \"insert after\", \"append near\", or \"update around\" without that edit shape.\n")
	b.WriteString("- If this is a new file, provide the full file body as `replace_all:true` with empty `old_text` and complete `new_text`.\n\n")

	if len(atom.TestObligations) > 0 {
		b.WriteString("## Design Doc Test Obligations\n\n")
		b.WriteString("These obligations were extracted from the source design document and are active for this atom:\n\n")

		for _, obligation := range atom.TestObligations {
			obligation = strings.TrimSpace(obligation)
			if obligation == "" {
				continue
			}

			b.WriteString("- ")
			b.WriteString(obligation)
			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	b.WriteString("## Acceptance\n\n")
	b.WriteString("- The requested behavior is implemented for `")
	b.WriteString(path)
	b.WriteString("`.\n")

	if len(atom.TestObligations) > 0 {
		b.WriteString("- The Design Doc Test Obligations for this atom are represented in source and named in closeout evidence.\n")
	}

	b.WriteString("- No files outside this atom's Files To Modify list are edited.\n\n")
	b.WriteString("## Source Design Document\n\n")
	b.WriteString("Reference only. This section preserves design intent; the active write scope remains this atom's Files To Modify list.\n\n")
	b.WriteString(quoteImplementationDispatchReference(truncateImplementationDispatchText(source.Content, 30000)))

	return b.String()
}

func implementationDispatchPathAuthorizedByDocument(doc implementationDispatchPlanDocument, path string) bool {
	return len(implementationDispatchAtomAuthorizedNewFiles(doc, path)) > 0
}

func implementationDispatchAtomAuthorizedNewFiles(doc implementationDispatchPlanDocument, path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	for _, authorized := range implementationDispatchAuthorizedNewFiles("", []implementationDispatchPlanDocument{doc}) {
		if strings.TrimSpace(authorized) == path {
			return []string{path}
		}
	}

	return nil
}

func implementationDispatchWorkspaceRoot(cfg config.Config) string {
	if root := strings.TrimSpace(cfg.Workspace.Root); root != "" {
		return root
	}

	return strings.TrimSpace(cfg.Workspace.CWD)
}

func implementationDispatchWorkspaceFile(root string, rel string) (string, bool) {
	root = strings.TrimSpace(root)
	rel = strings.TrimSpace(rel)

	if root == "" || rel == "" || filepath.IsAbs(rel) {
		return "", false
	}

	cleanRel := filepath.Clean(rel)
	if cleanRel == "." || cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", false
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}

	abs, err := filepath.Abs(filepath.Join(rootAbs, cleanRel))
	if err != nil {
		return "", false
	}

	back, err := filepath.Rel(rootAbs, abs)
	if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
		return "", false
	}

	return abs, true
}

func implementationDispatchLooksLikePlanDocument(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".mdx", ".markdown":
		return true
	default:
		return false
	}
}

func implementationDispatchIsRefineScratchpadPath(path string) bool {
	normalized := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if normalized == "" || normalized == "." {
		return false
	}

	if !strings.HasPrefix(normalized, ".idiom/refine/") && !strings.Contains(normalized, "/.idiom/refine/") {
		return false
	}

	if strings.Contains(normalized, "/iter-") {
		return true
	}

	base := strings.ToLower(filepath.Base(normalized))

	return strings.HasPrefix(base, "meta-") || base == "draft.md" || base == "review.md" || base == "patch.md"
}

func implementationDispatchIsAtomizedPlanDocumentPath(path string) bool {
	normalized := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	return strings.HasPrefix(normalized, ".idiom/atoms/")
}

func implementationDispatchLooksLikeConcreteFile(path string) bool {
	if pathparse.HasWorkspaceFileExtension(path) {
		return true
	}

	switch filepath.Base(path) {
	case "Makefile", "go.mod", "go.sum", "README.md":
		return true
	default:
		return false
	}
}

