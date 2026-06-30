package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/workflows/codingrules"
)

const codingRulesReportPayloadType = "application/vnd.idiom.coding_rules.report+json"

func (m *Manager) codingRulesCheck(ctx context.Context, req ToolRequest) ToolResult {
	if !m.Enabled() {
		return ToolResult{Tool: ToolNameCodingRulesCheck, Summary: "Coding rules unavailable", Error: "workspace root is not configured"}
	}

	paths, err := m.codingRulesCheckPaths(req)
	if err != nil {
		return ToolResult{Tool: ToolNameCodingRulesCheck, Summary: "Coding rules rejected", Error: err.Error()}
	}

	configPath, err := m.codingRulesConfigPath(req.ConfigPath, req.Workdir)
	if err != nil {
		return ToolResult{Tool: ToolNameCodingRulesCheck, Summary: "Coding rules rejected", Error: err.Error()}
	}

	result, err := codingrules.CheckWorkspace(ctx, codingrules.CheckOptions{
		ProjectRoot: m.cfg.Root,
		ConfigPath:  configPath,
		Paths:       paths,
	})
	if err != nil {
		return ToolResult{Tool: ToolNameCodingRulesCheck, Summary: "Coding rules failed", Error: err.Error()}
	}

	body, err := json.MarshalIndent(result.Report, "", "  ")
	if err != nil {
		return ToolResult{Tool: ToolNameCodingRulesCheck, Summary: "Coding rules encode failed", Error: err.Error()}
	}

	summary := fmt.Sprintf(
		"Coding rules %s: files=%d findings=%d blocking=%d suppressed=%d",
		result.Report.Status,
		result.Report.Summary.FilesScanned,
		result.Report.Summary.Findings,
		result.Report.Summary.Blocking,
		result.Report.Summary.Suppressed,
	)
	if !result.ConfigFound {
		summary += " config=default"
	}

	return ToolResult{
		Tool:        ToolNameCodingRulesCheck,
		Summary:     summary,
		Output:      string(body),
		PayloadType: codingRulesReportPayloadType,
		Payload:     body,
	}
}

func (m *Manager) codingRulesCheckPaths(req ToolRequest) ([]string, error) {
	path := strings.TrimSpace(req.Path)
	if path == "" || path == "." {
		return nil, nil
	}

	abs, err := m.resolvePath(path, req.Workdir)
	if err != nil {
		return nil, err
	}

	rel, err := filepath.Rel(m.cfg.Root, abs)
	if err != nil {
		return nil, fmt.Errorf("resolve coding rules path: %w", err)
	}

	return []string{filepath.ToSlash(rel)}, nil
}

func (m *Manager) codingRulesConfigPath(configPath string, workdir string) (string, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return "", nil
	}

	abs, err := m.resolvePath(configPath, workdir)
	if err != nil {
		return "", err
	}

	return abs, nil
}
