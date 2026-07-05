package commandmeta

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	lanecontract "idiom/internal/contracts/lane"
)

func TestRootCommandManifestMatchesRootDispatch(t *testing.T) {
	got := parseRootDispatchCommands(t)
	want := []string{
		"architect",
		"artifact",
		"atom",
		"audit",
		"backend",
		"brief",
		"coding-rules",
		"context",
		"contract",
		"decision",
		"doctor",
		"feature-planner",
		"implement",
		"init",
		"lane",
		"local-ops",
		"mcp",
		"plan-execute",
		"probe",
		"product",
		"product-evidence",
		"profile",
		"queue",
		"ranker",
		"recover",
		"release",
		"run",
		"serve",
		"session",
		"skills",
		"tool",
		"version",
	}

	assertStringSet(t, got, want)
}

func TestDesktopAllowedCommandsMatchRustValidator(t *testing.T) {
	rustCommands := parseRustMatchArmStrings(t, "validate_command_name", "validate_command_args")
	want := []string{
		"architect",
		"artifact",
		"atom",
		"audit",
		"backend",
		"brief",
		"context",
		"doctor",
		"feature-planner",
		"implement",
		"init",
		"lane",
		"local-ops",
		"mcp",
		"plan-execute",
		"probe",
		"product",
		"product-evidence",
		"profile",
		"queue",
		"ranker",
		"recover",
		"release",
		"skills",
		"tool",
		"version",
	}

	assertStringSet(t, rustCommands, want)
	assertSubset(t, rustCommands, parseRootDispatchCommands(t), "desktop allowed command is not a root dispatch command")
}

func TestDesktopQuickActionsUseAllowedCommands(t *testing.T) {
	quickActionCommands := parseQuotedAssignments(t, desktopAppSource(t), `command:\s*"([^"]+)"`)
	allowedCommands := parseRustMatchArmStrings(t, "validate_command_name", "validate_command_args")

	if len(quickActionCommands) == 0 {
		t.Fatal("expected desktop quick actions to contain command requests")
	}

	assertSubset(t, quickActionCommands, allowedCommands, "desktop quick action uses command not allowed by Rust validator")
}

func TestDesktopLanePresetsMatchRustAndTypeScript(t *testing.T) {
	rustLanes := parseRustMatchArmStrings(t, "validate_lane", "validate_tool_name")
	tsLaneKind := parseQuotedAssignments(t, desktopCoreSource(t), `export type LaneKind\s*=\s*([^;]+);`)
	appLanes := parseQuotedAssignments(t, desktopAppSource(t), `const lanes:\s*LaneKind\[\]\s*=\s*\[([^\]]+)\]`)

	assertStringSet(t, tsLaneKind, rustLanes)
	assertStringSet(t, appLanes, rustLanes)
}

func TestDesktopLanePresetsRemainSplitFromCLILanes(t *testing.T) {
	desktopLanes := parseRustMatchArmStrings(t, "validate_lane", "validate_tool_name")
	cliLanes := laneIDsAsStrings()

	assertSubset(t, cliLanes, desktopLanes, "CLI lane is missing from desktop presets")
	if containsString(cliLanes, "atom-spec") {
		t.Fatal("atom-spec must remain a desktop lane preset, not a CLI lane ID")
	}
	if !containsString(desktopLanes, "atom-spec") {
		t.Fatal("desktop lane presets should continue to include atom-spec")
	}
}

func parseRootDispatchCommands(t *testing.T) []string {
	t.Helper()

	source := readRepoFile(t, "internal/surfaces/cli/root/root.go")
	commands := parseQuotedAssignments(t, source, `case\s+"([^"]+)"\s*:`)

	laneCasePattern := regexp.MustCompile(`case\s+string\(lanecontract\.(Lane\w+)\)\s*:`)
	laneValues := map[string]string{
		"LaneImplement":   string(lanecontract.LaneImplement),
		"LanePlanExecute": string(lanecontract.LanePlanExecute),
	}
	for _, match := range laneCasePattern.FindAllStringSubmatch(source, -1) {
		value, ok := laneValues[match[1]]
		if !ok {
			t.Fatalf("unmapped lanecontract case %q", match[1])
		}
		commands = append(commands, value)
	}

	return sortedUnique(commands)
}

func parseRustMatchArmStrings(t *testing.T, functionName string, nextFunctionName string) []string {
	t.Helper()

	source := readRepoFile(t, "desktop/src-tauri/src/main.rs")
	start := strings.Index(source, "fn "+functionName+"(")
	if start < 0 {
		t.Fatalf("missing Rust function %s", functionName)
	}
	end := strings.Index(source[start:], "fn "+nextFunctionName+"(")
	if end < 0 {
		t.Fatalf("missing Rust function %s after %s", nextFunctionName, functionName)
	}

	body := source[start : start+end]
	rejectArm := strings.Index(body, "_ =>")
	if rejectArm < 0 {
		t.Fatalf("missing reject arm in Rust function %s", functionName)
	}

	return parseQuotedAssignments(t, body[:rejectArm], `"([^"]+)"`)
}

func desktopCoreSource(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, "desktop/src/core/idiom.ts")
}

func desktopAppSource(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, "desktop/src/ui/App.tsx")
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

func parseQuotedAssignments(t *testing.T, source string, pattern string) []string {
	t.Helper()

	re := regexp.MustCompile(pattern)
	quoted := regexp.MustCompile(`"([^"]+)"`)
	var values []string
	for _, match := range re.FindAllStringSubmatch(source, -1) {
		quotedMatches := quoted.FindAllStringSubmatch(match[1], -1)
		if len(quotedMatches) == 0 {
			values = append(values, match[1])
			continue
		}
		for _, value := range quotedMatches {
			values = append(values, value[1])
		}
	}
	return sortedUnique(values)
}

func laneIDsAsStrings() []string {
	var values []string
	for _, lane := range lanecontract.LaneIDs() {
		values = append(values, string(lane))
	}
	return sortedUnique(values)
}

func assertSubset(t *testing.T, got []string, allowed []string, message string) {
	t.Helper()

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	for _, value := range got {
		if _, ok := allowedSet[value]; !ok {
			t.Fatalf("%s: %q not in %v", message, value, allowed)
		}
	}
}

func assertStringSet(t *testing.T, got []string, want []string) {
	t.Helper()

	got = sortedUnique(got)
	want = sortedUnique(want)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("string set mismatch\ngot:  %v\nwant: %v", got, want)
	}
}

func sortedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
