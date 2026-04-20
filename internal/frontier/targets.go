package frontier

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"qwen-bubbles/internal/semantic"
	"qwen-bubbles/internal/workspace"
)

type Target struct {
	Path string
	Line int
}

type TargetPacket struct {
	SourceLane          string
	RefactorStage       int
	PrimaryPaths        []string
	EvidencePaths       []string
	AllowedDestinations []string
	ForbiddenPaths      []string
	Anchors             []Target
	AnalogyPaths        []string
	LiteralFragments    []string
	LocationHints       []string
	SymbolHints         []string
	StrategyHints       []string
	Symbols             []semantic.SymbolMatch
}

type TargetResumeContext struct {
	Active                bool
	SourceLane            Lane
	ApprovedEditTargets   []string
	ReadOnlyEvidencePaths []string
	AllowedDestinations   []string
	ForbiddenPaths        []string
	VerificationPlan      []string
}

func ApplyResumeContextToTargetPacket(packet TargetPacket, resume TargetResumeContext) TargetPacket {
	if !resume.Active {
		return packet
	}
	if len(resume.ApprovedEditTargets) > 0 {
		packet.PrimaryPaths = LimitStrings(resume.ApprovedEditTargets, 8)
	}
	packet.EvidencePaths = LimitStrings(append(packet.EvidencePaths, resume.ReadOnlyEvidencePaths...), 16)
	packet.AllowedDestinations = LimitStrings(append(packet.AllowedDestinations, resume.AllowedDestinations...), 8)
	packet.ForbiddenPaths = LimitStrings(append(packet.ForbiddenPaths, resume.ForbiddenPaths...), 16)
	if len(resume.VerificationPlan) > 0 {
		packet.StrategyHints = LimitStrings(append(packet.StrategyHints, resume.VerificationPlan...), 8)
	}
	if resume.SourceLane != "" && strings.TrimSpace(packet.SourceLane) == "" {
		packet.SourceLane = string(resume.SourceLane)
	}
	return packet
}

func RenderTargetPacket(packet TargetPacket) string {
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
		lines = append(lines, "- anchored lines: "+strings.Join(LimitStrings(anchors, 4), ", "))
	}
	if len(packet.AnalogyPaths) > 0 {
		lines = append(lines, "- analogous references: "+strings.Join(packet.AnalogyPaths, ", "))
	}
	if len(packet.Symbols) > 0 {
		symbols := make([]string, 0, len(packet.Symbols))
		for _, match := range packet.Symbols {
			symbols = append(symbols, fmt.Sprintf("%s (%s:%d-%d)", QualifiedSymbol(match), match.Path, match.StartLine, match.EndLine))
		}
		lines = append(lines, "- symbol anchors: "+strings.Join(LimitStrings(symbols, 4), ", "))
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
	if stage := StructuralStageHint(packet); stage != "" {
		lines = append(lines, "- structural stage: "+stage)
	}
	lines = append(lines, "- do not call list_files/search_files until you have consumed these anchors or proved they are insufficient")
	return strings.Join(lines, "\n")
}

func QualifiedSymbol(match semantic.SymbolMatch) string {
	if strings.TrimSpace(match.Receiver) != "" {
		return match.Receiver + "." + match.Name
	}
	return match.Name
}

func TargetPacketNeedsStructuralPolicy(packet TargetPacket) bool {
	if strings.TrimSpace(packet.SourceLane) == string(LaneArchitecture) {
		return true
	}
	return len(packet.StrategyHints) > 0
}

func StructuralStageHint(packet TargetPacket) string {
	if !TargetPacketNeedsStructuralPolicy(packet) || len(packet.PrimaryPaths) == 0 {
		return ""
	}
	first := NormalizePath(packet.PrimaryPaths[0])
	if first == "" {
		return ""
	}
	if len(packet.PrimaryPaths) == 1 {
		return "stage 1 edits " + first + " only, then verifies before any follow-up widening"
	}
	rest := LimitStrings(packet.PrimaryPaths[1:], 3)
	if len(rest) == 0 {
		return "stage 1 edits " + first + " only, then verifies before any follow-up widening"
	}
	return "stage 1 edits " + first + " first; verify before widening to " + strings.Join(rest, ", ")
}

func StructuredReasonLooksStructural(reason string, strategyHints []string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return false
	}
	if ContainsAny(
		reason,
		"structur",
		"boundary",
		"boundaries",
		"responsibilit",
		"split",
		"extract",
		"separate",
		"move",
		"adapter",
		"interface",
		"ownership",
		"coupling",
		"orchestration",
	) {
		return true
	}
	for _, hint := range strategyHints {
		for _, token := range strings.Fields(strings.ToLower(hint)) {
			token = strings.Trim(token, "`'\"()[]{}<>,:;!?|")
			if len(token) < 6 {
				continue
			}
			if strings.Contains(reason, token) {
				return true
			}
		}
	}
	return false
}

func StructuralPlanAllowsMultiFileStage(needsStructuralPolicy bool, strategyHints []string, operationReasons []string) bool {
	if !needsStructuralPolicy {
		return true
	}
	if !StructuralHintsAllowMultiFileStage(strategyHints) {
		return false
	}
	for _, reason := range operationReasons {
		if !StructuredReasonAllowsMultiFileStage(reason) {
			return false
		}
	}
	return true
}

func StructuralHintsAllowMultiFileStage(hints []string) bool {
	for _, hint := range hints {
		normalized := strings.ToLower(hint)
		if ContainsAny(normalized, "interface", "adapter", "move ", "moving ", "extract ", "extraction", "cross-file", "across files", "between files") {
			return true
		}
	}
	return false
}

func StructuredReasonAllowsMultiFileStage(reason string) bool {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	return ContainsAny(normalized, "interface", "adapter", "move ", "moving ", "extract ", "extraction", "cross-file", "across files", "boundary")
}

type TargetGuard struct {
	workspace           *workspace.Manager
	packet              TargetPacket
	targetConsumed      bool
	AllowFullTargetRead bool
	bootstrappedReads   map[string]struct{}
	bootstrapWindows    map[string][2]int
}

func NewTargetGuard(mgr *workspace.Manager, packet TargetPacket, bootstrap []workspace.ToolResult) *TargetGuard {
	g := &TargetGuard{
		workspace:         mgr,
		packet:            packet,
		bootstrappedReads: make(map[string]struct{}),
		bootstrapWindows:  make(map[string][2]int),
	}
	for _, result := range bootstrap {
		if result.Tool != "read_file" || result.Error != "" {
			continue
		}
		path, startLine, endLine, ok := ParseReadWindowSummary(result.Summary)
		if !ok {
			continue
		}
		if g.PathAllowed(path) {
			g.bootstrappedReads[path] = struct{}{}
			g.bootstrapWindows[path] = [2]int{startLine, endLine}
		}
	}
	return g
}

func (g *TargetGuard) PreExecute(req workspace.ToolRequest) *workspace.ToolResult {
	if g == nil {
		return nil
	}
	if blocked := g.BlockWriteOutsideEditableTargets(req); blocked != nil {
		return blocked
	}
	if g.targetConsumed || len(g.packet.PrimaryPaths) == 0 {
		return nil
	}
	path := NormalizePath(req.Path)
	if req.Tool == "read_file" && path != "" && req.ID == "" && req.StartLine == 0 && req.EndLine == 0 {
		if _, ok := g.bootstrappedReads[path]; ok && !g.AllowFullTargetRead {
			return &workspace.ToolResult{
				Tool:    "read_file",
				Summary: "Implementation bootstrap already inspected this target",
				Error:   "the target packet already includes a focused read of this file. Use edit_file, a narrower read_file window, or search_content scoped to this path instead of rereading the whole file.",
			}
		}
	}
	if g.targeted(req.Tool, path) {
		g.targetConsumed = true
		return nil
	}
	switch req.Tool {
	case "list_files", "search_files", "go_overview", "go_entrypoints", "go_workspace_symbol", "go_find_package":
		if redirected, ok := g.redirectedTargetInspection(req); ok {
			return redirected
		}
		return &workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Implementation target packet blocked broad discovery",
			Error:   "implementation already has concrete target files. Read or search those files first before broad repo discovery.",
		}
	case "search_content", "read_file", "go_find_symbol", "go_find_function", "go_read_symbol", "go_find_type", "go_find_const", "go_definition", "go_references", "go_implementations", "go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_related_files":
		if path == "" {
			if redirected, ok := g.redirectedTargetInspection(req); ok {
				return redirected
			}
			return &workspace.ToolResult{
				Tool:    req.Tool,
				Summary: "Implementation target packet requires scoped inspection",
				Error:   "implementation already has concrete target files. Scope this inspection to a target file before widening the search.",
			}
		}
		if !g.PathReadableAllowed(path) {
			if redirected, ok := g.redirectedTargetInspection(req); ok {
				return redirected
			}
			return &workspace.ToolResult{
				Tool:    req.Tool,
				Summary: "Implementation target packet blocked unrelated path",
				Error:   fmt.Sprintf("implementation should start with the target packet files (%s) before inspecting %s.", strings.Join(g.packet.PrimaryPaths, ", "), path),
			}
		}
		g.targetConsumed = true
		return nil
	default:
		return nil
	}
}

func (g *TargetGuard) redirectedTargetInspection(req workspace.ToolRequest) (*workspace.ToolResult, bool) {
	if g == nil || g.workspace == nil || len(g.packet.PrimaryPaths) == 0 {
		return nil, false
	}
	g.workspace.PrimeDiscoveredPaths(LimitStrings(append([]string(nil), g.packet.PrimaryPaths...), 8))
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
			if path, startLine, endLine, ok := ParseReadWindowSummary(result.Summary); ok && g.PathAllowed(path) {
				g.bootstrappedReads[path] = struct{}{}
				g.bootstrapWindows[path] = [2]int{startLine, endLine}
			}
		}
		result.Summary = fmt.Sprintf(
			"Implementation target packet redirected %s to %s on %s (%s)",
			req.Tool,
			fallback.Tool,
			NormalizePath(fallback.Path),
			strings.TrimSpace(result.Summary),
		)
		return &result, true
	}
	return nil, false
}

func (g *TargetGuard) redirectedReadFileResult(req workspace.ToolRequest) (workspace.ToolResult, bool) {
	path := NormalizePath(req.Path)
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

func (g *TargetGuard) redirectedTargetRequests(req workspace.ToolRequest) []workspace.ToolRequest {
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

func (g *TargetGuard) redirectTargetPath(requestedPath string) string {
	requestedPath = NormalizePath(requestedPath)
	if g.PathAllowed(requestedPath) {
		for _, candidate := range g.packet.PrimaryPaths {
			candidate = NormalizePath(candidate)
			if candidate == requestedPath {
				return candidate
			}
		}
	}
	for _, anchor := range g.packet.Anchors {
		candidate := NormalizePath(anchor.Path)
		if candidate != "" {
			return candidate
		}
	}
	for _, candidate := range g.packet.PrimaryPaths {
		candidate = NormalizePath(candidate)
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func (g *TargetGuard) redirectReadWindow(path string) (int, int) {
	path = NormalizePath(path)
	if path == "" {
		return 1, 180
	}
	if window, ok := g.bootstrapWindows[path]; ok && window[0] > 0 && window[1] >= window[0] {
		return window[0], window[1]
	}
	for _, anchor := range g.packet.Anchors {
		if NormalizePath(anchor.Path) != path || anchor.Line <= 0 {
			continue
		}
		return maxInt(1, anchor.Line-2), anchor.Line + 18
	}
	return 1, 180
}

func (g *TargetGuard) targeted(tool string, path string) bool {
	if path == "" {
		return false
	}
	switch tool {
	case "edit_file", "write_file", "append_file", "go_move_declarations":
		return g.PathEditableAllowed(path)
	case "read_file", "search_content", "go_find_symbol", "go_find_function", "go_read_symbol", "go_extract_body", "go_file_outline":
		return g.PathReadableAllowed(path)
	default:
		return false
	}
}

func (g *TargetGuard) PathAllowed(path string) bool {
	return g.PathReadableAllowed(path)
}

func (g *TargetGuard) PathReadableAllowed(path string) bool {
	path = NormalizePath(path)
	if path == "" {
		return false
	}
	if g.PathForbidden(path) {
		return false
	}
	candidates := append([]string(nil), g.packet.PrimaryPaths...)
	candidates = append(candidates, g.packet.EvidencePaths...)
	candidates = append(candidates, g.packet.AnalogyPaths...)
	for _, candidate := range candidates {
		candidate = NormalizePath(candidate)
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

func (g *TargetGuard) PathEditableAllowed(path string) bool {
	path = NormalizePath(path)
	if path == "" || g.PathForbidden(path) {
		return false
	}
	if g.PathPrimaryAllowed(path) {
		return true
	}
	for _, candidate := range g.packet.AllowedDestinations {
		if path == NormalizePath(candidate) {
			return true
		}
	}
	return false
}

func (g *TargetGuard) PathPrimaryAllowed(path string) bool {
	path = NormalizePath(path)
	if path == "" || g.PathForbidden(path) {
		return false
	}
	for _, candidate := range g.packet.PrimaryPaths {
		if path == NormalizePath(candidate) {
			return true
		}
	}
	return false
}

func (g *TargetGuard) PathForbidden(path string) bool {
	path = NormalizePath(path)
	if path == "" {
		return false
	}
	for _, candidate := range g.packet.ForbiddenPaths {
		if path == NormalizePath(candidate) {
			return true
		}
	}
	return false
}

func (g *TargetGuard) BlockWriteOutsideEditableTargets(req workspace.ToolRequest) *workspace.ToolResult {
	if g == nil || len(g.packet.PrimaryPaths) == 0 {
		return nil
	}
	tool := strings.TrimSpace(req.Tool)
	block := func(path string, reason string) *workspace.ToolResult {
		return &workspace.ToolResult{
			Tool:    tool,
			Summary: "Implementation target packet blocked write outside approved targets",
			Error:   fmt.Sprintf("%s is not an approved editable target (%s). %s", NormalizePath(path), strings.Join(g.packet.PrimaryPaths, ", "), reason),
		}
	}
	switch tool {
	case "edit_file", "write_file", "append_file", "mkdir":
		path := NormalizePath(req.Path)
		if path == "" {
			return nil
		}
		if !g.PathEditableAllowed(path) {
			return block(path, "Read-only evidence and analogy paths may be inspected but not modified.")
		}
	case "apply_patch":
		ops, err := workspace.ParsePatch(req.Content)
		if err != nil {
			return &workspace.ToolResult{
				Tool:    tool,
				Summary: "Implementation target packet blocked unparseable patch",
				Error:   err.Error(),
			}
		}
		for _, opPath := range workspace.PatchOpPaths(ops) {
			path := NormalizePath(opPath)
			if path == "" {
				continue
			}
			if !g.PathEditableAllowed(path) {
				return block(path, "Read-only evidence and analogy paths may be inspected but not modified.")
			}
		}
	case "go_move_declarations":
		source := NormalizePath(req.Path)
		if source != "" && !g.PathPrimaryAllowed(source) {
			return block(source, "Declaration moves may only originate from approved editable targets.")
		}
		destinations := []string{NormalizePath(req.Destination)}
		for _, move := range req.Moves {
			destinations = append(destinations, NormalizePath(move.Destination))
		}
		for _, destination := range destinations {
			if destination == "" {
				continue
			}
			if g.PathForbidden(destination) || (g.PathReadableAllowed(destination) && !g.PathEditableAllowed(destination)) {
				return block(destination, "Declaration moves may not overwrite read-only evidence, analogy, or forbidden paths.")
			}
		}
	}
	return nil
}

func ParseReadWindowSummary(summary string) (string, int, int, bool) {
	matches := regexp.MustCompile(`Read (?:f\d+\s+)?(.+?) lines (\d+)-(\d+)`).FindStringSubmatch(strings.TrimSpace(summary))
	if len(matches) != 4 {
		return "", 0, 0, false
	}
	startLine, err := strconv.Atoi(matches[2])
	if err != nil {
		return "", 0, 0, false
	}
	endLine, err := strconv.Atoi(matches[3])
	if err != nil {
		return "", 0, 0, false
	}
	return NormalizePath(matches[1]), startLine, endLine, true
}

type StructuralAnchorGuard struct {
	packet TargetPacket
	needed bool
	seen   bool
}

func NewStructuralAnchorGuard(packet TargetPacket) *StructuralAnchorGuard {
	return &StructuralAnchorGuard{
		packet: packet,
		needed: TargetPacketRequiresConcreteStructuralAnchor(packet),
	}
}

func (g *StructuralAnchorGuard) Required() bool {
	return g != nil && g.needed && !g.seen
}

func (g *StructuralAnchorGuard) PreExecute(req workspace.ToolRequest) *workspace.ToolResult {
	if !g.Required() || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return nil
	}
	return &workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Structural implementation target rejected",
		Error:   "the approved structural plan names only a file-level target. Inspect a concrete declaration or line anchor first with go_file_outline, go_find_function/go_find_symbol, search_content scoped to the primary file, or a narrow read_file window before editing.",
	}
}

func (g *StructuralAnchorGuard) Observe(req workspace.ToolRequest, result workspace.ToolResult) {
	if !g.Required() || result.Error != "" {
		return
	}
	if StructuralAnchorEvidenceResult(g.packet, req, result) {
		g.seen = true
	}
}

func TargetPacketRequiresConcreteStructuralAnchor(packet TargetPacket) bool {
	if !TargetPacketNeedsStructuralPolicy(packet) {
		return false
	}
	return !TargetPacketHasConcreteAnchor(packet)
}

func TargetPacketHasConcreteAnchor(packet TargetPacket) bool {
	return len(packet.Anchors) > 0 ||
		len(packet.Symbols) > 0 ||
		len(packet.SymbolHints) > 0 ||
		len(packet.LiteralFragments) > 0 ||
		len(packet.LocationHints) > 0
}

func StructuralAnchorEvidenceResult(packet TargetPacket, req workspace.ToolRequest, result workspace.ToolResult) bool {
	switch req.Tool {
	case "go_file_outline":
		return structuralAnchorPathAllowed(packet, req.Path)
	case "go_find_symbol", "go_find_function", "go_find_type", "go_find_const", "go_definition", "go_references", "go_calls_from", "go_calls_into", "go_extract_body", "go_read_symbol":
		if structuralAnchorPathAllowed(packet, req.Path) {
			return true
		}
		return resultReferencesPrimaryPath(packet, result.Output)
	case "search_content":
		return structuralAnchorPathAllowed(packet, req.Path) && strings.TrimSpace(result.Output) != ""
	case "read_file":
		path, startLine, endLine, ok := ParseReadWindowSummary(result.Summary)
		if !ok || !structuralAnchorPathAllowed(packet, path) {
			return false
		}
		if startLine <= 0 || endLine < startLine {
			return false
		}
		return endLine-startLine <= 80 && !(startLine == 1 && endLine >= 120)
	default:
		return false
	}
}

func structuralAnchorPathAllowed(packet TargetPacket, path string) bool {
	path = NormalizePath(path)
	if path == "" {
		return false
	}
	for _, candidate := range packet.PrimaryPaths {
		if NormalizePath(candidate) == path {
			return true
		}
	}
	return false
}

func resultReferencesPrimaryPath(packet TargetPacket, output string) bool {
	for _, path := range packet.PrimaryPaths {
		path = NormalizePath(path)
		if path != "" && strings.Contains(output, path) {
			return true
		}
	}
	return false
}

func LimitStrings(values []string, limit int) []string {
	if limit == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
		if limit > 0 && len(unique) >= limit {
			break
		}
	}
	return unique
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
