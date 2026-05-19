package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/llama"
	"idiom/internal/recoverstatus"
)

const recoverStatusUsage = "Usage: /recover-status [--packet <path>|<path>]"

func (m *Model) slashRecoverStatus(_ string, fields []string) bool {
	packetPath, err := recoverStatusPacketPath(fields)
	if err != nil {
		m.insertRecoverStatusOperatorMessage(err.Error())
		m.status = err.Error()
		return false
	}
	packetPath, err = m.recoverStatusAbsPacketPath(packetPath)
	if err != nil {
		message := "recover-status: " + err.Error()
		m.insertRecoverStatusOperatorMessage(message)
		m.status = message
		return false
	}

	report, err := recoverstatus.Build(m.recoverStatusWorkspaceRoot(), packetPath)
	if err != nil {
		body := strings.TrimRight(recoverstatus.FormatText(recoverstatus.ErrorReport(err.Error())), "\n")
		m.insertRecoverStatusOperatorMessage(body)
		m.status = "recover-status: " + err.Error()
		return false
	}

	body := strings.TrimRight(recoverstatus.FormatText(report), "\n")
	m.insertBeforePendingAssistant(llama.ChatMessage{
		Role:    "operator",
		Content: body,
	})
	if len(report.Blockers) > 0 {
		m.status = fmt.Sprintf("Recovery status loaded for %s: %d blocker(s).", m.recoverStatusDisplayPacketPath(packetPath), len(report.Blockers))
		return false
	}
	m.status = "Recovery status loaded for " + m.recoverStatusDisplayPacketPath(packetPath) + "."
	return false
}

func recoverStatusPacketPath(fields []string) (string, error) {
	switch len(fields) {
	case 2:
		if fields[1] == "--packet" || strings.HasPrefix(fields[1], "-") {
			return "", fmt.Errorf(recoverStatusUsage)
		}
		return fields[1], nil
	case 3:
		if fields[1] != "--packet" || strings.TrimSpace(fields[2]) == "" {
			return "", fmt.Errorf(recoverStatusUsage)
		}
		return fields[2], nil
	default:
		return "", fmt.Errorf(recoverStatusUsage)
	}
}

func (m *Model) recoverStatusWorkspaceRoot() string {
	if m != nil && m.workspace != nil && strings.TrimSpace(m.workspace.Root()) != "" {
		return m.workspace.Root()
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

func (m *Model) recoverStatusAbsPacketPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf(recoverStatusUsage)
	}
	base := ""
	if m != nil && m.workspace != nil && strings.TrimSpace(m.workspace.CWD()) != "" {
		base = m.workspace.CWD()
	} else if cwd, err := os.Getwd(); err == nil {
		base = cwd
	}
	if !filepath.IsAbs(path) && base != "" {
		path = filepath.Join(base, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve recovery packet: %w", err)
	}
	return filepath.Clean(absPath), nil
}

func (m *Model) insertRecoverStatusOperatorMessage(message string) {
	m.insertBeforePendingAssistant(llama.ChatMessage{
		Role:    "operator",
		Content: strings.TrimSpace(message),
	})
}

func (m *Model) recoverStatusDisplayPacketPath(packetPath string) string {
	if filepath.IsAbs(packetPath) {
		return m.displayPath(packetPath)
	}
	return filepath.ToSlash(packetPath)
}
