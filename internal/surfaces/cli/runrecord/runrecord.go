package runrecord

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/platform/sessionid"
)

const (
	RunCompletionSchemaVersion = "idiom.headless.run_completion.v1"
	RunCompletionRecordType    = "idiom.headless.run_completion"
)

type TerminalClass string

const (
	TerminalClassCompleted          TerminalClass = "completed"
	TerminalClassFailed             TerminalClass = "failed"
	TerminalClassDegraded           TerminalClass = "degraded"
	TerminalClassOutputSchemaFailed TerminalClass = "output_schema_failed"
	TerminalClassVerificationFailed TerminalClass = "verification_failed"
	TerminalClassArtifactFailed     TerminalClass = "artifact_failed"
	TerminalClassCommitFailed       TerminalClass = "commit_failed"
)

type RunCompletionRecord struct {
	SchemaVersion    string              `json:"schema_version"`
	Type             string              `json:"type"`
	RunID            string              `json:"run_id"`
	SessionID        string              `json:"session_id,omitempty"`
	DebugLogPath     string              `json:"debug_log_path,omitempty"`
	PromptSHA256     string              `json:"prompt_sha256,omitempty"`
	AnswerSHA256     string              `json:"answer_sha256,omitempty"`
	OutputMode       string              `json:"output_mode"`
	OutputSchema     string              `json:"output_schema,omitempty"`
	ExitCode         int                 `json:"exit_code"`
	TerminalClass    TerminalClass       `json:"terminal_class"`
	Error            string              `json:"error,omitempty"`
	ConfigPath       string              `json:"config_path,omitempty"`
	WorkspaceRoot    string              `json:"workspace_root,omitempty"`
	Policy           HeadlessPolicy      `json:"policy"`
	Artifacts        map[string]string   `json:"artifacts,omitempty"`
	Checks           []HeadlessCheck     `json:"checks,omitempty"`
	StartedAt        time.Time           `json:"started_at"`
	CompletedAt      time.Time           `json:"completed_at"`
	DurationMS       int64               `json:"duration_ms"`
	Implementation   string              `json:"implementation"`
	UnsupportedFlags []string            `json:"unsupported_flags,omitempty"`
	Metadata         map[string][]string `json:"metadata,omitempty"`
}

type RunOutputRecord struct {
	RunCompletionRecord
	Answer string `json:"answer,omitempty"`
}

type HeadlessPolicy struct {
	PermissionProfile string   `json:"permission_profile,omitempty"`
	ApprovalPolicy    string   `json:"approval_policy,omitempty"`
	Ephemeral         bool     `json:"ephemeral,omitempty"`
	AllowTools        []string `json:"allow_tools,omitempty"`
	DenyTools         []string `json:"deny_tools,omitempty"`
	AllowWrites       []string `json:"allow_writes,omitempty"`
	DenyPaths         []string `json:"deny_paths,omitempty"`
	MaxToolSteps      int      `json:"max_tool_steps,omitempty"`
	Timeout           string   `json:"timeout,omitempty"`
}

type HeadlessCheck struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

func SaveRunCompletionRecord(root string, record RunCompletionRecord) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	if err := ValidateRunID(record.RunID); err != nil {
		return "", err
	}

	relPath := RunCompletionRelPath(record.RunID)
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir headless run completion dir: %w", err)
	}

	if err := writeJSONFile(fullPath, record); err != nil {
		return "", err
	}

	return relPath, nil
}

func RunCompletionRelPath(runID string) string {
	return filepath.ToSlash(filepath.Join(".idiom", "runs", strings.TrimSpace(runID)+".completion.json"))
}

func ValidateRunID(runID string) error {
	if err := sessionid.Validate(strings.TrimSpace(runID)); err != nil {
		return fmt.Errorf("invalid run id %q: %w", runID, err)
	}
	return nil
}

func AddArtifact(record *RunCompletionRecord, name string, path string) {
	if record.Artifacts == nil {
		record.Artifacts = map[string]string{}
	}
	record.Artifacts[name] = path
}

func NewRunID() string {
	return time.Now().UTC().Format("20060102-150405000")
}

func SHA256Hex(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))

	return hex.EncodeToString(sum[:])
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run completion: %w", err)
	}

	data = append(data, '\n')
	if err := os.WriteFile(filepath.Clean(path), data, 0o644); err != nil {
		return fmt.Errorf("write run completion: %w", err)
	}

	return nil
}
