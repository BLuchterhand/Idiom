package contractcompile_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("keeps repository tests on Ginkgo and async assertions recoverable", func() {
	root := repoRoot()
	fset := token.NewFileSet()
	testDirs := map[string]bool{}
	testFilesByDir := map[string][]*ast.File{}
	suiteCounts := map[string]int{}
	var violations []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "testdata", "vendor":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		file, parseErr := parser.ParseFile(fset, path, source, parser.ParseComments)
		if parseErr != nil {
			return parseErr
		}
		appendMockFrameworkViolations(root, fset, file, &violations)

		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		dir := filepath.Dir(path)
		testDirs[dir] = true
		testFilesByDir[dir] = append(testFilesByDir[dir], file)
		testingPackages := testingPackageNames(file)

		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Type == nil {
				continue
			}
			if hasTestingTParameter(function.Type.Params) && !nativeTestException(root, path, function) {
				violations = append(violations, frameworkViolation(root, fset.Position(function.Pos()), "testing.T function "+function.Name.Name))
			}
			if hasTestingBParameter(function.Type.Params) && !nativeBenchmarkException(root, path, function) {
				violations = append(violations, frameworkViolation(root, fset.Position(function.Pos()), "testing.B function "+function.Name.Name))
			}
			if hasTestingMParameter(function.Type.Params) && !nativeTestMainException(root, path, function) {
				violations = append(violations, frameworkViolation(root, fset.Position(function.Pos()), "testing.M function "+function.Name.Name))
			}
		}

		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CallExpr:
				if callName(typed.Fun) == "RunSpecs" {
					suiteCounts[dir]++
				}
				if isTestingSubtestCall(typed) {
					violations = append(violations, frameworkViolation(root, fset.Position(typed.Pos()), "testing subtest"))
				}
				if isConstantFailureExpectation(typed) {
					violations = append(violations, frameworkViolation(root, fset.Position(typed.Pos()), "constant-failure Gomega assertion"))
				}
				if isCompoundBooleanExpectation(typed) {
					violations = append(violations, frameworkViolation(root, fset.Position(typed.Pos()), "compound-boolean Gomega assertion"))
				}
				if isTestingShortCall(typed, testingPackages) {
					violations = append(violations, frameworkViolation(root, fset.Position(typed.Pos()), "native testing.Short control in Ginkgo suite"))
				}
				if isHTTPHandlerFunc(typed.Fun) && len(typed.Args) > 0 {
					if literal, ok := typed.Args[0].(*ast.FuncLit); ok {
						appendUnsafeAsyncViolation(root, fset, literal, "HTTP handler", &violations)
					}
				}
			case *ast.GoStmt:
				if literal, ok := typed.Call.Fun.(*ast.FuncLit); ok {
					appendUnsafeAsyncViolation(root, fset, literal, "goroutine", &violations)
				}
			}
			return true
		})

		return nil
	})
	Expect(err).NotTo(HaveOccurred(), "scan test framework architecture")

	for dir := range testDirs {
		if suiteCounts[dir] != 1 {
			rel, _ := filepath.Rel(root, dir)
			violations = append(violations, filepath.ToSlash(rel)+": expected exactly one RunSpecs bootstrap, found "+strconv.Itoa(suiteCounts[dir]))
		}

		packageLabeled := false
		for _, file := range testFilesByDir[dir] {
			if fileHasAnyLabel(file, "e2e", "integration") {
				packageLabeled = true
				break
			}
		}
		for _, file := range testFilesByDir[dir] {
			appendLabeledTestDoubleViolations(root, fset, file, packageLabeled, &violations)
		}
	}

	sort.Strings(violations)
	Expect(violations).To(BeEmpty(), "test framework architecture violations:\n%s", strings.Join(violations, "\n"))
})

var _ = It("keeps authoritative verification commands on executable Ginkgo selectors", func() {
	root := repoRoot()
	var violations []string
	for _, relative := range authoritativeVerificationSelectorFiles {
		path := filepath.Join(root, filepath.FromSlash(relative))
		source, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred(), "read authoritative verification command source %s", relative)
		for _, location := range staleNativeGoTestSelector.FindAllIndex(source, -1) {
			line := 1 + strings.Count(string(source[:location[0]]), "\n")
			violations = append(violations, relative+":"+strconv.Itoa(line)+": native -run Test selector executes no migrated Ginkgo spec")
		}
		for _, location := range focusedGinkgoGoTestCommand.FindAllIndex(source, -1) {
			if strings.Contains(string(source[location[0]:location[1]]), "-ginkgo.fail-on-empty") {
				continue
			}
			line := 1 + strings.Count(string(source[:location[0]]), "\n")
			violations = append(violations, relative+":"+strconv.Itoa(line)+": focused Ginkgo command must fail when its selector matches no specs")
		}
		for _, location := range labelFilteredGinkgoCommand.FindAllIndex(source, -1) {
			command := string(source[location[0]:location[1]])
			if strings.Contains(command, "--fail-on-empty") || strings.Contains(command, "-ginkgo.fail-on-empty") {
				continue
			}
			line := 1 + strings.Count(string(source[:location[0]]), "\n")
			violations = append(violations, relative+":"+strconv.Itoa(line)+": label-filtered Ginkgo command must fail when its selector matches no specs")
		}
	}

	sort.Strings(violations)
	Expect(violations).To(BeEmpty(), "authoritative verification selector violations:\n%s", strings.Join(violations, "\n"))
})

var _ = It("keeps repository script behavior checks under Ginkgo subprocess ownership", func() {
	root := repoRoot()
	var nativePythonTests []string
	for _, pattern := range []string{"test_*.py", "*_test.py"} {
		matches, err := filepath.Glob(filepath.Join(root, "scripts", pattern))
		Expect(err).NotTo(HaveOccurred())
		for _, match := range matches {
			relative, relErr := filepath.Rel(root, match)
			Expect(relErr).NotTo(HaveOccurred())
			nativePythonTests = append(nativePythonTests, filepath.ToSlash(relative))
		}
	}
	sort.Strings(nativePythonTests)
	Expect(nativePythonTests).To(BeEmpty(), "script behavior assertions must live in Go Ginkgo specs that execute production scripts as subprocess peers")
})

var _ = DescribeTable("recognizes stale native test selectors in authoritative commands",
	func(command string, stale bool) {
		Expect(staleNativeGoTestSelector.MatchString(command)).To(Equal(stale))
	},
	Entry("rejects an unquoted native selector", "go test ./internal/example -run TestContract", true),
	Entry("rejects a quoted native selector expression", "go test ./internal/example -run '^TestContract$' -count=1", true),
	Entry("accepts a Ginkgo focus", "go test ./internal/example -ginkgo.focus 'contract behavior' -ginkgo.fail-on-empty", false),
	Entry("accepts package-wide execution", "go test ./internal/example", false),
)

var _ = DescribeTable("recognizes label-filtered commands that fail on empty selection",
	func(command string, guarded bool) {
		match := labelFilteredGinkgoCommand.FindString(command)
		Expect(match).NotTo(BeEmpty())
		Expect(strings.Contains(match, "--fail-on-empty") || strings.Contains(match, "-ginkgo.fail-on-empty")).To(Equal(guarded))
	},
	Entry("accepts the Ginkgo CLI fail-on-empty flag", "go tool ginkgo --label-filter='smoke' --fail-on-empty ./internal/example", true),
	Entry("accepts the go test fail-on-empty flag", "go test ./internal/example -ginkgo.label-filter='smoke' -ginkgo.fail-on-empty", true),
	Entry("rejects an unguarded Ginkgo CLI label filter", "go tool ginkgo --label-filter='smoke' ./internal/example", false),
)

var staleNativeGoTestSelector = regexp.MustCompile(`\bgo test[^\r\n]*\s-run(?:=|\s+)[^\r\n]*Test[A-Za-z0-9_(.*^$?]`)
var focusedGinkgoGoTestCommand = regexp.MustCompile(`\bgo test(?:\\[^\"]|[^\"\\\r\n])*-ginkgo\.focus(?:\\[^\"]|[^\"\\\r\n])*`)
var labelFilteredGinkgoCommand = regexp.MustCompile(`(?m)[^\r\n]*(?:--label-filter|-ginkgo\.label-filter)[^\r\n]*`)

var authoritativeVerificationSelectorFiles = []string{
	".github/workflows/ci.yml",
	".github/workflows/release.yml",
	"Makefile",
	"docs/client-demo-transcripts.md",
	"docs/mcp-classification-parity.md",
	"docs/rearchitecture-reduction-plan.md",
	"docs/security-privacy-posture.matrix.json",
	"docs/security-privacy-posture.md",
	"docs/sidecar-integration-proofs.md",
	"docs/sidecar-recipes.md",
	"internal/evidence/sidecarvalidation/client_demo_transcripts_test.go",
	"internal/evidence/sidecarvalidation/sidecar_recipe_test.go",
	"internal/surfaces/cli/featureplanner/feature_planner_test.go",
	"internal/surfaces/cli/mcp/p8_5_classification_parity_test.go",
	"internal/surfaces/tui/app/feature_planner_product_test.go",
	"testdata/product-workflows/prd-adr-brief-workflow/tdd.json",
	"testdata/scripted-contracts/scripts/scripted-feature-planner-tui-approval-chat-interactivity.json",
	"testdata/scripted-contracts/scripts/scripted-feature-planner-tui-rejects-unverified-completion.json",
	"testdata/scripted-contracts/scripts/scripted-feature-planner-tui-state.json",
	"testdata/scripted-contracts/scripts/scripted-feature-planner-tui-tdd-boundary-value-atom.json",
	"testdata/scripted-contracts/scripts/scripted-feature-planner-tui-tdd-fault-tree-atom.json",
	"testdata/scripted-contracts/scripts/scripted-feature-planner-tui-tdd-formal-verification-atom.json",
	"testdata/scripted-contracts/scripts/scripted-feature-planner-tui-tdd-post-mortem-atom.json",
	"testdata/scripted-contracts/scripts/scripted-feature-planner-tui-tdd-state-machine-atom.json",
	"testdata/sidecar/integration-proofs.json",
}

var _ = DescribeTable("permits only native Go benchmarks in dedicated benchmark files",
	func(path string, source string, allowed bool) {
		function := parseFrameworkGuardFunction(source)
		Expect(hasTestingBParameter(function.Type.Params)).To(BeTrue())
		Expect(nativeBenchmarkException("/repo", path, function)).To(Equal(allowed))
	},
	Entry("accepts the full graph benchmark", "/repo/internal/repository/comprehension/graph_benchmark_test.go", `package sample
import "testing"
func BenchmarkBuildFullGoRepository(b *testing.B) {}`,
		true),
	Entry("accepts the incremental graph benchmark", "/repo/internal/repository/comprehension/graph_benchmark_test.go", `package sample
import "testing"
func BenchmarkBuildExactScopeIncremental(b *testing.B) {}`,
		true),
	Entry("rejects an otherwise valid unlisted benchmark", "/repo/internal/repository/comprehension/graph_benchmark_test.go", `package sample
import "testing"
func BenchmarkGraph(b *testing.B) {}`,
		false),
	Entry("rejects a benchmark outside the audited file", "/repo/internal/repository/comprehension/other_benchmark_test.go", `package sample
import "testing"
func BenchmarkBuildFullGoRepository(b *testing.B) {}`,
		false),
	Entry("rejects a testing.B helper", "/repo/internal/repository/comprehension/graph_benchmark_test.go", `package sample
import "testing"
func buildGraph(b *testing.B) {}`,
		false),
	Entry("rejects a name the Go benchmark loader ignores", "/repo/internal/repository/comprehension/graph_benchmark_test.go", `package sample
import "testing"
func Benchmarkgraph(b *testing.B) {}`,
		false),
	Entry("rejects an extra parameter", "/repo/internal/repository/comprehension/graph_benchmark_test.go", `package sample
import "testing"
func BenchmarkBuildFullGoRepository(b *testing.B, size int) {}`,
		false),
	Entry("rejects a result", "/repo/internal/repository/comprehension/graph_benchmark_test.go", `package sample
import "testing"
func BenchmarkBuildFullGoRepository(b *testing.B) error { return nil }`,
		false),
	Entry("rejects a method", "/repo/internal/repository/comprehension/graph_benchmark_test.go", `package sample
import "testing"
type graph struct{}
func (graph) BenchmarkBuildFullGoRepository(b *testing.B) {}`,
		false),
)

var _ = DescribeTable("permits only audited native testing entrypoints",
	func(path string, source string, allowed bool) {
		function := parseFrameworkGuardFunction(source)
		Expect(nativeTestException("/repo", path, function)).To(Equal(allowed))
	},
	Entry("accepts the MCP subprocess helper", "/repo/internal/capabilities/mcp/client_test.go", `package sample
import "testing"
func TestMCPStdioHelperProcess(t *testing.T) {}`,
		true),
	Entry("accepts the ranker lock subprocess helper", "/repo/internal/repository/comprehension/ranker_state_store_test.go", `package sample
import "testing"
func TestRankerStateLockHelperProcess(t *testing.T) {}`,
		true),
	Entry("accepts the workspace append subprocess helper", "/repo/internal/capabilities/workspace/workspace_path_identity_reopen_development_e2e_test.go", `package sample
import "testing"
func TestWorkspaceExternalAppendHelper(t *testing.T) {}`,
		true),
	Entry("accepts the workspace MCP stdio subprocess helper", "/repo/internal/capabilities/workspace/tool_surface_setup_test.go", `package sample
import "testing"
func TestWorkspaceMCPStdioHelperProcess(t *testing.T) {}`,
		true),
	Entry("rejects the same helper name in another file", "/repo/internal/capabilities/mcp/other_test.go", `package sample
import "testing"
func TestMCPStdioHelperProcess(t *testing.T) {}`,
		false),
	Entry("rejects an unaudited HelperProcess name", "/repo/internal/capabilities/mcp/client_test.go", `package sample
import "testing"
func TestAnotherHelperProcess(t *testing.T) {}`,
		false),
	Entry("accepts a suite bootstrap that invokes RunSpecs", "/repo/internal/sample/sample_suite_test.go", `package sample
import "testing"
func TestSample(t *testing.T) { RunSpecs(t, "Sample") }`,
		true),
	Entry("rejects a suite-file test without RunSpecs", "/repo/internal/sample/sample_suite_test.go", `package sample
import "testing"
func TestSample(t *testing.T) {}`,
		false),
)

var _ = DescribeTable("permits TestMain only at its audited owner",
	func(path string, allowed bool) {
		function := parseFrameworkGuardFunction(`package sample
import "testing"
func TestMain(m *testing.M) {}`)
		Expect(nativeTestMainException("/repo", path, function)).To(Equal(allowed))
	},
	Entry("accepts the root CLI owner", "/repo/internal/surfaces/cli/root/testmain_test.go", true),
	Entry("rejects another package", "/repo/internal/surfaces/cli/other/testmain_test.go", false),
)

var _ = DescribeTable("rejects native testing.Short control in behavioral specs",
	func(source string, forbidden bool) {
		file := parseFrameworkGuardFile(source)
		Expect(fileContainsTestingShortCall(file)).To(Equal(forbidden))
	},
	Entry("rejects the standard import", `package sample
import "testing"
var _ = It("runs", func() { if testing.Short() {} })`, true),
	Entry("rejects an aliased import", `package sample
import stdtesting "testing"
var _ = It("runs", func() { if stdtesting.Short() {} })`, true),
	Entry("rejects a dot import", `package sample
import . "testing"
var _ = It("runs", func() { if Short() {} })`, true),
	Entry("accepts Ginkgo skip", `package sample
var _ = It("runs", func() { Skip("live label owns selection") })`, false),
)

var _ = DescribeTable("rejects legacy testing framework imports",
	func(path string, forbidden bool) {
		Expect(forbiddenTestFrameworkImport(path) != "").To(Equal(forbidden))
	},
	Entry("Testify", "github.com/stretchr/testify/require", true),
	Entry("legacy GoMock", "github.com/golang/mock/gomock", true),
	Entry("mockery", "github.com/vektra/mockery/v2/pkg", true),
	Entry("modern GoMock", "go.uber.org/mock/gomock", false),
	Entry("Gomega", "github.com/onsi/gomega", false),
)

var _ = DescribeTable("recognizes generated interface mocks without treating domain names as mocks",
	func(source string, generatedHeaderRequired bool) {
		file := parseFrameworkGuardFile(source)
		Expect(fileContainsGomockInterfaceMock(file)).To(Equal(generatedHeaderRequired))
	},
	Entry("GoMock controller structure", `package sample
import gomock "go.uber.org/mock/gomock"
type MockClock struct { ctrl *gomock.Controller }`, true),
	Entry("ordinary domain type", `package sample
type MockingbirdMigration struct { Name string }`, false),
	Entry("ordinary type whose field mentions a controller", `package sample
import gomock "go.uber.org/mock/gomock"
type ClockHarness struct { ctrl *gomock.Controller }`, false),
)

var _ = DescribeTable("requires the MockGen header on generated interface mocks",
	func(source string, missing bool) {
		file := parseFrameworkGuardFile(source)
		Expect(gomockInterfaceMockMissingHeader(file)).To(Equal(missing))
	},
	Entry("rejects a generated-shaped declaration without the header", `package sample
import gomock "go.uber.org/mock/gomock"
type MockClock struct { ctrl *gomock.Controller }`, true),
	Entry("accepts MockGen output", `// Code generated by MockGen. DO NOT EDIT.
package sample
import gomock "go.uber.org/mock/gomock"
type MockClock struct { ctrl *gomock.Controller }`, false),
	Entry("accepts a build constraint before the generated header", `//go:build linux

// Code generated by MockGen. DO NOT EDIT.
package sample
import gomock "go.uber.org/mock/gomock"
type MockClock struct { ctrl *gomock.Controller }`, false),
	Entry("does not require a header for a domain type", `package sample
type MockingbirdMigration struct { Name string }`, false),
	Entry("does not accept a displaced generated header", `package sample
// Code generated by MockGen. DO NOT EDIT.
import gomock "go.uber.org/mock/gomock"
type MockClock struct { ctrl *gomock.Controller }`, true),
)

var _ = DescribeTable("classifies manual test doubles in labeled specifications narrowly",
	func(source string, expectedNames ...string) {
		file := parseFrameworkGuardFile(source)
		Expect(unclassifiedLabeledTestDoubleNames(file)).To(ConsistOf(expectedNames))
	},
	Entry("rejects a manual fake in e2e", `package sample
var _ = It("runs", Label("e2e"), func(){})
type fakeClock struct{}`, "fakeClock"),
	Entry("rejects a camel-cased spy in integration", `package sample
var _ = It("runs", Label("integration"), func(){})
type requestSpyRecorder struct{}`, "requestSpyRecorder"),
	Entry("rejects a recording interface double in e2e", `package sample
var _ = It("runs", Label("e2e"), func(){})
type recordingMutationApprover struct{}`, "recordingMutationApprover"),
	Entry("permits a scripted protocol peer classification", `package sample
var _ = It("runs", Label("e2e"), func(){})
// test-double: scripted-protocol-peer
type fakeTransport struct{}`),
	Entry("permits an observation probe classification", `package sample
var _ = It("runs", Label("integration"), func(){})
// test-double: observation-probe
type laneFakeRunner struct{}`),
	Entry("recognizes a scripted peer marker independent of role spelling", `package sample
var _ = It("runs", Label("e2e"), func(){})
// test-double: scripted-protocol-peer
type Harness struct{}`),
	Entry("rejects an unclassified approver", `package sample
var _ = It("runs", Label("integration"), func(){})
type delayedApprover struct{}`, "delayedApprover"),
	Entry("rejects an unclassified model suffix", `package sample
var _ = It("runs", Label("e2e"), func(){})
type observedProgramModel struct{}`, "observedProgramModel"),
	Entry("does not govern an unlabeled unit test", `package sample
var _ = It("runs", func(){})
type fakeClock struct{}`),
	Entry("does not mistake a domain word for a role", `package sample
var _ = It("runs", Label("e2e"), func(){})
type MockingbirdMigration struct{}`),
)

var _ = It("applies e2e and integration labels to package helper files", func() {
	labeled := parseFrameworkGuardFile(`package sample
var _ = It("runs", Label("e2e"), func(){})`)
	helper := parseFrameworkGuardFile(`package sample
type delayedApprover struct{}`)

	packageLabeled := fileHasAnyLabel(labeled, "e2e", "integration")
	Expect(unclassifiedPackageTestDoubleNames(helper, packageLabeled)).To(ConsistOf("delayedApprover"))
})

var _ = It("keeps labeled suites reachable through their verification targets", func() {
	root := repoRoot()
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	Expect(err).NotTo(HaveOccurred())

	labelsByDir, violations, err := testLabelsByDirectory(root)
	Expect(err).NotTo(HaveOccurred())

	targets := verificationTargetExpectations()
	for _, expectation := range targets {
		recipe := makeTargetRecipe(string(makefile), expectation.target)
		if !strings.Contains(recipe, "--label-filter") || !strings.Contains(recipe, expectation.label) {
			violations = append(violations, "Makefile: make "+expectation.target+" does not select label "+strconv.Quote(expectation.label))
		}
		if expectation.race && !strings.Contains(recipe, "--race") {
			violations = append(violations, "Makefile: make "+expectation.target+" does not enable the race detector")
		}
	}

	for dir, labels := range labelsByDir {
		rel, relErr := filepath.Rel(root, dir)
		Expect(relErr).NotTo(HaveOccurred())
		packageRef := "./" + filepath.ToSlash(rel)

		for _, expectation := range targets {
			if !labels[expectation.label] {
				continue
			}
			if makeTargetContainsPackage(string(makefile), expectation.target, packageRef, expectation.variables...) {
				continue
			}
			violations = append(violations, packageRef+": label "+strconv.Quote(expectation.label)+" is not reachable through make "+expectation.target)
		}
	}

	buildTagRequirements, buildTagErr := labeledBuildTagRequirements(root)
	Expect(buildTagErr).NotTo(HaveOccurred())
	for _, requirement := range buildTagRequirements {
		expectation, found := verificationTargetForLabel(requirement.label)
		if !found {
			continue
		}
		rel, relErr := filepath.Rel(root, requirement.path)
		Expect(relErr).NotTo(HaveOccurred())
		packageRef := "./" + filepath.ToSlash(filepath.Dir(rel))
		if !makeTargetContainsPackage(string(makefile), expectation.target, packageRef, expectation.variables...) {
			violations = append(violations, filepath.ToSlash(rel)+": build tag "+strconv.Quote(requirement.tag)+" has no package route through make "+expectation.target)
			continue
		}
		if !makeTargetSupportsBuildTag(string(makefile), expectation.target, requirement.tag) {
			violations = append(violations, filepath.ToSlash(rel)+": build tag "+strconv.Quote(requirement.tag)+" is not enabled by make "+expectation.target)
		}
	}

	sort.Strings(violations)
	Expect(violations).To(BeEmpty(), "labeled-suite target violations:\n%s", strings.Join(violations, "\n"))
})

type verificationTargetExpectation struct {
	label     string
	target    string
	variables []string
	race      bool
}

func verificationTargetExpectations() []verificationTargetExpectation {
	return []verificationTargetExpectation{
		{label: "e2e", target: "test-e2e", variables: []string{"GINKGO_E2E_PACKAGES"}},
		{label: "integration", target: "test-e2e", variables: []string{"GINKGO_E2E_PACKAGES"}},
		{label: "smoke", target: "test-smoke", variables: []string{"GINKGO_SMOKE_PACKAGES"}},
		{label: "development-scenario", target: "test-development-scenarios", variables: []string{"GINKGO_DEVELOPMENT_SCENARIO_PACKAGES"}},
		{label: "race-contract", target: "test-race-contracts", variables: []string{"GINKGO_RACE_CONTRACT_PACKAGES"}, race: true},
		{label: "live", target: "test-live"},
	}
}

func verificationTargetForLabel(label string) (verificationTargetExpectation, bool) {
	for _, expectation := range verificationTargetExpectations() {
		if expectation.label == label {
			return expectation, true
		}
	}
	return verificationTargetExpectation{}, false
}

func testLabelsByDirectory(root string) (map[string]map[string]bool, []string, error) {
	fset := token.NewFileSet()
	labelsByDir := make(map[string]map[string]bool)
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "testdata", "vendor":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		dir := filepath.Dir(path)
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || callName(call.Fun) != "Label" {
				return true
			}
			callLabels := make(map[string]bool)
			if labelsByDir[dir] == nil {
				labelsByDir[dir] = make(map[string]bool)
			}
			for _, argument := range call.Args {
				literal, ok := argument.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				label, unquoteErr := strconv.Unquote(literal.Value)
				if unquoteErr == nil {
					labelsByDir[dir][label] = true
					callLabels[label] = true
				}
			}
			if callLabels["smoke"] && !callLabels["e2e"] {
				violations = append(violations, frameworkViolation(root, fset.Position(call.Pos()), "smoke label must include e2e"))
			}
			return true
		})
		return nil
	})
	return labelsByDir, violations, err
}

type labeledBuildTagRequirement struct {
	path  string
	label string
	tag   string
}

func labeledBuildTagRequirements(root string) ([]labeledBuildTagRequirement, error) {
	fset := token.NewFileSet()
	requirements := make(map[string]labeledBuildTagRequirement)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "testdata", "vendor":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		buildTags := explicitTestBuildTags(string(source))
		if len(buildTags) == 0 {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, source, 0)
		if parseErr != nil {
			return parseErr
		}
		for _, labels := range testLabelCallSets(file) {
			for _, label := range verificationLabelsForCall(labels) {
				for _, tag := range buildTags {
					key := path + "\x00" + label + "\x00" + tag
					requirements[key] = labeledBuildTagRequirement{path: path, label: label, tag: tag}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	result := make([]labeledBuildTagRequirement, 0, len(requirements))
	for _, requirement := range requirements {
		result = append(result, requirement)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].path != result[j].path {
			return result[i].path < result[j].path
		}
		if result[i].label != result[j].label {
			return result[i].label < result[j].label
		}
		return result[i].tag < result[j].tag
	})
	return result, nil
}

func explicitTestBuildTags(source string) []string {
	header := source
	if packageIndex := strings.Index(header, "package "); packageIndex >= 0 {
		header = header[:packageIndex]
	}
	var tags []string
	for _, tag := range []string{"integration", "qwen_e2e", "system_test"} {
		if strings.Contains(header, tag) {
			tags = append(tags, tag)
		}
	}
	return tags
}

func testLabelCallSets(file *ast.File) []map[string]bool {
	var result []map[string]bool
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || callName(call.Fun) != "Label" {
			return true
		}
		labels := make(map[string]bool)
		for _, argument := range call.Args {
			literal, ok := argument.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			label, err := strconv.Unquote(literal.Value)
			if err == nil {
				labels[label] = true
			}
		}
		result = append(result, labels)
		return true
	})
	return result
}

func verificationLabelsForCall(labels map[string]bool) []string {
	selected := make(map[string]bool)
	if labels["live"] {
		selected["live"] = true
	}
	if !labels["live"] {
		for _, label := range []string{"e2e", "integration", "smoke", "development-scenario"} {
			selected[label] = labels[label]
		}
	}
	selected["race-contract"] = labels["race-contract"]

	var result []string
	for label, included := range selected {
		if included {
			result = append(result, label)
		}
	}
	sort.Strings(result)
	return result
}

func makeTargetContainsPackage(makefile string, target string, packageRef string, variables ...string) bool {
	search := makeTargetRecipe(makefile, target)
	for _, variable := range variables {
		search += "\n" + makeVariableValue(makefile, variable)
	}
	for _, field := range strings.Fields(search) {
		if strings.TrimSpace(strings.TrimSuffix(field, "\\")) == packageRef {
			return true
		}
	}
	return false
}

func makeTargetSupportsBuildTag(makefile string, target string, tag string) bool {
	recipe := makeTargetRecipe(makefile, target)
	return strings.Contains(recipe, "--tags="+tag) || strings.Contains(recipe, "--tags "+tag)
}

func makeTargetRecipe(makefile string, target string) string {
	lines := strings.Split(makefile, "\n")
	prefix := target + ":"
	for index, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		var recipe []string
		for _, candidate := range lines[index+1:] {
			if strings.HasPrefix(candidate, "\t") || strings.TrimSpace(candidate) == "" {
				recipe = append(recipe, candidate)
				continue
			}
			break
		}
		return strings.Join(recipe, "\n")
	}
	return ""
}

func makeVariableValue(makefile string, name string) string {
	lines := strings.Split(makefile, "\n")
	prefix := name + " :="
	for index, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		for strings.HasSuffix(value, "\\") && index+1 < len(lines) {
			index++
			value += "\n" + strings.TrimSpace(lines[index])
		}
		return value
	}
	return ""
}

func hasTestingTParameter(fields *ast.FieldList) bool {
	return hasTestingParameter(fields, "T")
}

func hasTestingBParameter(fields *ast.FieldList) bool {
	return hasTestingParameter(fields, "B")
}

func hasTestingMParameter(fields *ast.FieldList) bool {
	return hasTestingParameter(fields, "M")
}

func testingPackageNames(file *ast.File) map[string]struct{} {
	names := make(map[string]struct{})
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil || path != "testing" {
			continue
		}
		name := "testing"
		if imported.Name != nil {
			name = imported.Name.Name
		}
		if name != "_" {
			names[name] = struct{}{}
		}
	}
	return names
}

func fileContainsTestingShortCall(file *ast.File) bool {
	packages := testingPackageNames(file)
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && isTestingShortCall(call, packages) {
			found = true
			return false
		}
		return true
	})
	return found
}

func isTestingShortCall(call *ast.CallExpr, packages map[string]struct{}) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if ok && selector.Sel.Name == "Short" {
		name, identOK := selector.X.(*ast.Ident)
		if identOK {
			_, imported := packages[name.Name]
			return imported
		}
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "Short" {
		return false
	}
	_, dotImported := packages["."]
	return dotImported
}

func hasTestingParameter(fields *ast.FieldList, typeName string) bool {
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		found := false
		ast.Inspect(field.Type, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != typeName {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			found = ok && pkg.Name == "testing"
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

func nativeBenchmarkException(root string, path string, function *ast.FuncDecl) bool {
	if _, allowed := nativeBenchmarkFunctions[repositoryFunctionKey(root, path, function.Name.Name)]; !allowed {
		return false
	}
	if function.Recv != nil || !isGoTestingFunctionName(function.Name.Name, "Benchmark") {
		return false
	}
	return hasSingleTestingParameterSignature(function, "B")
}

func isTestingPointer(expr ast.Expr, typeName string) bool {
	pointer, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != typeName {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "testing"
}

func isGoTestingFunctionName(name string, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	if len(name) == len(prefix) {
		return true
	}
	next, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return !unicode.IsLower(next)
}

func parseFrameworkGuardFunction(source string) *ast.FuncDecl {
	GinkgoHelper()

	file := parseFrameworkGuardFile(source)
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			return function
		}
	}
	Fail("framework guard fixture has no function declaration")
	return nil
}

func parseFrameworkGuardFile(source string) *ast.File {
	GinkgoHelper()

	file, err := parser.ParseFile(token.NewFileSet(), "guard_test.go", source, parser.ParseComments)
	Expect(err).NotTo(HaveOccurred())
	return file
}

func nativeTestException(root string, path string, function *ast.FuncDecl) bool {
	if _, allowed := nativeSubprocessTestFunctions[repositoryFunctionKey(root, path, function.Name.Name)]; allowed {
		return hasSingleTestingParameterSignature(function, "T")
	}
	if !strings.HasSuffix(filepath.ToSlash(path), "_suite_test.go") || !hasSingleTestingParameterSignature(function, "T") {
		return false
	}

	hasRunSpecs := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && callName(call.Fun) == "RunSpecs" {
			hasRunSpecs = true
			return false
		}
		return true
	})
	return hasRunSpecs
}

func nativeTestMainException(root string, path string, function *ast.FuncDecl) bool {
	if repositoryFunctionKey(root, path, function.Name.Name) != nativeTestMainFunction {
		return false
	}
	return function.Name.Name == "TestMain" && hasSingleTestingParameterSignature(function, "M")
}

func hasSingleTestingParameterSignature(function *ast.FuncDecl, typeName string) bool {
	if function == nil || function.Type == nil || function.Recv != nil {
		return false
	}
	if function.Type.Results != nil && len(function.Type.Results.List) > 0 {
		return false
	}
	if function.Type.TypeParams != nil && len(function.Type.TypeParams.List) > 0 {
		return false
	}
	params := function.Type.Params
	if params == nil || len(params.List) != 1 || len(params.List[0].Names) > 1 {
		return false
	}
	return isTestingPointer(params.List[0].Type, typeName)
}

func repositoryFunctionKey(root string, path string, functionName string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	return filepath.ToSlash(rel) + "#" + functionName
}

var nativeSubprocessTestFunctions = map[string]struct{}{
	"internal/capabilities/mcp/client_test.go#TestMCPStdioHelperProcess":                                                       {},
	"internal/capabilities/workspace/external_tool_test.go#TestWorkspaceExternalToolHelperProcess":                             {},
	"internal/capabilities/workspace/tool_surface_setup_test.go#TestWorkspaceMCPStdioHelperProcess":                            {},
	"internal/capabilities/workspace/workspace_path_identity_reopen_development_e2e_test.go#TestWorkspaceExternalAppendHelper": {},
	"internal/platform/commandexec/commandexec_test.go#TestCommandExecHelperProcess":                                           {},
	"internal/repository/comprehension/ranker_state_store_test.go#TestRankerStateLockHelperProcess":                            {},
	"internal/repository/semantic/languagetools/lsp_base_test.go#TestLSPHelperProcess":                                         {},
	"internal/surfaces/cli/root/signal_exit_test.go#runSignalExitHelperProcess":                                                {},
	"internal/surfaces/tui/app/prompt_worker_test.go#TestPromptWorkerServerHelperProcess":                                      {},
	"internal/workflows/postflight/project_test.go#TestPostflightProjectCommandHelperProcess":                                  {},
}

const nativeTestMainFunction = "internal/surfaces/cli/root/testmain_test.go#TestMain"

var nativeBenchmarkFunctions = map[string]struct{}{
	"internal/repository/comprehension/graph_benchmark_test.go#BenchmarkBuildExactScopeIncremental": {},
	"internal/repository/comprehension/graph_benchmark_test.go#BenchmarkBuildFullGoRepository":      {},
}

func forbiddenTestFrameworkImport(importPath string) string {
	frameworks := []struct {
		prefix string
		name   string
	}{
		{prefix: "github.com/stretchr/testify", name: "Testify"},
		{prefix: "github.com/golang/mock", name: "legacy GoMock"},
		{prefix: "github.com/vektra/mockery", name: "mockery"},
	}
	for _, framework := range frameworks {
		if importPath == framework.prefix || strings.HasPrefix(importPath, framework.prefix+"/") {
			return framework.name
		}
	}
	return ""
}

func appendMockFrameworkViolations(root string, fset *token.FileSet, file *ast.File, violations *[]string) {
	for _, imported := range file.Imports {
		importPath, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			continue
		}
		if framework := forbiddenTestFrameworkImport(importPath); framework != "" {
			*violations = append(*violations, frameworkViolation(root, fset.Position(imported.Pos()), "forbidden "+framework+" import "+strconv.Quote(importPath)))
		}
	}

	if gomockInterfaceMockMissingHeader(file) {
		*violations = append(*violations, frameworkViolation(root, fset.Position(file.Package), "GoMock interface mock is missing the MockGen generated header"))
	}
}

const mockGenGeneratedHeader = "// Code generated by MockGen. DO NOT EDIT."

func gomockInterfaceMockMissingHeader(file *ast.File) bool {
	return fileContainsGomockInterfaceMock(file) && !fileHasMockGenGeneratedHeader(file)
}

func fileHasMockGenGeneratedHeader(file *ast.File) bool {
	for _, commentGroup := range file.Comments {
		if commentGroup.Pos() > file.Package {
			continue
		}
		for _, comment := range commentGroup.List {
			if comment.Text == mockGenGeneratedHeader {
				return true
			}
		}
	}
	return false
}

func fileContainsGomockInterfaceMock(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		typeSpec, ok := node.(*ast.TypeSpec)
		if !ok || !identifierStartsWithRole(typeSpec.Name.Name, "mock") {
			return true
		}
		structure, ok := typeSpec.Type.(*ast.StructType)
		if !ok || structure.Fields == nil {
			return true
		}
		for _, field := range structure.Fields.List {
			if !fieldHasName(field, "ctrl") {
				continue
			}
			pointer, ok := field.Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			selector, ok := pointer.X.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Controller" {
				continue
			}
			packageName, ok := selector.X.(*ast.Ident)
			if ok && packageName.Name == "gomock" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func fieldHasName(field *ast.Field, name string) bool {
	for _, fieldName := range field.Names {
		if fieldName.Name == name {
			return true
		}
	}
	return false
}

func appendLabeledTestDoubleViolations(root string, fset *token.FileSet, file *ast.File, packageLabeled bool, violations *[]string) {
	for _, declaration := range unclassifiedTestDoubleDeclarations(file, packageLabeled) {
		*violations = append(*violations, frameworkViolation(root, fset.Position(declaration.Pos()), "manual "+declaration.Name.Name+" test double in e2e/integration spec; use MockGen or classify it as a scripted protocol peer or observation probe"))
	}
}

func unclassifiedLabeledTestDoubleNames(file *ast.File) []string {
	declarations := unclassifiedTestDoubleDeclarations(file, fileHasAnyLabel(file, "e2e", "integration"))
	names := make([]string, 0, len(declarations))
	for _, declaration := range declarations {
		names = append(names, declaration.Name.Name)
	}
	return names
}

func unclassifiedPackageTestDoubleNames(file *ast.File, packageLabeled bool) []string {
	declarations := unclassifiedTestDoubleDeclarations(file, packageLabeled)
	names := make([]string, 0, len(declarations))
	for _, declaration := range declarations {
		names = append(names, declaration.Name.Name)
	}
	return names
}

func unclassifiedTestDoubleDeclarations(file *ast.File, packageLabeled bool) []*ast.TypeSpec {
	if !packageLabeled || fileHasMockGenGeneratedHeader(file) {
		return nil
	}

	var declarations []*ast.TypeSpec
	ast.Inspect(file, func(node ast.Node) bool {
		general, ok := node.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			return true
		}
		for _, spec := range general.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			classified := hasTestDoubleClassification(typeSpec.Doc) || (len(general.Specs) == 1 && hasTestDoubleClassification(general.Doc))
			if !classified && !identifierContainsTestDoubleRole(typeSpec.Name.Name) && !identifierEndsWithRole(typeSpec.Name.Name, "model") {
				continue
			}
			if classified {
				continue
			}
			declarations = append(declarations, typeSpec)
		}
		return false
	})
	return declarations
}

func fileHasAnyLabel(file *ast.File, wanted ...string) bool {
	wantedLabels := make(map[string]struct{}, len(wanted))
	for _, label := range wanted {
		wantedLabels[label] = struct{}{}
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || callName(call.Fun) != "Label" {
			return true
		}
		for _, argument := range call.Args {
			literal, ok := argument.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			label, err := strconv.Unquote(literal.Value)
			if err != nil {
				continue
			}
			if _, ok := wantedLabels[label]; ok {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func hasTestDoubleClassification(comments *ast.CommentGroup) bool {
	if comments == nil {
		return false
	}
	for _, comment := range comments.List {
		text := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(comment.Text, "//"), "*/"))
		text = strings.TrimSpace(strings.TrimPrefix(text, "/*"))
		switch text {
		case "test-double: scripted-protocol-peer", "test-double: observation-probe":
			return true
		}
	}
	return false
}

func identifierContainsTestDoubleRole(name string) bool {
	for _, role := range []string{"approver", "fake", "mock", "peer", "probe", "recording", "spy", "stub", "transport"} {
		for offset := 0; offset+len(role) <= len(name); offset++ {
			if !strings.EqualFold(name[offset:offset+len(role)], role) {
				continue
			}
			if identifierRoleBoundary(name, offset, offset+len(role)) {
				return true
			}
		}
	}
	return false
}

func identifierEndsWithRole(name string, role string) bool {
	start := len(name) - len(role)
	return start >= 0 && strings.EqualFold(name[start:], role) && identifierRoleBoundary(name, start, len(name))
}

func identifierStartsWithRole(name string, role string) bool {
	return len(name) >= len(role) && strings.EqualFold(name[:len(role)], role) && identifierRoleBoundary(name, 0, len(role))
}

func identifierRoleBoundary(name string, start int, end int) bool {
	startBoundary := start == 0 || name[start-1] == '_' || (isASCIILower(name[start-1]) && isASCIIUpper(name[start]))
	endBoundary := end == len(name) || name[end] == '_' || isASCIIUpper(name[end]) || (name[end] >= '0' && name[end] <= '9')
	return startBoundary && endBoundary
}

func isASCIILower(value byte) bool {
	return value >= 'a' && value <= 'z'
}

func isASCIIUpper(value byte) bool {
	return value >= 'A' && value <= 'Z'
}

func isTestingSubtestCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Run" {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	return ok && receiver.Name == "t"
}

func isConstantFailureExpectation(call *ast.CallExpr) bool {
	actual, matcherName, negative, ok := gomegaAssertion(call)
	if !ok {
		return false
	}
	actualBool, actualOK := boolLiteral(actual)
	if !actualOK {
		return false
	}
	if negative {
		return (!actualBool && matcherName == "BeFalse") || (actualBool && matcherName == "BeTrue")
	}
	return (!actualBool && matcherName == "BeTrue") || (actualBool && matcherName == "BeFalse")
}

func isCompoundBooleanExpectation(call *ast.CallExpr) bool {
	actual, matcherName, _, ok := gomegaAssertion(call)
	return ok && logicalBooleanActual(actual) && (matcherName == "BeTrue" || matcherName == "BeFalse")
}

func gomegaAssertion(call *ast.CallExpr) (actual ast.Expr, matcherName string, negative bool, ok bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) == 0 {
		return nil, "", false, false
	}
	switch selector.Sel.Name {
	case "To", "Should":
	case "NotTo", "ShouldNot":
		negative = true
	default:
		return nil, "", false, false
	}
	expectation, ok := selector.X.(*ast.CallExpr)
	if !ok {
		return nil, "", false, false
	}
	expectName := callName(expectation.Fun)
	switch expectName {
	case "Expect":
		if len(expectation.Args) > 0 {
			actual = expectation.Args[0]
		}
	case "ExpectWithOffset":
		if len(expectation.Args) > 1 {
			actual = expectation.Args[1]
		}
	}
	matcher, ok := call.Args[0].(*ast.CallExpr)
	if actual == nil || !ok {
		return nil, "", false, false
	}
	return actual, callName(matcher.Fun), negative, true
}

func logicalBooleanActual(expr ast.Expr) bool {
	switch typed := expr.(type) {
	case *ast.ParenExpr:
		return logicalBooleanActual(typed.X)
	case *ast.UnaryExpr:
		return typed.Op == token.NOT && logicalBooleanActual(typed.X)
	case *ast.BinaryExpr:
		return typed.Op == token.LAND || typed.Op == token.LOR
	default:
		return false
	}
}

func boolLiteral(expr ast.Expr) (bool, bool) {
	identifier, ok := expr.(*ast.Ident)
	if !ok {
		return false, false
	}
	switch identifier.Name {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func isHTTPHandlerFunc(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "HandlerFunc"
}

func appendUnsafeAsyncViolation(root string, fset *token.FileSet, literal *ast.FuncLit, kind string, violations *[]string) {
	hasAssertion := false
	hasRecovery := false
	ast.Inspect(literal.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := callName(call.Fun)
		switch name {
		case "Expect", "ExpectWithOffset", "Eventually", "Consistently", "Fail":
			hasAssertion = true
		case "GinkgoRecover":
			hasRecovery = true
		}
		return true
	})
	if hasAssertion && !hasRecovery {
		*violations = append(*violations, frameworkViolation(root, fset.Position(literal.Pos()), kind+" assertion without GinkgoRecover"))
	}
}

func callName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	default:
		return ""
	}
}

func frameworkViolation(root string, position token.Position, message string) string {
	rel, _ := filepath.Rel(root, position.Filename)
	return filepath.ToSlash(rel) + ":" + strconv.Itoa(position.Line) + ": " + message
}
