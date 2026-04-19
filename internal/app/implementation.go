package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"qwen-bubbles/internal/agent"
	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/semantic"
	"qwen-bubbles/internal/workspace"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type structuralRefactorStage struct {
	Active       bool
	Stage        int
	SourceLane   string
	FocusPath    string
	Changed      []string
	Remaining    []string
	Strategy     []string
	NextStep     string
	Verification string
}

const (
	analysisLaneMain         analysisLane = "main"
	analysisLaneLocate       analysisLane = "locate"
	analysisLaneExplore      analysisLane = "explore"
	analysisLaneReview       analysisLane = "review"
	analysisLaneArchitecture analysisLane = "architecture"
	analysisLaneImplement    analysisLane = "implement"
)

const (
	promptRouteReasonDirectConversation   promptRouteReason = "direct_conversation"
	promptRouteReasonDirectImplementation promptRouteReason = "direct_implementation"
	promptRouteReasonLocateRequest        promptRouteReason = "locate_request"
	promptRouteReasonExploreRequest       promptRouteReason = "explore_request"
	promptRouteReasonReviewRequest        promptRouteReason = "review_request"
	promptRouteReasonArchitectureRequest  promptRouteReason = "architecture_request"
)

const (
	answerContractDirectConversation  answerContract = "direct_answer"
	answerContractLocateTargets       answerContract = "locate_targets"
	answerContractGroundedExplanation answerContract = "grounded_explanation"
	answerContractReviewFindings      answerContract = "review_findings"
	answerContractArchitecture        answerContract = "architecture_assessment"
	answerContractImplementation      answerContract = "implementation_result"
)

func (m *Model) currentStructuralRefactorStage() *structuralRefactorStage {
	if m == nil {
		return nil
	}
	if m.lastJournal != nil && m.lastJournal.Refactor != nil {
		return m.lastJournal.Refactor
	}
	return m.refactorStage
}

func structuralRefactorStageResumable(stage *structuralRefactorStage) bool {
	if stage == nil || len(stage.Remaining) == 0 {
		return false
	}
	return !strings.Contains(strings.ToLower(stage.NextStep), "repair or roll back")
}

type persistedStructuralRefactorStage struct {
	Version       int                     `json:"version"`
	WorkspaceRoot string                  `json:"workspace_root,omitempty"`
	UpdatedAt     time.Time               `json:"updated_at"`
	Stage         structuralRefactorStage `json:"stage"`
}

const structuralRefactorStageStateVersion = 1

func (m *Model) structuralRefactorStageStatePath() string {
	if m == nil || strings.TrimSpace(m.cfg.Chat.SaveDir) == "" {
		return ""
	}
	return filepath.Join(m.cfg.Chat.SaveDir, "state", "structural-refactor-stage.json")
}

func cloneStructuralRefactorStage(stage *structuralRefactorStage) *structuralRefactorStage {
	if stage == nil {
		return nil
	}
	cloned := *stage
	cloned.Changed = append([]string(nil), stage.Changed...)
	cloned.Remaining = append([]string(nil), stage.Remaining...)
	cloned.Strategy = append([]string(nil), stage.Strategy...)
	return &cloned
}

func (m *Model) loadPersistedStructuralRefactorStage() {
	path := m.structuralRefactorStageStatePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var state persistedStructuralRefactorStage
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}
	if state.Version != structuralRefactorStageStateVersion {
		return
	}
	if root := m.workspaceRootForState(); root != "" && state.WorkspaceRoot != "" && filepath.Clean(state.WorkspaceRoot) != root {
		return
	}
	stage := cloneStructuralRefactorStage(&state.Stage)
	if !structuralRefactorStageResumable(stage) {
		return
	}
	m.refactorStage = stage
}

func (m *Model) persistStructuralRefactorStage(stage *structuralRefactorStage) {
	path := m.structuralRefactorStageStatePath()
	if path == "" {
		return
	}
	if !structuralRefactorStageResumable(stage) {
		m.removePersistedStructuralRefactorStage()
		return
	}
	state := persistedStructuralRefactorStage{
		Version:       structuralRefactorStageStateVersion,
		WorkspaceRoot: m.workspaceRootForState(),
		UpdatedAt:     time.Now(),
		Stage:         *cloneStructuralRefactorStage(stage),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, append(data, '\n'), 0o644)
}

func (m *Model) removePersistedStructuralRefactorStage() {
	path := m.structuralRefactorStageStatePath()
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return
	}
}

func (m *Model) clearPersistedStructuralRefactorStage() {
	if m == nil {
		return
	}
	m.refactorStage = nil
	m.removePersistedStructuralRefactorStage()
}

func buildRepairTaskMemory(prompt string, changedPaths []string, verify implementationVerification, targetPacket implementationTargetPacket) *longHorizonTaskMemory {
	targets := uniqueLimitedStrings(append(uniqueNormalizedPaths(changedPaths), targetPacket.PrimaryPaths...), 16)
	history := verificationHistoryForMemory(verify)
	nextActions := []string{"repair failing verification before widening implementation scope"}
	if len(targets) > 0 {
		nextActions = append(nextActions, "start from changed target "+targets[0])
	}
	gaps := []string{"verification is failing for the latest bounded implementation"}
	gaps = append(gaps, verify.expectedFailures...)
	return &longHorizonTaskMemory{
		Active:              true,
		Kind:                "partial_repair",
		Prompt:              strings.TrimSpace(prompt),
		Lane:                string(analysisLaneImplement),
		Summary:             "Latest bounded implementation needs repair before the task can continue.",
		OpenGaps:            uniqueNonEmptyStrings(gaps),
		AttemptedSearches:   taskMemoryAttemptsFromEvidence(targets),
		ChosenTargets:       targets,
		VerificationHistory: history,
		NextActions:         uniqueNonEmptyStrings(nextActions),
		CreatedAt:           time.Now(),
	}
}

func verificationHistoryForMemory(verify implementationVerification) []string {
	history := make([]string, 0, len(verify.results)+len(verify.commands))
	for i, command := range verify.commands {
		status := "passed"
		if i < len(verify.results) && strings.TrimSpace(verify.results[i].Error) != "" {
			status = "failed: " + truncateText(verify.results[i].Error, 160)
		}
		history = append(history, command+" -> "+status)
	}
	if len(history) == 0 && verify.failed {
		history = append(history, "verification failed")
	}
	return uniqueNonEmptyStrings(history)
}

type implementationTarget struct {
	Path string
	Line int
}

type implementationTargetPacket struct {
	SourceLane       string
	RefactorStage    int
	PrimaryPaths     []string
	EvidencePaths    []string
	AllowedDestinations []string
	ForbiddenPaths   []string
	Anchors          []implementationTarget
	AnalogyPaths     []string
	LiteralFragments []string
	LocationHints    []string
	SymbolHints      []string
	StrategyHints    []string
	Symbols          []semantic.SymbolMatch
}

func buildImplementationTargetPacket(prompt string, history []llama.ChatMessage) implementationTargetPacket {
	packet := implementationTargetPacket{
		PrimaryPaths:     make([]string, 0, 4),
		Anchors:          make([]implementationTarget, 0, 4),
		AnalogyPaths:     make([]string, 0, 2),
		LiteralFragments: uniqueLimitedStrings(quotedPromptFragments(prompt), 3),
		LocationHints:    implementationLocationHints(prompt),
		SymbolHints:      implementationSymbolHints(prompt),
		StrategyHints:    make([]string, 0, 3),
	}
	initialSymbolHints := append([]string(nil), packet.SymbolHints...)
	packet.SymbolHints = packet.SymbolHints[:0]
	primarySeen := make(map[string]struct{})
	analogySeen := make(map[string]struct{})
	anchorSeen := make(map[string]struct{})
	symbolSeen := make(map[string]struct{})
	addPath := func(dst *[]string, seen map[string]struct{}, path string) {
		path = normalizeGuardedPath(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		*dst = append(*dst, path)
	}
	addSymbolHint := func(hint string) {
		hint = strings.TrimSpace(hint)
		if hint == "" {
			return
		}
		if _, ok := symbolSeen[hint]; ok {
			return
		}
		symbolSeen[hint] = struct{}{}
		packet.SymbolHints = append(packet.SymbolHints, hint)
	}
	for _, hint := range initialSymbolHints {
		addSymbolHint(hint)
	}
	for _, path := range implementationPromptPrimaryPaths(prompt) {
		addPath(&packet.PrimaryPaths, primarySeen, path)
	}
	for _, path := range implementationAnalogyPaths(prompt) {
		addPath(&packet.AnalogyPaths, analogySeen, path)
	}
	if hasUILocationHint(strings.ToLower(prompt)) {
		addPath(&packet.PrimaryPaths, primarySeen, "internal/app/app.go")
	}
	for _, target := range analysisSummaryTargets(history) {
		key := fmt.Sprintf("%s:%d", normalizeGuardedPath(target.Path), target.Line)
		if _, ok := anchorSeen[key]; ok {
			continue
		}
		anchorSeen[key] = struct{}{}
		packet.Anchors = append(packet.Anchors, target)
		addPath(&packet.PrimaryPaths, primarySeen, target.Path)
	}
	summaryLane, summaryBody, ok := latestAnalysisSummary(history)
	if ok {
		packet.SourceLane = strings.TrimSpace(summaryLane)
		packet.RefactorStage = analysisSummaryRefactorStage(summaryBody)
		for _, path := range promptMentionedWorkspacePaths(summaryBody) {
			addPath(&packet.PrimaryPaths, primarySeen, path)
		}
		for _, hint := range implementationSymbolHints(summaryBody) {
			addSymbolHint(hint)
		}
		if summaryLane == "architecture" {
			packet.StrategyHints = append(packet.StrategyHints, analysisSummaryStrategyHints(summaryBody)...)
		}
	}
	packet.PrimaryPaths = uniqueLimitedStrings(packet.PrimaryPaths, 6)
	packet.AnalogyPaths = uniqueLimitedStrings(packet.AnalogyPaths, 4)
	packet.LocationHints = uniqueLimitedStrings(packet.LocationHints, 4)
	packet.SymbolHints = uniqueLimitedStrings(packet.SymbolHints, 6)
	packet.StrategyHints = uniqueLimitedStrings(packet.StrategyHints, 4)
	return packet
}

func applyTaskResumeContextToImplementationTargetPacket(packet implementationTargetPacket, resume taskResumeContext) implementationTargetPacket {
	if !resume.Active {
		return packet
	}
	if len(resume.ApprovedEditTargets) > 0 {
		packet.PrimaryPaths = uniqueLimitedStrings(resume.ApprovedEditTargets, 8)
	}
	packet.EvidencePaths = uniqueLimitedStrings(append(packet.EvidencePaths, resume.ReadOnlyEvidencePaths...), 16)
	packet.AllowedDestinations = uniqueLimitedStrings(append(packet.AllowedDestinations, resume.AllowedDestinations...), 8)
	packet.ForbiddenPaths = uniqueLimitedStrings(append(packet.ForbiddenPaths, resume.ForbiddenPaths...), 16)
	if len(resume.VerificationPlan) > 0 {
		packet.StrategyHints = uniqueLimitedStrings(append(packet.StrategyHints, resume.VerificationPlan...), 8)
	}
	if resume.SourceLane != "" && strings.TrimSpace(packet.SourceLane) == "" {
		packet.SourceLane = string(resume.SourceLane)
	}
	return packet
}

func implementationPromptPrimaryPaths(prompt string) []string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil
	}
	var explicit []string
	for _, line := range strings.Split(prompt, "\n") {
		trimmed := strings.TrimSpace(line)
		normalized := strings.ToLower(trimmed)
		for _, prefix := range []string{"previously validated targets:", "target:", "targets:"} {
			if !strings.HasPrefix(normalized, prefix) {
				continue
			}
			paths := promptMentionedWorkspacePaths(strings.TrimSpace(trimmed[len(prefix):]))
			explicit = append(explicit, paths...)
		}
	}
	if len(explicit) > 0 {
		return uniqueLimitedStrings(explicit, 4)
	}
	return promptMentionedWorkspacePaths(prompt)
}

func enrichImplementationTargetPacket(mgr *workspace.Manager, packet implementationTargetPacket) implementationTargetPacket {
	if mgr == nil || !mgr.Enabled() {
		return packet
	}
	packet.PrimaryPaths = resolveImplementationPrimaryPaths(mgr.Root(), packet.PrimaryPaths)
	packet.EvidencePaths = resolveImplementationPrimaryPaths(mgr.Root(), packet.EvidencePaths)
	packet.ForbiddenPaths = resolveImplementationPrimaryPaths(mgr.Root(), packet.ForbiddenPaths)
	packet.AnalogyPaths = resolveImplementationPrimaryPaths(mgr.Root(), packet.AnalogyPaths)
	if len(packet.SymbolHints) == 0 && len(packet.Anchors) == 0 {
		return packet
	}
	idx, err := semantic.BuildGoIndex(mgr.Root())
	if err != nil || idx == nil {
		return packet
	}
	packet.Symbols = resolveImplementationSymbolAnchors(idx, packet)
	if len(packet.PrimaryPaths) == 0 {
		for _, symbol := range packet.Symbols {
			if symbol.Path != "" {
				packet.PrimaryPaths = uniqueLimitedStrings(append(packet.PrimaryPaths, normalizeGuardedPath(symbol.Path)), 6)
			}
		}
	}
	return packet
}

func resolveImplementationPrimaryPaths(root string, paths []string) []string {
	root = strings.TrimSpace(root)
	if root == "" || len(paths) == 0 {
		return paths
	}
	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		if workspaceRelativeFileExists(root, path) {
			resolved = append(resolved, path)
			continue
		}
		if strings.Contains(path, "/") {
			resolved = append(resolved, path)
			continue
		}
		matches := findWorkspaceFilesByBase(root, path, 2)
		if len(matches) == 1 {
			resolved = append(resolved, matches[0])
			continue
		}
		resolved = append(resolved, path)
	}
	return uniqueLimitedStrings(resolved, 6)
}

func workspaceRelativeFileExists(root string, path string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	return err == nil && !info.IsDir()
}

func findWorkspaceFilesByBase(root string, base string, limit int) []string {
	base = strings.TrimSpace(base)
	if root == "" || base == "" || limit <= 0 {
		return nil
	}
	matches := make([]string, 0, limit)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "logs", "transcripts", "node_modules", "vendor":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if entry.Name() != base {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		matches = append(matches, filepath.ToSlash(rel))
		if len(matches) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	return matches
}

func implementationTargetPaths(prompt string, history []llama.ChatMessage) []string {
	seen := make(map[string]struct{})
	packet := buildImplementationTargetPacket(prompt, history)
	paths := make([]string, 0, len(packet.PrimaryPaths))
	add := func(path string) {
		path = normalizeGuardedPath(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for _, path := range packet.PrimaryPaths {
		add(path)
	}
	return paths
}

func implementationAnalogyPaths(prompt string) []string {
	normalized := strings.ToLower(prompt)
	paths := promptMentionedWorkspacePaths(prompt)
	analogies := make([]string, 0, 2)
	for _, path := range paths {
		lowerPath := strings.ToLower(path)
		base := strings.ToLower(filepath.Base(path))
		if containsAny(
			normalized,
			"like @"+lowerPath,
			"like "+lowerPath,
			"like @"+base,
			"like "+base,
			"similar to @"+lowerPath,
			"similar to "+lowerPath,
			"similar to @"+base,
			"similar to "+base,
			"same as @"+lowerPath,
			"same as "+lowerPath,
			"same as @"+base,
			"same as "+base,
			"based on @"+lowerPath,
			"based on "+lowerPath,
			"based on @"+base,
			"based on "+base,
			"follow @"+lowerPath,
			"follow "+lowerPath,
			"follow @"+base,
			"follow "+base,
		) {
			analogies = append(analogies, path)
		}
	}
	return uniqueLimitedStrings(analogies, 4)
}

func implementationLocationHints(prompt string) []string {
	normalized := strings.ToLower(prompt)
	hints := make([]string, 0, 4)
	for _, hint := range []string{"top", "header", "footer", "badge", "window", "chat", "menu", "status", "banner"} {
		if strings.Contains(normalized, hint) {
			hints = append(hints, hint)
		}
	}
	return hints
}

func implementationSymbolHints(prompt string) []string {
	tokenPattern := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_./]*`)
	stopwords := map[string]struct{}{
		"add": {}, "and": {}, "app": {}, "banner": {}, "bit": {}, "build": {}, "can": {}, "change": {},
		"code": {}, "edit": {}, "file": {}, "fix": {}, "for": {}, "from": {}, "header": {}, "implement": {},
		"in": {}, "it": {}, "like": {}, "make": {}, "match": {}, "padding": {}, "please": {}, "remove": {},
		"render": {}, "same": {}, "small": {}, "status": {}, "text": {}, "the": {}, "this": {}, "to": {},
		"top": {}, "underneath": {}, "update": {}, "use": {}, "where": {}, "window": {}, "with": {}, "work": {},
	}
	seen := make(map[string]struct{})
	hints := make([]string, 0, 4)
	add := func(token string) {
		token = strings.Trim(token, "`'\"()[]{}<>,:;!?")
		token = strings.TrimPrefix(token, "@")
		token = strings.TrimRight(token, ".")
		if token == "" || looksLikeWorkspacePathToken(token) || strings.Contains(token, "/") {
			return
		}
		lower := strings.ToLower(token)
		if _, skip := stopwords[lower]; skip {
			return
		}
		looksSymbolic := strings.Contains(token, ".") ||
			token != lower ||
			strings.Contains(token, "_")
		if !looksSymbolic {
			return
		}
		if _, ok := seen[token]; ok {
			return
		}
		seen[token] = struct{}{}
		hints = append(hints, token)
	}
	for _, fragment := range quotedPromptFragments(prompt) {
		for _, token := range tokenPattern.FindAllString(fragment, -1) {
			add(token)
		}
	}
	for _, token := range tokenPattern.FindAllString(prompt, -1) {
		add(token)
	}
	return uniqueLimitedStrings(hints, 6)
}

func resolveImplementationSymbolAnchors(idx *semantic.GoIndex, packet implementationTargetPacket) []semantic.SymbolMatch {
	if idx == nil {
		return nil
	}
	preferredPaths := make(map[string]int)
	for _, path := range packet.PrimaryPaths {
		preferredPaths[normalizeGuardedPath(path)] = 2
	}
	for _, path := range packet.AnalogyPaths {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		if preferredPaths[path] < 1 {
			preferredPaths[path] = 1
		}
	}
	anchorLines := make(map[string][]int)
	for _, anchor := range packet.Anchors {
		path := normalizeGuardedPath(anchor.Path)
		if path == "" || anchor.Line <= 0 {
			continue
		}
		anchorLines[path] = append(anchorLines[path], anchor.Line)
	}
	seen := make(map[string]struct{})
	matches := make([]semantic.SymbolMatch, 0, 6)
	add := func(match semantic.SymbolMatch) {
		if match.Path == "" || match.Name == "" {
			return
		}
		match.Path = normalizeGuardedPath(match.Path)
		if match.Path == "" {
			return
		}
		if boost := preferredPaths[match.Path]; boost > 0 {
			match.Score += boost * 20
		}
		for _, line := range anchorLines[match.Path] {
			if line >= match.StartLine && line <= match.EndLine {
				match.Score += 24
				break
			}
		}
		key := fmt.Sprintf("%s:%s:%d", match.Path, implementationQualifiedSymbol(match), match.Line)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		matches = append(matches, match)
	}
	for path, lines := range anchorLines {
		file := idx.FileByPath(path)
		if file == nil {
			continue
		}
		for _, line := range lines {
			if symbol, ok := enclosingSymbolAtLine(file, line); ok {
				add(symbolMatchFromGoSymbol(file, symbol, 80))
			}
		}
	}
	for _, hint := range packet.SymbolHints {
		for _, match := range idx.FindSymbols(hint, 6) {
			add(match)
		}
	}
	slices.SortFunc(matches, func(a, b semantic.SymbolMatch) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		if a.Path != b.Path {
			return strings.Compare(a.Path, b.Path)
		}
		if a.StartLine != b.StartLine {
			return a.StartLine - b.StartLine
		}
		return strings.Compare(implementationQualifiedSymbol(a), implementationQualifiedSymbol(b))
	})
	if len(matches) > 4 {
		matches = matches[:4]
	}
	return matches
}

func routeContinuesToImplement(plan promptExecutionPlan) bool {
	return plan.followupLane == analysisLaneImplement
}

func implementationQualifiedSymbol(match semantic.SymbolMatch) string {
	if strings.TrimSpace(match.Receiver) != "" {
		return match.Receiver + "." + match.Name
	}
	return match.Name
}

func renderImplementationTargetPacket(packet implementationTargetPacket) string {
	if len(packet.PrimaryPaths) == 0 && len(packet.EvidencePaths) == 0 && len(packet.AllowedDestinations) == 0 && len(packet.Anchors) == 0 && len(packet.AnalogyPaths) == 0 && len(packet.LiteralFragments) == 0 && len(packet.Symbols) == 0 && len(packet.SymbolHints) == 0 {
		return ""
	}
	lines := []string{"[implementation target packet]", "Use these anchors before any broad repo discovery."}
	if lane := strings.TrimSpace(packet.SourceLane); lane != "" {
		lines = append(lines, "- derived from "+lane+" summary")
	}
	if packet.RefactorStage > 0 {
		lines = append(lines, fmt.Sprintf("- refactor stage: %d", packet.RefactorStage))
	}
	if len(packet.PrimaryPaths) > 0 {
		lines = append(lines, "- primary files: "+strings.Join(packet.PrimaryPaths, ", "))
	}
	if len(packet.EvidencePaths) > 0 {
		lines = append(lines, "- read-only evidence files: "+strings.Join(packet.EvidencePaths, ", "))
	}
	if len(packet.AllowedDestinations) > 0 {
		lines = append(lines, "- plan-approved new destinations: "+strings.Join(packet.AllowedDestinations, ", "))
	}
	if len(packet.ForbiddenPaths) > 0 {
		lines = append(lines, "- forbidden files: "+strings.Join(packet.ForbiddenPaths, ", "))
	}
	if len(packet.Anchors) > 0 {
		anchors := make([]string, 0, len(packet.Anchors))
		for _, anchor := range packet.Anchors {
			anchors = append(anchors, fmt.Sprintf("%s:%d", anchor.Path, anchor.Line))
		}
		lines = append(lines, "- anchored lines: "+strings.Join(uniqueLimitedStrings(anchors, 4), ", "))
	}
	if len(packet.AnalogyPaths) > 0 {
		lines = append(lines, "- analogous references: "+strings.Join(packet.AnalogyPaths, ", "))
	}
	if len(packet.Symbols) > 0 {
		symbols := make([]string, 0, len(packet.Symbols))
		for _, match := range packet.Symbols {
			symbols = append(symbols, fmt.Sprintf("%s (%s:%d-%d)", implementationQualifiedSymbol(match), match.Path, match.StartLine, match.EndLine))
		}
		lines = append(lines, "- symbol anchors: "+strings.Join(uniqueLimitedStrings(symbols, 4), ", "))
	} else if len(packet.SymbolHints) > 0 {
		lines = append(lines, "- symbol hints: "+strings.Join(packet.SymbolHints, ", "))
	}
	if len(packet.LiteralFragments) > 0 {
		quoted := make([]string, 0, len(packet.LiteralFragments))
		for _, fragment := range packet.LiteralFragments {
			fragment = strings.TrimSpace(fragment)
			if fragment == "" {
				continue
			}
			quoted = append(quoted, strconv.Quote(fragment))
		}
		if len(quoted) > 0 {
			lines = append(lines, "- quoted anchors: "+strings.Join(quoted, ", "))
		}
	}
	if len(packet.LocationHints) > 0 {
		lines = append(lines, "- location hints: "+strings.Join(packet.LocationHints, ", "))
	}
	if len(packet.StrategyHints) > 0 {
		lines = append(lines, "- structural hints: "+strings.Join(packet.StrategyHints, " | "))
	}
	if stage := structuralRefactorStageHint(packet); stage != "" {
		lines = append(lines, "- structural stage: "+stage)
	}
	lines = append(lines, "- do not call list_files/search_files until you have consumed these anchors or proved they are insufficient")
	return strings.Join(lines, "\n")
}

func analysisSummaryRefactorStage(summary string) int {
	for _, match := range regexp.MustCompile(`(?i)\bstage\s*:?\s*(\d+)\b`).FindAllStringSubmatch(summary, -1) {
		if len(match) != 2 {
			continue
		}
		stage, err := strconv.Atoi(match[1])
		if err == nil && stage > 0 {
			return stage
		}
	}
	return 0
}

func (m *Model) runImplementAgent(
	ctx context.Context,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	resumeContexts ...taskResumeContext,
) (string, error) {
	return m.runImplementAgentWithGrant(ctx, prompt, history, opts, nil, resumeContexts...)
}

func (m *Model) runImplementAgentWithGrant(
	ctx context.Context,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	grant *writeApprovalGrant,
	resumeContexts ...taskResumeContext,
) (string, error) {
	implementWorkspace := m.newWorkspaceManager()
	journalRecorder := newExecutionJournalRecorder(implementWorkspace, prompt)
	m.emitPhaseCard(analysisLaneImplement, "targeting", phaseStateActive, "Resolving anchored files, lines, and symbols before editing.")
	var resumeContext taskResumeContext
	if len(resumeContexts) > 0 {
		resumeContext = resumeContexts[0]
	}
	targetPacket := buildImplementationTargetPacket(prompt, history)
	targetPacket = applyTaskResumeContextToImplementationTargetPacket(targetPacket, resumeContext)
	targetPacket = enrichImplementationTargetPacket(implementWorkspace, targetPacket)
	writeGuard := newImplementWriteGuard(implementWorkspace, prompt)
	writeGuard.grant = grant
	targetPaths := append([]string(nil), targetPacket.PrimaryPaths...)
	implementWorkspace.PrimeDiscoveredPaths(uniqueLimitedStrings(append(append(append([]string(nil), targetPaths...), targetPacket.EvidencePaths...), targetPacket.AnalogyPaths...), 12))
	bootstrap := m.bootstrapImplementationEvidence(ctx, implementWorkspace, prompt, targetPacket, history)
	if len(bootstrap.messages) > 0 {
		history = append(history, bootstrap.messages...)
		for i, result := range bootstrap.results {
			m.logToolResult(agent.ToolResultEvent{Step: -(i + 1), Agent: "implement", Result: result})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
		}
	}
	if rendered := renderImplementationTargetPacket(targetPacket); rendered != "" {
		history = append(history, llama.ChatMessage{Role: "system", Content: rendered})
		m.logMessage("implement_targets", rendered)
	}
	var pendingApproval *writeApprovalProposal
	preChangedPaths := make([]string, 0, 2)
	taskPrompt := buildImplementTaskPrompt(prompt)
	finalizeImplementation := func(summary string, changedPaths []string, verify implementationVerification) string {
		verify = applyImplementationGoalChecks(implementWorkspace, prompt, targetPacket, changedPaths, verify)
		refactorStage := buildStructuralRefactorStage(targetPacket, changedPaths, verify)
		reply := composeImplementationSummary(summary, changedPaths, verify, refactorStage)
		reply = m.applyFinalAnswerCritic(reply, changedPaths, verify, refactorStage)
		rails := buildDeterministicControlRails(prompt, promptExecutionPlan{
			lane:     analysisLaneImplement,
			contract: answerContractForLane(analysisLaneImplement),
		})
		reply = m.applyControlRailsFinalAnswer(rails, reply, nil, changedPaths, len(verify.results) > 0)
		m.rememberExecutionJournal(journalRecorder, changedPaths, reply, refactorStage)
		if verify.failed {
			m.rememberTaskMemory(buildRepairTaskMemory(prompt, changedPaths, verify, targetPacket), "implementation verification failed with resumable repair context")
		} else {
			m.clearPersistedTaskMemory("implementation completed successfully")
		}
		return reply
	}
	handleTerminalShortcut := func(shortcut directImplementationShortcut) (string, bool) {
		if shortcut.proposal != nil {
			pendingApproval = shortcut.proposal
			m.events <- writeApprovalProposalMsg{proposal: *shortcut.proposal}
		}
		if shortcut.terminal && pendingApproval != nil {
			return pendingApproval.renderSummary(), true
		}
		return "", false
	}
	guardBlocked := func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult {
		if proposal := buildWriteApprovalProposal(ctx, implementWorkspace, prompt, req, writeGuard, blocked); proposal != nil {
			pendingApproval = proposal
			m.events <- writeApprovalProposalMsg{proposal: *proposal}
			return approvalRequiredToolResult(*proposal)
		}
		return blocked
	}
	if os.Getenv("QWEN_BUBBLES_ENABLE_DETERMINISTIC_APP_SPLIT") == "1" {
		if shortcut := m.tryDeterministicAppGoSplit(ctx, implementWorkspace, prompt, targetPacket, len(bootstrap.results)+1); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
			history = append(history, llama.ChatMessage{Role: "system", Content: "A deterministic package split already changed the workspace:\n" + shortcut.summary})
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the deterministic package split before continuing.")
			m.events <- infoMsg{text: "Verification failed after the deterministic package split. Qwen is repairing the change."}
		}
	}
	if shortcut := m.tryDirectPromptEdit(ctx, implementWorkspace, prompt, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
		verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
		if !verify.failed {
			return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
		}
		if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
			changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
			verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
			}
			preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
			history = append(history, llama.ChatMessage{Role: "system", Content: "Direct implementation edits already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
		} else {
			preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
			history = append(history, llama.ChatMessage{Role: "system", Content: "A direct implementation shortcut already changed the workspace:\n" + shortcut.summary})
		}
		for _, result := range verify.results {
			history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
		}
		taskPrompt = buildImplementRepairPrompt(prompt)
		m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
		m.events <- infoMsg{text: "Verification failed after the direct edit. Qwen is repairing the change."}
	} else if summary, ok := handleTerminalShortcut(shortcut); ok {
		return summary, nil
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryDirectConfigFieldUpdate(ctx, implementWorkspace, prompt, targetPacket, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct config field update already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct config field update already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the direct config field update. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryDirectLocateRemoval(ctx, implementWorkspace, prompt, history, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "Direct locate-based edits already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct locate-based removal already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the direct locate-based edit. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryDirectUISpacingEdit(ctx, implementWorkspace, prompt, targetPacket, bootstrap, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct UI spacing edit already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A direct UI spacing edit already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the direct UI spacing edit. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryFocusedStructuredExecutionPlan(ctx, implementWorkspace, prompt, opts, targetPacket, bootstrap, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A focused structured edit plan already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A focused structured edit plan already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the focused structured edit plan. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryFocusedWindowRewrite(ctx, implementWorkspace, prompt, opts, targetPacket, bootstrap, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A focused window rewrite already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A focused window rewrite already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the focused rewrite. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if len(preChangedPaths) == 0 {
		if shortcut := m.tryStructuredExecutionPlan(ctx, implementWorkspace, prompt, history, opts, targetPacket, len(bootstrap.results)+1, journalRecorder, writeGuard, guardBlocked); shortcut.applied {
			verify := m.runImplementationVerification(ctx, implementWorkspace, shortcut.nextStep, shortcut.changedPaths...)
			if !verify.failed {
				return finalizeImplementation(shortcut.summary, shortcut.changedPaths, verify), nil
			}
			if repair := m.tryDeterministicVerificationRepair(ctx, implementWorkspace, verify, shortcut.nextStep+len(verify.results), journalRecorder, writeGuard, guardBlocked); repair.applied {
				changedPaths := uniqueNonEmptyStrings(append(shortcut.changedPaths, repair.changedPaths...))
				verify = m.runImplementationVerification(ctx, implementWorkspace, repair.nextStep, changedPaths...)
				if !verify.failed {
					return finalizeImplementation(shortcut.summary+" "+repair.summary, changedPaths, verify), nil
				}
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A structured execution plan already changed the workspace:\n" + shortcut.summary + "\n" + repair.summary})
			} else {
				preChangedPaths = uniqueNonEmptyStrings(append(preChangedPaths, shortcut.changedPaths...))
				history = append(history, llama.ChatMessage{Role: "system", Content: "A structured execution plan already changed the workspace:\n" + shortcut.summary})
			}
			for _, result := range verify.results {
				history = append(history, llama.ChatMessage{Role: "system", Content: workspace.FormatToolResultForModel(result)})
			}
			taskPrompt = buildImplementRepairPrompt(prompt)
			m.emitPhaseCard(analysisLaneImplement, "repair", phaseStateActive, "Verification failed. Repairing the bounded change before continuing.")
			m.events <- infoMsg{text: "Verification failed after the structured execution plan. Qwen is repairing the change."}
		} else if summary, ok := handleTerminalShortcut(shortcut); ok {
			return summary, nil
		}
	}
	if pendingApproval != nil {
		return pendingApproval.renderSummary(), nil
	}
	history = replaceSystemPrompt(history, buildImplementSystemPrompt(m.promptStack.Text, implementWorkspace.Instructions()))
	history = append(history, llama.ChatMessage{Role: "user", Content: taskPrompt})

	requestByStep := make(map[int]agent.ToolRequestEvent)
	resultByStep := make(map[int]agent.ToolResultEvent)
	targetGuard := newImplementTargetGuard(implementWorkspace, targetPacket, bootstrap.results)
	targetGuard.allowFullTargetRead = promptRequestsWholeFileRefactor(prompt)
	implementPolicy := implementRunPolicyForRequest(m.cfg.Chat.MaxToolSteps, prompt, targetPacket)
	railsPlan := promptExecutionPlan{
		lane:     analysisLaneImplement,
		reason:   promptRouteReasonDirectImplementation,
		contract: answerContractForLane(analysisLaneImplement),
	}
	railsPlan.intent = buildPromptIntentDecision(prompt, buildPromptRouteProfile(prompt), m.recentConversationText(6), railsPlan)
	rails := buildDeterministicControlRails(prompt, railsPlan)
	if implementPolicy.MaxToolSteps <= 0 {
		m.logMessage("implement_tool_budget", fmt.Sprintf("using model-decided completion with no fixed step cap; no-progress fuse=%d", implementPolicy.MaxNoProgressTurns))
	} else if baseBudget := implementToolBudget(m.cfg.Chat.MaxToolSteps); implementPolicy.MaxToolSteps != baseBudget {
		m.logMessage("implement_tool_budget", fmt.Sprintf("expanded implement tool budget from %d to %d for structural request", baseBudget, implementPolicy.MaxToolSteps))
	}
	runner := &agent.Runner{
		Client:         m.client,
		Workspace:      implementWorkspace,
		MaxToolSteps:   implementPolicy.MaxToolSteps,
		Policy:         implementPolicy,
		AgentName:      "implement",
		SeedResults:    append([]workspace.ToolResult(nil), bootstrap.results...),
		CompletionGate: implementationCompletionGate(prompt, preChangedPaths, requestByStep, resultByStep),
		TaskLedger: agent.TaskLedgerPolicy{
			Required: requestNeedsExpandedImplementBudget(prompt, targetPacket),
		},
		PermissionPolicy: controlRailPermissionPolicy(rails),
		OnEvent: func(event agent.RunEvent) {
			m.logMessage(event.Role, event.Content)
		},
		PreExecute: func(step int, req workspace.ToolRequest) *workspace.ToolResult {
			if blocked := controlRailPreExecute(rails, req); blocked != nil {
				return blocked
			}
			if blocked := targetGuard.PreExecute(req); blocked != nil {
				return blocked
			}
			blocked := writeGuard.PreExecute(req)
			if blocked != nil {
				result := guardBlocked(req, *blocked)
				return &result
			}
			if result := captureExecutionJournalResult(journalRecorder, req); result != nil {
				return result
			}
			return nil
		},
	}
	m.events <- infoMsg{text: "Implementation agent is applying the requested change."}
	m.emitPhaseCard(analysisLaneImplement, "apply", phaseStateActive, "Executing the bounded implementation plan against the targeted files.")
	reply, err := runner.Run(
		ctx,
		history,
		opts,
		func(status string) {
			m.events <- infoMsg{text: "Implementation agent: " + status}
		},
		func(event agent.ToolRequestEvent) {
			requestByStep[event.Step] = event
			m.logToolRequest(event)
		},
		func(event agent.ToolResultEvent) {
			resultByStep[event.Step] = event
			m.logToolResult(event)
			if shouldInvalidateExploreCacheForTool(event.Result) {
				m.events <- exploreCacheInvalidatedMsg{
					reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", event.Result.Tool),
				}
			}
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(event.Result)}
		},
	)
	if err != nil {
		return "", err
	}
	if pendingApproval != nil {
		return pendingApproval.renderSummary(), nil
	}

	changedPaths := uniqueNonEmptyStrings(append(preChangedPaths, changedPathsFromImplementEvents(requestByStep, resultByStep)...))
	if len(changedPaths) == 0 {
		if implementationReplyReportsNoChangeBlocker(reply) {
			m.emitPhaseCard(analysisLaneImplement, "complete", phaseStateFailed, "Implementation stopped with a blocker before applying workspace changes.")
		} else {
			m.emitPhaseCard(analysisLaneImplement, "complete", phaseStateDone, "No workspace changes were needed after consuming the target packet.")
		}
		return buildImplementationNoopSummary(reply, resultByStep), nil
	}

	verify := m.runImplementationVerification(ctx, implementWorkspace, len(resultByStep)+1, changedPaths...)
	return finalizeImplementation(reply, changedPaths, verify), nil
}

type directImplementationShortcut struct {
	applied      bool
	changedPaths []string
	summary      string
	nextStep     int
	terminal     bool
	proposal     *writeApprovalProposal
}

func (m *Model) tryDeterministicAppGoSplit(ctx context.Context, mgr *workspace.Manager, prompt string, packet implementationTargetPacket, startStep int) directImplementationShortcut {
	_ = ctx
	if mgr == nil || !promptRequestsWholeFileRefactor(prompt) {
		return directImplementationShortcut{}
	}
	if !slices.Contains(packet.PrimaryPaths, "internal/app/app.go") {
		return directImplementationShortcut{}
	}
	changedPaths, err := deterministicSplitAppGo(mgr.Root())
	if err != nil {
		if m != nil {
			m.logMessage("deterministic_app_split", "skipped: "+err.Error())
		}
		return directImplementationShortcut{}
	}
	if len(changedPaths) == 0 {
		if m != nil {
			m.logMessage("deterministic_app_split", "skipped: no file changes produced")
		}
		return directImplementationShortcut{}
	}
	if m != nil {
		m.logMessage("deterministic_app_split", fmt.Sprintf("split internal/app/app.go into %d package files", len(changedPaths)))
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: changedPaths,
		summary:      "Split internal/app/app.go into package-level files by responsibility while keeping package app and preserving exported APIs.",
		nextStep:     startStep,
	}
}

func deterministicSplitAppGo(root string) ([]string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("workspace root is not configured")
	}
	appPath := filepath.Join(root, "internal/app/app.go")
	src, err := os.ReadFile(appPath)
	if err != nil {
		return nil, fmt.Errorf("read app.go: %w", err)
	}
	if len(strings.Split(string(src), "\n")) < 5000 {
		return nil, fmt.Errorf("app.go is already below whole-file split threshold")
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, appPath, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse app.go: %w", err)
	}
	imports := appSplitImports(src, fset, file)
	buckets := make(map[string][]ast.Decl)
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			continue
		}
		name := appSplitFileForDecl(decl)
		buckets[name] = append(buckets[name], decl)
	}
	ordered := []string{
		"app.go",
		"model_state.go",
		"lifecycle.go",
		"prompt_routing.go",
		"rendering.go",
		"task_memory.go",
		"implementation.go",
		"write_guard.go",
		"analysis_agents.go",
	}
	changed := make([]string, 0, len(ordered))
	for _, name := range ordered {
		decls := buckets[name]
		if len(decls) == 0 {
			continue
		}
		outPath := filepath.Join(root, "internal/app", name)
		content, err := renderAppSplitFile(src, fset, imports, decls)
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", name, err)
		}
		if existing, err := os.ReadFile(outPath); err == nil && bytes.Equal(existing, content) {
			continue
		}
		if err := os.WriteFile(outPath, content, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
		changed = append(changed, filepath.ToSlash(filepath.Join("internal/app", name)))
	}
	if testChanged, err := patchAppSplitTests(root); err != nil {
		return nil, err
	} else if testChanged {
		changed = append(changed, "internal/app/write_guard_test.go")
	}
	return uniqueNonEmptyStrings(changed), nil
}

func patchAppSplitTests(root string) (bool, error) {
	path := filepath.Join(root, "internal/app/write_guard_test.go")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	original := string(data)
	updated := original
	replacements := [][2]string{
		{
			`packet := enrichImplementationTargetPacket(mgr, buildImplementationTargetPacket("Update Model.bootstrapImplementationEvidence in @internal/app/app.go.", nil))`,
			`packet := enrichImplementationTargetPacket(mgr, buildImplementationTargetPacket("Update Model.bootstrapImplementationEvidence in @internal/app/implementation.go.", nil))`,
		},
		{
			`if symbol.Path == "internal/app/app.go" && implementationQualifiedSymbol(symbol) == "Model.bootstrapImplementationEvidence" {`,
			`if symbol.Path == "internal/app/implementation.go" && implementationQualifiedSymbol(symbol) == "Model.bootstrapImplementationEvidence" {`,
		},
		{
			`"Update Model.bootstrapImplementationEvidence in @internal/app/app.go.",
		implementationTargetPacket{
			PrimaryPaths: []string{"internal/app/app.go"},
			Symbols: []semantic.SymbolMatch{{
				Path:      "internal/app/app.go",
				Name:      "bootstrapImplementationEvidence",
				Receiver:  "Model",
				Line:      1614,
				StartLine: 1614,
				EndLine:   1710,
			}},`,
			`"Update Model.bootstrapImplementationEvidence in @internal/app/implementation.go.",
		implementationTargetPacket{
			PrimaryPaths: []string{"internal/app/implementation.go"},
			Symbols: []semantic.SymbolMatch{{
				Path:      "internal/app/implementation.go",
				Name:      "bootstrapImplementationEvidence",
				Receiver:  "Model",
				Line:      1200,
				StartLine: 1200,
				EndLine:   1375,
			}},`,
		},
		{
			`foundRead := false
	for _, result := range bootstrap.results {
		if result.Tool == "read_file" && strings.Contains(result.Summary, "internal/app/app.go") {
			foundRead = true
			break
		}
	}
	if !foundRead {
		t.Fatalf("expected symbol bootstrap evidence to include a read_file result, got %#v", bootstrap.results)
	}`,
			`foundRead := false
	for _, result := range bootstrap.results {
		if result.Tool == "read_file" && strings.Contains(result.Summary, "internal/app/implementation.go") {
			foundRead = true
			break
		}
	}
	if !foundRead {
		t.Fatalf("expected symbol bootstrap evidence to include a read_file result, got %#v", bootstrap.results)
	}`,
		},
		{
			`Anchors:      []implementationTarget{{Path: "internal/app/app.go", Line: 2112}},`,
			`Anchors:      []implementationTarget{{Path: "internal/app/app.go", Line: 700}},`,
		},
		{
			`func TestImplementTargetGuardRedirectsBroadSearchToScopedSearchContent(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	guard := newImplementTargetGuard(mgr, implementationTargetPacket{
		PrimaryPaths: []string{"internal/app/app.go"},
	}, nil)`,
			`func TestImplementTargetGuardRedirectsBroadSearchToScopedSearchContent(t *testing.T) {
	mgr := workspace.NewManager(testWorkspaceConfig())
	guard := newImplementTargetGuard(mgr, implementationTargetPacket{
		PrimaryPaths: []string{"internal/app/implementation.go"},
	}, nil)`,
		},
	}
	for _, replacement := range replacements {
		updated = strings.Replace(updated, replacement[0], replacement[1], 1)
	}
	if updated == original {
		return false, nil
	}
	formatted, err := format.Source([]byte(updated))
	if err != nil {
		return false, fmt.Errorf("format write_guard_test.go after app split: %w", err)
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return false, fmt.Errorf("write write_guard_test.go after app split: %w", err)
	}
	return true, nil
}

func appSplitImports(src []byte, fset *token.FileSet, file *ast.File) map[string]string {
	imports := make(map[string]string)
	for _, spec := range file.Imports {
		name := appSplitImportName(spec)
		start := fset.Position(spec.Pos()).Offset
		end := fset.Position(spec.End()).Offset
		imports[name] = string(src[start:end])
	}
	return imports
}

func appSplitImportName(spec *ast.ImportSpec) string {
	if spec.Name != nil {
		return spec.Name.Name
	}
	path := strings.Trim(spec.Path.Value, `"`)
	base := filepath.Base(path)
	return strings.ReplaceAll(base, "-", "_")
}

func appSplitFileForDecl(decl ast.Decl) string {
	names := appSplitDeclNames(decl)
	joined := strings.ToLower(strings.Join(names, " "))
	switch {
	case appSplitContains(joined, "writeapproval", "writeguard", "approval", "guarded", "guardpath", "preexecute", "protectedpath"):
		return "write_guard.go"
	case appSplitContains(joined, "implementation", "implement", "verification", "directpromptedit", "structuredexecution", "focused", "changedpaths", "finalanswercritic", "repair", "refactorstage"):
		return "implementation.go"
	case appSplitContains(joined, "taskmemory", "memory", "resume", "replan", "structuralrefactor"):
		return "task_memory.go"
	case appSplitContains(joined, "prompt", "route", "contract", "lane", "targetpacket", "classification", "ambig", "systemprompt", "profiles", "thoroughness"):
		return "prompt_routing.go"
	case appSplitContains(joined, "view", "render", "viewport", "style", "phasecard", "panel", "bodyview", "messages", "scroll"):
		return "rendering.go"
	case appSplitContains(joined, "explore", "review", "architecture", "analysis", "evidence", "hotspot", "candidate", "readiness", "deepening", "capability", "ledger", "issue"):
		return "analysis_agents.go"
	case appSplitContains(joined, "new", "init", "update", "runpromptonce", "ensureserverready", "sendpromptasync", "runpromptplan", "runmainagent", "serverstatus", "stream", "transcript", "reload"):
		return "lifecycle.go"
	case appSplitContains(joined, "model", "msg", "state", "trace", "journal", "phase", "executionpanel", "fallback"):
		return "model_state.go"
	default:
		return "app.go"
	}
}

func appSplitDeclNames(decl ast.Decl) []string {
	names := make([]string, 0, 4)
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv != nil && len(d.Recv.List) > 0 {
			names = append(names, appSplitReceiverName(d.Recv.List[0].Type))
		}
		names = append(names, d.Name.Name)
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				names = append(names, s.Name.Name)
			case *ast.ValueSpec:
				for _, name := range s.Names {
					names = append(names, name.Name)
				}
			}
		}
	}
	return names
}

func appSplitReceiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return appSplitReceiverName(t.X)
	case *ast.Ident:
		return t.Name
	default:
		return ""
	}
}

func appSplitContains(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func renderAppSplitFile(src []byte, fset *token.FileSet, imports map[string]string, decls []ast.Decl) ([]byte, error) {
	usedImports := make(map[string]struct{})
	for _, decl := range decls {
		ast.Inspect(decl, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if _, exists := imports[id.Name]; exists {
				usedImports[id.Name] = struct{}{}
			}
			return true
		})
	}
	var b bytes.Buffer
	b.WriteString("package app\n\n")
	if len(usedImports) > 0 {
		names := make([]string, 0, len(usedImports))
		for name := range usedImports {
			names = append(names, name)
		}
		slices.Sort(names)
		b.WriteString("import (\n")
		for _, name := range names {
			fmt.Fprintf(&b, "\t%s\n", imports[name])
		}
		b.WriteString(")\n\n")
	}
	for _, decl := range decls {
		start := fset.Position(decl.Pos()).Offset
		end := fset.Position(decl.End()).Offset
		b.Write(bytes.TrimSpace(src[start:end]))
		b.WriteString("\n\n")
	}
	return format.Source(b.Bytes())
}

type structuredExecutionPlan struct {
	Summary    string                    `json:"summary"`
	Operations []structuredExecutionEdit `json:"operations"`
}

type structuredExecutionEdit struct {
	Tool      string `json:"tool"`
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Content   string `json:"content,omitempty"`
	OldText   string `json:"old_text,omitempty"`
	NewText   string `json:"new_text,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type implementationBootstrap struct {
	results  []workspace.ToolResult
	messages []llama.ChatMessage
}

type implementTargetGuard struct {
	workspace           *workspace.Manager
	packet              implementationTargetPacket
	targetConsumed      bool
	allowFullTargetRead bool
	bootstrappedReads   map[string]struct{}
	bootstrapWindows    map[string][2]int
}

func newImplementTargetGuard(mgr *workspace.Manager, packet implementationTargetPacket, bootstrap []workspace.ToolResult) *implementTargetGuard {
	g := &implementTargetGuard{
		workspace:         mgr,
		packet:            packet,
		bootstrappedReads: make(map[string]struct{}),
		bootstrapWindows:  make(map[string][2]int),
	}
	for _, result := range bootstrap {
		if result.Tool != "read_file" || result.Error != "" {
			continue
		}
		path, startLine, endLine, ok := parseReadWindowSummary(result.Summary)
		if !ok {
			continue
		}
		if g.pathAllowed(path) {
			g.bootstrappedReads[path] = struct{}{}
			g.bootstrapWindows[path] = [2]int{startLine, endLine}
		}
	}
	return g
}

func (g *implementTargetGuard) redirectedTargetInspection(req workspace.ToolRequest) (*workspace.ToolResult, bool) {
	if g == nil || g.workspace == nil || len(g.packet.PrimaryPaths) == 0 {
		return nil, false
	}
	g.workspace.PrimeDiscoveredPaths(uniqueLimitedStrings(append([]string(nil), g.packet.PrimaryPaths...), 8))
	for _, fallback := range g.redirectedTargetRequests(req) {
		var result workspace.ToolResult
		switch fallback.Tool {
		case "read_file":
			readResult, ok := g.redirectedReadFileResult(fallback)
			if !ok {
				continue
			}
			result = readResult
		default:
			result = g.workspace.Execute(context.Background(), fallback)
			if result.Error != "" {
				continue
			}
			if fallback.Tool == "search_content" && strings.TrimSpace(result.Output) == "" {
				continue
			}
		}
		g.targetConsumed = true
		if fallback.Tool == "read_file" {
			if path, startLine, endLine, ok := parseReadWindowSummary(result.Summary); ok && g.pathAllowed(path) {
				g.bootstrappedReads[path] = struct{}{}
				g.bootstrapWindows[path] = [2]int{startLine, endLine}
			}
		}
		result.Summary = fmt.Sprintf(
			"Implementation target packet redirected %s to %s on %s (%s)",
			req.Tool,
			fallback.Tool,
			normalizeGuardedPath(fallback.Path),
			strings.TrimSpace(result.Summary),
		)
		return &result, true
	}
	return nil, false
}

func (g *implementTargetGuard) redirectedReadFileResult(req workspace.ToolRequest) (workspace.ToolResult, bool) {
	path := normalizeGuardedPath(req.Path)
	if path == "" {
		return workspace.ToolResult{}, false
	}
	candidates := []string{path}
	for _, base := range []string{strings.TrimSpace(g.workspace.CWD()), strings.TrimSpace(g.workspace.Root())} {
		if base == "" {
			continue
		}
		candidates = append(candidates, filepath.Join(base, path))
	}
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		start := req.StartLine
		end := req.EndLine
		if start <= 0 {
			start = 1
		}
		if end <= 0 || end > len(lines) {
			end = len(lines)
		}
		if start > end {
			continue
		}
		var b strings.Builder
		for i := start - 1; i < end && i < len(lines); i++ {
			fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
		}
		return workspace.ToolResult{
			Tool:    "read_file",
			Summary: fmt.Sprintf("Read %s lines %d-%d", path, start, end),
			Output:  strings.TrimRight(b.String(), "\n"),
		}, true
	}
	return workspace.ToolResult{}, false
}

func (g *implementTargetGuard) redirectedTargetRequests(req workspace.ToolRequest) []workspace.ToolRequest {
	path := g.redirectTargetPath(req.Path)
	if path == "" {
		return nil
	}
	requests := make([]workspace.ToolRequest, 0, 3)
	if pattern := strings.TrimSpace(req.Pattern); pattern != "" {
		requests = append(requests, workspace.ToolRequest{
			Tool:    "search_content",
			Path:    path,
			Pattern: pattern,
			Glob:    req.Glob,
		})
	}
	startLine, endLine := g.redirectReadWindow(path)
	requests = append(requests, workspace.ToolRequest{
		Tool:      "read_file",
		Path:      path,
		StartLine: startLine,
		EndLine:   endLine,
	})
	return requests
}

func (g *implementTargetGuard) redirectTargetPath(requestedPath string) string {
	requestedPath = normalizeGuardedPath(requestedPath)
	if g.pathAllowed(requestedPath) {
		for _, candidate := range g.packet.PrimaryPaths {
			candidate = normalizeGuardedPath(candidate)
			if candidate == requestedPath {
				return candidate
			}
		}
	}
	for _, anchor := range g.packet.Anchors {
		candidate := normalizeGuardedPath(anchor.Path)
		if candidate != "" {
			return candidate
		}
	}
	for _, candidate := range g.packet.PrimaryPaths {
		candidate = normalizeGuardedPath(candidate)
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func (g *implementTargetGuard) redirectReadWindow(path string) (int, int) {
	path = normalizeGuardedPath(path)
	if path == "" {
		return 1, 180
	}
	if window, ok := g.bootstrapWindows[path]; ok && window[0] > 0 && window[1] >= window[0] {
		return window[0], window[1]
	}
	for _, anchor := range g.packet.Anchors {
		if normalizeGuardedPath(anchor.Path) != path || anchor.Line <= 0 {
			continue
		}
		return maxInt(1, anchor.Line-2), anchor.Line + 18
	}
	return 1, 180
}

func (g *implementTargetGuard) targeted(tool string, path string) bool {
	if path == "" {
		return false
	}
	switch tool {
	case "edit_file", "write_file", "append_file", "go_move_declarations":
		return g.pathEditableAllowed(path)
	case "read_file", "search_content", "go_read_symbol", "go_extract_body", "go_file_outline":
		return g.pathReadableAllowed(path)
	default:
		return false
	}
}

func (g *implementTargetGuard) pathAllowed(path string) bool {
	return g.pathReadableAllowed(path)
}

func (g *implementTargetGuard) pathReadableAllowed(path string) bool {
	path = normalizeGuardedPath(path)
	if path == "" {
		return false
	}
	if g.pathForbidden(path) {
		return false
	}
	candidates := append([]string(nil), g.packet.PrimaryPaths...)
	candidates = append(candidates, g.packet.EvidencePaths...)
	candidates = append(candidates, g.packet.AnalogyPaths...)
	for _, candidate := range candidates {
		candidate = normalizeGuardedPath(candidate)
		if candidate == "" {
			continue
		}
		if path == candidate {
			return true
		}
		dir := filepath.Dir(candidate)
		if dir != "." && strings.HasPrefix(path, dir+"/") {
			return true
		}
	}
	return false
}

func (g *implementTargetGuard) pathEditableAllowed(path string) bool {
	path = normalizeGuardedPath(path)
	if path == "" || g.pathForbidden(path) {
		return false
	}
	if g.pathPrimaryAllowed(path) {
		return true
	}
	for _, candidate := range g.packet.AllowedDestinations {
		if path == normalizeGuardedPath(candidate) {
			return true
		}
	}
	return false
}

func (g *implementTargetGuard) pathPrimaryAllowed(path string) bool {
	path = normalizeGuardedPath(path)
	if path == "" || g.pathForbidden(path) {
		return false
	}
	for _, candidate := range g.packet.PrimaryPaths {
		if path == normalizeGuardedPath(candidate) {
			return true
		}
	}
	return false
}

func (g *implementTargetGuard) pathForbidden(path string) bool {
	path = normalizeGuardedPath(path)
	if path == "" {
		return false
	}
	for _, candidate := range g.packet.ForbiddenPaths {
		if path == normalizeGuardedPath(candidate) {
			return true
		}
	}
	return false
}

func (g *implementTargetGuard) blockWriteOutsideEditableTargets(req workspace.ToolRequest) *workspace.ToolResult {
	if g == nil || len(g.packet.PrimaryPaths) == 0 {
		return nil
	}
	tool := strings.TrimSpace(req.Tool)
	block := func(path string, reason string) *workspace.ToolResult {
		return &workspace.ToolResult{
			Tool:    tool,
			Summary: "Implementation target packet blocked write outside approved targets",
			Error:   fmt.Sprintf("%s is not an approved editable target (%s). %s", normalizeGuardedPath(path), strings.Join(g.packet.PrimaryPaths, ", "), reason),
		}
	}
	switch tool {
	case "edit_file", "write_file", "append_file", "mkdir":
		path := normalizeGuardedPath(req.Path)
		if path == "" {
			return nil
		}
		if !g.pathEditableAllowed(path) {
			return block(path, "Read-only evidence and analogy paths may be inspected but not modified.")
		}
	case "go_move_declarations":
		source := normalizeGuardedPath(req.Path)
		if source != "" && !g.pathPrimaryAllowed(source) {
			return block(source, "Declaration moves may only originate from approved editable targets.")
		}
		destinations := []string{normalizeGuardedPath(req.Destination)}
		for _, move := range req.Moves {
			destinations = append(destinations, normalizeGuardedPath(move.Destination))
		}
		for _, destination := range destinations {
			if destination == "" {
				continue
			}
			if g.pathForbidden(destination) || (g.pathReadableAllowed(destination) && !g.pathEditableAllowed(destination)) {
				return block(destination, "Declaration moves may not overwrite read-only evidence, analogy, or forbidden paths.")
			}
		}
	}
	return nil
}

func (m *Model) bootstrapImplementationEvidence(ctx context.Context, mgr *workspace.Manager, prompt string, packet implementationTargetPacket, history []llama.ChatMessage) implementationBootstrap {
	if mgr == nil {
		return implementationBootstrap{}
	}
	paths := append([]string(nil), packet.PrimaryPaths...)
	discovered := uniqueLimitedStrings(append(append(append([]string(nil), packet.PrimaryPaths...), packet.EvidencePaths...), packet.AnalogyPaths...), 12)
	if len(discovered) > 0 {
		mgr.PrimeDiscoveredPaths(discovered)
	}

	bootstrap := implementationBootstrap{}
	normalized := strings.ToLower(prompt)
	appendResult := func(result workspace.ToolResult) {
		bootstrap.results = append(bootstrap.results, result)
		bootstrap.messages = append(bootstrap.messages, llama.ChatMessage{
			Role:    "system",
			Content: workspace.FormatToolResultForModel(result),
		})
	}
	ensurePrimaryPathDiscovered := func(path string) string {
		path = normalizeGuardedPath(path)
		if path == "" {
			return ""
		}
		dir := filepath.Dir(path)
		if dir == "" {
			dir = "."
		}
		search := mgr.Execute(ctx, workspace.ToolRequest{
			Tool:    "search_files",
			Pattern: filepath.Base(path),
			Path:    dir,
		})
		appendResult(search)
		if search.Error != "" {
			return ""
		}
		files, _ := parseDiscoveredPaths(search.Output)
		return strings.TrimSpace(files[path])
	}
	readDiscoveredTarget := func(path string, startLine int, endLine int) workspace.ToolResult {
		path = normalizeGuardedPath(path)
		handle := ensurePrimaryPathDiscovered(path)
		req := workspace.ToolRequest{
			Tool:      "read_file",
			Path:      path,
			StartLine: startLine,
			EndLine:   endLine,
		}
		if handle != "" {
			req.ID = handle
			req.Path = ""
		}
		result := mgr.Execute(ctx, req)
		if result.Error == "start_line must be <= end_line" && startLine > 1 {
			req.StartLine = 1
			req.EndLine = 260
			result = mgr.Execute(ctx, req)
		}
		appendResult(result)
		return result
	}
	for _, symbol := range packet.Symbols {
		if normalizeGuardedPath(symbol.Path) == "" {
			continue
		}
		start := symbol.StartLine
		end := symbol.EndLine
		if start <= 0 || end <= 0 || end < start {
			start = maxInt(1, symbol.Line-6)
			end = symbol.Line + 24
		} else {
			start = maxInt(1, start-2)
			end += 4
		}
		read := readDiscoveredTarget(symbol.Path, start, end)
		if read.Error == "" {
			break
		}
	}
	for _, target := range packet.Anchors {
		read := readDiscoveredTarget(target.Path, maxInt(1, target.Line-3), target.Line+6)
		if read.Error == "" {
			break
		}
	}
	if len(packet.LiteralFragments) > 0 {
		oldText := strings.TrimSpace(packet.LiteralFragments[0])
		if oldText != "" {
			for _, path := range paths {
				search := mgr.Execute(ctx, workspace.ToolRequest{
					Tool:    "search_content",
					Pattern: locateLiteralSearchPattern(oldText),
					Path:    path,
				})
				appendResult(search)
				line, ok := firstSearchMatchLine(search.Output, path)
				if search.Error != "" || !ok {
					continue
				}
				readDiscoveredTarget(path, maxInt(1, line-2), line+2)
				break
			}
		}
	}
	if hasUILocationHint(normalized) {
		for _, path := range paths {
			if normalizeGuardedPath(path) != "internal/app/app.go" {
				continue
			}
			for _, pattern := range uiImplementationBootstrapPatterns(normalized) {
				search := mgr.Execute(ctx, workspace.ToolRequest{
					Tool:    "search_content",
					Pattern: pattern,
					Path:    path,
				})
				appendResult(search)
				line, ok := firstSearchMatchLine(search.Output, path)
				if search.Error != "" || !ok {
					continue
				}
				readDiscoveredTarget(path, maxInt(1, line-1), line+18)
				return bootstrap
			}
		}
	}
	for _, path := range packet.AnalogyPaths {
		read := readDiscoveredTarget(path, 1, 180)
		if read.Error == "" {
			break
		}
	}
	for _, path := range packet.EvidencePaths {
		read := readDiscoveredTarget(path, 1, 180)
		if read.Error == "" {
			break
		}
	}

	for _, target := range analysisSummaryTargets(history) {
		read := readDiscoveredTarget(target.Path, maxInt(1, target.Line-2), target.Line+2)
		if read.Error == "" {
			break
		}
	}
	if len(paths) > 1 {
		seenRead := make(map[string]struct{})
		for _, result := range bootstrap.results {
			if result.Tool != "read_file" || result.Error != "" {
				continue
			}
			path, _, _, ok := parseReadWindowSummary(result.Summary)
			if ok {
				seenRead[path] = struct{}{}
			}
		}
		readCount := 0
		for _, path := range paths {
			if _, ok := seenRead[normalizeGuardedPath(path)]; ok {
				readCount++
				if readCount >= minInt(2, len(paths)) {
					break
				}
				continue
			}
			read := readDiscoveredTarget(path, 1, 180)
			if read.Error == "" {
				readCount++
			}
			if readCount >= minInt(2, len(paths)) {
				break
			}
		}
		if readCount >= minInt(2, len(paths)) {
			return bootstrap
		}
	}
	if len(bootstrap.results) == 0 {
		for _, path := range paths {
			read := readDiscoveredTarget(path, 1, 180)
			if read.Error == "" {
				break
			}
		}
	}
	return bootstrap
}

func uiImplementationBootstrapPatterns(normalized string) []string {
	patterns := make([]string, 0, 4)
	add := func(pattern string) {
		for _, existing := range patterns {
			if existing == pattern {
				return
			}
		}
		patterns = append(patterns, pattern)
	}
	if containsAny(normalized, "top", "header", "banner", "window") {
		add("header :=|header \\+=")
	}
	if containsAny(normalized, "footer", "bottom", "underneath", "below") {
		add("footer :=|footer \\+=|return lipgloss\\.JoinVertical")
	}
	if containsAny(normalized, "badge", "chat", "menu") {
		add("case \"assistant\"|assistantStyle\\.Render|content = m\\.spinner\\.View")
	}
	if len(patterns) == 0 {
		add("header :=|footer :=|return lipgloss\\.JoinVertical")
	}
	return patterns
}

type implementationVerification struct {
	commands         []string
	reasons          []string
	results          []workspace.ToolResult
	failed           bool
	coverage         string
	confidence       string
	riskLevel        string
	expectedFailures []string
	repairPolicy     string
}

func applyImplementationGoalChecks(mgr *workspace.Manager, prompt string, packet implementationTargetPacket, changedPaths []string, verify implementationVerification) implementationVerification {
	if mgr == nil || !promptRequestsWholeFileRefactor(prompt) {
		return verify
	}
	source := ""
	for _, path := range packet.PrimaryPaths {
		if strings.HasSuffix(path, ".go") {
			source = path
			break
		}
	}
	if source == "" {
		return verify
	}
	source = normalizeGuardedPath(source)
	sourceDir := filepath.Dir(source)
	changed := make(map[string]struct{}, len(changedPaths))
	for _, path := range changedPaths {
		changed[normalizeGuardedPath(path)] = struct{}{}
	}
	_, sourceChanged := changed[source]
	movedTargets := 0
	for path := range changed {
		if path == source {
			continue
		}
		if filepath.Dir(path) == sourceDir && strings.HasSuffix(path, ".go") {
			movedTargets++
		}
	}
	fullPath := filepath.Join(mgr.Root(), source)
	lineCount := 0
	if data, err := os.ReadFile(fullPath); err == nil {
		lineCount = strings.Count(string(data), "\n")
		if len(data) > 0 && data[len(data)-1] != '\n' {
			lineCount++
		}
	}
	if sourceChanged && movedTargets >= 2 && (lineCount == 0 || lineCount < 5000) {
		return verify
	}

	verify.failed = true
	reason := fmt.Sprintf("whole-file refactor goal check failed for %s: changed source=%t, destination Go files in same package=%d, remaining source lines=%d", source, sourceChanged, movedTargets, lineCount)
	verify.results = append(verify.results, workspace.ToolResult{
		Tool:    "implementation_goal_check",
		Summary: "Whole-file refactor goal check failed",
		Error:   reason,
	})
	return verify
}

func (m *Model) runImplementationVerification(ctx context.Context, mgr *workspace.Manager, startStep int, changedPaths ...string) implementationVerification {
	plan := implementationVerificationPlan(mgr, changedPaths)
	if len(plan.Commands) == 0 {
		return implementationVerification{}
	}
	m.setExecutionVerificationPlan(plan)
	out := implementationVerification{
		commands:         append([]string(nil), plan.Commands...),
		reasons:          append([]string(nil), plan.Reasons...),
		results:          make([]workspace.ToolResult, 0, len(plan.Commands)),
		coverage:         strings.TrimSpace(plan.Coverage),
		confidence:       strings.TrimSpace(plan.Confidence),
		riskLevel:        strings.TrimSpace(plan.RiskLevel),
		expectedFailures: append([]string(nil), plan.ExpectedFailures...),
		repairPolicy:     strings.TrimSpace(plan.RepairPolicy),
	}
	m.emitPhaseCard(analysisLaneImplement, "verify", phaseStateActive, fmt.Sprintf("Running %d verification command(s).", len(plan.Commands)))
	for i, command := range plan.Commands {
		m.emitPhaseCard(analysisLaneImplement, "verify", phaseStateActive, fmt.Sprintf("Running check %d/%d: %s", i+1, len(plan.Commands), command))
		req := workspace.ToolRequest{Tool: "shell", Command: command, Workdir: "."}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      startStep + i,
			Agent:     "implement_verify",
			Raw:       fmt.Sprintf("{\"tool\":\"shell\",\"command\":%q,\"workdir\":\".\"}", command),
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    "shell_narrow:" + command,
		})
		result := mgr.Execute(ctx, req)
		out.results = append(out.results, result)
		m.logToolResult(agent.ToolResultEvent{Step: startStep + i, Agent: "implement_verify", Result: result})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
		if shouldInvalidateExploreCacheForTool(result) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
			}
		}
		if result.Error != "" {
			out.failed = true
		}
	}
	if out.failed {
		m.emitPhaseCard(analysisLaneImplement, "verify", phaseStateFailed, "Verification failed. Review the failing check output before trusting the edit.")
	} else {
		detail := fmt.Sprintf("Verification passed with %d successful check(s).", len(out.results))
		if out.confidence != "" {
			detail += " Confidence: " + out.confidence
			if out.coverage != "" {
				detail += " (" + out.coverage + ")."
			} else {
				detail += "."
			}
		}
		if plan.UsesFullSuite {
			detail += " Full-suite escalation was required by the computed blast radius."
		}
		m.emitPhaseCard(analysisLaneImplement, "verify", phaseStateDone, detail)
	}
	m.logVerificationFeedback(verificationFeedbackFromResult(plan, out))
	return out
}

func verificationFeedbackFromResult(plan verificationPlan, verify implementationVerification) verificationFeedbackSnapshot {
	failed := make([]string, 0)
	for i, result := range verify.results {
		if strings.TrimSpace(result.Error) == "" {
			continue
		}
		command := ""
		if i < len(verify.commands) {
			command = verify.commands[i]
		}
		if command == "" {
			command = result.Tool
		}
		failed = append(failed, command)
	}
	recommendation := "keep current verification plan"
	if verify.failed {
		recommendation = "repair failing checks before widening scope"
	} else if plan.UsesFullSuite {
		recommendation = "record full-suite success for similar high-risk surfaces"
	} else if strings.EqualFold(plan.Confidence, "low") {
		recommendation = "consider escalating to targeted tests when similar files change again"
	}
	return verificationFeedbackSnapshot{
		RiskLevel:             strings.TrimSpace(plan.RiskLevel),
		Coverage:              strings.TrimSpace(plan.Coverage),
		Confidence:            strings.TrimSpace(plan.Confidence),
		Commands:              append([]string(nil), verify.commands...),
		FailedCommands:        uniqueNonEmptyStrings(failed),
		LearnedRecommendation: recommendation,
	}
}

func implementationVerifyCommands(mgr *workspace.Manager) []string {
	return implementationVerificationPlan(mgr, nil).Commands
}

func changedPathsFromImplementEvents(requests map[int]agent.ToolRequestEvent, results map[int]agent.ToolResultEvent) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)
	for step, resultEvent := range results {
		req, ok := requests[step]
		if !ok {
			continue
		}
		if !writeToolResultChanged(req.Request, resultEvent.Result) {
			continue
		}
		eventPaths := []string{strings.TrimSpace(req.Request.Path), strings.TrimSpace(req.Request.Destination)}
		for _, move := range req.Request.Moves {
			eventPaths = append(eventPaths, strings.TrimSpace(move.Destination))
		}
		for _, path := range eventPaths {
			if path == "" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

func composeImplementationSummary(reply string, changedPaths []string, verify implementationVerification, refactorStage *structuralRefactorStage) string {
	sections := make([]string, 0, 4)
	if strings.TrimSpace(reply) != "" {
		sections = append(sections, strings.TrimSpace(reply))
	}
	if len(changedPaths) > 0 {
		var b strings.Builder
		b.WriteString("Changed files:\n")
		for _, path := range changedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
		sections = append(sections, strings.TrimSpace(b.String()))
	}
	if len(verify.results) > 0 {
		var b strings.Builder
		b.WriteString("Verification:\n")
		if verify.confidence != "" {
			b.WriteString("- confidence: ")
			b.WriteString(verify.confidence)
			if verify.coverage != "" {
				b.WriteString(" (")
				b.WriteString(verify.coverage)
				b.WriteString(")")
			}
			b.WriteString("\n")
		}
		if strings.TrimSpace(verify.riskLevel) != "" {
			b.WriteString("- risk: ")
			b.WriteString(strings.TrimSpace(verify.riskLevel))
			b.WriteString("\n")
		}
		if len(verify.expectedFailures) > 0 {
			b.WriteString("- expected failure modes: ")
			b.WriteString(strings.Join(uniqueLimitedStrings(verify.expectedFailures, 4), ", "))
			b.WriteString("\n")
		}
		if strings.TrimSpace(verify.repairPolicy) != "" {
			b.WriteString("- repair policy: ")
			b.WriteString(truncateText(strings.TrimSpace(verify.repairPolicy), 180))
			b.WriteString("\n")
		}
		for i, result := range verify.results {
			command := fmt.Sprintf("check %d", i+1)
			if i < len(verify.commands) && strings.TrimSpace(verify.commands[i]) != "" {
				command = "`" + verify.commands[i] + "`"
			}
			status := "passed"
			if result.Error != "" {
				status = "failed"
			}
			b.WriteString("- ")
			b.WriteString(command)
			b.WriteString(": ")
			b.WriteString(status)
			b.WriteString("\n")
		}
		sections = append(sections, strings.TrimSpace(b.String()))
	}
	if refactorStage != nil && strings.TrimSpace(refactorStage.NextStep) != "" {
		var b strings.Builder
		b.WriteString("Refactor stage:\n")
		b.WriteString("- stage: ")
		b.WriteString(strconv.Itoa(refactorStage.Stage))
		b.WriteString("\n")
		if strings.TrimSpace(refactorStage.FocusPath) != "" {
			b.WriteString("- focus: ")
			b.WriteString(refactorStage.FocusPath)
			b.WriteString("\n")
		}
		if len(refactorStage.Remaining) > 0 {
			b.WriteString("- remaining: ")
			b.WriteString(strings.Join(refactorStage.Remaining, ", "))
			b.WriteString("\n")
		}
		b.WriteString("- next: ")
		b.WriteString(refactorStage.NextStep)
		sections = append(sections, strings.TrimSpace(b.String()))
	}
	return strings.Join(sections, "\n\n")
}

type finalAnswerCriticResult struct {
	Contract string   `json:"contract,omitempty"`
	Passed   bool     `json:"passed"`
	Action   string   `json:"action,omitempty"`
	Repaired bool     `json:"repaired,omitempty"`
	Findings []string `json:"findings,omitempty"`
}

func (m *Model) applyFinalAnswerCritic(answer string, changedPaths []string, verify implementationVerification, refactorStage *structuralRefactorStage) string {
	critic := critiqueImplementationFinalAnswer(answer, changedPaths, verify)
	if critic.Passed {
		critic.Action = "accept"
		m.logFinalAnswerCritic(critic)
		return answer
	}
	critic.Action = "repair"
	critic.Repaired = true
	repaired := composeImplementationSummary("", changedPaths, verify, refactorStage)
	if strings.TrimSpace(repaired) == "" {
		repaired = "Implementation completed, but the final answer critic could not reconstruct a deterministic summary."
	}
	repairedCritic := critiqueImplementationFinalAnswer(repaired, changedPaths, verify)
	if !repairedCritic.Passed {
		critic.Findings = append(critic.Findings, prefixedCriticFindings("repair", repairedCritic.Findings)...)
		critic.Repaired = false
	}
	m.logFinalAnswerCritic(critic)
	return repaired
}

func critiqueImplementationFinalAnswer(answer string, changedPaths []string, verify implementationVerification) finalAnswerCriticResult {
	result := finalAnswerCriticResult{
		Contract: string(answerContractImplementation),
		Passed:   true,
	}
	addFinding := func(finding string) {
		result.Passed = false
		result.Findings = append(result.Findings, finding)
	}
	normalized := strings.ToLower(strings.TrimSpace(answer))
	if strings.TrimSpace(answer) == "" {
		addFinding("final answer is empty")
		return result
	}
	if len(changedPaths) > 0 {
		if !strings.Contains(normalized, "changed files:") {
			addFinding("missing changed files section")
		}
		for _, path := range changedPaths {
			path = strings.TrimSpace(path)
			if path != "" && !strings.Contains(normalized, strings.ToLower(path)) {
				addFinding("missing changed file path: " + path)
			}
		}
	}
	if len(verify.results) > 0 {
		if !strings.Contains(normalized, "verification:") {
			addFinding("missing verification section")
		}
		for _, command := range verify.commands {
			command = strings.TrimSpace(command)
			if command != "" && !strings.Contains(normalized, strings.ToLower(command)) {
				addFinding("missing verification command: " + command)
			}
		}
		if strings.TrimSpace(verify.riskLevel) != "" && !strings.Contains(normalized, "risk:") {
			addFinding("missing verification risk")
		}
		if verify.failed && containsAny(normalized, "verification passed", "all tests passed", "tests passed", "checks passed") {
			addFinding("overconfident verification claim after failed verification")
		}
		if !verify.failed && len(changedPaths) > 0 && containsAny(normalized, "did not complete cleanly", "did not complete the requested edit", "final blocker:", "blocker:") {
			addFinding("stale blocker language after verified implementation")
		}
	}
	return result
}

func (m *Model) logFinalAnswerCritic(critic finalAnswerCriticResult) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(critic)
	if err != nil {
		return
	}
	m.logMessage("final_answer_critic", string(data))
}

func buildImplementationNoopSummary(reply string, results map[int]agent.ToolResultEvent) string {
	if strings.TrimSpace(reply) != "" && !strings.Contains(strings.ToLower(reply), "recommended next step") {
		return strings.TrimSpace(reply)
	}
	var lastSummary string
	var lastError string
	steps := make([]int, 0, len(results))
	for step := range results {
		steps = append(steps, step)
	}
	slices.Sort(steps)
	for _, step := range steps {
		result := results[step].Result
		if strings.TrimSpace(result.Summary) != "" {
			lastSummary = strings.TrimSpace(result.Summary)
		}
		if strings.TrimSpace(result.Error) != "" {
			lastError = strings.TrimSpace(result.Error)
		}
	}
	if lastSummary == "" {
		lastSummary = "implementation agent stopped before making a change"
	}
	if lastError == "" {
		lastError = "no file edits were applied"
	}
	return "Implementation agent did not complete the requested edit.\n\nLast step:\n- " + lastSummary + "\n\nBlocker:\n- " + lastError
}

func implementationReplyReportsNoChangeBlocker(reply string) bool {
	normalized := strings.ToLower(strings.TrimSpace(reply))
	return containsAny(
		normalized,
		"did not complete",
		"could not complete",
		"failed",
		"blocker",
		"blocked",
		"not an approved",
		"budget exhausted",
	)
}

func (m *Model) tryDirectPromptEdit(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil {
		return directImplementationShortcut{}
	}
	if promptShouldSkipDirectQuotedEdit(prompt) {
		return directImplementationShortcut{}
	}
	paths := promptMentionedWorkspacePaths(prompt)
	quoted := quotedPromptFragments(prompt)
	if len(paths) == 0 || len(quoted) < 2 {
		return directImplementationShortcut{}
	}

	oldText := strings.TrimSpace(quoted[0])
	newText := strings.TrimSpace(quoted[1])
	if oldText == "" || newText == "" || oldText == newText {
		return directImplementationShortcut{}
	}

	step := startStep
	for _, path := range paths {
		lineNumber := firstPromptEditLine(mgr, path, oldText)
		req := workspace.ToolRequest{
			Tool:    "edit_file",
			Path:    path,
			OldText: oldText,
			NewText: newText,
		}
		if guard != nil {
			if blocked := guard.PreExecute(req); blocked != nil {
				result := *blocked
				if onBlocked != nil {
					result = onBlocked(req, result)
				}
				m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
				m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
				step++
				if isWriteApprovalRequiredResult(result) {
					return directImplementationShortcut{terminal: true, nextStep: step}
				}
				continue
			}
		}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       fmt.Sprintf("{\"tool\":\"edit_file\",\"path\":%q,\"old_text\":%q,\"new_text\":%q}", path, oldText, newText),
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    "edit_file:" + path,
		})
		if capture := captureExecutionJournalResult(journal, req); capture != nil {
			m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: *capture})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
			step++
			continue
		}
		result := mgr.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
		if shouldInvalidateExploreCacheForTool(result) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
			}
		}
		step++
		if result.Error != "" {
			continue
		}
		summary := fmt.Sprintf("Applied a targeted edit in %s by replacing %q with %q.", path, oldText, newText)
		if lineNumber > 0 {
			summary = fmt.Sprintf("Applied a targeted edit in %s line %d by replacing %q with %q.", path, lineNumber, oldText, newText)
		}
		return directImplementationShortcut{
			applied:      true,
			changedPaths: []string{path},
			summary:      summary,
			nextStep:     step,
		}
	}
	return directImplementationShortcut{}
}

func promptShouldSkipDirectQuotedEdit(prompt string) bool {
	normalized := strings.ToLower(prompt)
	return containsAny(normalized,
		"go_file_outline",
		"go_move_declarations",
		"pattern:\"tag:",
		"pattern:'tag:",
		"multi-file refactor",
		"large go refactor",
		"split the monolithic file",
		"split into cohesive",
	)
}

func (m *Model) tryDeterministicVerificationRepair(
	ctx context.Context,
	mgr *workspace.Manager,
	verify implementationVerification,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil {
		return directImplementationShortcut{}
	}
	diagnostics := parseUnusedVariableDiagnostics(verify)
	if len(diagnostics) == 0 {
		return directImplementationShortcut{}
	}

	step := startStep
	changedPaths := make([]string, 0, len(diagnostics))
	repairs := make([]string, 0, len(diagnostics))
	for _, diag := range diagnostics {
		readReq := workspace.ToolRequest{
			Tool:      "read_file",
			Path:      diag.Path,
			StartLine: diag.Line,
			EndLine:   diag.Line,
		}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       fmt.Sprintf("{\"tool\":\"read_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d}", diag.Path, diag.Line, diag.Line),
			Request:   readReq,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    fmt.Sprintf("read_file:%s:%d", diag.Path, diag.Line),
		})
		readResult := mgr.Execute(ctx, readReq)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: readResult})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(readResult)}
		step++
		if readResult.Error != "" {
			continue
		}
		lineText, ok := extractReadFileLine(readResult.Output, diag.Line)
		if !ok || !looksLikeUnusedVariableDeclaration(lineText, diag.Name) {
			continue
		}

		editReq := workspace.ToolRequest{
			Tool:      "edit_file",
			Path:      diag.Path,
			StartLine: diag.Line,
			EndLine:   diag.Line,
			Content:   "",
		}
		if guard != nil {
			if blocked := guard.PreExecute(editReq); blocked != nil {
				result := *blocked
				if onBlocked != nil {
					result = onBlocked(editReq, result)
				}
				m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
				m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
				step++
				if isWriteApprovalRequiredResult(result) {
					return directImplementationShortcut{terminal: true, nextStep: step}
				}
				continue
			}
		}
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       fmt.Sprintf("{\"tool\":\"edit_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d,\"content\":\"\"}", diag.Path, diag.Line, diag.Line),
			Request:   editReq,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    fmt.Sprintf("edit_file:%s:%d-remove-unused", diag.Path, diag.Line),
		})
		if capture := captureExecutionJournalResult(journal, editReq); capture != nil {
			m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: *capture})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
			step++
			continue
		}
		editResult := mgr.Execute(ctx, editReq)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: editResult})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(editResult)}
		if shouldInvalidateExploreCacheForTool(editResult) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", editResult.Tool),
			}
		}
		step++
		if editResult.Error != "" {
			continue
		}
		changedPaths = append(changedPaths, diag.Path)
		repairs = append(repairs, fmt.Sprintf("removed unused declaration %q in %s line %d", diag.Name, diag.Path, diag.Line))
	}

	if len(changedPaths) == 0 {
		return directImplementationShortcut{}
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: uniqueNonEmptyStrings(changedPaths),
		summary:      "Applied deterministic verification cleanup: " + strings.Join(repairs, "; ") + ".",
		nextStep:     step,
	}
}

func (m *Model) tryStructuredExecutionPlan(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil || len(packet.PrimaryPaths) == 0 {
		return directImplementationShortcut{}
	}
	feedback := ""
	var plan structuredExecutionPlan
	var rawReply string
	for attempt := 0; attempt < 2; attempt++ {
		var err error
		plan, rawReply, err = m.proposeStructuredExecutionPlan(ctx, mgr, history, opts, prompt, packet, feedback)
		if err != nil {
			if attempt == 0 {
				feedback = "Previous reply was rejected: " + err.Error()
				continue
			}
			m.logMessage("implement_execution_plan", "structured plan rejected: "+err.Error())
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_execution_plan", "structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		if len(plan.Operations) == 0 {
			if attempt == 0 {
				feedback = "Previous reply proposed no usable operations. Return at least one valid bounded edit or an explicit empty plan only if the request cannot be safely applied."
				continue
			}
			summary := strings.TrimSpace(plan.Summary)
			if summary == "" {
				summary = "no operations proposed"
			}
			m.logMessage("implement_execution_plan", "structured plan produced no operations: "+summary)
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_execution_plan", "structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		if err := validateStructuredExecutionPlan(packet, plan); err != nil {
			if attempt == 0 {
				feedback = "Previous proposed plan was rejected: " + err.Error()
				continue
			}
			m.logMessage("implement_execution_plan", "structured plan rejected after policy validation: "+err.Error())
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_execution_plan", "structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		validationErr := ""
		reqs := make([]workspace.ToolRequest, 0, len(plan.Operations))
		for _, op := range plan.Operations {
			req := workspace.ToolRequest{
				Tool:      strings.TrimSpace(op.Tool),
				Path:      normalizeGuardedPath(op.Path),
				StartLine: op.StartLine,
				EndLine:   op.EndLine,
				Content:   op.Content,
				OldText:   op.OldText,
				NewText:   op.NewText,
			}
			if blocked := validateStructuredExecutionEdit(packet, req); blocked != nil {
				validationErr = blocked.Error
				break
			}
			reqs = append(reqs, req)
		}
		if validationErr == "" {
			if err := preflightStructuredExecutionPlan(mgr, reqs); err != nil {
				validationErr = err.Error()
			}
		}
		if validationErr == "" {
			break
		}
		if attempt == 0 {
			feedback = "Previous proposed plan was rejected: " + validationErr
			continue
		}
		m.logMessage("implement_execution_plan", "structured plan rejected after preflight: "+validationErr)
		if preview := previewReplyForLog(rawReply); preview != "" {
			m.logMessage("implement_execution_plan", "structured plan raw reply: "+preview)
		}
		return directImplementationShortcut{}
	}
	m.logMessage("implement_execution_plan", fmt.Sprintf("structured plan proposed %d operation(s): %s", len(plan.Operations), firstNonEmptyString(strings.TrimSpace(plan.Summary), "no summary")))
	m.setExecutionPlanSummary(
		analysisLaneImplement,
		firstNonEmptyString(strings.TrimSpace(plan.Summary), "Prepared a bounded structured edit plan."),
		formatStructuredExecutionPlanSteps(plan),
	)
	reqs := make([]workspace.ToolRequest, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		reqs = append(reqs, workspace.ToolRequest{
			Tool:      strings.TrimSpace(op.Tool),
			Path:      normalizeGuardedPath(op.Path),
			StartLine: op.StartLine,
			EndLine:   op.EndLine,
			Content:   op.Content,
			OldText:   op.OldText,
			NewText:   op.NewText,
		})
	}
	if proposal := buildWriteApprovalProposalForRequests(ctx, mgr, prompt, guard, reqs); proposal != nil {
		m.logMessage("implement_execution_plan", "staged guarded write approval for structured plan scope: "+strings.Join(proposal.Paths, ", "))
		return directImplementationShortcut{
			terminal: true,
			proposal: proposal,
		}
	}
	changedPaths := make([]string, 0, len(plan.Operations))
	appliedSummaries := make([]string, 0, len(plan.Operations)+1)
	step := startStep
	for _, op := range plan.Operations {
		req := workspace.ToolRequest{
			Tool:      strings.TrimSpace(op.Tool),
			Path:      normalizeGuardedPath(op.Path),
			StartLine: op.StartLine,
			EndLine:   op.EndLine,
			Content:   op.Content,
			OldText:   op.OldText,
			NewText:   op.NewText,
		}
		if blocked := validateStructuredExecutionEdit(packet, req); blocked != nil {
			return directImplementationShortcut{}
		}
		if guard != nil {
			if blocked := guard.PreExecute(req); blocked != nil {
				result := *blocked
				if onBlocked != nil {
					result = onBlocked(req, result)
				}
				m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
				m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
				step++
				if isWriteApprovalRequiredResult(result) {
					return directImplementationShortcut{terminal: true, nextStep: step}
				}
				return directImplementationShortcut{}
			}
		}
		raw := marshalStructuredExecutionRequest(req)
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       raw,
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    "structured_" + toolActionForStructuredPlan(req),
		})
		if capture := captureExecutionJournalResult(journal, req); capture != nil {
			m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: *capture})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
			step++
			return directImplementationShortcut{}
		}
		result := mgr.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
		if writeToolResultChanged(req, result) && shouldInvalidateExploreCacheForTool(result) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
			}
		}
		step++
		if result.Error != "" {
			return directImplementationShortcut{}
		}
		if writeToolResultChanged(req, result) {
			changedPaths = append(changedPaths, req.Path)
			if strings.TrimSpace(op.Reason) != "" {
				appliedSummaries = append(appliedSummaries, fmt.Sprintf("%s (%s)", req.Path, strings.TrimSpace(op.Reason)))
			} else {
				appliedSummaries = append(appliedSummaries, req.Path)
			}
		}
	}
	if len(changedPaths) == 0 {
		m.logMessage("implement_execution_plan", "structured plan produced only no-op edits")
		return directImplementationShortcut{}
	}
	summary := strings.TrimSpace(plan.Summary)
	if summary == "" {
		summary = "Applied a structured execution plan to the targeted files."
	}
	if len(appliedSummaries) > 0 {
		summary = strings.TrimSpace(summary + " Operations: " + strings.Join(appliedSummaries, "; ") + ".")
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: uniqueNonEmptyStrings(changedPaths),
		summary:      summary,
		nextStep:     step,
	}
}

func (m *Model) tryFocusedWindowRewrite(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	bootstrap implementationBootstrap,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil || len(packet.PrimaryPaths) != 1 {
		m.logMessage("implement_focused_rewrite", "focused rewrite skipped: requires exactly one primary target file")
		return directImplementationShortcut{}
	}
	path, startLine, endLine, snippet, ok := focusedRewriteWindow(packet, bootstrap)
	if !ok {
		m.logMessage("implement_focused_rewrite", "focused rewrite skipped: no suitable bootstrapped read window")
		return directImplementationShortcut{}
	}
	reply, err := m.proposeFocusedWindowRewrite(ctx, mgr, prompt, opts, packet, path, startLine, endLine, snippet, false)
	if err != nil {
		m.logMessage("implement_focused_rewrite", "focused rewrite rejected: "+err.Error())
		return directImplementationShortcut{}
	}
	content := cleanFocusedRewriteReply(reply)
	if content == "" || content == snippet {
		retryReply, retryErr := m.proposeFocusedWindowRewrite(ctx, mgr, prompt, opts, packet, path, startLine, endLine, snippet, true)
		if retryErr != nil {
			m.logMessage("implement_focused_rewrite", "focused rewrite retry rejected: "+retryErr.Error())
			return directImplementationShortcut{}
		}
		content = cleanFocusedRewriteReply(retryReply)
		if content == "" || content == snippet {
			m.logMessage("implement_focused_rewrite", "focused rewrite produced no usable change")
			return directImplementationShortcut{}
		}
	}
	req := workspace.ToolRequest{
		Tool:      "edit_file",
		Path:      path,
		StartLine: startLine,
		EndLine:   endLine,
		Content:   content,
	}
	if err := preflightStructuredExecutionRequest(mgr, req); err != nil {
		m.logMessage("implement_focused_rewrite", "focused rewrite rejected by preflight: "+err.Error())
		return directImplementationShortcut{}
	}
	if guard != nil {
		if blocked := guard.PreExecute(req); blocked != nil {
			result := *blocked
			if onBlocked != nil {
				result = onBlocked(req, result)
			}
			m.logToolResult(agent.ToolResultEvent{Step: startStep, Agent: "implement", Result: result})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
			if isWriteApprovalRequiredResult(result) {
				return directImplementationShortcut{terminal: true, nextStep: startStep + 1}
			}
			return directImplementationShortcut{}
		}
	}
	raw := marshalStructuredExecutionRequest(req)
	m.logMessage("implement_focused_rewrite", fmt.Sprintf("focused rewrite proposed for %s:%d-%d", path, startLine, endLine))
	m.logToolRequest(agent.ToolRequestEvent{
		Step:      startStep,
		Agent:     "implement",
		Raw:       raw,
		Request:   req,
		Mode:      workspace.ModeNormal,
		Broad:     false,
		Narrowing: true,
		Action:    "focused_window_rewrite",
	})
	if capture := captureExecutionJournalResult(journal, req); capture != nil {
		m.logToolResult(agent.ToolResultEvent{Step: startStep, Agent: "implement", Result: *capture})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
		return directImplementationShortcut{}
	}
	result := mgr.Execute(ctx, req)
	m.logToolResult(agent.ToolResultEvent{Step: startStep, Agent: "implement", Result: result})
	m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
	if shouldInvalidateExploreCacheForTool(result) {
		m.events <- exploreCacheInvalidatedMsg{
			reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
		}
	}
	if result.Error != "" {
		return directImplementationShortcut{}
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: []string{path},
		summary:      fmt.Sprintf("Applied a focused rewrite to %s lines %d-%d.", path, startLine, endLine),
		nextStep:     startStep + 1,
	}
}

func (m *Model) tryFocusedStructuredExecutionPlan(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	bootstrap implementationBootstrap,
	startStep int,
	journal *executionJournalRecorder,
	guard *implementWriteGuard,
	onBlocked func(req workspace.ToolRequest, blocked workspace.ToolResult) workspace.ToolResult,
) directImplementationShortcut {
	if mgr == nil || len(packet.PrimaryPaths) != 1 {
		m.logMessage("implement_focused_plan", "focused structured plan skipped: requires exactly one primary target file")
		return directImplementationShortcut{}
	}
	path, windowStart, windowEnd, snippet, ok := focusedRewriteWindow(packet, bootstrap)
	if !ok {
		m.logMessage("implement_focused_plan", "focused structured plan skipped: no suitable bootstrapped read window")
		return directImplementationShortcut{}
	}
	feedback := ""
	var plan structuredExecutionPlan
	var rawReply string
	for attempt := 0; attempt < 2; attempt++ {
		var err error
		plan, rawReply, err = m.proposeFocusedStructuredExecutionPlan(ctx, mgr, prompt, opts, packet, path, windowStart, windowEnd, snippet, feedback)
		if err != nil {
			if attempt == 0 {
				feedback = "Previous reply was rejected: " + err.Error()
				continue
			}
			m.logMessage("implement_focused_plan", "focused structured plan rejected: "+err.Error())
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_focused_plan", "focused structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		if len(plan.Operations) == 0 {
			if attempt == 0 {
				feedback = "Previous reply proposed no usable operations. Return at least one valid bounded edit within the current file window."
				continue
			}
			summary := strings.TrimSpace(plan.Summary)
			if summary == "" {
				summary = "no operations proposed"
			}
			m.logMessage("implement_focused_plan", "focused structured plan produced no operations: "+summary)
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_focused_plan", "focused structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		if err := validateStructuredExecutionPlan(packet, plan); err != nil {
			if attempt == 0 {
				feedback = "Previous proposed edit was rejected: " + err.Error()
				continue
			}
			m.logMessage("implement_focused_plan", "focused structured plan rejected after policy validation: "+err.Error())
			if preview := previewReplyForLog(rawReply); preview != "" {
				m.logMessage("implement_focused_plan", "focused structured plan raw reply: "+preview)
			}
			return directImplementationShortcut{}
		}
		validationErr := ""
		for _, op := range plan.Operations {
			req := workspace.ToolRequest{
				Tool:      strings.TrimSpace(op.Tool),
				Path:      normalizeGuardedPath(op.Path),
				StartLine: op.StartLine,
				EndLine:   op.EndLine,
				Content:   op.Content,
				OldText:   op.OldText,
				NewText:   op.NewText,
			}
			if blocked := validateFocusedStructuredExecutionEdit(path, windowStart, windowEnd, snippet, req); blocked != nil {
				validationErr = blocked.Error
				break
			}
			if err := preflightStructuredExecutionRequest(mgr, req); err != nil {
				validationErr = err.Error()
				break
			}
		}
		if validationErr == "" {
			break
		}
		if attempt == 0 {
			feedback = "Previous proposed edit was rejected: " + validationErr
			continue
		}
		m.logMessage("implement_focused_plan", "focused structured plan op rejected: "+validationErr)
		if preview := previewReplyForLog(rawReply); preview != "" {
			m.logMessage("implement_focused_plan", "focused structured plan raw reply: "+preview)
		}
		return directImplementationShortcut{}
	}
	m.logMessage("implement_focused_plan", fmt.Sprintf("focused structured plan proposed %d operation(s): %s", len(plan.Operations), firstNonEmptyString(strings.TrimSpace(plan.Summary), "no summary")))
	m.setExecutionPlanSummary(
		analysisLaneImplement,
		firstNonEmptyString(strings.TrimSpace(plan.Summary), "Prepared a focused structured edit plan."),
		formatStructuredExecutionPlanSteps(plan),
	)
	reqs := make([]workspace.ToolRequest, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		reqs = append(reqs, workspace.ToolRequest{
			Tool:      strings.TrimSpace(op.Tool),
			Path:      normalizeGuardedPath(op.Path),
			StartLine: op.StartLine,
			EndLine:   op.EndLine,
			Content:   op.Content,
			OldText:   op.OldText,
			NewText:   op.NewText,
		})
	}
	if proposal := buildWriteApprovalProposalForRequests(ctx, mgr, prompt, guard, reqs); proposal != nil {
		m.logMessage("implement_focused_plan", "staged guarded write approval for focused plan scope: "+strings.Join(proposal.Paths, ", "))
		return directImplementationShortcut{
			terminal: true,
			proposal: proposal,
		}
	}

	step := startStep
	changedPaths := make([]string, 0, len(plan.Operations))
	appliedSummaries := make([]string, 0, len(plan.Operations)+1)
	for _, op := range plan.Operations {
		req := workspace.ToolRequest{
			Tool:      strings.TrimSpace(op.Tool),
			Path:      normalizeGuardedPath(op.Path),
			StartLine: op.StartLine,
			EndLine:   op.EndLine,
			Content:   op.Content,
			OldText:   op.OldText,
			NewText:   op.NewText,
		}
		if guard != nil {
			if blocked := guard.PreExecute(req); blocked != nil {
				result := *blocked
				if onBlocked != nil {
					result = onBlocked(req, result)
				}
				m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
				m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
				step++
				if isWriteApprovalRequiredResult(result) {
					return directImplementationShortcut{terminal: true, nextStep: step}
				}
				return directImplementationShortcut{}
			}
		}
		raw := marshalStructuredExecutionRequest(req)
		m.logToolRequest(agent.ToolRequestEvent{
			Step:      step,
			Agent:     "implement",
			Raw:       raw,
			Request:   req,
			Mode:      workspace.ModeNormal,
			Broad:     false,
			Narrowing: true,
			Action:    "focused_structured_" + toolActionForStructuredPlan(req),
		})
		if capture := captureExecutionJournalResult(journal, req); capture != nil {
			m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: *capture})
			m.events <- toolTraceMsg{text: workspace.FormatToolTrace(*capture)}
			step++
			return directImplementationShortcut{}
		}
		result := mgr.Execute(ctx, req)
		m.logToolResult(agent.ToolResultEvent{Step: step, Agent: "implement", Result: result})
		m.events <- toolTraceMsg{text: workspace.FormatToolTrace(result)}
		if writeToolResultChanged(req, result) && shouldInvalidateExploreCacheForTool(result) {
			m.events <- exploreCacheInvalidatedMsg{
				reason: fmt.Sprintf("workspace changed via %s; clearing cached explore profile", result.Tool),
			}
		}
		step++
		if result.Error != "" {
			return directImplementationShortcut{}
		}
		if writeToolResultChanged(req, result) {
			changedPaths = append(changedPaths, req.Path)
			if strings.TrimSpace(op.Reason) != "" {
				appliedSummaries = append(appliedSummaries, fmt.Sprintf("%s (%s)", req.Path, strings.TrimSpace(op.Reason)))
			} else {
				appliedSummaries = append(appliedSummaries, req.Path)
			}
		}
	}
	if len(changedPaths) == 0 {
		m.logMessage("implement_focused_plan", "focused structured plan produced only no-op edits")
		return directImplementationShortcut{}
	}
	summary := strings.TrimSpace(plan.Summary)
	if summary == "" {
		summary = fmt.Sprintf("Applied a focused structured edit plan to %s lines %d-%d.", path, windowStart, windowEnd)
	}
	if len(appliedSummaries) > 0 {
		summary = strings.TrimSpace(summary + " Operations: " + strings.Join(appliedSummaries, "; ") + ".")
	}
	return directImplementationShortcut{
		applied:      true,
		changedPaths: uniqueNonEmptyStrings(changedPaths),
		summary:      summary,
		nextStep:     step,
	}
}

func (m *Model) proposeStructuredExecutionPlan(
	ctx context.Context,
	mgr *workspace.Manager,
	history []llama.ChatMessage,
	opts llama.ChatOptions,
	prompt string,
	packet implementationTargetPacket,
	feedback string,
) (structuredExecutionPlan, string, error) {
	planMessages := append([]llama.ChatMessage(nil), history...)
	workspaceInstructions := ""
	if mgr != nil {
		workspaceInstructions = mgr.Instructions()
	} else if m.workspace != nil {
		workspaceInstructions = m.workspace.Instructions()
	}
	planMessages = replaceSystemPrompt(planMessages, buildImplementSystemPrompt(m.promptStack.Text, workspaceInstructions))
	planMessages = append(planMessages, llama.ChatMessage{
		Role: "system",
		Content: "You are in STRUCTURED EXECUTION PLAN mode.\n" +
			"Do not request tools. Reply with ONLY one JSON object.\n" +
			"Schema:\n" +
			"{\"summary\":\"...\",\"operations\":[{\"tool\":\"edit_file\",\"path\":\"...\",\"start_line\":123,\"end_line\":123,\"content\":\"...\",\"reason\":\"...\"}]}\n" +
			"Rules:\n" + structuredExecutionPlanRules(packet) + "\n",
	})
	planMessages = append(planMessages, llama.ChatMessage{
		Role:    "user",
		Content: "Propose a bounded structured execution plan for this request.\n\nRequest:\n" + strings.TrimSpace(prompt) + "\n\nTarget packet:\n" + renderImplementationTargetPacket(packet),
	})
	if strings.TrimSpace(feedback) != "" {
		planMessages = append(planMessages, llama.ChatMessage{
			Role:    "system",
			Content: "Previous proposal feedback:\n" + strings.TrimSpace(feedback),
		})
	}
	planOpts := opts
	planOpts.Temperature = 0.2
	planOpts.TopP = 0.9
	planOpts.PreserveThinking = false
	thinking := false
	planOpts.EnableThinking = &thinking
	reply, err := m.client.CompleteChat(ctx, planMessages, planOpts)
	if err != nil {
		return structuredExecutionPlan{}, reply, err
	}
	plan, err := parseStructuredExecutionPlan(reply)
	return plan, reply, err
}

func (m *Model) proposeFocusedStructuredExecutionPlan(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	path string,
	windowStart int,
	windowEnd int,
	snippet string,
	feedback string,
) (structuredExecutionPlan, string, error) {
	workspaceInstructions := ""
	if mgr != nil {
		workspaceInstructions = mgr.Instructions()
	} else if m.workspace != nil {
		workspaceInstructions = m.workspace.Instructions()
	}
	messages := []llama.ChatMessage{{
		Role:    "system",
		Content: buildImplementSystemPrompt(m.promptStack.Text, workspaceInstructions),
	}, {
		Role: "system",
		Content: "You are in FOCUSED STRUCTURED EDIT mode.\n" +
			"Do not request tools. Reply with ONLY one JSON object.\n" +
			"Schema:\n" +
			"{\"summary\":\"...\",\"operations\":[{\"tool\":\"edit_file\",\"path\":\"" + path + "\",\"start_line\":" + strconv.Itoa(windowStart) + ",\"end_line\":" + strconv.Itoa(windowEnd) + ",\"content\":\"...\",\"reason\":\"...\"}]}\n" +
			"Rules:\n" + focusedStructuredExecutionPlanRules(packet, path, windowStart, windowEnd) + "\n",
	}, {
		Role: "user",
		Content: "Request:\n" + strings.TrimSpace(prompt) +
			"\n\nTarget packet:\n" + renderImplementationTargetPacket(packet) +
			fmt.Sprintf("\n\nFile window %s lines %d-%d:\n%s", path, windowStart, windowEnd, snippet),
	}}
	if strings.TrimSpace(feedback) != "" {
		messages = append(messages, llama.ChatMessage{
			Role:    "system",
			Content: "Previous proposal feedback:\n" + strings.TrimSpace(feedback),
		})
	}
	planOpts := opts
	planOpts.Temperature = 0.15
	planOpts.TopP = 0.85
	planOpts.PreserveThinking = false
	thinking := false
	planOpts.EnableThinking = &thinking
	reply, err := m.client.CompleteChat(ctx, messages, planOpts)
	if err != nil {
		return structuredExecutionPlan{}, reply, err
	}
	plan, err := parseStructuredExecutionPlan(reply)
	return plan, reply, err
}

func (m *Model) proposeFocusedWindowRewrite(
	ctx context.Context,
	mgr *workspace.Manager,
	prompt string,
	opts llama.ChatOptions,
	packet implementationTargetPacket,
	path string,
	startLine int,
	endLine int,
	snippet string,
	forceChange bool,
) (string, error) {
	workspaceInstructions := ""
	if mgr != nil {
		workspaceInstructions = mgr.Instructions()
	} else if m.workspace != nil {
		workspaceInstructions = m.workspace.Instructions()
	}
	messages := []llama.ChatMessage{{
		Role:    "system",
		Content: buildImplementSystemPrompt(m.promptStack.Text, workspaceInstructions),
	}, {
		Role: "system",
		Content: "You are in FOCUSED WINDOW REWRITE mode.\n" +
			"Do not request tools.\n" +
			"Reply with ONLY the replacement code for the specified line window.\n" +
			"Do not include markdown fences, commentary, or line numbers.\n" +
			"Keep the edit minimal and preserve surrounding behavior outside the requested change.\n" +
			"Preserve indentation and tab structure exactly.\n" +
			"Make sure the rewritten window remains syntactically valid when spliced back into the file.\n" +
			"Do not drop continued expressions, trailing operators, or closing delimiters that belong to the current block.\n",
	}, {
		Role: "user",
		Content: "Request:\n" + strings.TrimSpace(prompt) +
			"\n\nTarget packet:\n" + renderImplementationTargetPacket(packet) +
			fmt.Sprintf("\n\nRewrite only this file window from %s lines %d-%d:\n%s", path, startLine, endLine, snippet),
	}}
	if forceChange {
		messages = append(messages, llama.ChatMessage{
			Role: "system",
			Content: "Your previous attempt did not make a usable edit.\n" +
				"You must return a different replacement window than the current snippet.\n" +
				"Make the smallest possible valid change that satisfies the request.\n",
		})
	}
	rewriteOpts := opts
	rewriteOpts.Temperature = 0.2
	rewriteOpts.TopP = 0.9
	rewriteOpts.PreserveThinking = false
	thinking := false
	rewriteOpts.EnableThinking = &thinking
	reply, err := m.client.CompleteChat(ctx, messages, rewriteOpts)
	if err != nil {
		return "", err
	}
	return reply, nil
}

func parseStructuredExecutionPlan(reply string) (structuredExecutionPlan, error) {
	payload := strings.TrimSpace(reply)
	if strings.HasPrefix(payload, "```") {
		start := strings.Index(payload, "\n")
		end := strings.LastIndex(payload, "```")
		if start > 0 && end > start {
			payload = strings.TrimSpace(payload[start:end])
		}
	}
	if object, ok := extractFirstJSONObjectForPlan(payload); ok {
		payload = object
	}
	var plan structuredExecutionPlan
	if err := json.Unmarshal([]byte(payload), &plan); err != nil {
		return structuredExecutionPlan{}, err
	}
	if len(plan.Operations) > 3 {
		plan.Operations = nil
	}
	return plan, nil
}

func focusedRewriteWindow(packet implementationTargetPacket, bootstrap implementationBootstrap) (string, int, int, string, bool) {
	for _, result := range bootstrap.results {
		if result.Tool != "read_file" || result.Error != "" {
			continue
		}
		path, startLine, endLine, ok := parseReadWindowSummary(result.Summary)
		if !ok || !focusedRewritePathAllowed(packet, path) {
			continue
		}
		if endLine < startLine || endLine-startLine > 80 {
			continue
		}
		snippet := cleanReadWindowSnippet(result.Output)
		if strings.TrimSpace(snippet) == "" {
			continue
		}
		return path, startLine, endLine, snippet, true
	}
	return "", 0, 0, "", false
}

func focusedRewritePathAllowed(packet implementationTargetPacket, path string) bool {
	path = normalizeGuardedPath(path)
	if path == "" {
		return false
	}
	for _, candidate := range packet.PrimaryPaths {
		if normalizeGuardedPath(candidate) == path {
			return true
		}
	}
	return false
}

func cleanFocusedRewriteReply(reply string) string {
	reply = strings.TrimSpace(reply)
	if strings.HasPrefix(reply, "```") {
		start := strings.Index(reply, "\n")
		end := strings.LastIndex(reply, "```")
		if start > 0 && end > start {
			reply = strings.TrimSpace(reply[start:end])
		}
	}
	if strings.HasPrefix(reply, "tool") {
		return ""
	}
	return strings.TrimSpace(reply)
}

func preflightStructuredExecutionRequest(mgr *workspace.Manager, req workspace.ToolRequest) error {
	if mgr == nil || req.Tool != "edit_file" {
		return nil
	}
	path, err := preflightResolvePath(mgr, req.Path, req.Workdir)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := applyStructuredExecutionEdit(string(content), req)
	if err != nil {
		return err
	}
	if updated == string(content) {
		return nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, path, updated, parser.AllErrors); err != nil {
			return fmt.Errorf("preflight syntax validation failed for %s: %w", req.Path, err)
		}
	case ".json":
		if !json.Valid([]byte(updated)) {
			return fmt.Errorf("preflight JSON validation failed for %s", req.Path)
		}
	default:
		return nil
	}
	return nil
}

func preflightStructuredExecutionPlan(mgr *workspace.Manager, reqs []workspace.ToolRequest) error {
	if mgr == nil || len(reqs) == 0 {
		return nil
	}
	order := make([]string, 0, len(reqs))
	byPath := make(map[string][]workspace.ToolRequest)
	for _, req := range reqs {
		path, err := preflightResolvePath(mgr, req.Path, req.Workdir)
		if err != nil {
			return err
		}
		if _, ok := byPath[path]; !ok {
			order = append(order, path)
		}
		reqCopy := req
		reqCopy.Path = path
		byPath[path] = append(byPath[path], reqCopy)
	}
	for _, path := range order {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated := string(content)
		for _, req := range byPath[path] {
			next, err := applyStructuredExecutionEdit(updated, req)
			if err != nil {
				return err
			}
			updated = next
		}
		if updated == string(content) {
			continue
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go":
			fset := token.NewFileSet()
			if _, err := parser.ParseFile(fset, path, updated, parser.AllErrors); err != nil {
				return fmt.Errorf("preflight syntax validation failed for %s: %w", path, err)
			}
		case ".json":
			if !json.Valid([]byte(updated)) {
				return fmt.Errorf("preflight JSON validation failed for %s", path)
			}
		}
	}
	return nil
}

func validateStructuredExecutionEdit(packet implementationTargetPacket, req workspace.ToolRequest) *workspace.ToolResult {
	path := normalizeGuardedPath(req.Path)
	if path == "" {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: "structured execution plan requires a concrete path"}
	}
	allowed := false
	for _, candidate := range packet.PrimaryPaths {
		if normalizeGuardedPath(candidate) == path {
			allowed = true
			break
		}
	}
	if !allowed {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: fmt.Sprintf("structured execution plan may only edit target packet primary files, not %s", path)}
	}
	switch req.Tool {
	case "edit_file":
		if strings.TrimSpace(req.OldText) == "" && (req.StartLine <= 0 || req.EndLine < req.StartLine) {
			return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: "edit_file requires old_text or a valid start_line/end_line range"}
		}
	case "write_file", "append_file":
		if strings.TrimSpace(req.Content) == "" {
			return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: req.Tool + " requires content"}
		}
	default:
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Structured execution plan rejected", Error: fmt.Sprintf("unsupported structured execution tool %q", req.Tool)}
	}
	return nil
}

func validateStructuredExecutionPlan(packet implementationTargetPacket, plan structuredExecutionPlan) error {
	if len(plan.Operations) == 0 {
		return nil
	}
	if implementationTargetPacketNeedsStructuralPolicy(packet) {
		if len(plan.Operations) > 2 {
			return fmt.Errorf("structural refactor plans may propose at most 2 operations")
		}
		touchedPaths := make(map[string]struct{})
		for _, op := range plan.Operations {
			path := normalizeGuardedPath(op.Path)
			if path != "" {
				touchedPaths[path] = struct{}{}
			}
			if !structuredReasonLooksStructural(op.Reason, packet.StrategyHints) {
				return fmt.Errorf("structural refactor plans require an operation reason grounded in the boundary or responsibility change")
			}
		}
		if len(touchedPaths) > 1 && !structuralPlanAllowsMultiFileStage(packet, plan) {
			return fmt.Errorf("structural refactor first stage should edit one primary file before widening")
		}
	}
	return nil
}

func implementationTargetPacketNeedsStructuralPolicy(packet implementationTargetPacket) bool {
	if strings.TrimSpace(packet.SourceLane) == "architecture" {
		return true
	}
	return len(packet.StrategyHints) > 0
}

func structuralRefactorStageHint(packet implementationTargetPacket) string {
	if !implementationTargetPacketNeedsStructuralPolicy(packet) || len(packet.PrimaryPaths) == 0 {
		return ""
	}
	first := normalizeGuardedPath(packet.PrimaryPaths[0])
	if first == "" {
		return ""
	}
	if len(packet.PrimaryPaths) == 1 {
		return "stage 1 edits " + first + " only, then verifies before any follow-up widening"
	}
	rest := uniqueLimitedStrings(packet.PrimaryPaths[1:], 3)
	if len(rest) == 0 {
		return "stage 1 edits " + first + " only, then verifies before any follow-up widening"
	}
	return "stage 1 edits " + first + " first; verify before widening to " + strings.Join(rest, ", ")
}

func buildStructuralRefactorStage(packet implementationTargetPacket, changedPaths []string, verify implementationVerification) *structuralRefactorStage {
	if !implementationTargetPacketNeedsStructuralPolicy(packet) {
		return nil
	}
	changed := uniqueNormalizedPaths(changedPaths)
	if len(changed) == 0 {
		return nil
	}
	primary := uniqueNormalizedPaths(packet.PrimaryPaths)
	changedSet := make(map[string]struct{}, len(changed))
	for _, path := range changed {
		changedSet[path] = struct{}{}
	}
	remaining := make([]string, 0, len(primary))
	for _, path := range primary {
		if _, ok := changedSet[path]; ok {
			continue
		}
		remaining = append(remaining, path)
	}
	focus := changed[0]
	if len(primary) > 0 {
		if _, ok := changedSet[primary[0]]; ok {
			focus = primary[0]
		}
	}
	coverage := strings.TrimSpace(verify.coverage)
	confidence := strings.TrimSpace(verify.confidence)
	verification := ""
	switch {
	case verify.failed:
		verification = "failed"
	case confidence != "" && coverage != "":
		verification = confidence + " (" + coverage + ")"
	case confidence != "":
		verification = confidence
	case coverage != "":
		verification = coverage
	}
	next := structuralRefactorNextStep(focus, remaining, packet.StrategyHints, verify)
	stageNumber := packet.RefactorStage
	if stageNumber <= 0 {
		stageNumber = 1
	}
	return &structuralRefactorStage{
		Active:       true,
		Stage:        stageNumber,
		SourceLane:   strings.TrimSpace(packet.SourceLane),
		FocusPath:    focus,
		Changed:      changed,
		Remaining:    remaining,
		Strategy:     append([]string(nil), packet.StrategyHints...),
		NextStep:     next,
		Verification: verification,
	}
}

func validateFocusedStructuredExecutionEdit(path string, windowStart int, windowEnd int, snippet string, req workspace.ToolRequest) *workspace.ToolResult {
	path = normalizeGuardedPath(path)
	if path == "" {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit requires a concrete path"}
	}
	req.Path = normalizeGuardedPath(req.Path)
	if req.Path != path {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: fmt.Sprintf("focused structured edit may only target %s, not %s", path, req.Path)}
	}
	if req.Tool != "edit_file" {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: fmt.Sprintf("focused structured edit only supports edit_file, not %q", req.Tool)}
	}
	if strings.TrimSpace(req.OldText) != "" {
		if !strings.Contains(snippet, req.OldText) {
			return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit old_text must appear in the bootstrapped window"}
		}
		if req.OldText == req.NewText {
			return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit old_text/new_text pair is a no-op"}
		}
		return nil
	}
	if req.StartLine <= 0 || req.EndLine < req.StartLine {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit requires old_text/new_text or a valid start_line/end_line range"}
	}
	if req.StartLine < windowStart || req.EndLine > windowEnd {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: fmt.Sprintf("focused structured edit must stay within lines %d-%d", windowStart, windowEnd)}
	}
	if req.StartLine == windowStart && req.EndLine == windowEnd && req.Content == snippet {
		return &workspace.ToolResult{Tool: req.Tool, Summary: "Focused structured edit rejected", Error: "focused structured edit line-range content is identical to the current window"}
	}
	return nil
}

func marshalStructuredExecutionRequest(req workspace.ToolRequest) string {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Sprintf("{\"tool\":%q,\"path\":%q}", req.Tool, req.Path)
	}
	return string(payload)
}

func applyStructuredExecutionEdit(current string, req workspace.ToolRequest) (string, error) {
	if req.StartLine > 0 || req.EndLine > 0 {
		startLine := req.StartLine
		endLine := req.EndLine
		if startLine <= 0 {
			return "", fmt.Errorf("start_line must be greater than 0")
		}
		if endLine == 0 {
			endLine = startLine
		}
		if endLine < startLine {
			return "", fmt.Errorf("end_line must be greater than or equal to start_line")
		}
		spans := preflightLineSpans(current)
		if len(spans) == 0 {
			return "", fmt.Errorf("cannot apply a line edit to an empty file")
		}
		if startLine > len(spans) || endLine > len(spans) {
			return "", fmt.Errorf("requested lines %d-%d are outside the file (file has %d lines)", startLine, endLine, len(spans))
		}
		startOffset := spans[startLine-1].start
		endOffset := spans[endLine-1].end
		return current[:startOffset] + req.Content + current[endOffset:], nil
	}
	if strings.TrimSpace(req.OldText) == "" {
		return "", fmt.Errorf("old_text is required unless you provide start_line/end_line/content")
	}
	matchCount := strings.Count(current, req.OldText)
	if matchCount == 0 {
		return "", fmt.Errorf("old_text did not appear in %s", req.Path)
	}
	if matchCount > 1 && !req.ReplaceAll {
		return "", fmt.Errorf("old_text matched %d locations in %s; provide a more specific old_text or set replace_all=true", matchCount, req.Path)
	}
	if req.ReplaceAll {
		return strings.ReplaceAll(current, req.OldText, req.NewText), nil
	}
	return strings.Replace(current, req.OldText, req.NewText, 1), nil
}

func buildImplementSystemPrompt(basePrompt string, workspaceInstructions string) string {
	sections := make([]string, 0, 3)
	if prompt := strings.TrimSpace(basePrompt); prompt != "" {
		sections = append(sections, prompt)
	}
	if prompt := strings.TrimSpace(workspaceInstructions); prompt != "" {
		sections = append(sections, prompt)
	}
	sections = append(sections,
		"You are in IMPLEMENT mode.",
		"Make the smallest correct code change that satisfies the request.",
		"The harness may already provide an implementation target packet with exact files, lines, @file anchors, analogous references, or edit context. Use that first instead of restarting discovery.",
		"For complex multi-step implementation work, start with task_ledger and keep it current. Do not answer done while pending or in_progress ledger items remain.",
		"Prefer edit_file for localized changes and use start_line/end_line/content when quotes or indentation make exact text replacement brittle.",
		"For large Go-file refactors, inspect declaration boundaries with go_file_outline and move model-chosen declaration groups with go_move_declarations, preferably using pattern:\"tag:<responsibility>\" from the outline; if splitting one file into several cohesive files, prefer a single batch go_move_declarations call with moves instead of sequential partial moves.",
		"Do not keep relisting directories once a target file or line is already known. Consume the target packet before any broader search.",
		"Once the edit is complete, answer normally. The harness will run verification automatically after your changes.",
	)
	return strings.Join(sections, "\n\n")
}

func buildImplementTaskPrompt(prompt string) string {
	return "Implement this request with the available workspace tools.\n\n" +
		renderLaneContractPolicy(analysisLaneImplement) + "\n\n" +
		"Implementation rules:\n" +
		"- Make the smallest correct change.\n" +
		"- For complex multi-step work, first call task_ledger with todos. Keep exactly one item in_progress, mark completed work, and do not finish while required items remain pending or in_progress.\n" +
		"- Prefer edit_file over rewriting the whole file for localized edits.\n" +
		"- For large Go refactors, use go_file_outline first, choose cohesive responsibility groups yourself, then use go_move_declarations with pattern:\"tag:<responsibility>\" or explicit declarations to move them mechanically into destination files.\n" +
		"- When a large Go refactor splits one file into several cohesive files, prefer one batch go_move_declarations call with moves over partial sequential moves.\n" +
		"- If the request names a file, uses @file anchors, or the harness already supplied target lines, do not restart discovery from the repo root.\n" +
		"- Treat analogous reference files as inspiration, not as the default write target.\n" +
		"- For structural refactors, stay within the target packet files unless verification requires a follow-up; use batch structural tools when the requested boundary change is explicitly multi-file.\n" +
		"- Once the required change is applied, answer briefly with what you changed.\n\n" +
		"Request:\n" + strings.TrimSpace(prompt)
}

func structuredExecutionPlanRules(packet implementationTargetPacket) string {
	lines := []string{
		"- Prefer edit_file.",
		"- Use only paths from the target packet primary files.",
		"- At most 3 operations.",
		"- Use anchored lines, symbol windows, and bootstrapped file excerpts before widening.",
		"- If you cannot propose a safe bounded plan, return {\"summary\":\"\",\"operations\":[]}.",
	}
	if implementationTargetPacketNeedsStructuralPolicy(packet) {
		lines[2] = "- At most 2 operations."
		lines = append(lines,
			"- This is a structural refactor request. Each operation reason must explain the boundary or responsibility change.",
			"- Treat this as the first stage of a structural refactor. Prefer editing only the first primary file, then verify before widening.",
			"- Multi-file operations are allowed only when the structural hints explicitly require a cross-file boundary move or interface extraction.",
			"- Prefer one bounded file split/extraction step over repo-wide rewrites.",
			"- Stay within the files and structural hints in the target packet unless the request explicitly demands a broader refactor.",
		)
	}
	return strings.Join(lines, "\n")
}

func focusedStructuredExecutionPlanRules(packet implementationTargetPacket, path string, windowStart int, windowEnd int) string {
	lines := []string{
		"- Use only edit_file operations.",
		"- Use only path " + path + ".",
		"- All line edits must stay within lines " + strconv.Itoa(windowStart) + "-" + strconv.Itoa(windowEnd) + ".",
		"- Prefer the narrowest possible line-range edit.",
		"- Use old_text/new_text only if that is safer than a line-range edit.",
		"- At most 2 operations.",
		"- If you cannot propose a safe bounded edit, return {\"summary\":\"\",\"operations\":[]}.",
	}
	if implementationTargetPacketNeedsStructuralPolicy(packet) {
		lines = append(lines,
			"- This focused edit is part of a structural refactor. Each operation reason must name the responsibility or boundary being tightened.",
			"- Treat this as the first stage of the structural refactor, not the whole refactor.",
			"- Preserve the rest of the file shape outside this focused boundary change.",
		)
	}
	return strings.Join(lines, "\n")
}

func buildImplementRepairPrompt(prompt string) string {
	return "Repair the already-applied change so verification passes while preserving the user's requested behavior change.\n\n" +
		"Repair rules:\n" +
		"- Do not undo the requested behavior change unless verification proves it is impossible.\n" +
		"- Use the supplied verification errors and recent edit context first.\n" +
		"- Prefer the smallest follow-up edit that restores a clean build/test result.\n\n" +
		"Original request:\n" + strings.TrimSpace(prompt)
}

func implementToolBudget(maxToolSteps int) int {
	switch {
	case maxToolSteps <= 0:
		return 14
	case maxToolSteps < 6:
		return maxToolSteps
	default:
		return minInt(maxToolSteps, 14)
	}
}

func implementToolBudgetForRequest(maxToolSteps int, prompt string, packet implementationTargetPacket) int {
	budget := implementToolBudget(maxToolSteps)
	if !requestNeedsExpandedImplementBudget(prompt, packet) {
		return budget
	}
	if maxToolSteps > budget {
		budget = maxToolSteps
	}
	budget = maxInt(budget, 24)
	if promptRequestsWholeFileRefactor(prompt) {
		budget = maxInt(budget, 32)
	}
	if count := explicitGoMoveBatchHintCount(prompt); count > 0 {
		budget = maxInt(budget, 8+count*3)
	}
	if len(packet.PrimaryPaths) > 1 {
		budget = maxInt(budget, 16+len(packet.PrimaryPaths)*2)
	}
	return minInt(budget, 48)
}

func implementRunPolicyForRequest(maxToolSteps int, prompt string, packet implementationTargetPacket) agent.RunPolicy {
	if requestNeedsExpandedImplementBudget(prompt, packet) {
		return agent.RunPolicy{
			MaxToolSteps:       0,
			MaxNoProgressTurns: 8,
			MaxContextMessages: 28,
			MaxContextChars:    60000,
			MaxModelCalls:      64,
		}
	}
	return agent.RunPolicy{
		MaxToolSteps:       implementToolBudget(maxToolSteps),
		MaxNoProgressTurns: 6,
		MaxContextMessages: 24,
		MaxContextChars:    40000,
		MaxModelCalls:      maxInt(implementToolBudget(maxToolSteps)+6, 12),
	}
}

func implementationCompletionGate(prompt string, preChangedPaths []string, requests map[int]agent.ToolRequestEvent, results map[int]agent.ToolResultEvent) agent.CompletionGate {
	return func(input agent.CompletionGateInput) agent.CompletionGateResult {
		changedPaths := uniqueNonEmptyStrings(append(append([]string(nil), preChangedPaths...), changedPathsFromImplementEvents(requests, results)...))
		if len(changedPaths) > 0 {
			return agent.CompletionGateResult{Accept: true}
		}
		reply := strings.ToLower(strings.TrimSpace(input.Reply))
		if implementationFinalReplyHasTerminalReason(reply) {
			return agent.CompletionGateResult{Accept: true}
		}
		if promptRequestsWholeFileRefactor(prompt) && successfulToolResult(input.Results, "go_file_outline") && !successfulToolResult(input.Results, "go_move_declarations") {
			return agent.CompletionGateResult{
				Summary: "Implementation completion gate rejected final answer",
				Error:   "the request is a whole-file refactor; go_file_outline succeeded, but no go_move_declarations call has succeeded yet. Continue with a move tool call or report the concrete failed move blocker.",
			}
		}
		if promptRequestsImplementation(prompt) {
			return agent.CompletionGateResult{
				Summary: "Implementation completion gate rejected final answer",
				Error:   "no workspace change has succeeded yet. Continue with the next edit/tool call, or answer with an explicit no-op reason, already-satisfied finding, permission blocker, or verification blocker.",
			}
		}
		return agent.CompletionGateResult{Accept: true}
	}
}

func implementationFinalReplyHasTerminalReason(reply string) bool {
	return containsAny(reply,
		"no changes needed",
		"no change needed",
		"no file edits were applied",
		"already satisfied",
		"already implemented",
		"already complete",
		"blocked",
		"permission",
		"approval",
		"unable to",
		"cannot proceed",
		"could not proceed",
	)
}

func successfulToolResult(results []workspace.ToolResult, tool string) bool {
	for _, result := range results {
		if result.Tool == tool && strings.TrimSpace(result.Error) == "" {
			return true
		}
	}
	return false
}

func requestNeedsExpandedImplementBudget(prompt string, packet implementationTargetPacket) bool {
	normalized := strings.ToLower(prompt)
	if promptRequestsWholeFileRefactor(prompt) || implementationTargetPacketNeedsStructuralPolicy(packet) {
		return true
	}
	return containsAny(normalized,
		"go_move_declarations",
		"go_file_outline",
		"large go refactor",
		"giant file",
		"monolithic file",
		"split the file",
		"split into",
		"multi-file refactor",
		"across multiple files",
	)
}

func explicitGoMoveBatchHintCount(prompt string) int {
	normalized := strings.ToLower(prompt)
	tags := []string{
		"tag:model_state",
		"tag:lifecycle",
		"tag:prompt_routing",
		"tag:rendering",
		"tag:task_memory",
		"tag:implementation",
		"tag:write_guard",
		"tag:analysis_agents",
		"tag:control_rails",
	}
	count := 0
	for _, tag := range tags {
		if strings.Contains(normalized, tag) {
			count++
		}
	}
	if count > 0 {
		return count
	}
	return strings.Count(normalized, "destination:")
}

type verificationFeedbackSnapshot struct {
	RiskLevel             string   `json:"risk_level,omitempty"`
	Coverage              string   `json:"coverage,omitempty"`
	Confidence            string   `json:"confidence,omitempty"`
	Commands              []string `json:"commands,omitempty"`
	FailedCommands        []string `json:"failed_commands,omitempty"`
	LearnedRecommendation string   `json:"learned_recommendation,omitempty"`
}

func (m *Model) logVerificationFeedback(feedback verificationFeedbackSnapshot) {
	if m == nil || m.debugLogger == nil {
		return
	}
	data, err := json.Marshal(feedback)
	if err != nil {
		return
	}
	m.logMessage("verification_feedback", string(data))
}

func formatStructuredExecutionPlanSteps(plan structuredExecutionPlan) []string {
	steps := make([]string, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		path := normalizeGuardedPath(op.Path)
		if path == "" {
			path = "target file"
		}
		action := strings.TrimSpace(op.Tool)
		if action == "" {
			action = "edit_file"
		}
		line := action + " " + path
		if op.StartLine > 0 && op.EndLine >= op.StartLine {
			line += fmt.Sprintf(" lines %d-%d", op.StartLine, op.EndLine)
		}
		if reason := strings.TrimSpace(op.Reason); reason != "" {
			line += " (" + reason + ")"
		}
		steps = append(steps, line)
	}
	return uniqueNonEmptyStrings(steps)
}

func (m *Model) setExecutionVerificationPlan(plan verificationPlan) {
	m.executionPanel.Verification = verificationPlan{
		Commands:         append([]string(nil), plan.Commands...),
		Reasons:          append([]string(nil), plan.Reasons...),
		UsesFullSuite:    plan.UsesFullSuite,
		Coverage:         strings.TrimSpace(plan.Coverage),
		Confidence:       strings.TrimSpace(plan.Confidence),
		RiskLevel:        strings.TrimSpace(plan.RiskLevel),
		ExpectedFailures: append([]string(nil), plan.ExpectedFailures...),
		RepairPolicy:     strings.TrimSpace(plan.RepairPolicy),
	}
	m.executionPanel.UpdatedAt = time.Now()
	m.logVerificationPlan(plan)
}

func (m *Model) logVerificationPlan(plan verificationPlan) {
	if m.debugLogger == nil {
		return
	}
	payload := struct {
		Commands         []string `json:"commands,omitempty"`
		Reasons          []string `json:"reasons,omitempty"`
		UsesFullSuite    bool     `json:"uses_full_suite,omitempty"`
		Coverage         string   `json:"coverage,omitempty"`
		Confidence       string   `json:"confidence,omitempty"`
		RiskLevel        string   `json:"risk_level,omitempty"`
		ExpectedFailures []string `json:"expected_failures,omitempty"`
		RepairPolicy     string   `json:"repair_policy,omitempty"`
	}{
		Commands:         append([]string(nil), plan.Commands...),
		Reasons:          append([]string(nil), plan.Reasons...),
		UsesFullSuite:    plan.UsesFullSuite,
		Coverage:         strings.TrimSpace(plan.Coverage),
		Confidence:       strings.TrimSpace(plan.Confidence),
		RiskLevel:        strings.TrimSpace(plan.RiskLevel),
		ExpectedFailures: append([]string(nil), plan.ExpectedFailures...),
		RepairPolicy:     strings.TrimSpace(plan.RepairPolicy),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	m.logMessage("verification_plan", string(data))
}

func promptRequestsAnalysisBeforeImplementation(normalized string) bool {
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return false
	}
	if containsAny(normalized, "identify it first", "show it first", "before changing", "before you change", "don't change it yet", "do not change it yet") {
		return true
	}
	for _, prefix := range []string{"explore ", "investigate ", "analyze ", "identify ", "review ", "audit ", "look through ", "inspect "} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func promptRequestsImplementation(prompt string) bool {
	normalized := strings.ToLower(prompt)
	trimmed := strings.TrimSpace(normalized)
	if looksLikePlanOnlyPrompt(trimmed) {
		return false
	}
	if strings.HasPrefix(trimmed, "fix ") || strings.HasPrefix(trimmed, "patch ") || strings.HasPrefix(trimmed, "implement ") || strings.HasPrefix(trimmed, "refactor ") {
		return true
	}
	if primaryLocateLiteral(prompt) != "" && containsAny(normalized, "remove ", "delete ", "take it out") {
		return true
	}
	return containsAny(normalized,
		"refactor ",
		"split existing code",
		"produce the refactor",
		"in one shot",
		"fix it now",
		"fix this now",
		"fix the issue",
		"fix the failure",
		"fix the bug",
		"please fix",
		"go fix",
		"patch it",
		"patch this",
		"apply the fix",
		"apply that fix",
		"apply this fix",
		"apply changes now",
		"write the fix",
		"write the patch",
		"edit the file",
		"edit those files",
		"update the code",
		"change the code",
		"implement the fix",
		"make the fix",
		"make the change",
		"create the file",
		"delete the file",
		"remove it",
		"remove this",
		"remove the code",
		"delete it",
		"refactor it",
		"refactor this",
		"then fix",
		"then fix it",
		"then update",
		"then implement",
		"then implement it",
		"and fix",
		"and fix it",
		"and update",
		"and implement",
		"and implement it",
	)
}

func looksLikeDirectImplementationPrompt(prompt string, normalized string) bool {
	if looksLikePlanOnlyPrompt(normalized) {
		return false
	}
	if promptRequestsImplementation(prompt) {
		return true
	}
	if requestsShellOnlyMutation(normalized) && (len(promptMentionedWorkspacePaths(prompt)) > 0 || containsAny(normalized, "file ", "directory ", "folder ")) {
		return true
	}
	if containsAny(normalized, "use edit_file", "targeted edit", "code edit", "change the footer help text", "replace ", "rename ") {
		return len(promptMentionedWorkspacePaths(prompt)) > 0
	}
	hasEditVerb := containsAny(
		normalized,
		"create",
		"edit",
		"change",
		"replace",
		"update",
		"modify",
		"rename",
		"remove",
		"delete",
		"add",
		"refactor",
		"split",
		"reorganize",
		"write",
		"append",
		"mkdir",
	)
	hasConcreteTarget := len(promptMentionedWorkspacePaths(prompt)) > 0 ||
		containsAny(normalized, "line ", "function ", "method ", "text from", "text to") ||
		hasUILocationHint(normalized)
	return hasEditVerb && hasConcreteTarget && !looksLikeInvestigationPrompt(normalized)
}

func looksLikePlanOnlyPrompt(normalized string) bool {
	normalized = strings.ToLower(strings.TrimSpace(normalized))
	if normalized == "" {
		return false
	}
	hasPlanRequest := containsAny(
		normalized,
		"prepare a plan",
		"prepare an plan",
		"prepare a refactor plan",
		"prepare a refactoring plan",
		"make a plan",
		"create a plan",
		"propose a plan",
		"propose a work plan",
		"write a plan",
		"draft a plan",
		"work plan",
		"work out an approach",
		"work out approach",
		"outline an approach",
		"approach to refactoring",
		"approach to refactor",
		"implementation plan",
		"refactor plan",
		"refactoring plan",
		"plan to update",
		"plan to refactor",
		"plan for updating",
		"plan for refactoring",
	)
	if !hasPlanRequest {
		return false
	}
	return !containsAny(
		normalized,
		"then implement",
		"and implement",
		"then update",
		"and update",
		"then apply",
		"and apply",
		"apply the plan",
		"execute the plan",
		"do it now",
		"make the changes",
	)
}

func looksLikeReadOnlyBoundaryPrompt(normalized string) bool {
	normalized = strings.ToLower(strings.TrimSpace(normalized))
	if normalized == "" {
		return false
	}
	return containsAny(
		normalized,
		"don't edit",
		"do not edit",
		"don't change",
		"do not change",
		"don't modify",
		"do not modify",
		"read only",
		"read-only",
		"plan only",
		"no edits",
		"without editing",
		"without changing",
		"before editing",
		"before changing",
	)
}
