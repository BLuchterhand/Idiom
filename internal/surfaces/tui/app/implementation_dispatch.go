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

	cmdRun := commandexec.Run(ctx, commandexec.Spec{
		Mode:    commandexec.ModeArgv,
		Name:    binary,
		Args:    append([]string(nil), args...),
		Dir:     root,
		Timeout: 30 * time.Minute,
	})

	result.ExitCode = cmdRun.ExitCode
	result.Stdout = cmdRun.Stdout
	result.Stderr = cmdRun.Stderr
	result.Err = cmdRun.Err
	if ctx.Err() != nil {
		result.ExitCode = 124
		result.Err = ctx.Err()
	} else if cmdRun.TimedOut {
		result.ExitCode = 124
		result.Err = context.DeadlineExceeded
	} else if cmdRun.Err != nil && !cmdRun.ExitError {
		result.ExitCode = 2
	}

	if cmdRun.ExitCode != 0 {
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

func implementationDispatchFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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
