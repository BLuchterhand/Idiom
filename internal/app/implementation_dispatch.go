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
	OriginalPath string
	OutputPath   string
	OK           bool
	ExitCode     int
	Stdout       string
	Stderr       string
	Err          error
	Document     implementationDispatchPlanDocument
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
			parts = append(parts, truncateImplementationDispatchText(strings.Join(report.Reasons, "; "), 260))
		}
		if len(report.Dirty) > 0 {
			parts = append(parts, "dirty paths: "+strings.Join(report.Dirty, ", "))
		}
		if len(report.Invalid) > 0 {
			parts = append(parts, "invalid paths: "+strings.Join(report.Invalid, ", "))
		}
		if len(report.NextActions) > 0 {
			parts = append(parts, "next: "+truncateImplementationDispatchText(strings.Join(report.NextActions, "; "), 180))
		}
		return state, []string{strings.Join(parts, " - ")}
	}
	return "", []string{"atom intake failed: " + implementationDispatchCompactCommandOutput(cmd)}
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
		blockers = append(blockers, "brief lint: "+truncateImplementationDispatchText(message, 280))
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
		warnings = append(warnings, "brief lint: "+truncateImplementationDispatchText(message, 220))
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
		parts = append(parts, truncateImplementationDispatchText(collapseWhitespace(combined), 360))
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
		diag := strings.TrimSpace(result.Stderr)
		if diag == "" {
			diag = strings.TrimSpace(result.Stdout)
		}
		if diag != "" {
			b.WriteString("\n  - ")
			b.WriteString(truncateImplementationDispatchText(collapseWhitespace(diag), 400))
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
	requiredPaths := appendUniqueBriefStrings(nil, plan.runtimeBrief.RequiredEditPaths...)
	planDocuments := implementationDispatchPlanDocuments(prompt, plan, cfg)
	for _, doc := range planDocuments {
		requiredPaths = appendUniqueBriefStrings(requiredPaths, doc.EditPaths...)
	}
	if len(requiredPaths) > 0 {
		return requiredPaths, nil, planDocuments
	}
	return nil, nil, planDocuments
}

func implementationDispatchPlanDocumentEditScope(prompt string, plan promptExecutionPlan, cfg config.Config) []string {
	_, allowedPaths, _ := implementationDispatchEditScopeAndPlanDocuments(prompt, plan, cfg)
	return allowedPaths
}

func implementationDispatchPlanDocuments(prompt string, plan promptExecutionPlan, cfg config.Config) []implementationDispatchPlanDocument {
	root := implementationDispatchWorkspaceRoot(cfg)
	if strings.TrimSpace(root) == "" {
		return nil
	}
	refs := appendUniqueBriefStrings(nil, plan.runtimeBrief.TargetRefs...)
	refs = appendUniqueBriefStrings(refs, pathparse.MentionedWorkspacePaths(prompt)...)
	var docs []implementationDispatchPlanDocument
	for _, ref := range refs {
		doc, ok := readImplementationDispatchPlanDocument(root, ref)
		if !ok {
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
	return docs
}

func implementationDispatchPlanDocumentPaths(root string, ref string) []string {
	doc, ok := readImplementationDispatchPlanDocument(root, ref)
	if !ok {
		return nil
	}
	return doc.EditPaths
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
	headings := []string{
		"expected file scope",
		"implementation file scope",
		"file scope",
		"files to modify",
		"files to edit",
		"write scope",
		"edit scope",
	}
	found := pathparse.MentionedWorkspacePathsUnderHeadings(text, headings...)
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
	requiredPaths, allowedPaths, planDocuments := implementationDispatchEditScopeAndPlanDocuments(prompt, plan, m.cfg)
	if blocked := preflightImplementationDispatchPlanDocuments(ctx, m.cfg, planDocuments); len(blocked) > 0 {
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
					m.logMessage("dispatch_refine", fmt.Sprintf("refine failed for %s (code=%d output=%s)", docPath, refined.ExitCode, strings.TrimSpace(refined.OutputPath)))
					return humanPlanDocumentRefineFailedMessage(blocked, refineResults), nil
				}
				m.logMessage("dispatch_refine", fmt.Sprintf("refined %s to %s", docPath, refined.OutputPath))
				refinedPlanDocuments = replaceImplementationDispatchPlanDocument(refinedPlanDocuments, docPath, refined.Document)
			}
			planDocuments = refinedPlanDocuments
			requiredPaths, allowedPaths = implementationDispatchEditScopeFromPlanDocuments(plan, planDocuments)
			if refinedBlocked := preflightImplementationDispatchPlanDocuments(ctx, m.cfg, planDocuments); len(refinedBlocked) > 0 {
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

	m.logMessage("dispatch_result", fmt.Sprintf("plan-execute finished (code=%d session=%s stdout=%d stderr=%d)",
		result.ExitCode, result.SessionID, outBuf.Len(), errBuf.Len()))

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
		return "", fmt.Errorf("plan-execute exited %d: %s", result.ExitCode, strings.TrimSpace(errBuf.String()))
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
		m.logMessage("dispatch_result", fmt.Sprintf("plan-execute retry finished (code=%d session=%s stdout=%d stderr=%d)",
			retryResult.ExitCode, retryResult.SessionID, retryOut.Len(), retryErr.Len()))
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
