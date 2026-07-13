package sessionlog

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/contracts/sensitivity"
	"idiom/internal/platform/config"
	"idiom/internal/platform/jsonl"
	"idiom/internal/platform/redaction"
)

// SessionInfo describes a recorded session under the chat debug-log directory.
type SessionInfo struct {
	ID            string
	Path          string
	UserMessages  int
	AssistMessage int
	FirstPrompt   string
}

const MaxRecordedSessionsScanned = 200

// ResolveSessionLogPath returns the JSONL path for the given session id under
// the configured debug-log directory. If id already looks like a path it is
// returned unchanged. The .jsonl extension is appended when missing.
func ResolveSessionLogPath(cfg config.Config, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}

	if strings.ContainsAny(id, string(os.PathSeparator)+"/") || strings.HasSuffix(id, ".jsonl") {
		if !strings.HasSuffix(id, ".jsonl") {
			id += ".jsonl"
		}

		return id
	}

	dir := RecordedSessionLogDir(cfg)

	return filepath.Join(dir, id+".jsonl")
}

func SessionIDFromPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	base := filepath.Base(path)
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}

	return strings.TrimSpace(base)
}

// ListRecordedSessions returns the recorded sessions in the configured
// debug-log directory, sorted newest-first by id (id is a sortable timestamp).
func ListRecordedSessions(cfg config.Config) ([]SessionInfo, error) {
	sessionFiles, err := ListRecordedSessionFiles(cfg)
	if err != nil {
		return nil, err
	}

	sessions := make([]SessionInfo, 0, len(sessionFiles))
	for _, sessionFile := range sessionFiles {
		info := SummarizeSessionLog(sessionFile.Path)
		info.ID = sessionFile.ID
		info.Path = sessionFile.Path
		if !IsUserVisibleSession(info) {
			continue
		}

		sessions = append(sessions, info)
	}

	return sessions, nil
}

func RecordedSessionLogDir(cfg config.Config) string {
	dir := strings.TrimSpace(cfg.Chat.DebugLogDir)
	if dir == "" {
		dir = "logs/conversations"
	}

	return dir
}

func ListRecordedSessionFiles(cfg config.Config) ([]SessionInfo, error) {
	dir := RecordedSessionLogDir(cfg)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("read debug log dir %q: %w", dir, err)
	}

	sessionFiles := make([]SessionInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}

		id := strings.TrimSuffix(name, ".jsonl")
		path := filepath.Join(dir, name)
		sessionFiles = append(sessionFiles, SessionInfo{ID: id, Path: path})
	}

	slices.SortFunc(sessionFiles, func(a, b SessionInfo) int {
		return -cmp.Compare(a.ID, b.ID)
	})

	if len(sessionFiles) > MaxRecordedSessionsScanned {
		sessionFiles = sessionFiles[:MaxRecordedSessionsScanned]
	}

	return sessionFiles, nil
}

func SummarizeSessionLog(path string) SessionInfo {
	info := SessionInfo{}

	_ = jsonl.ScanFile(path, func(raw json.RawMessage) error {
		var record map[string]any

		if err := json.Unmarshal(raw, &record); err != nil {
			return nil
		}

		if record["type"] != "message" {
			return nil
		}

		role, _ := record["role"].(string)
		content, _ := record["content"].(string)

		switch role {
		case "user":
			info.UserMessages++
			if info.FirstPrompt == "" {
				info.FirstPrompt = SafePromptSummary(content, 80)
			}
		case "assistant":
			info.AssistMessage++
		}

		return nil
	})

	return info
}

func IsUserVisibleSession(info SessionInfo) bool {
	return info.UserMessages > 0
}

func SafePromptSummary(text string, max int) string {
	summary := oneLineSummary(text, max)
	if summary == "" {
		return ""
	}

	if sensitivity.ContainsSensitiveText(summary) {
		return "[redacted prompt summary]"
	}

	redacted := redaction.RedactSecrets(summary)
	if redacted != summary {
		return "[redacted prompt summary]"
	}

	return redacted
}

func oneLineSummary(text string, max int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}

	text = strings.TrimSpace(text)
	if len(text) > max {
		text = text[:max-1] + "..."
	}

	return text
}
