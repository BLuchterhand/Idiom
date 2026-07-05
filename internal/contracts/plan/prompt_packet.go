package plan

import (
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/contracts/workflowstate"
)

type PromptPacket struct {
	RequiredEditPaths      []string
	AllowedEditPaths       []string
	EvidencePaths          []string
	TargetPacketMarkers    []string
	Context                PromptContextPacketProjection
	HasExactEditAnchor     bool
	HasRequiredEditPayload bool
}

type PromptContextPacketProjection struct {
	WorkflowID            workflowstate.WorkflowID
	State                 workflowstate.StateID
	TerminalState         workflowstate.TerminalState
	Dispatchable          bool
	StructuredPacket      bool
	LegacyHeadingFallback bool
	LegacyHeadings        []string
	Markers               []string
	Reason                string
}

type RequiredEditSnippet struct {
	ID      string
	Path    string
	Content string
}

func PromptPacketFromPrompt(prompt string) PromptPacket {
	packet := PromptPacket{
		RequiredEditPaths:   append([]string(nil), RequiredEditPathsFromPrompt(prompt)...),
		AllowedEditPaths:    append([]string(nil), AllowedEditPathsFromPrompt(prompt)...),
		EvidencePaths:       PromptContextEvidencePathsFromPrompt(prompt),
		TargetPacketMarkers: PromptContextPacketMarkersFromPrompt(prompt),
		Context:             PromptContextPacketProjectionFromPrompt(prompt),
		HasExactEditAnchor:  PromptContainsExactEditAnchor(prompt),
	}
	packet.HasRequiredEditPayload = PromptHasRequiredEditPayloadFromMarkers(packet.HasExactEditAnchor, packet.TargetPacketMarkers)
	return packet
}

func RequiredEditSnippetsFromPrompt(prompt string, requiredPaths []string) []RequiredEditSnippet {
	headings := []string{
		"required edits",
		"edits to apply",
		"edits",
		"implementation shape",
		"implementation",
	}

	var blocks []requiredEditBlock

	for _, heading := range headings {
		candidate := promptMarkdownSectionByHeading(prompt, heading)
		if strings.TrimSpace(candidate) == "" {
			continue
		}

		if found := requiredEditCodeBlocksWithPaths(candidate, requiredPaths); len(found) > 0 {
			blocks = found
			break
		}
	}

	if len(blocks) == 0 {
		return nil
	}

	path := ""
	if len(requiredPaths) == 1 {
		path = strings.TrimSpace(requiredPaths[0])
	}

	snippets := make([]RequiredEditSnippet, 0, len(blocks))
	for _, block := range blocks {
		content := strings.TrimSpace(block.Content)
		if content == "" {
			continue
		}

		blockPaths := block.Paths
		if len(blockPaths) == 0 && path != "" {
			blockPaths = []string{path}
		}

		if len(blockPaths) == 0 {
			blockPaths = []string{""}
		}

		for _, blockPath := range blockPaths {
			snippets = append(snippets, RequiredEditSnippet{
				ID:      fmt.Sprintf("required_edit_%d", len(snippets)+1),
				Path:    strings.TrimSpace(blockPath),
				Content: content,
			})
		}
	}

	if len(snippets) == 0 {
		return nil
	}

	return snippets
}

type requiredEditBlock struct {
	Paths   []string
	Content string
}

func requiredEditCodeBlocksWithPaths(section string, requiredPaths []string) []requiredEditBlock {
	var (
		blocks  []requiredEditBlock
		current []string
	)

	previousNonEmpty := ""

	var (
		currentPaths []string
		blockPaths   []string
	)

	fence := ""
	skipCurrent := false

	for _, rawLine := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if fence == "" {
			if marker, info, ok := promptMarkdownFenceOpenInfo(trimmed); ok {
				fence = marker
				current = nil

				blockPaths = append([]string(nil), currentPaths...)
				skipCurrent = skipRequiredEditCodeBlock(previousNonEmpty, info)

				continue
			}

			if trimmed != "" {
				if paths := requiredEditPathsMentionedInLine(trimmed, requiredPaths); len(paths) > 0 {
					currentPaths = paths
				}

				previousNonEmpty = trimmed
			}

			continue
		}

		if strings.HasPrefix(trimmed, fence) {
			if !skipCurrent {
				if block := strings.TrimSpace(strings.Join(current, "\n")); block != "" {
					blocks = append(blocks, requiredEditBlock{Paths: append([]string(nil), blockPaths...), Content: block})
				}
			}

			fence = ""
			current = nil
			blockPaths = nil
			skipCurrent = false
			previousNonEmpty = ""

			continue
		}

		current = append(current, rawLine)
	}

	return blocks
}

func requiredEditPathsMentionedInLine(line string, requiredPaths []string) []string {
	if len(requiredPaths) == 0 {
		return nil
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	matched := make(map[string]struct{}, len(requiredPaths))
	for _, path := range requiredPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		if strings.Contains(line, path) || strings.Contains(line, "`"+path+"`") {
			matched[path] = struct{}{}
		}
	}

	baseCounts := make(map[string]int, len(requiredPaths))
	for _, path := range requiredPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		baseCounts[filepath.Base(path)]++
	}

	for _, path := range requiredPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		base := filepath.Base(path)
		if base == "." || base == "/" || base == "" || baseCounts[base] != 1 {
			continue
		}

		if strings.Contains(line, "`"+base+"`") {
			matched[path] = struct{}{}
		}
	}

	if len(matched) == 0 {
		return nil
	}

	out := make([]string, 0, len(matched))

	for _, path := range requiredPaths {
		path = strings.TrimSpace(path)
		if _, ok := matched[path]; ok {
			out = append(out, path)
		}
	}

	return out
}

func skipRequiredEditCodeBlock(label string, info string) bool {
	lowerInfo := strings.ToLower(strings.TrimSpace(info))
	if strings.Contains(lowerInfo, "diff") || strings.Contains(lowerInfo, "patch") {
		return true
	}

	lowerLabel := strings.Trim(strings.ToLower(strings.TrimSpace(label)), ":. ")

	return strings.HasPrefix(lowerLabel, "old") ||
		strings.HasPrefix(lowerLabel, "before") ||
		strings.HasPrefix(lowerLabel, "existing") ||
		strings.HasPrefix(lowerLabel, "current") ||
		strings.HasPrefix(lowerLabel, "replace this")
}

func PromptContextEvidencePathsFromPrompt(prompt string) []string {
	projection := PromptContextPacketProjectionFromPrompt(prompt)
	headings := projection.LegacyHeadings
	if len(headings) == 0 {
		return nil
	}

	paths := promptWorkspacePathsUnderHeadings(prompt, headings)

	for _, heading := range headings {
		section := promptMarkdownSectionByHeading(prompt, heading)
		if strings.TrimSpace(section) == "" {
			continue
		}

		paths = appendUniquePromptScopeStrings(paths, PromptWorkspacePaths(section)...)
	}

	return paths
}

func PromptContextPacketMarkersFromPrompt(prompt string) []string {
	projection := PromptContextPacketProjectionFromPrompt(prompt)

	type packetSection struct {
		heading string
		marker  string
	}

	sections := []packetSection{
		{heading: "verified current state", marker: "prompt_context:verified_current_state"},
		{heading: "current state", marker: "prompt_context:current_state"},
		{heading: "current-state anchors", marker: "prompt_context:current_state_anchors"},
		{heading: "source evidence", marker: "prompt_context:source_evidence"},
		{heading: "context packet", marker: "prompt_context:context_packet"},
		{heading: "context pack", marker: "prompt_context:context_packet"},
		{heading: "context view", marker: "prompt_context:context_view"},
		{heading: "provided context", marker: "prompt_context:provided_context"},
		{heading: "implementation context", marker: "prompt_context:implementation_context"},
		{heading: "discovery evidence", marker: "prompt_context:discovery_evidence"},
		{heading: "candidate scope", marker: "prompt_context:candidate_scope"},
		{heading: "required edits", marker: "prompt_context:required_edits"},
	}

	markers := append([]string(nil), projection.Markers...)
	if !projection.LegacyHeadingFallback && !projection.StructuredPacket {
		return markers
	}

	for _, section := range sections {
		if !promptHasSectionHeading(prompt, section.heading) {
			continue
		}

		markers = appendUniquePromptScopeStrings(markers, section.marker)
	}

	return markers
}

func PromptHasRequiredEditPayloadFromMarkers(hasExactEditAnchor bool, markers []string) bool {
	if hasExactEditAnchor {
		return true
	}

	for _, marker := range markers {
		if marker == "prompt_context:required_edits" {
			return true
		}
	}

	return false
}

func PromptContextPacketProjectionFromPrompt(prompt string) PromptContextPacketProjection {
	headings := promptContextPacketLegacyHeadings()
	projection := PromptContextPacketProjection{
		WorkflowID:       workflowstate.WorkflowPromptContextPacket,
		State:            workflowstate.StateTerminal,
		TerminalState:    workflowstate.TerminalStateBlocked,
		LegacyHeadings:   headings,
		StructuredPacket: PromptHasStructuredPacketMarker(prompt),
	}
	if projection.StructuredPacket {
		projection.State = workflowstate.StateDispatchable
		projection.TerminalState = ""
		projection.Dispatchable = true
		projection.Markers = appendUniquePromptScopeStrings(projection.Markers, "prompt_context:structured_packet")
		projection.Reason = "structured_packet"
		return projection
	}

	if !promptHasAnySectionHeading(prompt, headings) {
		projection.Reason = "missing_structured_packet_or_legacy_heading"
		return projection
	}

	projection.LegacyHeadingFallback = true
	projection.State = workflowstate.StateDispatchable
	projection.TerminalState = ""
	projection.Dispatchable = true
	projection.Reason = "legacy_heading_fallback"

	return projection
}

func promptContextPacketLegacyHeadings() []string {
	return []string{
		"verified current state",
		"current state",
		"current-state anchors",
		"source evidence",
		"context packet",
		"context pack",
		"context view",
		"provided context",
		"implementation context",
		"discovery evidence",
		"evidence",
		"evidence paths",
		"primary evidence paths",
		"read-only evidence paths",
		"required reads",
		"required read",
		"input files",
		"relevant files",
		"candidate scope",
	}
}

func PromptHasStructuredPacketMarker(prompt string) bool {
	lower := strings.ToLower(prompt)
	if strings.Contains(lower, `"context_packet"`) ||
		strings.Contains(lower, `"context_pack"`) ||
		(strings.Contains(lower, `"planning_packet`) && strings.Contains(lower, `"schema"`)) {

		return true
	}

	keys := []string{`"context_budget"`, `"candidate_scope"`, `"anchors"`, `"evidence"`, `"files_to_modify"`, `"required_edits"`}
	count := 0

	for _, key := range keys {
		if strings.Contains(lower, key) {
			count++
		}
	}

	return count >= 2
}

func PromptContainsExactEditAnchor(prompt string) bool {
	lower := strings.ToLower(prompt)
	if strings.Contains(prompt, "*** Begin Patch") ||
		strings.Contains(prompt, "```diff") ||
		(strings.Contains(prompt, "\n--- ") && strings.Contains(prompt, "\n+++ ") && strings.Contains(prompt, "\n@@")) {

		return true
	}

	return promptHasStructuredExactEditPair(lower)
}

func promptWorkspacePathsUnderHeadings(prompt string, headings []string) []string {
	headingSet := promptHeadingSet(headings)

	var paths []string

	active := false
	sawPath := false

	for _, rawLine := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(rawLine)
		normalized := normalizePromptSectionHeading(line)

		if promptHeadingInSet(normalized, headingSet) {
			active = true
			sawPath = false

			continue
		}

		if !active {
			continue
		}

		if line == "" {
			if sawPath {
				active = false
			}

			continue
		}

		linePaths := PromptWorkspacePaths(line)
		if len(linePaths) > 0 {
			sawPath = true
			paths = appendUniquePromptScopeStrings(paths, linePaths...)

			continue
		}

		if looksLikePromptSectionBoundary(line) {
			if !sawPath && looksLikeRequiredPathPrelude(line) {
				continue
			}

			active = false

			continue
		}
	}

	return paths
}

func promptHasAnySectionHeading(prompt string, headings []string) bool {
	for _, heading := range headings {
		if promptHasSectionHeading(prompt, heading) {
			return true
		}
	}
	return false
}

func promptHasSectionHeading(prompt string, heading string) bool {
	if strings.TrimSpace(promptMarkdownSectionByHeading(prompt, heading)) != "" {
		return true
	}

	want := normalizePromptSectionHeading(heading)
	if want == "" {
		return false
	}

	inFence := false
	fence := ""
	lines := strings.Split(prompt, "\n")

	for i, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if marker, ok := promptMarkdownFenceOpen(line); ok {
			if !inFence {
				inFence = true
				fence = marker
			} else if strings.HasPrefix(line, fence) {
				inFence = false
				fence = ""
			}

			continue
		}

		if inFence {
			continue
		}

		if normalizePromptSectionHeading(line) == want {
			if loosePromptSectionHasContent(lines, i+1) {
				return true
			}

			continue
		}
	}

	return false
}

func loosePromptSectionHasContent(lines []string, start int) bool {
	if start < 0 || start >= len(lines) {
		return false
	}

	for _, rawLine := range lines[start:] {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		if len(PromptWorkspacePaths(line)) > 0 {
			return true
		}

		if looksLikePromptSectionBoundary(line) {
			return isPromptPacketNestedSection(line)
		}

		return true
	}

	return false
}

func isPromptPacketNestedSection(line string) bool {
	switch normalizePromptSectionHeading(line) {
	case "anchors",
		"candidate scope",
		"context",
		"context evidence",
		"current state",
		"evidence",
		"files",
		"source evidence",
		"symbols",
		"target refs",
		"tests",
		"verified current state":
		return true
	default:
		return false
	}
}

func promptHeadingInSet(candidate string, headingSet map[string]struct{}) bool {
	if _, ok := headingSet[candidate]; ok {
		return true
	}

	if trimmed := trimPromptHeadingQualifier(candidate); trimmed != candidate {
		_, ok := headingSet[trimmed]
		return ok
	}

	return false
}

func trimPromptHeadingQualifier(heading string) string {
	heading = strings.TrimSpace(heading)
	for _, marker := range []string{" (", " [", " - ", " -- "} {
		if idx := strings.Index(heading, marker); idx > 0 {
			return strings.TrimSpace(heading[:idx])
		}
	}

	return heading
}

func promptMarkdownSectionByHeading(markdown string, headingName string) string {
	want := normalizePromptMarkdownHeadingText(headingName)
	lines := strings.Split(markdown, "\n")
	start := -1
	startLevel := 0
	inFence := false
	fence := ""

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if marker, ok := promptMarkdownFenceOpen(trimmed); ok {
			if !inFence {
				inFence = true
				fence = marker
			} else if strings.HasPrefix(trimmed, fence) {
				inFence = false
				fence = ""
			}
			continue
		}
		if inFence {
			continue
		}
		level, text, ok := promptMarkdownHeading(trimmed)
		if !ok || trimPromptHeadingQualifier(normalizePromptMarkdownHeadingText(text)) != trimPromptHeadingQualifier(want) {
			continue
		}
		start = i + 1
		startLevel = level
		break
	}

	if start < 0 {
		return ""
	}

	end := len(lines)
	inFence = false
	fence = ""
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if marker, ok := promptMarkdownFenceOpen(trimmed); ok {
			if !inFence {
				inFence = true
				fence = marker
			} else if strings.HasPrefix(trimmed, fence) {
				inFence = false
				fence = ""
			}
			continue
		}
		if inFence {
			continue
		}
		level, _, ok := promptMarkdownHeading(trimmed)
		if ok && level <= startLevel {
			end = i
			break
		}
	}

	return strings.Join(lines[start:end], "\n")
}

func promptMarkdownHeading(line string) (int, string, bool) {
	if !strings.HasPrefix(line, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	text := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(line[level+1:]), "#"))
	if text == "" {
		return 0, "", false
	}
	return level, text, true
}

func normalizePromptMarkdownHeadingText(text string) string {
	text = strings.Trim(strings.ToLower(strings.TrimSpace(text)), ": ")
	return strings.Join(strings.Fields(text), " ")
}

func promptMarkdownFenceOpen(line string) (string, bool) {
	marker, _, ok := promptMarkdownFenceOpenInfo(line)
	return marker, ok
}

func promptMarkdownFenceOpenInfo(line string) (marker string, info string, ok bool) {
	switch {
	case strings.HasPrefix(line, "```"):
		return "```", strings.TrimSpace(strings.TrimPrefix(line, "```")), true
	case strings.HasPrefix(line, "~~~"):
		return "~~~", strings.TrimSpace(strings.TrimPrefix(line, "~~~")), true
	default:
		return "", "", false
	}
}

func promptHasStructuredExactEditPair(lowerPrompt string) bool {
	type pair struct {
		left  string
		right string
	}

	pairs := []pair{
		{left: "old_text", right: "new_text"},
		{left: "old text", right: "new text"},
		{left: "old snippet", right: "new snippet"},
		{left: "before", right: "after"},
		{left: "replace", right: "with"},
		{left: "exact old", right: "exact new"},
	}
	labels := make(map[string]struct{}, len(pairs)*2)

	for _, line := range strings.Split(lowerPrompt, "\n") {
		label, ok := exactEditAnchorLineLabel(line)
		if !ok {
			continue
		}

		labels[label] = struct{}{}
	}

	for _, pair := range pairs {
		if _, ok := labels[pair.left]; !ok {
			continue
		}

		if _, ok := labels[pair.right]; ok {
			return true
		}
	}

	return false
}

func exactEditAnchorLineLabel(line string) (string, bool) {
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "-*")
	line = strings.TrimSpace(line)

	if line == "" {
		return "", false
	}

	separator := strings.Index(line, ":")
	if equals := strings.Index(line, "="); equals >= 0 && (separator < 0 || equals < separator) {
		separator = equals
	}

	if separator < 0 {
		return "", false
	}

	label := strings.TrimSpace(line[:separator])

	label = strings.Trim(label, "`'\"*_ {}")
	label = strings.NewReplacer("-", " ", "_", " ").Replace(label)
	label = strings.Join(strings.Fields(label), " ")

	switch label {
	case "old text", "new text", "old snippet", "new snippet", "before", "after", "replace", "with", "exact old", "exact new":
		return label, true
	case "old":
		return "old text", true
	case "new":
		return "new text", true
	default:
		return "", false
	}
}
