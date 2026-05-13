package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultDir    = "logs/telemetry"
	schemaVersion = "idiom.telemetry.v1"
)

type Options struct {
	Command       string
	Args          []string
	WorkspaceRoot string
	Dir           string
	// Version is the binary version stamp (the same string `idiom
	// version` prints, e.g. "v0.1.0" or "dev"). When set, it lands in
	// session_start and process_start records so a log-file reader can
	// attribute behavior to a specific build without scraping the
	// binary that produced it. Empty values are not emitted.
	Version string
}

type Recorder struct {
	mu              sync.Mutex
	file            *os.File
	path            string
	startedAt       time.Time
	command         string
	closeoutMu      sync.Mutex
	closeouts       []func()
	closeoutsCalled bool
}

func Open(opts Options) (*Recorder, error) {
	if telemetryDisabled() {
		return nil, nil
	}
	dir := telemetryDir(opts.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create telemetry dir: %w", err)
	}
	command := safeCommandName(opts.Command)
	if command == "" {
		command = "idiom"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s-%d.jsonl", time.Now().Format("20060102-150405.000"), command, os.Getpid()))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open telemetry log: %w", err)
	}
	r := &Recorder{
		file:      file,
		path:      path,
		startedAt: time.Now(),
		command:   command,
	}
	workspaceRoot := filepath.Clean(strings.TrimSpace(opts.WorkspaceRoot))
	version := strings.TrimSpace(opts.Version)
	sessionFields := map[string]any{
		"app_name":       "Idiom",
		"schema_version": schemaVersion,
		"workspace_root": workspaceRoot,
		"command":        command,
	}
	processFields := map[string]any{
		"schema_version": schemaVersion,
		"command":        command,
		"args":           sanitizeArgs(opts.Args),
		"arg_count":      len(opts.Args),
		"pid":            os.Getpid(),
		"workspace_root": workspaceRoot,
	}
	if version != "" {
		sessionFields["version"] = version
		processFields["version"] = version
	}
	_ = r.Log("session_start", sessionFields)
	_ = r.Log("process_start", processFields)
	return r, nil
}

func (r *Recorder) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

func (r *Recorder) Log(eventType string, fields map[string]any) error {
	if r == nil {
		return nil
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		eventType = "event"
	}
	record := make(map[string]any, len(fields)+3)
	record["ts"] = time.Now().Format(time.RFC3339Nano)
	record["type"] = eventType
	record["schema_version"] = schemaVersion
	for key, value := range fields {
		if strings.TrimSpace(key) == "" {
			continue
		}
		record[key] = value
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal telemetry record: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	if _, err := r.file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write telemetry record: %w", err)
	}
	return nil
}

func (r *Recorder) RegisterCloseout(fn func()) func() {
	if r == nil || fn == nil {
		return func() {}
	}
	r.closeoutMu.Lock()
	defer r.closeoutMu.Unlock()
	if r.closeoutsCalled {
		return func() {}
	}
	r.closeouts = append(r.closeouts, fn)
	index := len(r.closeouts) - 1
	return func() {
		r.closeoutMu.Lock()
		defer r.closeoutMu.Unlock()
		if index >= 0 && index < len(r.closeouts) {
			r.closeouts[index] = nil
		}
	}
}

func (r *Recorder) runCloseouts() {
	if r == nil {
		return
	}
	r.closeoutMu.Lock()
	if r.closeoutsCalled {
		r.closeoutMu.Unlock()
		return
	}
	closeouts := append([]func(){}, r.closeouts...)
	r.closeouts = nil
	r.closeoutsCalled = true
	r.closeoutMu.Unlock()

	for i := len(closeouts) - 1; i >= 0; i-- {
		if closeouts[i] != nil {
			closeouts[i]()
		}
	}
}

func (r *Recorder) Close(exitCode int) error {
	return r.CloseWithFields(exitCode, nil)
}

func (r *Recorder) CloseWithFields(exitCode int, fields map[string]any) error {
	if r == nil {
		return nil
	}
	r.runCloseouts()
	record := make(map[string]any, len(fields)+6)
	record["ts"] = time.Now().Format(time.RFC3339Nano)
	record["type"] = "process_exit"
	record["schema_version"] = schemaVersion
	record["command"] = r.command
	record["exit_code"] = exitCode
	record["duration_ms"] = time.Since(r.startedAt).Milliseconds()
	for key, value := range fields {
		if strings.TrimSpace(key) == "" {
			continue
		}
		record[key] = value
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal telemetry process_exit: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	if _, err := r.file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write telemetry process_exit: %w", err)
	}
	err = r.file.Close()
	r.file = nil
	return err
}

func telemetryDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("IDIOM_TELEMETRY"))) {
	case "0", "false", "off", "no", "disabled":
		return true
	default:
		return false
	}
}

func telemetryDir(configured string) string {
	if env := strings.TrimSpace(os.Getenv("IDIOM_TELEMETRY_DIR")); env != "" {
		return filepath.Clean(env)
	}
	if strings.TrimSpace(configured) != "" {
		return filepath.Clean(configured)
	}
	return defaultDir
}

var commandNameRe = regexp.MustCompile(`[^a-zA-Z0-9_.:-]+`)

func safeCommandName(command string) string {
	command = strings.ToLower(strings.TrimSpace(command))
	command = commandNameRe.ReplaceAllString(command, "-")
	command = strings.Trim(command, "-")
	if len(command) > 80 {
		command = command[:80]
	}
	return command
}

func sanitizeArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if flag, value, ok := strings.Cut(arg, "="); ok && strings.HasPrefix(flag, "-") {
			if isSensitiveFlag(flag) {
				out = append(out, flag+"="+redactedValue(value))
			} else {
				out = append(out, flag+"="+sanitizeArgValue(value))
			}
			continue
		}
		out = append(out, arg)
		if isSensitiveFlag(arg) && i+1 < len(args) {
			i++
			out = append(out, redactedValue(args[i]))
		}
	}
	return out
}

func isSensitiveFlag(flag string) bool {
	switch strings.TrimSpace(flag) {
	case "-prompt", "--prompt", "-input", "--input", "-prior", "--prior",
		"-task", "--task", "-validator", "--validator", "-system", "--system":
		return true
	default:
		return false
	}
}

func sanitizeArgValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if len(value) > 240 {
		return redactedValue(value)
	}
	return value
}

func redactedValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<empty>"
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("<redacted len=%d sha256:%s>", len(value), hex.EncodeToString(sum[:4]))
}
