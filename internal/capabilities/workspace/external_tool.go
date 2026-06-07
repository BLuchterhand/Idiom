package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ExternalTool registers an arbitrary shell-invokable tool that the model
// can call by name, just like a built-in or MCP tool. Tools are described
// with a JSON Schema so the model gets typed hints, and invoked via
// exec.Command (no shell expansion) so untrusted argument values cannot
// inject extra shell tokens.
//
// Tools are GENERIC — idiom does not hard-code any specific tool, command,
// or project. Project-specific tools live in the host project's config
// (a top-level external_tools array in idiom's config.json, or a
// per-tool JSON file under workspace.external_tools_dir).
type ExternalTool struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Command        string          `json:"command"`
	Args           []string        `json:"args"`
	Cwd            string          `json:"cwd,omitempty"`
	TimeoutSeconds int             `json:"timeout_seconds,omitempty"`
	InputSchema    json.RawMessage `json:"input_schema,omitempty"`
	// ReadOnly declares that invoking this tool must not mutate project
	// source, generated artifacts, queues, network state, or other external
	// state. Idiom treats this as a host-project contract.
	ReadOnly bool `json:"read_only,omitempty"`
	// Discovery declares that the tool returns evidence for navigation,
	// lookup, tracing, impact analysis, or validation. Combined with ReadOnly
	// it lets narrow implementation lanes keep project-specific discovery
	// wrappers visible without exposing opaque mutating commands.
	Discovery bool `json:"discovery,omitempty"`
}

// ExternalToolRegistry holds the loaded ExternalTool definitions, keyed
// by Name. Lookups are O(1); the manager queries it once per Execute call
// before falling through to MCP / unsupported-tool.
type ExternalToolRegistry struct {
	tools map[string]ExternalTool
}

// NewExternalToolRegistry returns a registry pre-loaded with the provided
// tools. Returns an error if any tool name collides or is empty.
func NewExternalToolRegistry(tools []ExternalTool) (*ExternalToolRegistry, error) {
	reg := &ExternalToolRegistry{tools: make(map[string]ExternalTool, len(tools))}
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

// Names returns the registered tool names in undefined order. Used by the
// tool manifest + tool-definition builders.
func (r *ExternalToolRegistry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.tools))
	for name := range r.tools {
		out = append(out, name)
	}
	return out
}

// AvailableNames returns registered tool names whose command can be
// resolved from the same base directory Invoke uses. Unavailable tools
// remain registered so direct invocations can return a typed error, but
// they should not be advertised to models as callable affordances.
func (r *ExternalToolRegistry) AvailableNames(baseDir string) []string {
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
func (r *ExternalToolRegistry) Has(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.tools[name]
	return ok
}

// Tool returns the registered external tool definition.
func (r *ExternalToolRegistry) Tool(name string) (ExternalTool, bool) {
	if r == nil {
		return ExternalTool{}, false
	}
	t, ok := r.tools[strings.TrimSpace(name)]
	return t, ok
}

// Availability reports whether a registered external tool can be invoked
// from baseDir. The reason is stable enough for logs and direct CLI
// errors, but intentionally avoids raw input or environment details.
func (r *ExternalToolRegistry) Availability(name string, baseDir string) (bool, string) {
	tool, ok := r.Tool(name)
	if !ok {
		return false, "not registered"
	}
	if reason := externalToolUnavailableReason(tool, baseDir); reason != "" {
		return false, reason
	}
	return true, ""
}

// IsReadOnlyDiscovery reports whether the host project declared an external
// tool as a read-only discovery wrapper. Idiom does not infer safety from the
// command text or tool name; the project owns this contract explicitly.
func (r *ExternalToolRegistry) IsReadOnlyDiscovery(name string) bool {
	t, ok := r.Tool(name)
	return ok && t.ReadOnly && t.Discovery
}

// Definitions converts the registered tools into ToolDefinition entries
// suitable for BuildToolDefinitions. Schemas are passed through verbatim;
// an empty schema becomes the always-empty `{"type":"object","properties":{}}`.
func (r *ExternalToolRegistry) Definitions() []ToolDefinition {
	if r == nil {
		return nil
	}
	out := make([]ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		params := string(t.InputSchema)
		if strings.TrimSpace(params) == "" {
			params = `{"type":"object","properties":{}}`
		}
		out = append(out, ToolDefinition{
			Name:           t.Name,
			Description:    t.Description,
			ParametersJSON: params,
		})
	}
	return out
}

// DefinitionsForBaseDir converts available registered tools into
// ToolDefinition entries suitable for BuildToolDefinitions. Invalid or
// missing commands are omitted from the advertised model surface; direct
// execution still returns a typed unavailable-tool result.
func (r *ExternalToolRegistry) DefinitionsForBaseDir(baseDir string) []ToolDefinition {
	if r == nil {
		return nil
	}
	out := make([]ToolDefinition, 0, len(r.tools))
	for name, t := range r.tools {
		if ok, _ := r.Availability(name, baseDir); !ok {
			continue
		}
		params := string(t.InputSchema)
		if strings.TrimSpace(params) == "" {
			params = `{"type":"object","properties":{}}`
		}
		out = append(out, ToolDefinition{
			Name:           t.Name,
			Description:    t.Description,
			ParametersJSON: params,
		})
	}
	return out
}

// Invoke runs the registered tool with the provided JSON-encoded input.
// The input is passed via two channels:
//   - Each Args entry is template-substituted: `{key}` → string value of
//     input[key], `{?key:flag}` → `flag` if input[key] is truthy, else
//     dropped entirely (token elided from the argv slice).
//   - The full input JSON is also fed on stdin so tools that prefer JSON
//     over argv can ignore the templated args.
//
// No shell expansion is ever performed; arguments are passed via
// exec.Command's argv, so values containing spaces, quotes, or globs are
// safe.
func (r *ExternalToolRegistry) Invoke(ctx context.Context, name string, input json.RawMessage, baseDir string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("external tool %q: registry not initialised", name)
	}
	tool, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("external tool %q: not registered", name)
	}
	if ok, reason := r.Availability(name, baseDir); !ok {
		return "", fmt.Errorf("external tool %q: unavailable: %s", name, reason)
	}

	parsed := map[string]any{}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &parsed); err != nil {
			return "", fmt.Errorf("external tool %q: invalid input JSON: %w", name, err)
		}
	}

	args := make([]string, 0, len(tool.Args))
	for _, raw := range tool.Args {
		args = append(args, substituteArgs(raw, parsed)...)
	}

	timeout := time.Duration(tool.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, tool.Command, args...)
	if cwd := strings.TrimSpace(tool.Cwd); cwd != "" {
		cmd.Dir = expandCwd(cwd, baseDir)
	} else if baseDir != "" {
		cmd.Dir = baseDir
	}
	cmd.Stdin = strings.NewReader(string(input))
	if os.Getenv("IDIOM_DEBUG_TOOLS") != "" {
		fmt.Fprintf(os.Stderr, "[idiom-debug] external tool=%s argv=%v cwd=%s\n", name, args, cmd.Dir)
	}

	out, err := cmd.CombinedOutput()
	output := string(out)
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return output, fmt.Errorf("external tool %q: timed out after %s", name, timeout)
		}
		return output, fmt.Errorf("external tool %q: %w", name, err)
	}
	return output, nil
}

func externalToolUnavailableReason(tool ExternalTool, baseDir string) string {
	command := strings.TrimSpace(tool.Command)
	if command == "" {
		return "empty command"
	}
	if strings.ContainsRune(command, os.PathSeparator) {
		path := command
		if !filepath.IsAbs(path) {
			workingDir := externalToolWorkingDir(tool, baseDir)
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

func externalToolWorkingDir(tool ExternalTool, baseDir string) string {
	if cwd := strings.TrimSpace(tool.Cwd); cwd != "" {
		return expandCwd(cwd, baseDir)
	}
	return baseDir
}

// substituteArgs expands one argv template token into zero, one, or many
// argv tokens.
//
// Supported syntax:
//   - `{key}`           → input[key] as string (anywhere in the token).
//     Missing whole-token key → drop token; missing embedded key → empty string.
//     Whole-token array values expand to multiple argv
//     tokens, which supports CLIs like `--files FILES...`.
//   - `{key|default}`   → input[key] as string, else literal `default`.
//   - `{?key:value}`    → ONLY valid as a whole-token form. `value` if
//     input[key] is truthy, else drop the token.
//
// Plain `{key}` and `{key|default}` substitute IN PLACE so prefixed
// flag forms like `--{mode}` work. The conditional `{?…}` stays
// whole-token-only because its drop-token semantics don't compose with
// in-string interpolation.
//
// Truthy means: bool true, non-empty string, non-zero number. Maps and
// arrays are always treated as truthy if present.
func substituteArgs(template string, input map[string]any) []string {
	t := template

	// Whole-token conditional first: `{?key:flag}` either becomes `flag`
	// or drops the entire token. Mixing this with in-string subs would
	// be ambiguous ("did the prefix survive the drop?"), so it stays a
	// whole-token form.
	if strings.HasPrefix(t, "{?") && strings.HasSuffix(t, "}") {
		inner := t[2 : len(t)-1]
		colon := strings.Index(inner, ":")
		if colon < 0 {
			return []string{template} // malformed; pass through literal
		}
		key := inner[:colon]
		flag := inner[colon+1:]
		if isTruthy(input[key]) {
			return []string{flag}
		}
		return nil
	}

	// In-string substitution for `{key}` and `{key|default}`. Walks the
	// string left-to-right, replacing `{...}` segments. Unmatched braces
	// are left literal so non-template content (e.g. paths with curly
	// braces) is preserved.
	//
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
		// Quick path: copy literal runs until we hit `{`.
		if t[i] != '{' {
			b.WriteByte(t[i])
			i++
			continue
		}
		// Scan for the matching `}`.
		end := strings.IndexByte(t[i+1:], '}')
		if end < 0 {
			// Unterminated brace — copy verbatim and stop scanning.
			b.WriteString(t[i:])
			break
		}
		inner := t[i+1 : i+1+end]
		// Don't expand the conditional form mid-token; it's whole-token
		// only and won't appear here unless the user wrote a malformed
		// arg like `prefix{?…}` — leave it literal.
		if strings.HasPrefix(inner, "?") {
			b.WriteString(t[i : i+1+end+1])
			i = i + 1 + end + 1
			continue
		}
		expanded := expandSingle(inner, input)
		b.WriteString(expanded)
		i = i + 1 + end + 1
	}
	return []string{b.String()}
}

// expandSingle handles `{key}` and `{key|default}` semantics for one
// brace-segment (without surrounding braces). Used by substituteArg's
// in-string walk.
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
		// JSON unmarshal default for numbers; format without trailing zeros.
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

// expandCwd resolves a tool's cwd. Relative paths are resolved against
// baseDir (typically the workspace root). Absolute paths are returned
// unchanged. Empty input returns empty.
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
