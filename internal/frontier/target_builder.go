package frontier

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"qwen-bubbles/internal/llama"
	"qwen-bubbles/internal/semantic"
	"qwen-bubbles/internal/workspace"
)

func BuildTargetPacket(prompt string, history []llama.ChatMessage) TargetPacket {
	packet := TargetPacket{
		PrimaryPaths:     make([]string, 0, 4),
		Anchors:          make([]Target, 0, 4),
		AnalogyPaths:     make([]string, 0, 2),
		LiteralFragments: LimitStrings(QuotedPromptFragments(prompt), 3),
		LocationHints:    TargetLocationHints(prompt),
		SymbolHints:      TargetSymbolHints(prompt),
		StrategyHints:    make([]string, 0, 3),
	}
	initialSymbolHints := append([]string(nil), packet.SymbolHints...)
	packet.SymbolHints = packet.SymbolHints[:0]
	primarySeen := make(map[string]struct{})
	analogySeen := make(map[string]struct{})
	anchorSeen := make(map[string]struct{})
	symbolSeen := make(map[string]struct{})
	addPath := func(dst *[]string, seen map[string]struct{}, path string) {
		path = NormalizePath(path)
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
	for _, path := range PromptPrimaryPaths(prompt) {
		addPath(&packet.PrimaryPaths, primarySeen, path)
	}
	for _, path := range TargetAnalogyPaths(prompt) {
		addPath(&packet.AnalogyPaths, analogySeen, path)
	}
	if PromptForbidsDeclarationMoves(prompt) {
		packet.StrategyHints = append(packet.StrategyHints, "declaration moves forbidden; make any refactor with edit_file inside the approved primary file")
	}
	for _, target := range AnalysisSummaryTargets(history) {
		key := fmt.Sprintf("%s:%d", NormalizePath(target.Path), target.Line)
		if _, ok := anchorSeen[key]; ok {
			continue
		}
		anchorSeen[key] = struct{}{}
		packet.Anchors = append(packet.Anchors, target)
		addPath(&packet.PrimaryPaths, primarySeen, target.Path)
	}
	summaryLane, summaryBody, ok := LatestAnalysisSummary(history)
	if ok {
		packet.SourceLane = strings.TrimSpace(summaryLane)
		packet.RefactorStage = AnalysisSummaryRefactorStage(summaryBody)
		for _, path := range PromptMentionedWorkspacePaths(summaryBody) {
			addPath(&packet.PrimaryPaths, primarySeen, path)
		}
		for _, hint := range TargetSymbolHints(summaryBody) {
			addSymbolHint(hint)
		}
		if summaryLane == "architecture" {
			packet.StrategyHints = append(packet.StrategyHints, AnalysisSummaryStrategyHints(summaryBody)...)
		}
	}
	packet.PrimaryPaths = LimitStrings(packet.PrimaryPaths, 6)
	packet.AnalogyPaths = LimitStrings(packet.AnalogyPaths, 4)
	packet.LocationHints = LimitStrings(packet.LocationHints, 4)
	packet.SymbolHints = LimitStrings(packet.SymbolHints, 6)
	packet.StrategyHints = LimitStrings(packet.StrategyHints, 4)
	return packet
}

func PromptPrimaryPaths(prompt string) []string {
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
			paths := PromptMentionedWorkspacePaths(strings.TrimSpace(trimmed[len(prefix):]))
			explicit = append(explicit, paths...)
		}
	}
	if len(explicit) > 0 {
		return LimitStrings(explicit, 4)
	}
	return PromptMentionedWorkspacePaths(prompt)
}

func TargetAnalogyPaths(prompt string) []string {
	normalized := strings.ToLower(prompt)
	paths := PromptMentionedWorkspacePaths(prompt)
	analogies := make([]string, 0, 2)
	for _, path := range paths {
		lowerPath := strings.ToLower(path)
		base := strings.ToLower(filepath.Base(path))
		if ContainsAny(
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
	return LimitStrings(analogies, 4)
}

func TargetLocationHints(prompt string) []string {
	normalized := strings.ToLower(prompt)
	hints := make([]string, 0, 4)
	for _, hint := range []string{"top", "header", "footer", "badge", "window", "chat", "menu", "status", "banner"} {
		if strings.Contains(normalized, hint) {
			hints = append(hints, hint)
		}
	}
	return hints
}

func TargetSymbolHints(prompt string) []string {
	tokenPattern := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_./]*`)
	stopwords := map[string]struct{}{
		"add": {}, "and": {}, "apis": {}, "app": {}, "approved": {}, "banner": {}, "bit": {}, "build": {}, "can": {}, "change": {},
		"code": {}, "edit": {}, "file": {}, "fix": {}, "for": {}, "from": {}, "header": {}, "implement": {},
		"do": {}, "go": {}, "in": {}, "it": {}, "keep": {}, "like": {}, "make": {}, "match": {}, "padding": {}, "please": {}, "remove": {},
		"render": {}, "same": {}, "small": {}, "status": {}, "text": {}, "the": {}, "this": {}, "to": {},
		"execute": {}, "run": {}, "top": {}, "underneath": {}, "update": {}, "use": {}, "where": {}, "window": {}, "with": {}, "work": {},
	}
	seen := make(map[string]struct{})
	hints := make([]string, 0, 4)
	add := func(token string) {
		token = strings.Trim(token, "`'\"()[]{}<>,:;!?")
		token = strings.TrimPrefix(token, "@")
		token = strings.TrimRight(token, ".")
		if token == "" || LooksLikeWorkspacePathToken(token) || strings.Contains(token, "/") {
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
	for _, fragment := range QuotedPromptFragments(prompt) {
		for _, token := range tokenPattern.FindAllString(fragment, -1) {
			add(token)
		}
	}
	for _, token := range tokenPattern.FindAllString(prompt, -1) {
		add(token)
	}
	return LimitStrings(hints, 6)
}

func PromptForbidsDeclarationMoves(prompt string) bool {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	return ContainsAny(normalized,
		"do not move declarations",
		"do not move declaration",
		"don't move declarations",
		"no declaration moves",
		"without moving declarations",
		"do not create files or move declarations",
	)
}

func AnalysisSummaryTargets(history []llama.ChatMessage) []Target {
	if len(history) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	targets := make([]Target, 0, 4)
	add := func(path string, line int) {
		path = NormalizePath(path)
		if path == "" || line <= 0 {
			return
		}
		key := fmt.Sprintf("%s:%d", path, line)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		targets = append(targets, Target{Path: path, Line: line})
	}
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "system") || !strings.Contains(msg.Content, "subagent summary") {
			continue
		}
		for _, field := range strings.Fields(msg.Content) {
			if path, line, ok := ParseAnalysisSummaryTargetToken(field); ok {
				add(path, line)
			}
		}
		if len(targets) > 0 {
			break
		}
	}
	return targets
}

func ParseAnalysisSummaryTargetToken(field string) (string, int, bool) {
	candidate := strings.Trim(field, "`'\"()[]{}<>,:;!?.")
	idx := strings.LastIndex(candidate, ":")
	if idx <= 0 || idx >= len(candidate)-1 {
		return "", 0, false
	}
	path := candidate[:idx]
	lineText := candidate[idx+1:]
	line, err := strconv.Atoi(lineText)
	if err != nil || !LooksLikeWorkspacePathToken(path) {
		return "", 0, false
	}
	return path, line, true
}

func LatestAnalysisSummary(history []llama.ChatMessage) (string, string, bool) {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "system") || !strings.Contains(msg.Content, "subagent summary]") {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		firstLine := content
		body := ""
		if idx := strings.IndexByte(content, '\n'); idx >= 0 {
			firstLine = strings.TrimSpace(content[:idx])
			body = strings.TrimSpace(content[idx+1:])
		}
		lane := ""
		if strings.HasPrefix(firstLine, "[") && strings.HasSuffix(firstLine, "]") {
			label := strings.TrimSuffix(strings.TrimPrefix(firstLine, "["), "]")
			label = strings.TrimSpace(strings.TrimSuffix(label, "subagent summary"))
			lane = strings.TrimSpace(label)
		}
		if body == "" {
			body = content
		}
		if idx := strings.Index(body, "\n\n"); idx >= 0 {
			body = strings.TrimSpace(body[idx+2:])
		}
		return lane, body, true
	}
	return "", "", false
}

func AnalysisSummaryRefactorStage(summary string) int {
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

func AnalysisSummaryStrategyHints(summary string) []string {
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	lines := strings.Split(summary, "\n")
	hints := make([]string, 0, 4)
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
		lower := strings.ToLower(trimmed)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 180 {
			trimmed = strings.TrimSpace(trimmed[:180])
			if trimmed != "" {
				trimmed += "..."
			}
		}
		if strings.Contains(lower, "direction:") ||
			strings.Contains(lower, "suggested") ||
			strings.Contains(lower, "should ") ||
			strings.Contains(lower, "split ") ||
			strings.Contains(lower, "extract ") ||
			strings.Contains(lower, "separate ") ||
			strings.Contains(lower, "move ") {
			hints = append(hints, trimmed)
		}
	}
	return LimitStrings(hints, 4)
}

func EnrichTargetPacket(mgr *workspace.Manager, packet TargetPacket) TargetPacket {
	if mgr == nil || !mgr.Enabled() {
		return packet
	}
	packet.PrimaryPaths = ResolvePrimaryPaths(mgr.Root(), packet.PrimaryPaths)
	packet.EvidencePaths = ResolvePrimaryPaths(mgr.Root(), packet.EvidencePaths)
	packet.ForbiddenPaths = ResolvePrimaryPaths(mgr.Root(), packet.ForbiddenPaths)
	packet.AnalogyPaths = ResolvePrimaryPaths(mgr.Root(), packet.AnalogyPaths)
	if len(packet.SymbolHints) == 0 && len(packet.Anchors) == 0 {
		return packet
	}
	idx, err := semantic.BuildGoIndex(mgr.Root())
	if err != nil || idx == nil {
		return packet
	}
	packet.Symbols = ResolveSymbolAnchors(idx, packet)
	if len(packet.PrimaryPaths) == 0 {
		for _, symbol := range packet.Symbols {
			if symbol.Path != "" {
				packet.PrimaryPaths = LimitStrings(append(packet.PrimaryPaths, NormalizePath(symbol.Path)), 6)
			}
		}
	}
	return packet
}

func ResolvePrimaryPaths(root string, paths []string) []string {
	root = strings.TrimSpace(root)
	if root == "" || len(paths) == 0 {
		return paths
	}
	resolved := make([]string, 0, len(paths))
	for _, path := range paths {
		path = NormalizePath(path)
		if path == "" {
			continue
		}
		if WorkspaceRelativeFileExists(root, path) {
			resolved = append(resolved, path)
			continue
		}
		if strings.Contains(path, "/") {
			resolved = append(resolved, path)
			continue
		}
		matches := FindWorkspaceFilesByBase(root, path, 2)
		if len(matches) == 1 {
			resolved = append(resolved, matches[0])
			continue
		}
		resolved = append(resolved, path)
	}
	return LimitStrings(resolved, 6)
}

func WorkspaceRelativeFileExists(root string, path string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	return err == nil && !info.IsDir()
}

func FindWorkspaceFilesByBase(root string, base string, limit int) []string {
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

func ResolveSymbolAnchors(idx *semantic.GoIndex, packet TargetPacket) []semantic.SymbolMatch {
	if idx == nil {
		return nil
	}
	preferredPaths := make(map[string]int)
	primaryPaths := make(map[string]struct{})
	for _, path := range packet.PrimaryPaths {
		path = NormalizePath(path)
		if path == "" {
			continue
		}
		preferredPaths[path] = 2
		primaryPaths[path] = struct{}{}
	}
	for _, path := range packet.AnalogyPaths {
		path = NormalizePath(path)
		if path == "" {
			continue
		}
		if preferredPaths[path] < 1 {
			preferredPaths[path] = 1
		}
	}
	anchorLines := make(map[string][]int)
	for _, anchor := range packet.Anchors {
		path := NormalizePath(anchor.Path)
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
		match.Path = NormalizePath(match.Path)
		if match.Path == "" {
			return
		}
		if len(primaryPaths) > 0 {
			if _, ok := primaryPaths[match.Path]; !ok {
				return
			}
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
		key := fmt.Sprintf("%s:%s:%d", match.Path, QualifiedSymbol(match), match.Line)
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
			if symbol, ok := EnclosingSymbolAtLine(file, line); ok {
				add(SymbolMatchFromGoSymbol(file, symbol, 80))
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
		return strings.Compare(QualifiedSymbol(a), QualifiedSymbol(b))
	})
	if len(matches) > 4 {
		matches = matches[:4]
	}
	return matches
}

func SymbolMatchFromGoSymbol(file *semantic.GoFile, symbol semantic.GoSymbol, score int) semantic.SymbolMatch {
	pkg := ""
	path := symbol.SourceFile
	if file != nil {
		pkg = file.Package
		if path == "" {
			path = file.Path
		}
	}
	return semantic.SymbolMatch{
		Path:      path,
		Package:   pkg,
		Name:      symbol.Name,
		Kind:      symbol.Kind,
		Receiver:  symbol.Receiver,
		Line:      symbol.Line,
		Column:    symbol.Column,
		StartLine: symbol.StartLine,
		EndLine:   symbol.EndLine,
		Score:     score,
	}
}

func EnclosingSymbolAtLine(file *semantic.GoFile, line int) (semantic.GoSymbol, bool) {
	if file == nil || line <= 0 {
		return semantic.GoSymbol{}, false
	}
	var best semantic.GoSymbol
	bestSpan := 1 << 30
	found := false
	for _, symbol := range file.Symbols {
		if symbol.StartLine <= 0 || symbol.EndLine <= 0 {
			continue
		}
		if line < symbol.StartLine || line > symbol.EndLine {
			continue
		}
		span := symbol.EndLine - symbol.StartLine
		if !found || span < bestSpan {
			best = symbol
			bestSpan = span
			found = true
		}
	}
	return best, found
}
