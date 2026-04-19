package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"qwen-bubbles/internal/config"
	"qwen-bubbles/internal/llama"
)

// SessionInfo describes a recorded session under the chat debug-log directory.
type SessionInfo struct {
	ID            string
	Path          string
	UserMessages  int
	AssistMessage int
	FirstPrompt   string
}

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
	dir := cfg.Chat.DebugLogDir
	if strings.TrimSpace(dir) == "" {
		dir = "logs/conversations"
	}
	return filepath.Join(dir, id+".jsonl")
}

// ListRecordedSessions returns the recorded sessions in the configured
// debug-log directory, sorted newest-first by id (id is a sortable timestamp).
func ListRecordedSessions(cfg config.Config) ([]SessionInfo, error) {
	dir := cfg.Chat.DebugLogDir
	if strings.TrimSpace(dir) == "" {
		dir = "logs/conversations"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read debug log dir %q: %w", dir, err)
	}
	sessions := make([]SessionInfo, 0, len(entries))
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
		info := summarizeSessionLog(path)
		info.ID = id
		info.Path = path
		sessions = append(sessions, info)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ID > sessions[j].ID
	})
	return sessions, nil
}

func summarizeSessionLog(path string) SessionInfo {
	info := SessionInfo{}
	file, err := os.Open(path)
	if err != nil {
		return info
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<22)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		if record["type"] != "message" {
			continue
		}
		role, _ := record["role"].(string)
		content, _ := record["content"].(string)
		switch role {
		case "user":
			info.UserMessages++
			if info.FirstPrompt == "" {
				info.FirstPrompt = oneLineSummary(content, 80)
			}
		case "assistant":
			info.AssistMessage++
		}
	}
	return info
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
		text = text[:max-1] + "…"
	}
	return text
}

// LoadHistoryFromSession replays the user/assistant message records from a
// prior session log into the model's conversation history. It does not restore
// workspace, plan, or workflow state — those load from their own per-workspace
// persistence on Model construction. Tool results are not replayed; the model
// just sees the prior conversation as plain chat history.
func (m *Model) LoadHistoryFromSession(path string) error {
	if m == nil {
		return fmt.Errorf("model is nil")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("session path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<22)
	loaded := 0
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		if record["type"] != "message" {
			continue
		}
		role, _ := record["role"].(string)
		content, _ := record["content"].(string)
		if role != "user" && role != "assistant" {
			continue
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		m.messages = append(m.messages, llama.ChatMessage{Role: role, Content: content})
		loaded++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read session log: %w", err)
	}
	if loaded == 0 {
		return fmt.Errorf("session log %q contained no user/assistant messages", path)
	}
	return nil
}
