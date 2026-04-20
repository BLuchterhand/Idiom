package app

import (
	"qwen-bubbles/internal/frontier"
	"qwen-bubbles/internal/workspace"
)

func implementationVerificationPlan(mgr *workspace.Manager, changedPaths []string) verificationPlan {
	return frontier.BuildVerificationPlan(mgr, changedPaths)
}

func uniqueNormalizedPaths(paths []string) []string {
	return frontier.UniqueNormalizedPaths(paths)
}
