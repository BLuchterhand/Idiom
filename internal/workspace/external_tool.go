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

// Has reports whether name is registered.
func (r *ExternalToolRegistry) Has(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.tools[name]
	return ok
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

	parsed := map[string]any{}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &parsed); err != nil {
			return "", fmt.Errorf("external tool %q: invalid input JSON: %w", name, err)
		}
	}

	args := make([]string, 0, len(tool.Args))
	for _, raw := range tool.Args {
		expanded, keep := substituteArg(raw, parsed)
		if !keep {
			continue
		}
		args = append(args, expanded)
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

// substituteArg expands one argv template token. Returns (value, keep).
// If keep is false, the token is dropped entirely (used by `{?key:flag}`
// when the conditional evaluates false).
//
// Supported syntax:
//   - `{key}`           → input[key] as string (anywhere in the token).
//                         Missing key → empty string.
//   - `{key|default}`   → input[key] as string, else literal `default`.
//   - `{?key:value}`    → ONLY valid as a whole-token form. `value` if
//                         input[key] is truthy, else drop the token.
//
// Plain `{key}` and `{key|default}` substitute IN PLACE so prefixed
// flag forms like `--{mode}` work. The conditional `{?…}` stays
// whole-token-only because its drop-token semantics don't compose with
// in-string interpolation.
//
// Truthy means: bool true, non-empty string, non-zero number. Maps and
// arrays are always treated as truthy if present.
func substituteArg(template string, input map[string]any) (string, bool) {
	t := template

	// Whole-token conditional first: `{?key:flag}` either becomes `flag`
	// or drops the entire token. Mixing this with in-string subs would
	// be ambiguous ("did the prefix survive the drop?"), so it stays a
	// whole-token form.
	if strings.HasPrefix(t, "{?") && strings.HasSuffix(t, "}") {
		inner := t[2 : len(t)-1]
		colon := strings.Index(inner, ":")
		if colon < 0 {
			return template, true // malformed; pass through literal
		}
		key := inner[:colon]
		flag := inner[colon+1:]
		if isTruthy(input[key]) {
			return flag, true
		}
		return "", false
	}

	// In-string substitution for `{key}` and `{key|default}`. Walks the
	// string left-to-right, replacing `{...}` segments. Unmatched braces
	// are left literal so non-template content (e.g. paths with curly
	// braces) is preserved.
	//
	// Empty-render-drops-token rule: if the template is a SINGLE `{key}`
	// (no prefix/suffix, no default) and the input key is missing,
	// drop the whole token. This keeps args clean when the model omits
	// optional fields — without this, you get empty-string args which
	// most CLIs interpret as "an actual empty argument" and reject.
	// Templates like `prefix{key}` always keep the token (rendering as
	// "prefix" if key is missing) since the prefix indicates intent.
	if strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") && !strings.HasPrefix(t, "{?") {
		inner := t[1 : len(t)-1]
		// Plain `{key}` with no default and missing key → drop token.
		if !strings.Contains(inner, "|") {
			if _, ok := input[inner]; !ok {
				return "", false
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
	return b.String(), true
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
