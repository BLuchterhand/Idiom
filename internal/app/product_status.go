package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/llama"
	"idiom/internal/productworkflow"
)

func (m *Model) slashProductStatus(_ string, fields []string) bool {
	absRoot, err := m.productStatusEffortRoot(fields)
	if err != nil {
		m.status = err.Error()
		return false
	}

	statusView, workflowState, statusErr := productworkflow.LoadCheckedProductStatus(absRoot)
	operatorStatus := productworkflow.BuildProductOperatorStatusView(statusView, workflowState)
	body := productworkflow.FormatProductOperatorStatusText(filepath.Base(absRoot), operatorStatus)
	m.insertBeforePendingAssistant(llama.ChatMessage{
		Role:    "operator",
		Content: strings.TrimRight(body, "\n"),
	})

	displayRoot := m.displayPath(absRoot)
	if statusErr != nil {
		m.status = "Product status failed for " + displayRoot + ": " + statusErr.Error()
		return false
	}
	if len(operatorStatus.Blockers) > 0 {
		m.status = fmt.Sprintf("Product status loaded for %s: %d blocker(s).", displayRoot, len(operatorStatus.Blockers))
		return false
	}
	m.status = "Product status loaded for " + displayRoot + "."
	return false
}

func (m *Model) productStatusEffortRoot(fields []string) (string, error) {
	switch len(fields) {
	case 1:
		absRoot, err := m.productStatusAbsPath(".")
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(filepath.Join(absRoot, "workflow_state.json")); err != nil {
			return "", errors.New("Usage: /product-status --effort-root <path> (or run from an effort root)")
		}
		return absRoot, nil
	case 2:
		if fields[1] == "--effort-root" {
			return "", errors.New("Usage: /product-status [--effort-root <path>|<path>]")
		}
		return m.productStatusAbsPath(fields[1])
	case 3:
		if fields[1] != "--effort-root" {
			return "", errors.New("Usage: /product-status [--effort-root <path>|<path>]")
		}
		return m.productStatusAbsPath(fields[2])
	default:
		return "", errors.New("Usage: /product-status [--effort-root <path>|<path>]")
	}
}

func (m *Model) productStatusAbsPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
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
	absRoot, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("product status: resolve effort root: %w", err)
	}
	return filepath.Clean(absRoot), nil
}
