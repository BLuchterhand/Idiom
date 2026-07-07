package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/platform/commandexec"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/implementationdispatch"
)

var planDocumentPreflightRunner implementationdispatch.PlanDocumentPreflightRunner = implementationdispatch.PreflightPlanDocument
var planDocumentRefineRunner = defaultImplementationDispatchPlanDocumentRefine

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

func composeImplementationBriefWithEditScopeAndPlanDocuments(prompt string, plan promptExecutionPlan, cfg config.Config, requiredPaths []string, allowedPaths []string, planDocuments []implementationdispatch.PlanDocument) string {
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

func defaultImplementationDispatchPlanDocumentRefine(ctx context.Context, cfg config.Config, doc implementationdispatch.PlanDocument) implementationdispatch.PlanDocumentRefineResult {
	result := implementationdispatch.PlanDocumentRefineResult{OriginalPath: doc.Path}
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

func materializeImplementationDispatchNonzeroRefineAcceptedCandidate(ctx context.Context, cfg config.Config, acceptedRel string, allowDirtyAtomScope ...bool) (implementationdispatch.PlanDocument, string, []implementationdispatch.PlanDocumentPreflightResult, bool) {
	root := implementationdispatch.WorkspaceRoot(cfg.Workspace.Root, cfg.Workspace.CWD)
	if strings.TrimSpace(root) == "" {
		return implementationdispatch.PlanDocument{}, "", nil, false
	}

	acceptedAbs, ok := implementationdispatch.WorkspaceFile(root, acceptedRel)
	if !ok {
		return implementationdispatch.PlanDocument{}, "", nil, false
	}

	body, err := os.ReadFile(acceptedAbs)
	if err != nil {
		return implementationdispatch.PlanDocument{}, "", nil, false
	}

	sanitized, changed := implementationdispatch.SanitizeNonzeroRefineAcceptedContent(string(body))
	if !changed {
		return implementationdispatch.PlanDocument{}, "", nil, false
	}

	candidateRel := strings.TrimSuffix(acceptedRel, filepath.Ext(acceptedRel)) + ".dispatch" + filepath.Ext(acceptedRel)
	candidateAbs, ok := implementationdispatch.WorkspaceFile(root, candidateRel)

	if !ok {
		return implementationdispatch.PlanDocument{}, "", nil, false
	}

	if err := os.WriteFile(candidateAbs, []byte(sanitized), 0o644); err != nil {
		return implementationdispatch.PlanDocument{}, "", nil, false
	}

	candidate, ok := implementationdispatch.ReadPlanDocument(root, candidateRel)
	if !ok {
		return implementationdispatch.PlanDocument{}, "", nil, false
	}

	candidate.AllowDirtyAtomScope = len(allowDirtyAtomScope) > 0 && allowDirtyAtomScope[0]
	if blocked := implementationdispatch.PreflightPlanDocuments(ctx, implementationdispatch.PlanDocumentsPreflightInput{
		WorkspaceRoot: cfg.Workspace.Root,
		WorkspaceCWD:  cfg.Workspace.CWD,
		PlanDocuments: []implementationdispatch.PlanDocument{candidate},
		Runner:        planDocumentPreflightRunner,
	}); len(blocked) > 0 {
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

type implementationDispatchCommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error
}

func runImplementationDispatchCommand(ctx context.Context, root string, timeout time.Duration, binary string, args ...string) implementationDispatchCommandResult {
	if timeout <= 0 {
		timeout = 45 * time.Second
	}

	execResult := commandexec.Run(ctx, commandexec.Spec{
		Mode:    commandexec.ModeArgv,
		Name:    binary,
		Args:    args,
		Dir:     root,
		Timeout: timeout,
	})
	result := implementationDispatchCommandResult{
		ExitCode: execResult.ExitCode,
		Stdout:   execResult.Stdout,
		Stderr:   execResult.Stderr,
		Err:      execResult.Err,
	}

	if execResult.Err == nil {
		return result
	}

	if ctx != nil && ctx.Err() != nil {
		result.ExitCode = 124
		result.Err = ctx.Err()
		return result
	}

	if execResult.TimedOut {
		result.ExitCode = 124
		result.Err = context.DeadlineExceeded
		return result
	}

	if execResult.ExitError {
		return result
	}

	result.ExitCode = 2
	return result
}

func implementationDispatchFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func implementationDispatchEditScopeFromPlanDocuments(prompt string, plan promptExecutionPlan, cfg config.Config, planDocuments []implementationdispatch.PlanDocument) ([]string, []string) {
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

func implementationDispatchEditScopePlanDocumentsAndResolution(prompt string, plan promptExecutionPlan, cfg config.Config) ([]string, []string, []implementationdispatch.PlanDocument, []implementationdispatch.ResolutionBlocker) {
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
