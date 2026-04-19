package app

import (
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/semantic"
	"qwen-bubbles/internal/workspace"
	"slices"
	"sort"
	"strings"
	"time"
)

type phaseCardMsg struct {
	Lane   analysisLane
	Phase  string
	Detail string
	State  phaseDisplayState
	At     time.Time
}

type phaseCardState struct {
	Lane   analysisLane
	Phase  string
	Detail string
	State  phaseDisplayState
	At     time.Time
}

type executionPanelState struct {
	Request      string
	Lane         analysisLane
	FollowupLane analysisLane
	RouteReason  promptRouteReason
	Contract     answerContract
	Summary      string
	Steps        []string
	Progress     []string
	Verification verificationPlan
	UpdatedAt    time.Time
}

func (m *Model) View() string {
	if !m.ready {
		return "Loading UI..."
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("62")).Padding(0, 1)
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("109")).Padding(0, 1)
	approvalStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("136")).Padding(0, 1)
	errorStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("160")).Padding(0, 1)

	serverState := "down"
	if m.serverUp {
		serverState = "up"
	}
	if m.bootingServer {
		serverState = m.spinner.View() + " booting"
	}

	header := titleStyle.Render(m.cfg.AppName) +
		statusStyle.Render(fmt.Sprintf("server:%s model:%s", serverState, fallback(m.modelID, "unknown")))
	body := m.bodyView()
	contextStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Padding(0, 1)
	footer := helpStyle.Render("Enter send | PgUp/PgDn scroll | Mouse wheel scroll | Ctrl+P panel | Ctrl+S save | Ctrl+L clear | Ctrl+R restart | Ctrl+C quit | /help") + "\n" +
		contextStyle.Render("context: "+m.contextSummary()) + "\n" +
		contextStyle.Render("mode: "+m.modeSummary()) + "\n" +
		contextStyle.Render("writes: "+m.writeSummary()) + "\n" +
		contextStyle.Render("panel: "+m.panelSummary()) + "\n" +
		contextStyle.Render("trace: "+m.traceSummary()) + "\n" +
		contextStyle.Render("workspace: "+m.workspaceSummary()) + "\n" +
		statusStyle.Render(m.status)
	if card := m.phaseCardView(); card != "" {
		footer += "\n" + m.phaseCardStyle().Render(card)
	}
	if banner := m.writeApprovalBanner(); banner != "" {
		footer += "\n" + approvalStyle.Render(banner)
	}
	if banner := m.errorBanner(); banner != "" {
		footer += "\n" + errorStyle.Render(banner)
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, body, m.input.View(), footer)
}

func (m *Model) requestMessages() []llama.ChatMessage {
	var messages []llama.ChatMessage
	if prompt := strings.TrimSpace(m.systemPrompt()); prompt != "" {
		messages = append(messages, llama.ChatMessage{Role: "system", Content: prompt})
	}

	history := m.messages
	if len(history) > 0 {
		last := history[len(history)-1]
		if last.Role == "assistant" && last.Content == "" {
			history = history[:len(history)-1]
		}
	}

	start := 0
	if limit := m.cfg.Chat.MaxHistory; limit > 0 && len(history) > limit {
		start = len(history) - limit
	}
	if start > 0 {
		pinned := make([]string, 0, 8)
		pinned = append(pinned, promptMentionedWorkspacePaths(m.executionPanel.Summary)...)
		if m.taskMemory != nil {
			pinned = append(pinned, m.taskMemory.ChosenTargets...)
		}
		if stage := m.currentStructuralRefactorStage(); stage != nil {
			pinned = append(pinned, stage.FocusPath)
			pinned = append(pinned, stage.Remaining...)
		}
		memoryKind := ""
		if m.taskMemory != nil {
			memoryKind = strings.TrimSpace(m.taskMemory.Kind)
		}
		m.logContextCompaction(contextCompactionSnapshot{
			DroppedMessages:  start,
			RetainedMessages: len(history) - start,
			MaxHistory:       m.cfg.Chat.MaxHistory,
			PinnedPaths:      uniqueLimitedStrings(pinned, 12),
			PinnedMemoryKind: memoryKind,
			Strategy:         "retain_recent_history_with_pinned_task_memory_and_refactor_targets",
		})
	}
	for _, msg := range history[start:] {
		messages = append(messages, llama.ChatMessage{
			Role:    normalizeHistoryRole(msg.Role),
			Content: msg.Content,
		})
	}
	return messages
}

func renderLocateSummary(prompt string, outcome locateOutcome) string {
	best := outcome.Best
	if best.Region == "" {
		best.Region = classifyLocateRegion(best)
	}

	targetLabel := fmt.Sprintf("The code rendering %q", outcome.Literal)
	if containsAny(strings.ToLower(prompt), "top", "header", "window", "banner") && best.Region == "header" {
		targetLabel = fmt.Sprintf("The top-of-window %q banner", outcome.Literal)
	}

	var b strings.Builder
	b.WriteString(renderLaneContractPolicy(analysisLaneLocate))
	b.WriteString("\n\nBest target:\n")
	fmt.Fprintf(&b, "- `%s:%d`\n", best.Path, best.Line)
	fmt.Fprintf(&b, "- %s is in `%s:%d`.", targetLabel, best.Path, best.Line)
	if best.Region == "header" {
		b.WriteString(" It is appended to the Bubble Tea header during `Model.View()`.")
	}
	if strings.TrimSpace(best.Region) != "" {
		fmt.Fprintf(&b, "\n- region: %s", best.Region)
	}
	if strings.TrimSpace(best.Snippet) != "" {
		b.WriteString("\n\nSnippet:\n")
		b.WriteString(best.Snippet)
	}

	for _, alt := range outcome.Alternates {
		switch alt.Region {
		case "chat":
			fmt.Fprintf(&b, "\n\nSeparate similar match:\n- chat placeholder: `%s:%d`", alt.Path, alt.Line)
		case "status":
			fmt.Fprintf(&b, "\n\nSeparate similar match:\n- status text assignment: `%s:%d`", alt.Path, alt.Line)
		case "footer":
			fmt.Fprintf(&b, "\n\nSeparate similar match:\n- footer/help text: `%s:%d`", alt.Path, alt.Line)
		}
	}
	return strings.TrimSpace(b.String())
}

func previewReplyForLog(reply string) string {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return ""
	}
	reply = strings.ReplaceAll(reply, "\n", "\\n")
	if len(reply) > 240 {
		reply = reply[:240] + "..."
	}
	return reply
}

func previewLineRangeEdit(path string, current string, startLine int, endLine int, replacement string) (string, bool) {
	spans := preflightLineSpans(current)
	if startLine <= 0 || endLine < startLine || endLine > len(spans) {
		return "", false
	}
	contextBefore := ""
	if startLine > 1 {
		contextBefore, _ = extractReadFileLine(previewReadOutput(current, startLine-1, startLine-1), startLine-1)
	}
	contextAfter := ""
	if endLine < len(spans) {
		contextAfter, _ = extractReadFileLine(previewReadOutput(current, endLine+1, endLine+1), endLine+1)
	}
	beforeLines := previewLinesFromText(current[spans[startLine-1].start:spans[endLine-1].end])
	afterLines := previewLinesFromText(replacement)
	var b strings.Builder
	b.WriteString("--- ")
	b.WriteString(path)
	b.WriteString("\n+++ ")
	b.WriteString(path)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", startLine, maxInt(1, len(beforeLines)), startLine, maxInt(1, len(afterLines))))
	if contextBefore != "" {
		b.WriteString(" ")
		b.WriteString(contextBefore)
		b.WriteString("\n")
	}
	for _, line := range beforeLines {
		b.WriteString("-")
		b.WriteString(line)
		b.WriteString("\n")
	}
	for _, line := range afterLines {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	if contextAfter != "" {
		b.WriteString(" ")
		b.WriteString(contextAfter)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n"), true
}

func previewReplaceEdit(path string, current string, oldText string, newText string) (string, bool) {
	if strings.TrimSpace(oldText) == "" {
		return "", false
	}
	idx := strings.Index(current, oldText)
	if idx < 0 {
		return "", false
	}
	line := 1 + strings.Count(current[:idx], "\n")
	beforeLines := previewLinesFromText(oldText)
	afterLines := previewLinesFromText(newText)
	var b strings.Builder
	b.WriteString("--- ")
	b.WriteString(path)
	b.WriteString("\n+++ ")
	b.WriteString(path)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", line, maxInt(1, len(beforeLines)), line, maxInt(1, len(afterLines))))
	for _, lineText := range beforeLines {
		b.WriteString("-")
		b.WriteString(lineText)
		b.WriteString("\n")
	}
	for _, lineText := range afterLines {
		b.WriteString("+")
		b.WriteString(lineText)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n"), true
}

func previewLinesFromText(text string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func previewReadOutput(content string, startLine int, endLine int) string {
	spans := preflightLineSpans(content)
	if startLine <= 0 || endLine < startLine || endLine > len(spans) {
		return ""
	}
	var b strings.Builder
	for line := startLine; line <= endLine; line++ {
		span := spans[line-1]
		text := strings.TrimRight(content[span.start:span.end], "\n")
		b.WriteString(fmt.Sprintf("%d | %s\n", line, text))
	}
	return b.String()
}

func (m *Model) runReviewSubagent(
	ctx context.Context,
	prompt string,
	opts llama.ChatOptions,
	thoroughness workspace.ExploreThoroughness,
) (reviewOutcome, error) {
	reviewWorkspace := m.newWorkspaceManager()
	if err := reviewWorkspace.SetExploreThoroughness(thoroughness); err != nil {
		return reviewOutcome{}, err
	}
	if err := reviewWorkspace.SetMode(workspace.ModeExplore); err != nil {
		return reviewOutcome{}, err
	}

	bootstrap, err := m.bootstrapReviewEvidence(ctx, reviewWorkspace, prompt)
	if err != nil {
		return reviewOutcome{}, err
	}

	reviewMessages := []llama.ChatMessage{
		{Role: "system", Content: buildReviewSystemPrompt(m.promptStack.Text)},
		{Role: "user", Content: buildReviewTaskPrompt(prompt)},
	}
	reviewMessages = append(reviewMessages, bootstrap.messages...)

	m.events <- infoMsg{text: fmt.Sprintf("Review agent is triaging the codebase (%s).", thoroughness)}
	if bootstrap.usedCache {
		m.events <- infoMsg{text: "Review agent is reusing cached repo understanding for this session."}
	}
	if bootstrap.confirmed != nil {
		summary := formatReviewFinding(*bootstrap.confirmed)
		m.logMessage("review_confirmed", summary)
		return reviewOutcome{Summary: summary, Finding: bootstrap.confirmed, EvidencePaths: reviewCandidatePaths(bootstrap.candidates)}, nil
	}
	if bootstrap.councilBlocked && strings.TrimSpace(bootstrap.fallbackSummary) != "" {
		return reviewOutcome{Summary: strings.TrimSpace(bootstrap.fallbackSummary), EvidencePaths: reviewCandidatePaths(bootstrap.candidates)}, nil
	}
	summary, err := m.completeAnalysisSummary(
		ctx,
		reviewMessages,
		opts,
		bootstrap.fallbackSummary,
		reviewCandidatePaths(bootstrap.candidates),
		bootstrap.ledger,
		analysisLaneReview,
		func(ctx context.Context, req analysisDeepeningRequest) (analysisDeepeningResult, error) {
			return m.deepenReviewEvidence(ctx, reviewWorkspace, prompt, bootstrap.candidates, req)
		},
	)
	if err != nil {
		return reviewOutcome{}, err
	}
	return reviewOutcome{Summary: summary, EvidencePaths: reviewCandidatePaths(bootstrap.candidates)}, nil
}

type reviewBootstrap struct {
	messages        []llama.ChatMessage
	results         []workspace.ToolResult
	ledger          evidenceLedger
	fallbackSummary string
	usedCache       bool
	candidates      []reviewCandidate
	councilBlocked  bool
	confirmed       *reviewFinding
}

type reviewCandidate struct {
	Path           string
	Score          int
	Reasons        []string
	Hits           []searchMatch
	CouncilVotes   int
	CouncilSignals []string
}

type reviewFinding struct {
	Title      string
	Path       string
	Symbol     string
	Reason     string
	Evidence   []string
	Confidence string
	Scope      string
	Plan       []string
	NextStep   string
}

type reviewOutcome struct {
	Summary       string
	Finding       *reviewFinding
	EvidencePaths []string
}

func (m *Model) bootstrapReviewEvidence(
	ctx context.Context,
	reviewWorkspace *workspace.Manager,
	prompt string,
) (reviewBootstrap, error) {
	bootstrap := reviewBootstrap{
		messages: []llama.ChatMessage{{
			Role: "system",
			Content: "The harness already collected deterministic review evidence below. " +
				"Do not request tools in this review lane. Pick the single best issue supported by the evidence, " +
				"or say that the evidence is inconclusive.",
		}},
	}

	discoveredFiles := make(map[string]struct{})
	discoveredDirs := make(map[string]struct{})
	level := reviewWorkspace.ExploreThoroughness()
	repoProfile := ""
	var goIndex *semantic.GoIndex
	step := -1

	runTool := func(req workspace.ToolRequest) workspace.ToolResult {
		raw := bootstrapRawToolRequest(req)
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "review",
			Raw:       raw,
			Request:   req,
			Mode:      reviewWorkspace.Mode(),
			Broad:     workspace.IsBroadInspectionRequest(req),
			Narrowing: workspace.IsNarrowingRequest(req),
			Action:    agentToolActionClass(req),
		})
		result := reviewWorkspace.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{
			Step:   step,
			Agent:  "review",
			Result: result,
		})
		bootstrap.results = append(bootstrap.results, result)
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: workspace.FormatToolResultForModel(result),
		})
		step--
		return result
	}

	if cache, ok := m.currentExploreCache(reviewWorkspace); ok {
		reviewWorkspace.PrimeDiscoveredPaths(cache.discovered)
		bootstrap.usedCache = true
		repoProfile = cache.repoProfile
		goIndex = cache.goIndex
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Cached repository profile for this session:\n" + strings.TrimSpace(cache.repoProfile),
		})
		for _, path := range cache.discovered {
			discoveredFiles[path] = struct{}{}
		}
		for _, dir := range cache.directories {
			discoveredDirs[dir] = struct{}{}
		}
		m.logMessage("review_cache", cache.repoProfile)
	} else {
		overview := runTool(workspace.ToolRequest{
			Tool:      "list_files",
			Path:      ".",
			Recursive: false,
			MaxDepth:  1,
		})
		if overview.Error != "" {
			bootstrap.fallbackSummary = "Review bootstrap failed during the initial repository overview."
			return bootstrap, nil
		}

		overviewFiles, overviewDirs := parseDiscoveredPaths(overview.Output)
		for path := range overviewFiles {
			discoveredFiles[path] = struct{}{}
		}
		for dir := range overviewDirs {
			discoveredDirs[dir] = struct{}{}
		}

		readLimit := exploreBootstrapReadBudget(level)
		readCount := 0
		for _, path := range []string{"README.md", "go.mod", "config.json"} {
			if _, ok := overviewFiles[path]; !ok {
				continue
			}
			endLine := 120
			if path == "config.json" {
				endLine = 160
			}
			runTool(workspace.ToolRequest{
				Tool:      "read_file",
				Path:      path,
				StartLine: 1,
				EndLine:   endLine,
			})
			discoveredFiles[path] = struct{}{}
			readCount++
			if readCount >= readLimit {
				break
			}
		}

		if _, ok := overviewDirs["cmd"]; ok {
			mainSearch := runTool(workspace.ToolRequest{
				Tool:    "search_files",
				Pattern: "main.go",
				Path:    "cmd",
			})
			mainPaths := parseDiscoveredFileList(mainSearch.Output)
			if len(mainPaths) > 0 {
				runTool(workspace.ToolRequest{
					Tool:      "read_file",
					Path:      mainPaths[0],
					StartLine: 1,
					EndLine:   160,
				})
				discoveredFiles[mainPaths[0]] = struct{}{}
			}
			for _, path := range mainPaths {
				discoveredFiles[path] = struct{}{}
			}
		}

		builtIndex, buildErr := semantic.BuildGoIndex(reviewWorkspace.Root())
		goIndex = builtIndex
		if buildErr != nil {
			m.logMessage("review_semantic_error", buildErr.Error())
		}
		repoProfile = buildExploreRepoProfile(overview.Output, nil)
		if goIndex != nil {
			semanticSummary := goIndex.Summary()
			if semanticSummary != "" {
				repoProfile += "\n\nGo semantic map:\n" + semanticSummary
				m.logMessage("review_semantic", semanticSummary)
			}
		}
		repoProfile = appendLanguageCapabilityRegistry(repoProfile, reviewWorkspace.Root())
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Repository profile:\n" + repoProfile,
		})
		m.logMessage("review_bootstrap", repoProfile)
	}

	searchPaths := prioritizedExploreSearchPaths(discoveredDirs, level)
	pattern := issueSearchPattern(prompt)
	matches := make([]searchMatch, 0, 24)
	if strings.TrimSpace(pattern) != "" {
		for _, path := range uniqueLimitedStrings(searchPaths, reviewSearchPathBudget(level)) {
			req := workspace.ToolRequest{
				Tool:    "search_content",
				Pattern: pattern,
				Path:    path,
			}
			if glob := exploreSearchGlob(path); glob != "" {
				req.Glob = glob
			}
			search := runTool(req)
			for _, match := range parseSearchMatches(search.Output) {
				discoveredFiles[match.Path] = struct{}{}
				matches = append(matches, match)
			}
		}
	}

	rankedCandidates := rankReviewCandidates(prompt, goIndex, matches, reviewCandidateBudget(level))
	if len(rankedCandidates) == 0 {
		rankedCandidates = fallbackReviewCandidates(goIndex, reviewCandidateBudget(level))
	}
	candidates := reviewCouncilCandidates(prompt, goIndex, rankedCandidates, reviewCandidateBudget(level))
	bootstrap.candidates = candidates

	if len(candidates) == 0 {
		candidateBoard := buildReviewCandidateBoard(rankedCandidates)
		note := "Review council did not reach a 2-of-3 agreement on a candidate, so the review lane stopped before additional file reads."
		if candidateBoard != "" {
			bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
				Role:    "system",
				Content: note + "\n\nCandidate board:\n" + candidateBoard,
			})
			m.logMessage("review_council", note+"\n"+candidateBoard)
		} else {
			bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
				Role:    "system",
				Content: note,
			})
			m.logMessage("review_council", note)
		}
		if strings.TrimSpace(repoProfile) != "" {
			m.setExploreCache(reviewWorkspace, repoProfile, discoveredFiles, discoveredDirs, goIndex)
		}
		bootstrap.councilBlocked = true
		bootstrap.ledger = deterministicEvidenceLedger(prompt, bootstrap.results, reviewCandidatePaths(rankedCandidates), "review_candidate_ranker")
		bootstrap.fallbackSummary = buildReviewFallbackSummary(prompt, rankedCandidates, candidateBoard, repoProfile, note, bootstrap.ledger)
		return bootstrap, nil
	}

	for _, candidate := range candidates {
		reviewWorkspace.PrimeDiscoveredPaths([]string{candidate.Path})
		discoveredFiles[candidate.Path] = struct{}{}
		start, end := reviewReadWindow(candidate)
		runTool(workspace.ToolRequest{
			Tool:      "read_file",
			Path:      candidate.Path,
			StartLine: start,
			EndLine:   end,
		})
	}

	candidateBoard := buildReviewCandidateBoard(candidates)
	if candidateBoard != "" {
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Review candidate ranking:\n" + candidateBoard,
		})
		m.logMessage("review_candidates", candidateBoard)
	}

	if finding := confirmReviewFinding(prompt, goIndex, candidates); finding != nil {
		bootstrap.confirmed = finding
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Deterministically confirmed review finding:\n" + formatReviewFinding(*finding),
		})
	}

	if strings.TrimSpace(repoProfile) != "" {
		m.setExploreCache(reviewWorkspace, repoProfile, discoveredFiles, discoveredDirs, goIndex)
	}
	bootstrap.ledger = deterministicEvidenceLedger(prompt, bootstrap.results, reviewCandidatePaths(candidates), "review_candidate_ranker")
	bootstrap.fallbackSummary = buildReviewFallbackSummary(prompt, candidates, candidateBoard, repoProfile, "", bootstrap.ledger)
	return bootstrap, nil
}

func (m *Model) deepenReviewEvidence(
	ctx context.Context,
	reviewWorkspace *workspace.Manager,
	prompt string,
	candidates []reviewCandidate,
	req analysisDeepeningRequest,
) (analysisDeepeningResult, error) {
	paths := reviewCandidatePaths(candidates)
	if len(paths) == 0 {
		paths = req.RequiredPaths
	}
	pathLimit := 2
	readEndLine := 420
	if req.Attempt >= 2 {
		pathLimit = 4
		readEndLine = 720
	}
	paths = uniqueLimitedStrings(paths, pathLimit)
	if len(paths) == 0 {
		return analysisDeepeningResult{
			Lane:      string(analysisLaneReview),
			Attempted: false,
			Exhausted: true,
			Action:    "skip",
			Reason:    "review deepening had no candidate paths to inspect",
		}, nil
	}

	results := make([]workspace.ToolResult, 0, len(paths)*2)
	for i, path := range paths {
		reviewWorkspace.PrimeDiscoveredPaths([]string{path})
		results = append(results, m.executeAnalysisDeepeningTool(ctx, reviewWorkspace, analysisLaneReview, i+1, workspace.ToolRequest{
			Tool:      "read_file",
			Path:      path,
			StartLine: 1,
			EndLine:   readEndLine,
		}))
		if pattern := issueSearchPattern(prompt); strings.TrimSpace(pattern) != "" {
			results = append(results, m.executeAnalysisDeepeningTool(ctx, reviewWorkspace, analysisLaneReview, i+101, workspace.ToolRequest{
				Tool:    "search_content",
				Pattern: pattern,
				Path:    path,
			}))
		}
	}
	attempt := maxInt(req.Attempt, 1)
	return buildAnalysisDeepeningResult(prompt, analysisLaneReview, attempt, results, paths, fmt.Sprintf("readiness gate failed; retry with wider candidate reads (attempt %d)", attempt)), nil
}

func renderLanguageCapabilityRegistry(capabilities []languageCapability) string {
	if len(capabilities) == 0 {
		return ""
	}
	lines := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		line := "- " + capability.Language
		if len(capability.Tools) > 0 {
			line += " tools: " + strings.Join(capability.Tools, ", ")
		}
		if len(capability.Signals) > 0 {
			line += " | signals: " + strings.Join(uniqueLimitedStrings(capability.Signals, 4), ", ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func renderEvidenceLedger(ledger evidenceLedger) string {
	var b strings.Builder
	b.WriteString("Evidence ledger:\n")
	if len(ledger.VerifiedPaths) > 0 {
		b.WriteString("- verified paths: ")
		b.WriteString(strings.Join(uniqueLimitedStrings(ledger.VerifiedPaths, 8), ", "))
		b.WriteString("\n")
	} else {
		b.WriteString("- verified paths: none captured\n")
	}
	if len(ledger.Tools) > 0 {
		b.WriteString("- tools: ")
		b.WriteString(strings.Join(uniqueLimitedStrings(ledger.Tools, 8), ", "))
		b.WriteString("\n")
	}
	if len(ledger.Errors) > 0 {
		b.WriteString("- tool errors: ")
		b.WriteString(strings.Join(uniqueLimitedStrings(ledger.Errors, 3), " | "))
		b.WriteString("\n")
	}
	if len(ledger.InferredGaps) > 0 {
		b.WriteString("- inferred gaps: ")
		b.WriteString(strings.Join(uniqueLimitedStrings(ledger.InferredGaps, 3), " | "))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func rankReviewCandidates(prompt string, goIndex *semantic.GoIndex, matches []searchMatch, limit int) []reviewCandidate {
	if limit == 0 {
		return nil
	}

	candidateMap := make(map[string]*reviewCandidate)
	ensureCandidate := func(path string) *reviewCandidate {
		path = strings.TrimSpace(path)
		if path == "" {
			return nil
		}
		if existing, ok := candidateMap[path]; ok {
			return existing
		}
		candidate := &reviewCandidate{Path: path}
		candidateMap[path] = candidate
		return candidate
	}
	addReason := func(candidate *reviewCandidate, reason string, score int) {
		if candidate == nil || strings.TrimSpace(reason) == "" {
			return
		}
		candidate.Score += score
		if !slices.Contains(candidate.Reasons, reason) {
			candidate.Reasons = append(candidate.Reasons, reason)
		}
	}

	matchCounts := make(map[string]int)
	for _, match := range matches {
		if !isMeaningfulReviewHit(match) {
			continue
		}
		candidate := ensureCandidate(match.Path)
		if candidate == nil {
			continue
		}
		candidate.Hits = append(candidate.Hits, match)
		matchCounts[match.Path]++
	}
	for path, count := range matchCounts {
		candidate := ensureCandidate(path)
		addReason(candidate, fmt.Sprintf("%d issue-marker hit(s)", count), 14+count*4)
	}

	if goIndex != nil {
		semanticCandidates := goIndex.IssueHuntFiles(limit * 2)
		semanticReason := "high-complexity Go hot path"
		if looksLikeCodeSmellPrompt(prompt) {
			semanticCandidates = goIndex.SmellHuntFiles(limit * 2)
			semanticReason = "high-complexity cleanup target"
		}
		for rank, path := range semanticCandidates {
			candidate := ensureCandidate(path)
			addReason(candidate, semanticReason, maxInt(6, 18-rank*2))
		}
		for rank, path := range goIndex.DefaultFiles(limit) {
			candidate := ensureCandidate(path)
			addReason(candidate, "key entrypoint or anchor file", maxInt(2, 8-rank))
		}
		for _, candidate := range candidateMap {
			file := goIndex.FileByPath(candidate.Path)
			if file == nil {
				continue
			}
			surface := file.Funcs + file.Methods
			switch {
			case surface >= 120:
				addReason(candidate, "very large method/function surface", 12)
			case surface >= 80:
				addReason(candidate, "large method/function surface", 8)
			case surface >= 40:
				addReason(candidate, "moderate method/function surface", 4)
			}
			if file.Types >= 8 {
				addReason(candidate, "many types in one file", 4)
			}
			if file.IsTest {
				candidate.Score -= 20
			}
		}
	}

	normalized := strings.ToLower(prompt)
	for _, candidate := range candidateMap {
		pathLower := strings.ToLower(candidate.Path)
		switch {
		case containsAny(pathLower, "manager", "runner", "controller", "client", "server", "app"):
			addReason(candidate, "central orchestration file", 4)
		case containsAny(pathLower, "config", "main"):
			addReason(candidate, "high-impact entry/config path", 2)
		}
		if containsAny(normalized, "code smell", "cleanup", "clean up", "refactor") && containsAny(pathLower, "manager", "app", "runner") {
			addReason(candidate, "likely cleanup target", 6)
		}
		if containsAny(normalized, "bug", "error", "issue", "problem") && len(candidate.Hits) > 0 {
			addReason(candidate, "bug-oriented markers present", 4)
		}
	}

	candidates := make([]reviewCandidate, 0, len(candidateMap))
	for _, candidate := range candidateMap {
		if candidate.Score <= 0 {
			continue
		}
		sort.Slice(candidate.Hits, func(i, j int) bool {
			if candidate.Hits[i].Path != candidate.Hits[j].Path {
				return candidate.Hits[i].Path < candidate.Hits[j].Path
			}
			return candidate.Hits[i].Line < candidate.Hits[j].Line
		})
		candidates = append(candidates, *candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Path < candidates[j].Path
	})
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func reviewCouncilCandidates(prompt string, goIndex *semantic.GoIndex, candidates []reviewCandidate, limit int) []reviewCandidate {
	if len(candidates) == 0 || limit == 0 {
		return nil
	}

	issueHotPaths := make(map[string]struct{})
	defaultHotPaths := make(map[string]struct{})
	if goIndex != nil {
		semanticPaths := goIndex.IssueHuntFiles(limit * 3)
		if looksLikeCodeSmellPrompt(prompt) {
			semanticPaths = goIndex.SmellHuntFiles(limit * 3)
		}
		for _, path := range semanticPaths {
			issueHotPaths[path] = struct{}{}
		}
		for _, path := range goIndex.DefaultFiles(limit * 2) {
			defaultHotPaths[path] = struct{}{}
		}
	}

	filtered := make([]reviewCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		signals := make([]string, 0, 3)
		if len(candidate.Hits) > 0 {
			signals = append(signals, "markers")
		}
		if reviewHasSemanticSupport(goIndex, candidate.Path, issueHotPaths, defaultHotPaths) {
			signals = append(signals, "semantic")
		}
		if reviewPathRoleScore(prompt, candidate.Path) > 0 {
			signals = append(signals, "path-role")
		}
		candidate.CouncilVotes = len(signals)
		candidate.CouncilSignals = signals
		if candidate.CouncilVotes >= 2 {
			filtered = append(filtered, candidate)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CouncilVotes != filtered[j].CouncilVotes {
			return filtered[i].CouncilVotes > filtered[j].CouncilVotes
		}
		if filtered[i].Score != filtered[j].Score {
			return filtered[i].Score > filtered[j].Score
		}
		return filtered[i].Path < filtered[j].Path
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered
}

func confirmReviewFinding(prompt string, goIndex *semantic.GoIndex, candidates []reviewCandidate) *reviewFinding {
	if goIndex == nil || len(candidates) == 0 {
		return nil
	}

	for _, candidate := range candidates {
		finding := confirmCandidateFinding(prompt, goIndex, candidate)
		if finding != nil {
			return finding
		}
	}
	return nil
}

func formatReviewFinding(finding reviewFinding) string {
	var b strings.Builder
	b.WriteString("Finding: ")
	b.WriteString(strings.TrimSpace(finding.Title))
	b.WriteString("\n")
	b.WriteString("File: ")
	b.WriteString(strings.TrimSpace(finding.Path))
	b.WriteString("\n")
	if strings.TrimSpace(finding.Symbol) != "" {
		b.WriteString("Symbol: ")
		b.WriteString(strings.TrimSpace(finding.Symbol))
		b.WriteString("\n")
	}
	b.WriteString("Why it matters: ")
	b.WriteString(strings.TrimSpace(finding.Reason))
	b.WriteString("\n")
	if len(finding.Evidence) > 0 {
		b.WriteString("Evidence:\n")
		for _, line := range finding.Evidence {
			if strings.TrimSpace(line) == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(line))
			b.WriteString("\n")
		}
	}
	if strings.TrimSpace(finding.Confidence) != "" {
		b.WriteString("Confidence: ")
		b.WriteString(strings.TrimSpace(finding.Confidence))
		b.WriteString("\n")
	}
	if strings.TrimSpace(finding.Scope) != "" {
		b.WriteString("Scope: ")
		b.WriteString(strings.TrimSpace(finding.Scope))
		b.WriteString("\n")
	}
	if len(finding.Plan) > 0 {
		b.WriteString("Refactor plan:\n")
		for i, step := range finding.Plan {
			if strings.TrimSpace(step) == "" {
				continue
			}
			fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(step))
		}
	}
	if strings.TrimSpace(finding.NextStep) != "" {
		b.WriteString("Recommended next step: ")
		b.WriteString(strings.TrimSpace(finding.NextStep))
	}
	return strings.TrimSpace(b.String())
}

func reviewFindingAllowsAutoApply(finding reviewFinding) bool {
	return strings.EqualFold(strings.TrimSpace(finding.Scope), "small")
}

func reviewSymbolDisplayName(symbol semantic.GoSymbol) string {
	if strings.TrimSpace(symbol.Receiver) != "" {
		return symbol.Receiver + "." + symbol.Name
	}
	return symbol.Name
}

func fallbackReviewCandidates(goIndex *semantic.GoIndex, limit int) []reviewCandidate {
	if goIndex == nil || limit == 0 {
		return nil
	}
	paths := uniqueLimitedStrings(append(goIndex.IssueHuntFiles(limit), goIndex.DefaultFiles(limit)...), limit)
	candidates := make([]reviewCandidate, 0, len(paths))
	for _, path := range paths {
		candidate := reviewCandidate{
			Path:    path,
			Score:   1,
			Reasons: []string{"likely high-value source file"},
		}
		if file := goIndex.FileByPath(path); file != nil {
			surface := file.Funcs + file.Methods
			switch {
			case surface >= 120:
				candidate.Score += 12
				candidate.Reasons = append(candidate.Reasons, "very large method/function surface")
			case surface >= 80:
				candidate.Score += 8
				candidate.Reasons = append(candidate.Reasons, "large method/function surface")
			case surface >= 40:
				candidate.Score += 4
				candidate.Reasons = append(candidate.Reasons, "moderate method/function surface")
			}
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func reviewReadWindow(candidate reviewCandidate) (int, int) {
	if len(candidate.Hits) == 0 {
		return 1, 220
	}
	line := candidate.Hits[0].Line
	start := line - 24
	if start < 1 {
		start = 1
	}
	return start, line + 60
}

func buildReviewCandidateBoard(candidates []reviewCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	lines := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		line := fmt.Sprintf("- %s (score %d)", candidate.Path, candidate.Score)
		if len(candidate.Reasons) > 0 {
			line += ": " + strings.Join(uniqueLimitedStrings(candidate.Reasons, 3), ", ")
		}
		if candidate.CouncilVotes > 0 && len(candidate.CouncilSignals) > 0 {
			line += fmt.Sprintf(" | council %d/3: %s", candidate.CouncilVotes, strings.Join(candidate.CouncilSignals, ", "))
		}
		if hit := firstNonMetaHit(candidate.Hits); hit != nil {
			line += fmt.Sprintf(" | first hit line %d: %s", hit.Line, truncateText(hit.Text, 80))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func buildReviewFallbackSummary(prompt string, candidates []reviewCandidate, candidateBoard string, repoProfile string, note string, ledger evidenceLedger) string {
	var b strings.Builder
	b.WriteString("Deterministic review assessment.\n\n")
	if strings.TrimSpace(note) != "" {
		b.WriteString(strings.TrimSpace(note))
		b.WriteString("\n\n")
	}
	if evidenceLedgerHasContent(ledger) {
		b.WriteString(renderEvidenceLedger(ledger))
		b.WriteString("\n\n")
	}
	b.WriteString("Confidence:\n")
	if len(candidates) > 0 {
		b.WriteString("- medium-high: ranked semantic evidence identifies concrete candidate files; validate the top candidate before making changes.\n\n")
	} else {
		b.WriteString("- medium: repository evidence was gathered, but no single review candidate dominated.\n\n")
	}
	if len(candidates) > 0 {
		top := candidates[0]
		label := "Most likely finding"
		if looksLikeCodeSmellPrompt(prompt) {
			label = "Most likely cleanup target"
		}
		b.WriteString(label)
		b.WriteString(":\n")
		b.WriteString("- ")
		b.WriteString(top.Path)
		if len(top.Reasons) > 0 {
			b.WriteString(" because ")
			b.WriteString(strings.Join(uniqueLimitedStrings(top.Reasons, 3), ", "))
		}
		b.WriteString("\n")
		if hit := firstNonMetaHit(top.Hits); hit != nil {
			b.WriteString(fmt.Sprintf("- first concrete evidence at line %d: %s\n\n", hit.Line, truncateText(hit.Text, 120)))
		} else {
			b.WriteString("- selected from semantic review signals rather than explicit marker hits\n\n")
		}
	}
	if strings.TrimSpace(candidateBoard) != "" {
		b.WriteString("Top ranked candidates:\n")
		b.WriteString(strings.TrimSpace(candidateBoard))
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(repoProfile) != "" {
		b.WriteString("Repository profile:\n")
		b.WriteString(compactOutput(repoProfile, 14))
		b.WriteString("\n\n")
	}
	b.WriteString("Recommended next step:\n")
	b.WriteString("- inspect the top ranked candidate and either confirm one concrete finding or ask for a narrower review target")
	return strings.TrimSpace(b.String())
}

func reviewSearchPathBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 6
	default:
		return 4
	}
}

func reviewCandidateBudget(level workspace.ExploreThoroughness) int {
	switch level {
	case workspace.ThoroughnessQuick:
		return 2
	case workspace.ThoroughnessThorough:
		return 5
	default:
		return 3
	}
}

func (m *Model) bodyView() string {
	body := m.viewport.View()
	transcriptWidth, panelWidth, showPanel := m.bodyLayout()
	if !showPanel {
		return body
	}
	transcript := lipgloss.NewStyle().Width(transcriptWidth).Render(body)
	panel := m.executionPanelView(panelWidth, m.viewport.Height)
	return lipgloss.JoinHorizontal(lipgloss.Top, transcript, " ", panel)
}

func (m *Model) panelSummary() string {
	if !m.panelVisible {
		return "collapsed"
	}
	if _, _, show := m.bodyLayout(); !show {
		return "expanded (auto-hidden on narrow width)"
	}
	return "expanded"
}

func (m *Model) renderMessages(forceBottom bool) {
	if !m.ready {
		return
	}

	stickBottom := forceBottom || m.viewport.AtBottom()

	userStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("63")).Padding(0, 1).Bold(true)
	assistantStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("31")).Padding(0, 1).Bold(true)
	systemStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("238")).Padding(0, 1).Bold(true)
	toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("94")).Padding(0, 1).Bold(true)
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Padding(0, 1)

	var b strings.Builder
	for _, msg := range m.messages {
		var label string
		content := strings.TrimSpace(msg.Content)
		switch msg.Role {
		case "user":
			label = userStyle.Render("You")
		case "assistant":
			label = assistantStyle.Render("Qwen")
			if m.streaming && content == "" {
				content = m.spinner.View() + " Qwen is working on your request" + m.workingDots()
			}
		case "tool":
			label = toolStyle.Render("Tool")
		default:
			label = systemStyle.Render(strings.Title(msg.Role))
		}
		b.WriteString(label)
		b.WriteString("\n")
		b.WriteString(bodyStyle.Render(content))
		b.WriteString("\n\n")
	}

	if len(m.messages) == 0 {
		b.WriteString(bodyStyle.Render("No messages yet. Type a prompt below or use /help."))
	}

	m.viewport.SetContent(strings.TrimRight(b.String(), "\n"))
	if stickBottom {
		m.viewport.GotoBottom()
	}
}

func (m *Model) phaseCardStyle() lipgloss.Style {
	style := lipgloss.NewStyle().Padding(0, 1).Bold(true)
	switch m.phaseCard.State {
	case phaseStateDone:
		return style.Foreground(lipgloss.Color("230")).Background(lipgloss.Color("28"))
	case phaseStateBlocked:
		return style.Foreground(lipgloss.Color("230")).Background(lipgloss.Color("136"))
	case phaseStateFailed:
		return style.Foreground(lipgloss.Color("230")).Background(lipgloss.Color("160"))
	default:
		return style.Foreground(lipgloss.Color("230")).Background(lipgloss.Color("62"))
	}
}

func (m *Model) phaseCardView() string {
	if strings.TrimSpace(m.phaseCard.Phase) == "" {
		return ""
	}
	header := string(m.phaseCard.Lane)
	if header == "" {
		header = "main"
	}
	state := string(m.phaseCard.State)
	if state == "" {
		state = string(phaseStateActive)
	}
	line := fmt.Sprintf("phase: %s / %s [%s", header, m.phaseCard.Phase, state)
	if !m.phaseCard.At.IsZero() {
		line += " | " + formatPhaseElapsed(time.Since(m.phaseCard.At))
	}
	line += "]"
	if strings.TrimSpace(m.phaseCard.Detail) == "" {
		return line
	}
	return line + "\n" + truncateText(strings.TrimSpace(m.phaseCard.Detail), 120)
}

func (m *Model) executionPanelView(width int, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230"))
	headingStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("151"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	sections := make([]string, 0, 6)
	current := make([]string, 0, 4)
	if strings.TrimSpace(m.executionPanel.Request) != "" {
		current = append(current, "request: "+truncateText(strings.TrimSpace(m.executionPanel.Request), 160))
	}
	if lane := strings.TrimSpace(string(m.executionPanel.Lane)); lane != "" {
		current = append(current, "lane: "+lane)
	}
	if followup := strings.TrimSpace(string(m.executionPanel.FollowupLane)); followup != "" {
		current = append(current, "followup: "+followup)
	}
	if reason := strings.TrimSpace(string(m.executionPanel.RouteReason)); reason != "" {
		current = append(current, "route: "+reason)
	}
	if contract := strings.TrimSpace(string(m.executionPanel.Contract)); contract != "" {
		current = append(current, "contract: "+contract)
	}
	if card := strings.TrimSpace(m.phaseCardView()); card != "" {
		current = append(current, strings.ReplaceAll(card, "\n", " | "))
	}
	if strings.TrimSpace(m.status) != "" {
		current = append(current, "status: "+truncateText(strings.TrimSpace(m.status), 160))
	}
	if len(current) > 0 {
		sections = append(sections, headingStyle.Render("Current")+"\n"+bodyStyle.Render(strings.Join(current, "\n")))
	}

	if summary := strings.TrimSpace(m.executionPanel.Summary); summary != "" || len(m.executionPanel.Steps) > 0 {
		lines := make([]string, 0, len(m.executionPanel.Steps)+1)
		if summary != "" {
			lines = append(lines, truncateText(summary, 180))
		}
		for _, step := range m.executionPanel.Steps {
			step = strings.TrimSpace(step)
			if step == "" {
				continue
			}
			lines = append(lines, "- "+truncateText(step, 180))
		}
		if len(lines) > 0 {
			sections = append(sections, headingStyle.Render("Plan")+"\n"+bodyStyle.Render(strings.Join(lines, "\n")))
		}
	}

	if plan := m.executionPanel.Verification; len(plan.Commands) > 0 {
		lines := make([]string, 0, len(plan.Commands)+1)
		for _, command := range plan.Commands {
			lines = append(lines, "- "+command)
		}
		meta := ""
		switch {
		case strings.TrimSpace(plan.Confidence) != "" && strings.TrimSpace(plan.Coverage) != "":
			meta = fmt.Sprintf("confidence: %s (%s)", plan.Confidence, plan.Coverage)
		case strings.TrimSpace(plan.Confidence) != "":
			meta = "confidence: " + plan.Confidence
		case strings.TrimSpace(plan.Coverage) != "":
			meta = "coverage: " + plan.Coverage
		}
		if meta != "" {
			lines = append(lines, meta)
		}
		if strings.TrimSpace(plan.RiskLevel) != "" {
			lines = append(lines, "risk: "+strings.TrimSpace(plan.RiskLevel))
		}
		if len(plan.ExpectedFailures) > 0 {
			lines = append(lines, "expected failures: "+strings.Join(uniqueLimitedStrings(plan.ExpectedFailures, 3), ", "))
		}
		if strings.TrimSpace(plan.RepairPolicy) != "" {
			lines = append(lines, "repair: "+truncateText(strings.TrimSpace(plan.RepairPolicy), 140))
		}
		sections = append(sections, headingStyle.Render("Verification")+"\n"+bodyStyle.Render(strings.Join(lines, "\n")))
	}

	if proposal := m.pendingWrite; proposal != nil {
		lines := make([]string, 0, 4)
		lines = append(lines, "scope: "+strings.Join(proposal.Paths, ", "))
		for _, step := range proposalPanelSteps(proposal) {
			lines = append(lines, "- "+truncateText(step, 180))
		}
		sections = append(sections, headingStyle.Render("Approval")+"\n"+bodyStyle.Render(strings.Join(lines, "\n")))
	}

	if journal := m.lastJournal; journal != nil {
		lines := make([]string, 0, len(journal.Paths)+5)
		if strings.TrimSpace(journal.Summary) != "" {
			lines = append(lines, truncateText(strings.TrimSpace(journal.Summary), 180))
		}
		for _, path := range journal.Paths {
			lines = append(lines, "- "+path)
		}
		if stage := journal.Refactor; stage != nil && strings.TrimSpace(stage.NextStep) != "" {
			stageLine := "refactor stage"
			if stage.Stage > 0 {
				stageLine += fmt.Sprintf(" %d", stage.Stage)
			}
			if strings.TrimSpace(stage.FocusPath) != "" {
				stageLine += ": " + stage.FocusPath
			}
			if strings.TrimSpace(stage.Verification) != "" {
				stageLine += " [" + stage.Verification + "]"
			}
			lines = append(lines, stageLine)
			lines = append(lines, "next: "+truncateText(stage.NextStep, 180))
		}
		lines = append(lines, "rollback: /undo")
		sections = append(sections, headingStyle.Render("Latest")+"\n"+bodyStyle.Render(strings.Join(lines, "\n")))
	}

	if stage := m.currentStructuralRefactorStage(); stage != nil && structuralRefactorStageResumable(stage) && (m.lastJournal == nil || m.lastJournal.Refactor == nil) {
		stageLine := "stage"
		if stage.Stage > 0 {
			stageLine += fmt.Sprintf(" %d", stage.Stage)
		}
		if strings.TrimSpace(stage.FocusPath) != "" {
			stageLine += ": " + stage.FocusPath
		}
		lines := []string{stageLine}
		if strings.TrimSpace(stage.NextStep) != "" {
			lines = append(lines, "next: "+truncateText(stage.NextStep, 180))
		}
		lines = append(lines, "resume: /resume")
		sections = append(sections, headingStyle.Render("Refactor")+"\n"+bodyStyle.Render(strings.Join(lines, "\n")))
	}

	if len(m.executionPanel.Progress) > 0 {
		lines := make([]string, 0, len(m.executionPanel.Progress))
		for _, entry := range m.executionPanel.Progress {
			lines = append(lines, "- "+truncateText(entry, 180))
		}
		sections = append(sections, headingStyle.Render("Progress")+"\n"+bodyStyle.Render(strings.Join(lines, "\n")))
	}

	content := titleStyle.Render("Execution Panel")
	if len(sections) > 0 {
		content += "\n\n" + strings.Join(sections, "\n\n")
	}
	panelStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(0, 1)
	return panelStyle.
		Width(maxInt(1, width-panelStyle.GetHorizontalFrameSize())).
		Height(maxInt(1, height-panelStyle.GetVerticalFrameSize())).
		Render(content)
}

func proposalPanelSteps(proposal *writeApprovalProposal) []string {
	if proposal == nil {
		return nil
	}
	steps := make([]string, 0, len(proposal.PlannedEdits)+len(proposal.VerifySteps)+1)
	for _, line := range proposal.PlannedEdits {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		steps = append(steps, line)
	}
	if len(steps) == 0 && strings.TrimSpace(proposal.Reason) != "" {
		steps = append(steps, strings.TrimSpace(proposal.Reason))
	}
	return uniqueNonEmptyStrings(steps)
}

func (m *Model) setPhaseCard(lane analysisLane, phase string, state phaseDisplayState, detail string) {
	m.applyPhaseCardUpdate(phaseCardState{
		Lane:   lane,
		Phase:  phase,
		Detail: detail,
		State:  state,
		At:     time.Now(),
	})
}

func (m *Model) emitPhaseCard(lane analysisLane, phase string, state phaseDisplayState, detail string) {
	m.events <- phaseCardMsg{
		Lane:   lane,
		Phase:  phase,
		Detail: detail,
		State:  state,
		At:     time.Now(),
	}
}

func (m *Model) logPhaseCard() {
	if m.debugLogger == nil || strings.TrimSpace(m.phaseCard.Phase) == "" {
		return
	}
	_ = m.debugLogger.LogPhase(string(m.phaseCard.Lane), m.phaseCard.Phase, string(m.phaseCard.State), m.phaseCard.Detail)
}

func (m *Model) applyPhaseCardUpdate(card phaseCardState) {
	m.phaseCard = card
	if card.Lane != "" {
		m.executionPanel.Lane = card.Lane
	}
	if strings.TrimSpace(card.Phase) != "" {
		lane := firstNonEmptyString(string(card.Lane), "main")
		state := firstNonEmptyString(string(card.State), string(phaseStateActive))
		entry := fmt.Sprintf("%s / %s [%s]", lane, card.Phase, state)
		if detail := strings.TrimSpace(card.Detail); detail != "" {
			entry += ": " + detail
		}
		m.appendExecutionProgress(entry)
	}
	m.logPhaseCard()
}

func (m *Model) resetExecutionPanel(prompt string, plan promptExecutionPlan) {
	m.executionPanel = executionPanelState{
		Request:      truncateText(strings.TrimSpace(prompt), 180),
		Lane:         plan.lane,
		FollowupLane: plan.followupLane,
		RouteReason:  plan.reason,
		Contract:     promptPlanAnswerContract(plan),
		Summary:      defaultExecutionPlanSummary(plan),
		UpdatedAt:    time.Now(),
	}
}

func (m *Model) updateViewport(msg tea.Msg) {
	if !m.ready {
		return
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	_ = cmd
}

func scrollableViewportKey(msg tea.Msg) bool {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return false
	}

	switch keyMsg.Type {
	case tea.KeyPgUp, tea.KeyPgDown:
		return true
	default:
		return false
	}
}

func reviewCandidatePaths(candidates []reviewCandidate) []string {
	paths := make([]string, 0, len(candidates))
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		path := strings.TrimSpace(candidate.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func isMeaningfulReviewHit(match searchMatch) bool {
	text := strings.ToLower(strings.TrimSpace(match.Text))
	if text == "" {
		return false
	}
	if looksLikeMarkerPatternDefinition(text) || looksLikeBareMarkerLiteral(text) {
		return false
	}
	if containsAny(text,
		"\\\"tool\\\":",
		"\"tool\":\"",
		"available tools",
		"required output",
		"review rules",
		"request:",
		"review this request",
		"buildreviewsystemprompt",
		"buildreviewtaskprompt",
		"identify any",
		"find one",
		"code smell",
		"cleanup candidate",
		"likely cleanup target",
		"recommended next step",
		"return one concrete finding",
		"addreason(candidate",
		"containsany(",
		"issuesearchpattern(",
		"workspace access is enabled",
	) {
		return false
	}
	return containsAny(text, "todo", "fixme", "xxx", "hack", "deprecated", "panic(", "bug", "regression", "cleanup", "error")
}

func reviewHasSemanticSupport(goIndex *semantic.GoIndex, path string, issueHotPaths map[string]struct{}, defaultHotPaths map[string]struct{}) bool {
	if goIndex == nil {
		return false
	}
	if _, ok := issueHotPaths[path]; ok {
		return true
	}
	if _, ok := defaultHotPaths[path]; ok {
		return true
	}
	file := goIndex.FileByPath(path)
	if file == nil {
		return false
	}
	surface := file.Funcs + file.Methods
	return surface >= 40 || file.Types >= 8 || file.HasMain
}

func reviewPathRoleScore(prompt string, path string) int {
	path = strings.ToLower(strings.TrimSpace(path))
	prompt = strings.ToLower(strings.TrimSpace(prompt))
	if path == "" {
		return 0
	}

	score := 0
	switch {
	case strings.HasSuffix(path, "_test.go"):
		score -= 2
	case path == "readme.md" || path == "go.mod" || path == "config.json":
		score -= 2
	}
	if strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "cmd/") || strings.HasPrefix(path, "pkg/") || strings.HasPrefix(path, "src/") {
		score++
	}
	if containsAny(path, "/manager", "/runner", "/client", "/server", "/controller", "/workspace", "/app") {
		score += 2
	}
	if containsAny(path, "prompts/", "logs/", "models/", "transcript/") {
		score -= 2
	}
	if containsAny(prompt, "code smell", "cleanup", "clean up", "refactor") && containsAny(path, "/manager", "/runner", "/workspace", "/app") {
		score++
	}
	if containsAny(prompt, "bug", "issue", "problem", "error", "regression") && containsAny(path, "/client", "/server", "/runner", "/workspace") {
		score++
	}
	return score
}
