package sidecarvalidation

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

type externalDependencyManifest struct {
	SchemaVersion string                            `json:"schema_version"`
	ManifestKind  string                            `json:"manifest_kind"`
	Dependencies  []externalDependencyManifestEntry `json:"dependencies"`
}

type externalDependencyManifestEntry struct {
	ID       string                         `json:"id"`
	Category string                         `json:"category"`
	Tools    []string                       `json:"tools"`
	Binaries []externalDependencyBinary     `json:"binaries"`
	Install  []externalDependencyInstall    `json:"install"`
	Doctor   externalDependencyDoctorStatus `json:"doctor"`
	Audit    externalDependencyAuditStatus  `json:"audit"`
}

type externalDependencyBinary struct {
	Name       string `json:"name"`
	Validation string `json:"validation"`
}

type externalDependencyInstall struct {
	Platform      string `json:"platform"`
	Mode          string `json:"mode"`
	InstallerFlag string `json:"installer_flag"`
	ImplementedBy string `json:"implemented_by"`
	Package       string `json:"package"`
	Command       string `json:"command"`
}

type externalDependencyDoctorStatus struct {
	CurrentOwner  string `json:"current_owner"`
	CurrentStatus string `json:"current_status"`
}

type externalDependencyAuditStatus struct {
	CurrentOwner string `json:"current_owner"`
	BackendName  string `json:"backend_name"`
}

var _ = ginkgo.It("external dependency manifest shape and required coverage", func() {
	manifest := readExternalDependencyManifest()

	gomega.Expect(manifest.SchemaVersion).To(gomega.BeEquivalentTo("external_dependency_backend_manifest.v1"), fmt.Sprintf("unexpected schema version %q", manifest.SchemaVersion))

	gomega.Expect(manifest.ManifestKind).To(gomega.BeEquivalentTo("external_dependency_backend_manifest"), fmt.Sprintf("manifest must identify external dependencies, got %q", manifest.ManifestKind))

	entries := externalDependencyEntriesByID(manifest)
	for _, id := range []string{
		"git",
		"ripgrep",
		"ast_grep",
		"universal_ctags",
		"semgrep",
		"joern",
		"go",
		"python_lsp",
		"typescript_language_server",
		"rust",
		"java",
		"clangd",
		"madge",
		"pydeps",
	} {
		{
			_, ok := entries[id]
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("manifest missing dependency %q", id))
		}
	}

	for _, dep := range manifest.Dependencies {
		gomega.Expect(dep.Category).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("%s missing category", dep.ID))

		gomega.Expect(dep.Tools).NotTo(gomega.BeEmpty(), fmt.Sprintf("%s missing affected tools", dep.ID))

		gomega.Expect(dep.Binaries).NotTo(gomega.BeEmpty(), fmt.Sprintf("%s missing binary probes", dep.ID))

		for _, binary := range dep.Binaries {
			gomega.Expect(binary.Name).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("%s has incomplete binary probe: %#v", dep.ID, binary))
			gomega.Expect(binary.Validation).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("%s has incomplete binary probe: %#v", dep.ID, binary))
		}

		gomega.Expect(dep.Install).NotTo(gomega.BeEmpty(), fmt.Sprintf("%s missing install guidance", dep.ID))

		for _, install := range dep.Install {
			gomega.Expect(install.Platform).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("%s has incomplete install guidance: %#v", dep.ID, install))
			gomega.Expect(install.Mode).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("%s has incomplete install guidance: %#v", dep.ID, install))
			gomega.Expect(install.InstallerFlag).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("%s has incomplete install guidance: %#v", dep.ID, install))
			gomega.Expect(install.ImplementedBy).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("%s has incomplete install guidance: %#v", dep.ID, install))
		}

		gomega.Expect(dep.Doctor.CurrentStatus).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("%s missing doctor ownership/status", dep.ID))
		gomega.Expect(dep.Doctor.CurrentOwner).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("%s missing doctor ownership/status", dep.ID))

		gomega.Expect(dep.Audit.BackendName).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("%s missing audit ownership/backend", dep.ID))
		gomega.Expect(dep.Audit.CurrentOwner).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("%s missing audit ownership/backend", dep.ID))
	}
})
var _ = ginkgo.It("external dependency manifest matches current installer surface", func() {
	root := sidecarValidationRepoRoot()
	installer := readInstallToolFile(root, filepath.Join("scripts", "install-requirements.sh"))
	manifest := readExternalDependencyManifest()

	for _, dep := range manifest.Dependencies {
		for _, install := range dep.Install {
			if install.ImplementedBy != "scripts/install-requirements.sh" {
				continue
			}

			if install.InstallerFlag == "default" && !strings.Contains(installer, "brew_install go ripgrep ast-grep universal-ctags semgrep joern gopls") {
				gomega.Expect(dep.ID).To(gomega.BeEquivalentTo("git"), fmt.Sprintf("default installer command block missing while validating %s", dep.ID))
			}

			if install.InstallerFlag == "--all" {
				gomega.Expect(installer).To(gomega.ContainSubstring("if $install_all"), fmt.Sprintf("--all installer block missing while validating %s", dep.ID))
			}

			assertInstallerMentionsPackageOrCommand(installer, dep.ID, install)
		}
	}
})
var _ = ginkgo.It("external dependency manifest matches current audit backends", func() {
	root := sidecarValidationRepoRoot()
	searchBackendSource := readInstallToolFile(root, filepath.Join("internal", "capabilities", "workspace", "search_backend.go"))
	searchBackendLiterals := goStringLiterals(searchBackendSource, "search_backend.go")
	manifest := readExternalDependencyManifest()
	ownerBackends := map[string]map[string]map[string]struct{}{}

	for _, dep := range manifest.Dependencies {
		owner := filepath.FromSlash(dep.Audit.CurrentOwner)
		backends, ok := ownerBackends[owner]
		if !ok {
			ownerSource := readInstallToolFile(root, owner)
			backends = auditBackendsInSource(ownerSource, owner)
			ownerBackends[owner] = backends
		}

		backendBinaries, found := backends[dep.Audit.BackendName]
		gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("%s audit backend %q not found in declared owner %s", dep.ID, dep.Audit.BackendName, dep.Audit.CurrentOwner))

		for _, binary := range dep.Binaries {
			if strings.Contains(binary.Validation, "precondition") || binary.Validation == "conditional_package" {
				continue
			}

			if dep.Audit.BackendName == "content_search" {
				if _, declaredBySearchResolver := searchBackendLiterals[binary.Name]; declaredBySearchResolver {
					continue
				}
			}
			gomega.Expect(backendBinaries).To(gomega.HaveKey(binary.Name), fmt.Sprintf("%s binary %q not found in harness audit backend surface", dep.ID, binary.Name))
		}
	}
})
var _ = ginkgo.It("external dependency manifest doctor scope is current state", func() {
	root := sidecarValidationRepoRoot()
	doctorSource := readInstallToolFile(root, filepath.Join("internal", "surfaces", "cli", "doctor", "doctor.go"))
	manifest := readExternalDependencyManifest()

	gomega.Expect(doctorSource).To(gomega.ContainSubstring("checkGit()"), fmt.Sprint("doctor source no longer shows the git host check this manifest describes"))

	for _, dep := range manifest.Dependencies {
		switch dep.Doctor.CurrentStatus {
		case "host_check":
			gomega.Expect(dep.ID).To(gomega.BeEquivalentTo("git"), fmt.Sprintf("%s claims doctor host_check, but current doctor only probes git from this manifest", dep.ID))
		case "not_wired_currently":
			gomega.Expect(dep.Doctor.CurrentOwner).To(gomega.BeEquivalentTo("harness_audit"), fmt.Sprintf("%s not-wired doctor metadata should point users to harness_audit ownership, got %#v", dep.ID, dep.Doctor))
		default:
			ginkgo.Fail(fmt.Sprintf("%s has unknown doctor status %q", dep.ID, dep.Doctor.CurrentStatus), 1)
		}
	}
})

func readExternalDependencyManifest() externalDependencyManifest {
	ginkgo.GinkgoHelper()

	root := sidecarValidationRepoRoot()
	data, err := os.ReadFile(filepath.Join(root, "scripts", "install", "manifest.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read external dependency manifest: %v", err))

	var manifest externalDependencyManifest
	gomega.Expect(json.Unmarshal(data, &manifest)).To(gomega.Succeed(), "decode external dependency manifest")

	return manifest
}

func externalDependencyEntriesByID(manifest externalDependencyManifest) map[string]externalDependencyManifestEntry {
	ginkgo.GinkgoHelper()

	entries := make(map[string]externalDependencyManifestEntry, len(manifest.Dependencies))
	for _, dep := range manifest.Dependencies {
		gomega.Expect(dep.ID).NotTo(gomega.BeEquivalentTo(""), fmt.Sprint("manifest contains dependency with blank id"))

		{
			_, exists := entries[dep.ID]
			gomega.Expect(exists).To(gomega.BeFalse(), fmt.Sprintf("manifest contains duplicate dependency id %q", dep.ID))
		}

		entries[dep.ID] = dep
	}

	return entries
}

func auditBackendsInSource(source, filename string) map[string]map[string]struct{} {
	ginkgo.GinkgoHelper()

	file, err := parser.ParseFile(token.NewFileSet(), filename, source, 0)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse audit backend owner %s: %v", filename, err))

	backends := map[string]map[string]struct{}{}
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}

		name := ""
		var binaries map[string]struct{}
		for _, element := range literal.Elts {
			field, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}

			key, ok := field.Key.(*ast.Ident)
			if !ok {
				continue
			}

			switch key.Name {
			case "Name":
				name = goStringLiteral(field.Value)
			case "Binaries":
				binaries = goStringLiteralsInNode(field.Value)
			}
		}

		if name != "" && binaries != nil {
			backends[name] = binaries
		}

		return true
	})

	return backends
}

func goStringLiterals(source, filename string) map[string]struct{} {
	ginkgo.GinkgoHelper()

	file, err := parser.ParseFile(token.NewFileSet(), filename, source, 0)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("parse Go source %s: %v", filename, err))

	return goStringLiteralsInNode(file)
}

func goStringLiteralsInNode(node ast.Node) map[string]struct{} {
	literals := map[string]struct{}{}
	ast.Inspect(node, func(node ast.Node) bool {
		value := goStringLiteral(node)
		if value != "" {
			literals[value] = struct{}{}
		}

		return true
	})

	return literals
}

func goStringLiteral(node ast.Node) string {
	literal, ok := node.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return ""
	}

	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return ""
	}

	return value
}

func assertInstallerMentionsPackageOrCommand(installer, id string, install externalDependencyInstall) {
	ginkgo.GinkgoHelper()

	if install.Command != "" {
		for _, part := range strings.Fields(install.Command) {
			gomega.Expect(installer).To(gomega.ContainSubstring(part), fmt.Sprintf("%s installer guidance command %q not reflected in install script", id, install.Command))
		}

		return
	}

	gomega.Expect(install.Package).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("%s installer guidance has neither command nor package: %#v", id, install))

	for _, part := range strings.Fields(install.Package) {
		gomega.Expect(installer).To(gomega.ContainSubstring(part), fmt.Sprintf("%s installer package %q not reflected in install script", id, install.Package))
	}
}
