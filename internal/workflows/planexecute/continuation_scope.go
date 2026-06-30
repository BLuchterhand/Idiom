package planexecute

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
)

type ContinuationScope struct {
	SessionID          string
	StatePath          string
	PlanPath           string
	RequiredEditPaths  []string
	AuthorizedNewFiles []string
}

type ContinuationScopeRequest struct {
	Prompt    string
	SessionID string
	PlanPath  string
}

func LoadApprovedContinuationScope(root string, request ContinuationScopeRequest, deps Dependencies) (ContinuationScope, bool, error) {
	continuation, found, err := loadExecutorContinuation(root, request.SessionID, continuationScopeRequestMatches(root, request), deps)
	if err != nil || !found {
		return ContinuationScope{}, false, err
	}

	return ContinuationScope{
		SessionID:          strings.TrimSpace(continuation.State.SessionID),
		StatePath:          continuation.StatePath,
		PlanPath:           continuation.PlanPath,
		RequiredEditPaths:  append([]string(nil), continuation.RequiredEditPaths...),
		AuthorizedNewFiles: append([]string(nil), continuation.AuthorizedNewFiles...),
	}, true, nil
}

func continuationScopeRequestMatches(root string, request ContinuationScopeRequest) func(ProgressState) bool {
	prompt := strings.TrimSpace(request.Prompt)
	planPath := strings.TrimSpace(request.PlanPath)
	sessionID := strings.TrimSpace(request.SessionID)

	if prompt == "" && planPath == "" {
		if sessionID == "" {
			return func(ProgressState) bool { return false }
		}
		return nil
	}

	return func(state ProgressState) bool {
		if prompt != "" {
			if strings.TrimSpace(state.PromptSHA256) == "" || !state.PromptMatches(prompt) {
				return false
			}
		}
		if planPath != "" && !sameContinuationPlanPath(root, state, planPath) {
			return false
		}

		return true
	}
}

func sameContinuationPlanPath(root string, state ProgressState, requestedPlanPath string) bool {
	statePlanPath := strings.TrimSpace(state.ResolvedPlanPath(root))
	requestedPlanPath = strings.TrimSpace(requestedPlanPath)
	if statePlanPath == "" || requestedPlanPath == "" {
		return false
	}

	if !filepath.IsAbs(requestedPlanPath) {
		requestedPlanPath = filepath.Join(root, filepath.FromSlash(requestedPlanPath))
	}

	stateAbs, stateErr := filepath.Abs(statePlanPath)
	requestedAbs, requestedErr := filepath.Abs(requestedPlanPath)
	if stateErr == nil && requestedErr == nil {
		return filepath.Clean(stateAbs) == filepath.Clean(requestedAbs)
	}

	return filepath.Clean(statePlanPath) == filepath.Clean(requestedPlanPath)
}

func continuationScopeRequiredEditPaths(state ProgressState, plan planartifact.PlanArtifact) []string {
	return mergePlanExecutePaths(state.RequiredEditPaths, state.ChangedPaths, planChangedPaths(plan))
}

func findLatestContinuationProgressState(root string, matches func(ProgressState) bool) (ProgressState, string, bool, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return ProgressState{}, "", false, nil
	}

	dir := filepath.Join(root, ".idiom", "plan-execute", "sessions")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return ProgressState{}, "", false, nil
	}
	if err != nil {
		return ProgressState{}, "", false, err
	}

	type candidate struct {
		sessionID string
		mtime     int64
	}

	var candidates []candidate
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".terminal.json") {
			continue
		}

		sessionID := strings.TrimSuffix(name, ".json")
		if !validProgressSessionID(sessionID) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		candidates = append(candidates, candidate{sessionID: sessionID, mtime: info.ModTime().UnixNano()})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].mtime == candidates[j].mtime {
			return candidates[i].sessionID > candidates[j].sessionID
		}

		return candidates[i].mtime > candidates[j].mtime
	})

	for _, candidate := range candidates {
		state, statePath, found, err := TryLoadProgressState(root, candidate.sessionID)
		if err != nil {
			return ProgressState{}, "", false, err
		}
		if !found || !continuationProgressStateEligible(state) {
			continue
		}
		if matches != nil && !matches(state) {
			continue
		}

		return state, statePath, true, nil
	}

	return ProgressState{}, "", false, nil
}

func continuationProgressStateEligible(state ProgressState) bool {
	if strings.TrimSpace(state.PlanPath) == "" {
		return false
	}
	if state.Phase != plancontract.PlanExecuteProgressPhaseExecutor {
		return false
	}

	return state.Status == plancontract.PlanExecuteProgressStatusFailed ||
		state.Status == plancontract.PlanExecuteProgressStatusBlocked ||
		state.Status == plancontract.PlanExecuteProgressStatusDegraded
}
