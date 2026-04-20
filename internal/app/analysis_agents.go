package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/frontier"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/semantic"
	"qwen-bubbles/internal/workspace"
	"slices"
	"strings"
)

type exploreCacheInvalidatedMsg struct {
	reason string
}

func analysisTransition(plan promptExecutionPlan, completedLane analysisLane) (analysisLane, bool) {
	if completedLane == "" || plan.lane != completedLane {
		return "", false
	}
	switch completedLane {
	case analysisLaneLocate, analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture:
		if routeContinuesToImplement(plan) {
			return analysisLaneImplement, true
		}
	}
	return "", false
}

func analysisSummaryTargets(history []llama.ChatMessage) []implementationTarget {
	return frontier.AnalysisSummaryTargets(history)
}

func parseAnalysisSummaryTargetToken(field string) (string, int, bool) {
	return frontier.ParseAnalysisSummaryTargetToken(field)
}

func latestAnalysisSummary(history []llama.ChatMessage) (string, string, bool) {
	return frontier.LatestAnalysisSummary(history)
}

func analysisSummaryStrategyHints(summary string) []string {
	return frontier.AnalysisSummaryStrategyHints(summary)
}

func parseLocateCandidates(output string) []locateCandidate {
	return frontier.ParseLocateCandidates(output)
}

func scoreLocateCandidate(prompt string, candidate locateCandidate) int {
	return frontier.ScoreLocateCandidate(prompt, candidate)
}

func sortLocateCandidates(candidates []locateCandidate) {
	frontier.SortLocateCandidates(candidates)
}

func (m *Model) applyAnalysisAnswerCritic(answer string, lane analysisLane, evidencePaths []string) {
	critic := critiqueAnalysisFinalAnswer(answer, lane, evidencePaths)
	if critic.Passed {
		critic.Action = "accept"
	} else {
		critic.Action = "warn"
	}
	m.logFinalAnswerCritic(critic)
}

func critiqueAnalysisFinalAnswer(answer string, lane analysisLane, evidencePaths []string) finalAnswerCriticResult {
	result := finalAnswerCriticResult{
		Contract: string(answerContractForLane(lane)),
		Passed:   true,
	}
	addFinding := func(finding string) {
		result.Passed = false
		result.Findings = append(result.Findings, finding)
	}
	trimmed := strings.TrimSpace(answer)
	normalized := strings.ToLower(trimmed)
	if trimmed == "" {
		addFinding("final answer is empty")
		return result
	}
	if lane != analysisLaneMain && lane != analysisLaneImplement && len(evidencePaths) == 0 {
		addFinding("missing evidence paths for analysis answer")
	}
	if lane == analysisLaneLocate && len(promptMentionedWorkspacePaths(answer)) == 0 && !containsAny(normalized, "not found", "no match") {
		addFinding("locate answer lacks concrete path or explicit no-match statement")
	}
	if lane == analysisLaneReview && !containsAny(normalized, "finding", "no bounded", "no actionable", "risk", "residual") {
		addFinding("review answer lacks finding or bounded-no-finding framing")
	}
	if lane == analysisLaneArchitecture && !containsAny(normalized, "structure", "architecture", "boundary", "package", "ownership", "hotspot") {
		addFinding("architecture answer lacks structural framing")
	}
	if lane == analysisLaneArchitecture {
		if containsAny(normalized, "did not complete cleanly", "final blocker", "budget_exhausted", "budget exhausted") &&
			!containsAny(normalized, "opportunity", "recommend", "suggested direction", "refactor", "split", "hotspot") {
			addFinding("architecture answer reports a runner failure without architecture recommendations")
		}
		if !containsAny(normalized, "opportunity", "recommend", "suggested direction", "refactor", "split", "hotspot") {
			addFinding("architecture answer lacks a concrete recommendation or hotspot")
		}
	}
	if _, low := lowConfidenceReadinessReason(answer); low && !containsAny(normalized, "not enough evidence", "insufficient evidence", "unable to confirm", "not found", "no bounded") {
		addFinding("low-confidence answer lacks explicit uncertainty framing")
	}
	return result
}

func (m *Model) runExploreSubagent(
	ctx context.Context,
	prompt string,
	opts llama.ChatOptions,
	thoroughness workspace.ExploreThoroughness,
) (exploreOutcome, error) {
	exploreWorkspace := m.newWorkspaceManager()
	if err := exploreWorkspace.SetExploreThoroughness(thoroughness); err != nil {
		return exploreOutcome{}, err
	}
	if err := exploreWorkspace.SetMode(workspace.ModeExplore); err != nil {
		return exploreOutcome{}, err
	}

	rails := buildDeterministicControlRails(prompt, promptExecutionPlan{
		lane:     analysisLaneExplore,
		contract: answerContractForLane(analysisLaneExplore),
	})
	exploreRunner := &agent.Runner{
		Client:           m.client,
		Workspace:        exploreWorkspace,
		MaxToolSteps:     exploreToolBudget(m.cfg.Chat.MaxToolSteps),
		AgentName:        "explore",
		PermissionPolicy: controlRailPermissionPolicy(rails),
		RuntimeBrief: buildWorkerRuntimeBrief(
			prompt,
			rails,
			implementationTargetPacket{},
			"",
			nil,
			agent.RunPolicy{MaxToolSteps: exploreToolBudget(m.cfg.Chat.MaxToolSteps)},
			nil,
			[]string{"use bootstrapped repository evidence before requesting new broad discovery"},
			nil,
		),
		OnEvent: func(event agent.RunEvent) {
			m.logMessage(event.Role, event.Content)
		},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			return controlRailPreExecute(rails, req)
		},
	}
	exploreSystemPrompt := buildSubagentSystemPrompt(m.promptStack.Text, exploreWorkspace.Instructions())
	bootstrap, err := m.bootstrapExploreEvidence(ctx, exploreWorkspace, prompt)
	if err != nil {
		return exploreOutcome{}, err
	}
	exploreRunner.InitialNeedsNarrowing = bootstrap.performedBroadReview
	if bootstrap.performedBroadReview {
		exploreRunner.InitialBroadInspects = 1
	}
	exploreRunner.SeedResults = append(exploreRunner.SeedResults, bootstrap.results...)
	exploreMessages := []llama.ChatMessage{
		{Role: "system", Content: exploreSystemPrompt},
		{Role: "user", Content: buildExploreTaskPrompt(prompt)},
	}
	exploreMessages = append(exploreMessages, bootstrap.messages...)

	m.events <- infoMsg{text: fmt.Sprintf("Explore agent is investigating (%s).", thoroughness)}
	if bootstrap.usedCache {
		m.events <- infoMsg{text: "Explore agent is reusing cached repo understanding for this session."}
	}
	reply, err := exploreRunner.Run(
		ctx,
		exploreMessages,
		opts,
		func(status string) {
			m.events <- infoMsg{text: "Explore agent: " + status}
		},
		func(event agent.ToolRequestEvent) {
			m.logToolRequest(event)
		},
		func(event agent.ToolResultEvent) {
			m.logToolResult(event)
		},
	)
	if err != nil {
		return exploreOutcome{}, err
	}
	requiredPaths := exploreEvidencePaths(bootstrap.results)
	evidencePaths := analysisEvidencePaths(requiredPaths, bootstrap.ledger)
	if answer, ok := cleanAnalysisReply(reply); ok {
		accepted := analysisReplyAcceptedWithEvidenceLedger(answer, requiredPaths, bootstrap.ledger, analysisLaneExplore)
		readiness := analysisReadinessAssessmentForReply(answer, requiredPaths, bootstrap.ledger, analysisLaneExplore, accepted, "fallback")
		m.logAnalysisReadiness(readiness)
		if readiness.Ready {
			return exploreOutcome{Summary: answer, EvidencePaths: evidencePaths}, nil
		}
		if accepted {
			m.logMessage(analysisSummaryLogKey(analysisLaneExplore), "explore summary rejected: failed readiness gate\n"+truncateText(answer, 480))
		} else {
			m.logMessage(analysisSummaryLogKey(analysisLaneExplore), "explore summary rejected: failed grounded explanation contract\n"+truncateText(reply, 480))
		}
	} else {
		m.logAnalysisReadiness(analysisReadinessAssessmentForInvalidReply(analysisLaneExplore, "fallback", "empty or tool-formatted response"))
		m.logMessage(analysisSummaryLogKey(analysisLaneExplore), "explore summary rejected: empty or tool-formatted response")
	}
	return exploreOutcome{Summary: buildExploreFallbackSummary(prompt, bootstrap), EvidencePaths: evidencePaths}, nil
}

func (m *Model) runArchitectureSubagent(
	ctx context.Context,
	prompt string,
	opts llama.ChatOptions,
	thoroughness workspace.ExploreThoroughness,
	targetRefs []string,
) (architectureOutcome, error) {
	architectureWorkspace := m.newWorkspaceManager()
	if err := architectureWorkspace.SetExploreThoroughness(thoroughness); err != nil {
		return architectureOutcome{}, err
	}
	if err := architectureWorkspace.SetMode(workspace.ModeExplore); err != nil {
		return architectureOutcome{}, err
	}

	bootstrap, err := m.bootstrapArchitectureEvidence(ctx, architectureWorkspace, prompt, targetRefs)
	if err != nil {
		return architectureOutcome{}, err
	}

	architectureMessages := []llama.ChatMessage{
		{Role: "system", Content: buildArchitectureSystemPrompt(m.promptStack.Text)},
		{Role: "user", Content: buildArchitectureTaskPrompt(prompt)},
	}
	architectureMessages = append(architectureMessages, bootstrap.messages...)

	m.events <- infoMsg{text: fmt.Sprintf("Architecture agent is mapping the codebase (%s).", thoroughness)}
	if bootstrap.usedCache {
		m.events <- infoMsg{text: "Architecture agent is reusing cached repo understanding for this session."}
	}
	summary, err := m.completeAnalysisSummary(
		ctx,
		architectureMessages,
		opts,
		bootstrap.fallbackSummary,
		architectureHotspotPaths(bootstrap.hotspots),
		bootstrap.ledger,
		analysisLaneArchitecture,
		func(ctx context.Context, req analysisDeepeningRequest) (analysisDeepeningResult, error) {
			return m.deepenArchitectureEvidence(ctx, architectureWorkspace, prompt, bootstrap.hotspots, req)
		},
	)
	if err != nil {
		return architectureOutcome{}, err
	}
	return architectureOutcome{Summary: summary, EvidencePaths: architectureHotspotPaths(bootstrap.hotspots)}, nil
}

type exploreBootstrap struct {
	messages             []llama.ChatMessage
	results              []workspace.ToolResult
	ledger               evidenceLedger
	performedBroadReview bool
	usedCache            bool
}

type architectureBootstrap struct {
	messages        []llama.ChatMessage
	results         []workspace.ToolResult
	ledger          evidenceLedger
	fallbackSummary string
	usedCache       bool
	hotspots        []architectureHotspot
}

type exploreSessionCache struct {
	workspaceRoot string
	workspaceCWD  string
	stateToken    string
	repoProfile   string
	discovered    []string
	directories   []string
	goIndex       *semantic.GoIndex
}

type architectureHotspot struct {
	Path             string
	Package          string
	Score            int
	FuncsMethods     int
	Types            int
	Imports          int
	LocalImports     int
	ImporterFiles    int
	PackageFiles     int
	BroadSurface     bool
	Responsibilities []string
	Reasons          []string
}

type exploreOutcome struct {
	Summary       string
	EvidencePaths []string
}

type architectureOutcome struct {
	Summary       string
	EvidencePaths []string
}

func (m *Model) bootstrapExploreEvidence(
	ctx context.Context,
	exploreWorkspace *workspace.Manager,
	prompt string,
) (exploreBootstrap, error) {
	bootstrap := exploreBootstrap{
		messages: []llama.ChatMessage{{
			Role: "system",
			Content: "The harness already performed a source-focused overview and targeted bootstrap reads below. " +
				"Do not call list_files again unless the user explicitly asks for another broad overview. " +
				"Start from this evidence first; if you need more detail, prefer search_content, search_files, or read_file on discovered handles.",
		}},
	}
	discoveredFiles := make(map[string]struct{})
	discoveredDirs := make(map[string]struct{})
	level := exploreWorkspace.ExploreThoroughness()
	repoProfile := ""
	var goIndex *semantic.GoIndex
	step := -1
	runTool := func(req workspace.ToolRequest) workspace.ToolResult {
		raw := bootstrapRawToolRequest(req)
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "explore",
			Raw:       raw,
			Request:   req,
			Mode:      exploreWorkspace.Mode(),
			Broad:     workspace.IsBroadInspectionRequest(req),
			Narrowing: workspace.IsNarrowingRequest(req),
			Action:    agentToolActionClass(req),
		})
		result := exploreWorkspace.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{
			Step:   step,
			Agent:  "explore",
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

	if cache, ok := m.currentExploreCache(exploreWorkspace); ok {
		exploreWorkspace.PrimeDiscoveredPaths(cache.discovered)
		bootstrap.performedBroadReview = true
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
		m.logMessage("explore_cache", cache.repoProfile)
	} else {
		overview := runTool(workspace.ToolRequest{
			Tool:      "list_files",
			Path:      ".",
			Recursive: false,
			MaxDepth:  1,
		})
		if overview.Error == "" {
			bootstrap.performedBroadReview = true
		}
		if overview.Error != "" {
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

		goIndex, err := semantic.BuildGoIndex(exploreWorkspace.Root())
		if err != nil {
			m.logMessage("explore_semantic_error", err.Error())
		}
		repoProfile = buildExploreRepoProfile(overview.Output, bootstrap.results)
		if goIndex != nil {
			semanticSummary := goIndex.Summary()
			if semanticSummary != "" {
				repoProfile += "\n\nGo semantic map:\n" + semanticSummary
				m.logMessage("explore_semantic", semanticSummary)
			}
		}
		repoProfile = appendLanguageCapabilityRegistry(repoProfile, exploreWorkspace.Root())
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Repository profile:\n" + repoProfile,
		})
		m.logMessage("explore_bootstrap", repoProfile)
	}

	searchPaths := prioritizedExploreSearchPaths(discoveredDirs, level)
	queryTerms := exploreQueryTerms(prompt)
	if goIndex != nil {
		semanticCandidates := make([]string, 0, 8)
		semanticCandidates = append(semanticCandidates, goIndex.DefaultFiles(exploreSemanticDefaultFileBudget(level))...)
		semanticCandidates = append(semanticCandidates, goIndex.RelevantFiles(queryTerms, exploreSemanticRelevantFileBudget(level))...)
		if looksLikeIssueHuntPrompt(prompt) {
			semanticCandidates = append(semanticCandidates, goIndex.IssueHuntFiles(exploreSemanticIssueFileBudget(level))...)
		}
		for _, path := range uniqueLimitedStrings(semanticCandidates, exploreSemanticTotalReadBudget(level)) {
			exploreWorkspace.PrimeDiscoveredPaths([]string{path})
			runTool(workspace.ToolRequest{
				Tool:      "read_file",
				Path:      path,
				StartLine: 1,
				EndLine:   200,
			})
			discoveredFiles[path] = struct{}{}
		}
	}
	if len(queryTerms) > 0 {
		for _, path := range uniqueLimitedStrings(searchPaths, exploreKeywordSearchPathBudget(level)) {
			req := workspace.ToolRequest{
				Tool:    "search_content",
				Pattern: strings.Join(queryTerms, "|"),
				Path:    path,
			}
			if glob := exploreSearchGlob(path); glob != "" {
				req.Glob = glob
			}
			search := runTool(req)
			hitPaths := parseSearchMatchPaths(search.Output)
			for _, hit := range hitPaths {
				discoveredFiles[hit] = struct{}{}
			}
			for _, hit := range uniqueLimitedStrings(hitPaths, exploreKeywordReadBudget(level)) {
				runTool(workspace.ToolRequest{
					Tool:      "read_file",
					Path:      hit,
					StartLine: 1,
					EndLine:   200,
				})
				discoveredFiles[hit] = struct{}{}
			}
		}
	}

	if looksLikeIssueHuntPrompt(prompt) {
		for _, path := range uniqueLimitedStrings(searchPaths, exploreIssueSearchPathBudget(level)) {
			req := workspace.ToolRequest{
				Tool:    "search_content",
				Pattern: issueSearchPattern(prompt),
				Path:    path,
			}
			if glob := exploreSearchGlob(path); glob != "" {
				req.Glob = glob
			}
			search := runTool(req)
			hitPaths := parseSearchMatchPaths(search.Output)
			for _, hit := range hitPaths {
				discoveredFiles[hit] = struct{}{}
			}
			for _, hit := range uniqueLimitedStrings(hitPaths, exploreIssueReadBudget(level)) {
				runTool(workspace.ToolRequest{
					Tool:      "read_file",
					Path:      hit,
					StartLine: 1,
					EndLine:   200,
				})
				discoveredFiles[hit] = struct{}{}
			}
		}
	}

	if strings.TrimSpace(repoProfile) != "" {
		m.setExploreCache(exploreWorkspace, repoProfile, discoveredFiles, discoveredDirs, goIndex)
	}
	bootstrap.ledger = buildEvidenceLedger(prompt, bootstrap.results)
	return bootstrap, nil
}

func (m *Model) bootstrapArchitectureEvidence(
	ctx context.Context,
	architectureWorkspace *workspace.Manager,
	prompt string,
	targetRefs []string,
) (architectureBootstrap, error) {
	bootstrap := architectureBootstrap{
		messages: []llama.ChatMessage{{
			Role: "system",
			Content: "The harness already collected deterministic architecture evidence below. " +
				"Do not request tools in this architecture lane. Explain the current structure, identify the best structural opportunities, " +
				"and ground your answer in the supplied hotspot paths and repository profile.",
		}},
	}

	discoveredFiles := make(map[string]struct{})
	discoveredDirs := make(map[string]struct{})
	level := architectureWorkspace.ExploreThoroughness()
	repoProfile := ""
	var goIndex *semantic.GoIndex
	step := -1
	explicitTargetCandidates := append(promptMentionedWorkspacePaths(prompt), targetRefs...)
	explicitTargets := resolveImplementationPrimaryPaths(architectureWorkspace.Root(), uniqueNormalizedPaths(explicitTargetCandidates))

	runTool := func(req workspace.ToolRequest) workspace.ToolResult {
		raw := bootstrapRawToolRequest(req)
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "architecture",
			Raw:       raw,
			Request:   req,
			Mode:      architectureWorkspace.Mode(),
			Broad:     workspace.IsBroadInspectionRequest(req),
			Narrowing: workspace.IsNarrowingRequest(req),
			Action:    agentToolActionClass(req),
		})
		result := architectureWorkspace.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{
			Step:   step,
			Agent:  "architecture",
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

	if cache, ok := m.currentExploreCache(architectureWorkspace); ok {
		architectureWorkspace.PrimeDiscoveredPaths(cache.discovered)
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
		m.logMessage("architecture_cache", cache.repoProfile)
	} else {
		overview := runTool(workspace.ToolRequest{
			Tool:      "list_files",
			Path:      ".",
			Recursive: false,
			MaxDepth:  1,
		})
		if overview.Error != "" {
			bootstrap.fallbackSummary = "Architecture bootstrap failed during the initial repository overview."
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

		builtIndex, buildErr := semantic.BuildGoIndex(architectureWorkspace.Root())
		goIndex = builtIndex
		if buildErr != nil {
			m.logMessage("architecture_semantic_error", buildErr.Error())
		}
		repoProfile = buildExploreRepoProfile(overview.Output, nil)
		if goIndex != nil {
			semanticSummary := goIndex.Summary()
			if semanticSummary != "" {
				repoProfile += "\n\nGo semantic map:\n" + semanticSummary
				m.logMessage("architecture_semantic", semanticSummary)
			}
		}
		repoProfile = appendLanguageCapabilityRegistry(repoProfile, architectureWorkspace.Root())
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Repository profile:\n" + repoProfile,
		})
		m.logMessage("architecture_bootstrap", repoProfile)
	}

	searchPaths := prioritizedExploreSearchPaths(discoveredDirs, level)
	for _, path := range explicitTargets {
		architectureWorkspace.PrimeDiscoveredPaths([]string{path})
		discoveredFiles[path] = struct{}{}
		runTool(workspace.ToolRequest{
			Tool:      "read_file",
			Path:      path,
			StartLine: 1,
			EndLine:   260,
		})
	}
	if pattern := architectureSearchPattern(prompt); strings.TrimSpace(pattern) != "" {
		for _, path := range uniqueLimitedStrings(searchPaths, architectureSearchPathBudget(level)) {
			req := workspace.ToolRequest{
				Tool:    "search_content",
				Pattern: pattern,
				Path:    path,
			}
			if glob := exploreSearchGlob(path); glob != "" {
				req.Glob = glob
			}
			search := runTool(req)
			for _, hit := range uniqueLimitedStrings(parseSearchMatchPaths(search.Output), architectureSearchReadBudget(level)) {
				architectureWorkspace.PrimeDiscoveredPaths([]string{hit})
				discoveredFiles[hit] = struct{}{}
				runTool(workspace.ToolRequest{
					Tool:      "read_file",
					Path:      hit,
					StartLine: 1,
					EndLine:   220,
				})
			}
		}
	}

	excludedPaths := architectureExcludedPathsForPrompt(architectureWorkspace.Root(), prompt)
	if len(excludedPaths) > 0 {
		m.logMessage("architecture_excluded_paths", "Excluded dirty workspace paths:\n- "+strings.Join(mapKeysSorted(excludedPaths), "\n- "))
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "User requested git-clean targets. Exclude these dirty workspace paths from architecture target selection:\n- " + strings.Join(mapKeysSorted(excludedPaths), "\n- "),
		})
	}
	hotspots := rankArchitectureHotspotsWithOptions(prompt, goIndex, explicitTargets, architectureHotspotBudget(level), architectureHotspotOptions{
		ExcludedPaths: excludedPaths,
	})
	bootstrap.hotspots = hotspots
	readPaths := append([]string{}, explicitTargets...)
	readPaths = append(readPaths, architectureHotspotPaths(hotspots)...)
	readPaths = append(readPaths, architectureRelevantPaths(prompt, goIndex, architectureRelevantFileBudget(level))...)
	readPaths = filterArchitecturePaths(readPaths, excludedPaths)
	for _, path := range uniqueLimitedStrings(readPaths, architectureReadBudget(level)) {
		architectureWorkspace.PrimeDiscoveredPaths([]string{path})
		discoveredFiles[path] = struct{}{}
		runTool(workspace.ToolRequest{
			Tool:      "read_file",
			Path:      path,
			StartLine: 1,
			EndLine:   220,
		})
	}

	hotspotBoard := buildArchitectureHotspotBoard(hotspots)
	if hotspotBoard != "" {
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: "Architecture hotspot board:\n" + hotspotBoard,
		})
		m.logMessage("architecture_hotspots", hotspotBoard)
	}

	if strings.TrimSpace(repoProfile) != "" {
		m.setExploreCache(architectureWorkspace, repoProfile, discoveredFiles, discoveredDirs, goIndex)
	}
	bootstrap.ledger = deterministicEvidenceLedger(prompt, bootstrap.results, architectureHotspotPaths(hotspots), "architecture_hotspot_ranker")
	bootstrap.fallbackSummary = buildArchitectureFallbackSummary(prompt, hotspots, hotspotBoard, repoProfile, "", bootstrap.ledger)
	return bootstrap, nil
}

func (m *Model) deepenArchitectureEvidence(
	ctx context.Context,
	architectureWorkspace *workspace.Manager,
	prompt string,
	hotspots []architectureHotspot,
	req analysisDeepeningRequest,
) (analysisDeepeningResult, error) {
	paths := architectureHotspotPaths(hotspots)
	if len(paths) == 0 {
		paths = req.RequiredPaths
	}
	pathLimit := 2
	readEndLine := 460
	if req.Attempt >= 2 {
		pathLimit = 4
		readEndLine = 760
	}
	paths = uniqueLimitedStrings(paths, pathLimit)
	if len(paths) == 0 {
		return analysisDeepeningResult{
			Lane:      string(analysisLaneArchitecture),
			Attempted: false,
			Exhausted: true,
			Action:    "skip",
			Reason:    "architecture deepening had no hotspot paths to inspect",
		}, nil
	}

	results := make([]workspace.ToolResult, 0, len(paths)*2)
	for i, path := range paths {
		architectureWorkspace.PrimeDiscoveredPaths([]string{path})
		results = append(results, m.executeAnalysisDeepeningTool(ctx, architectureWorkspace, analysisLaneArchitecture, i+1, workspace.ToolRequest{
			Tool:      "read_file",
			Path:      path,
			StartLine: 1,
			EndLine:   readEndLine,
		}))
		results = append(results, m.executeAnalysisDeepeningTool(ctx, architectureWorkspace, analysisLaneArchitecture, i+101, workspace.ToolRequest{
			Tool:     "go_related_files",
			Pattern:  architectureRelatedPatternForPath(path),
			Path:     filepath.Dir(path),
			MaxDepth: 12,
		}))
	}
	attempt := maxInt(req.Attempt, 1)
	return buildAnalysisDeepeningResult(prompt, analysisLaneArchitecture, attempt, results, paths, fmt.Sprintf("readiness gate failed; retry with deeper hotspot evidence (attempt %d)", attempt)), nil
}

func architectureRelatedPatternForPath(path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" {
		return "architecture structure"
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	dir := filepath.Base(filepath.Dir(path))
	terms := []string{base}
	if dir != "." && dir != "" && dir != base {
		terms = append(terms, dir)
	}
	return strings.Join(uniqueNonEmptyStrings(terms), " ")
}

func (m *Model) executeAnalysisDeepeningTool(ctx context.Context, mgr *workspace.Manager, lane analysisLane, step int, req workspace.ToolRequest) workspace.ToolResult {
	raw := bootstrapRawToolRequest(req)
	agentName := string(lane) + "_deepening"
	m.logToolRequest(agent.ToolRequestEvent{
		Step:      -1000 - step,
		Agent:     agentName,
		Raw:       raw,
		Request:   req,
		Mode:      mgr.Mode(),
		Broad:     workspace.IsBroadInspectionRequest(req),
		Narrowing: workspace.IsNarrowingRequest(req),
		Action:    agentToolActionClass(req),
	})
	result := mgr.Execute(ctx, req)
	if strings.TrimSpace(result.Error) != "" {
		m.logToolFailureConfidence(toolFailureConfidenceSnapshot{
			Lane:             string(lane),
			Tool:             req.Tool,
			Action:           agentToolActionClass(req),
			ConfidenceImpact: "degrade_until_alternate_evidence_covers_gap",
			Reason:           truncateText(result.Error, 180),
		})
	}
	m.logToolResult(agent.ToolResultEvent{
		Step:   -1000 - step,
		Agent:  agentName,
		Result: result,
	})
	return result
}

func buildAnalysisDeepeningResult(prompt string, lane analysisLane, attempt int, results []workspace.ToolResult, seedPaths []string, reason string) analysisDeepeningResult {
	successful := make([]workspace.ToolResult, 0, len(results))
	failedTools := 0
	messages := make([]llama.ChatMessage, 0, len(results)+1)
	messages = append(messages, llama.ChatMessage{
		Role:    "system",
		Content: "Additional deterministic readiness deepening evidence follows. Prefer this evidence over the earlier low-confidence summary attempt.",
	})
	for _, result := range results {
		messages = append(messages, llama.ChatMessage{
			Role:    "system",
			Content: workspace.FormatToolResultForModel(result),
		})
		if result.Error == "" {
			successful = append(successful, result)
		} else {
			failedTools++
		}
	}
	ledger := deterministicEvidenceLedger(prompt, successful, seedPaths, "readiness_deepener")
	evidencePaths := analysisEvidencePaths(seedPaths, ledger)
	newPaths := evidenceDeltaPaths(evidencePaths, seedPaths)
	return analysisDeepeningResult{
		Lane:          string(lane),
		Attempt:       attempt,
		Attempted:     len(results) > 0,
		Exhausted:     len(successful) == 0,
		Action:        "retry_with_evidence",
		Reason:        reason,
		EvidencePaths: evidencePaths,
		NewPaths:      newPaths,
		ToolResults:   len(results),
		FailedTools:   failedTools,
		Messages:      messages,
		Ledger:        ledger,
	}
}

func evidenceDeltaPaths(after []string, before []string) []string {
	beforeSet := make(map[string]struct{}, len(before))
	for _, path := range before {
		path = normalizeGuardedPath(path)
		if path != "" {
			beforeSet[path] = struct{}{}
		}
	}
	out := make([]string, 0)
	for _, path := range after {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		if _, ok := beforeSet[path]; ok {
			continue
		}
		out = append(out, path)
	}
	return uniqueNonEmptyStrings(out)
}

func evidenceMarginalValue(newPaths []string, failedTools int, totalTools int) string {
	switch {
	case len(newPaths) >= 2:
		return "high"
	case len(newPaths) == 1:
		return "medium"
	case totalTools > 0 && failedTools == totalTools:
		return "blocked_by_tool_failures"
	default:
		return "low"
	}
}

func architectureHotspotPaths(hotspots []architectureHotspot) []string {
	paths := make([]string, 0, len(hotspots))
	for _, hotspot := range hotspots {
		paths = append(paths, hotspot.Path)
	}
	return uniqueLimitedStrings(paths, len(paths))
}

func architectureRelevantPaths(prompt string, goIndex *semantic.GoIndex, limit int) []string {
	if goIndex == nil || limit == 0 {
		return nil
	}
	return goIndex.RelevantFiles(exploreQueryTerms(prompt), limit)
}

func architectureExcludedPathsForPrompt(root string, prompt string) map[string]struct{} {
	normalized := strings.ToLower(prompt)
	if !containsAny(normalized, "git-clean", "git clean", "avoid files already modified", "avoid modified", "already modified", "git status") {
		return nil
	}
	return parseGitStatusDirtyPaths(gitStatusShort(root))
}

func gitStatusShort(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "status", "--short").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func parseGitStatusDirtyPaths(output string) map[string]struct{} {
	dirty := make(map[string]struct{})
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if path == "" {
			continue
		}
		if before, after, ok := strings.Cut(path, " -> "); ok {
			if normalized := normalizeGuardedPath(before); normalized != "" {
				dirty[normalized] = struct{}{}
			}
			path = after
		}
		if normalized := normalizeGuardedPath(path); normalized != "" {
			dirty[normalized] = struct{}{}
		}
	}
	if len(dirty) == 0 {
		return nil
	}
	return dirty
}

func filterArchitecturePaths(paths []string, excluded map[string]struct{}) []string {
	if len(excluded) == 0 {
		return paths
	}
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, skip := excluded[normalizeGuardedPath(path)]; skip {
			continue
		}
		filtered = append(filtered, path)
	}
	return filtered
}

func mapKeysSorted(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

type architectureHotspotOptions struct {
	ExcludedPaths map[string]struct{}
}

func rankArchitectureHotspots(prompt string, goIndex *semantic.GoIndex, explicitTargets []string, limit int) []architectureHotspot {
	return rankArchitectureHotspotsWithOptions(prompt, goIndex, explicitTargets, limit, architectureHotspotOptions{})
}

func rankArchitectureHotspotsWithOptions(prompt string, goIndex *semantic.GoIndex, explicitTargets []string, limit int, opts architectureHotspotOptions) []architectureHotspot {
	if goIndex == nil || limit == 0 {
		return nil
	}
	relevance := make(map[string]int)
	for rank, path := range architectureRelevantPaths(prompt, goIndex, limit*3) {
		relevance[path] = limit*3 - rank
	}
	explicit := make(map[string]struct{})
	for _, path := range explicitTargets {
		path = normalizeGuardedPath(path)
		if path != "" {
			explicit[path] = struct{}{}
		}
	}

	hotspots := make([]architectureHotspot, 0, len(goIndex.Files))
	for _, file := range goIndex.Files {
		if _, excluded := opts.ExcludedPaths[normalizeGuardedPath(file.Path)]; excluded {
			continue
		}
		if file.IsTest {
			continue
		}

		pkg := goIndex.Package[file.PathDir()]
		funcsMethods := file.Funcs + file.Methods
		localImports := architectureLocalImportCount(file, goIndex.Module)
		importerFiles := len(goImporterFiles(goIndex, file.PathDir(), 32))
		responsibilities := architectureResponsibilitySignals(file)
		broadSurface := architectureBroadSurface(file.Path, funcsMethods, file.Types, len(file.Imports), importerFiles)
		score := 0
		reasons := make([]string, 0, 5)

		score += minInt(funcsMethods*3, 72)
		if funcsMethods >= 10 {
			reasons = append(reasons, fmt.Sprintf("%d funcs/methods", funcsMethods))
		}

		score += minInt(file.Types*4, 24)
		if file.Types >= 4 {
			reasons = append(reasons, fmt.Sprintf("%d types", file.Types))
		}

		score += minInt(len(file.Imports)*2, 24)
		if len(file.Imports) >= 8 {
			reasons = append(reasons, fmt.Sprintf("%d imports", len(file.Imports)))
		}
		score += minInt(localImports*2, 18)
		if localImports >= 4 {
			reasons = append(reasons, fmt.Sprintf("%d local package imports", localImports))
		}
		score += minInt(importerFiles*2, 20)
		if importerFiles >= 4 {
			reasons = append(reasons, fmt.Sprintf("%d workspace importer files", importerFiles))
		}

		score += architectureSourceDirPriority(file.SourceDir)
		if pkg.Files >= 4 {
			score += minInt(pkg.Files*3, 18)
			reasons = append(reasons, fmt.Sprintf("package %s spans %d files", pkg.Name, pkg.Files))
		}
		if file.HasMain {
			score += 8
			reasons = append(reasons, "entrypoint responsibilities")
		}
		if broadSurface {
			score += 10
			reasons = append(reasons, "mixed orchestration/integration surface")
		}
		if len(responsibilities) >= 3 {
			score += minInt(len(responsibilities)*6, 30)
			reasons = append(reasons, "mixed responsibilities: "+strings.Join(responsibilities, ", "))
		}
		if containsAny(strings.ToLower(file.Path), "/manager", "/workspace", "/runner", "/app", "/controller") && funcsMethods >= 12 {
			score += 8
			reasons = append(reasons, "manager-style broad surface")
		}
		if bonus := relevance[file.Path]; bonus > 0 {
			score += bonus * 4
			reasons = append(reasons, "matches the current architecture question")
		}
		if _, ok := explicit[file.Path]; ok {
			score += 1000
			reasons = append([]string{"explicitly requested target"}, reasons...)
		}
		if score == 0 {
			continue
		}

		hotspots = append(hotspots, architectureHotspot{
			Path:             file.Path,
			Package:          file.Package,
			Score:            score,
			FuncsMethods:     funcsMethods,
			Types:            file.Types,
			Imports:          len(file.Imports),
			LocalImports:     localImports,
			ImporterFiles:    importerFiles,
			PackageFiles:     pkg.Files,
			BroadSurface:     broadSurface,
			Responsibilities: responsibilities,
			Reasons:          uniqueLimitedStrings(reasons, 5),
		})
	}

	slices.SortFunc(hotspots, func(a, b architectureHotspot) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		if a.FuncsMethods != b.FuncsMethods {
			return b.FuncsMethods - a.FuncsMethods
		}
		return strings.Compare(a.Path, b.Path)
	})
	if len(hotspots) > limit {
		hotspots = hotspots[:limit]
	}
	return hotspots
}

func architectureLocalImportCount(file semantic.GoFile, module string) int {
	module = strings.TrimSpace(module)
	if module == "" {
		return 0
	}
	count := 0
	for _, imp := range file.Imports {
		if strings.HasPrefix(strings.TrimSpace(imp), module+"/") {
			count++
		}
	}
	return count
}

func architectureResponsibilitySignals(file semantic.GoFile) []string {
	signalText := strings.ToLower(file.Path + " " + strings.Join(file.Imports, " "))
	for _, symbol := range file.Symbols {
		signalText += " " + strings.ToLower(symbol.Name)
		if symbol.Receiver != "" {
			signalText += " " + strings.ToLower(symbol.Receiver)
		}
	}
	type responsibilityRule struct {
		label string
		terms []string
	}
	rules := []responsibilityRule{
		{label: "ui", terms: []string{"render", "view", "style", "layout", "panel", "textarea", "viewport", "lipgloss", "bubbletea"}},
		{label: "routing", terms: []string{"route", "classify", "lane", "plan", "transition", "prompt"}},
		{label: "workspace", terms: []string{"workspace", "tool", "shell", "file", "path", "guard", "write"}},
		{label: "agent", terms: []string{"agent", "runner", "subagent", "llama", "chat", "model", "client"}},
		{label: "state", terms: []string{"state", "cache", "session", "journal", "rollback", "resume", "history"}},
		{label: "config", terms: []string{"config", "profile", "preset", "setting", "option"}},
		{label: "verification", terms: []string{"verify", "test", "diagnostic", "check", "build"}},
	}
	signals := make([]string, 0, len(rules))
	for _, rule := range rules {
		if containsAny(signalText, rule.terms...) {
			signals = append(signals, rule.label)
		}
	}
	return uniqueLimitedStrings(signals, 6)
}

func architectureBroadSurface(path string, funcsMethods int, types int, imports int, importerFiles int) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	switch {
	case funcsMethods >= 18 && types >= 3 && imports >= 8:
		return true
	case funcsMethods >= 14 && importerFiles >= 4 && imports >= 6:
		return true
	case containsAny(path, "/manager", "/workspace", "/runner", "/app", "/controller") && funcsMethods >= 12 && imports >= 6:
		return true
	default:
		return false
	}
}

func buildArchitectureHotspotBoard(hotspots []architectureHotspot) string {
	if len(hotspots) == 0 {
		return ""
	}
	lines := make([]string, 0, len(hotspots))
	for _, hotspot := range hotspots {
		line := fmt.Sprintf("- %s (score %d", hotspot.Path, hotspot.Score)
		if hotspot.Package != "" {
			line += ", package " + hotspot.Package
		}
		line += ")"
		if len(hotspot.Reasons) > 0 {
			line += ": " + strings.Join(hotspot.Reasons, ", ")
		}
		if hotspot.ImporterFiles > 0 || hotspot.LocalImports > 0 {
			line += fmt.Sprintf(" | coupling: fan-out %d local imports, fan-in %d importer files", hotspot.LocalImports, hotspot.ImporterFiles)
		}
		if len(hotspot.Responsibilities) > 0 {
			line += " | responsibilities: " + strings.Join(hotspot.Responsibilities, ", ")
		}
		line += " | direction: " + architectureHotspotDirection(hotspot)
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func architectureHotspotDirection(hotspot architectureHotspot) string {
	switch {
	case hotspot.BroadSurface && hotspot.FuncsMethods >= 18 && hotspot.Types >= 4:
		return "split orchestration/state handling from helper and rendering concerns"
	case hotspot.BroadSurface && hotspot.ImporterFiles >= 4:
		return "extract an interface or subpackage so high-dependency callers stop relying on one large coordination surface"
	case hotspot.FuncsMethods >= 18:
		return "separate routing and coordination concerns from leaf helpers"
	case hotspot.Imports >= 10:
		return "introduce a narrower facade so callers do not depend on a broad manager surface"
	case hotspot.PackageFiles >= 5:
		return "re-evaluate package boundaries and move specialized responsibilities behind adapters"
	default:
		return "tighten the file boundary around one primary responsibility"
	}
}

func (m *Model) completeAnalysisSummary(
	ctx context.Context,
	messages []llama.ChatMessage,
	opts llama.ChatOptions,
	fallback string,
	requiredPaths []string,
	ledger evidenceLedger,
	lane analysisLane,
	deepener analysisDeepener,
) (string, error) {
	modelCalls := 0
	usedDeepening := 0
	usedToolResults := 0
	newEvidenceCount := 0
	logReadiness := func(readiness analysisReadinessAssessment) {
		m.logAnalysisReadiness(readiness)
		m.logClaimConfidence(claimConfidenceFromReadiness(readiness))
	}
	logDeepening := func(deepening analysisDeepeningResult) {
		m.logAnalysisDeepening(deepening)
		m.logEvidenceDelta(evidenceDeltaSnapshot{
			Lane:             strings.TrimSpace(deepening.Lane),
			Attempt:          deepening.Attempt,
			NewEvidencePaths: uniqueNonEmptyStrings(deepening.NewPaths),
			TotalPaths:       len(deepening.EvidencePaths),
			ToolResults:      deepening.ToolResults,
			SuccessfulTools:  maxInt(deepening.ToolResults-deepening.FailedTools, 0),
			FailedTools:      deepening.FailedTools,
			MarginalValue:    evidenceMarginalValue(deepening.NewPaths, deepening.FailedTools, deepening.ToolResults),
		})
		if deepening.Attempted {
			usedDeepening++
			usedToolResults += deepening.ToolResults
			newEvidenceCount += len(deepening.NewPaths)
		}
	}
	logBudget := func(stopReason string, evidencePaths []string, exhausted bool) {
		m.logBudgetLedger(budgetLedgerSnapshot{
			Lane:                   string(lane),
			Level:                  "analysis",
			UsedModelCalls:         modelCalls,
			UsedDeepeningPasses:    usedDeepening,
			MaxDeepeningPasses:     2,
			UsedToolResults:        usedToolResults,
			NewEvidencePaths:       newEvidenceCount,
			TotalEvidencePaths:     len(uniqueNonEmptyStrings(evidencePaths)),
			StopReason:             strings.TrimSpace(stopReason),
			BudgetExhausted:        exhausted,
			NextAllowedActions:     analysisBudgetNextActions(stopReason),
			MarginalEvidencePolicy: "continue while readiness is false and deterministic deepening adds evidence before bounded pass limit",
			UserVisibleUncertainty: uncertaintyForStopReason(stopReason),
		})
	}
	if deterministicAnswer, readiness, ok := deterministicArchitectureAnswer(fallback, requiredPaths, ledger, lane); ok {
		logReadiness(readiness)
		logBudget("ready_deterministic_architecture_evidence", readiness.EvidencePaths, false)
		return deterministicAnswer, nil
	}
	reply, err := m.client.CompleteChat(ctx, messages, opts)
	if err != nil {
		return "", err
	}
	modelCalls++
	retryMessagesBase := append([]llama.ChatMessage(nil), messages...)
	retryRequiredPaths := append([]string(nil), requiredPaths...)
	retryLedger := ledger
	if answer, ok := cleanAnalysisReply(reply); ok {
		accepted := analysisReplyAcceptedWithEvidenceLedger(answer, requiredPaths, ledger, lane)
		readiness := analysisReadinessAssessmentForReply(answer, requiredPaths, ledger, lane, accepted, "retry")
		logReadiness(readiness)
		if readiness.Ready {
			logBudget("ready_initial_answer", readiness.EvidencePaths, false)
			return answer, nil
		}
		if shouldDeepenAfterReadiness(readiness) && deepener != nil {
			deepening, err := deepener(ctx, analysisDeepeningRequest{
				Attempt:       1,
				Lane:          lane,
				Readiness:     readiness,
				RequiredPaths: analysisEvidencePaths(requiredPaths, ledger),
				Ledger:        ledger,
			})
			if err != nil {
				return "", err
			}
			logDeepening(deepening)
			if deepening.Attempted {
				retryMessagesBase = append(retryMessagesBase, deepening.Messages...)
				retryRequiredPaths = uniqueLimitedStrings(append(retryRequiredPaths, deepening.EvidencePaths...), 16)
				retryLedger = mergeEvidenceLedgers(retryLedger, deepening.Ledger)
			}
		}
		if accepted {
			m.logMessage(analysisSummaryLogKey(lane), "initial summary rejected: failed readiness gate\n"+truncateText(answer, 480))
		} else {
			m.logMessage(analysisSummaryLogKey(lane), "initial summary rejected: failed lane-specific grounding or contract\n"+truncateText(answer, 480))
		}
	} else {
		logReadiness(analysisReadinessAssessmentForInvalidReply(lane, "retry", "empty or tool-formatted response"))
		m.logMessage(analysisSummaryLogKey(lane), "initial summary rejected: empty or tool-formatted response")
	}

	retryMessages := append([]llama.ChatMessage(nil), retryMessagesBase...)
	requirement := analysisRetryRequirement(lane, analysisEvidencePaths(retryRequiredPaths, retryLedger))
	retryMessages = append(retryMessages, llama.ChatMessage{
		Role:    "system",
		Content: requirement,
	})
	retryReply, err := m.client.CompleteChat(ctx, retryMessages, opts)
	if err != nil {
		return "", err
	}
	modelCalls++
	if answer, ok := cleanAnalysisReply(retryReply); ok {
		accepted := analysisReplyAcceptedWithEvidenceLedger(answer, retryRequiredPaths, retryLedger, lane)
		nextAction := "fallback"
		if deepener != nil {
			nextAction = "retry"
		}
		readiness := analysisReadinessAssessmentForReply(answer, retryRequiredPaths, retryLedger, lane, accepted, nextAction)
		logReadiness(readiness)
		if readiness.Ready {
			logBudget("ready_after_retry", readiness.EvidencePaths, false)
			return answer, nil
		}
		if shouldDeepenAfterReadiness(readiness) && deepener != nil {
			deepening, err := deepener(ctx, analysisDeepeningRequest{
				Attempt:       2,
				Lane:          lane,
				Readiness:     readiness,
				RequiredPaths: analysisEvidencePaths(retryRequiredPaths, retryLedger),
				Ledger:        retryLedger,
			})
			if err != nil {
				return "", err
			}
			logDeepening(deepening)
			if deepening.Attempted {
				retryRequiredPaths = uniqueLimitedStrings(append(retryRequiredPaths, deepening.EvidencePaths...), 20)
				retryLedger = mergeEvidenceLedgers(retryLedger, deepening.Ledger)
				finalMessages := append([]llama.ChatMessage(nil), retryMessagesBase...)
				finalMessages = append(finalMessages, deepening.Messages...)
				finalMessages = append(finalMessages, llama.ChatMessage{
					Role:    "system",
					Content: analysisRetryRequirement(lane, analysisEvidencePaths(retryRequiredPaths, retryLedger)) + " This is the final bounded readiness attempt before the harness falls back to deterministic evidence.",
				})
				finalReply, err := m.client.CompleteChat(ctx, finalMessages, opts)
				if err != nil {
					return "", err
				}
				modelCalls++
				if finalAnswer, finalOK := cleanAnalysisReply(finalReply); finalOK {
					finalAccepted := analysisReplyAcceptedWithEvidenceLedger(finalAnswer, retryRequiredPaths, retryLedger, lane)
					finalReadiness := analysisReadinessAssessmentForReply(finalAnswer, retryRequiredPaths, retryLedger, lane, finalAccepted, "fallback")
					logReadiness(finalReadiness)
					if finalReadiness.Ready {
						logBudget("ready_after_final_deepening", finalReadiness.EvidencePaths, false)
						return finalAnswer, nil
					}
					m.logMessage(analysisSummaryLogKey(lane), "final summary rejected: failed readiness gate\n"+truncateText(finalAnswer, 480))
				} else {
					logReadiness(analysisReadinessAssessmentForInvalidReply(lane, "fallback", "empty or tool-formatted response"))
					m.logMessage(analysisSummaryLogKey(lane), "final summary rejected: empty or tool-formatted response")
				}
			}
		}
		if accepted {
			m.logMessage(analysisSummaryLogKey(lane), "retry summary rejected: failed readiness gate\n"+truncateText(answer, 480))
		} else {
			m.logMessage(analysisSummaryLogKey(lane), "retry summary rejected: failed lane-specific grounding or contract\n"+truncateText(answer, 480))
		}
	} else {
		logReadiness(analysisReadinessAssessmentForInvalidReply(lane, "fallback", "empty or tool-formatted response"))
		m.logMessage(analysisSummaryLogKey(lane), "retry summary rejected: empty or tool-formatted response")
	}
	if strings.TrimSpace(fallback) != "" {
		fallbackAnswer := strings.TrimSpace(fallback)
		fallbackAccepted := analysisReplyAcceptedWithEvidenceLedger(fallbackAnswer, retryRequiredPaths, retryLedger, lane)
		fallbackReadiness := analysisReadinessAssessmentForReply(fallbackAnswer, retryRequiredPaths, retryLedger, lane, fallbackAccepted, "answer")
		logReadiness(fallbackReadiness)
		logBudget("fallback_after_exhausted_readiness_attempts", fallbackReadiness.EvidencePaths, usedDeepening >= 2)
		return fallbackAnswer, nil
	}
	logBudget("no_usable_summary", analysisEvidencePaths(retryRequiredPaths, retryLedger), usedDeepening >= 2)
	return "", fmt.Errorf("analysis lane returned no usable summary")
}

func cleanAnalysisReply(reply string) (string, bool) {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "", false
	}
	if _, _, ok, err := workspace.ParseToolRequest(reply); err != nil || ok {
		return "", false
	}
	return reply, true
}

func deterministicArchitectureAnswer(fallback string, requiredPaths []string, ledger evidenceLedger, lane analysisLane) (string, analysisReadinessAssessment, bool) {
	if lane != analysisLaneArchitecture {
		return "", analysisReadinessAssessment{}, false
	}
	answer := strings.TrimSpace(fallback)
	if answer == "" {
		return "", analysisReadinessAssessment{}, false
	}
	accepted := analysisReplyAcceptedWithEvidenceLedger(answer, requiredPaths, ledger, lane)
	readiness := analysisReadinessAssessmentForReply(answer, requiredPaths, ledger, lane, accepted, "answer")
	if !readiness.Ready || readiness.Confidence != "high" {
		return "", analysisReadinessAssessment{}, false
	}
	return answer, readiness, true
}

type analysisReadinessAssessment struct {
	Lane          string   `json:"lane,omitempty"`
	Ready         bool     `json:"ready"`
	Confidence    string   `json:"confidence,omitempty"`
	Action        string   `json:"action,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	EvidencePaths []string `json:"evidence_paths,omitempty"`
}

type analysisDeepener func(context.Context, analysisDeepeningRequest) (analysisDeepeningResult, error)

type analysisDeepeningRequest struct {
	Attempt       int
	Lane          analysisLane
	Readiness     analysisReadinessAssessment
	RequiredPaths []string
	Ledger        evidenceLedger
}

type analysisDeepeningResult struct {
	Lane          string              `json:"lane,omitempty"`
	Attempt       int                 `json:"attempt,omitempty"`
	Attempted     bool                `json:"attempted"`
	Exhausted     bool                `json:"exhausted,omitempty"`
	Action        string              `json:"action,omitempty"`
	Reason        string              `json:"reason,omitempty"`
	EvidencePaths []string            `json:"evidence_paths,omitempty"`
	NewPaths      []string            `json:"new_paths,omitempty"`
	ToolResults   int                 `json:"tool_results,omitempty"`
	FailedTools   int                 `json:"failed_tools,omitempty"`
	Messages      []llama.ChatMessage `json:"-"`
	Ledger        evidenceLedger      `json:"-"`
}

func analysisReadinessAssessmentForReply(
	reply string,
	requiredPaths []string,
	ledger evidenceLedger,
	lane analysisLane,
	contractAccepted bool,
	notReadyAction string,
) analysisReadinessAssessment {
	evidencePaths := analysisEvidencePaths(requiredPaths, ledger)
	reason := "summary satisfies lane contract and evidence gate"
	confidence := "medium"
	if contractAccepted && len(evidencePaths) > 0 && analysisReplyGrounded(reply, evidencePaths) {
		confidence = "high"
	}
	if lowReason, low := lowConfidenceReadinessReason(reply); low {
		confidence = "low"
		reason = lowReason
	}
	if !contractAccepted {
		reason = "summary does not satisfy lane contract or evidence gate"
	}
	ready := contractAccepted && confidence != "low"
	action := "answer"
	if !ready {
		action = strings.TrimSpace(notReadyAction)
		if action == "" {
			action = "retry"
		}
	}
	return analysisReadinessAssessment{
		Lane:          string(lane),
		Ready:         ready,
		Confidence:    confidence,
		Action:        action,
		Reason:        reason,
		EvidencePaths: evidencePaths,
	}
}

func analysisReadinessAssessmentForInvalidReply(lane analysisLane, action string, reason string) analysisReadinessAssessment {
	action = strings.TrimSpace(action)
	if action == "" {
		action = "retry"
	}
	return analysisReadinessAssessment{
		Lane:       string(lane),
		Ready:      false,
		Confidence: "low",
		Action:     action,
		Reason:     strings.TrimSpace(reason),
	}
}

func claimConfidenceFromReadiness(readiness analysisReadinessAssessment) claimConfidenceSnapshot {
	unresolved := make([]string, 0, 2)
	if !readiness.Ready && strings.TrimSpace(readiness.Reason) != "" {
		unresolved = append(unresolved, readiness.Reason)
	}
	supportedClaims := 0
	if readiness.Ready {
		supportedClaims = 1
	}
	basis := "contract acceptance plus grounded evidence path coverage"
	if len(readiness.EvidencePaths) == 0 {
		basis = "contract acceptance without explicit evidence paths"
	}
	return claimConfidenceSnapshot{
		Lane:                  readiness.Lane,
		Confidence:            readiness.Confidence,
		SupportedClaims:       supportedClaims,
		EvidencePaths:         uniqueNonEmptyStrings(readiness.EvidencePaths),
		UnresolvedUncertainty: uniqueNonEmptyStrings(unresolved),
		CalibrationBasis:      basis,
	}
}

func analysisBudgetNextActions(stopReason string) []string {
	return frontier.AnalysisBudgetNextActions(stopReason)
}

func shouldDeepenAfterReadiness(readiness analysisReadinessAssessment) bool {
	if readiness.Ready {
		return false
	}
	action := strings.ToLower(strings.TrimSpace(readiness.Action))
	return action == "" || action == "retry"
}

func lowConfidenceReadinessReason(reply string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(reply))
	switch {
	case containsAny(normalized, "not confident", "not enough evidence", "insufficient evidence", "insufficient context"):
		return "response explicitly reports insufficient evidence", true
	case containsAny(normalized, "can't determine", "cannot determine", "unable to determine", "unable to confirm", "cannot confirm"):
		return "response cannot determine or confirm the claim", true
	case containsAny(normalized, "need more evidence", "need to inspect", "would need to inspect", "would need more"):
		return "response asks for more evidence before it is ready", true
	default:
		return "", false
	}
}

func (m *Model) logAnalysisReadiness(readiness analysisReadinessAssessment) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(readiness)
	if err != nil {
		return
	}
	m.logMessage("readiness_check", string(data))
}

func (m *Model) logAnalysisDeepening(deepening analysisDeepeningResult) {
	if m == nil || m.debugLogger == nil {
		return
	}
	payload := struct {
		Lane          string   `json:"lane,omitempty"`
		Attempt       int      `json:"attempt,omitempty"`
		Attempted     bool     `json:"attempted"`
		Exhausted     bool     `json:"exhausted,omitempty"`
		Action        string   `json:"action,omitempty"`
		Reason        string   `json:"reason,omitempty"`
		EvidencePaths []string `json:"evidence_paths,omitempty"`
		NewPaths      []string `json:"new_paths,omitempty"`
		ToolResults   int      `json:"tool_results,omitempty"`
		FailedTools   int      `json:"failed_tools,omitempty"`
	}{
		Lane:          strings.TrimSpace(deepening.Lane),
		Attempt:       deepening.Attempt,
		Attempted:     deepening.Attempted,
		Exhausted:     deepening.Exhausted,
		Action:        strings.TrimSpace(deepening.Action),
		Reason:        strings.TrimSpace(deepening.Reason),
		EvidencePaths: uniqueNonEmptyStrings(deepening.EvidencePaths),
		NewPaths:      uniqueNonEmptyStrings(deepening.NewPaths),
		ToolResults:   deepening.ToolResults,
		FailedTools:   deepening.FailedTools,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	m.logMessage("deepening_attempt", string(data))
}

func analysisReplyGrounded(reply string, requiredPaths []string) bool {
	if len(requiredPaths) == 0 {
		return true
	}
	normalized := strings.ToLower(reply)
	for _, path := range requiredPaths {
		path = strings.ToLower(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		if strings.Contains(normalized, path) {
			return true
		}
	}
	return false
}

func analysisEvidencePaths(requiredPaths []string, ledger evidenceLedger) []string {
	paths := make([]string, 0, len(requiredPaths)+len(ledger.VerifiedPaths))
	paths = append(paths, requiredPaths...)
	paths = append(paths, ledger.VerifiedPaths...)
	return uniqueLimitedStrings(paths, 12)
}

func analysisReplyAcceptedWithEvidenceLedger(reply string, requiredPaths []string, ledger evidenceLedger, lane analysisLane) bool {
	return analysisReplyAccepted(reply, analysisEvidencePaths(requiredPaths, ledger), lane)
}

func analysisReplyAccepted(reply string, requiredPaths []string, lane analysisLane) bool {
	if !analysisReplyGrounded(reply, requiredPaths) {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(reply))
	switch lane {
	case analysisLaneArchitecture:
		return containsAny(
			normalized,
			"architecture",
			"structur",
			"boundary",
			"boundaries",
			"modular",
			"responsibilit",
			"opportunit",
			"coupling",
			"ownership",
			"refactor",
			"split",
			"decompose",
			"extension",
			"extensible",
		)
	case analysisLaneReview:
		return containsAny(
			normalized,
			"issue",
			"finding",
			"cleanup",
			"bug",
			"regression",
			"because",
			"evidence",
			"confidence",
			"next step",
		)
	case analysisLaneExplore:
		return containsAny(
			normalized,
			"verified",
			"confirmed",
			"evidence",
			"observed",
			"found",
			"read",
			"from ",
			"grounded",
		) && containsAny(
			normalized,
			"inferred",
			"inference",
			"likely",
			"appears",
			"uncertain",
			"confidence",
			"verified",
			"confirmed",
		)
	case analysisLaneLocate:
		return containsAny(normalized, "best target", "exact file path", "snippet", "line number") &&
			containsAny(normalized, "why", "reason", "region", "best target")
	default:
		return true
	}
}

func analysisSummaryLogKey(lane analysisLane) string {
	switch lane {
	case analysisLaneArchitecture:
		return "architecture_summary_rejected"
	case analysisLaneReview:
		return "review_summary_rejected"
	case analysisLaneExplore:
		return "explore_summary_rejected"
	default:
		return "analysis_summary_rejected"
	}
}

func analysisRetryRequirement(lane analysisLane, requiredPaths []string) string {
	subject := "analysis"
	pathLabel := "relevant"
	switch lane {
	case analysisLaneArchitecture:
		subject = "architecture"
		pathLabel = "hotspot"
	case analysisLaneReview:
		subject = "review"
		pathLabel = "candidate"
	case analysisLaneExplore:
		subject = "explore"
		pathLabel = "evidence"
	}
	requirement := "Do not emit tool requests in this lane. Reply now with a concise, non-empty " + subject + " summary based only on the provided evidence."
	if len(requiredPaths) > 0 {
		requirement += " Mention at least one exact " + pathLabel + " path from this set: " + strings.Join(requiredPaths, ", ") + "."
	}
	requirement += " The response is ready only if it states evidence-backed claims; do not present a low-confidence or insufficient-evidence answer as final."
	switch lane {
	case analysisLaneArchitecture:
		requirement += " Focus on structure, boundaries, extension points, or refactor direction rather than bug triage."
	case analysisLaneReview:
		requirement += " Prefer one concrete issue or cleanup candidate over a broad structural sweep."
	case analysisLaneExplore:
		requirement += " Include the major files or packages involved, and explicitly separate what was verified from what is inferred."
	}
	return requirement
}

type evidenceLedger struct {
	VerifiedPaths []string
	Tools         []string
	Errors        []string
	InferredGaps  []string
}

type languageCapability struct {
	Language string
	Signals  []string
	Tools    []string
}

func appendLanguageCapabilityRegistry(profile string, root string) string {
	registry := renderLanguageCapabilityRegistry(detectLanguageCapabilities(root))
	if strings.TrimSpace(registry) == "" {
		return profile
	}
	if strings.TrimSpace(profile) == "" {
		return "Language capability registry:\n" + registry
	}
	return profile + "\n\nLanguage capability registry:\n" + registry
}

func buildEvidenceLedger(prompt string, results []workspace.ToolResult) evidenceLedger {
	toolsSeen := make(map[string]struct{})
	tools := make([]string, 0, 6)
	errors := make([]string, 0, 3)
	for _, result := range results {
		tool := strings.TrimSpace(result.Tool)
		if tool != "" {
			if _, ok := toolsSeen[tool]; !ok {
				toolsSeen[tool] = struct{}{}
				tools = append(tools, tool)
			}
		}
		if errText := strings.TrimSpace(result.Error); errText != "" {
			errors = append(errors, truncateText(errText, 140))
		}
	}
	ledger := evidenceLedger{
		VerifiedPaths: exploreEvidencePaths(results),
		Tools:         uniqueLimitedStrings(tools, 8),
		Errors:        uniqueLimitedStrings(errors, 4),
	}
	switch {
	case len(ledger.VerifiedPaths) == 0:
		ledger.InferredGaps = append(ledger.InferredGaps, "no concrete source path was captured in deterministic evidence")
	case strings.TrimSpace(prompt) != "":
		ledger.InferredGaps = append(ledger.InferredGaps, "relationships between cited files still need targeted symbol or call-flow validation")
	default:
		ledger.InferredGaps = append(ledger.InferredGaps, "implementation should verify the exact target before editing")
	}
	return ledger
}

func deterministicEvidenceLedger(prompt string, results []workspace.ToolResult, seedPaths []string, toolNames ...string) evidenceLedger {
	ledger := buildEvidenceLedger(prompt, results)
	if len(seedPaths) > 0 {
		priorityPaths := append([]string(nil), seedPaths...)
		priorityPaths = append(priorityPaths, ledger.VerifiedPaths...)
		ledger.VerifiedPaths = uniqueLimitedStrings(priorityPaths, 12)
	}
	if len(toolNames) > 0 {
		ledger.Tools = uniqueLimitedStrings(append(append([]string(nil), ledger.Tools...), toolNames...), 8)
	}
	if len(ledger.VerifiedPaths) > 0 && len(ledger.InferredGaps) > 0 {
		filtered := make([]string, 0, len(ledger.InferredGaps))
		for _, gap := range ledger.InferredGaps {
			if strings.Contains(strings.ToLower(gap), "no concrete source path") {
				continue
			}
			filtered = append(filtered, gap)
		}
		ledger.InferredGaps = filtered
	}
	if len(ledger.InferredGaps) == 0 {
		ledger.InferredGaps = append(ledger.InferredGaps, "relationships between cited files still need targeted symbol or call-flow validation")
	}
	return ledger
}

func mergeEvidenceLedgers(base evidenceLedger, extra evidenceLedger) evidenceLedger {
	return evidenceLedger{
		VerifiedPaths: uniqueLimitedStrings(append(append([]string(nil), base.VerifiedPaths...), extra.VerifiedPaths...), 16),
		Tools:         uniqueLimitedStrings(append(append([]string(nil), base.Tools...), extra.Tools...), 12),
		Errors:        uniqueLimitedStrings(append(append([]string(nil), base.Errors...), extra.Errors...), 6),
		InferredGaps:  uniqueLimitedStrings(append(append([]string(nil), base.InferredGaps...), extra.InferredGaps...), 8),
	}
}

func evidenceLedgerHasContent(ledger evidenceLedger) bool {
	return len(ledger.VerifiedPaths) > 0 || len(ledger.Tools) > 0 || len(ledger.Errors) > 0 || len(ledger.InferredGaps) > 0
}

func exploreEvidencePaths(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 8)
	add := func(path string) {
		path = normalizeGuardedPath(path)
		if path == "" || path == "." {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for _, result := range results {
		for _, path := range promptMentionedWorkspacePaths(result.Summary) {
			add(path)
		}
		switch result.Tool {
		case "list_files", "search_files":
			for _, path := range parseDiscoveredFileList(result.Output) {
				add(path)
			}
		case "search_content", "rg":
			for _, path := range parseSearchMatchPaths(result.Output) {
				add(path)
			}
		case "go_entrypoints", "go_find_symbol", "go_find_function", "go_find_type", "go_related_files", "go_importers":
			for _, path := range parseDiscoveredFileList(result.Output) {
				add(path)
			}
			for _, path := range parseSearchMatchPaths(result.Output) {
				add(path)
			}
		}
		if len(paths) >= 12 {
			break
		}
	}
	return uniqueLimitedStrings(paths, 12)
}

func buildExploreFallbackSummary(prompt string, bootstrap exploreBootstrap) string {
	ledger := bootstrap.ledger
	if !evidenceLedgerHasContent(ledger) {
		ledger = buildEvidenceLedger(prompt, bootstrap.results)
	}
	paths := ledger.VerifiedPaths
	var b strings.Builder
	b.WriteString("Grounded exploration summary.\n\n")
	if strings.TrimSpace(prompt) != "" {
		b.WriteString("Request:\n")
		b.WriteString("- ")
		b.WriteString(truncateText(strings.TrimSpace(prompt), 180))
		b.WriteString("\n\n")
	}
	if rendered := renderEvidenceLedger(ledger); rendered != "" {
		b.WriteString(rendered)
		b.WriteString("\n\n")
	}
	b.WriteString("Verified evidence:\n")
	if len(paths) == 0 {
		b.WriteString("- The deterministic explore bootstrap ran, but it did not expose a concrete source path in the captured evidence.\n")
	} else {
		for _, path := range uniqueLimitedStrings(paths, 6) {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString(" was discovered or inspected during explore bootstrap.\n")
		}
	}
	b.WriteString("\nInference:\n")
	if len(paths) == 0 {
		b.WriteString("- Any code relationship beyond the bootstrap result needs another targeted search or read before implementation.\n")
	} else {
		b.WriteString("- Treat relationships between these files as inferred until a targeted read or symbol query confirms the exact call flow.\n")
	}
	b.WriteString("\nNext validation:\n")
	if len(paths) > 0 {
		b.WriteString("- Open the most relevant listed path and verify the specific symbol or behavior before making changes.")
	} else {
		b.WriteString("- Run a narrower search for the user-requested term, symbol, or behavior before making changes.")
	}
	return strings.TrimSpace(b.String())
}

func (m *Model) currentExploreCache(exploreWorkspace *workspace.Manager) (*exploreSessionCache, bool) {
	if m.exploreCache == nil {
		return nil, false
	}
	if m.exploreCache.workspaceRoot != exploreWorkspace.Root() || m.exploreCache.workspaceCWD != exploreWorkspace.CWD() {
		return nil, false
	}
	if token := currentWorkspaceStateToken(exploreWorkspace.Root()); token != "" && token != m.exploreCache.stateToken {
		return nil, false
	}
	return m.exploreCache, true
}

func (m *Model) setExploreCache(
	exploreWorkspace *workspace.Manager,
	repoProfile string,
	discoveredFiles map[string]struct{},
	discoveredDirs map[string]struct{},
	goIndex *semantic.GoIndex,
) {
	m.exploreCache = &exploreSessionCache{
		workspaceRoot: exploreWorkspace.Root(),
		workspaceCWD:  exploreWorkspace.CWD(),
		stateToken:    currentWorkspaceStateToken(exploreWorkspace.Root()),
		repoProfile:   strings.TrimSpace(repoProfile),
		discovered:    sortedStringKeys(discoveredFiles),
		directories:   sortedStringKeys(discoveredDirs),
		goIndex:       goIndex,
	}
}

func buildExploreRepoProfile(overview string, results []workspace.ToolResult) string {
	var b strings.Builder
	b.WriteString("Top-level layout:\n")
	for _, line := range strings.Split(compactOutput(strings.TrimSpace(overview), 10), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}

	count := 0
	for _, result := range results {
		if result.Error != "" {
			continue
		}
		switch result.Tool {
		case "read_file", "search_content", "search_files":
		default:
			continue
		}
		if count == 0 {
			b.WriteString("\nKey evidence:\n")
		}
		b.WriteString("- ")
		b.WriteString(result.Summary)
		if output := compactOutput(result.Output, 4); output != "" {
			b.WriteString("\n")
			b.WriteString(output)
		}
		b.WriteString("\n")
		count++
		if count >= 4 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func confirmCandidateFinding(prompt string, goIndex *semantic.GoIndex, candidate reviewCandidate) *reviewFinding {
	file := goIndex.FileByPath(candidate.Path)
	if file == nil {
		return nil
	}

	surface := file.Funcs + file.Methods
	exported := exportedSymbolCountInFile(file)
	longest := longestSymbolInFile(file)
	longestSpan := symbolSpan(longest)
	isSmellPrompt := looksLikeCodeSmellPrompt(prompt)

	if isSmellPrompt {
		switch {
		case surface >= 160 || (surface >= 120 && file.Types >= 12):
			evidence := []string{
				fmt.Sprintf("%s contains %d funcs/methods and %d types in a single file", candidate.Path, surface, file.Types),
			}
			if longest != nil && longestSpan >= 60 {
				evidence = append(evidence, fmt.Sprintf("%s spans lines %d-%d (%d lines)", reviewSymbolDisplayName(*longest), longest.StartLine, longest.EndLine, longestSpan))
			}
			if len(file.Imports) >= 18 {
				evidence = append(evidence, fmt.Sprintf("the file also imports %d packages", len(file.Imports)))
			}
			return &reviewFinding{
				Title:      "Overgrown source file",
				Path:       candidate.Path,
				Reason:     "This file is carrying too many responsibilities and is likely harder to maintain or change safely.",
				Evidence:   evidence,
				Confidence: "high",
				Scope:      "large",
				Plan: []string{
					"Identify one cohesive slice inside the file, starting with the longest helper cluster or bootstrap path.",
					"Extract that slice behind a narrow helper type or new package-level module without changing outward behavior.",
					"Move callers over incrementally and keep the remaining file focused on orchestration only.",
					"Verify the extracted seam with a focused build/test pass before attempting a second split.",
				},
				NextStep: "Review the split plan, choose the first extraction seam, and then implement that slice explicitly.",
			}
		case longest != nil && longestSpan >= 110:
			return &reviewFinding{
				Title:  "Oversized function body",
				Path:   candidate.Path,
				Symbol: reviewSymbolDisplayName(*longest),
				Reason: "The function body is unusually large and likely mixes orchestration with lower-level details.",
				Evidence: []string{
					fmt.Sprintf("%s spans lines %d-%d (%d lines)", reviewSymbolDisplayName(*longest), longest.StartLine, longest.EndLine, longestSpan),
					fmt.Sprintf("%s still lives inside a file with %d funcs/methods", candidate.Path, surface),
				},
				Confidence: "high",
				Scope:      "medium",
				Plan: []string{
					"Split the function into one or two helpers around the dominant sub-steps already visible in the body.",
					"Keep the public entrypoint intact and move only internal logic on the first pass.",
					"Re-run the focused build/test path after the extraction.",
				},
				NextStep: "If you want this fixed now, extract one helper seam first rather than rewriting the whole function.",
			}
		case exported >= 28 && surface >= 90:
			return &reviewFinding{
				Title:  "Excessive exported surface",
				Path:   candidate.Path,
				Reason: "This file exposes a very large public surface, which usually signals muddled responsibilities or weak boundaries.",
				Evidence: []string{
					fmt.Sprintf("%s exposes %d exported symbols", candidate.Path, exported),
					fmt.Sprintf("the same file also contains %d funcs/methods", surface),
				},
				Confidence: "medium",
				Scope:      "medium",
				Plan: []string{
					"Identify which exported symbols are truly part of the file's public contract.",
					"Move unrelated helpers or internal-only symbols behind narrower files or unexport them where possible.",
					"Re-check downstream callers after shrinking the surface.",
				},
				NextStep: "Narrow the public API before adding more behavior to this file.",
			}
		}
	}

	if len(candidate.Hits) > 0 {
		hit := firstNonMetaHit(candidate.Hits)
		if hit != nil {
			return &reviewFinding{
				Title:      "Suspicious marker-backed issue",
				Path:       candidate.Path,
				Reason:     "This file contains an explicit marker or panic/error pattern that should be reviewed before more work lands nearby.",
				Evidence:   []string{fmt.Sprintf("line %d: %s", hit.Line, truncateText(hit.Text, 140))},
				Confidence: "medium",
				Scope:      "small",
				NextStep:   "Inspect the cited line and decide whether it reflects dead code, a real bug path, or cleanup debt that should be resolved now.",
			}
		}
	}

	return nil
}

func buildArchitectureFallbackSummary(prompt string, hotspots []architectureHotspot, hotspotBoard string, repoProfile string, note string, ledger evidenceLedger) string {
	var b strings.Builder
	targetedHotspot, hasTargetedHotspot := explicitlyRequestedArchitectureHotspot(hotspots)
	focusedPlan := hasTargetedHotspot && architecturePromptWantsFocusedTargetPlan(prompt)
	if focusedPlan {
		b.WriteString("Focused architecture plan.\n\n")
	} else {
		b.WriteString("Deterministic architecture assessment.\n\n")
	}
	if strings.TrimSpace(note) != "" {
		b.WriteString(strings.TrimSpace(note))
		b.WriteString("\n\n")
	}
	if evidenceLedgerHasContent(ledger) {
		b.WriteString(renderEvidenceLedger(ledger))
		b.WriteString("\n\n")
	}
	b.WriteString("Confidence:\n")
	switch {
	case len(hotspots) > 0 && strings.TrimSpace(hotspotBoard) != "":
		b.WriteString("- high: Go semantic indexing, hotspot scoring, repository profile, and direct file reads converge on the same structural pressure points.\n\n")
	case len(hotspots) > 0:
		b.WriteString("- medium-high: hotspot scoring found concrete paths, but relationship evidence is still limited.\n\n")
	default:
		b.WriteString("- medium: repository structure was profiled, but no concrete hotspot dominated.\n\n")
	}
	if architecturePromptWantsBoundedOneFilePlan(prompt) && len(hotspots) > 0 {
		b.WriteString(renderBoundedOneFileArchitecturePlan(hotspots[0]))
		return strings.TrimSpace(b.String())
	}
	if hasTargetedHotspot {
		b.WriteString(renderTargetedArchitecturePlan(targetedHotspot, focusedPlan))
		if focusedPlan {
			return strings.TrimSpace(b.String())
		}
	}
	if strings.TrimSpace(repoProfile) != "" {
		b.WriteString("Structural baseline:\n")
		b.WriteString(compactOutput(repoProfile, 14))
		b.WriteString("\n\n")
		if capabilities := architectureCapabilityNames(repoProfile); len(capabilities) > 0 {
			b.WriteString("Detected capability surface:\n")
			b.WriteString("- ")
			b.WriteString(strings.Join(uniqueLimitedStrings(capabilities, 6), ", "))
			b.WriteString("\n\n")
		}
	}
	if len(hotspots) > 0 {
		b.WriteString(architectureOpportunityHeading(repoProfile))
		b.WriteString(":\n")
		for _, hotspot := range hotspots[:minInt(3, len(hotspots))] {
			b.WriteString("- ")
			b.WriteString(hotspot.Path)
			if len(hotspot.Reasons) > 0 {
				b.WriteString(" because ")
				b.WriteString(strings.Join(uniqueLimitedStrings(hotspot.Reasons, 3), ", "))
			}
			b.WriteString("; suggested direction: ")
			b.WriteString(architectureHotspotDirection(hotspot))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(hotspotBoard) != "" {
		b.WriteString("Architecture hotspot board:\n")
		b.WriteString(strings.TrimSpace(hotspotBoard))
		b.WriteString("\n\n")
	}
	if frontier.LooksLikeArchitecturePrompt(strings.ToLower(prompt)) {
		b.WriteString("Recommended next step:\n")
		b.WriteString("- validate the top hotspot boundaries and split one responsibility at a time instead of attempting a repo-wide refactor")
	} else {
		b.WriteString("Recommended next step:\n")
		b.WriteString("- inspect the top hotspot paths and confirm which structural boundary should change first")
	}
	return strings.TrimSpace(b.String())
}

func architecturePromptWantsFocusedTargetPlan(prompt string) bool {
	return frontier.ArchitecturePromptWantsFocusedTargetPlan(prompt)
}

func architecturePromptWantsBoundedOneFilePlan(prompt string) bool {
	return frontier.ArchitecturePromptWantsBoundedOneFilePlan(prompt)
}

func renderBoundedOneFileArchitecturePlan(hotspot architectureHotspot) string {
	var b strings.Builder
	b.WriteString("Focused refactor plan:\n")
	b.WriteString("- Target: ")
	b.WriteString(hotspot.Path)
	b.WriteString("\n")
	b.WriteString("- Scope: edit only this existing Go file; do not create files or move declarations.\n")
	if len(hotspot.Reasons) > 0 {
		b.WriteString("- Why safe: this is a git-clean candidate selected from deterministic hotspot scoring because ")
		b.WriteString(strings.Join(uniqueLimitedStrings(hotspot.Reasons, 3), ", "))
		b.WriteString(". Keep exported APIs and call sites unchanged.\n")
	} else {
		b.WriteString("- Why safe: this is a git-clean candidate selected from deterministic hotspot scoring. Keep exported APIs and call sites unchanged.\n")
	}
	b.WriteString("- Candidate intended change: inspect the local helper/control-flow cluster in ")
	b.WriteString(hotspot.Path)
	b.WriteString(" and consolidate the smallest repeated normalization or branching logic into an unexported helper in the same file.\n")
	b.WriteString("- Verification: run go test ./")
	b.WriteString(packageTestPathForFile(hotspot.Path))
	b.WriteString(", then go test ./... if behavior-affecting code changed.\n")
	return b.String()
}

func packageTestPathForFile(path string) string {
	dir := filepath.Dir(normalizeGuardedPath(path))
	if dir == "." || dir == "" {
		return "..."
	}
	return filepath.ToSlash(dir)
}

func renderTargetedArchitecturePlan(hotspot architectureHotspot, focused bool) string {
	var b strings.Builder
	if focused {
		b.WriteString("Focused refactor plan:\n")
	} else {
		b.WriteString("Targeted refactor approach:\n")
	}
	b.WriteString("- Target: ")
	b.WriteString(hotspot.Path)
	b.WriteString("\n")
	if len(hotspot.Responsibilities) > 0 {
		b.WriteString("- Current mixed responsibilities: ")
		b.WriteString(strings.Join(hotspot.Responsibilities, ", "))
		b.WriteString("\n")
	}
	b.WriteString("- First boundary: ")
	b.WriteString(architectureHotspotDirection(hotspot))
	b.WriteString("\n")
	if focused {
		b.WriteString("- Phase 1: stage 1 scope is to edit only this existing Go file; do not create files or move declarations.\n")
		b.WriteString("- Phase 2: inspect the local helper/control-flow cluster in ")
		b.WriteString(hotspot.Path)
		b.WriteString(" and consolidate the smallest repeated normalization or branching logic into an unexported helper in the same file, keeping exported APIs and call sites unchanged.\n")
		b.WriteString("- Verification: Phase 3 runs go test ./")
		b.WriteString(packageTestPathForFile(hotspot.Path))
		b.WriteString(", then go test ./... if behavior-affecting code changed.\n\n")
		return b.String()
	}
	b.WriteString("- Safe sequence: extract one responsibility at a time, keep existing callers stable, run focused tests after each move, then repeat only after the boundary is proven.\n\n")
	return b.String()
}

func explicitlyRequestedArchitectureHotspot(hotspots []architectureHotspot) (architectureHotspot, bool) {
	for _, hotspot := range hotspots {
		for _, reason := range hotspot.Reasons {
			if strings.EqualFold(strings.TrimSpace(reason), "explicitly requested target") {
				return hotspot, true
			}
		}
	}
	return architectureHotspot{}, false
}
func architectureOpportunityHeading(repoProfile string) string {
	capabilities := architectureCapabilityNames(repoProfile)
	has := func(name string) bool {
		for _, capability := range capabilities {
			if strings.EqualFold(capability, name) {
				return true
			}
		}
		return false
	}
	switch {
	case has("c/c++") && !has("go"):
		return "Highest-value C/C++ structural opportunities"
	case has("go") && !has("c/c++"):
		return "Highest-value Go structural opportunities"
	case has("prompts") && !has("go") && !has("c/c++"):
		return "Highest-value prompt-system structural opportunities"
	case has("docs") && !has("go") && !has("c/c++"):
		return "Highest-value documentation structural opportunities"
	default:
		return "Highest-value structural opportunities"
	}
}

func architectureCapabilityNames(repoProfile string) []string {
	_, tail, ok := strings.Cut(repoProfile, "Language capability registry:")
	if !ok {
		return nil
	}
	names := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(tail, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		name := line
		if before, _, ok := strings.Cut(name, " tools:"); ok {
			name = before
		}
		if before, _, ok := strings.Cut(name, "|"); ok {
			name = before
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	return names
}

func issueSearchPattern(prompt string) string {
	return frontier.IssueSearchPattern(prompt)
}

func exploreBootstrapReadBudget(level workspace.ExploreThoroughness) int {
	return frontier.ExploreBootstrapReadBudget(level)
}

func exploreKeywordSearchPathBudget(level workspace.ExploreThoroughness) int {
	return frontier.ExploreKeywordSearchPathBudget(level)
}

func exploreKeywordReadBudget(level workspace.ExploreThoroughness) int {
	return frontier.ExploreKeywordReadBudget(level)
}

func exploreIssueSearchPathBudget(level workspace.ExploreThoroughness) int {
	return frontier.ExploreIssueSearchPathBudget(level)
}

func exploreIssueReadBudget(level workspace.ExploreThoroughness) int {
	return frontier.ExploreIssueReadBudget(level)
}

func exploreSemanticDefaultFileBudget(level workspace.ExploreThoroughness) int {
	return frontier.ExploreSemanticDefaultFileBudget(level)
}

func exploreSemanticRelevantFileBudget(level workspace.ExploreThoroughness) int {
	return frontier.ExploreSemanticRelevantFileBudget(level)
}

func exploreSemanticIssueFileBudget(level workspace.ExploreThoroughness) int {
	return frontier.ExploreSemanticIssueFileBudget(level)
}

func exploreSemanticTotalReadBudget(level workspace.ExploreThoroughness) int {
	return frontier.ExploreSemanticTotalReadBudget(level)
}

func prioritizedExploreSearchPaths(dirs map[string]struct{}, level workspace.ExploreThoroughness) []string {
	return frontier.PrioritizedExploreSearchPaths(dirs, level)
}

func exploreSearchGlob(path string) string {
	return frontier.ExploreSearchGlob(path)
}

func exploreSearchPathLimit(level workspace.ExploreThoroughness) int {
	return frontier.ExploreSearchPathLimit(level)
}

func architectureHotspotBudget(level workspace.ExploreThoroughness) int {
	return frontier.ArchitectureHotspotBudget(level)
}

func architectureReadBudget(level workspace.ExploreThoroughness) int {
	return frontier.ArchitectureReadBudget(level)
}

func architectureRelevantFileBudget(level workspace.ExploreThoroughness) int {
	return frontier.ArchitectureRelevantFileBudget(level)
}

func architectureSearchPathBudget(level workspace.ExploreThoroughness) int {
	return frontier.ArchitectureSearchPathBudget(level)
}

func architectureSearchReadBudget(level workspace.ExploreThoroughness) int {
	return frontier.ArchitectureSearchReadBudget(level)
}

func architectureSearchPattern(prompt string) string {
	return frontier.ArchitectureSearchPattern(prompt)
}

func architectureSourceDirPriority(dir string) int {
	return frontier.ArchitectureSourceDirPriority(dir)
}

func exploreQueryTerms(prompt string) []string {
	return frontier.ExploreQueryTerms(prompt)
}

func appendAnalysisSummary(history []llama.ChatMessage, lane string, summary string) []llama.ChatMessage {
	if strings.TrimSpace(summary) == "" {
		return append([]llama.ChatMessage(nil), history...)
	}

	augmented := append([]llama.ChatMessage(nil), history...)
	augmented = append(augmented, llama.ChatMessage{
		Role: "system",
		Content: "[" + lane + " subagent summary]\n" +
			"Use this as initial orientation, but verify before editing.\n\n" +
			strings.TrimSpace(summary),
	})
	return augmented
}

func exploreToolBudget(maxToolSteps int) int {
	return frontier.ExploreToolBudget(maxToolSteps)
}

type budgetLedgerSnapshot struct {
	Lane                   string   `json:"lane,omitempty"`
	Level                  string   `json:"level,omitempty"`
	UsedModelCalls         int      `json:"used_model_calls,omitempty"`
	UsedDeepeningPasses    int      `json:"used_deepening_passes,omitempty"`
	MaxDeepeningPasses     int      `json:"max_deepening_passes,omitempty"`
	UsedToolResults        int      `json:"used_tool_results,omitempty"`
	NewEvidencePaths       int      `json:"new_evidence_paths,omitempty"`
	TotalEvidencePaths     int      `json:"total_evidence_paths,omitempty"`
	StopReason             string   `json:"stop_reason,omitempty"`
	BudgetExhausted        bool     `json:"budget_exhausted,omitempty"`
	NextAllowedActions     []string `json:"next_allowed_actions,omitempty"`
	MarginalEvidencePolicy string   `json:"marginal_evidence_policy,omitempty"`
	UserVisibleUncertainty string   `json:"user_visible_uncertainty,omitempty"`
}

type evidenceDeltaSnapshot struct {
	Lane             string   `json:"lane,omitempty"`
	Attempt          int      `json:"attempt,omitempty"`
	NewEvidencePaths []string `json:"new_evidence_paths,omitempty"`
	TotalPaths       int      `json:"total_paths,omitempty"`
	ToolResults      int      `json:"tool_results,omitempty"`
	SuccessfulTools  int      `json:"successful_tools,omitempty"`
	FailedTools      int      `json:"failed_tools,omitempty"`
	MarginalValue    string   `json:"marginal_value,omitempty"`
}

func (m *Model) logBudgetLedger(ledger budgetLedgerSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(ledger)
	if err != nil {
		return
	}
	m.logMessage("budget_ledger", string(data))
}

func (m *Model) logEvidenceDelta(delta evidenceDeltaSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(delta)
	if err != nil {
		return
	}
	m.logMessage("evidence_delta", string(data))
}

func shouldInvalidateExploreCacheForTool(result workspace.ToolResult) bool {
	if result.Error != "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(result.Summary)), "no-op") {
		return false
	}
	if workspace.IsWorkspaceWriteTool(result.Tool) || result.Tool == "shell" {
		return true
	}
	return false
}

func (m *Model) invalidateExploreCache(reason string) {
	if m.exploreCache == nil {
		return
	}
	m.exploreCache = nil
	if strings.TrimSpace(reason) != "" {
		m.logMessage("explore_cache_invalidate", reason)
	}
}
