package languagetools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/repository/semantic/languagetools"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Rust workspace tools", func() {
	It("detects a Rust workspace", func() {
		_, root := newRustFixtureManager()
		Expect(languageDetects(root, "rust")).To(BeTrue())
	})

	DescribeTable("provides syntax-index discovery and fallback navigation",
		func(request workspace.ToolRequest, expectedOutput string) {
			manager, _ := newRustFixtureManager()
			expectToolSuccess(manager.Execute(context.Background(), request), expectedOutput)
		},
		Entry("overview", workspace.ToolRequest{Tool: "rust_overview"}, "Indexed 3 Rust files"),
		Entry("entrypoints", workspace.ToolRequest{Tool: "rust_entrypoints"}, "main.rs"),
		Entry("related files", workspace.ToolRequest{Tool: "rust_related_files", Pattern: "worker"}, "worker.rs"),
		Entry("implementations", workspace.ToolRequest{Tool: "rust_implementations", Pattern: "Runnable"}, "worker.rs"),
		Entry("calls from", workspace.ToolRequest{Tool: "rust_calls_from", Pattern: "main"}, "helper"),
		Entry("calls into", workspace.ToolRequest{Tool: "rust_calls_into", Pattern: "helper"}, "main"),
		Entry("definition", workspace.ToolRequest{Tool: "rust_definition", Pattern: "do_work"}, "worker.rs"),
		Entry("workspace symbol", workspace.ToolRequest{Tool: "rust_workspace_symbol", Pattern: "Worker"}, "worker.rs"),
	)

	It("normalizes declaration-shaped find-symbol queries", func() {
		manager, _ := newRustFixtureManager()
		for pattern, output := range map[string]string{
			"Worker":                    "worker.rs",
			"pub struct Worker":         "Worker",
			"pub type WorkerId":         "WorkerId",
			"pub static mut GLOBAL_MAX": "GLOBAL_MAX",
		} {
			expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{
				Tool: "rust_find_symbol", Pattern: pattern,
			}), output)
		}
	})

	It("rejects an empty find-symbol pattern", func() {
		manager, _ := newRustFixtureManager()
		Expect(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "rust_find_symbol"}).Error).NotTo(BeEmpty())
	})

	It("reads symbols from identifier and declaration-shaped patterns", func() {
		manager, _ := newRustFixtureManager()
		for _, expectation := range []struct{ pattern, path, output string }{
			{"spawn", filepath.Join("src", "worker.rs"), "spawn"},
			{"pub struct Worker", filepath.Join("src", "worker.rs"), "pub struct Worker"},
			{"pub const fn default_worker_id", filepath.Join("src", "utils.rs"), "pub const fn default_worker_id"},
			{"macro_rules! helper_macro", filepath.Join("src", "utils.rs"), "macro_rules! helper_macro"},
		} {
			expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{
				Tool: "rust_read_symbol", Pattern: expectation.pattern, Path: expectation.path,
			}), expectation.output)
		}
	})

	It("outlines Rust files", func() {
		manager, _ := newRustFixtureManager()
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: "rust_file_outline", Path: filepath.Join("src", "worker.rs"),
		}), "file: src/worker.rs", "Worker", "Runnable")
	})

	It("finds references through rust-analyzer or text fallback", func() {
		manager, _ := newRustFixtureManager()
		result := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "rust_references", Pattern: "helper", Path: "."})
		Expect(result.Error).To(BeEmpty())
		Expect(strings.ToLower(result.Summary)).To(Or(ContainSubstring("rust"), BeEmpty()))
		Expect(result.Summary).To(ContainSubstring("Rust"))
	})

	It("reports a missing cargo capability", func() {
		manager, _ := newRustFixtureManager()
		GinkgoT().Setenv("PATH", "")
		result := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "rust_diagnostics"})
		Expect(result.Error).To(ContainSubstring("cargo"))
	})

	It("uses rust-analyzer for precise operations when available", func() {
		if _, err := exec.LookPath("rust-analyzer"); err != nil {
			Skip("rust-analyzer not installed")
		}
		manager, _ := newRustFixtureManager()
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "rust_workspace_symbol", Pattern: "Worker"}), "Worker")
		Expect(manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: "rust_definition", Pattern: "spawn", Path: filepath.Join("src", "worker.rs"),
		}).Error).To(BeEmpty())
	})

	It("dispatches every cataloged Rust tool", func() {
		manager, _ := newRustFixtureManager()
		var names []string
		for _, spec := range languagetools.ToolCatalog() {
			if spec.Language == "rust" {
				names = append(names, spec.Tool)
			}
		}
		Expect(names).NotTo(BeEmpty())
		for _, name := range names {
			if name == "rust_diagnostics" {
				continue
			}
			request := workspace.ToolRequest{Tool: name}
			switch name {
			case "rust_overview", "rust_entrypoints":
			case "rust_file_outline":
				request.Path = filepath.Join("src", "worker.rs")
			case "rust_read_symbol":
				request.Pattern, request.Path = "spawn", filepath.Join("src", "worker.rs")
			default:
				request.Pattern = "Worker"
			}
			Expect(manager.Execute(context.Background(), request).Tool).NotTo(BeEmpty(), "tool %q", name)
		}
	})

	It("rejects an unknown Rust tool", func() {
		manager, _ := newRustFixtureManager()
		Expect(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "rust_bogus"}).Error).NotTo(BeEmpty())
	})
})

func newRustFixtureManager() (*workspace.Manager, string) {
	GinkgoHelper()
	root := GinkgoT().TempDir()
	writeRustFixture(filepath.Join(root, "Cargo.toml"), "[package]\nname = \"fixture\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	writeRustFixture(filepath.Join(root, "src", "main.rs"), `
mod worker;
mod utils;

fn helper() -> u32 {
    42
}

fn main() {
    let value = helper();
    worker::Worker::spawn(value).run();
}
`)
	writeRustFixture(filepath.Join(root, "src", "worker.rs"), `
pub struct Worker {
    pub id: u32,
}

pub trait Runnable {
    fn run(&self);
}

pub unsafe trait UnsafeRunnable {
    unsafe fn run_unchecked(&self);
}

pub type WorkerId = u32;

impl Runnable for Worker {
    fn run(&self) {
        do_work(self.id);
    }
}

impl Worker {
    pub fn spawn(id: u32) -> Worker {
        Worker { id }
    }
}

fn do_work(id: u32) {
    let _ = id;
}
`)
	writeRustFixture(filepath.Join(root, "src", "utils.rs"), `
pub const DEFAULT_MAX: u32 = 64;

pub static mut GLOBAL_MAX: u32 = 64;

pub const fn default_worker_id() -> u32 {
    0
}

macro_rules! helper_macro {
    () => { 1 };
}

#[test]
fn trivial() {
    assert_eq!(2 + 2, 4);
}
`)
	return newLanguageWorkspaceManager(root, 30), root
}

func writeRustFixture(path string, content string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644)).To(Succeed())
}
