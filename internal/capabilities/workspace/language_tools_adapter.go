package workspace

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/commandexec"
	"idiom/internal/platform/failures"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
)

type languageToolHost struct {
	manager *Manager
}

func (h languageToolHost) Enabled() bool {
	return h.manager != nil && h.manager.Enabled()
}

func (h languageToolHost) Root() string {
	if h.manager == nil {
		return ""
	}

	return h.manager.cfg.Root
}

func (h languageToolHost) MaxDirectoryEntries() int {
	if h.manager == nil {
		return 0
	}

	return h.manager.cfg.MaxDirectoryEntries
}

func (h languageToolHost) WritesEnabled() bool {
	return h.manager != nil && h.manager.cfg.EnableWrites
}

func (h languageToolHost) ResolvePath(path string, workdir string) (string, error) {
	if h.manager == nil {
		return "", fmt.Errorf("workspace root is not configured")
	}

	return h.manager.pathAccess.ResolvePath(path, workdir)
}

func (h languageToolHost) DisplayPath(path string) string {
	if h.manager == nil {
		return ""
	}

	return h.manager.pathAccess.DisplayPath(path)
}

// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by languagetools.Host
func (h languageToolHost) HandleDisplay(abs string) string {
	return h.manager.handleDisplay(abs)
}

// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by languagetools.Host
func (h languageToolHost) RememberPath(abs string) {
	h.manager.rememberPath(abs)
}

// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by languagetools.Host
func (h languageToolHost) PrimeDiscoveredPaths(paths []string) {
	h.manager.PrimeDiscoveredPaths(paths)
}

func (h languageToolHost) PathForHandle(ctx context.Context, handle string) (string, error) {
	if h.manager == nil {
		return "", fmt.Errorf("workspace manager is not configured")
	}

	identity, bound := executionSessionIdentityFromContext(ctx)
	if !bound {
		return "", fmt.Errorf("execution session identity is not bound")
	}

	return h.manager.resolveReadHandleTarget(identity, handle)
}

func (h languageToolHost) RouteLanguageTool(ctx context.Context, req languagetools.RouteLanguageToolRequest) languagetools.ToolResult {
	result := h.manager.Execute(ctx, ToolRequest{
		Tool:      req.Tool,
		Path:      req.Path,
		Workdir:   req.Workdir,
		Pattern:   req.Pattern,
		StartLine: req.StartLine,
		EndLine:   req.EndLine,
		MaxDepth:  req.MaxDepth,
		Recursive: req.Recursive,
		Limit:     req.Limit,
	})

	return toLanguageToolResult(result)
}

func (h languageToolHost) SearchContent(ctx context.Context, req languagetools.SearchContentRequest) languagetools.ToolResult {
	result := h.manager.Execute(ctx, ToolRequest{
		Tool:    ToolNameSearchContent,
		Pattern: req.Pattern,
		Path:    req.Path,
		Glob:    req.Glob,
		Workdir: req.Workdir,
	})

	return languagetools.ToolResult{
		Tool:    result.Tool,
		Summary: result.Summary,
		Output:  result.Output,
		Error:   result.Error,
		Failure: result.Failure,
	}
}

// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by languagetools.Host
func (h languageToolHost) RunCommand(ctx context.Context, dir string, extraEnv []string, name string, args ...string) languagetools.CommandResult {
	result := h.manager.runCommandResult(ctx, commandexec.Spec{Mode: commandexec.ModeArgv, Name: name, Args: args, Dir: dir, Env: extraEnv})

	return languagetools.CommandResult{
		Output: h.manager.commandDisplayOutput(result).Text,
		Stdout: h.manager.commandStdoutOutput(result).Text,
		Err:    result.FailureError(""),
	}
}

func (m *Manager) executeLanguageTool(ctx context.Context, req ToolRequest) ToolResult {
	if strings.TrimSpace(req.Tool) == ToolNameGoMoveDeclarations {
		return m.executeGoMoveDeclarations(ctx, req)
	}

	languageTools := m.loadLanguageTools()
	result := languageTools.Dispatch(ctx, toLanguageToolRequest(req))
	converted := fromLanguageToolResult(result)
	if err := m.bindLanguageSourceWindows(ctx, req, result.SourceWindows, &converted); err != nil {
		return ToolResult{Tool: converted.Tool, Summary: "Language source read could not be rooted", Error: err.Error()}
	}
	return converted
}

func (m *Manager) bindLanguageSourceWindows(ctx context.Context, request ToolRequest, plans []languagetools.SourceWindowPlan, result *ToolResult) error {
	if len(plans) == 0 || result == nil || strings.TrimSpace(result.Error) != "" {
		return nil
	}
	mutationEpoch := m.MutationEpoch()
	windows := make([]WorkspaceSourceWindow, 0, len(plans))
	for _, plan := range plans {
		presented := plan.Path
		workdir := ""
		if strings.TrimSpace(request.Path) != "" {
			plannedIdentity, plannedErr := m.CanonicalizeWorkspacePath(plan.Path, "")
			requestedIdentity, requestedErr := m.CanonicalizeWorkspacePath(request.Path, request.Workdir)
			if plannedErr == nil && requestedErr == nil && plannedIdentity.CanonicalID == requestedIdentity.CanonicalID {
				presented = request.Path
				workdir = request.Workdir
			}
		}
		snapshot, err := m.SnapshotWorkspace(ctx, WorkspaceSnapshotRequest{
			Purpose: WorkspaceSnapshotSourceWindow,
			Paths:   []string{presented},
			Workdir: workdir,
		})
		if err != nil {
			return err
		}
		if len(snapshot.Entries) != 1 || snapshot.Entries[0].Kind != WorkspaceSnapshotFile {
			return fmt.Errorf("language source path %q is not a regular workspace file", plan.Path)
		}
		authority := toolfacts.SourceWindowAuthorityEvidence{
			PolicySubject:  snapshot.Entries[0].Metadata.PolicySubject,
			PolicyDecision: snapshot.Entries[0].Metadata.PolicyDecision,
		}
		window, err := workspaceSourceWindow(snapshot.Entries[0].Identity, authority, snapshot.Entries[0].Body(), plan.StartLine, plan.EndLine, plan.Truncated, mutationEpoch)
		if err != nil {
			return err
		}
		windows = append(windows, window)
	}
	if mutationEpoch != m.MutationEpoch() {
		return fmt.Errorf("workspace mutation generation changed while binding language source evidence")
	}
	attachWorkspaceSourceWindows(result, windows...)
	return nil
}

func (m *Manager) loadLanguageTools() *languagetools.Manager {
	// A dispatch owns the generation it loads. Concurrent workspace mutations
	// publish a fresh generation for later calls without interrupting this one.
	languageTools := m.languageTools.Load()
	if languageTools == nil {
		candidate := languagetools.NewManager(languageToolHost{manager: m})
		if m.languageTools.CompareAndSwap(nil, candidate) {
			languageTools = candidate
		} else {
			languageTools = m.languageTools.Load()
		}
	}

	return languageTools
}

type plannedGoMoveMutation struct {
	plan     languagetools.FileMutationPlan
	target   workspaceMutationTarget
	preimage workspaceMutationPreimage
}

func (m *Manager) executeGoMoveDeclarations(ctx context.Context, req ToolRequest) ToolResult {
	if !m.cfg.EnableWrites {
		return toolWriteDisabledResult(ToolNameGoMoveDeclarations, "Go declaration move rejected", "workspace writes are disabled")
	}

	mutation, err := m.pathAccess.beginWorkspaceMutation(ctx, req)
	if err != nil {
		return toolFailureFromErrorResult(ToolNameGoMoveDeclarations, "Failed to open Go declaration move transaction", err, failures.PermissionDenied, reasonToolPermissionPolicy)
	}
	defer mutation.Close()

	analysisTargets := mutation.authorization.orderedTargets
	rootedTargets := make(map[string]workspaceMutationTarget, len(analysisTargets))
	preimages := make(map[string]workspaceMutationPreimage, len(analysisTargets))
	languagePreimages := make([]languagetools.FileMutationPreimage, 0, len(analysisTargets))
	canonicalByRequested := make(map[string]string, len(analysisTargets))
	for _, analyzed := range analysisTargets {
		target, resolveErr := mutation.Resolve(analyzed.RequestedPath)
		if resolveErr != nil {
			return toolFailureFromErrorResult(ToolNameGoMoveDeclarations, "Failed to resolve Go declaration move target", resolveErr, failures.InvalidArgument, reasonToolPathResolutionFailed)
		}
		canonicalByRequested[mutationRequestedPathKey(analyzed.RequestedPath)] = target.absolute
		if _, seen := rootedTargets[target.canonicalID]; seen {
			continue
		}
		preimage, observeErr := mutation.Observe(target)
		if observeErr != nil {
			return toolFailureFromErrorResult(ToolNameGoMoveDeclarations, "Failed to capture Go declaration move preimage", observeErr, failures.FailedPrecondition, reasonPlanSubmissionRejected)
		}
		if analyzed.Preimage == MutationPreimageRequired && !preimage.exists {
			return toolNotFoundResult(ToolNameGoMoveDeclarations, "Go declaration move rejected", fmt.Sprintf("required source %s does not exist", target.display))
		}
		rootedTargets[target.canonicalID] = target
		preimages[target.canonicalID] = preimage
		languagePreimages = append(languagePreimages, languagetools.FileMutationPreimage{
			Path:   target.absolute,
			Exists: preimage.exists,
			Body:   append([]byte(nil), preimage.body...),
		})
	}

	plannerRequest := req
	plannerRequest.Path = canonicalByRequested[mutationRequestedPathKey(req.Path)]
	plannerRequest.Destination = canonicalByRequested[mutationRequestedPathKey(req.Destination)]
	plannerRequest.Moves = append([]GoDeclarationMove(nil), req.Moves...)
	for index := range plannerRequest.Moves {
		plannerRequest.Moves[index].Declarations = append([]string(nil), req.Moves[index].Declarations...)
		plannerRequest.Moves[index].Destination = canonicalByRequested[mutationRequestedPathKey(req.Moves[index].Destination)]
	}
	languageRequest := toLanguageToolRequest(plannerRequest)
	languageRequest.MutationPreimages = languagePreimages
	plannedResult := m.loadLanguageTools().Dispatch(ctx, languageRequest)
	if strings.TrimSpace(plannedResult.Error) != "" || plannedResult.MutationPlan == nil {
		return fromLanguageToolResult(plannedResult)
	}

	planned := make([]plannedGoMoveMutation, 0, len(plannedResult.MutationPlan.Files))
	for _, filePlan := range plannedResult.MutationPlan.Files {
		target, resolveErr := mutation.Resolve(filePlan.Path)
		if resolveErr != nil {
			return toolFailureFromErrorResult(ToolNameGoMoveDeclarations, "Go declaration move plan rejected", resolveErr, failures.FailedPrecondition, reasonPlanSubmissionRejected)
		}
		preimage, ok := preimages[target.canonicalID]
		if !ok || preimage.exists != filePlan.ExpectedExists || !bytes.Equal(preimage.body, filePlan.ExpectedBody) {
			return toolFailureResult(ToolNameGoMoveDeclarations, "Go declaration move plan rejected", fmt.Sprintf("planner preimage for %s does not match the rooted workspace snapshot", target.display), failures.FailedPrecondition, reasonPlanSubmissionRejected)
		}
		if rejected := m.writeGuard.RejectPlannedWrite(ToolNameGoMoveDeclarations, "Go declaration move rejected", target.absolute, preimage.body, filePlan.RenderedBody, m.pathAccess.DisplayPath); rejected != nil {
			return *rejected
		}
		planned = append(planned, plannedGoMoveMutation{plan: filePlan, target: target, preimage: preimage})
	}

	for index, item := range planned {
		mutation.reachCheckpoint(workspaceMutationAfterPreflight, item.target, index+1)
	}
	for failedIndex, item := range planned {
		if validateErr := mutation.ValidatePreimage(item.target, item.preimage); validateErr != nil {
			unattempted := make([]workspaceMutationTarget, 0, len(planned)-1)
			for index, candidate := range planned {
				if index != failedIndex {
					unattempted = append(unattempted, candidate.target)
				}
			}
			mutation.setFailureIntent(item.target, unattempted)
			return m.workspaceMutationFailureResult(
				ToolNameGoMoveDeclarations,
				"Go declaration move preflight changed",
				"Go declaration move partially applied",
				validateErr,
				mutation,
				failures.FailedPrecondition,
				reasonPlanSubmissionRejected,
				nil,
			)
		}
	}

	for index, item := range planned {
		if commitErr := mutation.Replace(item.target, item.preimage, item.plan.RenderedBody, 0o644, index+1); commitErr != nil {
			remainingTargets := make([]workspaceMutationTarget, 0, len(planned)-index-1)
			for _, unattempted := range planned[index+1:] {
				remainingTargets = append(remainingTargets, unattempted.target)
			}
			mutation.setFailureIntent(item.target, remainingTargets)
			remaining := make([]string, 0, len(planned)-index-1)
			for _, unattempted := range planned[index+1:] {
				remaining = append(remaining, unattempted.plan.Description)
			}
			return m.workspaceMutationFailureResult(
				ToolNameGoMoveDeclarations,
				"Go declaration move commit failed",
				"Go declaration move partially applied",
				fmt.Errorf("%s: %w", item.plan.Description, commitErr),
				mutation,
				failures.Internal,
				reasonToolWriteFailed,
				remaining,
			)
		}
	}

	result := fromLanguageToolResult(plannedResult)
	result.Output = strings.Replace(result.Output, "planned files:", "committed files:", 1)
	mutation.attachCommitReceipt(ToolNameGoMoveDeclarations, &result)
	if len(result.ChangedPaths) > 0 {
		m.resetLanguageTools()
	}
	return result
}

func mutationRequestedPathKey(path string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
}

func (m *Manager) resetLanguageTools() {
	// Publishing a new empty-cache generation makes completed mutations visible
	// without serializing unrelated in-flight language queries.
	m.languageTools.Store(languagetools.NewManager(languageToolHost{manager: m}))
}

func (m *Manager) languageDetects(name string) bool {
	if !m.Enabled() {
		return false
	}

	lang, ok := semanticlanguage.LanguageForName(name)

	return ok && lang.Detect != nil && lang.Detect(m.cfg.Root)
}

func toLanguageToolRequest(req ToolRequest) languagetools.ToolRequest {
	moves := make([]languagetools.GoDeclarationMove, 0, len(req.Moves))
	for _, move := range req.Moves {
		moves = append(moves, languagetools.GoDeclarationMove{
			Destination:  move.Destination,
			Pattern:      move.Pattern,
			Declarations: append([]string(nil), move.Declarations...),
		})
	}

	return languagetools.ToolRequest{
		Tool:         req.Tool,
		ID:           req.ID,
		Path:         req.Path,
		Workdir:      req.Workdir,
		StartLine:    req.StartLine,
		EndLine:      req.EndLine,
		Pattern:      req.Pattern,
		Capability:   req.Capability,
		Language:     req.Language,
		Resolution:   req.Resolution,
		Glob:         req.Glob,
		MaxDepth:     req.MaxDepth,
		Limit:        req.Limit,
		Recursive:    req.Recursive,
		Destination:  req.Destination,
		Declarations: append([]string(nil), req.Declarations...),
		Moves:        moves,
	}
}

func fromLanguageToolResult(result languagetools.ToolResult) ToolResult {
	converted := ToolResult{
		Tool:          result.Tool,
		Summary:       result.Summary,
		Output:        result.Output,
		Error:         result.Error,
		Failure:       result.Failure,
		Paths:         append([]string(nil), result.Paths...),
		ChangedPaths:  append([]string(nil), result.ChangedPaths...),
		Facts:         result.Facts.Clone(),
		PrecisionTier: result.PrecisionTier,
		Relationships: fromLanguageToolRelationships(result.Relationships),
	}
	if result.MutationPlan != nil {
		converted.ChangedPaths = nil
	}
	return converted
}

func toLanguageToolResult(result ToolResult) languagetools.ToolResult {
	plans := make([]languagetools.SourceWindowPlan, 0, len(result.sourceWindows))
	for _, window := range result.sourceWindows {
		plans = append(plans, languagetools.SourceWindowPlan{
			Path: window.Evidence.CanonicalID, StartLine: window.Evidence.StartLine, EndLine: window.Evidence.EndLine, Truncated: window.Evidence.Truncated,
		})
	}
	return languagetools.ToolResult{
		Tool:          result.Tool,
		Summary:       result.Summary,
		Output:        result.Output,
		Error:         result.Error,
		Failure:       result.Failure,
		Paths:         append([]string(nil), result.Paths...),
		ChangedPaths:  append([]string(nil), result.ChangedPaths...),
		Facts:         result.Facts.Clone(),
		SourceWindows: plans,
		PrecisionTier: result.PrecisionTier,
		Relationships: toLanguageToolRelationships(result.Relationships),
	}
}

func fromLanguageToolRelationships(in []languagetools.ToolRelationship) []ToolRelationship {
	out := make([]ToolRelationship, 0, len(in))
	for _, rel := range in {
		out = append(out, ToolRelationship{
			Kind:         rel.Kind,
			SourcePath:   rel.SourcePath,
			SourceLine:   rel.SourceLine,
			SourceSymbol: rel.SourceSymbol,
			TargetPath:   rel.TargetPath,
			TargetLine:   rel.TargetLine,
			TargetSymbol: rel.TargetSymbol,
			Evidence:     rel.Evidence,
		})
	}

	return out
}

func toLanguageToolRelationships(in []ToolRelationship) []languagetools.ToolRelationship {
	out := make([]languagetools.ToolRelationship, 0, len(in))
	for _, rel := range in {
		out = append(out, languagetools.ToolRelationship{
			Kind:         rel.Kind,
			SourcePath:   rel.SourcePath,
			SourceLine:   rel.SourceLine,
			SourceSymbol: rel.SourceSymbol,
			TargetPath:   rel.TargetPath,
			TargetLine:   rel.TargetLine,
			TargetSymbol: rel.TargetSymbol,
			Evidence:     rel.Evidence,
		})
	}

	return out
}
