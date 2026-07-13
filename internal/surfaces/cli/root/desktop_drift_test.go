package root

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	lanecontract "idiom/internal/contracts/lane"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Desktop and CLI command contracts", func() {
	ginkgo.It("keeps the root command manifest aligned with root dispatch", func() {
		got := parseRootDispatchCommands()
		want := []string{
			"architect", "artifact", "atom", "audit", "backend", "brief", "coding-rules",
			"contract", "decision", "doctor", "feature-planner", "implement", "init", "lane",
			"local-ops", "mcp", "plan-execute", "probe", "product", "product-evidence", "profile",
			"queue", "ranker", "recover", "release", "run", "serve", "session", "skills", "tool", "version",
		}

		gomega.Expect(sortedUnique(got)).To(gomega.Equal(sortedUnique(want)))
	})

	ginkgo.It("keeps desktop commands aligned with the Rust validator and root dispatch", func() {
		rustCommands := parseRustMatchArmStrings("validate_command_name", "validate_command_args")
		want := []string{
			"architect", "artifact", "atom", "audit", "backend", "brief", "doctor", "feature-planner",
			"implement", "init", "lane", "local-ops", "mcp", "plan-execute", "probe", "product",
			"product-evidence", "profile", "queue", "ranker", "recover", "release", "skills", "tool", "version",
		}

		gomega.Expect(sortedUnique(rustCommands)).To(gomega.Equal(sortedUnique(want)))
		expectSubset(rustCommands, parseRootDispatchCommands(), "desktop allowed command is not a root dispatch command")
	})

	ginkgo.It("keeps desktop quick actions inside the Rust command allowlist", func() {
		quickActionCommands := parseQuotedAssignments(desktopAppSource(), `command:\s*"([^"]+)"`)
		allowedCommands := parseRustMatchArmStrings("validate_command_name", "validate_command_args")

		gomega.Expect(quickActionCommands).NotTo(gomega.BeEmpty())
		expectSubset(quickActionCommands, allowedCommands, "desktop quick action uses command not allowed by Rust validator")
	})

	ginkgo.It("keeps desktop lane presets aligned across Rust and TypeScript", func() {
		rustLanes := parseRustMatchArmStrings("validate_lane", "validate_tool_name")
		tsLaneKind := parseQuotedAssignments(desktopCoreSource(), `export type LaneKind\s*=\s*([^;]+);`)
		appLanes := parseQuotedAssignments(desktopAppSource(), `const lanes:\s*LaneKind\[\]\s*=\s*\[([^\]]+)\]`)

		gomega.Expect(sortedUnique(tsLaneKind)).To(gomega.Equal(sortedUnique(rustLanes)))
		gomega.Expect(sortedUnique(appLanes)).To(gomega.Equal(sortedUnique(rustLanes)))
	})

	ginkgo.It("keeps atom-spec as a desktop-only lane preset", func() {
		desktopLanes := parseRustMatchArmStrings("validate_lane", "validate_tool_name")
		cliLanes := laneIDsAsStrings()

		expectSubset(cliLanes, desktopLanes, "CLI lane is missing from desktop presets")
		gomega.Expect(cliLanes).NotTo(gomega.ContainElement("atom-spec"))
		gomega.Expect(desktopLanes).To(gomega.ContainElement("atom-spec"))
	})
})

func parseRootDispatchCommands() []string {
	source := readRepoFile("internal/surfaces/cli/root/root.go")
	commands := parseQuotedAssignments(source, `case\s+"([^"]+)"\s*:`)

	laneCasePattern := regexp.MustCompile(`case\s+string\(lanecontract\.(Lane\w+)\)\s*:`)
	laneValues := map[string]string{
		"LaneImplement":   string(lanecontract.LaneImplement),
		"LanePlanExecute": string(lanecontract.LanePlanExecute),
	}
	for _, match := range laneCasePattern.FindAllStringSubmatch(source, -1) {
		value, ok := laneValues[match[1]]
		gomega.Expect(ok).To(gomega.BeTrue(), "unmapped lanecontract case %q", match[1])
		commands = append(commands, value)
	}

	return sortedUnique(commands)
}

func parseRustMatchArmStrings(functionName, nextFunctionName string) []string {
	source := readRepoFile("desktop/src-tauri/src/main.rs")
	start := strings.Index(source, "fn "+functionName+"(")
	gomega.Expect(start).To(gomega.BeNumerically(">=", 0), "missing Rust function %s", functionName)
	end := strings.Index(source[start:], "fn "+nextFunctionName+"(")
	gomega.Expect(end).To(gomega.BeNumerically(">=", 0), "missing Rust function %s after %s", nextFunctionName, functionName)

	body := source[start : start+end]
	rejectArm := strings.Index(body, "_ =>")
	gomega.Expect(rejectArm).To(gomega.BeNumerically(">=", 0), "missing reject arm in Rust function %s", functionName)
	return parseQuotedAssignments(body[:rejectArm], `"([^"]+)"`)
}

func desktopCoreSource() string {
	return readRepoFile("desktop/src/core/idiom.ts")
}

func desktopAppSource() string {
	return readRepoFile("desktop/src/ui/App.tsx")
}

func readRepoFile(rel string) string {
	data, err := os.ReadFile(filepath.Join(repoRoot(), filepath.FromSlash(rel)))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read %s", rel)
	return string(data)
}

func repoRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	gomega.Expect(ok).To(gomega.BeTrue(), "runtime.Caller failed")
	root, err := filepath.Abs(filepath.Join(filepath.Dir(filename), "..", "..", "..", ".."))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return root
}

func parseQuotedAssignments(source, pattern string) []string {
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

func expectSubset(got, allowed []string, message string) {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	for _, value := range got {
		_, ok := allowedSet[value]
		gomega.Expect(ok).To(gomega.BeTrue(), "%s: %q not in %v", message, value, allowed)
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
