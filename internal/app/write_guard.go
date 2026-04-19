package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"qwen-bubbles/internal/semantic"
	"qwen-bubbles/internal/workspace"
	"slices"
	"strings"
	"time"
)

type writeApprovalProposalMsg struct {
	proposal writeApprovalProposal
}

type writeApprovalKind string

const (
	writeApprovalProtectedPath writeApprovalKind = "protected_path"
	writeApprovalBroadScope    writeApprovalKind = "broad_scope"
)

type writeApprovalProposal struct {
	Prompt string
	Tool   string
	Kind   writeApprovalKind
	Paths  []string
	Reason string

	PlannedEdits []string
	PreviewHunks []string

	Risk           writeApprovalRisk
	ChangeKinds    []string
	RiskReasons    []string
	ImpactSummary  []string
	BlastRadius    []string
	DeepAnalysis   []string
	AffectedTests  []string
	ApprovalNotes  []string
	VerifyCoverage string
	VerifySteps    []string
	VerifyRisk     string
	VerifyExpected []string
	VerifyRepair   string
}

type writeApprovalGrant struct {
	Tool  string
	Paths map[string]struct{}
}

func (p writeApprovalProposal) bannerText() string {
	scope := "guarded write approval pending"
	switch p.Kind {
	case writeApprovalProtectedPath:
		scope = "protected path approval pending"
	case writeApprovalBroadScope:
		scope = "broad write approval pending"
	}
	if strings.TrimSpace(string(p.Risk)) != "" {
		scope += " [" + string(p.Risk) + " risk]"
	}
	return scope + ": " + strings.Join(p.Paths, ", ") + " | /approve writes | /deny writes"
}

func (p writeApprovalProposal) statusText() string {
	if strings.TrimSpace(string(p.Risk)) != "" {
		return "Pending " + string(p.Risk) + "-risk guarded write approval. Use /approve writes to rerun with the proposed scope, /deny writes to cancel, or /writes auto for broader autonomy."
	}
	return "Pending guarded write approval. Use /approve writes to rerun with the proposed scope, /deny writes to cancel, or /writes auto for broader autonomy."
}

func (p writeApprovalProposal) renderSummary() string {
	var b strings.Builder
	b.WriteString("Guarded write approval required.\n\n")
	switch p.Kind {
	case writeApprovalProtectedPath:
		b.WriteString("This request wants to modify a protected path under guarded writes.\n\n")
	case writeApprovalBroadScope:
		b.WriteString("This request wants to expand beyond the default guarded write scope.\n\n")
	default:
		b.WriteString("This request needs a broader write scope than guarded mode allows by default.\n\n")
	}
	b.WriteString("Proposed scope:\n")
	for _, path := range p.Paths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteString("\n")
	}
	if strings.TrimSpace(p.Reason) != "" {
		b.WriteString("\nReason:\n- ")
		b.WriteString(strings.TrimSpace(p.Reason))
		b.WriteString("\n")
	}
	if len(p.PlannedEdits) > 0 {
		b.WriteString("\nProposed edits:\n")
		for _, line := range p.PlannedEdits {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.PreviewHunks) > 0 {
		b.WriteString("\nDiff preview:\n")
		for _, hunk := range p.PreviewHunks {
			hunk = strings.TrimSpace(hunk)
			if hunk == "" {
				continue
			}
			b.WriteString("```diff\n")
			b.WriteString(hunk)
			b.WriteString("\n```\n")
		}
	}
	if strings.TrimSpace(string(p.Risk)) != "" {
		b.WriteString("\nAssessed risk:\n- ")
		b.WriteString(string(p.Risk))
		b.WriteString("\n")
	}
	if len(p.ChangeKinds) > 0 {
		b.WriteString("\nChange classes:\n")
		for _, line := range p.ChangeKinds {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.RiskReasons) > 0 {
		b.WriteString("\nRisk drivers:\n")
		for _, line := range p.RiskReasons {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.ImpactSummary) > 0 {
		b.WriteString("\nImpact summary:\n")
		for _, line := range p.ImpactSummary {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.BlastRadius) > 0 {
		b.WriteString("\nBlast radius:\n")
		for _, line := range p.BlastRadius {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.DeepAnalysis) > 0 {
		b.WriteString("\nDeep analysis:\n")
		for _, line := range p.DeepAnalysis {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.AffectedTests) > 0 {
		b.WriteString("\nAffected test surfaces:\n")
		for _, line := range p.AffectedTests {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if len(p.ApprovalNotes) > 0 {
		b.WriteString("\nApproval guidance:\n")
		for _, line := range p.ApprovalNotes {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if strings.TrimSpace(p.VerifyCoverage) != "" {
		b.WriteString("\nVerification confidence:\n- ")
		b.WriteString(strings.TrimSpace(p.VerifyCoverage))
		b.WriteString("\n")
	}
	if strings.TrimSpace(p.VerifyRisk) != "" || len(p.VerifyExpected) > 0 || strings.TrimSpace(p.VerifyRepair) != "" {
		b.WriteString("\nVerification risk contract:\n")
		if strings.TrimSpace(p.VerifyRisk) != "" {
			b.WriteString("- risk: ")
			b.WriteString(strings.TrimSpace(p.VerifyRisk))
			b.WriteString("\n")
		}
		if len(p.VerifyExpected) > 0 {
			b.WriteString("- expected failure modes: ")
			b.WriteString(strings.Join(uniqueLimitedStrings(p.VerifyExpected, 4), ", "))
			b.WriteString("\n")
		}
		if strings.TrimSpace(p.VerifyRepair) != "" {
			b.WriteString("- repair policy: ")
			b.WriteString(truncateText(strings.TrimSpace(p.VerifyRepair), 180))
			b.WriteString("\n")
		}
	}
	if len(p.VerifySteps) > 0 {
		b.WriteString("\nRecommended verification:\n")
		for _, step := range p.VerifySteps {
			b.WriteString("- `")
			b.WriteString(step)
			b.WriteString("`\n")
		}
	}
	b.WriteString("\nNext step:\n- `/approve writes` to rerun this request with only that exact scope unlocked\n- `/deny writes` to clear the proposal\n- `/writes auto` if you want broader write autonomy")
	return b.String()
}

func (p writeApprovalProposal) approvalNote() string {
	return "Write approval granted for: " + strings.Join(p.Paths, ", ") + ". Re-running the request within that exact guarded scope."
}

func (p writeApprovalProposal) grant() *writeApprovalGrant {
	paths := make(map[string]struct{}, len(p.Paths))
	for _, path := range p.Paths {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		paths[path] = struct{}{}
	}
	return &writeApprovalGrant{
		Tool:  strings.TrimSpace(p.Tool),
		Paths: paths,
	}
}

func (g *implementTargetGuard) PreExecute(req workspace.ToolRequest) *workspace.ToolResult {
	if g == nil {
		return nil
	}
	if blocked := g.blockWriteOutsideEditableTargets(req); blocked != nil {
		return blocked
	}
	if g.targetConsumed || len(g.packet.PrimaryPaths) == 0 {
		return nil
	}
	path := normalizeGuardedPath(req.Path)
	if req.Tool == "read_file" && path != "" && req.ID == "" && req.StartLine == 0 && req.EndLine == 0 {
		if _, ok := g.bootstrappedReads[path]; ok && !g.allowFullTargetRead {
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
	case "search_content", "read_file", "go_find_symbol", "go_read_symbol", "go_find_type", "go_find_const", "go_definition", "go_references", "go_implementations", "go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_related_files":
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
		if !g.pathReadableAllowed(path) {
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

type implementWriteGuard struct {
	mgr           *workspace.Manager
	policy        workspace.WritePolicy
	normalized    string
	shellOnly     bool
	explicitPaths map[string]struct{}
	writePaths    map[string]struct{}
	grant         *writeApprovalGrant
}

type mainWriteGuard struct {
	policy     workspace.WritePolicy
	normalized string
}

func newMainWriteGuard(mgr *workspace.Manager, prompt string) *mainWriteGuard {
	if mgr == nil {
		return nil
	}
	return &mainWriteGuard{
		policy:     mgr.WritePolicy(),
		normalized: strings.ToLower(prompt),
	}
}

func (g *mainWriteGuard) PreExecute(req workspace.ToolRequest) *workspace.ToolResult {
	if g == nil || g.policy == workspace.WritePolicyAuto {
		return nil
	}
	switch req.Tool {
	case "edit_file", "write_file", "append_file", "mkdir", "go_move_declarations", "apply_patch":
		return &workspace.ToolResult{
			Tool:    req.Tool,
			Summary: "Guarded write policy blocked main-lane mutation",
			Error:   "guarded writes keep the main lane read-first. Use an explicit implementation prompt so Qwen routes through the guarded implement lane, or switch to /writes auto for broader mutation.",
		}
	case "shell":
		if isGuardedImplementShellAllowed(req.Command) {
			return nil
		}
		reason := "guarded writes only allow read-only inspection and verification shell commands in the main lane. Use an explicit implementation prompt so Qwen routes through the guarded implement lane, or switch to /writes auto."
		if requestsShellOnlyMutation(g.normalized) {
			reason = "guarded writes do not permit shell-only mutations. Relax the request so Qwen can use file tools safely, or switch to /writes auto."
		}
		return &workspace.ToolResult{
			Tool:    "shell",
			Summary: "Guarded write policy blocked main-lane shell mutation",
			Error:   reason,
		}
	default:
		return nil
	}
}

func newImplementWriteGuard(mgr *workspace.Manager, prompt string) *implementWriteGuard {
	explicitPaths := make(map[string]struct{})
	for _, path := range promptMentionedWorkspacePaths(prompt) {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		explicitPaths[path] = struct{}{}
		explicitPaths[filepath.Base(path)] = struct{}{}
	}
	return &implementWriteGuard{
		mgr:           mgr,
		policy:        mgr.WritePolicy(),
		normalized:    strings.ToLower(prompt),
		shellOnly:     requestsShellOnlyMutation(strings.ToLower(prompt)),
		explicitPaths: explicitPaths,
		writePaths:    make(map[string]struct{}),
	}
}

func (g *implementWriteGuard) PreExecute(req workspace.ToolRequest) *workspace.ToolResult {
	if g == nil || g.policy == workspace.WritePolicyAuto {
		return nil
	}
	path := normalizeGuardedPath(req.Path)
	switch req.Tool {
	case "edit_file":
		if g.shellOnly {
			return &workspace.ToolResult{
				Tool:    "edit_file",
				Summary: "Guarded write policy blocked shell-only edit",
				Error:   "guarded writes do not permit satisfying a shell-only mutation request with file edits. Relax the request so Qwen can use file tools safely, or switch to /writes auto.",
			}
		}
		if blocked := g.guardPathScope(req.Tool, path); blocked != nil {
			return blocked
		}
		return nil
	case "write_file":
		if g.shellOnly {
			return &workspace.ToolResult{
				Tool:    "write_file",
				Summary: "Guarded write policy blocked shell-only file creation",
				Error:   "guarded writes do not permit satisfying a shell-only mutation request with write_file. Relax the request so Qwen can use file tools safely, or switch to /writes auto.",
			}
		}
		if blocked := g.guardPathScope(req.Tool, path); blocked != nil {
			return blocked
		}
		if g.fileExists(path) && !g.explicitRewriteRequested(path) {
			return &workspace.ToolResult{
				Tool:    "write_file",
				Summary: "Guarded write policy blocked rewrite",
				Error:   "guarded writes do not allow write_file on an existing file unless the prompt explicitly requests a full rewrite. Use edit_file for targeted edits or switch to /writes auto.",
			}
		}
		if !g.fileExists(path) && !g.explicitCreateRequested(path) {
			return &workspace.ToolResult{
				Tool:    "write_file",
				Summary: "Guarded write policy blocked file creation",
				Error:   "guarded writes only allow creating a new file when the prompt clearly requests creating that file. Ask explicitly to create it or switch to /writes auto.",
			}
		}
		return nil
	case "append_file":
		if g.shellOnly {
			return &workspace.ToolResult{
				Tool:    "append_file",
				Summary: "Guarded write policy blocked shell-only append",
				Error:   "guarded writes do not permit satisfying a shell-only mutation request with append_file. Relax the request so Qwen can use file tools safely, or switch to /writes auto.",
			}
		}
		if blocked := g.guardPathScope(req.Tool, path); blocked != nil {
			return blocked
		}
		if !g.explicitAppendRequested(path) {
			return &workspace.ToolResult{
				Tool:    "append_file",
				Summary: "Guarded write policy blocked append",
				Error:   "guarded writes only allow append_file when the prompt explicitly asks to append content. Use edit_file for precise changes or switch to /writes auto.",
			}
		}
		return nil
	case "go_move_declarations":
		if g.shellOnly {
			return &workspace.ToolResult{
				Tool:    "go_move_declarations",
				Summary: "Guarded write policy blocked shell-only declaration move",
				Error:   "guarded writes do not permit satisfying a shell-only mutation request with declaration moves. Relax the request so Qwen can use file tools safely, or switch to /writes auto.",
			}
		}
		if blocked := g.guardPathScope(req.Tool, path); blocked != nil {
			return blocked
		}
		destinations := []string{normalizeGuardedPath(req.Destination)}
		for _, move := range req.Moves {
			destinations = append(destinations, normalizeGuardedPath(move.Destination))
		}
		hasDestination := false
		for _, destination := range destinations {
			if destination == "" {
				continue
			}
			hasDestination = true
			if blocked := g.guardPathScope(req.Tool, destination); blocked != nil {
				return blocked
			}
		}
		if !hasDestination {
			return &workspace.ToolResult{
				Tool:    "go_move_declarations",
				Summary: "Guarded write policy blocked declaration move",
				Error:   "go_move_declarations requires a concrete destination.",
			}
		}
		return nil
	case "mkdir":
		if g.shellOnly {
			return &workspace.ToolResult{
				Tool:    "mkdir",
				Summary: "Guarded write policy blocked shell-only directory creation",
				Error:   "guarded writes do not permit satisfying a shell-only mutation request with mkdir. Relax the request so Qwen can use file tools safely, or switch to /writes auto.",
			}
		}
		if blocked := g.guardPathScope(req.Tool, path); blocked != nil {
			return blocked
		}
		if !g.explicitCreateRequested(path) {
			return &workspace.ToolResult{
				Tool:    "mkdir",
				Summary: "Guarded write policy blocked directory creation",
				Error:   "guarded writes only allow mkdir when the prompt explicitly asks to create a directory. Ask explicitly or switch to /writes auto.",
			}
		}
		return nil
	case "apply_patch":
		if g.shellOnly {
			return &workspace.ToolResult{
				Tool:    "apply_patch",
				Summary: "Guarded write policy blocked shell-only patch",
				Error:   "guarded writes do not permit satisfying a shell-only mutation request with apply_patch. Relax the request so Qwen can use file tools safely, or switch to /writes auto.",
			}
		}
		ops, err := workspace.ParsePatch(req.Content)
		if err != nil {
			return &workspace.ToolResult{
				Tool:    "apply_patch",
				Summary: "Guarded write policy blocked unparseable patch",
				Error:   err.Error(),
			}
		}
		for _, opPath := range workspace.PatchOpPaths(ops) {
			if blocked := g.guardPathScope(req.Tool, normalizeGuardedPath(opPath)); blocked != nil {
				return blocked
			}
		}
		for _, op := range ops {
			opPath := normalizeGuardedPath(op.Path)
			switch op.Kind {
			case workspace.PatchOpAdd:
				if !g.explicitCreateRequested(opPath) {
					return &workspace.ToolResult{
						Tool:    "apply_patch",
						Summary: "Guarded write policy blocked file creation in patch",
						Error:   fmt.Sprintf("apply_patch Add File for %s requires the prompt to explicitly ask to create that file, or switch to /writes auto.", opPath),
					}
				}
			case workspace.PatchOpUpdate:
				if op.MoveTo == "" && !g.fileExists(opPath) {
					return &workspace.ToolResult{
						Tool:    "apply_patch",
						Summary: "Guarded write policy blocked update of missing file",
						Error:   fmt.Sprintf("apply_patch Update File for %s found no existing file. Use Add File or correct the path.", opPath),
					}
				}
			}
		}
		return nil
	case "shell":
		if isGuardedImplementShellAllowed(req.Command) {
			return nil
		}
		return &workspace.ToolResult{
			Tool:    "shell",
			Summary: "Guarded write policy blocked shell command",
			Error:   "guarded writes only allow read-only inspection and verification shell commands. Use file tools for edits, or switch to /writes auto for broader shell execution.",
		}
	default:
		return nil
	}
}

func (g *implementWriteGuard) guardPathScope(tool string, path string) *workspace.ToolResult {
	if path == "" {
		return &workspace.ToolResult{
			Tool:    tool,
			Summary: "Guarded write policy blocked request",
			Error:   "guarded writes require a concrete path for file changes.",
		}
	}
	if g.approvalGranted(tool, path) {
		g.writePaths[path] = struct{}{}
		return nil
	}
	if g.isProtectedPath(path) {
		return &workspace.ToolResult{
			Tool:    tool,
			Summary: "Guarded write policy blocked protected path",
			Error:   fmt.Sprintf("%s is a protected path under guarded writes. Approve the scoped proposal, or switch to /writes auto for broader autonomy.", path),
		}
	}
	if _, ok := g.writePaths[path]; ok {
		return nil
	}
	if len(g.writePaths) >= 3 && !g.explicitMultiFileRequested() {
		return &workspace.ToolResult{
			Tool:    tool,
			Summary: "Guarded write policy blocked broad write scope",
			Error:   "guarded writes stop after a small write set. For broader multi-file changes, ask explicitly for a multi-file refactor or switch to /writes auto.",
		}
	}
	g.writePaths[path] = struct{}{}
	return nil
}

func (g *implementWriteGuard) approvalGranted(tool string, path string) bool {
	if g == nil || g.grant == nil {
		return false
	}
	if strings.TrimSpace(g.grant.Tool) != "" && strings.TrimSpace(g.grant.Tool) != strings.TrimSpace(tool) {
		return false
	}
	path = normalizeGuardedPath(path)
	if path == "" {
		return false
	}
	_, ok := g.grant.Paths[path]
	return ok
}

func (g *implementWriteGuard) fileExists(path string) bool {
	if g == nil || g.mgr == nil || path == "" {
		return false
	}
	fullPath := path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(g.mgr.Root(), path)
	}
	info, err := os.Stat(fullPath)
	return err == nil && !info.IsDir()
}

func (g *implementWriteGuard) pathExplicitlyAuthorized(path string) bool {
	path = normalizeGuardedPath(path)
	if path == "" {
		return false
	}
	if _, ok := g.explicitPaths[path]; ok {
		return true
	}
	if _, ok := g.explicitPaths[filepath.Base(path)]; ok {
		return true
	}
	return strings.Contains(g.normalized, strings.ToLower(path))
}

func (g *implementWriteGuard) explicitCreateRequested(path string) bool {
	if g == nil {
		return false
	}
	if !containsAny(g.normalized, "create", "new file", "add file", "add a file", "make a file", "create directory", "new directory", "mkdir") {
		return false
	}
	return path == "" || g.pathExplicitlyAuthorized(path) || !g.fileExists(path)
}

func (g *implementWriteGuard) explicitRewriteRequested(path string) bool {
	if g == nil {
		return false
	}
	if !containsAny(g.normalized, "rewrite", "full rewrite", "rewrite the file", "replace the entire", "replace the whole file", "overwrite") {
		return false
	}
	return path == "" || g.pathExplicitlyAuthorized(path)
}

func (g *implementWriteGuard) explicitAppendRequested(path string) bool {
	if g == nil {
		return false
	}
	if !containsAny(g.normalized, "append", "append to", "add to the end") {
		return false
	}
	return path == "" || g.pathExplicitlyAuthorized(path)
}

func (g *implementWriteGuard) explicitMultiFileRequested() bool {
	if g == nil {
		return false
	}
	return containsAny(g.normalized, "multiple files", "several files", "across files", "multi-file", "broad refactor", "whole repo", "codebase")
}

func (g *implementWriteGuard) isProtectedPath(path string) bool {
	path = normalizeGuardedPath(path)
	switch {
	case path == "", path == ".":
		return false
	case path == "config.json", path == "config.example.json":
		return true
	case strings.HasPrefix(path, "prompts/"), strings.HasPrefix(path, "scripts/"):
		return true
	case strings.HasPrefix(path, ".git/"), strings.HasPrefix(path, "logs/"), strings.HasPrefix(path, "models/"), strings.HasPrefix(path, "bin/"):
		return true
	default:
		return false
	}
}

func normalizeGuardedPath(path string) string {
	path = strings.TrimSpace(strings.TrimPrefix(path, "./"))
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func buildWriteApprovalProposal(ctx context.Context, mgr *workspace.Manager, prompt string, req workspace.ToolRequest, guard *implementWriteGuard, blocked workspace.ToolResult) *writeApprovalProposal {
	path := normalizeGuardedPath(req.Path)
	var proposal *writeApprovalProposal
	switch strings.TrimSpace(blocked.Summary) {
	case "Guarded write policy blocked protected path":
		if path == "" {
			return nil
		}
		proposal = &writeApprovalProposal{
			Prompt: prompt,
			Tool:   req.Tool,
			Kind:   writeApprovalProtectedPath,
			Paths:  []string{path},
			Reason: blocked.Error,
		}
	case "Guarded write policy blocked broad write scope":
		paths := make([]string, 0, len(guard.writePaths)+1)
		seen := make(map[string]struct{}, len(guard.writePaths)+1)
		for existing := range guard.writePaths {
			existing = normalizeGuardedPath(existing)
			if existing == "" {
				continue
			}
			if _, ok := seen[existing]; ok {
				continue
			}
			seen[existing] = struct{}{}
			paths = append(paths, existing)
		}
		if path != "" {
			if _, ok := seen[path]; !ok {
				paths = append(paths, path)
			}
		}
		slices.Sort(paths)
		if len(paths) == 0 {
			return nil
		}
		proposal = &writeApprovalProposal{
			Prompt: prompt,
			Tool:   req.Tool,
			Kind:   writeApprovalBroadScope,
			Paths:  paths,
			Reason: blocked.Error,
		}
	default:
		return nil
	}
	proposal.PlannedEdits = summarizeWriteApprovalEdits(mgr, []workspace.ToolRequest{req})
	proposal.PreviewHunks = previewWriteApprovalEdits(mgr, []workspace.ToolRequest{req})
	enrichWriteApprovalProposal(ctx, mgr, proposal, req)
	return proposal
}

func buildWriteApprovalProposalForPaths(ctx context.Context, mgr *workspace.Manager, prompt string, tool string, paths []string, guard *implementWriteGuard) *writeApprovalProposal {
	if guard == nil || guard.policy == workspace.WritePolicyAuto {
		return nil
	}
	normalizedPaths := uniqueNonEmptyStrings(paths)
	if len(normalizedPaths) == 0 {
		return nil
	}

	protectedPending := make([]string, 0, len(normalizedPaths))
	for _, path := range normalizedPaths {
		path = normalizeGuardedPath(path)
		if path == "" || guard.approvalGranted(tool, path) {
			continue
		}
		if guard.isProtectedPath(path) {
			protectedPending = append(protectedPending, path)
		}
	}
	if len(protectedPending) > 0 {
		reason := fmt.Sprintf("%s are protected paths under guarded writes. Approve the scoped proposal, or switch to /writes auto for broader autonomy.", strings.Join(protectedPending, ", "))
		proposal := &writeApprovalProposal{
			Prompt: prompt,
			Tool:   tool,
			Kind:   writeApprovalProtectedPath,
			Paths:  normalizedPaths,
			Reason: reason,
		}
		enrichWriteApprovalProposal(ctx, mgr, proposal, workspace.ToolRequest{Tool: tool, Path: normalizedPaths[0]})
		return proposal
	}

	union := make(map[string]struct{}, len(guard.writePaths)+len(normalizedPaths))
	for existing := range guard.writePaths {
		existing = normalizeGuardedPath(existing)
		if existing == "" {
			continue
		}
		union[existing] = struct{}{}
	}
	for _, path := range normalizedPaths {
		path = normalizeGuardedPath(path)
		if path == "" || guard.approvalGranted(tool, path) {
			continue
		}
		union[path] = struct{}{}
	}
	if len(union) <= 3 || guard.explicitMultiFileRequested() {
		return nil
	}
	scope := make([]string, 0, len(union))
	for path := range union {
		scope = append(scope, path)
	}
	slices.Sort(scope)
	proposal := &writeApprovalProposal{
		Prompt: prompt,
		Tool:   tool,
		Kind:   writeApprovalBroadScope,
		Paths:  scope,
		Reason: "guarded writes stop after a small write set. Approve this broader exact scope, or switch to /writes auto.",
	}
	enrichWriteApprovalProposal(ctx, mgr, proposal, workspace.ToolRequest{Tool: tool, Path: scope[0]})
	return proposal
}

func buildWriteApprovalProposalForRequests(ctx context.Context, mgr *workspace.Manager, prompt string, guard *implementWriteGuard, reqs []workspace.ToolRequest) *writeApprovalProposal {
	if len(reqs) == 0 {
		return nil
	}
	tool := strings.TrimSpace(reqs[0].Tool)
	paths := make([]string, 0, len(reqs))
	for _, req := range reqs {
		paths = append(paths, normalizeGuardedPath(req.Path))
	}
	proposal := buildWriteApprovalProposalForPaths(ctx, mgr, prompt, tool, paths, guard)
	if proposal == nil {
		return nil
	}
	proposal.PlannedEdits = summarizeWriteApprovalEdits(mgr, reqs)
	proposal.PreviewHunks = previewWriteApprovalEdits(mgr, reqs)
	return proposal
}

func summarizeWriteApprovalEdits(mgr *workspace.Manager, reqs []workspace.ToolRequest) []string {
	if mgr == nil || len(reqs) == 0 {
		return nil
	}
	lines := make([]string, 0, len(reqs))
	for _, req := range reqs {
		path := normalizeGuardedPath(req.Path)
		if path == "" {
			continue
		}
		fullPath, err := preflightResolvePath(mgr, path, req.Workdir)
		if err != nil {
			continue
		}
		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		current := string(content)
		if req.StartLine > 0 && req.EndLine >= req.StartLine {
			spans := preflightLineSpans(current)
			if req.StartLine > len(spans) || req.EndLine > len(spans) {
				continue
			}
			startOffset := spans[req.StartLine-1].start
			endOffset := spans[req.EndLine-1].end
			before := strings.TrimSpace(strings.TrimRight(current[startOffset:endOffset], "\n"))
			after := strings.TrimSpace(strings.TrimRight(req.Content, "\n"))
			lines = append(lines, fmt.Sprintf("%s:%d-%d %q -> %q", path, req.StartLine, req.EndLine, before, after))
			continue
		}
		if strings.TrimSpace(req.OldText) == "" {
			continue
		}
		after := strings.TrimSpace(req.NewText)
		lines = append(lines, fmt.Sprintf("%s replace %q -> %q", path, strings.TrimSpace(req.OldText), after))
	}
	return uniqueNonEmptyStrings(lines)
}

func previewWriteApprovalEdits(mgr *workspace.Manager, reqs []workspace.ToolRequest) []string {
	if mgr == nil || len(reqs) == 0 {
		return nil
	}
	hunks := make([]string, 0, len(reqs))
	for _, req := range reqs {
		path := normalizeGuardedPath(req.Path)
		if path == "" {
			continue
		}
		fullPath, err := preflightResolvePath(mgr, path, req.Workdir)
		if err != nil {
			continue
		}
		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		current := string(content)
		switch {
		case req.StartLine > 0 && req.EndLine >= req.StartLine:
			hunk, ok := previewLineRangeEdit(path, current, req.StartLine, req.EndLine, req.Content)
			if ok {
				hunks = append(hunks, hunk)
			}
		case strings.TrimSpace(req.OldText) != "":
			hunk, ok := previewReplaceEdit(path, current, req.OldText, req.NewText)
			if ok {
				hunks = append(hunks, hunk)
			}
		}
	}
	return uniqueNonEmptyStrings(hunks)
}

func enrichWriteApprovalProposal(ctx context.Context, mgr *workspace.Manager, proposal *writeApprovalProposal, req workspace.ToolRequest) {
	if proposal == nil {
		return
	}
	profile := analyzeWriteApprovalProposal(ctx, mgr, proposal, req)
	proposal.Risk = profile.Risk
	proposal.ChangeKinds = uniqueNonEmptyStrings(append(proposal.ChangeKinds, profile.ChangeKinds...))
	proposal.RiskReasons = uniqueNonEmptyStrings(append(proposal.RiskReasons, profile.RiskReasons...))
	proposal.ImpactSummary = uniqueNonEmptyStrings(append(proposal.ImpactSummary, profile.ImpactSummary...))
	proposal.BlastRadius = uniqueNonEmptyStrings(append(proposal.BlastRadius, profile.BlastRadius...))
	proposal.DeepAnalysis = uniqueNonEmptyStrings(append(proposal.DeepAnalysis, profile.DeepAnalysis...))
	proposal.AffectedTests = uniqueNonEmptyStrings(append(proposal.AffectedTests, profile.AffectedTests...))
	proposal.ApprovalNotes = uniqueNonEmptyStrings(append(proposal.ApprovalNotes, profile.ApprovalNotes...))
	proposal.VerifySteps = uniqueNonEmptyStrings(append(proposal.VerifySteps, profile.VerifySteps...))
	if plan := implementationVerificationPlan(mgr, proposal.Paths); strings.TrimSpace(plan.Confidence) != "" {
		coverage := strings.TrimSpace(plan.Coverage)
		confidence := strings.TrimSpace(plan.Confidence)
		if coverage != "" {
			proposal.VerifyCoverage = confidence + " (" + coverage + ")"
		} else {
			proposal.VerifyCoverage = confidence
		}
		proposal.VerifyRisk = strings.TrimSpace(plan.RiskLevel)
		proposal.VerifyExpected = uniqueNonEmptyStrings(append(proposal.VerifyExpected, plan.ExpectedFailures...))
		proposal.VerifyRepair = strings.TrimSpace(plan.RepairPolicy)
	}
}

func approvalPathRoleLines(paths []string) []string {
	lines := make([]string, 0, len(paths)+2)
	if len(paths) > 1 {
		lines = append(lines, fmt.Sprintf("requested scope spans %d files", len(paths)))
	}
	for _, path := range paths {
		path = normalizeGuardedPath(path)
		if path == "" {
			continue
		}
		switch {
		case path == "config.json" || path == "config.example.json":
			lines = append(lines, fmt.Sprintf("%s controls runtime defaults and launch behavior for future sessions", path))
		case strings.HasPrefix(path, "prompts/"):
			lines = append(lines, fmt.Sprintf("%s changes baseline assistant behavior and can affect every future session", path))
		case strings.HasPrefix(path, "scripts/"):
			lines = append(lines, fmt.Sprintf("%s affects launcher or developer workflow entrypoints", path))
		case strings.HasPrefix(path, "internal/"):
			lines = append(lines, fmt.Sprintf("%s is core application code under guarded scope", path))
		}
	}
	return lines
}

func approvalVerificationCommands(mgr *workspace.Manager, paths []string) []string {
	return implementationVerificationPlan(mgr, paths).Commands
}

func approvalGoImpact(ctx context.Context, mgr *workspace.Manager, idx *semantic.GoIndex, proposal *writeApprovalProposal, req workspace.ToolRequest) ([]string, []string) {
	if idx == nil {
		return nil, nil
	}
	impact := make([]string, 0, len(proposal.Paths)+2)
	blast := make([]string, 0, 8)
	timeoutCtx := ctx
	if timeoutCtx == nil {
		timeoutCtx = context.Background()
	}
	var cancel context.CancelFunc
	timeoutCtx, cancel = context.WithTimeout(timeoutCtx, 3*time.Second)
	defer cancel()

	for _, path := range proposal.Paths {
		path = normalizeGuardedPath(path)
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		file := idx.FileByPath(path)
		if file == nil {
			continue
		}
		impact = append(impact, fmt.Sprintf("%s is in package %s with %d funcs/methods, %d types, and %d imports", path, file.Package, file.Funcs+file.Methods, file.Types, len(file.Imports)))
		if pkgDir := file.PathDir(); pkgDir != "" {
			importers := mgr.Execute(timeoutCtx, workspace.ToolRequest{Tool: "go_importers", Pattern: pkgDir})
			for _, line := range limitedResultLines(importers.Output, 3) {
				blast = append(blast, "importer: "+line)
			}
		}
		for _, related := range limitedRelatedFiles(idx, path, proposal.Prompt, 3) {
			if related == path {
				continue
			}
			blast = append(blast, "related file: "+related)
		}
		if len(blast) == 0 {
			for _, peer := range packagePeerFiles(idx, file.PathDir(), path, 3) {
				blast = append(blast, "package peer: "+peer)
			}
		}
		if symbol, ok := approvalTargetSymbol(timeoutCtx, mgr, idx, path, req); ok {
			impact = append(impact, fmt.Sprintf("target symbol likely falls inside %s (%s lines %d-%d)", symbolDisplayNameForApproval(symbol), path, symbol.StartLine, symbol.EndLine))
			callsInto := mgr.Execute(timeoutCtx, workspace.ToolRequest{Tool: "go_calls_into", Pattern: symbol.Name, Path: path})
			for _, line := range limitedResultLines(callsInto.Output, 2) {
				blast = append(blast, "incoming call: "+line)
			}
			callsFrom := mgr.Execute(timeoutCtx, workspace.ToolRequest{Tool: "go_calls_from", Pattern: symbol.Name, Path: path})
			for _, line := range limitedResultLines(callsFrom.Output, 2) {
				blast = append(blast, "outgoing call: "+line)
			}
		}
	}
	return impact, blast
}

func approvalTargetSymbol(ctx context.Context, mgr *workspace.Manager, idx *semantic.GoIndex, path string, req workspace.ToolRequest) (semantic.GoSymbol, bool) {
	file := idx.FileByPath(path)
	if file == nil {
		return semantic.GoSymbol{}, false
	}
	line := req.StartLine
	if line <= 0 {
		line = req.EndLine
	}
	if line <= 0 && strings.TrimSpace(req.OldText) != "" && mgr != nil {
		search := mgr.Execute(ctx, workspace.ToolRequest{
			Tool:    "search_content",
			Pattern: req.OldText,
			Path:    path,
		})
		if resolved, ok := firstSearchMatchLine(search.Output, path); ok {
			line = resolved
		}
	}
	if line <= 0 {
		return semantic.GoSymbol{}, false
	}
	return enclosingSymbolAtLine(file, line)
}

func approvalPromptTerms(prompt string) []string {
	normalized := strings.ToLower(prompt)
	fields := strings.FieldsFunc(normalized, func(r rune) bool {
		return !(r == '_' || r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z'))
	})
	terms := make([]string, 0, 6)
	for _, field := range fields {
		if len(field) < 3 {
			continue
		}
		switch field {
		case "change", "verify", "write", "using", "shell", "only", "approval", "smoke", "test", "file", "path":
			continue
		}
		terms = append(terms, field)
		if len(terms) >= 6 {
			break
		}
	}
	return terms
}

func symbolDisplayNameForApproval(symbol semantic.GoSymbol) string {
	if strings.TrimSpace(symbol.Receiver) == "" {
		return symbol.Name
	}
	return symbol.Receiver + "." + symbol.Name
}

func approvalRequiredToolResult(proposal writeApprovalProposal) workspace.ToolResult {
	return workspace.ToolResult{
		Tool:    proposal.Tool,
		Summary: "Write approval required",
		Error:   proposal.bannerText(),
	}
}

func isWriteApprovalRequiredResult(result workspace.ToolResult) bool {
	return strings.TrimSpace(result.Summary) == "Write approval required"
}

func isGuardedImplementShellAllowed(command string) bool {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "rg", "grep", "awk", "sed", "cat", "head", "tail", "wc", "file", "pwd", "ls", "find":
		return true
	case "git":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "status", "diff", "show", "grep", "log":
			return true
		default:
			return false
		}
	case "go":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "test", "build", "vet", "fmt", "list":
			return true
		default:
			return false
		}
	case "cargo":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "test", "check", "build", "fmt", "clippy":
			return true
		default:
			return false
		}
	case "pytest":
		return true
	case "make":
		if len(fields) < 2 {
			return false
		}
		return containsAny(strings.ToLower(fields[1]), "test", "build", "lint", "check", "verify")
	case "npm", "pnpm", "yarn":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "test", "lint", "build", "check":
			return true
		case "run":
			if len(fields) < 3 {
				return false
			}
			return containsAny(strings.ToLower(fields[2]), "test", "lint", "build", "check", "verify")
		default:
			return false
		}
	case "uv":
		return strings.Contains(strings.ToLower(command), "test") || strings.Contains(strings.ToLower(command), "pytest")
	default:
		return false
	}
}

func (m *Model) writeApprovalBanner() string {
	if m.pendingWrite == nil {
		return ""
	}
	return m.pendingWrite.bannerText()
}
