package contractcompile_test

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

type goPackage struct {
	ImportPath string
	Imports    []string
	Deps       []string
}

func TestCoreContractPackagesKeepNarrowDirectImports(t *testing.T) {
	packages := loadPackages(t)
	assertAllDirectContractPackagesCovered(t, packages, coreContractPackageDirectImports())

	for importPath, allowed := range coreContractPackageDirectImports() {
		assertDirectInternalImports(t, packages, importPath, allowed)
	}
}

func TestCoreContractPackagesDoNotImportIOOrFrameworkDetails(t *testing.T) {
	packages := loadPackages(t)
	for importPath := range coreContractPackageDirectImports() {
		assertNoDirectImports(t, packages, importPath, []string{
			"database/sql",
			"net/http",
			"os",
			"os/exec",
			"idiom/internal/runtime/llama",
			"idiom/internal/capabilities/mcp",
		})
	}
}

func TestCoreContractPackagesDoNotDependOnOuterLayers(t *testing.T) {
	packages := loadPackages(t)

	for importPath := range coreContractPackageDirectImports() {
		assertNoDependencyOn(t, packages, importPath, coreContractForbiddenOuterPackages())
	}
}

func coreContractPackageDirectImports() map[string][]string {
	return map[string][]string{
		"idiom/internal/contracts/atomintake":     {},
		"idiom/internal/contracts/autonomy":       {"idiom/internal/contracts/lane"},
		"idiom/internal/contracts/codingrules":    {},
		"idiom/internal/contracts/compile":        {"idiom/internal/contracts/autonomy", "idiom/internal/contracts/lane"},
		"idiom/internal/contracts/contextpack":    {},
		"idiom/internal/contracts/featureplanner": {},
		"idiom/internal/contracts/intentloop":     {},
		"idiom/internal/contracts/lane":           {},
		"idiom/internal/contracts/pathparse":      {},
		"idiom/internal/contracts/plan":           {"idiom/internal/contracts/lane", "idiom/internal/contracts/pathparse", "idiom/internal/contracts/recovery", "idiom/internal/contracts/sourcekind", "idiom/internal/contracts/workflowstate"},
		"idiom/internal/contracts/planartifact":   {"idiom/internal/contracts/plan", "idiom/internal/contracts/sensitivity"},
		"idiom/internal/contracts/queue":          {},
		"idiom/internal/contracts/readapproval":   {},
		"idiom/internal/contracts/recovery":       {"idiom/internal/contracts/lane", "idiom/internal/contracts/workflowstate"},
		"idiom/internal/contracts/sensitivity":    {},
		"idiom/internal/contracts/sourcekind":     {},
		"idiom/internal/contracts/toolfacts":      {},
		"idiom/internal/contracts/toolpolicy":     {},
		"idiom/internal/contracts/workflowstate":  {},
		"idiom/internal/contracts/writerroute":    {},
	}
}

func assertAllDirectContractPackagesCovered(t *testing.T, packages map[string]goPackage, allowed map[string][]string) {
	t.Helper()

	var missing []string
	for importPath := range packages {
		if !strings.HasPrefix(importPath, "idiom/internal/contracts/") {
			continue
		}
		rest := strings.TrimPrefix(importPath, "idiom/internal/contracts/")
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		if _, ok := allowed[importPath]; !ok {
			missing = append(missing, importPath)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("direct contract packages missing explicit import policy: %s", strings.Join(missing, ", "))
	}
}

func coreContractForbiddenOuterPackages() []string {
	return []string{
		"idiom/cmd/",
		"idiom/internal/runtime/agent",
		"idiom/internal/surfaces/tui/app",
		"idiom/internal/runtime/backends",
		"idiom/internal/repository/comprehension",
		"idiom/internal/evidence/eval",
		"idiom/internal/runtime/workerruntime",
		"idiom/internal/runtime/llama",
		"idiom/internal/capabilities/mcp",
		"idiom/internal/platform/",
		"idiom/internal/workflows/planexecute",
		"idiom/internal/workflows/productworkflow",
		"idiom/internal/capabilities/workspace",
	}
}

func TestLegacyContractRootsAreRetired(t *testing.T) {
	packages := loadPackages(t)

	for _, importPath := range []string{
		"idiom/internal/autonomy",
		"idiom/internal/contractcompile",
		"idiom/internal/frontier/pathparse",
		"idiom/internal/plancontract",
		"idiom/internal/writerroute",
	} {
		if _, ok := packages[importPath]; ok {
			t.Fatalf("legacy contract package %s still exists; move it under idiom/internal/contracts", importPath)
		}
	}
}

func TestMovedInternalRootsAreRetired(t *testing.T) {
	packages := loadPackages(t)

	for _, importPath := range []string{
		"idiom/internal/agent",
		"idiom/internal/app",
		"idiom/internal/answergrounding",
		"idiom/internal/architect",
		"idiom/internal/auditenum",
		"idiom/internal/autodetect",
		"idiom/internal/backends",
		"idiom/internal/cliassets",
		"idiom/internal/cmd/root",
		"idiom/internal/comprehension",
		"idiom/internal/config",
		"idiom/internal/discovery",
		"idiom/internal/eval",
		"idiom/internal/failures",
		"idiom/internal/featureplanner",
		"idiom/internal/frontier/workerruntime",
		"idiom/internal/lanehistory",
		"idiom/internal/llama",
		"idiom/internal/localops",
		"idiom/internal/mcp",
		"idiom/internal/planexecute",
		"idiom/internal/planintent",
		"idiom/internal/productevidence",
		"idiom/internal/productworkflow",
		"idiom/internal/promptstack",
		"idiom/internal/queueadmission",
		"idiom/internal/readiness",
		"idiom/internal/recovery",
		"idiom/internal/runner",
		"idiom/internal/semantic",
		"idiom/internal/sidecarproof",
		"idiom/internal/evidence/sidecarproof",
		"idiom/internal/taskfamily",
		"idiom/internal/telemetry",
		"idiom/internal/transcript",
		"idiom/internal/util",
		"idiom/internal/workspace",
	} {
		if _, ok := packages[importPath]; ok {
			t.Fatalf("retired package root %s still exists; keep it under its architecture parent", importPath)
		}
	}
}

func TestRuntimeAndCapabilitiesDoNotDependOnSurfaces(t *testing.T) {
	packages := loadPackages(t)

	assertNoDependencyOn(t, packages, "idiom/internal/runtime/agent", []string{
		"idiom/cmd/",
		"idiom/internal/surfaces/tui/app",
		"idiom/internal/workflows/planexecute",
	})

	assertNoDependencyOn(t, packages, "idiom/internal/capabilities/workspace", []string{
		"idiom/cmd/",
		"idiom/internal/runtime/agent",
		"idiom/internal/surfaces/tui/app",
		"idiom/internal/workflows/planexecute",
	})
}

func TestWorkflowAndEvidencePackagesDoNotDependOnSurfacesInProductionImports(t *testing.T) {
	packages := loadPackages(t)

	for importPath := range packages {
		if !strings.HasPrefix(importPath, "idiom/internal/workflows/") &&
			!strings.HasPrefix(importPath, "idiom/internal/evidence/") {
			continue
		}

		assertNoDependencyOn(t, packages, importPath, []string{
			"idiom/internal/surfaces/",
		})
	}
}

func TestContractAuthorityVocabularyIsNotRedefinedOutsideAutonomy(t *testing.T) {
	root := repoRoot(t)
	allowed := filepath.Join(root, "internal", "contracts", "autonomy")
	authorityTypes := []string{
		"ActionClass",
		"ContractPackID",
		"EnforcementMode",
		"GateKind",
		"ObligationKind",
		"ObligationStatus",
		"PathRoleKind",
		"SideEffectClass",
		"ToolPolicyDecision",
	}

	var violations []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			if path == allowed {
				return filepath.SkipDir
			}
			return nil
		}

		if filepath.Ext(path) != ".go" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		content := string(data)
		for _, typeName := range authorityTypes {
			if strings.Contains(content, "type "+typeName+" ") {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				violations = append(violations, rel+": type "+typeName)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("scan internal for duplicate authority vocabulary: %v", err)
	}

	if len(violations) > 0 {
		t.Fatalf("authority vocabulary must be owned by internal/contracts/autonomy; duplicate declarations: %s", strings.Join(uniqueSortedStrings(violations), ", "))
	}
}

func TestDependencyPrefixMatcherDistinguishesChildrenFromSiblings(t *testing.T) {
	cases := []struct {
		name            string
		dependency      string
		forbiddenPrefix string
		want            bool
	}{
		{
			name:            "exact match",
			dependency:      "idiom/internal/capabilities/workspace",
			forbiddenPrefix: "idiom/internal/capabilities/workspace",
			want:            true,
		},
		{
			name:            "child match",
			dependency:      "idiom/internal/capabilities/workspace/subpkg",
			forbiddenPrefix: "idiom/internal/capabilities/workspace",
			want:            true,
		},
		{
			name:            "trailing slash prefix",
			dependency:      "idiom/cmd/idiom",
			forbiddenPrefix: "idiom/cmd/",
			want:            true,
		},
		{
			name:            "sibling is not child",
			dependency:      "idiom/internal/capabilities/workspaceextra",
			forbiddenPrefix: "idiom/internal/capabilities/workspace",
			want:            false,
		},
		{
			name:            "stdlib child match",
			dependency:      "net/http/httptest",
			forbiddenPrefix: "net/http",
			want:            true,
		},
		{
			name:            "stdlib sibling is not child",
			dependency:      "net/http2",
			forbiddenPrefix: "net/http",
			want:            false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dependencyMatchesPrefix(tc.dependency, tc.forbiddenPrefix); got != tc.want {
				t.Fatalf("dependencyMatchesPrefix(%q, %q) = %v, want %v", tc.dependency, tc.forbiddenPrefix, got, tc.want)
			}
		})
	}
}

func TestArchitectureAssertionsCatchExpectedViolations(t *testing.T) {
	packages := map[string]goPackage{
		"idiom/internal/contracts/autonomy": {
			ImportPath: "idiom/internal/contracts/autonomy",
			Imports: []string{
				"idiom/internal/capabilities/workspace",
				"net/http/httptest",
			},
			Deps: []string{
				"idiom/internal/capabilities/workspace",
			},
		},
		"idiom/internal/runtime/agent": {
			ImportPath: "idiom/internal/runtime/agent",
			Imports:    []string{"idiom/internal/capabilities/workspace"},
			Deps:       []string{"idiom/internal/capabilities/workspace"},
		},
		"idiom/internal/capabilities/workspace": {
			ImportPath: "idiom/internal/capabilities/workspace",
		},
	}

	if got := directInternalImportViolations(packages["idiom/internal/contracts/autonomy"], nil); !containsString(got, "idiom/internal/capabilities/workspace") {
		t.Fatalf("directInternalImportViolations = %#v, want workspace violation", got)
	}

	if got := directImportViolations(packages["idiom/internal/contracts/autonomy"], []string{"net/http"}); !containsString(got, "net/http/httptest") {
		t.Fatalf("directImportViolations = %#v, want net/http/httptest violation", got)
	}

	if got := transitiveDependencyViolations(packages["idiom/internal/contracts/autonomy"], []string{"idiom/internal/capabilities/workspace"}); !containsString(got, "idiom/internal/capabilities/workspace") {
		t.Fatalf("transitiveDependencyViolations = %#v, want workspace violation", got)
	}

	if got := transitiveDependencyViolationDetails(packages, "idiom/internal/runtime/agent", []string{"idiom/internal/capabilities/workspace"}); !containsString(got, "idiom/internal/capabilities/workspace via idiom/internal/runtime/agent -> idiom/internal/capabilities/workspace") {
		t.Fatalf("transitiveDependencyViolationDetails = %#v, want import path detail", got)
	}
}

func TestViolationCollectorsReturnDeduplicatedSortedResults(t *testing.T) {
	pkg := goPackage{
		ImportPath: "idiom/internal/example",
		Imports: []string{
			"os/exec",
			"os/exec",
			"net/http/httptest",
			"net/http/httptest",
		},
		Deps: []string{
			"idiom/internal/capabilities/workspace",
			"idiom/internal/capabilities/workspace",
			"idiom/internal/capabilities/workspace/subpkg",
		},
	}

	if got, want := directImportViolations(pkg, []string{"os", "os/exec", "net/http"}), []string{"net/http/httptest", "os/exec"}; !equalStrings(got, want) {
		t.Fatalf("directImportViolations = %#v, want %#v", got, want)
	}

	if got, want := transitiveDependencyViolations(pkg, []string{"idiom/internal/capabilities/workspace"}), []string{"idiom/internal/capabilities/workspace", "idiom/internal/capabilities/workspace/subpkg"}; !equalStrings(got, want) {
		t.Fatalf("transitiveDependencyViolations = %#v, want %#v", got, want)
	}
}

func loadPackages(t *testing.T) map[string]goPackage {
	t.Helper()

	root := repoRoot(t)
	cmd := exec.Command("go", "list", "-json", "./...")

	cmd.Dir = root

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list failed: %v\n%s", err, string(exitErr.Stderr))
		}

		t.Fatalf("go list failed: %v", err)
	}

	decoder := json.NewDecoder(strings.NewReader(string(out)))
	packages := make(map[string]goPackage)

	for {
		var pkg goPackage

		if err := decoder.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			t.Fatalf("decode go list package: %v", err)
		}

		packages[pkg.ImportPath] = pkg
	}

	return packages
}

func assertDirectInternalImports(t *testing.T, packages map[string]goPackage, importPath string, allowed []string) {
	t.Helper()

	pkg, ok := packages[importPath]
	if !ok {
		t.Fatalf("package %s not found in go list output", importPath)
	}

	violations := directInternalImportViolations(pkg, allowed)
	if len(violations) > 0 {
		t.Fatalf("%s imports unapproved internal package(s): %s", importPath, strings.Join(uniqueStrings(violations), ", "))
	}
}

func directInternalImportViolations(pkg goPackage, allowed []string) []string {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, importPath := range allowed {
		allowedSet[importPath] = struct{}{}
	}

	var violations []string

	for _, imported := range pkg.Imports {
		if !strings.HasPrefix(imported, "idiom/") {
			continue
		}

		if _, ok := allowedSet[imported]; ok {
			continue
		}

		violations = append(violations, imported)
	}

	return uniqueSortedStrings(violations)
}

func assertNoDirectImports(t *testing.T, packages map[string]goPackage, importPath string, forbidden []string) {
	t.Helper()

	pkg, ok := packages[importPath]
	if !ok {
		t.Fatalf("package %s not found in go list output", importPath)
	}

	violations := directImportViolations(pkg, forbidden)
	if len(violations) > 0 {
		t.Fatalf("%s imports forbidden detail package(s): %s", importPath, strings.Join(uniqueStrings(violations), ", "))
	}
}

func directImportViolations(pkg goPackage, forbidden []string) []string {
	forbiddenSet := make(map[string]struct{}, len(forbidden))
	for _, importPath := range forbidden {
		forbiddenSet[importPath] = struct{}{}
	}

	var violations []string

	for _, imported := range pkg.Imports {
		for forbidden := range forbiddenSet {
			if dependencyMatchesPrefix(imported, forbidden) {
				violations = append(violations, imported)
			}
		}
	}

	return uniqueSortedStrings(violations)
}

func assertNoDependencyOn(t *testing.T, packages map[string]goPackage, importPath string, forbiddenPrefixes []string) {
	t.Helper()

	if _, ok := packages[importPath]; !ok {
		t.Fatalf("package %s not found in go list output", importPath)
	}

	violations := transitiveDependencyViolationDetails(packages, importPath, forbiddenPrefixes)
	if len(violations) > 0 {
		t.Fatalf("%s depends on forbidden outer package(s): %s", importPath, strings.Join(uniqueStrings(violations), ", "))
	}
}

func transitiveDependencyViolations(pkg goPackage, forbiddenPrefixes []string) []string {
	var violations []string

	for _, dep := range pkg.Deps {
		for _, forbidden := range forbiddenPrefixes {
			if dependencyMatchesPrefix(dep, forbidden) {
				violations = append(violations, dep)
			}
		}
	}

	return uniqueSortedStrings(violations)
}

func transitiveDependencyViolationDetails(packages map[string]goPackage, importPath string, forbiddenPrefixes []string) []string {
	violations := transitiveDependencyViolations(packages[importPath], forbiddenPrefixes)
	details := make([]string, 0, len(violations))

	for _, violation := range violations {
		path := shortestImportPath(packages, importPath, violation)
		if len(path) > 0 {
			details = append(details, violation+" via "+strings.Join(path, " -> "))
			continue
		}

		details = append(details, violation)
	}

	return uniqueSortedStrings(details)
}

func shortestImportPath(packages map[string]goPackage, start string, target string) []string {
	if start == target {
		return []string{start}
	}

	type queueItem struct {
		path []string
	}

	queue := []queueItem{{path: []string{start}}}
	seen := map[string]struct{}{start: {}}

	for len(queue) > 0 {
		item := queue[0]

		queue = queue[1:]

		current := item.path[len(item.path)-1]
		pkg, ok := packages[current]

		if !ok {
			continue
		}

		imports := append([]string(nil), pkg.Imports...)
		sort.Strings(imports)

		for _, imported := range imports {
			if _, ok := seen[imported]; ok {
				continue
			}

			nextPath := append(append([]string(nil), item.path...), imported)
			if imported == target {
				return nextPath
			}

			seen[imported] = struct{}{}
			if _, ok := packages[imported]; ok {
				queue = append(queue, queueItem{path: nextPath})
			}
		}
	}

	return nil
}

func dependencyMatchesPrefix(dep string, forbiddenPrefix string) bool {
	forbiddenPrefix = strings.TrimSuffix(forbiddenPrefix, "/")
	if dep == forbiddenPrefix {
		return true
	}

	return strings.HasPrefix(dep, forbiddenPrefix+"/")
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root not found from %s", file)
		}

		dir = parent
	}
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))

	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	sort.Strings(out)

	return out
}

func uniqueStrings(values []string) []string {
	return uniqueSortedStrings(values)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}

	return false
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}

	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}

	return true
}
