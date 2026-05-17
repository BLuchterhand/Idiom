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

	"idiom/internal/agent"
	"idiom/internal/config"
	"idiom/internal/frontier/pathparse"
	"idiom/internal/llama"
	"idiom/internal/planexecute"
)

type implementationDispatchPlanDocument struct {
	Path      string
	Content   string
	EditPaths []string
}

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
	Path    string
	Change  string
	Excerpt string
	Anchors []string
}

type implementationDispatchRecoveryDecision struct {
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
	Message       string `json:"message"`
	RetryAddendum string `json:"retry_addendum"`
}

var implementationDispatchPlanDocumentPreflightRunner = defaultImplementationDispatchPlanDocumentPreflight
var implementationDispatchPlanDocumentRefineRunner = defaultImplementationDispatchPlanDocumentRefine

func shouldDispatchImplementationToPlanExecute(plan promptExecutionPlan, mode agent.Mode, cfg config.Config, prompt string) bool {
	if !isImplementationLikeFamily(plan.route.Family) {
		return false
	}
	if isPlanExecuteInternalFamily(plan.route.Family) {
		return false
	}
	if mode.Name != "build" {
		return false
	}
	if !cfg.Workspace.EnableWrites {
		return false
	}
	if strings.TrimSpace(cfg.Chat.ImplementationDispatch) != "plan_execute" {
		return false
	}
	if implementationDispatchPromptIsRefineScratchpad(prompt) {
		return false
	}
	return true
}

func isPlanExecuteInternalFamily(family string) bool {
	switch strings.TrimSpace(family) {
	case agent.PlanThenExecutePlannerTaskFamily, agent.PlanThenExecuteReviewRepairTaskFamily:
		return true
	default:
		return false
	}
}

func composeImplementationBrief(prompt string, plan promptExecutionPlan, cfg config.Config) string {
	requiredPaths, allowedPaths, planDocuments := implementationDispatchEditScopeAndPlanDocuments(prompt, plan, cfg)
	return composeImplementationBriefWithEditScopeAndPlanDocuments(prompt, plan, cfg, requiredPaths, allowedPaths, planDocuments)
}

func composeImplementationBriefWithEditScope(prompt string, plan promptExecutionPlan, cfg config.Config, requiredPaths []string, allowedPaths []string) string {
	return composeImplementationBriefWithEditScopeAndPlanDocuments(prompt, plan, cfg, requiredPaths, allowedPaths, nil)
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
		appendImplementationDispatchPlanDocuments(&b, planDocuments)
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

func appendImplementationDispatchPlanDocuments(b *strings.Builder, planDocuments []implementationDispatchPlanDocument) {
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
		b.WriteString(quoteImplementationDispatchReference(truncateImplementationDispatchText(content, 40000)))
	}
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

	atom := runImplementationDispatchPreflightCommand(ctx, root, binary, "atom", "intake", "--spec", abs, "--project-root", root, "--json")
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
		candidate, candidatePath, blockers, ok := materializeImplementationDispatchNonzeroRefineAcceptedCandidate(ctx, cfg, outRel)
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
	result.Document = refined
	result.OK = true
	return result
}

func materializeImplementationDispatchNonzeroRefineAcceptedCandidate(ctx context.Context, cfg config.Config, acceptedRel string) (implementationDispatchPlanDocument, string, []implementationDispatchPlanDocumentPreflightResult, bool) {
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
		if strings.EqualFold(strings.TrimSpace(finding.Severity), "WARN") || strings.EqualFold(strings.TrimSpace(finding.Severity), "INFO") {
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
		if !strings.EqualFold(strings.TrimSpace(finding.Severity), "WARN") {
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

func implementationDispatchEditScopeFromPlanDocuments(plan promptExecutionPlan, planDocuments []implementationDispatchPlanDocument) ([]string, []string) {
	requiredPaths := appendUniqueBriefStrings(nil, plan.runtimeBrief.RequiredEditPaths...)
	for _, doc := range planDocuments {
		requiredPaths = appendUniqueBriefStrings(requiredPaths, doc.EditPaths...)
	}
	if len(requiredPaths) > 0 {
		return requiredPaths, nil
	}
	return nil, nil
}

func replaceImplementationDispatchPlanDocument(planDocuments []implementationDispatchPlanDocument, originalPath string, refined implementationDispatchPlanDocument) []implementationDispatchPlanDocument {
	var out []implementationDispatchPlanDocument
	replaced := false
	for _, doc := range planDocuments {
		if doc.Path == originalPath {
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

func implementationDispatchEditScope(prompt string, plan promptExecutionPlan, cfg config.Config) ([]string, []string) {
	requiredPaths, allowedPaths, _ := implementationDispatchEditScopeAndPlanDocuments(prompt, plan, cfg)
	return requiredPaths, allowedPaths
}

func implementationDispatchEditScopeAndPlanDocuments(prompt string, plan promptExecutionPlan, cfg config.Config) ([]string, []string, []implementationDispatchPlanDocument) {
	requiredPaths, allowedPaths, planDocuments, _ := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, plan, cfg)
	return requiredPaths, allowedPaths, planDocuments
}

func implementationDispatchEditScopePlanDocumentsAndResolution(prompt string, plan promptExecutionPlan, cfg config.Config) ([]string, []string, []implementationDispatchPlanDocument, []implementationDispatchPlanDocumentResolutionBlocker) {
	requiredPaths := appendUniqueBriefStrings(nil, plan.runtimeBrief.RequiredEditPaths...)
	planDocuments, blockers := implementationDispatchPlanDocumentsAndResolutionBlockers(prompt, plan, cfg)
	for _, doc := range planDocuments {
		requiredPaths = appendUniqueBriefStrings(requiredPaths, doc.EditPaths...)
	}
	if len(requiredPaths) > 0 {
		return requiredPaths, nil, planDocuments, blockers
	}
	return nil, nil, planDocuments, blockers
}

func implementationDispatchPlanDocumentEditScope(prompt string, plan promptExecutionPlan, cfg config.Config) []string {
	_, allowedPaths, _ := implementationDispatchEditScopeAndPlanDocuments(prompt, plan, cfg)
	return allowedPaths
}

func implementationDispatchPlanDocuments(prompt string, plan promptExecutionPlan, cfg config.Config) []implementationDispatchPlanDocument {
	docs, _ := implementationDispatchPlanDocumentsAndResolutionBlockers(prompt, plan, cfg)
	return docs
}

func implementationDispatchPlanDocumentsAndResolutionBlockers(prompt string, plan promptExecutionPlan, cfg config.Config) ([]implementationDispatchPlanDocument, []implementationDispatchPlanDocumentResolutionBlocker) {
	root := implementationDispatchWorkspaceRoot(cfg)
	if strings.TrimSpace(root) == "" {
		return nil, nil
	}
	refs := implementationDispatchPlanDocumentRefs(prompt, plan)
	var docs []implementationDispatchPlanDocument
	var blockers []implementationDispatchPlanDocumentResolutionBlocker
	blockUnresolvedPlanDocuments := implementationDispatchPromptRequiresResolvedPlanDocument(prompt, plan, refs)
	for _, ref := range refs {
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

func implementationDispatchPlanDocumentRefs(prompt string, plan promptExecutionPlan) []string {
	refs := appendUniqueBriefStrings(nil, plan.runtimeBrief.TargetRefs...)
	return appendUniqueBriefStrings(refs, pathparse.MentionedWorkspacePaths(prompt)...)
}

func implementationDispatchPlanDocumentPaths(root string, ref string) []string {
	doc, _, ok := resolveImplementationDispatchPlanDocument(root, ref)
	if !ok {
		return nil
	}
	return doc.EditPaths
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
	return implementationDispatchPlanDocument{
		Path:      ref,
		Content:   content,
		EditPaths: implementationDispatchPlanDocumentPathsFromText(content, ref),
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
		return atoms
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
	return atoms
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
	DocumentPath string
	EditPath     string
	Evidence     []string
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

func implementationDispatchPlanDocumentSourceSatisfied(root string, doc implementationDispatchPlanDocument) (implementationDispatchSourceSatisfiedRecord, bool) {
	if !implementationDispatchIsAtomizedPlanDocumentPath(doc.Path) {
		return implementationDispatchSourceSatisfiedRecord{}, false
	}
	atoms := implementationDispatchPlanDocumentAtomsFromText(doc.Content, doc.Path)
	if len(atoms) != 1 {
		return implementationDispatchSourceSatisfiedRecord{}, false
	}
	atom := atoms[0]
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
		return implementationDispatchSourceSatisfiedRecord{
			DocumentPath: doc.Path,
			EditPath:     path,
			Evidence:     evidence,
		}, true
	}
	if evidence := implementationDispatchSourceSatisfiedAnchorEvidence(path, content, scopeText, doc.Content); len(evidence) > 0 {
		return implementationDispatchSourceSatisfiedRecord{
			DocumentPath: doc.Path,
			EditPath:     path,
			Evidence:     evidence,
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
	var tokens []string
	var current []rune
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
			rel := filepath.ToSlash(filepath.Join(dirRel, fmt.Sprintf("atom-%03d.md", i+1)))
			abs, ok := implementationDispatchWorkspaceFile(root, rel)
			if !ok {
				return nil, fmt.Errorf("atom output path is outside the workspace: %s", rel)
			}
			atom = implementationDispatchPlanDocumentAtomWithWorkspaceShape(root, atom)
			body := composeImplementationDispatchPlanDocumentAtom(doc, atom, i+1, len(atoms))
			if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
				return nil, err
			}
			atomDoc, ok := readImplementationDispatchPlanDocument(root, rel)
			if !ok {
				return nil, fmt.Errorf("materialized atom was not readable: %s", rel)
			}
			atomDocuments = append(atomDocuments, atomDoc)
		}
	}
	return atomDocuments, nil
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
	text := strings.ToLower(strings.Join([]string{atom.Change, atom.Excerpt}, " "))
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
	b.WriteString("Dispatch contract:\n\n")
	b.WriteString("- If the Host current-state anchors already show this atom's requested final state, close the refined brief as `Status: source-satisfied - do not dispatch` and cite the exact path evidence; do not invent a no-op edit.\n")
	b.WriteString("- If work remains for an existing file, state executable edit shape only: exact `old_text`/`new_text` blocks or `start_line`/`end_line` plus replacement `content`. Do not use prose-only mutation sites such as \"insert after\", \"append near\", or \"update around\" without that edit shape.\n")
	b.WriteString("- If this is a new file, provide the full file body as `replace_all:true` with empty `old_text` and complete `new_text`.\n\n")
	b.WriteString("## Acceptance\n\n")
	b.WriteString("- The requested behavior is implemented for `")
	b.WriteString(path)
	b.WriteString("`.\n")
	b.WriteString("- No files outside this atom's Files To Modify list are edited.\n\n")
	b.WriteString("## Source Design Document\n\n")
	b.WriteString("Reference only. This section preserves design intent; the active write scope remains this atom's Files To Modify list.\n\n")
	b.WriteString(quoteImplementationDispatchReference(truncateImplementationDispatchText(source.Content, 30000)))
	return b.String()
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

func implementationDispatchPromptIsRefineScratchpad(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	if !strings.Contains(lower, ".idiom/refine/") {
		return false
	}
	if strings.Contains(lower, "refine loop") {
		return true
	}
	if strings.Contains(lower, "# meta") && strings.Contains(lower, "expected path:") {
		return true
	}
	return false
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
		restore := m.applyPlanExecutePromptRunnerState(cfg, opts, stdout)

		priorMessages := append([]llama.ChatMessage(nil), m.messages...)
		priorReasoning := m.lastAssistantReasoningContent
		priorPinnedFiles := m.pinnedFiles
		m.messages = nil
		m.lastAssistantReasoningContent = ""
		m.pinnedFiles = nil
		defer func() {
			m.messages = priorMessages
			m.lastAssistantReasoningContent = priorReasoning
			m.pinnedFiles = priorPinnedFiles
			restore()
		}()
		return m.RunPromptOnce(ctx, prompt)
	}
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
	priorEventStream := m.eventStream
	priorPlanExecuteActive := m.planExecuteActive
	disableThinking := priorDisableThinkingOverride || opts.DisableThinking
	if strings.TrimSpace(opts.TaskFamily) == agent.PlanThenExecutePlannerTaskFamily && m.shouldDisableThinkingForPlanExecutePlanner() {
		disableThinking = true
	}

	m.planExecuteActive = true
	m.cfg.Workspace.SessionID = cfg.Workspace.SessionID
	if m.workspace != nil {
		m.workspace.SetSessionID(cfg.Workspace.SessionID)
	}
	m.cfg.Chat.ImplementationDispatch = ""
	m.SetForcedTaskFamily(opts.TaskFamily)
	m.SetPlannerOwnedVerification(opts.PlannerOwnedVerification)
	m.SetMaxTokensOverride(opts.MaxTokensOverride)
	m.SetDisableThinkingOverride(disableThinking)
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
		m.events <- infoMsg{text: "Materializing design document atoms before plan-execute..."}
		atomDocuments, err := materializeImplementationDispatchPlanDocumentAtoms(m.cfg, planDocuments)
		if err != nil {
			return "", err
		}
		return m.runAtomizedImplementationPlanDocumentsViaPlanExecute(ctx, prompt, plan, atomDocuments)
	}
	return m.runImplementationPlanDocumentsViaPlanExecute(ctx, prompt, plan, planDocuments)
}

func (m *Model) runImplementationPlanDocumentsViaPlanExecute(ctx context.Context, prompt string, plan promptExecutionPlan, planDocuments []implementationDispatchPlanDocument) (string, error) {
	requiredPaths, allowedPaths := implementationDispatchEditScopeFromPlanDocuments(plan, planDocuments)
	if satisfied := implementationDispatchPlanDocumentsSourceSatisfied(m.cfg, planDocuments); len(satisfied) == len(planDocuments) && len(satisfied) > 0 {
		m.logMessage("dispatch_source_satisfied", fmt.Sprintf("skipping plan-execute for %d already-satisfied design-doc atom(s)", len(satisfied)))
		return humanImplementationDispatchSourceSatisfiedMessage(satisfied), nil
	}
	if implementationDispatchPlanDocumentsNeedRefineBeforeDispatch(planDocuments) {
		m.logMessage("dispatch_refine", "materialized design-doc atom requires refine before plan-execute")
		m.events <- infoMsg{text: "Refining materialized design-doc atom before plan-execute..."}
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
		requiredPaths, allowedPaths = implementationDispatchEditScopeFromPlanDocuments(plan, planDocuments)
		if refinedBlocked := preflightImplementationDispatchPlanDocuments(ctx, m.cfg, planDocuments); len(refinedBlocked) > 0 {
			if implementationDispatchPreflightAllSourceSatisfied(refinedBlocked) {
				m.logMessage("dispatch_source_satisfied", fmt.Sprintf("refined atom preflight classified %d atom(s) as source-satisfied", len(refinedBlocked)))
				return humanImplementationDispatchPreflightSourceSatisfiedMessage(refinedBlocked), nil
			}
			m.logMessage("dispatch_refine", "refined atom brief still blocked by preflight")
			return humanPlanDocumentRefinePostPreflightBlockedMessage(refineResults, refinedBlocked), nil
		}
	}
	if blocked := preflightImplementationDispatchPlanDocuments(ctx, m.cfg, planDocuments); len(blocked) > 0 {
		if implementationDispatchPreflightAllSourceSatisfied(blocked) {
			m.logMessage("dispatch_source_satisfied", fmt.Sprintf("preflight classified %d atom(s) as source-satisfied", len(blocked)))
			return humanImplementationDispatchPreflightSourceSatisfiedMessage(blocked), nil
		}
		if implementationDispatchPreflightNeedsAtomization(blocked) && implementationDispatchCanAtomizePlanDocuments(planDocuments) {
			m.logMessage("dispatch_atomize", "preflight requested sub-atoms; materializing atom briefs before plan-execute")
			m.events <- infoMsg{text: "Materializing design document atoms before plan-execute..."}
			atomDocuments, err := materializeImplementationDispatchPlanDocumentAtoms(m.cfg, planDocuments)
			if err != nil {
				return "", err
			}
			return m.runAtomizedImplementationPlanDocumentsViaPlanExecute(ctx, prompt, plan, atomDocuments)
		}
		if implementationDispatchPreflightCanRefine(blocked) {
			m.logMessage("dispatch_refine", "preflight found non-dispatchable plan document shape; running refine before plan-execute")
			m.events <- infoMsg{text: "Refining referenced plan document before plan-execute..."}
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
			requiredPaths, allowedPaths = implementationDispatchEditScopeFromPlanDocuments(plan, planDocuments)
			if refinedBlocked := preflightImplementationDispatchPlanDocuments(ctx, m.cfg, planDocuments); len(refinedBlocked) > 0 {
				if implementationDispatchPreflightAllSourceSatisfied(refinedBlocked) {
					m.logMessage("dispatch_source_satisfied", fmt.Sprintf("refined preflight classified %d atom(s) as source-satisfied", len(refinedBlocked)))
					return humanImplementationDispatchPreflightSourceSatisfiedMessage(refinedBlocked), nil
				}
				if implementationDispatchPreflightNeedsAtomization(refinedBlocked) && implementationDispatchCanAtomizePlanDocuments(planDocuments) {
					m.logMessage("dispatch_atomize", "refined brief still exceeds single-purpose limits; materializing atom briefs before plan-execute")
					m.events <- infoMsg{text: "Materializing refined design document atoms before plan-execute..."}
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
	m.logMessage("dispatch_atomize", fmt.Sprintf("dispatching %d materialized atom brief(s) through plan-execute", len(atomDocuments)))
	var summaries []string
	for i, atomDoc := range atomDocuments {
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
		summaries = append(summaries, fmt.Sprintf("Atom %d/%d `%s`:\n%s", i+1, len(atomDocuments), atomDoc.Path, strings.TrimSpace(answer)))
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Design document implementation completed through %d atom(s).", len(atomDocuments)))
	for _, summary := range summaries {
		b.WriteString("\n\n")
		b.WriteString(summary)
	}
	return strings.TrimSpace(b.String()), nil
}

func implementationDispatchAnswerLooksBlocked(answer string) bool {
	lower := strings.ToLower(strings.TrimSpace(answer))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "blocked dispatch") ||
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
	brief = planexecute.ComposePlannerPromptWithContextPack(brief, contextPackSection)
	if len(requiredPaths) > 0 {
		m.recordRollbackSnapshotsForPaths(requiredPaths)
		m.logMessage("dispatch_scope", fmt.Sprintf("derived %d required edit path(s) for plan-execute dispatch", len(requiredPaths)))
	} else if len(allowedPaths) > 0 {
		m.recordRollbackSnapshotsForPaths(allowedPaths)
		m.logMessage("dispatch_scope", fmt.Sprintf("derived %d candidate edit path(s) from referenced plan document", len(allowedPaths)))
	}

	opts := planexecute.Options{
		Prompt:            brief,
		RequiredEditPaths: requiredPaths,
		AllowedEditPaths:  allowedPaths,
		OnPhase: func(phase, detail string) {
			m.events <- planPhaseMsg{phase: phase, detail: detail}
		},
		OnEditProgress: func(step, total int, path string, succeeded bool) {
			if succeeded {
				m.attachWriteToSubmittedEdit(path, fmt.Sprintf("pe-%d", step-1))
			}
			m.events <- planRefreshMsg{}
			label := "succeeded"
			if !succeeded {
				label = "failed"
			}
			m.events <- planPhaseMsg{phase: "executing", detail: fmt.Sprintf("Edit %d/%d %s (%s)", step, total, filepath.Base(path), label)}
		},
	}
	promptRun := m.newPlanExecutePromptRunner(ctx)

	m.logMessage("dispatch", "invoking plan-execute in-process")
	m.events <- infoMsg{text: "Dispatching to plan-execute..."}

	var outBuf, errBuf bytes.Buffer
	result := planexecute.Run(m.cfg, opts, promptRun, nil, &outBuf, &errBuf)

	m.logMessage("dispatch_result", fmt.Sprintf("plan-execute finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
		result.ExitCode, result.SessionID, result.ResumeStatePath, outBuf.Len(), errBuf.Len()))

	if result.ExitCode != 0 {
		if result.FailureKind == planexecute.RunFailurePlannerBlocked {
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

	return strings.TrimSpace(outBuf.String()), nil
}

type implementationDispatchPlanExecuteFailure struct {
	Prompt        string
	Plan          promptExecutionPlan
	Brief         string
	Options       planexecute.Options
	RequiredPaths []string
	AllowedPaths  []string
	Result        planexecute.RunResult
	Stdout        string
	Stderr        string
}

func (m *Model) handlePlanExecutePlannerBlocked(ctx context.Context, failure implementationDispatchPlanExecuteFailure) (string, bool, error) {
	decision, raw, err := m.decidePlanExecutePlannerBlocked(ctx, failure)
	if err != nil {
		m.logMessage("dispatch_recovery", "recovery decision failed; surfacing planner blocker: "+err.Error())
		return humanPlanExecutePlannerBlockedMessage(failure, implementationDispatchRecoveryDecision{
			Decision: "surface",
			Reason:   "recovery decision failed: " + err.Error(),
		}), true, nil
	}
	m.logMessage("dispatch_recovery", fmt.Sprintf("planner_blocked decision=%s reason=%s", strings.TrimSpace(decision.Decision), truncateImplementationDispatchText(strings.TrimSpace(decision.Reason), 240)))
	if strings.EqualFold(strings.TrimSpace(decision.Decision), "retry") {
		retryPrompt := appendPlanExecutePlannerRecoveryAddendum(failure.Brief, failure, decision, raw)
		retryOptions := failure.Options
		retryOptions.Prompt = retryPrompt
		m.logMessage("dispatch_recovery", "retrying plan-execute after planner_blocked decision")
		m.events <- infoMsg{text: "Retrying plan-execute after planner recovery decision..."}
		var retryOut, retryErr bytes.Buffer
		retryResult := planexecute.Run(m.cfg, retryOptions, m.newPlanExecutePromptRunner(ctx), nil, &retryOut, &retryErr)
		m.logMessage("dispatch_result", fmt.Sprintf("plan-execute retry finished (code=%d session=%s state=%s stdout=%d stderr=%d)",
			retryResult.ExitCode, retryResult.SessionID, retryResult.ResumeStatePath, retryOut.Len(), retryErr.Len()))
		if retryResult.ExitCode == 0 {
			return strings.TrimSpace(retryOut.String()), true, nil
		}
		retryFailure := failure
		retryFailure.Result = retryResult
		retryFailure.Stdout = retryOut.String()
		retryFailure.Stderr = retryErr.String()
		return humanPlanExecutePlannerBlockedMessage(retryFailure, implementationDispatchRecoveryDecision{
			Decision: "surface",
			Reason:   "automatic retry did not produce a plan",
			Message:  strings.TrimSpace(decision.Message),
		}), true, nil
	}
	return humanPlanExecutePlannerBlockedMessage(failure, decision), true, nil
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
	b.WriteString(`{"decision":"retry|surface","reason":"short reason","message":"human-facing message when surfacing","retry_addendum":"extra instruction to append when retrying"}`)
	b.WriteString("\n\nRetry only when the next run can materially change conditions with a concise prompt addendum. Surface when the failure is missing human information, missing body evidence, repeated no-tool/no-progress behavior, or a contract failure that a same-model retry is likely to repeat.\n\n")
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
	if addendum := strings.TrimSpace(decision.RetryAddendum); addendum != "" {
		b.WriteString("- Retry instruction: ")
		b.WriteString(addendum)
		b.WriteString("\n")
	}
	if summary := strings.TrimSpace(failure.Result.FailureReason); summary != "" {
		b.WriteString("- Previous blocker: ")
		b.WriteString(summary)
		b.WriteString("\n")
	}
	if strings.TrimSpace(rawDecision) != "" && strings.TrimSpace(decision.RetryAddendum) == "" {
		b.WriteString("- Recovery controller returned a retry decision; proceed only with `submit_plan`, `Decision: source-satisfied`, or a path-specific `<<QUESTION>>` blocker.\n")
	}
	return b.String()
}

func humanPlanExecutePlannerBlockedMessage(failure implementationDispatchPlanExecuteFailure, decision implementationDispatchRecoveryDecision) string {
	if msg := strings.TrimSpace(decision.Message); msg != "" {
		return msg
	}
	var b strings.Builder
	b.WriteString("<<QUESTION>>: Plan-execute could not produce a plan artifact.")
	if reason := strings.TrimSpace(decision.Reason); reason != "" {
		b.WriteString("\n\nDecision: ")
		b.WriteString(reason)
	}
	if summary := strings.TrimSpace(failure.Result.FailureReason); summary != "" {
		b.WriteString("\n\nPlanner blocker: ")
		b.WriteString(summary)
	}
	if len(failure.RequiredPaths) > 0 {
		b.WriteString("\n\nRequired edit paths still need a plan:")
		for _, path := range failure.RequiredPaths {
			b.WriteString("\n- ")
			b.WriteString(path)
		}
	}
	if answer := strings.TrimSpace(failure.Result.PlannerAnswer); answer != "" && !strings.Contains(answer, "Completion gate rejected the final answer repeatedly") {
		b.WriteString("\n\nPlanner answer:\n")
		b.WriteString(truncateImplementationDispatchText(answer, 2000))
	}
	return strings.TrimSpace(b.String())
}

func (m *Model) shouldDisableThinkingForPlanExecutePlanner() bool {
	if m == nil || m.modelThinkingFloor {
		return false
	}
	for _, value := range []string{
		m.modelID,
		m.cfg.Chat.ModelFamily,
		m.cfg.LlamaServer.Model,
		m.plannerSpec.Model,
	} {
		lower := strings.ToLower(strings.TrimSpace(value))
		if strings.Contains(lower, "qwen") {
			return true
		}
	}
	return false
}
