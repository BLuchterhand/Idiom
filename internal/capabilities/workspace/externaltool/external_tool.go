package externaltool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/platform/commandexec"
)

// Tool registers an arbitrary shell-invokable tool.
type Tool struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Command        string          `json:"command"`
	Args           []string        `json:"args"`
	Cwd            string          `json:"cwd,omitempty"`
	TimeoutSeconds int             `json:"timeout_seconds,omitempty"`
	InputSchema    json.RawMessage `json:"input_schema,omitempty"`
	ReadOnly       bool            `json:"read_only,omitempty"`
	Discovery      bool            `json:"discovery,omitempty"`
}

// Definition is the neutral tool-schema shape exposed by project tools.
type Definition struct {
	Name           string
	Description    string
	ParametersJSON string
}

// Registry holds external tool definitions keyed by name.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry returns a registry pre-loaded with the provided tools.
func NewRegistry(tools []Tool) (*Registry, error) {
	reg := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			return nil, fmt.Errorf("external tool: empty name")
		}

		if strings.ContainsAny(name, " \t\n/") {
			return nil, fmt.Errorf("external tool %q: name must not contain whitespace or slashes", name)
		}

		if _, dup := reg.tools[name]; dup {
			return nil, fmt.Errorf("external tool %q: duplicate name", name)
		}

		if strings.TrimSpace(t.Command) == "" {
			return nil, fmt.Errorf("external tool %q: empty command", name)
		}

		t.Name = name
		reg.tools[name] = t
	}

	return reg, nil
}

// Names returns the registered tool names in undefined order.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}

	out := make([]string, 0, len(r.tools))
	for name := range r.tools {
		out = append(out, name)
	}

	return out
}

// AvailableNames returns registered tool names whose command can be resolved.
func (r *Registry) AvailableNames(baseDir string) []string {
	if r == nil {
		return nil
	}

	out := make([]string, 0, len(r.tools))
	for name := range r.tools {
		if ok, _ := r.Availability(name, baseDir); ok {
			out = append(out, name)
		}
	}

	return out
}

// Has reports whether name is registered.
func (r *Registry) Has(name string) bool {
	if r == nil {
		return false
	}

	_, ok := r.tools[name]

	return ok
}

// Tool returns the registered external tool definition.
func (r *Registry) Tool(name string) (Tool, bool) {
	if r == nil {
		return Tool{}, false
	}

	t, ok := r.tools[strings.TrimSpace(name)]

	return t, ok
}

// Availability reports whether a registered external tool can be invoked.
func (r *Registry) Availability(name string, baseDir string) (bool, string) {
	tool, ok := r.Tool(name)
	if !ok {
		return false, "not registered"
	}

	if reason := unavailableReason(tool, baseDir); reason != "" {
		return false, reason
	}

	return true, ""
}

// IsReadOnlyDiscovery reports whether the host project declared an external
// tool as a read-only discovery wrapper.
func (r *Registry) IsReadOnlyDiscovery(name string) bool {
	t, ok := r.Tool(name)
	return ok && t.ReadOnly && t.Discovery
}

// Definitions converts registered tools into schema definitions.
func (r *Registry) Definitions() []Definition {
	if r == nil {
		return nil
	}

	out := make([]Definition, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, definitionForTool(t))
	}

	return out
}

// DefinitionsForBaseDir converts available tools into schema definitions.
func (r *Registry) DefinitionsForBaseDir(baseDir string) []Definition {
	if r == nil {
		return nil
	}

	out := make([]Definition, 0, len(r.tools))
	for name, t := range r.tools {
		if ok, _ := r.Availability(name, baseDir); !ok {
			continue
		}

		out = append(out, definitionForTool(t))
	}

	return out
}

func definitionForTool(tool Tool) Definition {
	params := string(tool.InputSchema)
	if strings.TrimSpace(params) == "" {
		params = `{"type":"object","properties":{}}`
	}

	return Definition{
		Name:           tool.Name,
		Description:    tool.Description,
		ParametersJSON: params,
	}
}

// Invoke runs the registered tool with the provided JSON-encoded input.
func (r *Registry) Invoke(ctx context.Context, name string, input json.RawMessage, baseDir string, maxOutputBytes int) (string, bool, error) {
	if r == nil {
		return "", false, fmt.Errorf("external tool %q: registry not initialised", name)
	}

	tool, ok := r.tools[name]
	if !ok {
		return "", false, fmt.Errorf("external tool %q: not registered", name)
	}

	if ok, reason := r.Availability(name, baseDir); !ok {
		return "", false, fmt.Errorf("external tool %q: unavailable: %s", name, reason)
	}

	parsed := map[string]any{}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &parsed); err != nil {
			return "", false, fmt.Errorf("external tool %q: invalid input JSON: %w", name, err)
		}
	}

	args := make([]string, 0, len(tool.Args))
	for _, raw := range tool.Args {
		args = append(args, SubstituteArgs(raw, parsed)...)
	}

	timeout := time.Duration(tool.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	workdir := baseDir
	if cwd := strings.TrimSpace(tool.Cwd); cwd != "" {
		workdir = expandCwd(cwd, baseDir)
	}

	if os.Getenv("IDIOM_DEBUG_TOOLS") != "" {
		fmt.Fprintf(os.Stderr, "[idiom-debug] external tool=%s argv=%v cwd=%s\n", name, args, workdir)
	}

	result := commandexec.Run(ctx, commandexec.Spec{
		Name:           tool.Command,
		Args:           args,
		Dir:            workdir,
		Stdin:          string(input),
		Timeout:        timeout,
		MaxOutputBytes: maxOutputBytes,
		MergeOutput:    true,
	})
	output := commandexec.CombineOutput(result.Stdout, result.Stderr)
	truncated := result.StdoutTruncated || result.StderrTruncated

	if result.Err != nil {
		if result.TimedOut {
			return output, truncated, fmt.Errorf("external tool %q: timed out after %s", name, timeout)
		}

		return output, truncated, fmt.Errorf("external tool %q: %w", name, result.Err)
	}

	return output, truncated, nil
}

func unavailableReason(tool Tool, baseDir string) string {
	command := strings.TrimSpace(tool.Command)
	if command == "" {
		return "empty command"
	}

	if strings.ContainsRune(command, os.PathSeparator) {
		path := command
		if !filepath.IsAbs(path) {
			workingDir := workingDir(tool, baseDir)
			if workingDir != "" {
				path = filepath.Join(workingDir, path)
			}
		}

		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return "command path does not exist"
			}

			return "command path is not accessible"
		}

		if info.IsDir() {
			return "command path is a directory"
		}

		if info.Mode().Perm()&0o111 == 0 {
			return "command path is not executable"
		}

		return ""
	}

	if _, err := exec.LookPath(command); err != nil {
		return "command not found on PATH"
	}

	return ""
}

func workingDir(tool Tool, baseDir string) string {
	if cwd := strings.TrimSpace(tool.Cwd); cwd != "" {
		return expandCwd(cwd, baseDir)
	}

	return baseDir
}

// SubstituteArgs expands one argv template token into zero, one, or many argv
// tokens.
func SubstituteArgs(template string, input map[string]any) []string {
	t := template
	if strings.HasPrefix(t, "{?") && strings.HasSuffix(t, "}") {
		inner := t[2 : len(t)-1]
		colon := strings.Index(inner, ":")

		if colon < 0 {
			return []string{template}
		}

		key := inner[:colon]
		flag := inner[colon+1:]

		if isTruthy(input[key]) {
			return []string{flag}
		}

		return nil
	}

	if strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") && !strings.HasPrefix(t, "{?") {
		inner := t[1 : len(t)-1]
		if !strings.Contains(inner, "|") {
			value, ok := input[inner]
			if !ok {
				return nil
			}

			if parts, ok := expandArrayValue(value); ok {
				return parts
			}
		}
	}

	var b strings.Builder

	i := 0

	for i < len(t) {
		if t[i] != '{' {
			b.WriteByte(t[i])

			i++

			continue
		}

		end := strings.IndexByte(t[i+1:], '}')
		if end < 0 {
			b.WriteString(t[i:])

			break
		}

		inner := t[i+1 : i+1+end]
		if strings.HasPrefix(inner, "?") {
			b.WriteString(t[i : i+1+end+1])

			i = i + 1 + end + 1

			continue
		}

		b.WriteString(expandSingle(inner, input))

		i = i + 1 + end + 1
	}

	return []string{b.String()}
}

func expandSingle(inner string, input map[string]any) string {
	if pipe := strings.Index(inner, "|"); pipe >= 0 {
		key := inner[:pipe]
		def := inner[pipe+1:]

		if v, ok := input[key]; ok {
			return toString(v)
		}

		return def
	}

	if v, ok := input[inner]; ok {
		return toString(v)
	}

	return ""
}

func expandArrayValue(v any) ([]string, bool) {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			out = append(out, toString(item))
		}

		return out, true
	case []string:
		return append([]string(nil), x...), true
	default:
		return nil, false
	}
}

func isTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return strings.TrimSpace(x) != ""
	case float64:
		return x != 0
	case int:
		return x != 0
	default:
		return true
	}
}

func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}

		return "false"
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}

		return fmt.Sprintf("%v", x)
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return ""
		}

		return string(b)
	}
}

func expandCwd(cwd, baseDir string) string {
	if cwd == "" {
		return ""
	}

	if filepath.IsAbs(cwd) {
		return cwd
	}

	if baseDir == "" {
		return cwd
	}

	return filepath.Join(baseDir, cwd)
}
