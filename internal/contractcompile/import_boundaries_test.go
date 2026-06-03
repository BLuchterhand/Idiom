package contractcompile_test

import (
	"encoding/json"
	"errors"
	"io"
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
			"idiom/internal/llama",
			"idiom/internal/mcp",
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
		"idiom/internal/autonomy":        {"idiom/internal/util"},
		"idiom/internal/contractcompile": {"idiom/internal/autonomy"},
		"idiom/internal/plancontract":    {"idiom/internal/frontier/pathparse"},
	}
}

func coreContractForbiddenOuterPackages() []string {
	return []string{
		"idiom/cmd/",
		"idiom/internal/agent",
		"idiom/internal/app",
		"idiom/internal/backends",
		"idiom/internal/comprehension",
		"idiom/internal/eval",
		"idiom/internal/frontier/workerruntime",
		"idiom/internal/llama",
		"idiom/internal/mcp",
		"idiom/internal/planexecute",
		"idiom/internal/productworkflow",
		"idiom/internal/workspace",
	}
}

func TestRunnerAndWorkspaceDoNotDependOnSurfaces(t *testing.T) {
	packages := loadPackages(t)

	assertNoDependencyOn(t, packages, "idiom/internal/agent", []string{
		"idiom/cmd/",
		"idiom/internal/app",
		"idiom/internal/planexecute",
	})

	assertNoDependencyOn(t, packages, "idiom/internal/workspace", []string{
		"idiom/cmd/",
		"idiom/internal/agent",
		"idiom/internal/app",
		"idiom/internal/planexecute",
	})
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
			dependency:      "idiom/internal/workspace",
			forbiddenPrefix: "idiom/internal/workspace",
			want:            true,
		},
		{
			name:            "child match",
			dependency:      "idiom/internal/workspace/subpkg",
			forbiddenPrefix: "idiom/internal/workspace",
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
			dependency:      "idiom/internal/workspaceextra",
			forbiddenPrefix: "idiom/internal/workspace",
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
		"idiom/internal/autonomy": {
			ImportPath: "idiom/internal/autonomy",
			Imports: []string{
				"idiom/internal/util",
				"idiom/internal/workspace",
				"net/http/httptest",
			},
			Deps: []string{
				"idiom/internal/util",
				"idiom/internal/workspace",
			},
		},
		"idiom/internal/agent": {
			ImportPath: "idiom/internal/agent",
			Imports:    []string{"idiom/internal/workspace"},
			Deps:       []string{"idiom/internal/workspace"},
		},
		"idiom/internal/workspace": {
			ImportPath: "idiom/internal/workspace",
		},
	}

	if got := directInternalImportViolations(packages["idiom/internal/autonomy"], []string{"idiom/internal/util"}); !containsString(got, "idiom/internal/workspace") {
		t.Fatalf("directInternalImportViolations = %#v, want workspace violation", got)
	}
	if got := directImportViolations(packages["idiom/internal/autonomy"], []string{"net/http"}); !containsString(got, "net/http/httptest") {
		t.Fatalf("directImportViolations = %#v, want net/http/httptest violation", got)
	}
	if got := transitiveDependencyViolations(packages["idiom/internal/autonomy"], []string{"idiom/internal/workspace"}); !containsString(got, "idiom/internal/workspace") {
		t.Fatalf("transitiveDependencyViolations = %#v, want workspace violation", got)
	}
	if got := transitiveDependencyViolationDetails(packages, "idiom/internal/agent", []string{"idiom/internal/workspace"}); !containsString(got, "idiom/internal/workspace via idiom/internal/agent -> idiom/internal/workspace") {
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
			"idiom/internal/workspace",
			"idiom/internal/workspace",
			"idiom/internal/workspace/subpkg",
		},
	}

	if got, want := directImportViolations(pkg, []string{"os", "os/exec", "net/http"}), []string{"net/http/httptest", "os/exec"}; !equalStrings(got, want) {
		t.Fatalf("directImportViolations = %#v, want %#v", got, want)
	}
	if got, want := transitiveDependencyViolations(pkg, []string{"idiom/internal/workspace"}), []string{"idiom/internal/workspace", "idiom/internal/workspace/subpkg"}; !equalStrings(got, want) {
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
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
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
