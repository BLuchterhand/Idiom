package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/config"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/surfaces/tui/implementationdispatch"
	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/planexecute"
	"idiom/internal/workflows/recovery"
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
	b.WriteString("The referenced document is not a dispatch-ready plan-execute brief yet. Convert it through refine or rewrite it with the canonical `## Files To Modify`, verified current state, exact `old_text`/`new_text` or `replace_all` edit shape, and acceptance commands before retrying.\n")

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

func (m *Model) newPlanExecutePromptRunner(ctx context.Context) planexecute.PromptRunner {
	if m == nil {
		return func(config.Config, string, planexecute.RunOptions, io.Writer) (string, error) {
			return "", fmt.Errorf("plan-execute prompt runner unavailable")
		}
	}

	return func(cfg config.Config, prompt string, opts planexecute.RunOptions, stdout io.Writer) (string, error) {
		restoreRoute := m.applyPlanExecutePromptRunnerRoute(ctx, opts)
		restore := m.applyPlanExecutePromptRunnerState(cfg, opts, stdout)

		priorMessages := append([]llama.ChatMessage(nil), m.messages...)
		priorReasoning := m.lastAssistantReasoningContent
		priorPinnedFiles := m.pinnedFiles
		priorSuppressPromptTranscript := m.suppressPromptTranscript
		priorSuppressGreenfield := m.suppressGreenfieldConfirmation

		m.messages = nil
		m.lastAssistantReasoningContent = ""
		m.pinnedFiles = nil
		m.suppressPromptTranscript = true
		m.suppressGreenfieldConfirmation = true

		defer func() {
			m.messages = priorMessages
			m.lastAssistantReasoningContent = priorReasoning
			m.pinnedFiles = priorPinnedFiles
			m.suppressPromptTranscript = priorSuppressPromptTranscript
			m.suppressGreenfieldConfirmation = priorSuppressGreenfield

			restore()
			restoreRoute()
		}()

		return m.RunPromptOnce(ctx, prompt)
	}
}

func (m *Model) applyPlanExecutePromptRunnerRoute(ctx context.Context, opts planexecute.RunOptions) func() {
	if workerruntime.TaskFamilyIsPlanThenExecutePlanner(opts.TaskFamily) {
		return func() {}
	}

	return m.applyExecutorPromptRoute(ctx, "plan-execute in-process runner", opts.UseActiveModel)
}

func (m *Model) applyExecutorPromptRoute(ctx context.Context, routeLabel string, useActiveModel bool) func() {
	if m == nil || m.implementerClient == nil {
		return func() {}
	}

	routeLabel = strings.TrimSpace(routeLabel)
	if routeLabel == "" {
		routeLabel = "executor route"
	}

	if useActiveModel {
		m.logMessage("dispatch_route", fmt.Sprintf("%s using active model %s for fallback", routeLabel, strings.TrimSpace(m.activeModelDisplayID())))

		return func() {}
	}

	if sameLlamaTarget(m.executorSpec, m.plannerSpec) {
		return func() {}
	}

	if !m.executorPromptRouteReachable(ctx) {
		m.logMessage("dispatch_route", fmt.Sprintf("%s falling back to active model %s because executor model %s is not reachable", routeLabel, strings.TrimSpace(m.activeModelDisplayID()), strings.TrimSpace(resolveModelDisplayID(m.executorSpec, m.executorSpec.Model))))

		return func() {}
	}

	priorClient := m.client
	priorPlannerSpec := m.plannerSpec
	priorModelID := m.modelID
	priorModelDisplayID := m.modelDisplayID
	priorModelThinkingFloor := m.modelThinkingFloor

	m.client = m.implementerClient
	m.plannerSpec = m.executorSpec
	m.modelID = resolveModelID(m.executorSpec, m.executorSpec.Model)
	m.modelDisplayID = resolveModelDisplayID(m.executorSpec, m.executorSpec.Model)
	m.modelThinkingFloor = m.executorSpec.IsHosted() || m.executorSpec.IsMLX()
	m.invalidateComposedPrompt()
	m.logMessage("dispatch_route", fmt.Sprintf("%s using executor model %s", routeLabel, strings.TrimSpace(m.activeModelDisplayID())))

	return func() {
		m.client = priorClient
		m.plannerSpec = priorPlannerSpec
		m.modelID = priorModelID
		m.modelDisplayID = priorModelDisplayID
		m.modelThinkingFloor = priorModelThinkingFloor
		m.invalidateComposedPrompt()
	}
}

func (m *Model) executorPromptRouteReachable(ctx context.Context) bool {
	if m == nil || m.implementerClient == nil {
		return false
	}

	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)

	defer cancel()

	models, err := m.implementerClient.ListModels(probeCtx)

	return err == nil && len(models) > 0
}

func (m *Model) applyPlanExecutePromptRunnerState(cfg config.Config, opts planexecute.RunOptions, stdout io.Writer) func() {
	if m == nil {
		return func() {}
	}

	priorSessionID := m.cfg.Workspace.SessionID
	priorWorkspaceSessionID := ""

	if m.workspace != nil {
		priorWorkspaceSessionID = m.workspace.SessionID()
	}

	priorImplementationDispatch := m.cfg.Chat.ImplementationDispatch
	priorForcedTaskFamily := m.forcedTaskFamily
	priorPlannerOwnedVerification := m.plannerOwnedVerification
	priorMaxTokensOverride := m.maxTokensOverride
	priorDisableThinkingOverride := m.disableThinkingOverride
	priorForcedReadinessOpenObligations := append([]string(nil), m.forcedReadinessOpenObligations...)
	priorEventStream := m.eventStream
	priorPlanExecuteActive := m.planExecuteActive
	priorPlanExecuteScopeContract := m.planExecuteScopeContract
	priorPlanExecuteWriteEnvelope := append([]string(nil), m.planExecuteWriteEnvelope...)
	priorPlanExecuteWriteEnvelopeExplicit := m.planExecuteWriteEnvelopeExplicit
	disableThinking := priorDisableThinkingOverride || opts.DisableThinking ||
		(workerruntime.TaskFamilyIsPlanThenExecutePlanner(opts.TaskFamily) && m.shouldDisableThinkingForPlanExecutePlanner())

	m.planExecuteActive = true
	m.planExecuteScopeContract = opts.ScopeContract.Normalized()
	m.cfg.Workspace.SessionID = cfg.Workspace.SessionID

	if m.workspace != nil {
		m.workspace.SetSessionID(cfg.Workspace.SessionID)
	}

	m.cfg.Chat.ImplementationDispatch = ""
	m.SetForcedTaskFamily(opts.TaskFamily)
	m.SetPlannerOwnedVerification(opts.PlannerOwnedVerification)
	m.SetMaxTokensOverride(opts.MaxTokensOverride)
	m.SetDisableThinkingOverride(disableThinking)
	m.SetForcedReadinessOpenObligations(opts.ReviewRepairOpenObligations)

	if opts.JSONEvents && stdout != nil {
		m.SetJSONEventStream(stdout, cfg.Workspace.SessionID)
	}

	return func() {
		m.planExecuteActive = priorPlanExecuteActive
		m.cfg.Workspace.SessionID = priorSessionID

		if m.workspace != nil {
			m.workspace.SetSessionID(priorWorkspaceSessionID)
		}

		m.cfg.Chat.ImplementationDispatch = priorImplementationDispatch
		m.SetForcedTaskFamily(priorForcedTaskFamily)
		m.SetPlannerOwnedVerification(priorPlannerOwnedVerification)
		m.SetMaxTokensOverride(priorMaxTokensOverride)
		m.SetDisableThinkingOverride(priorDisableThinkingOverride)
		m.SetForcedReadinessOpenObligations(priorForcedReadinessOpenObligations)

		m.planExecuteScopeContract = priorPlanExecuteScopeContract
		m.planExecuteWriteEnvelope = priorPlanExecuteWriteEnvelope
		m.planExecuteWriteEnvelopeExplicit = priorPlanExecuteWriteEnvelopeExplicit
		m.eventStream = priorEventStream
	}
}

func (m *Model) runImplementationViaPlanExecute(ctx context.Context, prompt string, plan promptExecutionPlan) (string, error) {
	_, _, planDocuments, resolutionBlockers := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, plan, m.cfg)
	if len(resolutionBlockers) > 0 {
		var refs []string

		for _, blocker := range resolutionBlockers {
			refs = append(refs, strings.TrimSpace(blocker.Ref))
		}

		m.logMessage("dispatch_preflight", fmt.Sprintf("blocked plan-execute dispatch for unresolved referenced design document(s): %s", strings.Join(refs, ", ")))

		return humanPlanDocumentResolutionBlockedMessage(resolutionBlockers), nil
	}

	if implementationDispatchPlanDocumentsShouldPreAtomize(planDocuments) {
		m.logMessage("dispatch_atomize", "referenced design document exceeds single-purpose dispatch limits; materializing atom briefs before plan-execute")

		m.queueUIEvent(infoMsg{text: "Materializing design document atoms before plan-execute..."})

		atomDocuments, err := materializeImplementationDispatchPlanDocumentAtoms(m.cfg, planDocuments)
		if err != nil {
			return "", err
		}

		return m.runAtomizedImplementationPlanDocumentsViaPlanExecute(ctx, prompt, plan, atomDocuments)
	}

	return m.runImplementationPlanDocumentsViaPlanExecute(ctx, prompt, plan, planDocuments)
}

func (m *Model) runImplementationPlanDocumentsViaPlanExecute(ctx context.Context, prompt string, plan promptExecutionPlan, planDocuments []implementationDispatchPlanDocument) (string, error) {
	planDocuments = implementationDispatchPlanDocumentsWithDirtyAllowance(prompt, planDocuments)

	requiredPaths, allowedPaths := implementationDispatchEditScopeFromPlanDocuments(prompt, plan, m.cfg, planDocuments)

	if closed := implementationDispatchPlanDocumentsTerminalAtoms(planDocuments); len(closed) == len(planDocuments) && len(closed) > 0 {
		m.logMessage("dispatch_source_satisfied", fmt.Sprintf("skipping plan-execute for %d closed design-doc atom(s)", len(closed)))

		return humanImplementationDispatchTerminalAtomMessage(closed), nil
	}

	if satisfied := implementationDispatchPlanDocumentsSourceSatisfied(m.cfg, planDocuments); len(satisfied) == len(planDocuments) && len(satisfied) > 0 {
		m.logMessage("dispatch_source_satisfied", fmt.Sprintf("skipping plan-execute for %d already-satisfied design-doc atom(s)", len(satisfied)))
		implementationDispatchMarkSourceSatisfiedAtomDocuments(m.cfg, satisfied)

		return humanImplementationDispatchSourceSatisfiedMessage(satisfied), nil
	}

	if implementationDispatchPlanDocumentsNeedRefineBeforeDispatch(planDocuments) {
		m.logMessage("dispatch_refine", "materialized design-doc atom requires refine before plan-execute")

		m.queueUIEvent(infoMsg{text: "Refining materialized design-doc atom before plan-execute..."})

		var refineResults []implementationDispatchPlanDocumentRefineResult

		refinedPlanDocuments := append([]implementationDispatchPlanDocument(nil), planDocuments...)

		for _, doc := range planDocuments {
			if !implementationDispatchPlanDocumentNeedsRefineBeforeDispatch(doc) {
				continue
			}

			refined := implementationDispatchPlanDocumentRefineRunner(ctx, m.cfg, doc)

			refineResults = append(refineResults, refined)

			if !refined.OK {
				if len(refined.PostPreflightBlockers) > 0 {
					if implementationDispatchPreflightAllSourceSatisfied(refined.PostPreflightBlockers) {
						m.logMessage("dispatch_source_satisfied", fmt.Sprintf("refined atom candidate classified %d atom(s) as source-satisfied", len(refined.PostPreflightBlockers)))
						implementationDispatchMarkPreflightSourceSatisfiedAtomDocuments(m.cfg, refined.PostPreflightBlockers)

						return humanImplementationDispatchPreflightSourceSatisfiedMessage(refined.PostPreflightBlockers), nil
					}

					m.logMessage("dispatch_refine", fmt.Sprintf("refined atom candidate still blocked by preflight for %s", strings.TrimSpace(refined.OutputPath)))

					return humanPlanDocumentRefinePostPreflightBlockedMessage(refineResults, refined.PostPreflightBlockers), nil
				}

				if implementationDispatchRefinedDocumentIsDispatchable(ctx, m.cfg, refined.Document) {
					m.logMessage("dispatch_refine", fmt.Sprintf("using structurally dispatchable refined atom %s despite nonzero refine exit", strings.TrimSpace(refined.OutputPath)))

					refinedPlanDocuments = replaceImplementationDispatchPlanDocument(refinedPlanDocuments, doc.Path, refined.Document)

					continue
				}

				m.logMessage("dispatch_refine", fmt.Sprintf("refine failed for %s (code=%d output=%s)", doc.Path, refined.ExitCode, strings.TrimSpace(refined.OutputPath)))

				return humanPlanDocumentRefineFailedMessage(nil, refineResults), nil
			}

			m.logMessage("dispatch_refine", fmt.Sprintf("refined %s to %s", doc.Path, refined.OutputPath))

			refinedPlanDocuments = replaceImplementationDispatchPlanDocument(refinedPlanDocuments, doc.Path, refined.Document)
		}

		planDocuments = refinedPlanDocuments
		requiredPaths, allowedPaths = implementationDispatchEditScopeFromPlanDocuments(prompt, plan, m.cfg, planDocuments)

		if refinedBlocked := preflightImplementationDispatchPlanDocuments(ctx, m.cfg, planDocuments); len(refinedBlocked) > 0 {
			if implementationDispatchPreflightAllSourceSatisfied(refinedBlocked) {
				m.logMessage("dispatch_source_satisfied", fmt.Sprintf("refined atom preflight classified %d atom(s) as source-satisfied", len(refinedBlocked)))
				implementationDispatchMarkPreflightSourceSatisfiedAtomDocuments(m.cfg, refinedBlocked)

				return humanImplementationDispatchPreflightSourceSatisfiedMessage(refinedBlocked), nil
			}

			m.logMessage("dispatch_refine", "refined atom brief still blocked by preflight")

			return humanPlanDocumentRefinePostPreflightBlockedMessage(refineResults, refinedBlocked), nil
		}
	}

	if blocked := preflightImplementationDispatchPlanDocuments(ctx, m.cfg, planDocuments); len(blocked) > 0 {
		if implementationDispatchPreflightAllSourceSatisfied(blocked) {
			m.logMessage("dispatch_source_satisfied", fmt.Sprintf("preflight classified %d atom(s) as source-satisfied", len(blocked)))
			implementationDispatchMarkPreflightSourceSatisfiedAtomDocuments(m.cfg, blocked)

			return humanImplementationDispatchPreflightSourceSatisfiedMessage(blocked), nil
		}

		if implementationDispatchPreflightNeedsAtomization(blocked) && implementationDispatchCanAtomizePlanDocuments(planDocuments) {
			m.logMessage("dispatch_atomize", "preflight requested sub-atoms; materializing atom briefs before plan-execute")

			m.queueUIEvent(infoMsg{text: "Materializing design document atoms before plan-execute..."})

			atomDocuments, err := materializeImplementationDispatchPlanDocumentAtoms(m.cfg, planDocuments)
			if err != nil {
				return "", err
			}

			return m.runAtomizedImplementationPlanDocumentsViaPlanExecute(ctx, prompt, plan, atomDocuments)
		}

		if implementationDispatchPreflightCanRefine(blocked) {
			m.logMessage("dispatch_refine", "preflight found non-dispatchable plan document shape; running refine before plan-execute")

			m.queueUIEvent(infoMsg{text: "Refining referenced plan document before plan-execute..."})

			var refineResults []implementationDispatchPlanDocumentRefineResult

			refinedPlanDocuments := append([]implementationDispatchPlanDocument(nil), planDocuments...)

			for _, result := range blocked {
				docPath := strings.TrimSpace(result.Path)

				var original implementationDispatchPlanDocument

				found := false

				for _, doc := range planDocuments {
					if doc.Path == docPath {
						original = doc
						found = true

						break
					}
				}

				if !found {
					continue
				}

				refined := implementationDispatchPlanDocumentRefineRunner(ctx, m.cfg, original)

				refineResults = append(refineResults, refined)

				if !refined.OK {
					if len(refined.PostPreflightBlockers) > 0 {
						if implementationDispatchPreflightAllSourceSatisfied(refined.PostPreflightBlockers) {
							m.logMessage("dispatch_source_satisfied", fmt.Sprintf("refined candidate classified %d atom(s) as source-satisfied", len(refined.PostPreflightBlockers)))
							implementationDispatchMarkPreflightSourceSatisfiedAtomDocuments(m.cfg, refined.PostPreflightBlockers)

							return humanImplementationDispatchPreflightSourceSatisfiedMessage(refined.PostPreflightBlockers), nil
						}

						m.logMessage("dispatch_refine", fmt.Sprintf("refined candidate still blocked by preflight for %s", strings.TrimSpace(refined.OutputPath)))

						return humanPlanDocumentRefinePostPreflightBlockedMessage(refineResults, refined.PostPreflightBlockers), nil
					}

					if implementationDispatchRefinedDocumentIsDispatchable(ctx, m.cfg, refined.Document) {
						m.logMessage("dispatch_refine", fmt.Sprintf("using structurally dispatchable refined brief %s despite nonzero refine exit", strings.TrimSpace(refined.OutputPath)))

						refinedPlanDocuments = replaceImplementationDispatchPlanDocument(refinedPlanDocuments, docPath, refined.Document)

						continue
					}

					m.logMessage("dispatch_refine", fmt.Sprintf("refine failed for %s (code=%d output=%s)", docPath, refined.ExitCode, strings.TrimSpace(refined.OutputPath)))

					return humanPlanDocumentRefineFailedMessage(blocked, refineResults), nil
				}

				m.logMessage("dispatch_refine", fmt.Sprintf("refined %s to %s", docPath, refined.OutputPath))

				refinedPlanDocuments = replaceImplementationDispatchPlanDocument(refinedPlanDocuments, docPath, refined.Document)
			}

			planDocuments = refinedPlanDocuments
			requiredPaths, allowedPaths = implementationDispatchEditScopeFromPlanDocuments(prompt, plan, m.cfg, planDocuments)

			if refinedBlocked := preflightImplementationDispatchPlanDocuments(ctx, m.cfg, planDocuments); len(refinedBlocked) > 0 {
				if implementationDispatchPreflightAllSourceSatisfied(refinedBlocked) {
					m.logMessage("dispatch_source_satisfied", fmt.Sprintf("refined preflight classified %d atom(s) as source-satisfied", len(refinedBlocked)))
					implementationDispatchMarkPreflightSourceSatisfiedAtomDocuments(m.cfg, refinedBlocked)

					return humanImplementationDispatchPreflightSourceSatisfiedMessage(refinedBlocked), nil
				}

				if implementationDispatchPreflightNeedsAtomization(refinedBlocked) && implementationDispatchCanAtomizePlanDocuments(planDocuments) {
					m.logMessage("dispatch_atomize", "refined brief still exceeds single-purpose limits; materializing atom briefs before plan-execute")

					m.queueUIEvent(infoMsg{text: "Materializing refined design document atoms before plan-execute..."})

					atomDocuments, err := materializeImplementationDispatchPlanDocumentAtoms(m.cfg, planDocuments)
					if err != nil {
						return "", err
					}

					return m.runAtomizedImplementationPlanDocumentsViaPlanExecute(ctx, prompt, plan, atomDocuments)
				}

				m.logMessage("dispatch_refine", "refined brief still blocked by preflight")

				return humanPlanDocumentRefinePostPreflightBlockedMessage(refineResults, refinedBlocked), nil
			}
		} else {
			var paths []string

			for _, result := range blocked {
				paths = append(paths, strings.TrimSpace(result.Path))
			}

			m.logMessage("dispatch_preflight", fmt.Sprintf("blocked plan-execute dispatch for referenced plan document(s): %s", strings.Join(paths, ", ")))

			return humanPlanDocumentPreflightBlockedMessage(blocked), nil
		}
	}

	return m.runSingleImplementationPlanExecute(ctx, prompt, plan, requiredPaths, allowedPaths, planDocuments)
}

func implementationDispatchPlanDocumentsNeedRefineBeforeDispatch(planDocuments []implementationDispatchPlanDocument) bool {
	for _, doc := range planDocuments {
		if implementationDispatchPlanDocumentNeedsRefineBeforeDispatch(doc) {
			return true
		}
	}

	return false
}

func implementationDispatchPlanDocumentNeedsRefineBeforeDispatch(doc implementationDispatchPlanDocument) bool {
	return implementationDispatchIsAtomizedPlanDocumentPath(doc.Path)
}

func implementationDispatchRefinedDocumentIsDispatchable(ctx context.Context, cfg config.Config, doc implementationDispatchPlanDocument) bool {
	if strings.TrimSpace(doc.Path) == "" || strings.TrimSpace(doc.Content) == "" {
		return false
	}

	return len(preflightImplementationDispatchPlanDocuments(ctx, cfg, []implementationDispatchPlanDocument{doc})) == 0
}

func (m *Model) runAtomizedImplementationPlanDocumentsViaPlanExecute(ctx context.Context, prompt string, plan promptExecutionPlan, atomDocuments []implementationDispatchPlanDocument) (string, error) {
	if len(atomDocuments) == 0 {
		return "Plan-execute dispatch blocked before the planner ran.\n\nThe referenced design document needed sub-atoms, but no dispatchable atom briefs could be materialized.", nil
	}

	atomDocuments = implementationDispatchPlanDocumentsWithDirtyAllowance(prompt, atomDocuments)
	m.logMessage("dispatch_atomize", fmt.Sprintf("dispatching %d materialized atom brief(s) through plan-execute", len(atomDocuments)))

	var (
		summaries []string
		skipped   []implementationDispatchTerminalAtomRecord
	)

	for i, atomDoc := range atomDocuments {
		if status, ok := implementationDispatchPlanDocumentTerminalAtomStatus(atomDoc); ok {
			skipped = append(skipped, implementationDispatchTerminalAtomRecord{DocumentPath: atomDoc.Path, Status: status})
			summaries = append(summaries, fmt.Sprintf("Atom %d/%d `%s` skipped: %s", i+1, len(atomDocuments), atomDoc.Path, status))

			continue
		}

		atomPrompt := prompt
		if len(atomDocuments) > 1 {
			atomPrompt = fmt.Sprintf("%s\n\nDispatch atom %d/%d: %s", strings.TrimSpace(prompt), i+1, len(atomDocuments), atomDoc.Path)
		}

		atomPlan := plan

		atomPlan.runtimeBrief.RequiredEditPaths = nil

		answer, err := m.runImplementationPlanDocumentsViaPlanExecute(ctx, atomPrompt, atomPlan, []implementationDispatchPlanDocument{atomDoc})
		if err != nil {
			return "", err
		}

		if implementationDispatchAnswerLooksBlocked(answer) {
			return answer, nil
		}

		if pendingCommands := implementationDispatchUnprovenVerificationCommands(atomDoc, answer); len(pendingCommands) > 0 {
			return humanImplementationDispatchPendingVerificationCommandMessage(atomDoc, pendingCommands), nil
		}

		implementationDispatchMarkCompletedAtomDocument(m.cfg, atomDoc, answer)

		summaries = append(summaries, fmt.Sprintf("Atom %d/%d `%s`:\n%s", i+1, len(atomDocuments), atomDoc.Path, strings.TrimSpace(answer)))
	}

	var b strings.Builder

	if len(skipped) == len(atomDocuments) {
		b.WriteString(fmt.Sprintf("Design document implementation already closed across %d atom(s).", len(atomDocuments)))
	} else {
		b.WriteString(fmt.Sprintf("Design document implementation completed through %d atom(s).", len(atomDocuments)-len(skipped)))

		if len(skipped) > 0 {
			b.WriteString(fmt.Sprintf(" Skipped %d already-closed atom(s).", len(skipped)))
		}
	}

	if obligations := implementationDispatchTestObligationsFromDocuments(atomDocuments); len(obligations) > 0 {
		b.WriteString("\n\nDesign-doc test obligations tracked:")

		for _, obligation := range obligations {
			b.WriteString("\n- ")
			b.WriteString(obligation)
		}
	}

	for _, summary := range summaries {
		b.WriteString("\n\n")
		b.WriteString(summary)
	}

	return strings.TrimSpace(b.String()), nil
}

func implementationDispatchTestObligationsFromDocuments(docs []implementationDispatchPlanDocument) []string {
	var obligations []string

	for _, doc := range docs {
		obligations = appendUniqueBriefStrings(obligations, doc.TestObligations...)
	}

	return obligations
}

func implementationDispatchAnswerLooksBlocked(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}

	return strings.HasPrefix(lower, "<<question>>") ||
		strings.Contains(lower, "<<question>>") ||
		strings.Contains(lower, "plan-execute could not produce a plan artifact") ||
		strings.Contains(lower, "planner phase did not produce a plan artifact") ||
		strings.Contains(lower, "planner phase reported a blocker") ||
		strings.Contains(lower, "required edit paths still need a plan") ||
		strings.Contains(lower, "blocked dispatch") ||
		strings.Contains(lower, "blocked before the planner ran") ||
		strings.Contains(lower, "automatic refine did not produce") ||
		strings.Contains(lower, "still blocked by preflight")
}

func (m *Model) runSingleImplementationPlanExecute(ctx context.Context, prompt string, plan promptExecutionPlan, requiredPaths []string, allowedPaths []string, planDocuments []implementationDispatchPlanDocument) (string, error) {
	brief := composeImplementationBriefWithEditScopeAndPlanDocuments(prompt, plan, m.cfg, requiredPaths, allowedPaths, planDocuments)

	var contextPackLog bytes.Buffer

	contextPackSection := planexecute.BuildPromptContextPackSection(m.cfg, planexecute.PromptContextPackOptions{
		Mode:              "plan-execute",
		Title:             prompt,
		Brief:             brief,
		ProjectRoot:       implementationDispatchWorkspaceRoot(m.cfg),
		RequiredEditPaths: requiredPaths,
	}, &contextPackLog)

	if diag := strings.TrimSpace(contextPackLog.String()); diag != "" {
		m.logMessage("dispatch_context", truncateImplementationDispatchText(diag, 500))
	}

	if strings.TrimSpace(contextPackSection) != "" {
		m.logMessage("dispatch_context", "preloaded context_pack for in-process plan-execute dispatch")
	}

	if len(requiredPaths) == 0 && len(allowedPaths) == 0 && strings.TrimSpace(contextPackSection) != "" {
		allowedPaths = m.implementationDispatchAllowedPathsFromPreloadedContextPack(prompt, contextPackSection)
		if len(allowedPaths) > 0 {
			m.logMessage("dispatch_scope", fmt.Sprintf("derived %d candidate edit path(s) from preloaded context_pack", len(allowedPaths)))

			brief = composeImplementationBriefWithEditScopeAndPlanDocuments(prompt, plan, m.cfg, requiredPaths, allowedPaths, planDocuments)
		} else {
			allowedPaths = m.resolveUnscopedImplementationAllowedPaths(ctx, prompt)
			allowedPaths = uniqueImplementationDispatchPaths(allowedPaths)

			if len(allowedPaths) > 0 {
				m.logMessage("dispatch_scope", fmt.Sprintf("derived %d candidate edit path(s) from fallback discovery after context_pack scope was not relevant", len(allowedPaths)))

				brief = composeImplementationBriefWithEditScopeAndPlanDocuments(prompt, plan, m.cfg, requiredPaths, allowedPaths, planDocuments)
			}
		}
	}

	brief = planexecute.ComposePlannerPromptWithContextPack(brief, contextPackSection)

	if len(requiredPaths) > 0 {
		m.recordRollbackSnapshotsForPaths(requiredPaths)
		m.logMessage("dispatch_scope", fmt.Sprintf("derived %d required edit path(s) for plan-execute dispatch", len(requiredPaths)))
	} else if len(allowedPaths) > 0 {
		m.recordRollbackSnapshotsForPaths(allowedPaths)
		m.logMessage("dispatch_scope", fmt.Sprintf("derived %d candidate edit path(s) from referenced plan document", len(allowedPaths)))
	}

	var editSetObligations planexecute.EditSetObligations

	scopeContract := planexecute.NewPlanScopeContract(requiredPaths, allowedPaths, editSetObligations)

	if m.planExecuteWriteEnvelopeExplicit {
		scopeContract = scopeContract.WithWriteAllowedPaths(m.planExecuteWriteEnvelope)
	}

	opts := planexecute.Options{
		Prompt:             brief,
		Model:              m.implementationDispatchPlanExecuteModelOverride(),
		SessionID:          featureplanner.PlanExecuteSessionIDFromImplementorPrompt(prompt),
		RequiredEditPaths:  requiredPaths,
		AllowedEditPaths:   allowedPaths,
		EditSetObligations: editSetObligations,
		ScopeContract:      scopeContract,
		AuthorizedNewFiles: implementationDispatchAuthorizedNewFiles(prompt, planDocuments),
		MaxTokens:          implementationDispatchPlanExecuteMaxTokens(m.cfg.Workspace.Root, prompt, requiredPaths, allowedPaths, planDocuments),
		OnPhase: func(phase, detail string) {
			m.queueUIEvent(planPhaseMsg{phase: phase, detail: detail})
		},
		OnEditProgress: func(step, total int, path string, succeeded bool) {
			if succeeded {
				m.attachWriteToSubmittedEdit(path, fmt.Sprintf("pe-%d", step-1))
			}

			m.queueUIEvent(planRefreshMsg{})

			label := "succeeded"
			if !succeeded {
				label = "failed"
			}

			m.queueUIEvent(planPhaseMsg{phase: "executing", detail: fmt.Sprintf("Edit %d/%d %s (%s)", step, total, filepath.Base(path), label)})
		},
	}
	promptRun := m.newPlanExecutePromptRunner(ctx)

	m.logMessage("dispatch", "invoking plan-execute in-process")

	m.queueUIEvent(infoMsg{text: "Dispatching to plan-execute..."})

	var outBuf, errBuf bytes.Buffer

	result := planexecute.Run(m.cfg, opts, promptRun, nil, &outBuf, &errBuf)

	m.lastPlanExecuteRunResult = &result

	m.logMessage("dispatch_result", fmt.Sprintf("plan-execute finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
		result.ExitCode, result.SessionID, result.ResumeStatePath, outBuf.Len(), errBuf.Len()))

	if result.ExitCode != 0 {
		if msg, ok := humanPlanExecuteExecutionRecoveryOpenMessage(m.cfg.Workspace.Root, result, requiredPaths); ok {
			return msg, nil
		}

		if implementationDispatchPlanExecuteFailureCanUseRecovery(result) {
			answer, handled, err := m.handlePlanExecutePlannerBlocked(ctx, implementationDispatchPlanExecuteFailure{
				Prompt:        prompt,
				Plan:          plan,
				Brief:         brief,
				Options:       opts,
				RequiredPaths: requiredPaths,
				AllowedPaths:  allowedPaths,
				Result:        result,
				Stdout:        outBuf.String(),
				Stderr:        errBuf.String(),
			})
			if handled || err != nil {
				return answer, err
			}
		}

		return "", fmt.Errorf("plan-execute exited %d (session=%s state=%s): %s", result.ExitCode, result.SessionID, result.ResumeStatePath, strings.TrimSpace(errBuf.String()))
	}

	answer := strings.TrimSpace(outBuf.String())
	if strings.TrimSpace(result.ResumeStatePath) != "" && !strings.Contains(strings.ToLower(answer), "progress_state:") {
		answer = strings.TrimSpace(answer + "\nprogress_state: " + strings.TrimSpace(result.ResumeStatePath))
	}

	return answer, nil
}

func (m *Model) implementationDispatchPlanExecuteModelOverride() string {
	if m == nil || !m.plannerSpec.IsHosted() {
		return ""
	}

	return strings.TrimSpace(util.FirstNonEmpty(
		m.modelID,
		resolveModelID(m.plannerSpec, m.plannerSpec.Model),
		m.plannerSpec.Model,
		m.cfg.LlamaServer.Model,
	))
}

func humanPlanExecuteExecutionRecoveryOpenMessage(root string, result planexecute.RunResult, fallbackRequiredPaths []string) (string, bool) {
	switch strings.TrimSpace(result.FailureKind) {
	case planexecute.RunFailureSourceSanityFailed, planexecute.RunFailureVerificationRequired:
	default:
		return "", false
	}

	state, _, found, _ := planexecute.TryLoadProgressState(root, result.SessionID)
	requiredPaths := appendUniqueBriefStrings(nil, fallbackRequiredPaths...)

	if found && len(state.RequiredEditPaths) > 0 {
		requiredPaths = appendUniqueBriefStrings(nil, state.RequiredEditPaths...)
	}

	var b strings.Builder

	b.WriteString("plan-execute: recovery_open\n")
	fmt.Fprintf(&b, "session_id: %s\n", strings.TrimSpace(result.SessionID))

	if strings.TrimSpace(result.ResumeStatePath) != "" {
		fmt.Fprintf(&b, "progress_state: %s\n", strings.TrimSpace(result.ResumeStatePath))
	}

	fmt.Fprintf(&b, "failure_kind: %s\n", strings.TrimSpace(result.FailureKind))

	if reason := strings.TrimSpace(result.FailureReason); reason != "" {
		fmt.Fprintf(&b, "reason: %s\n", reason)
	}

	if len(requiredPaths) > 0 {
		b.WriteString("required_edit_paths:\n")

		for _, path := range requiredPaths {
			fmt.Fprintf(&b, "- %s\n", path)
		}
	}

	if found {
		if len(state.Executor.SourceSanity) > 0 {
			b.WriteString("source_sanity:\n")

			for _, check := range state.Executor.SourceSanity {
				fmt.Fprintf(&b, "- %s %s %s\n", strings.TrimSpace(check.Path), strings.TrimSpace(check.Status), strings.TrimSpace(firstNonEmpty(check.Error, check.Summary)))
			}
		}

		if len(state.Executor.Verification) > 0 {
			b.WriteString("verification:\n")

			for _, check := range state.Executor.Verification {
				fmt.Fprintf(&b, "- %s %s %s\n", strings.TrimSpace(check.Command), strings.TrimSpace(check.Status), strings.TrimSpace(firstNonEmpty(check.Error, check.Summary)))
			}
		}
	}

	b.WriteString("next: inspect the progress state and recover the generated source before dispatching sibling atoms\n")

	return strings.TrimSpace(b.String()), true
}

func implementationDispatchPlanExecuteMaxTokens(root string, prompt string, requiredPaths []string, allowedPaths []string, planDocuments []implementationDispatchPlanDocument) int {
	if implementationDispatchPlanExecuteNeedsLargeBodyBudget(root, prompt, requiredPaths, allowedPaths, planDocuments) {
		return 16384
	}

	return 8192
}

func implementationDispatchPlanExecuteNeedsLargeBodyBudget(root string, prompt string, requiredPaths []string, allowedPaths []string, planDocuments []implementationDispatchPlanDocument) bool {
	if len(planDocuments) > 0 {
		for _, doc := range planDocuments {
			docText := strings.ToLower(doc.Content)
			if strings.Contains(docText, "new file") || strings.Contains(docText, "replace_all") || strings.Contains(docText, "full file body") {
				return true
			}

			for _, path := range doc.EditPaths {
				base := strings.ToLower(filepath.Base(filepath.ToSlash(path)))
				if strings.Contains(base, "new") {
					return true
				}
			}
		}
	}

	lower := strings.ToLower(prompt)
	for _, phrase := range []string{"create new file", "add new file", "write new file", "whole-file", "replace_all", "full file body"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	for _, path := range append(append([]string(nil), requiredPaths...), allowedPaths...) {
		lowerPath := strings.ToLower(filepath.ToSlash(path))
		if strings.Contains(lowerPath, "/new/") || strings.Contains(filepath.Base(lowerPath), "new_") {
			return true
		}

		if implementationDispatchPromptImpliesNewFilePath(lower, lowerPath) {
			return true
		}

		if root != "" && implementationDispatchPathMissing(root, path) && implementationDispatchPromptMentionsPathCreation(lower, lowerPath) {
			return true
		}
	}

	return false
}

func implementationDispatchPromptImpliesNewFilePath(lowerPrompt string, lowerPath string) bool {
	if lowerPrompt == "" || lowerPath == "" || !strings.Contains(lowerPrompt, lowerPath) {
		return false
	}

	if strings.Contains(lowerPrompt, " create ") ||
		strings.HasPrefix(lowerPrompt, "create ") ||
		strings.Contains(lowerPrompt, " write ") ||
		strings.HasPrefix(lowerPrompt, "write ") {

		return true
	}

	if strings.Contains(lowerPrompt, " add "+lowerPath) || strings.Contains(lowerPrompt, " add new") || strings.Contains(lowerPrompt, " add file") {
		return true
	}

	return strings.HasPrefix(lowerPrompt, "add "+lowerPath) ||
		strings.HasPrefix(lowerPrompt, "add new ") ||
		strings.HasPrefix(lowerPrompt, "add file ")
}

func implementationDispatchPromptMentionsPathCreation(lowerPrompt string, lowerPath string) bool {
	if !implementationDispatchPromptImpliesNewFilePath(lowerPrompt, lowerPath) {
		return false
	}

	return strings.Contains(lowerPrompt, " file") || strings.Contains(lowerPrompt, lowerPath)
}

func implementationDispatchPathMissing(root string, path string) bool {
	path = strings.TrimSpace(path)
	if root == "" || path == "" || filepath.IsAbs(path) {
		return false
	}

	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))

	return os.IsNotExist(err)
}

type implementationDispatchPlanExecuteFailureKind string

const (
	implementationDispatchFailurePlannerBlocked                 implementationDispatchPlanExecuteFailureKind = implementationDispatchPlanExecuteFailureKind(planexecute.RunFailurePlannerBlocked)
	implementationDispatchFailurePlanArtifactMissing            implementationDispatchPlanExecuteFailureKind = "plan_artifact_missing"
	implementationDispatchFailureMissingRequestedTestEdit       implementationDispatchPlanExecuteFailureKind = implementationDispatchPlanExecuteFailureKind(planexecute.EditSetFailureMissingRequestedTestEdit)
	implementationDispatchFailureMissingRequestedImplementation implementationDispatchPlanExecuteFailureKind = implementationDispatchPlanExecuteFailureKind(planexecute.EditSetFailureMissingRequestedImplementationEdit)
	implementationDispatchFailureExecutorFailed                 implementationDispatchPlanExecuteFailureKind = "executor_failed"
)

func implementationDispatchPlanExecuteFailureKindFromString(kind string) implementationDispatchPlanExecuteFailureKind {
	return implementationDispatchPlanExecuteFailureKind(strings.TrimSpace(kind))
}

func implementationDispatchPlanExecuteRunFailureKind(result planexecute.RunResult) implementationDispatchPlanExecuteFailureKind {
	return implementationDispatchPlanExecuteFailureKindFromString(result.FailureKind)
}

func implementationDispatchAuthorizedNewFiles(prompt string, planDocuments []implementationDispatchPlanDocument) []string {
	paths := append([]string{}, featureplanner.PlanExecuteAuthorizedNewFilesFromImplementorPrompt(prompt)...)
	for _, doc := range planDocuments {
		paths = append(paths, doc.AuthorizedNewFiles...)
		paths = append(paths, featureplanner.PlanExecuteAuthorizedNewFilesFromImplementorPrompt(doc.Content)...)
	}

	return uniqueImplementationDispatchPaths(paths)
}

func implementationDispatchPlanExecuteRunFailureIs(result planexecute.RunResult, kind implementationDispatchPlanExecuteFailureKind) bool {
	return implementationDispatchPlanExecuteRunFailureKind(result) == kind
}

func implementationDispatchPlanExecuteFailureCanUseRecovery(result planexecute.RunResult) bool {
	switch implementationDispatchPlanExecuteRunFailureKind(result) {
	case implementationDispatchFailurePlannerBlocked,
		implementationDispatchFailurePlanArtifactMissing,
		implementationDispatchFailureMissingRequestedTestEdit,
		implementationDispatchFailureMissingRequestedImplementation:
		return true
	default:
		return false
	}
}

type implementationDispatchPlanExecuteFailure struct {
	Prompt                   string
	Plan                     promptExecutionPlan
	Brief                    string
	Options                  planexecute.Options
	RequiredPaths            []string
	AllowedPaths             []string
	Result                   planexecute.RunResult
	Stdout                   string
	Stderr                   string
	PlannerBlockedChainDepth int
}

func implementationDispatchFailureKindToEditSetObligation(kind string) (planexecute.EditSetObligationKind, bool) {
	switch implementationDispatchPlanExecuteFailureKindFromString(kind) {
	case implementationDispatchFailureMissingRequestedTestEdit:
		return planexecute.EditSetObligationKindTestEdit, true
	case implementationDispatchFailureMissingRequestedImplementation:
		return planexecute.EditSetObligationKindImplementationEdit, true
	default:
		return "", false
	}
}

func implementationDispatchOptionsAlreadyCarriedEditSetObligation(opts planexecute.Options, kind planexecute.EditSetObligationKind) bool {
	return implementationDispatchEditSetObligationHasNonPromptSource(opts.EditSetObligations, kind) ||
		implementationDispatchEditSetObligationHasNonPromptSource(opts.ScopeContract.EditSetObligations, kind)
}

func implementationDispatchEditSetObligationHasNonPromptSource(obligations planexecute.EditSetObligations, kind planexecute.EditSetObligationKind) bool {
	for _, obligation := range obligations.Normalized() {
		if obligation.Kind != kind {
			continue
		}

		source := strings.TrimSpace(obligation.Source)

		return source == "" || source != "prompt"
	}

	return false
}

func implementationDispatchShouldChainMissingEditRecovery(result planexecute.RunResult) bool {
	_, ok := implementationDispatchFailureKindToEditSetObligation(result.FailureKind)
	return ok
}

type implementationDispatchMissingEditRetrySpec struct {
	failureKind          implementationDispatchPlanExecuteFailureKind
	obligationKind       planexecute.EditSetObligationKind
	allowedPaths         func(*Model, implementationDispatchPlanExecuteFailure) []string
	appendAddendum       func(string, []string) string
	alreadyCarriedLog    string
	alreadyCarriedReason string
	noScopeLog           string
	noScopeReason        string
	retryLog             string
	retryInfo            string
	resultLogLabel       string
	chainLogPrefix       string
	executorFailedReason string
	finalReason          string
}

func (m *Model) handlePlanExecutePlannerBlocked(ctx context.Context, failure implementationDispatchPlanExecuteFailure) (string, bool, error) {
	const maxPlannerBlockedChainDepth = 3

	if failure.PlannerBlockedChainDepth >= maxPlannerBlockedChainDepth {
		m.logMessage("dispatch_recovery", fmt.Sprintf("planner_blocked chain depth %d reached limit %d; surfacing to user", failure.PlannerBlockedChainDepth, maxPlannerBlockedChainDepth))

		return humanPlanExecutePlannerBlockedMessage(failure, implementationDispatchRecoveryDecision{
			Decision: "surface",
			Reason:   fmt.Sprintf("plan-execute retry chain depth %d exceeded; surfacing blocker to user", failure.PlannerBlockedChainDepth),
		}), true, nil
	}

	if answer, ok := humanPlanExecuteSourceSatisfiedCoverageBlockerMessage(failure); ok {
		m.logMessage("dispatch_recovery", "surfacing source-satisfied coverage mismatch without retry")

		return answer, true, nil
	}

	for _, spec := range []implementationDispatchMissingEditRetrySpec{
		{
			failureKind:    implementationDispatchFailureMissingRequestedTestEdit,
			obligationKind: planexecute.EditSetObligationKindTestEdit,
			allowedPaths: func(m *Model, failure implementationDispatchPlanExecuteFailure) []string {
				return m.implementationDispatchAllowedPathsForMissingRequestedTestEdit(failure)
			},
			appendAddendum:       appendMissingRequestedTestEditRecoveryAddendum,
			alreadyCarriedLog:    "surfacing missing test-edit rejection after typed test obligation was already carried",
			alreadyCarriedReason: "test-edit obligation was already explicit, but the retry still did not produce a valid test edit",
			noScopeLog:           "surfacing missing test-edit rejection without retry because no concrete test scope was available",
			noScopeReason:        "typed test-edit obligation is unsatisfied, but no concrete test path was available for retry",
			retryLog:             "retrying plan-execute after missing requested test edit with candidate scope: %s",
			retryInfo:            "Retrying plan-execute with required test edit obligation...",
			resultLogLabel:       "missing-test",
			chainLogPrefix:       "missing-test",
			executorFailedReason: "automatic missing-test retry submitted a plan, but the executor rejected an edit",
			finalReason:          "automatic missing-test retry did not produce a valid test edit plan",
		},
		{
			failureKind:    implementationDispatchFailureMissingRequestedImplementation,
			obligationKind: planexecute.EditSetObligationKindImplementationEdit,
			allowedPaths: func(m *Model, failure implementationDispatchPlanExecuteFailure) []string {
				return m.implementationDispatchAllowedPathsForMissingRequestedImplementationEdit(failure)
			},
			appendAddendum:       appendMissingRequestedImplementationEditRecoveryAddendum,
			alreadyCarriedLog:    "surfacing missing implementation-edit rejection after typed implementation obligation was already carried",
			alreadyCarriedReason: "implementation-edit obligation was already explicit, but the retry still did not produce a valid implementation edit",
			noScopeLog:           "surfacing missing implementation-edit rejection without retry because no concrete source scope was available",
			noScopeReason:        "typed implementation-edit obligation is unsatisfied, but no concrete source path was available for retry",
			retryLog:             "retrying plan-execute after missing requested implementation edit with candidate scope: %s",
			retryInfo:            "Retrying plan-execute with required implementation edit obligation...",
			resultLogLabel:       "missing-implementation",
			chainLogPrefix:       "missing-implementation",
			executorFailedReason: "automatic missing-implementation retry submitted a plan, but the executor rejected an edit",
			finalReason:          "automatic missing-implementation retry did not produce a valid implementation edit plan",
		},
	} {
		if implementationDispatchPlanExecuteRunFailureIs(failure.Result, spec.failureKind) {
			return m.handlePlanExecuteMissingEditRetry(ctx, failure, spec)
		}
	}

	if !m.planExecuteFailureHasPlanExecuteHandoff(failure) {
		allowedPaths := implementationDispatchAllowedPathsFromContextPack(failure.Options.Prompt, 8)
		if len(allowedPaths) > 0 {
			allowedPaths = uniqueImplementationDispatchPaths(allowedPaths)
			m.logMessage("dispatch_recovery", fmt.Sprintf("using context-pack candidate scope for planner-blocked retry: %s", strings.Join(allowedPaths, ", ")))
		} else {
			allowedPaths = m.resolveUnscopedImplementationAllowedPaths(ctx, failure.Prompt)
		}

		if len(allowedPaths) == 0 {
			m.logMessage("dispatch_recovery", "surfacing planner blocker without retry because plan-execute dispatch has no concrete edit scope")

			return humanPlanExecuteDispatchWithoutScopeMessage(failure), true, nil
		}

		retryOptions := failure.Options

		retryOptions.AllowedEditPaths = allowedPaths
		retryOptions.Prompt = appendDiscoveredCandidateScopeAddendum(failure.Options.Prompt, allowedPaths)
		m.logMessage("dispatch_recovery", fmt.Sprintf("retrying plan-execute with discovered candidate scope: %s", strings.Join(allowedPaths, ", ")))

		m.queueUIEvent(infoMsg{text: "Retrying plan-execute with discovered candidate scope..."})

		var retryOut, retryErr bytes.Buffer

		retryResult := planexecute.Run(m.cfg, retryOptions, m.newPlanExecutePromptRunner(ctx), nil, &retryOut, &retryErr)
		m.logMessage("dispatch_result", fmt.Sprintf("plan-execute discovered-scope retry finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
			retryResult.ExitCode, retryResult.SessionID, retryResult.ResumeStatePath, retryOut.Len(), retryErr.Len()))

		if retryResult.ExitCode == 0 {
			return strings.TrimSpace(retryOut.String()), true, nil
		}

		retryFailure := failure

		retryFailure.AllowedPaths = allowedPaths
		retryFailure.Options = retryOptions
		retryFailure.Result = retryResult
		retryFailure.Stdout = retryOut.String()
		retryFailure.Stderr = retryErr.String()

		if implementationDispatchShouldChainMissingEditRecovery(retryResult) {
			m.logMessage("dispatch_recovery", fmt.Sprintf("discovered-scope retry produced %s; chaining missing-edit recovery", strings.TrimSpace(retryResult.FailureKind)))

			retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

			return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
		}

		return humanPlanExecutePlannerBlockedMessage(retryFailure, implementationDispatchRecoveryDecision{
			Decision: "surface",
			Reason:   "automatic discovered-scope retry did not produce a plan",
		}), true, nil
	}

	if path, ok := planExecuteRequiredSourceReadBlockerPath(failure); ok {
		decision := implementationDispatchRecoveryDecision{
			Decision:      "retry",
			Reason:        "planner blocker names an unread required source path that a retry can satisfy with a source-body read",
			RetryAddendum: fmt.Sprintf("First read source for `%s` using a source-body route, then submit the plan when every required path has source evidence.", path),
		}
		retryPrompt := appendPlanExecutePlannerRecoveryAddendum(failure.Brief, failure, decision, "")
		retryOptions := failure.Options

		retryOptions.Prompt = retryPrompt

		m.logMessage("dispatch_recovery", fmt.Sprintf("planner_blocked decision=retry reason=required source read blocker for %s", path))

		m.queueUIEvent(infoMsg{text: "Retrying plan-execute after required source-read blocker..."})

		var retryOut, retryErr bytes.Buffer

		retryResult := planexecute.Run(m.cfg, retryOptions, m.newPlanExecutePromptRunner(ctx), nil, &retryOut, &retryErr)
		m.logMessage("dispatch_result", fmt.Sprintf("plan-execute source-read retry finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
			retryResult.ExitCode, retryResult.SessionID, retryResult.ResumeStatePath, retryOut.Len(), retryErr.Len()))

		if retryResult.ExitCode == 0 {
			return strings.TrimSpace(retryOut.String()), true, nil
		}

		retryFailure := failure

		retryFailure.Result = retryResult
		retryFailure.Options = retryOptions
		retryFailure.Stdout = retryOut.String()
		retryFailure.Stderr = retryErr.String()

		if implementationDispatchShouldChainMissingEditRecovery(retryResult) {
			m.logMessage("dispatch_recovery", fmt.Sprintf("source-read retry produced %s; chaining missing-edit recovery", strings.TrimSpace(retryResult.FailureKind)))

			retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

			return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
		}

		if planExecutePlannerBlockedCanRetryOnActiveModel(retryFailure) {
			m.logMessage("dispatch_recovery", "source-read retry hit repeated submit_plan gate; chaining to active-model fallback")

			retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

			return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
		}

		return humanPlanExecutePlannerBlockedMessage(retryFailure, implementationDispatchRecoveryDecision{
			Decision: "surface",
			Reason:   "automatic source-read retry did not produce a plan",
		}), true, nil
	}

	if planExecutePlannerBlockedCanRetryOnActiveModel(failure) {
		retryOptions := failure.Options

		retryOptions.UseActiveModel = true
		retryOptions.Prompt = appendPlanExecuteActiveModelFallbackAddendum(failure.Options.Prompt)

		m.logMessage("dispatch_recovery", "retrying plan-execute on active model after previous planner attempt failed to produce a valid submit_plan")

		m.queueUIEvent(infoMsg{text: "Retrying plan-execute on active model..."})

		var retryOut, retryErr bytes.Buffer

		retryResult := planexecute.Run(m.cfg, retryOptions, m.newPlanExecutePromptRunner(ctx), nil, &retryOut, &retryErr)
		m.logMessage("dispatch_result", fmt.Sprintf("plan-execute active-model retry finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
			retryResult.ExitCode, retryResult.SessionID, retryResult.ResumeStatePath, retryOut.Len(), retryErr.Len()))

		if retryResult.ExitCode == 0 {
			return strings.TrimSpace(retryOut.String()), true, nil
		}

		retryFailure := failure

		retryFailure.Options = retryOptions
		retryFailure.Result = retryResult
		retryFailure.Stdout = retryOut.String()
		retryFailure.Stderr = retryErr.String()

		if implementationDispatchShouldChainMissingEditRecovery(retryResult) {
			m.logMessage("dispatch_recovery", fmt.Sprintf("active-model retry produced %s; chaining missing-edit recovery", strings.TrimSpace(retryResult.FailureKind)))

			retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

			return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
		}

		return humanPlanExecutePlannerBlockedMessage(retryFailure, implementationDispatchRecoveryDecision{
			Decision: "surface",
			Reason:   "active-model fallback did not produce a plan",
		}), true, nil
	}

	decision, raw, err := m.decidePlanExecutePlannerBlocked(ctx, failure)
	if err != nil {
		m.logMessage("dispatch_recovery", "recovery decision failed; surfacing planner blocker: "+err.Error())

		return humanPlanExecutePlannerBlockedMessage(failure, implementationDispatchRecoveryDecision{
			Decision: "surface",
			Reason:   "recovery decision failed: " + err.Error(),
		}), true, nil
	}

	m.logMessage("dispatch_recovery", fmt.Sprintf("planner_blocked decision=%s reason=%s", strings.TrimSpace(decision.Decision), truncateImplementationDispatchText(strings.TrimSpace(decision.Reason), 240)))

	if util.EqualTrimFold(decision.Decision, "retry") {
		retryPrompt := appendPlanExecutePlannerRecoveryAddendum(failure.Brief, failure, decision, raw)
		retryOptions := failure.Options

		retryOptions.Prompt = retryPrompt

		m.logMessage("dispatch_recovery", "retrying plan-execute after planner_blocked decision")

		m.queueUIEvent(infoMsg{text: "Retrying plan-execute after planner recovery decision..."})

		var retryOut, retryErr bytes.Buffer

		retryResult := planexecute.Run(m.cfg, retryOptions, m.newPlanExecutePromptRunner(ctx), nil, &retryOut, &retryErr)
		m.logMessage("dispatch_result", fmt.Sprintf("plan-execute retry finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
			retryResult.ExitCode, retryResult.SessionID, retryResult.ResumeStatePath, retryOut.Len(), retryErr.Len()))

		if retryResult.ExitCode == 0 {
			return strings.TrimSpace(retryOut.String()), true, nil
		}

		retryFailure := failure

		retryFailure.Result = retryResult
		retryFailure.Options = retryOptions
		retryFailure.Stdout = retryOut.String()
		retryFailure.Stderr = retryErr.String()

		if implementationDispatchShouldChainMissingEditRecovery(retryResult) {
			m.logMessage("dispatch_recovery", fmt.Sprintf("planner-blocked retry produced %s; chaining missing-edit recovery", strings.TrimSpace(retryResult.FailureKind)))

			retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

			return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
		}

		if planExecutePlannerBlockedCanRetryOnActiveModel(retryFailure) {
			m.logMessage("dispatch_recovery", "planner-blocked retry hit repeated submit_plan gate; chaining to active-model fallback")

			retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

			return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
		}

		return humanPlanExecutePlannerBlockedMessage(retryFailure, implementationDispatchRecoveryDecision{
			Decision: "surface",
			Reason:   "automatic retry did not produce a plan",
			Message:  strings.TrimSpace(decision.Message),
		}), true, nil
	}

	return humanPlanExecutePlannerBlockedMessage(failure, decision), true, nil
}

func (m *Model) handlePlanExecuteMissingEditRetry(ctx context.Context, failure implementationDispatchPlanExecuteFailure, spec implementationDispatchMissingEditRetrySpec) (string, bool, error) {
	if implementationDispatchOptionsAlreadyCarriedEditSetObligation(failure.Options, spec.obligationKind) {
		m.logMessage("dispatch_recovery", spec.alreadyCarriedLog)

		return humanPlanExecutePlannerBlockedMessage(failure, implementationDispatchRecoveryDecision{
			Decision: "surface",
			Reason:   spec.alreadyCarriedReason,
		}), true, nil
	}

	allowedPaths := spec.allowedPaths(m, failure)
	if len(allowedPaths) == 0 {
		m.logMessage("dispatch_recovery", spec.noScopeLog)

		return humanPlanExecutePlannerBlockedMessage(failure, implementationDispatchRecoveryDecision{
			Decision: "surface",
			Reason:   spec.noScopeReason,
		}), true, nil
	}

	retryOptions := failure.Options

	retryOptions = implementationDispatchRetryOptionsWithCandidateScope(retryOptions, allowedPaths)
	retryOptions = implementationDispatchRetryOptionsWithEditSetObligation(retryOptions, spec.obligationKind)
	retryOptions.Prompt = spec.appendAddendum(failure.Options.Prompt, allowedPaths)
	m.logMessage("dispatch_recovery", fmt.Sprintf(spec.retryLog, strings.Join(allowedPaths, ", ")))

	m.queueUIEvent(infoMsg{text: spec.retryInfo})

	var retryOut, retryErr bytes.Buffer

	retryResult := planexecute.Run(m.cfg, retryOptions, m.newPlanExecutePromptRunner(ctx), nil, &retryOut, &retryErr)
	m.logMessage("dispatch_result", fmt.Sprintf("plan-execute %s retry finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
		spec.resultLogLabel, retryResult.ExitCode, retryResult.SessionID, retryResult.ResumeStatePath, retryOut.Len(), retryErr.Len()))

	if retryResult.ExitCode == 0 {
		return strings.TrimSpace(retryOut.String()), true, nil
	}

	retryFailure := failure

	retryFailure.AllowedPaths = allowedPaths
	retryFailure.Options = retryOptions
	retryFailure.Result = retryResult
	retryFailure.Stdout = retryOut.String()
	retryFailure.Stderr = retryErr.String()

	if implementationDispatchShouldChainMissingEditRecovery(retryResult) {
		m.logMessage("dispatch_recovery", fmt.Sprintf("%s retry produced %s; chaining missing-edit recovery", spec.chainLogPrefix, strings.TrimSpace(retryResult.FailureKind)))

		retryFailure.PlannerBlockedChainDepth = failure.PlannerBlockedChainDepth + 1

		return m.handlePlanExecutePlannerBlocked(ctx, retryFailure)
	}

	if implementationDispatchPlanExecuteRunFailureIs(retryResult, implementationDispatchFailureExecutorFailed) {
		return humanPlanExecutePlannerBlockedMessage(retryFailure, implementationDispatchRecoveryDecision{
			Decision: "surface",
			Reason:   spec.executorFailedReason,
		}), true, nil
	}

	return humanPlanExecutePlannerBlockedMessage(retryFailure, implementationDispatchRecoveryDecision{
		Decision: "surface",
		Reason:   spec.finalReason,
	}), true, nil
}

func planExecutePlannerBlockedCanRetryOnActiveModel(failure implementationDispatchPlanExecuteFailure) bool {
	if failure.Options.UseActiveModel {
		return false
	}

	if len(failure.RequiredPaths) == 0 {
		return false
	}

	switch implementationDispatchPlanExecuteRunFailureKind(failure.Result) {
	case implementationDispatchFailurePlannerBlocked, implementationDispatchFailurePlanArtifactMissing:
	default:
		return false
	}

	text := strings.ToLower(strings.Join([]string{
		failure.Result.FailureKind,
		failure.Result.FailureReason,
		failure.Result.PlannerAnswer,
		failure.Stdout,
		failure.Stderr,
	}, "\n"))
	if !strings.Contains(text, "submit_plan") {
		return false
	}

	if strings.Contains(text, "completion_gate_required_paths_remaining_repeated") {
		return true
	}

	if strings.Contains(text, "submit_plan rejected") &&
		(strings.Contains(text, "required edit path coverage mismatch") ||
			strings.Contains(text, "missing required edit path") ||
			strings.Contains(text, "invalid edit anchor")) {

		return true
	}

	if planExecuteFailureShowsRequiredSourcesSatisfied(failure) && strings.Contains(text, "submit_plan") {
		return true
	}

	if strings.Contains(text, "required edit path(s) remain unchanged") && strings.Contains(text, "submit_plan") {
		return true
	}

	if strings.Contains(text, "required edit paths not changed") && strings.Contains(text, "submit_plan") {
		return true
	}

	if strings.Contains(text, "required_edit_paths_remaining") && strings.Contains(text, "submit_plan") {
		return true
	}

	return strings.Contains(text, "missing completion evidence") &&
		(strings.Contains(text, "gate retry 3/3") ||
			strings.Contains(text, "repeated") ||
			strings.Contains(text, "last attempt before tools disable"))
}

func planExecuteFailureShowsRequiredSourcesSatisfied(failure implementationDispatchPlanExecuteFailure) bool {
	text := strings.ToLower(strings.Join([]string{
		failure.Result.FailureReason,
		failure.Result.PlannerAnswer,
		failure.Stdout,
		failure.Stderr,
	}, "\n"))

	return strings.Contains(text, "every required_edit_path has satisfied its source-body obligation")
}

func appendPlanExecuteActiveModelFallbackAddendum(prompt string) string {
	section := strings.Join([]string{
		"## Active Model Fallback",
		"",
		"The previous plan-execute planner attempt failed to produce a valid `submit_plan` artifact. It may have omitted required edit paths, used stale anchors, or emitted prose instead of a valid tool call. This retry is running on the active/default model with a fresh tool transcript. The required edit scope is still authoritative, but source-body evidence from the previous attempt is not executable evidence in this retry. Read or re-read any required path the gate reports as unread, then submit one `submit_plan` whose `edits[]` coverage includes every required edit path, answer `Decision: source-satisfied` with per-path current-state evidence, or answer `<<QUESTION>>:` with the exact path-specific blocker. Do not repeat a prose final answer.",
	}, "\n")
	if end := strings.Index(prompt, pathparse.BriefBodyEndMarker); end >= 0 {
		return strings.TrimSpace(prompt[:end]) + "\n\n" + section + "\n\n" + strings.TrimSpace(prompt[end:])
	}

	return strings.TrimSpace(prompt) + "\n\n" + section
}

func appendDiscoveredCandidateScopeAddendum(prompt string, allowedPaths []string) string {
	allowedPaths = uniqueImplementationDispatchPaths(allowedPaths)
	if len(allowedPaths) == 0 {
		return strings.TrimSpace(prompt)
	}

	section := discoveredCandidateScopeSection(allowedPaths)
	if end := strings.Index(prompt, pathparse.BriefBodyEndMarker); end >= 0 {
		return strings.TrimSpace(prompt[:end]) + "\n\n" + section + "\n\n" + strings.TrimSpace(prompt[end:])
	}

	return strings.TrimSpace(prompt) + "\n\n" + section
}

func appendMissingRequestedTestEditRecoveryAddendum(prompt string, allowedPaths []string) string {
	allowedPaths = uniqueImplementationDispatchPaths(allowedPaths)

	var b strings.Builder

	b.WriteString("## Missing Requested Test Edit Recovery\n\n")
	b.WriteString("The previous planner attempt submitted a production-only plan while a typed test-edit obligation remained unsatisfied. This retry is allowed exactly once. The first successful `submit_plan.edits` array must include at least one real edit to a test path and may include source edits still needed for the objective. The paths below are candidate scope, not mandatory coverage for every path; the typed obligation is at least one real test edit. If current source and test files already contain the requested final state, answer exactly `Decision: source-satisfied` and name both source and test evidence paths instead of submitting stale edits. Do not submit another production-only plan.\n")

	if len(allowedPaths) > 0 {
		b.WriteString("\nallowed_edit_paths:\n")

		for _, path := range allowedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}

	section := strings.TrimSpace(b.String())
	if end := strings.Index(prompt, pathparse.BriefBodyEndMarker); end >= 0 {
		return strings.TrimSpace(prompt[:end]) + "\n\n" + section + "\n\n" + strings.TrimSpace(prompt[end:])
	}

	return strings.TrimSpace(prompt) + "\n\n" + section
}

func appendMissingRequestedImplementationEditRecoveryAddendum(prompt string, allowedPaths []string) string {
	allowedPaths = uniqueImplementationDispatchPaths(allowedPaths)

	var b strings.Builder

	b.WriteString("## Missing Requested Implementation Edit Recovery\n\n")
	b.WriteString("The previous planner attempt submitted a test-only plan while a typed implementation-edit obligation remained unsatisfied. This retry is allowed exactly once. The first successful `submit_plan.edits` array must include at least one real non-test implementation edit and may include test edits still needed for the objective. The paths below are candidate scope, not mandatory coverage for every path; the typed obligation is at least one real non-test implementation edit. If current source and test files already contain the requested final state, answer exactly `Decision: source-satisfied` and name both implementation and test evidence paths instead of submitting stale edits. Do not submit another test-only plan.\n")

	if len(allowedPaths) > 0 {
		b.WriteString("\nallowed_edit_paths:\n")

		for _, path := range allowedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}

	section := strings.TrimSpace(b.String())
	if end := strings.Index(prompt, pathparse.BriefBodyEndMarker); end >= 0 {
		return strings.TrimSpace(prompt[:end]) + "\n\n" + section + "\n\n" + strings.TrimSpace(prompt[end:])
	}

	return strings.TrimSpace(prompt) + "\n\n" + section
}

func implementationDispatchRetryOptionsWithCandidateScope(opts planexecute.Options, candidatePaths []string) planexecute.Options {
	opts.RequiredEditPaths = uniqueImplementationDispatchPaths(opts.RequiredEditPaths)
	opts.AllowedEditPaths = uniqueImplementationDispatchPaths(append(append([]string{}, opts.AllowedEditPaths...), candidatePaths...))
	opts.ScopeContract = planexecute.NewPlanScopeContract([]string(opts.RequiredEditPaths), []string(opts.AllowedEditPaths), opts.EditSetObligations)

	return opts
}

func implementationDispatchRetryOptionsWithEditSetObligation(opts planexecute.Options, kind planexecute.EditSetObligationKind) planexecute.Options {
	opts.EditSetObligations = implementationDispatchEditSetObligationsWithSource(opts.EditSetObligations, kind, "tui_recovery")
	opts.ScopeContract = planexecute.NewPlanScopeContract([]string(opts.RequiredEditPaths), []string(opts.AllowedEditPaths), opts.EditSetObligations)

	return opts
}

func implementationDispatchEditSetObligationsWithSource(obligations planexecute.EditSetObligations, kind planexecute.EditSetObligationKind, source string) planexecute.EditSetObligations {
	obligations = obligations.Normalized()
	for i := range obligations {
		if obligations[i].Kind == kind {
			obligations[i].Source = strings.TrimSpace(source)
			return obligations.Normalized()
		}
	}

	return obligations.With(kind, source)
}

func discoveredCandidateScopeSection(allowedPaths []string) string {
	var b strings.Builder

	b.WriteString("## Discovered Candidate Edit Scope\n\n")
	b.WriteString("The first planner attempt stopped without `submit_plan` and the original TUI request had no explicit edit path. A bounded fallback discovery pass found these candidate source paths. Use them as the candidate edit scope; read an exact current anchor if needed, then submit a plan or name the concrete blocker.\n")
	b.WriteString("\nallowed_edit_paths:\n")

	for _, path := range allowedPaths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

func uniqueImplementationDispatchPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))

	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		out = append(out, path)
	}

	return out
}

func uniqueImplementationDispatchStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	return out
}

func (m *Model) resolveUnscopedImplementationAllowedPaths(ctx context.Context, prompt string) []string {
	if m == nil {
		return nil
	}

	wsCfg := m.cfg.Workspace
	if strings.TrimSpace(wsCfg.Root) == "" {
		wsCfg.Root = m.projectRoot()
	}

	if strings.TrimSpace(wsCfg.CWD) == "" {
		wsCfg.CWD = wsCfg.Root
	}

	wsCfg.EnableWrites = false

	manager := workspace.NewManager(wsCfg)

	for _, filePattern := range implementationDispatchFallbackFilePatterns(prompt) {
		result := manager.Execute(ctx, workspace.ToolRequest{
			Tool:            "search_files",
			Path:            ".",
			Pattern:         filePattern,
			NamedGap:        "locate candidate source path for unscoped TUI implementation dispatch",
			WhyNotAnswering: "need candidate edit scope before plan-execute retry",
			BudgetTokens:    1200,
		})
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		paths := implementationDispatchPathsFromSearchFilesOutput(result.Output, 4)
		if len(paths) > 0 {
			m.logMessage("dispatch_recovery", fmt.Sprintf("fallback file search %q found %s", filePattern, strings.Join(paths, ", ")))

			return paths
		}
	}

	pattern := implementationDispatchFallbackSearchPattern(prompt)
	if pattern == "" {
		return nil
	}

	result := manager.Execute(ctx, workspace.ToolRequest{
		Tool:              "search_content",
		Path:              ".",
		Pattern:           pattern,
		NamedGap:          "locate candidate source path for unscoped TUI implementation dispatch",
		WhyNotAnswering:   "need candidate edit scope before plan-execute retry",
		BudgetTokens:      1600,
		FreshnessPolicy:   "scoped",
		FreshnessMaxFiles: 64,
	})
	if strings.TrimSpace(result.Error) != "" {
		m.logMessage("dispatch_recovery", "fallback scope search failed: "+truncateImplementationDispatchText(result.Error, 240))

		return nil
	}

	paths := implementationDispatchRankSearchPaths(prompt, result.Output, 4)
	if len(paths) > 0 {
		m.logMessage("dispatch_recovery", fmt.Sprintf("fallback scope search %q found %s", pattern, strings.Join(paths, ", ")))
	}

	return paths
}

func implementationDispatchFallbackFilePatterns(prompt string) []string {
	raw := strings.Split(implementationDispatchFallbackSearchPattern(prompt), "|")

	var terms []string

	for _, term := range raw {
		term = strings.TrimSpace(term)
		if term != "" {
			terms = append(terms, term)
		}
	}

	var patterns []string

	for width := 3; width >= 2; width-- {
		for i := 0; i+width <= len(terms); i++ {
			patterns = append(patterns, strings.Join(terms[i:i+width], "_"))
		}
	}

	return uniqueImplementationDispatchStrings(patterns)
}

func implementationDispatchPathsFromSearchFilesOutput(output string, max int) []string {
	if max <= 0 {
		max = 4
	}

	var paths []string

	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}

		path := filepath.ToSlash(fields[len(fields)-1])
		if !implementationDispatchPathLooksEditableSource(path) || !strings.Contains(path, "/") {
			continue
		}

		paths = append(paths, path)
		if len(paths) >= max {
			break
		}
	}

	return uniqueImplementationDispatchPaths(paths)
}

func implementationDispatchFallbackSearchPattern(prompt string) string {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	if lower == "" {
		return ""
	}

	replacer := strings.NewReplacer("'", " ", "\"", " ", "`", " ", "’", " ", "-", " ", "_", " ", "/", " ")

	lower = replacer.Replace(lower)

	words := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	stop := map[string]struct{}{
		"actual": {}, "against": {}, "bounded": {}, "brief": {}, "completion": {}, "continue": {}, "continuing": {}, "design": {}, "docs": {}, "durable": {}, "existing": {}, "focus": {}, "from": {}, "implement": {}, "implementation": {}, "left": {}, "minimal": {}, "model": {}, "must": {}, "only": {}, "partial": {}, "path": {}, "please": {}, "prd": {}, "proving": {}, "replace": {}, "research": {}, "right": {}, "that": {}, "this": {}, "through": {}, "tdd": {}, "tests": {}, "topic": {}, "with": {}, "work": {}, "workflow": {}, "you": {}, "can": {}, "top": {}, "alias": {},
	}

	var terms []string

	seen := map[string]struct{}{}

	for _, word := range words {
		word = strings.TrimSpace(word)
		if len(word) < 4 {
			continue
		}

		if _, skip := stop[word]; skip {
			continue
		}

		if _, ok := seen[word]; ok {
			continue
		}

		seen[word] = struct{}{}
		terms = append(terms, word)

		if len(terms) >= 4 {
			break
		}
	}

	return strings.Join(terms, "|")
}

func implementationDispatchAllowedPathsFromContextPack(prompt string, max int) []string {
	if max <= 0 {
		max = 8
	}

	inCandidateFiles := false

	var paths []string

	for _, line := range strings.Split(prompt, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "### Candidate Files") || trimmed == "candidate_files:":
			inCandidateFiles = true
			continue
		case inCandidateFiles && isImplementationDispatchContextPackSectionBoundary(trimmed):
			inCandidateFiles = false
		case inCandidateFiles && strings.HasPrefix(trimmed, "### "):
			inCandidateFiles = false
		case inCandidateFiles && strings.HasPrefix(trimmed, "## "):
			inCandidateFiles = false
		}

		if !inCandidateFiles || !strings.HasPrefix(trimmed, "- ") {
			continue
		}

		path, role, ok := implementationDispatchContextPackCandidateLine(trimmed)
		if !ok {
			continue
		}

		role = strings.ToLower(role)
		if !strings.Contains(role, "editable") && !strings.Contains(role, "test") {
			continue
		}

		if !implementationDispatchPathLooksEditableSource(path) {
			continue
		}

		paths = append(paths, path)
		if len(paths) >= max {
			break
		}
	}

	return uniqueImplementationDispatchPaths(paths)
}

func implementationDispatchTestPathsFromContextPack(prompt string, max int) []string {
	if max <= 0 {
		max = 8
	}

	inCandidateTests := false

	var paths []string

	for _, line := range strings.Split(prompt, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "### Candidate Tests") || trimmed == "tests:":
			inCandidateTests = true
			continue
		case inCandidateTests && isImplementationDispatchContextPackSectionBoundary(trimmed):
			inCandidateTests = false
		case inCandidateTests && strings.HasPrefix(trimmed, "### "):
			inCandidateTests = false
		case inCandidateTests && strings.HasPrefix(trimmed, "## "):
			inCandidateTests = false
		}

		if !inCandidateTests || !strings.HasPrefix(trimmed, "- ") {
			continue
		}

		path, ok := implementationDispatchContextPackTestLinePath(trimmed)
		if !ok || !implementationDispatchPathLooksEditableSource(path) {
			continue
		}

		paths = append(paths, path)
		if len(paths) >= max {
			break
		}
	}

	return uniqueImplementationDispatchPaths(paths)
}

func implementationDispatchContextPackTestLinePath(line string) (string, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
	if line == "" {
		return "", false
	}

	// Support old backtick-quoted format: `command` - path
	if idx := strings.Index(line, "`"); idx >= 0 {
		if idx2 := strings.LastIndex(line, " - "); idx2 >= 0 {
			line = strings.TrimSpace(line[idx2+3:])
		}

		if fields := strings.Fields(line); len(fields) > 0 {
			line = fields[len(fields)-1]
		}

		path := filepath.ToSlash(strings.Trim(strings.TrimSpace(line), "`"))
		if path == "" || strings.Contains(path, " ") {
			return "", false
		}

		return path, true
	}

	// New compact format: "path: detail" or "path"
	parts := strings.SplitN(line, ": ", 2)
	path := filepath.ToSlash(strings.TrimSpace(parts[0]))

	if path == "" || strings.Contains(path, " ") {
		return "", false
	}

	return path, true
}

func implementationDispatchContextPackCandidateLine(line string) (string, string, bool) {
	line = strings.TrimPrefix(strings.TrimSpace(line), "- ")
	if line == "" {
		return "", "", false
	}

	// Support old backtick-quoted format: `path` (role, reason)
	if firstTick := strings.Index(line, "`"); firstTick >= 0 {
		rest := line[firstTick+1:]
		secondTick := strings.Index(rest, "`")

		if secondTick < 0 {
			return "", "", false
		}

		path := filepath.ToSlash(strings.TrimSpace(rest[:secondTick]))
		if path == "" {
			return "", "", false
		}

		afterPath := rest[secondTick+1:]
		open := strings.Index(afterPath, "(")
		close := strings.Index(afterPath, ")")

		if open < 0 || close < 0 || close <= open {
			return path, "", true
		}

		role := strings.TrimSpace(afterPath[open+1 : close])
		if comma := strings.Index(role, ","); comma >= 0 {
			role = strings.TrimSpace(role[:comma])
		}

		return path, role, true
	}

	// New compact format: "path: role" or "path: role (reason)" or "path"
	parts := strings.SplitN(line, ": ", 2)
	path := filepath.ToSlash(strings.TrimSpace(parts[0]))

	if path == "" {
		return "", "", false
	}

	if len(parts) < 2 {
		return path, "", true
	}

	detail := strings.TrimSpace(parts[1])
	// Extract role from "role" or "role (reason)"
	role := detail
	if open := strings.Index(detail, "("); open >= 0 {
		if close := strings.Index(detail[open:], ")"); close >= 0 {
			role = strings.TrimSpace(detail[:open])
		}
	}

	if role == "" {
		role = detail
	}

	// Take first comma-separated token as role (like old behavior)
	if comma := strings.Index(role, ","); comma >= 0 {
		role = strings.TrimSpace(role[:comma])
	}

	return path, role, true
}

// isImplementationDispatchContextPackSectionBoundary returns true when line is a
// compact-projection section header (like "candidate_symbols:", "tests:",
// "context_gaps:", "omitted:", or "evidence_note:"). These mark the end of the
// current candidate section. Also matches old-format "### " and "## " headings.
// Line must not be prefixed with "- ".
func isImplementationDispatchContextPackSectionBoundary(line string) bool {
	if strings.HasPrefix(line, "- ") {
		return false
	}

	if strings.HasPrefix(line, "### ") || strings.HasPrefix(line, "## ") {
		return true
	}

	// Known section headers in the compact projection format.
	switch line {
	case "candidate_symbols:", "tests:", "context_gaps:", "omitted:":
		return true
	}

	// evidence_note: may have trailing content.
	if strings.HasPrefix(line, "evidence_note:") {
		return true
	}

	return false
}

func (m *Model) implementationDispatchAllowedPathsFromPreloadedContextPack(prompt string, contextPackSection string) []string {
	if strings.TrimSpace(contextPackSection) == "" {
		return nil
	}

	paths := implementationDispatchAllowedPathsFromContextPack(contextPackSection, 8)

	paths = append(paths, implementationDispatchTestPathsFromContextPack(contextPackSection, 8)...)
	paths = implementationDispatchFilterContextPackPathsForPrompt(prompt, paths)

	return uniqueImplementationDispatchPaths(paths)
}

func implementationDispatchFilterContextPackPathsForPrompt(prompt string, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	promptTokens := implementationDispatchScopeTokens(prompt, true)
	if len(promptTokens) == 0 {
		return uniqueImplementationDispatchPaths(paths)
	}

	var filtered []string

	for _, path := range uniqueImplementationDispatchPaths(paths) {
		pathTokens := implementationDispatchScopeTokens(path, false)
		if implementationDispatchPathTokenOverlap(promptTokens, pathTokens) > 0 {
			filtered = append(filtered, path)
		}
	}

	return uniqueImplementationDispatchPaths(filtered)
}

func implementationDispatchPathTokenOverlap(promptTokens map[string]struct{}, pathTokens map[string]struct{}) int {
	overlap := 0

	for token := range pathTokens {
		if _, ok := promptTokens[token]; ok {
			overlap++
		}
	}

	return overlap
}

func implementationDispatchScopeTokens(text string, fromPrompt bool) map[string]struct{} {
	tokens := map[string]struct{}{}

	for _, token := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		token = strings.Trim(token, "_-")
		if len(token) < 4 {
			continue
		}

		if implementationDispatchScopeTokenIsWeak(token, fromPrompt) {
			continue
		}

		tokens[token] = struct{}{}
	}

	return tokens
}

func implementationDispatchScopeTokenIsWeak(token string, fromPrompt bool) bool {
	switch token {
	case "candidate", "change", "changes", "code", "docs", "edit", "edits", "existing", "file", "files", "focus", "from", "implementation", "implement", "internal", "make", "minimal", "only", "path", "paths", "please", "product", "requested", "source", "test", "tests", "that", "this", "work", "workflow":
		return true
	}

	if fromPrompt {
		switch token {
		case "adr", "brief", "completion", "continuing", "design", "prd", "research", "tdd", "through":
			return true
		}
	}

	return false
}

func humanPlanExecuteSourceSatisfiedCoverageBlockerMessage(failure implementationDispatchPlanExecuteFailure) (string, bool) {
	answer := strings.TrimSpace(failure.Result.PlannerAnswer)
	requiredCoveragePaths := implementationDispatchPlanExecuteFailureRequiredCoveragePaths(failure)

	if len(requiredCoveragePaths) == 0 {
		return "", false
	}

	if agent.PlanThenExecuteSourceSatisfiedAnswerCoversPaths(answer, requiredCoveragePaths) {
		return "", false
	}

	missing := implementationDispatchPathsMissingFromAnswer(requiredCoveragePaths, answer)
	if len(missing) == 0 {
		return "", false
	}

	evidencePaths := implementationDispatchCoveredSourceSatisfiedPaths(requiredCoveragePaths, missing)
	if len(evidencePaths) == 0 {
		return "", false
	}

	var b strings.Builder

	b.WriteString("<<QUESTION>>: Plan-execute reported `Decision: source-satisfied`, but the answer did not cover every required edit path.")
	b.WriteString("\n\nThis is a terminal evidence mismatch, not a retryable planner failure. The TUI will not stack another recovery prompt.")
	b.WriteString("\n\nCovered source-satisfied evidence paths:")

	for _, path := range evidencePaths {
		b.WriteString("\n- ")
		b.WriteString(path)
	}

	b.WriteString("\n\nMissing required edit paths:")

	for _, path := range missing {
		b.WriteString("\n- ")
		b.WriteString(path)
	}

	if summary := strings.TrimSpace(failure.Result.FailureReason); summary != "" {
		b.WriteString("\n\nPlanner/orchestrator reason:\n")
		b.WriteString(summary)
	}

	return strings.TrimSpace(b.String()), true
}

func implementationDispatchPlanExecuteFailureRequiredCoveragePaths(failure implementationDispatchPlanExecuteFailure) []string {
	var paths []string

	paths = append(paths, failure.RequiredPaths...)
	paths = append(paths, failure.Options.RequiredEditPaths...)

	return uniqueImplementationDispatchPaths(paths)
}

func implementationDispatchPathsMissingFromAnswer(paths []string, answer string) []string {
	return agent.PlanThenExecuteSourceSatisfiedMissingCoveragePaths(answer, uniqueImplementationDispatchPaths(paths))
}

func implementationDispatchCoveredSourceSatisfiedPaths(requiredPaths []string, missingPaths []string) []string {
	missing := map[string]struct{}{}
	for _, path := range uniqueImplementationDispatchPaths(missingPaths) {
		missing[path] = struct{}{}
	}

	var covered []string

	for _, path := range uniqueImplementationDispatchPaths(requiredPaths) {
		if _, ok := missing[path]; !ok {
			covered = append(covered, path)
		}
	}

	return uniqueImplementationDispatchPaths(covered)
}

func implementationDispatchRankSearchPaths(prompt string, output string, max int) []string {
	if max <= 0 {
		max = 4
	}

	terms := strings.Split(implementationDispatchFallbackSearchPattern(prompt), "|")
	uiObjective := strings.Contains(strings.ToLower(prompt), "tui") ||
		strings.Contains(strings.ToLower(prompt), "ui") ||
		strings.Contains(strings.ToLower(prompt), "banner") ||
		strings.Contains(strings.ToLower(prompt), "header") ||
		strings.Contains(strings.ToLower(prompt), "top-right") ||
		strings.Contains(strings.ToLower(prompt), "top right") ||
		strings.Contains(strings.ToLower(prompt), "view") ||
		strings.Contains(strings.ToLower(prompt), "render")

	type scored struct {
		path  string
		score int
		order int
	}

	scores := map[string]*scored{}
	order := 0

	for _, line := range strings.Split(output, "\n") {
		path, text, ok := implementationDispatchSearchLinePathText(line)
		if !ok {
			continue
		}

		entry := scores[path]
		if entry == nil {
			entry = &scored{path: path, order: order}
			scores[path] = entry
			order++
		}

		entry.score += implementationDispatchSearchPathScore(path, text, terms, uiObjective)
	}

	ranked := make([]scored, 0, len(scores))
	for _, entry := range scores {
		ranked = append(ranked, *entry)
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}

		return ranked[i].order < ranked[j].order
	})

	var out []string

	for _, entry := range ranked {
		if entry.score <= -50 {
			continue
		}

		out = append(out, entry.path)
		if len(out) >= max {
			break
		}
	}

	return out
}

func implementationDispatchSearchLinePathText(line string) (string, string, bool) {
	line = strings.TrimSpace(line)

	firstColon := strings.Index(line, ":")

	if firstColon < 0 {
		return "", "", false
	}

	fields := strings.Fields(strings.TrimSpace(line[:firstColon]))
	if len(fields) == 0 {
		return "", "", false
	}

	path := filepath.ToSlash(fields[len(fields)-1])
	if !implementationDispatchPathLooksEditableSource(path) || !strings.Contains(path, "/") {
		return "", "", false
	}

	rest := line[firstColon+1:]
	text := ""

	if secondColon := strings.Index(rest, ":"); secondColon >= 0 {
		text = strings.TrimSpace(rest[secondColon+1:])
	}

	return path, text, true
}

func implementationDispatchPathLooksEditableSource(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if lower == "" {
		return false
	}

	for _, suffix := range []string{
		".go", ".rs", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".java", ".kt", ".swift", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs",
		".rb", ".php", ".sh", ".bash", ".zsh", ".fish", ".lua", ".sql",
		".html", ".css", ".scss", ".sass", ".vue", ".svelte", ".templ",
		".json", ".yaml", ".yml", ".toml", ".xml", ".md", ".mdx",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	return false
}

func implementationDispatchSearchPathScore(path string, text string, terms []string, uiObjective bool) int {
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	lowerText := strings.ToLower(text)
	score := 1

	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}

		if strings.Contains(lowerPath, term) {
			score += 10
		}

		if strings.Contains(lowerText, term) {
			score += 12
		}
	}

	if strings.HasSuffix(lowerPath, "_test.go") {
		score -= 100
	}

	if strings.HasPrefix(strings.TrimSpace(lowerText), "//") {
		score -= 8
	}

	if strings.Contains(lowerPath, "/agent/") || strings.Contains(lowerPath, "/workspace/") || strings.Contains(lowerPath, "/eval/") {
		score -= 30
	}

	if uiObjective {
		if strings.HasPrefix(lowerPath, "internal/surfaces/tui/app/") {
			score += 45
		}

		if strings.Contains(lowerPath, "render") || strings.Contains(lowerPath, "view") {
			score += 70
		}

		if strings.Contains(lowerPath, "model_state") {
			score += 5
		}

		if strings.Contains(lowerPath, "frontier/") || strings.Contains(lowerPath, "comprehension/") {
			score -= 25
		}
	}

	return score
}

func (m *Model) planExecuteFailureHasPlanExecuteHandoff(failure implementationDispatchPlanExecuteFailure) bool {
	if len(failure.RequiredPaths) > 0 || len(failure.AllowedPaths) > 0 {
		return true
	}

	if m == nil {
		return false
	}

	return implementationDispatchHasPlanExecuteHandoff(failure.Prompt, failure.Plan, m.cfg)
}

func (m *Model) decidePlanExecutePlannerBlocked(ctx context.Context, failure implementationDispatchPlanExecuteFailure) (implementationDispatchRecoveryDecision, string, error) {
	if m == nil || m.client == nil {
		return implementationDispatchRecoveryDecision{}, "", fmt.Errorf("model client unavailable")
	}

	opts := m.chatOptions()

	opts.Tools = nil
	opts.ToolChoice = ""
	opts.MaxTokens = 1024

	thinkingOff := false

	opts.EnableThinking = &thinkingOff
	opts.PreserveThinking = false
	opts.ChatTemplateKwargs = true

	prompt := planExecutePlannerBlockedDecisionPrompt(failure)

	raw, err := m.client.CompleteChat(ctx, []llama.ChatMessage{
		{Role: "system", Content: "You are a recovery controller for a local coding-agent harness. Return only the requested JSON object."},
		{Role: "user", Content: prompt},
	}, opts)
	if err != nil {
		return implementationDispatchRecoveryDecision{}, raw, err
	}

	decision, ok := parseImplementationDispatchRecoveryDecision(raw)
	if !ok {
		return implementationDispatchRecoveryDecision{}, raw, fmt.Errorf("recovery decision was not valid JSON")
	}

	decision.Decision = strings.ToLower(strings.TrimSpace(decision.Decision))
	if decision.Decision != "retry" && decision.Decision != "surface" {
		return implementationDispatchRecoveryDecision{}, raw, fmt.Errorf("unsupported recovery decision %q", decision.Decision)
	}

	return decision, raw, nil
}

func planExecutePlannerBlockedDecisionPrompt(failure implementationDispatchPlanExecuteFailure) string {
	var b strings.Builder

	b.WriteString("# Plan-Execute Planner Blocked Recovery\n\n")
	b.WriteString("The in-process plan-execute planner stopped before a successful `submit_plan`. Decide whether one automatic retry is worthwhile or whether this should be surfaced to the human.\n\n")
	b.WriteString("Return JSON only with this schema:\n")
	b.WriteString(`{"decision":"retry|surface","reason":"short reason","message":"human-facing message when surfacing","retry_addendum":"optional short observation; protocol instructions are ignored"}`)
	b.WriteString("\n\nRetry only when the next run can materially change conditions. Surface when the failure is missing human information, missing body evidence, repeated no-tool/no-progress behavior, or a contract failure that a same-model retry is likely to repeat. The harness owns the retry contract; do not invent submit_plan schemas or tool-call instructions in retry_addendum.\n\n")
	b.WriteString("## Objective\n\n")
	b.WriteString(strings.TrimSpace(failure.Prompt))
	b.WriteString("\n\n## Required Edit Paths\n\n")

	for _, path := range failure.RequiredPaths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteString("\n")
	}

	if len(failure.AllowedPaths) > 0 {
		b.WriteString("\n## Allowed Edit Paths\n\n")

		for _, path := range failure.AllowedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}

	if reason := strings.TrimSpace(failure.Result.FailureReason); reason != "" {
		b.WriteString("\n## Planner Blocker Summary\n\n")
		b.WriteString(reason)
		b.WriteString("\n")
	}

	if answer := strings.TrimSpace(failure.Result.PlannerAnswer); answer != "" {
		b.WriteString("\n## Planner Blocker Answer\n\n")
		b.WriteString(truncateImplementationDispatchText(answer, 6000))
		b.WriteString("\n")
	}

	if stderr := strings.TrimSpace(failure.Stderr); stderr != "" {
		b.WriteString("\n## Orchestrator Error\n\n")
		b.WriteString(truncateImplementationDispatchText(stderr, 2000))
		b.WriteString("\n")
	}

	if stdout := strings.TrimSpace(failure.Stdout); stdout != "" && !strings.Contains(stdout, failure.Result.PlannerAnswer) {
		b.WriteString("\n## Orchestrator Output\n\n")
		b.WriteString(truncateImplementationDispatchText(stdout, 3000))
		b.WriteString("\n")
	}

	return b.String()
}

func parseImplementationDispatchRecoveryDecision(raw string) (implementationDispatchRecoveryDecision, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return implementationDispatchRecoveryDecision{}, false
	}

	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "```json"))
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "```"))
		raw = strings.TrimSpace(strings.TrimSuffix(raw, "```"))
	}

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")

	if start < 0 || end < start {
		return implementationDispatchRecoveryDecision{}, false
	}

	var decision implementationDispatchRecoveryDecision

	if err := json.Unmarshal([]byte(raw[start:end+1]), &decision); err != nil {
		return implementationDispatchRecoveryDecision{}, false
	}

	return decision, true
}

func appendPlanExecutePlannerRecoveryAddendum(brief string, failure implementationDispatchPlanExecuteFailure, decision implementationDispatchRecoveryDecision, rawDecision string) string {
	var b strings.Builder

	b.WriteString(strings.TrimSpace(brief))
	b.WriteString("\n\n## Previous Planner Blocker Recovery\n\n")
	b.WriteString("The previous planner attempt stopped before `submit_plan`. This retry is allowed exactly once. Do not repeat a prose final answer.\n\n")

	if reason := strings.TrimSpace(decision.Reason); reason != "" {
		b.WriteString("- Recovery decision reason: ")
		b.WriteString(reason)
		b.WriteString("\n")
	}

	b.WriteString("- Retry instruction: ")
	b.WriteString(planExecutePlannerRecoveryRetryInstruction(failure))
	b.WriteString("\n")

	if addendum := planExecutePlannerRecoveryObservation(decision.RetryAddendum); addendum != "" {
		b.WriteString("- Recovery controller observation: ")
		b.WriteString(addendum)
		b.WriteString("\n")
	}

	if summary := strings.TrimSpace(failure.Result.FailureReason); summary != "" {
		b.WriteString("- Previous blocker: ")
		b.WriteString(summary)
		b.WriteString("\n")
	}

	if strings.TrimSpace(rawDecision) != "" && strings.TrimSpace(decision.RetryAddendum) == "" {
		b.WriteString("- Recovery controller returned a retry decision without an observation.\n")
	}

	return b.String()
}

func planExecutePlannerRecoveryRetryInstruction(failure implementationDispatchPlanExecuteFailure) string {
	var b strings.Builder

	paths := uniqueImplementationDispatchPaths(append(append([]string{}, failure.RequiredPaths...), failure.Options.RequiredEditPaths...))

	if refresh, ok := planExecuteRequiredSourceReadBlocker(failure); ok {
		b.WriteString(recovery.PlanExecuteSourceRefreshRetryInstruction(refresh, paths))
	} else {
		switch {
		case len(paths) > 0:
			fmt.Fprintf(&b, "After this retry has satisfied source-body obligations and runtime-required git history evidence for every existing required path listed above (%s), produce one valid `submit_plan` call whose edits include at least one real edit for each required path, with no outside paths. Do not treat a source read or history read as plan coverage.", strings.Join(paths, ", "))
		case len(failure.AllowedPaths) > 0 || len(failure.Options.AllowedEditPaths) > 0:
			allowed := uniqueImplementationDispatchPaths(append(append([]string{}, failure.AllowedPaths...), failure.Options.AllowedEditPaths...))
			fmt.Fprintf(&b, "Produce one valid `submit_plan` call using the allowed candidate paths listed above (%s) as the edit allowlist; do not treat every candidate path as mandatory coverage.", strings.Join(allowed, ", "))
		default:
			b.WriteString("Produce one valid `submit_plan` call grounded in the objective and target references.")
		}
	}

	b.WriteString(" Each edit entry must include `path`, exactly one edit shape (`start_line`+`end_line`+`content` for non-trivial existing-source edits, `old_text`+`new_text` for tiny exact snippets, or `replace_all:true`+`new_text` for missing/new files only), and one-sentence `purpose`. Existing source/test files must use bounded anchored edits. If the current files already contain the requested final state, answer `Decision: source-satisfied` with current-state evidence for every relevant path instead of submitting stale edits. If the plan cannot be produced from the available evidence, answer `<<QUESTION>>:` with the exact path-specific blocker.")

	return b.String()
}

func planExecuteRequiredSourceReadBlockerPath(failure implementationDispatchPlanExecuteFailure) (string, bool) {
	refresh, ok := planExecuteRequiredSourceReadBlocker(failure)
	if !ok {
		return "", false
	}

	return refresh.Path, true
}

func planExecuteRequiredSourceReadBlocker(failure implementationDispatchPlanExecuteFailure) (recovery.PlanExecutePlannerBlockedSourceRefresh, bool) {
	if len(failure.RequiredPaths) == 0 && len(failure.Options.RequiredEditPaths) == 0 {
		return recovery.PlanExecutePlannerBlockedSourceRefresh{}, false
	}

	text := strings.Join([]string{
		failure.Result.FailureReason,
		failure.Result.PlannerAnswer,
		failure.Stdout,
		failure.Stderr,
	}, "\n")
	paths := uniqueImplementationDispatchPaths(append(append([]string{}, failure.RequiredPaths...), failure.Options.RequiredEditPaths...))
	nextAction := ""
	if failure.Result.Completion != nil {
		nextAction = failure.Result.Completion.NextAction
	}
	return recovery.PlanExecutePlannerBlockedRetryRefreshResult(failure.Result.FailureKind, text, paths, nextAction)
}

func planExecutePlannerRecoveryObservation(addendum string) string {
	addendum = strings.TrimSpace(addendum)
	if addendum == "" || implementationDispatchRecoveryMessageIsInternal(addendum) || planExecutePlannerRecoveryAddendumContradictsContract(addendum) {
		return ""
	}

	return truncateImplementationDispatchText(addendum, 600)
}

func planExecutePlannerRecoveryAddendumContradictsContract(addendum string) bool {
	lower := strings.ToLower(strings.TrimSpace(addendum))
	if lower == "" {
		return false
	}

	contractTerms := []string{
		"decision: source-satisfied",
		"`decision: source-satisfied`",
		"source-satisfied",
	}
	entryTerms := []string{
		"each entry",
		"each edit",
		"every entry",
		"every edit",
		"must have",
		"must contain",
		"submit_plan",
	}
	hasContractTerm := false

	for _, term := range contractTerms {
		if strings.Contains(lower, term) {
			hasContractTerm = true
			break
		}
	}

	if !hasContractTerm {
		return false
	}

	for _, term := range entryTerms {
		if strings.Contains(lower, term) {
			return true
		}
	}

	return false
}

func (m *Model) implementationDispatchAllowedPathsForMissingRequestedTestEdit(failure implementationDispatchPlanExecuteFailure) []string {
	var paths []string

	submittedPaths := m.implementationDispatchSubmittedPlanPaths(failure)

	paths = append(paths, submittedPaths...)
	paths = append(paths, failure.RequiredPaths...)
	paths = append(paths, failure.Options.RequiredEditPaths...)
	paths = append(paths, failure.AllowedPaths...)
	paths = append(paths, failure.Options.AllowedEditPaths...)
	paths = append(paths, implementationDispatchAllowedPathsFromContextPack(failure.Options.Prompt, 8)...)
	paths = append(paths, implementationDispatchTestPathsFromContextPack(failure.Options.Prompt, 8)...)

	return uniqueImplementationDispatchPaths(paths)
}

func (m *Model) implementationDispatchAllowedPathsForMissingRequestedImplementationEdit(failure implementationDispatchPlanExecuteFailure) []string {
	submittedPaths := m.implementationDispatchSubmittedPlanPaths(failure)

	var paths []string

	paths = append(paths, submittedPaths...)

	for _, path := range submittedPaths {
		if sourcePath := m.implementationDispatchSiblingSourceForTestPath(path); sourcePath != "" {
			paths = append(paths, sourcePath)
		}
	}

	paths = append(paths, failure.RequiredPaths...)
	paths = append(paths, failure.Options.RequiredEditPaths...)
	paths = append(paths, failure.AllowedPaths...)
	paths = append(paths, failure.Options.AllowedEditPaths...)
	paths = append(paths, implementationDispatchAllowedPathsFromContextPack(failure.Options.Prompt, 8)...)

	return uniqueImplementationDispatchPaths(paths)
}

func (m *Model) implementationDispatchSiblingSourceForTestPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || !strings.HasSuffix(strings.ToLower(path), "_test.go") {
		return ""
	}

	if m == nil {
		return ""
	}

	sourcePath := strings.TrimSuffix(path, "_test.go") + ".go"
	root := strings.TrimSpace(m.projectRoot())

	if root == "" {
		return ""
	}

	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(sourcePath)))
	if err != nil || info.IsDir() {
		return ""
	}

	return sourcePath
}

func (m *Model) implementationDispatchSubmittedPlanPaths(failure implementationDispatchPlanExecuteFailure) []string {
	if m == nil {
		return nil
	}

	root := implementationDispatchWorkspaceRoot(m.cfg)
	if strings.TrimSpace(root) == "" {
		return nil
	}

	sessionID := strings.TrimSpace(failure.Result.SessionID)
	if sessionID == "" {
		base := strings.TrimSpace(filepath.Base(filepath.ToSlash(failure.Result.ResumeStatePath)))

		sessionID = strings.TrimSuffix(base, ".json")
	}

	if sessionID == "" {
		return nil
	}

	state, _, found, err := planexecute.TryLoadProgressState(root, sessionID)
	if err != nil || !found {
		return nil
	}

	var paths []string

	paths = append(paths, state.ChangedPaths...)

	for _, edit := range state.Edits {
		paths = append(paths, edit.Path)
	}

	return uniqueImplementationDispatchPaths(paths)
}

func humanPlanExecutePlannerBlockedMessage(failure implementationDispatchPlanExecuteFailure, decision implementationDispatchRecoveryDecision) string {
	if msg := strings.TrimSpace(decision.Message); msg != "" {
		if !implementationDispatchRecoveryMessageIsInternal(msg) && !planExecuteRecoveryMessageContradictsSourceSatisfied(failure, msg) {
			return msg
		}
	}

	var b strings.Builder

	b.WriteString("<<QUESTION>>: Plan-execute could not produce a plan artifact.")

	if reason := strings.TrimSpace(decision.Reason); reason != "" {
		b.WriteString("\n\nDecision: ")
		b.WriteString(reason)
	}

	blockerSummary := humanPlanExecutePlannerBlockerSummary(failure)
	if blockerSummary != "" {
		b.WriteString("\n\nPlanner blocker: ")
		b.WriteString(blockerSummary)
	}

	if len(failure.RequiredPaths) > 0 {
		b.WriteString("\n\nRequired edit paths still need a plan:")

		for _, path := range failure.RequiredPaths {
			b.WriteString("\n- ")
			b.WriteString(path)
		}
	}

	if answer := strings.TrimSpace(failure.Result.PlannerAnswer); answer != "" && !planExecutePlannerAnswerIsInternalCompletionGate(answer) {
		if detail, ok := humanPlanExecutePlannerAnswerDetail(answer); ok {
			b.WriteString("\n\nPlanner detail:\n")
			b.WriteString(truncateImplementationDispatchText(detail, 1200))
		} else {
			b.WriteString("\n\nPlanner answer:\n")
			b.WriteString(truncateImplementationDispatchText(answer, 2000))
		}
	}

	return strings.TrimSpace(b.String())
}

func humanPlanExecutePlannerAnswerDetail(answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}

	lower := strings.ToLower(answer)
	if !strings.Contains(lower, "changed paths: none") ||
		!strings.Contains(lower, "required edit paths not changed:") ||
		!strings.Contains(lower, "blocker:") {

		return "", false
	}

	idx := strings.Index(lower, "blocker:")
	if idx < 0 {
		return "", false
	}

	detail := strings.TrimSpace(answer[idx:])

	return detail, detail != ""
}

func planExecuteRecoveryMessageContradictsSourceSatisfied(failure implementationDispatchPlanExecuteFailure, message string) bool {
	if !planExecuteFailureShowsRequiredSourcesSatisfied(failure) {
		return false
	}

	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return false
	}

	return strings.Contains(lower, "has not read") ||
		strings.Contains(lower, "lacks the current source") ||
		strings.Contains(lower, "without reading") ||
		strings.Contains(lower, "please allow the planner to read")
}

func planExecuteFailureIsRepeatedCompletionGate(failure implementationDispatchPlanExecuteFailure) bool {
	text := strings.ToLower(strings.TrimSpace(failure.Result.FailureReason + "\n" + failure.Result.PlannerAnswer))

	return strings.Contains(text, "completion gate rejected") &&
		(strings.Contains(text, "repeatedly") || strings.Contains(text, "gate retry 3/3") || strings.Contains(text, "completion_gate_repeated_rejection"))
}

func implementationDispatchRecoveryMessageIsInternal(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return false
	}

	normalized := strings.NewReplacer("`", "", `"`, "", "'", "").Replace(lower)

	return strings.Contains(normalized, "completion gate") ||
		strings.Contains(normalized, "submit_plan tool") ||
		(strings.Contains(normalized, "submit_plan") && strings.Contains(normalized, "tool call")) ||
		strings.Contains(normalized, "manually invoke") ||
		strings.Contains(normalized, "system rejected") ||
		strings.Contains(normalized, "tool calls")
}

func planExecutePlannerAnswerIsInternalCompletionGate(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))

	return strings.Contains(lower, "completion gate rejected") ||
		strings.Contains(lower, "missing completion evidence") ||
		strings.Contains(lower, "gate retry") ||
		strings.Contains(lower, "requires a successful submit_plan tool result")
}

func humanPlanExecutePlannerBlockerSummary(failure implementationDispatchPlanExecuteFailure) string {
	summary := strings.TrimSpace(failure.Result.FailureReason)
	if summary == "" {
		return ""
	}

	lowerSummary := strings.ToLower(summary)
	answer := strings.ToLower(strings.TrimSpace(failure.Result.PlannerAnswer))

	if planExecuteFailureShowsRequiredSourcesSatisfied(failure) {
		return "planner satisfied required source reads but kept choosing discovery instead of emitting a `submit_plan` tool call"
	}

	if strings.Contains(lowerSummary, "completion gate rejected") ||
		strings.Contains(answer, "completion gate rejected the final answer repeatedly") {

		if len(failure.AllowedPaths) > 0 {
			return "planner stopped before emitting a `submit_plan` tool call for the discovered candidate scope"
		}

		if len(failure.RequiredPaths) > 0 {
			return "planner stopped before emitting a `submit_plan` tool call covering the required edit paths"
		}

		return "planner stopped before emitting a `submit_plan` tool call"
	}

	return summary
}

func humanPlanExecuteDispatchWithoutScopeMessage(failure implementationDispatchPlanExecuteFailure) string {
	var b strings.Builder

	b.WriteString("<<QUESTION>>: Plan-execute dispatch blocked before retry because the TUI did not provide a concrete edit scope.")
	b.WriteString("\n\nThe TUI plan-execute path needs required edit paths, allowed edit paths, or a resolved plan document before retrying the planner.")

	if prompt := strings.TrimSpace(failure.Prompt); prompt != "" {
		b.WriteString("\n\nObjective:\n")
		b.WriteString(prompt)
	}

	if summary := strings.TrimSpace(failure.Result.FailureReason); summary != "" {
		b.WriteString("\n\nPlanner blocker:\n")
		b.WriteString(summary)
	}

	return strings.TrimSpace(b.String())
}

func (m *Model) shouldDisableThinkingForPlanExecutePlanner() bool {
	return m == nil || !m.modelThinkingFloor
}
