package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/implementationdispatch"
	"idiom/internal/workflows/recovery"
)

type implementationDispatchPlanDocument = implementationdispatch.PlanDocument
type implementationDispatchPlanDocumentPreflightResult = implementationdispatch.PlanDocumentPreflightResult
type implementationDispatchPlanDocumentRefineResult = implementationdispatch.PlanDocumentRefineResult
type implementationDispatchPlanDocumentResolutionBlocker = implementationdispatch.ResolutionBlocker
type implementationDispatchPreflightCommandResult = implementationdispatch.PreflightCommandResult

type implementationDispatchRecoveryDecision = recovery.PlanExecuteRecoveryDecision

var implementationDispatchPlanDocumentPreflightRunner = defaultImplementationDispatchPlanDocumentPreflight
var implementationDispatchPlanDocumentRefineRunner = defaultImplementationDispatchPlanDocumentRefine

func isPlanExecuteInternalFamily(family string) bool {
	return workerruntime.TaskFamilyIsPlanThenExecutePlanner(family) || workerruntime.TaskFamilyIsPlanThenExecuteReview(family)
}

func implementationDispatchHasPlanExecuteHandoff(prompt string, plan promptExecutionPlan, cfg config.Config) bool {
	requiredPaths, allowedPaths, planDocuments, blockers := implementationDispatchEditScopePlanDocumentsAndResolution(prompt, plan, cfg)

	return len(requiredPaths) > 0 ||
		len(allowedPaths) > 0 ||
		len(planDocuments) > 0 ||
		len(blockers) > 0
}

func composeImplementationBriefWithEditScopeAndPlanDocuments(prompt string, plan promptExecutionPlan, cfg config.Config, requiredPaths []string, allowedPaths []string, planDocuments []implementationDispatchPlanDocument) string {
	brief := plan.runtimeBrief
	snippets := make([]implementationdispatch.RequiredEditSnippet, 0, len(brief.RequiredEditSnippets))

	for _, snippet := range brief.RequiredEditSnippets {
		snippets = append(snippets, implementationdispatch.RequiredEditSnippet{
			Path:    snippet.Path,
			Content: snippet.Content,
		})
	}

	return implementationdispatch.ComposeBrief(implementationdispatch.BriefInput{
		Objective:            prompt,
		RequiredPaths:        requiredPaths,
		AllowedPaths:         allowedPaths,
		PlanDocuments:        planDocuments,
		EvidencePaths:        brief.EvidencePaths,
		TargetRefs:           brief.TargetRefs,
		RequiredEditSnippets: snippets,
		VerificationPlan:     brief.VerificationPlan,
		WorkspaceRoot:        cfg.Workspace.Root,
	})
}

func preflightImplementationDispatchPlanDocuments(ctx context.Context, cfg config.Config, planDocuments []implementationDispatchPlanDocument) []implementationDispatchPlanDocumentPreflightResult {
	return implementationdispatch.PreflightPlanDocuments(ctx, planDocuments, func(ctx context.Context, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentPreflightResult {
		return implementationDispatchPlanDocumentPreflightRunner(ctx, cfg, doc)
	})
}

func defaultImplementationDispatchPlanDocumentPreflight(ctx context.Context, cfg config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentPreflightResult {
	result := implementationDispatchPlanDocumentPreflightResult{Path: doc.Path, OK: true}
	root := implementationdispatch.WorkspaceRoot(cfg.Workspace.Root, cfg.Workspace.CWD)

	if strings.TrimSpace(root) == "" {
		result.OK = false
		result.Blockers = append(result.Blockers, "workspace root is unavailable; cannot run dispatch preflight")

		return result
	}

	abs, ok := implementationdispatch.WorkspaceFile(root, doc.Path)
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

	atomArgs := []string{"atom", "intake", "--spec", abs, "--project-root", root, "--json"}
	if doc.AllowDirtyAtomScope {
		atomArgs = append(atomArgs, "--allow-dirty")
	}

	atom := runImplementationDispatchPreflightCommand(ctx, root, binary, atomArgs...)
	if atom.ExitCode != 0 {
		atomState, blockers := implementationdispatch.AtomIntakeBlocker(atom)

		result.AtomState = atomState
		result.Blockers = append(result.Blockers, blockers...)

		if implementationdispatch.AtomStateIsSourceSatisfied(atomState) {
			result.OK = false
			return result
		}
	}

	lint := runImplementationDispatchPreflightCommand(ctx, root, binary, "brief", "lint", abs, "--json")
	if lint.ExitCode != 0 {
		blockers := implementationdispatch.BriefLintBlockers(lint)

		result.LintBlockers = append(result.LintBlockers, blockers...)
		result.Blockers = append(result.Blockers, blockers...)
	} else {
		result.Warnings = append(result.Warnings, implementationdispatch.BriefLintWarnings(lint)...)
	}

	result.OK = len(result.Blockers) == 0

	return result
}

func defaultImplementationDispatchPlanDocumentRefine(ctx context.Context, cfg config.Config, doc implementationDispatchPlanDocument) implementationDispatchPlanDocumentRefineResult {
	result := implementationDispatchPlanDocumentRefineResult{OriginalPath: doc.Path}
	root := implementationdispatch.WorkspaceRoot(cfg.Workspace.Root, cfg.Workspace.CWD)

	if strings.TrimSpace(root) == "" {
		result.Err = fmt.Errorf("workspace root is unavailable")
		result.ExitCode = 2

		return result
	}

	absSpec, ok := implementationdispatch.WorkspaceFile(root, doc.Path)
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

	title := implementationdispatch.DocumentOutputTitle(doc.Path)
	outRel := filepath.Join(".idiom", "refine", title, "accepted.md")
	outAbs, ok := implementationdispatch.WorkspaceFile(root, outRel)

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
		candidate, candidatePath, blockers, ok := materializeImplementationDispatchNonzeroRefineAcceptedCandidate(ctx, cfg, outRel, doc.AllowDirtyAtomScope)
		if strings.TrimSpace(candidatePath) != "" {
			result.OutputPath = candidatePath
			result.Document = candidate
		}

		if len(blockers) > 0 {
			result.PostPreflightBlockers = blockers
		}

		if ok {
			result.OK = true
			return result
		}

		if strings.TrimSpace(result.Document.Path) == "" {
			if refined, ok := implementationdispatch.ReadPlanDocument(root, outRel); ok {
				refined.AllowDirtyAtomScope = doc.AllowDirtyAtomScope
				result.Document = refined
			}
		}

		if strings.TrimSpace(result.OutputPath) == "" {
			result.OutputPath = outRel
		}

		return result
	}

	refined, ok := implementationdispatch.ReadPlanDocument(root, outRel)
	if !ok {
		result.ExitCode = 1
		result.Err = fmt.Errorf("refine completed but output brief was not readable: %s", outRel)

		return result
	}

	refined.AllowDirtyAtomScope = doc.AllowDirtyAtomScope
	result.Document = refined
	result.OK = true

	return result
}

func materializeImplementationDispatchNonzeroRefineAcceptedCandidate(ctx context.Context, cfg config.Config, acceptedRel string, allowDirtyAtomScope ...bool) (implementationDispatchPlanDocument, string, []implementationDispatchPlanDocumentPreflightResult, bool) {
	root := implementationdispatch.WorkspaceRoot(cfg.Workspace.Root, cfg.Workspace.CWD)
	if strings.TrimSpace(root) == "" {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	acceptedAbs, ok := implementationdispatch.WorkspaceFile(root, acceptedRel)
	if !ok {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	body, err := os.ReadFile(acceptedAbs)
	if err != nil {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	sanitized, changed := implementationdispatch.SanitizeNonzeroRefineAcceptedContent(string(body))
	if !changed {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	candidateRel := strings.TrimSuffix(acceptedRel, filepath.Ext(acceptedRel)) + ".dispatch" + filepath.Ext(acceptedRel)
	candidateAbs, ok := implementationdispatch.WorkspaceFile(root, candidateRel)

	if !ok {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	if err := os.WriteFile(candidateAbs, []byte(sanitized), 0o644); err != nil {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	candidate, ok := implementationdispatch.ReadPlanDocument(root, candidateRel)
	if !ok {
		return implementationDispatchPlanDocument{}, "", nil, false
	}

	candidate.AllowDirtyAtomScope = len(allowDirtyAtomScope) > 0 && allowDirtyAtomScope[0]
	if blocked := preflightImplementationDispatchPlanDocuments(ctx, cfg, []implementationDispatchPlanDocument{candidate}); len(blocked) > 0 {
		return candidate, candidateRel, blocked, false
	}

	return candidate, candidateRel, nil, true
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

func implementationDispatchEditScopeFromPlanDocuments(prompt string, plan promptExecutionPlan, cfg config.Config, planDocuments []implementationDispatchPlanDocument) ([]string, []string) {
	intake := implementationDispatchCompilePlanExecuteScopeIntake(prompt, plan, cfg)
	requiredPaths := appendUniqueBriefStrings(nil, intake.RequiredTargets...)

	allowedPaths := appendUniqueBriefStrings(nil, intake.AllowedTargets...)
	root := implementationdispatch.WorkspaceRoot(cfg.Workspace.Root, cfg.Workspace.CWD)

	for _, doc := range planDocuments {
		if implementationdispatch.PlanDocumentScopeIsRequired(doc, root) {
			requiredPaths = appendUniqueBriefStrings(requiredPaths, doc.EditPaths...)
			continue
		}

		allowedPaths = appendUniqueBriefStrings(allowedPaths, doc.EditPaths...)
	}

	return requiredPaths, allowedPaths
}

func implementationDispatchEditScopePlanDocumentsAndResolution(prompt string, plan promptExecutionPlan, cfg config.Config) ([]string, []string, []implementationDispatchPlanDocument, []implementationDispatchPlanDocumentResolutionBlocker) {
	intake := implementationDispatchCompilePlanExecuteScopeIntake(prompt, plan, cfg)
	root := implementationdispatch.WorkspaceRoot(cfg.Workspace.Root, cfg.Workspace.CWD)

	resolution := implementationdispatch.ResolveScope(implementationdispatch.ScopeInput{
		Prompt:          prompt,
		WorkspaceRoot:   root,
		TargetRefs:      plan.runtimeBrief.TargetRefs,
		RequiredTargets: intake.RequiredTargets,
		AllowedTargets:  intake.AllowedTargets,
		MutationCapable: workerruntime.TaskFamilyMutationCapable(plan.route.Family),
		RouteReason:     plan.route.Reason,
	})

	return resolution.RequiredPaths, resolution.AllowedPaths, resolution.PlanDocuments, resolution.Blockers
}
