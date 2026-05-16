package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

func shouldDispatchImplementationToPlanExecute(plan promptExecutionPlan, mode agent.Mode, cfg config.Config) bool {
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
	return func(cfg config.Config, prompt string, opts planexecute.RunOptions, stdout io.Writer) (string, error) {
		restore := m.applyPlanExecutePromptRunnerState(cfg, opts, stdout)
		defer restore()

		priorMessages := append([]llama.ChatMessage(nil), m.messages...)
		priorReasoning := m.lastAssistantReasoningContent
		answer, err := m.RunPromptOnce(ctx, prompt)
		m.messages = priorMessages
		m.lastAssistantReasoningContent = priorReasoning
		return answer, err
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
	brief := composeImplementationBriefWithEditScopeAndPlanDocuments(prompt, plan, m.cfg, requiredPaths, allowedPaths, planDocuments)
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
		return "", fmt.Errorf("plan-execute exited %d: %s", result.ExitCode, strings.TrimSpace(errBuf.String()))
	}

	return strings.TrimSpace(outBuf.String()), nil
}
