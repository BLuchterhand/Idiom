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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type goPackage struct {
	ImportPath string
	Imports    []string
	Deps       []string
}

var _ = It("keeps every core contract package under an explicit narrow import policy", func() {
	packages := loadPackages()
	assertAllDirectContractPackagesCovered(packages, coreContractPackageDirectImports())

	for importPath, allowed := range coreContractPackageDirectImports() {
		assertDirectInternalImports(packages, importPath, allowed)
	}
})

var _ = It("keeps core contract packages independent of IO and framework details", func() {
	packages := loadPackages()
	for importPath := range coreContractPackageDirectImports() {
		assertNoDirectImports(packages, importPath, []string{
			"database/sql",
			"net/http",
			"os",
			"os/exec",
			"idiom/internal/runtime/llama",
			"idiom/internal/capabilities/mcp",
		})
	}
})

var _ = It("keeps core contract packages independent of outer layers", func() {
	packages := loadPackages()

	for importPath := range coreContractPackageDirectImports() {
		assertNoDependencyOn(packages, importPath, coreContractForbiddenOuterPackages())
	}
})

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
		"idiom/internal/contracts/mcpidentity":    {},
		"idiom/internal/contracts/pathparse":      {},
		"idiom/internal/contracts/plan":           {"idiom/internal/contracts/lane", "idiom/internal/contracts/pathparse", "idiom/internal/contracts/recovery", "idiom/internal/contracts/sourcekind", "idiom/internal/contracts/workflowstate"},
		"idiom/internal/contracts/planartifact":   {"idiom/internal/contracts/plan", "idiom/internal/contracts/sensitivity"},
		"idiom/internal/contracts/queue":          {},
		"idiom/internal/contracts/readapproval":   {},
		"idiom/internal/contracts/recovery":       {"idiom/internal/contracts/lane", "idiom/internal/contracts/toolfacts", "idiom/internal/contracts/workflowstate"},
		"idiom/internal/contracts/runprofile":     {},
		"idiom/internal/contracts/sensitivity":    {},
		"idiom/internal/contracts/sourcekind":     {},
		"idiom/internal/contracts/taskledger":     {},
		"idiom/internal/contracts/toolfacts":      {"idiom/internal/contracts/toolpolicy"},
		"idiom/internal/contracts/toolpolicy":     {},
		"idiom/internal/contracts/workflowstate":  {},
		"idiom/internal/contracts/writerroute":    {},
	}
}

func assertAllDirectContractPackagesCovered(packages map[string]goPackage, allowed map[string][]string) {
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
	Expect(missing).To(BeEmpty(), "direct contract packages missing explicit import policy: %s", strings.Join(missing, ", "))
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

var _ = It("keeps legacy contract roots retired", func() {
	packages := loadPackages()

	for _, importPath := range []string{
		"idiom/internal/autonomy",
		"idiom/internal/contractcompile",
		"idiom/internal/frontier/pathparse",
		"idiom/internal/plancontract",
		"idiom/internal/writerroute",
	} {
		Expect(packages).NotTo(HaveKey(importPath), "legacy contract package %s must remain under idiom/internal/contracts", importPath)
	}
})

var _ = It("keeps moved internal package roots retired", func() {
	packages := loadPackages()

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
		Expect(packages).NotTo(HaveKey(importPath), "retired package root %s must remain under its architecture parent", importPath)
	}
})

var _ = It("keeps runtime and capability packages independent of delivery surfaces", func() {
	packages := loadPackages()

	assertNoDependencyOn(packages, "idiom/internal/runtime/agent", []string{
		"idiom/cmd/",
		"idiom/internal/surfaces/tui/app",
		"idiom/internal/workflows/planexecute",
	})

	assertNoDependencyOn(packages, "idiom/internal/capabilities/workspace", []string{
		"idiom/cmd/",
		"idiom/internal/runtime/agent",
		"idiom/internal/surfaces/tui/app",
		"idiom/internal/workflows/planexecute",
	})
})

var _ = It("keeps workflow and evidence production imports independent of surfaces", func() {
	packages := loadPackages()

	for importPath := range packages {
		if !strings.HasPrefix(importPath, "idiom/internal/workflows/") &&
			!strings.HasPrefix(importPath, "idiom/internal/evidence/") {
			continue
		}

		assertNoDependencyOn(packages, importPath, []string{
			"idiom/internal/surfaces/",
		})
	}
})

var _ = It("keeps project-artifact workflow policy independent of workspace capabilities", func() {
	packages := loadPackages()

	assertNoDependencyOn(packages, "idiom/internal/workflows/projectartifacts", []string{
		"idiom/internal/capabilities/workspace",
	})
})

var _ = It("keeps headless session boundaries independent of the TUI surface", func() {
	packages := loadPackages()

	assertNoDependencyOn(packages, "idiom/internal/runtime/session", []string{
		"idiom/internal/surfaces/",
	})
	assertNoDependencyOn(packages, "idiom/internal/surfaces/cli/runtime", []string{
		"idiom/internal/surfaces/tui/app",
	})
})

var _ = It("keeps contract authority vocabulary owned by autonomy", func() {
	root := repoRoot()
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
	Expect(err).NotTo(HaveOccurred(), "scan internal packages for duplicate authority vocabulary")
	violations = uniqueSortedStrings(violations)
	Expect(violations).To(BeEmpty(),
		"authority vocabulary must be owned by internal/contracts/autonomy; duplicate declarations: %s",
		strings.Join(violations, ", "))
})

var _ = DescribeTable("matches forbidden dependency roots without matching siblings",
	func(dependency string, forbiddenPrefix string, want bool) {
		Expect(dependencyMatchesPrefix(dependency, forbiddenPrefix)).To(Equal(want))
	},
	Entry("exact match", "idiom/internal/capabilities/workspace", "idiom/internal/capabilities/workspace", true),
	Entry("child match", "idiom/internal/capabilities/workspace/subpkg", "idiom/internal/capabilities/workspace", true),
	Entry("trailing-slash prefix", "idiom/cmd/idiom", "idiom/cmd/", true),
	Entry("package sibling is not a child", "idiom/internal/capabilities/workspaceextra", "idiom/internal/capabilities/workspace", false),
	Entry("standard-library child match", "net/http/httptest", "net/http", true),
	Entry("standard-library sibling is not a child", "net/http2", "net/http", false),
)

var _ = It("reports exact dependency paths for representative architecture violations", func() {
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

	Expect(directInternalImportViolations(packages["idiom/internal/contracts/autonomy"], nil)).To(
		ContainElement("idiom/internal/capabilities/workspace"))
	Expect(directImportViolations(packages["idiom/internal/contracts/autonomy"], []string{"net/http"})).To(
		ContainElement("net/http/httptest"))
	Expect(transitiveDependencyViolations(packages["idiom/internal/contracts/autonomy"], []string{"idiom/internal/capabilities/workspace"})).To(
		ContainElement("idiom/internal/capabilities/workspace"))
	Expect(transitiveDependencyViolationDetails(packages, "idiom/internal/runtime/agent", []string{"idiom/internal/capabilities/workspace"})).To(
		ContainElement("idiom/internal/capabilities/workspace via idiom/internal/runtime/agent -> idiom/internal/capabilities/workspace"))
})

var _ = It("returns deterministic deduplicated architecture violations", func() {
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

	Expect(directImportViolations(pkg, []string{"os", "os/exec", "net/http"})).To(Equal(
		[]string{"net/http/httptest", "os/exec"}))
	Expect(transitiveDependencyViolations(pkg, []string{"idiom/internal/capabilities/workspace"})).To(Equal(
		[]string{"idiom/internal/capabilities/workspace", "idiom/internal/capabilities/workspace/subpkg"}))
})

func loadPackages() map[string]goPackage {
	root := repoRoot()
	cmd := exec.Command("go", "list", "-json", "./...")

	cmd.Dir = root

	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "go list -json ./... failed:\n%s", string(out))

	decoder := json.NewDecoder(strings.NewReader(string(out)))
	packages := make(map[string]goPackage)

	for {
		var pkg goPackage

		if err := decoder.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			Expect(err).NotTo(HaveOccurred(), "decode go list package metadata")
		}

		packages[pkg.ImportPath] = pkg
	}

	return packages
}

func assertDirectInternalImports(packages map[string]goPackage, importPath string, allowed []string) {
	pkg, ok := packages[importPath]
	Expect(ok).To(BeTrue(), "package %s not found in go list output", importPath)

	violations := directInternalImportViolations(pkg, allowed)
	Expect(violations).To(BeEmpty(), "%s imports unapproved internal package(s): %s", importPath, strings.Join(violations, ", "))
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

func assertNoDirectImports(packages map[string]goPackage, importPath string, forbidden []string) {
	pkg, ok := packages[importPath]
	Expect(ok).To(BeTrue(), "package %s not found in go list output", importPath)

	violations := directImportViolations(pkg, forbidden)
	Expect(violations).To(BeEmpty(), "%s imports forbidden detail package(s): %s", importPath, strings.Join(violations, ", "))
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

func assertNoDependencyOn(packages map[string]goPackage, importPath string, forbiddenPrefixes []string) {
	_, ok := packages[importPath]
	Expect(ok).To(BeTrue(), "package %s not found in go list output", importPath)

	violations := transitiveDependencyViolationDetails(packages, importPath, forbiddenPrefixes)
	Expect(violations).To(BeEmpty(), "%s depends on forbidden outer package(s): %s", importPath, strings.Join(violations, ", "))
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

func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	Expect(ok).To(BeTrue(), "runtime.Caller should locate the architecture test source")

	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		Expect(parent).NotTo(Equal(dir), "repo root not found from %s", file)

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
