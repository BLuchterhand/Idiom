package workspace

import (
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/platform/safepath"
)

const (
	workspaceConversationLogDir  = "logs/conversations"
	projectConversationLogDir    = ".idiom/logs/conversations"
	userGlobalConversationLogDir = ".idiom/logs/conversations"
)

// IsDefaultReadableConversationLogPath reports whether path is one of the
// harness conversation-log locations that the agent may inspect as read-only
// diagnostic evidence without asking the operator for per-file read approval.
func IsDefaultReadableConversationLogPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}

	if filepath.IsAbs(path) {
		return isUserGlobalConversationLogPath(path)
	}

	return isConversationLogRelativePath(path)
}

func resolveUserGlobalConversationLogReadPath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", false
	}

	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", false
	}

	resolved, rel, err := safepath.UnderRoot(home, path, "user-global conversation log read path", safepath.Options{
		AllowMissing:           true,
		RejectSymlinkAncestors: true,
		RejectSymlinkTarget:    true,
	})
	if err != nil {
		return "", false
	}
	if !pathIsAtOrUnder(rel, userGlobalConversationLogDir) {
		return "", false
	}

	return resolved, true
}

func isUserGlobalConversationLogPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return false
	}

	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return false
	}

	rel, ok := safepath.Rel(filepath.Clean(home), filepath.Clean(path))
	if !ok {
		return false
	}

	return pathIsAtOrUnder(rel, userGlobalConversationLogDir)
}

func isConversationLogRelativePath(path string) bool {
	normalized := normalizeConversationLogPath(path)
	return pathIsAtOrUnder(normalized, workspaceConversationLogDir) ||
		pathIsAtOrUnder(normalized, projectConversationLogDir)
}

func normalizeConversationLogPath(path string) string {
	normalized := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	normalized = strings.TrimPrefix(normalized, "./")
	return strings.Trim(normalized, "/")
}

func pathIsAtOrUnder(path string, dir string) bool {
	dir = normalizeConversationLogPath(dir)
	return path == dir || strings.HasPrefix(path, dir+"/")
}
