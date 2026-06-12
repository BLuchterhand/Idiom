package app

import (
	"encoding/json"
	"strings"

	"idiom/internal/platform/config"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
)

const maxInputHistoryEntries = 50

func (m *Model) recordInputHistory(text string) {
	if m == nil {
		return
	}

	m.inputHistory = appendInputHistoryValue(m.inputHistory, text, maxInputHistoryEntries)
	m.resetInputHistoryNavigation()
}

func (m *Model) recordInputSubmission(text string) {
	m.recordInputHistory(text)

	if m == nil || m.debugLogger == nil {
		return
	}

	_ = m.debugLogger.LogInputSubmission(text)
}

func (m *Model) loadInputHistoryFromLogs() error {
	if m == nil {
		return nil
	}

	history, err := inputHistoryFromRecordedSessions(m.cfg, maxInputHistoryEntries)
	if err != nil {
		return err
	}

	m.inputHistory = history
	m.resetInputHistoryNavigation()

	return nil
}

func (m *Model) loadInputHistoryFromSessionMessages(messages []llama.ChatMessage) {
	if m == nil {
		return
	}

	for _, msg := range messages {
		if strings.TrimSpace(msg.Role) != "user" {
			continue
		}

		m.inputHistory = appendInputHistoryValue(m.inputHistory, msg.Content, maxInputHistoryEntries)
	}

	m.resetInputHistoryNavigation()
}

func appendInputHistoryValue(history []string, text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return history
	}

	if limit <= 0 {
		return nil
	}

	next := history[:0]
	for _, existing := range history {
		if strings.TrimSpace(existing) == "" || existing == text {
			continue
		}

		next = append(next, existing)
	}

	next = append(next, text)
	if len(next) > limit {
		next = next[len(next)-limit:]
	}

	return next
}

func inputHistoryFromRecordedSessions(cfg config.Config, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}

	sessions, err := ListRecordedSessions(cfg)
	if err != nil {
		return nil, err
	}

	var history []string

	for i := len(sessions) - 1; i >= 0; i-- {
		messages, err := inputHistoryValuesFromSessionLog(sessions[i].Path)
		if err != nil {
			continue
		}

		for _, message := range messages {
			history = appendInputHistoryValue(history, message, limit)
		}
	}

	return history, nil
}

func inputHistoryValuesFromSessionLog(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}

	var (
		submissions []string
		messages    []string
	)

	err := util.ScanFile(path, func(raw json.RawMessage) error {
		var record map[string]any

		if err := json.Unmarshal(raw, &record); err != nil {
			return nil
		}

		switch sessionStringField(record, "type") {
		case "input_submission":
			content := strings.TrimSpace(sessionStringField(record, "content"))
			if content != "" {
				submissions = append(submissions, content)
			}
		case "message":
			if sessionStringField(record, "role") != "user" {
				return nil
			}

			content := strings.TrimSpace(sessionStringField(record, "content"))
			if content != "" {
				messages = append(messages, content)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(submissions) > 0 {
		return submissions, nil
	}

	return messages, nil
}
