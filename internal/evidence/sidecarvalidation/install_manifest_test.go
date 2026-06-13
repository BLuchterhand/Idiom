package sidecarvalidation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestExternalDependencyManifestShapeAndRequiredCoverage(t *testing.T) {
	manifest := readExternalDependencyManifest(t)

	if manifest.SchemaVersion != "external_dependency_backend_manifest.v1" {
		t.Fatalf("unexpected schema version %q", manifest.SchemaVersion)
	}

	if manifest.ManifestKind != "external_dependency_backend_manifest" {
		t.Fatalf("manifest must identify external dependencies, got %q", manifest.ManifestKind)
	}

	entries := externalDependencyEntriesByID(t, manifest)
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
		if _, ok := entries[id]; !ok {
			t.Fatalf("manifest missing dependency %q", id)
		}
	}

	for _, dep := range manifest.Dependencies {
		if dep.Category == "" {
			t.Fatalf("%s missing category", dep.ID)
		}

		if len(dep.Tools) == 0 {
			t.Fatalf("%s missing affected tools", dep.ID)
		}

		if len(dep.Binaries) == 0 {
			t.Fatalf("%s missing binary probes", dep.ID)
		}

		for _, binary := range dep.Binaries {
			if binary.Name == "" || binary.Validation == "" {
				t.Fatalf("%s has incomplete binary probe: %#v", dep.ID, binary)
			}
		}

		if len(dep.Install) == 0 {
			t.Fatalf("%s missing install guidance", dep.ID)
		}

		for _, install := range dep.Install {
			if install.Platform == "" || install.Mode == "" || install.InstallerFlag == "" || install.ImplementedBy == "" {
				t.Fatalf("%s has incomplete install guidance: %#v", dep.ID, install)
			}
		}

		if dep.Doctor.CurrentStatus == "" || dep.Doctor.CurrentOwner == "" {
			t.Fatalf("%s missing doctor ownership/status", dep.ID)
		}

		if dep.Audit.BackendName == "" || dep.Audit.CurrentOwner == "" {
			t.Fatalf("%s missing audit ownership/backend", dep.ID)
		}
	}
}

func TestExternalDependencyManifestMatchesCurrentInstallerSurface(t *testing.T) {
	root := sidecarValidationRepoRoot(t)
	installer := readInstallToolFile(t, root, filepath.Join("scripts", "install-requirements.sh"))
	manifest := readExternalDependencyManifest(t)

	for _, dep := range manifest.Dependencies {
		for _, install := range dep.Install {
			if install.ImplementedBy != "scripts/install-requirements.sh" {
				continue
			}

			if install.InstallerFlag == "default" && !strings.Contains(installer, "brew_install go ripgrep ast-grep universal-ctags semgrep joern gopls") && dep.ID != "git" {
				t.Fatalf("default installer command block missing while validating %s", dep.ID)
			}

			if install.InstallerFlag == "--all" && !strings.Contains(installer, "if $install_all") {
				t.Fatalf("--all installer block missing while validating %s", dep.ID)
			}

			assertInstallerMentionsPackageOrCommand(t, installer, dep.ID, install)
		}
	}
}

func TestExternalDependencyManifestMatchesCurrentAuditBackends(t *testing.T) {
	root := sidecarValidationRepoRoot(t)
	auditSource := readInstallToolFile(t, root, filepath.Join("internal", "capabilities", "workspace", "harness_audit.go"))
	searchBackendSource := readInstallToolFile(t, root, filepath.Join("internal", "capabilities", "workspace", "search_backend.go"))
	auditBackendSources := auditSource + "\n" + searchBackendSource
	manifest := readExternalDependencyManifest(t)

	for _, dep := range manifest.Dependencies {
		if !strings.Contains(auditSource, `Name:     "`+dep.Audit.BackendName+`"`) {
			t.Fatalf("%s audit backend %q not found in harness_audit.go", dep.ID, dep.Audit.BackendName)
		}

		for _, binary := range dep.Binaries {
			if strings.Contains(binary.Validation, "precondition") || binary.Validation == "conditional_package" {
				continue
			}

			if !strings.Contains(auditBackendSources, `"`+binary.Name+`"`) {
				t.Fatalf("%s binary %q not found in harness audit backend surface", dep.ID, binary.Name)
			}
		}
	}
}

func TestExternalDependencyManifestDoctorScopeIsCurrentState(t *testing.T) {
	root := sidecarValidationRepoRoot(t)
	doctorSource := readInstallToolFile(t, root, filepath.Join("internal", "surfaces", "cli", "doctor", "doctor.go"))
	manifest := readExternalDependencyManifest(t)

	if !strings.Contains(doctorSource, "checkGit()") {
		t.Fatal("doctor source no longer shows the git host check this manifest describes")
	}

	for _, dep := range manifest.Dependencies {
		switch dep.Doctor.CurrentStatus {
		case "host_check":
			if dep.ID != "git" {
				t.Fatalf("%s claims doctor host_check, but current doctor only probes git from this manifest", dep.ID)
			}
		case "not_wired_currently":
			if dep.Doctor.CurrentOwner != "harness_audit" {
				t.Fatalf("%s not-wired doctor metadata should point users to harness_audit ownership, got %#v", dep.ID, dep.Doctor)
			}
		default:
			t.Fatalf("%s has unknown doctor status %q", dep.ID, dep.Doctor.CurrentStatus)
		}
	}
}

func readExternalDependencyManifest(t *testing.T) externalDependencyManifest {
	t.Helper()

	root := sidecarValidationRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "scripts", "install", "manifest.json"))
	if err != nil {
		t.Fatalf("read external dependency manifest: %v", err)
	}

	var manifest externalDependencyManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode external dependency manifest: %v", err)
	}

	return manifest
}

func externalDependencyEntriesByID(t *testing.T, manifest externalDependencyManifest) map[string]externalDependencyManifestEntry {
	t.Helper()

	entries := make(map[string]externalDependencyManifestEntry, len(manifest.Dependencies))
	for _, dep := range manifest.Dependencies {
		if dep.ID == "" {
			t.Fatal("manifest contains dependency with blank id")
		}

		if _, exists := entries[dep.ID]; exists {
			t.Fatalf("manifest contains duplicate dependency id %q", dep.ID)
		}

		entries[dep.ID] = dep
	}

	return entries
}

func assertInstallerMentionsPackageOrCommand(t *testing.T, installer, id string, install externalDependencyInstall) {
	t.Helper()

	if install.Command != "" {
		for _, part := range strings.Fields(install.Command) {
			if !strings.Contains(installer, part) {
				t.Fatalf("%s installer guidance command %q not reflected in install script", id, install.Command)
			}
		}

		return
	}

	if install.Package == "" {
		t.Fatalf("%s installer guidance has neither command nor package: %#v", id, install)
	}

	for _, part := range strings.Fields(install.Package) {
		if !strings.Contains(installer, part) {
			t.Fatalf("%s installer package %q not reflected in install script", id, install.Package)
		}
	}
}
