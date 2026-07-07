package workspace

import (
	"strings"

	"idiom/internal/platform/textutil"
)

type WorkbranchShellPolicy struct {
	AllowedPrefixes []string
	DeniedPrefixes  []string
	DeniedFragments []string
}

func ReadOnlyChildShellAllowedPrefixes() []string {
	return append([]string(nil), readOnlyChildShellAllowedPrefixes...)
}

func WorkbranchChildShellPolicy(forbiddenTools []string) WorkbranchShellPolicy {
	policy := WorkbranchShellPolicy{
		AllowedPrefixes: append([]string(nil), workbranchChildShellAllowedPrefixes...),
		DeniedPrefixes: []string{
			"gh pr create",
			"git -C",
		},
		DeniedFragments: append([]string(nil), workbranchChildShellDeniedFragments...),
	}

	for _, forbidden := range forbiddenTools {
		switch strings.TrimSpace(forbidden) {
		case "git_push":
			policy.DeniedPrefixes = append(policy.DeniedPrefixes, "git push", "gh pr merge")
		case "git_history_mutation":
			policy.DeniedPrefixes = append(policy.DeniedPrefixes, "git commit", "git reset", "git checkout", "git switch", "git worktree", "git branch", "git clean")
		case "git_index_mutation":
			policy.DeniedPrefixes = append(policy.DeniedPrefixes, "git add", "git restore")
		case "publish":
			policy.DeniedPrefixes = append(policy.DeniedPrefixes, "npm publish", "pnpm publish", "yarn publish", "docker push")
		case "deploy":
			policy.DeniedPrefixes = append(policy.DeniedPrefixes, "kubectl ", "deploy", "release", "gh release")
		}
	}

	policy.DeniedPrefixes = textutil.Merge(policy.DeniedPrefixes)
	return policy
}

var readOnlyChildShellAllowedPrefixes = []string{
	"rg ", "grep ", "awk ", "sed -n", "cat ", "head ", "tail ", "wc ",
	"pwd", "ls ", "find ",
	"git status", "git diff", "git log", "git show", "git grep",
	"go test", "go list", "go env",
}

var workbranchChildShellAllowedPrefixes = []string{
	"go test", "go list",
	"git status", "git diff", "git log", "git show", "git grep",
	"pwd", "ls ", "rg ", "grep ", "cat ", "head ", "tail ", "wc ",
}

var workbranchChildShellDeniedFragments = []string{"\n", "\r", "&&", "||", ";", "|", ">", "<", "$(", "`", "go env -w", "go env -u", "--output="}
