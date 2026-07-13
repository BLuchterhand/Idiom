package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/planexecute"
)

type developmentFixtureFile struct {
	path string
	body string
}

type productionDevelopmentScenario struct {
	summary            string
	initialFiles       []developmentFixtureFile
	edits              []planartifact.PlanEdit
	requiredPaths      []string
	authorizedNewFiles []string
	verification       []string
	expectedFiles      []developmentFixtureFile
}

var _ = ginkgo.Describe("Production development change families", ginkgo.Label("development-scenario", "e2e", "integration", "smoke"), func() {
	ginkgo.DescribeTable("applies and verifies a real hermetic repository change",
		func(scenario productionDevelopmentScenario) {
			t := ginkgo.GinkgoT()
			root := t.TempDir()
			writeDevelopmentFixture(root, developmentFixtureFile{path: "go.mod", body: "module example.com/development-scenario\n\ngo 1.22\n"})
			for _, file := range scenario.initialFiles {
				writeDevelopmentFixture(root, file)
			}

			cfg := config.Default()
			cfg.Workspace.Root = root
			cfg.Workspace.CWD = root
			cfg.Workspace.EnableWrites = true
			prompt := productionDevelopmentPrompt(scenario.summary, scenario.requiredPaths, scenario.authorizedNewFiles, scenario.verification)

			plannerCalls := 0
			promptRun := func(callCfg config.Config, _ string, opts planexecute.RunOptions, _ io.Writer) (string, error) {
				gomega.Expect(opts.TaskFamily).To(gomega.Equal(string(autonomy.TaskFamilyPlanThenExecutePlanner)))
				plannerCalls++
				result := workspace.NewManager(callCfg.Workspace).Execute(context.Background(), workspace.ToolRequest{
					Tool:      workspace.ToolNameSubmitPlan,
					Rationale: scenario.summary,
					Edits:     append([]planartifact.PlanEdit(nil), scenario.edits...),
				})
				if strings.TrimSpace(result.Error) != "" {
					return "", fmt.Errorf("submit plan: %s", result.Error)
				}

				return "Plan submitted.", nil
			}

			var stdout, stderr bytes.Buffer
			result := Run(cfg, planexecute.Options{
				Prompt:                           prompt,
				JSONEvents:                       true,
				SkipReviewRepair:                 true,
				RequiredEditPaths:                planexecute.MultiFlag(scenario.requiredPaths),
				AuthorizedNewFiles:               append([]string(nil), scenario.authorizedNewFiles...),
				VerificationPlanExplicitCommands: append([]string(nil), scenario.verification...),
			}, Options{
				IntentLoop: true,
				MaxCycles:  1,
			}, promptRun, nil, &stdout, &stderr)

			gomega.Expect(result.ExitCode).To(gomega.Equal(0), "stdout=%s\nstderr=%s", stdout.String(), stderr.String())
			gomega.Expect(result.Completion).NotTo(gomega.BeNil())
			gomega.Expect(plannerCalls).To(gomega.Equal(1))
			for _, command := range scenario.verification {
				gomega.Expect(result.Completion.Verification).To(gomega.ContainElement(gomega.And(
					gomega.HaveField("Command", command),
					gomega.HaveField("Status", plancontract.PhaseEvidenceStatusOK),
				)))
			}
			for _, expected := range scenario.expectedFiles {
				body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(expected.path)))
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(string(body)).To(gomega.Equal(expected.body), "path=%s", expected.path)
			}
		},
		ginkgo.Entry("greenfield service", productionDevelopmentScenario{
			summary: "Create a greenfield catalog greeting service that satisfies its pre-existing acceptance test.",
			initialFiles: []developmentFixtureFile{{path: "catalog/service_test.go", body: `package catalog

import "testing"

func TestGreeting(t *testing.T) {
	if got := Greeting("Ada"); got != "hello, Ada" {
		t.Fatalf("Greeting() = %q", got)
	}
}
`}},
			edits: []planartifact.PlanEdit{{
				Path:       "catalog/service.go",
				ReplaceAll: true,
				NewText:    "package catalog\n\nfunc Greeting(name string) string { return \"hello, \" + name }\n",
				Purpose:    "implement the new catalog service",
			}},
			requiredPaths:      []string{"catalog/service.go"},
			authorizedNewFiles: []string{"catalog/service.go"},
			verification:       []string{"go test ./catalog/..."},
			expectedFiles:      []developmentFixtureFile{{path: "catalog/service.go", body: "package catalog\n\nfunc Greeting(name string) string { return \"hello, \" + name }\n"}},
		}),
		ginkgo.Entry("bug fix", productionDevelopmentScenario{
			summary: "Fix identifier parsing so whitespace-only input is rejected and valid identifiers are trimmed.",
			initialFiles: []developmentFixtureFile{
				{path: "parser/parser.go", body: "package parser\n\nfunc ParseID(raw string) (string, error) { return raw, nil }\n"},
				{path: "parser/parser_test.go", body: `package parser

import "testing"

func TestParseID(t *testing.T) {
	if _, err := ParseID("   "); err == nil { t.Fatal("whitespace-only identifier must fail") }
	if got, err := ParseID(" item-1 "); err != nil || got != "item-1" { t.Fatalf("got %q, %v", got, err) }
}
`},
			},
			edits: []planartifact.PlanEdit{{
				Path:    "parser/parser.go",
				OldText: "package parser\n\nfunc ParseID(raw string) (string, error) { return raw, nil }\n",
				NewText: "package parser\n\nimport (\n\t\"errors\"\n\t\"strings\"\n)\n\nfunc ParseID(raw string) (string, error) {\n\tid := strings.TrimSpace(raw)\n\tif id == \"\" { return \"\", errors.New(\"identifier is empty\") }\n\treturn id, nil\n}\n",
				Purpose: "reject empty identifiers and normalize valid input",
			}},
			requiredPaths: []string{"parser/parser.go"},
			verification:  []string{"go test ./parser/..."},
			expectedFiles: []developmentFixtureFile{{path: "parser/parser.go", body: "package parser\n\nimport (\n\t\"errors\"\n\t\"strings\"\n)\n\nfunc ParseID(raw string) (string, error) {\n\tid := strings.TrimSpace(raw)\n\tif id == \"\" { return \"\", errors.New(\"identifier is empty\") }\n\treturn id, nil\n}\n"}},
		}),
		ginkgo.Entry("cross-package refactor", productionDevelopmentScenario{
			summary: "Move name-validation authority from registration into validation, update the caller, and remove the old local policy.",
			initialFiles: []developmentFixtureFile{
				{path: "validation/rules.go", body: "package validation\n\nfunc ValidName(name string) bool { return name != \"\" }\n"},
				{path: "registration/registration.go", body: "package registration\n\nfunc CanRegister(name string, active bool) bool { return active && validName(name) }\n\nfunc validName(name string) bool { return name != \"\" && name != \"root\" }\n"},
				{path: "registration/registration_test.go", body: `package registration

import "testing"

func TestCanRegister(t *testing.T) {
	if CanRegister("   ", true) { t.Fatal("blank name must fail") }
	if CanRegister("root", true) { t.Fatal("reserved name must fail") }
	if CanRegister("alice", false) { t.Fatal("inactive registration must fail") }
	if !CanRegister("alice", true) { t.Fatal("active ordinary name must pass") }
}
`},
			},
			edits: []planartifact.PlanEdit{
				{Path: "validation/rules.go", OldText: "package validation\n\nfunc ValidName(name string) bool { return name != \"\" }\n", NewText: "package validation\n\nimport \"strings\"\n\nfunc ValidName(name string) bool {\n\tname = strings.TrimSpace(name)\n\treturn name != \"\" && name != \"root\"\n}\n", Purpose: "own the complete validation policy"},
				{Path: "registration/registration.go", OldText: "package registration\n\nfunc CanRegister(name string, active bool) bool { return active && validName(name) }\n\nfunc validName(name string) bool { return name != \"\" && name != \"root\" }\n", NewText: "package registration\n\nimport \"example.com/development-scenario/validation\"\n\nfunc CanRegister(name string, active bool) bool {\n\treturn active && validation.ValidName(name)\n}\n", Purpose: "move the caller to validation and remove the old local authority"},
			},
			requiredPaths: []string{"validation/rules.go", "registration/registration.go"},
			verification:  []string{"go test ./..."},
			expectedFiles: []developmentFixtureFile{
				{path: "validation/rules.go", body: "package validation\n\nimport \"strings\"\n\nfunc ValidName(name string) bool {\n\tname = strings.TrimSpace(name)\n\treturn name != \"\" && name != \"root\"\n}\n"},
				{path: "registration/registration.go", body: "package registration\n\nimport \"example.com/development-scenario/validation\"\n\nfunc CanRegister(name string, active bool) bool {\n\treturn active && validation.ValidName(name)\n}\n"},
			},
		}),
		ginkgo.Entry("schema migration", productionDevelopmentScenario{
			summary: "Migrate durable session records and their checked-in schema from version one to version two.",
			initialFiles: []developmentFixtureFile{
				{path: "session/migrate.go", body: "package session\n\nimport \"encoding/json\"\n\ntype legacyRecord struct { Name string `json:\"name\"` }\ntype Record struct { Version int `json:\"version\"`; DisplayName string `json:\"display_name\"` }\n\nfunc Migrate(body []byte) (Record, error) {\n\tvar old legacyRecord\n\tif err := json.Unmarshal(body, &old); err != nil { return Record{}, err }\n\treturn Record{Version: 1, DisplayName: old.Name}, nil\n}\n"},
				{path: "session/schema/session-v2.json", body: "{\"version\":1,\"required\":[\"name\"]}\n"},
				{path: "session/migrate_test.go", body: `package session

import (
	"encoding/json"
	"os"
	"testing"
)

func TestMigrationAndSchema(t *testing.T) {
	got, err := Migrate([]byte(` + "`" + `{"name":"Ada"}` + "`" + `))
	if err != nil || got.Version != 2 || got.DisplayName != "Ada" { t.Fatalf("record=%+v err=%v", got, err) }
	body, err := os.ReadFile("schema/session-v2.json")
	if err != nil { t.Fatal(err) }
	var schema struct { Version int ` + "`" + `json:"version"` + "`" + `; Required []string ` + "`" + `json:"required"` + "`" + ` }
	if err := json.Unmarshal(body, &schema); err != nil { t.Fatal(err) }
	if schema.Version != 2 || len(schema.Required) != 1 || schema.Required[0] != "display_name" { t.Fatalf("schema=%+v", schema) }
}
`},
			},
			edits: []planartifact.PlanEdit{
				{Path: "session/migrate.go", OldText: "return Record{Version: 1, DisplayName: old.Name}, nil", NewText: "return Record{Version: 2, DisplayName: old.Name}, nil", Purpose: "emit version-two durable records"},
				{Path: "session/schema/session-v2.json", ReplaceAll: true, NewText: "{\"version\":2,\"required\":[\"display_name\"]}\n", Purpose: "publish the version-two schema"},
			},
			requiredPaths: []string{"session/migrate.go", "session/schema/session-v2.json"},
			verification:  []string{"go test ./session/..."},
			expectedFiles: []developmentFixtureFile{
				{path: "session/migrate.go", body: "package session\n\nimport \"encoding/json\"\n\ntype legacyRecord struct { Name string `json:\"name\"` }\ntype Record struct { Version int `json:\"version\"`; DisplayName string `json:\"display_name\"` }\n\nfunc Migrate(body []byte) (Record, error) {\n\tvar old legacyRecord\n\tif err := json.Unmarshal(body, &old); err != nil { return Record{}, err }\n\treturn Record{Version: 2, DisplayName: old.Name}, nil\n}\n"},
				{path: "session/schema/session-v2.json", body: "{\"version\":2,\"required\":[\"display_name\"]}\n"},
			},
		}),
		ginkgo.Entry("documentation and configuration", productionDevelopmentScenario{
			summary: "Align retry configuration with its operator documentation and validate both artifacts together.",
			initialFiles: []developmentFixtureFile{
				{path: "config/app.json", body: "{\"retry_limit\":1}\n"},
				{path: "docs/configuration.md", body: "# Configuration\n\nRetries are not configurable.\n"},
				{path: "policy/policy_test.go", body: `package policy

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRetryConfigurationIsDocumented(t *testing.T) {
	body, err := os.ReadFile("../config/app.json")
	if err != nil { t.Fatal(err) }
	var cfg struct { RetryLimit int ` + "`" + `json:"retry_limit"` + "`" + ` }
	if err := json.Unmarshal(body, &cfg); err != nil { t.Fatal(err) }
	docs, err := os.ReadFile("../docs/configuration.md")
	if err != nil { t.Fatal(err) }
	if cfg.RetryLimit != 3 || !strings.Contains(string(docs), "retry_limit: 3") { t.Fatalf("config=%+v docs=%s", cfg, docs) }
}
`},
			},
			edits: []planartifact.PlanEdit{
				{Path: "config/app.json", ReplaceAll: true, NewText: "{\"retry_limit\":3}\n", Purpose: "set the reviewed retry limit"},
				{Path: "docs/configuration.md", ReplaceAll: true, NewText: "# Configuration\n\nSet `retry_limit: 3` for bounded transient retries.\n", Purpose: "document the reviewed retry limit"},
			},
			requiredPaths: []string{"config/app.json", "docs/configuration.md"},
			verification:  []string{"go test ./policy/..."},
			expectedFiles: []developmentFixtureFile{
				{path: "config/app.json", body: "{\"retry_limit\":3}\n"},
				{path: "docs/configuration.md", body: "# Configuration\n\nSet `retry_limit: 3` for bounded transient retries.\n"},
			},
		}),
		ginkgo.Entry("vendored dependency upgrade", productionDevelopmentScenario{
			summary: "Upgrade a hermetically vendored formatter dependency while keeping its manifest, checksum lock, vendor metadata, and embedded release payload consistent.",
			initialFiles: []developmentFixtureFile{
				{path: "go.mod", body: "module example.com/development-scenario\n\ngo 1.22\n\nrequire example.com/format v1.0.0\n"},
				{path: "go.sum", body: "example.com/format v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nexample.com/format v1.0.0/go.mod h1:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=\n"},
				{path: "vendor/modules.txt", body: "# example.com/format v1.0.0\n## explicit; go 1.22\nexample.com/format\n"},
				{path: "vendor/example.com/format/VERSION", body: "v1.0.0\n"},
				{path: "vendor/example.com/format/format.go", body: `package format

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var release string

func CurrentVersion() string { return strings.TrimSpace(release) }
`},
				{path: "app/dependency_test.go", body: `package app

import (
	"os"
	"strings"
	"testing"

	"example.com/format"
)

func TestVendoredFormatterRelease(t *testing.T) {
	if got := format.CurrentVersion(); got != "v1.1.0" {
		t.Fatalf("CurrentVersion() = %q, want v1.1.0", got)
	}
	lock, err := os.ReadFile("../go.sum")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lock), "example.com/format v1.1.0 ") || strings.Contains(string(lock), "example.com/format v1.0.0 ") {
		t.Fatalf("checksum lock does not exclusively select v1.1.0:\n%s", lock)
	}
}
`},
			},
			edits: []planartifact.PlanEdit{
				{Path: "vendor/example.com/format/VERSION", ReplaceAll: true, NewText: "v1.1.0\n", Purpose: "install the reviewed vendored release payload"},
				{Path: "go.sum", ReplaceAll: true, NewText: "example.com/format v1.1.0 h1:CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=\nexample.com/format v1.1.0/go.mod h1:DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD=\n", Purpose: "lock the reviewed dependency checksums"},
				{Path: "vendor/modules.txt", ReplaceAll: true, NewText: "# example.com/format v1.1.0\n## explicit; go 1.22\nexample.com/format\n", Purpose: "align Go vendor metadata with the upgraded module"},
				{Path: "go.mod", ReplaceAll: true, NewText: "module example.com/development-scenario\n\ngo 1.22\n\nrequire example.com/format v1.1.0\n", Purpose: "select the upgraded formatter dependency"},
			},
			requiredPaths: []string{"go.mod", "go.sum", "vendor/modules.txt", "vendor/example.com/format/VERSION"},
			verification:  []string{"go test -mod=vendor ./..."},
			expectedFiles: []developmentFixtureFile{
				{path: "go.mod", body: "module example.com/development-scenario\n\ngo 1.22\n\nrequire example.com/format v1.1.0\n"},
				{path: "go.sum", body: "example.com/format v1.1.0 h1:CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=\nexample.com/format v1.1.0/go.mod h1:DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD=\n"},
				{path: "vendor/modules.txt", body: "# example.com/format v1.1.0\n## explicit; go 1.22\nexample.com/format\n"},
				{path: "vendor/example.com/format/VERSION", body: "v1.1.0\n"},
			},
		}),
		ginkgo.Entry("build and CI pipeline", productionDevelopmentScenario{
			summary: "Route CI through the repository-owned portable verification script and add vet without hiding the commands the pipeline actually executes.",
			initialFiles: []developmentFixtureFile{
				{path: "cmd/service/main.go", body: "package main\n\nfunc main() {}\n"},
				{path: ".github/workflows/ci.yml", body: "name: ci\n\non: [push]\n\njobs:\n  verify:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-go@v5\n      - run: go test ./...\n"},
				{path: "scripts/verify_ci.go", body: `package main

import (
	"fmt"
	"os"
	"os/exec"
)

func main() {
	command := exec.Command("go", "test", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		fmt.Fprintln(os.Stderr, string(output))
		os.Exit(1)
	}
}
`},
				{path: "scripts/verify_ci_test.go", body: `package main

import (
	"os/exec"
	"testing"
)

func TestVerificationScript(t *testing.T) {
	command := exec.Command("go", "run", "./scripts/verify_ci.go")
	command.Dir = ".."
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("verification script failed: %v\n%s", err, output)
	}
}
`},
			},
			edits: []planartifact.PlanEdit{
				{Path: "scripts/verify_ci.go", OldText: `package main

import (
	"fmt"
	"os"
	"os/exec"
)

func main() {
	command := exec.Command("go", "test", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		fmt.Fprintln(os.Stderr, string(output))
		os.Exit(1)
	}
}
`, NewText: `package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	workflow, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !strings.Contains(string(workflow), "run: go run ./scripts/verify_ci.go") {
		fmt.Fprintln(os.Stderr, "CI workflow does not invoke the repository verification script")
		os.Exit(1)
	}

	tests := exec.Command("go", "test", "./cmd/service")
	if output, err := tests.CombinedOutput(); err != nil {
		fmt.Fprintln(os.Stderr, string(output))
		os.Exit(1)
	}

	vet := exec.Command("go", "vet", "./cmd/service")
	if output, err := vet.CombinedOutput(); err != nil {
		fmt.Fprintln(os.Stderr, string(output))
		os.Exit(1)
	}
}
`, Purpose: "make the repository verifier check workflow ownership and run both test and vet"},
				{Path: ".github/workflows/ci.yml", ReplaceAll: true, NewText: "name: ci\n\non: [push]\n\njobs:\n  verify:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-go@v5\n      - run: go run ./scripts/verify_ci.go\n", Purpose: "make CI execute the repository-owned verification contract"},
			},
			requiredPaths: []string{"scripts/verify_ci.go", ".github/workflows/ci.yml"},
			verification:  []string{"go test ./scripts/..."},
			expectedFiles: []developmentFixtureFile{
				{path: "scripts/verify_ci.go", body: `package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	workflow, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !strings.Contains(string(workflow), "run: go run ./scripts/verify_ci.go") {
		fmt.Fprintln(os.Stderr, "CI workflow does not invoke the repository verification script")
		os.Exit(1)
	}

	tests := exec.Command("go", "test", "./cmd/service")
	if output, err := tests.CombinedOutput(); err != nil {
		fmt.Fprintln(os.Stderr, string(output))
		os.Exit(1)
	}

	vet := exec.Command("go", "vet", "./cmd/service")
	if output, err := vet.CombinedOutput(); err != nil {
		fmt.Fprintln(os.Stderr, string(output))
		os.Exit(1)
	}
}
`},
				{path: ".github/workflows/ci.yml", body: "name: ci\n\non: [push]\n\njobs:\n  verify:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-go@v5\n      - run: go run ./scripts/verify_ci.go\n"},
			},
		}),
	)
})

func writeDevelopmentFixture(root string, file developmentFixtureFile) {
	abs := filepath.Join(root, filepath.FromSlash(file.path))
	gomega.Expect(os.MkdirAll(filepath.Dir(abs), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(abs, []byte(file.body), 0o644)).To(gomega.Succeed())
}

func productionDevelopmentPrompt(summary string, requiredPaths []string, authorizedNewPaths []string, commands []string) string {
	tests := make([]map[string]string, 0, len(commands))
	for i, command := range commands {
		tests = append(tests, map[string]string{"id": fmt.Sprintf("verification-%d", i+1), "command": command})
	}
	authorizedNewFiles := make([]map[string]string, 0, len(authorizedNewPaths))
	for _, path := range authorizedNewPaths {
		authorizedNewFiles = append(authorizedNewFiles, map[string]string{
			"path":   path,
			"reason": "greenfield file required by the scenario contract",
			"source": "development-scenario fixture",
		})
	}
	contract, err := json.Marshal(map[string]any{"atoms": []map[string]any{{
		"atom_id":              "development-change",
		"required_edit_paths":  requiredPaths,
		"required_tests":       tests,
		"authorized_new_files": authorizedNewFiles,
		"terminal_evidence_requirements": map[string]bool{
			"explicit":               true,
			"require_verification":   true,
			"require_required_tests": true,
		},
	}}})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	return summary + "\n\nTyped plan execution contract:\n" + string(contract)
}

var _ = ginkgo.It("repairs a failed implementation through the production intent-loop runner", ginkgo.Label("development-scenario", "e2e", "integration", "smoke"), func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	gomega.Expect(os.MkdirAll(filepath.Join(root, "pkg"), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/runner-scenario\n\ngo 1.26.2\n"), 0o644)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(
		filepath.Join(root, "pkg", "value.go"),
		[]byte("package pkg\n\nfunc Value() int { return 1 }\n"),
		0o644,
	)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(
		filepath.Join(root, "pkg", "value_test.go"),
		[]byte("package pkg\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 3 { t.Fatalf(\"Value() = %d, want 3\", Value()) } }\n"),
		0o644,
	)).To(gomega.Succeed())

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Workspace.EnableWrites = true
	command := "go test ./pkg/..."
	prompt := developmentRunnerPrompt(command)

	var plannerPrompts []string
	promptRun := func(callCfg config.Config, plannerPrompt string, opts planexecute.RunOptions, _ io.Writer) (string, error) {
		gomega.Expect(opts.TaskFamily).To(gomega.Equal(string(autonomy.TaskFamilyPlanThenExecutePlanner)))
		gomega.Expect(opts.UseActiveModel).To(gomega.BeTrue())
		gomega.Expect(callCfg.LlamaServer.Model).To(gomega.Equal("scenario-model"))
		plannerPrompts = append(plannerPrompts, plannerPrompt)

		body, err := os.ReadFile(filepath.Join(root, "pkg", "value.go"))
		if err != nil {
			return "", err
		}
		oldValue := "return 1"
		newValue := "return 2"
		if strings.Contains(string(body), "return 2") {
			oldValue = "return 2"
			newValue = "return 3"
		}

		result := workspace.NewManager(callCfg.Workspace).Execute(context.Background(), workspace.ToolRequest{
			Tool:      workspace.ToolNameSubmitPlan,
			Rationale: "Repair Value while preserving the existing public API.",
			Edits: []planartifact.PlanEdit{{
				Path:    "pkg/value.go",
				OldText: oldValue,
				NewText: newValue,
				Purpose: "make Value satisfy its regression test",
			}},
		})
		if strings.TrimSpace(result.Error) != "" {
			return "", fmt.Errorf("submit plan: %s", result.Error)
		}

		return "Plan submitted.", nil
	}

	var stdout, stderr bytes.Buffer
	result := Run(cfg, planexecute.Options{
		Prompt:                           prompt,
		Model:                            "scenario-model",
		JSONEvents:                       true,
		SkipReviewRepair:                 true,
		RequiredEditPaths:                planexecute.MultiFlag{"pkg/value.go"},
		VerificationPlanExplicitCommands: []string{command},
	}, Options{
		IntentLoop: true,
		MaxCycles:  2,
	}, promptRun, nil, &stdout, &stderr)

	gomega.Expect(result.ExitCode).To(gomega.Equal(0), "stdout=%s\nstderr=%s", stdout.String(), stderr.String())
	gomega.Expect(result.Completion).NotTo(gomega.BeNil())
	gomega.Expect(plannerPrompts).To(gomega.HaveLen(2), "stdout=%s\nstderr=%s", stdout.String(), stderr.String())
	gomega.Expect(plannerPrompts[1]).To(gomega.And(
		gomega.ContainSubstring("Prior Cycles"),
		gomega.ContainSubstring(command),
	))
	finalBody, err := os.ReadFile(filepath.Join(root, "pkg", "value.go"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(string(finalBody)).To(gomega.ContainSubstring("return 3"))
	gomega.Expect(result.Completion.Verification).To(gomega.ContainElement(gomega.And(
		gomega.HaveField("Command", command),
		gomega.HaveField("Status", plancontract.PhaseEvidenceStatusOK),
	)))
})

func developmentRunnerPrompt(command string) string {
	return `Repair pkg/value.go so Value returns 3 and the existing regression test passes.

Typed plan execution contract:
{
  "atoms": [{
    "atom_id": "repair-value",
    "required_edit_paths": ["pkg/value.go"],
    "required_tests": [{"id": "value-regression", "command": "` + command + `"}],
    "terminal_evidence_requirements": {
      "explicit": true,
      "require_verification": true,
      "require_required_tests": true
    }
  }]
}`
}
