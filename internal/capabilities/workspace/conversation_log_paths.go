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
	userGlobalCodexSessionDir    = ".codex/sessions"
	userGlobalCodexArchiveDir    = ".codex/archived_sessions"
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
		_, ok := resolveUserGlobalConversationLogPath(path, false)
		return ok
	}

	return isConversationLogRelativePath(path)
}

func resolveUserGlobalConversationLogReadPath(path string) (string, bool) {
	return resolveUserGlobalConversationLogPath(path, false)
}

func resolveUserGlobalConversationLogSearchPath(path string) (string, bool) {
	return resolveUserGlobalConversationLogPath(path, true)
}

func resolveUserGlobalConversationLogPath(path string, allowDirectoryScope bool) (string, bool) {
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
	if !isUserGlobalConversationLogRel(rel, allowDirectoryScope) {
		return "", false
	}

	return resolved, true
}

func isConversationLogRelativePath(path string) bool {
	normalized := normalizeConversationLogPath(path)
	return pathIsAtOrUnder(normalized, workspaceConversationLogDir) ||
		pathIsAtOrUnder(normalized, projectConversationLogDir)
}

func isUserGlobalConversationLogRel(path string, allowDirectoryScope bool) bool {
	normalized := normalizeConversationLogPath(path)
	if pathIsAtOrUnder(normalized, userGlobalConversationLogDir) {
		return true
	}

	if allowDirectoryScope && (pathIsAtOrUnder(normalized, userGlobalCodexSessionDir) ||
		pathIsAtOrUnder(normalized, userGlobalCodexArchiveDir)) {

		return true
	}

	if !strings.EqualFold(filepath.Ext(normalized), ".jsonl") {
		return false
	}

	return pathIsAtOrUnder(normalized, userGlobalCodexSessionDir) ||
		pathIsAtOrUnder(normalized, userGlobalCodexArchiveDir)
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
