package rust

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func writeRustFixture(path, content string) {
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644)).To(Succeed())
}

var _ = Describe("Rust semantic index", func() {
	It("returns no index when the workspace has no Rust source", func() {
		idx, err := BuildRustIndex(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).To(BeNil())
	})

	It("indexes declarations, modules, traits, calls, tests, and entrypoints", func() {
		root := GinkgoT().TempDir()
		writeRustFixture(filepath.Join(root, "Cargo.toml"), `
[package]
name = "fixture"
version = "0.1.0"
edition = "2021"
`)
		writeRustFixture(filepath.Join(root, "src", "main.rs"), `
mod worker;
mod lib_utils;

fn helper() -> i32 {
    42
}

fn main() {
    let value = helper();
    worker::spawn(value);
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
    println!("{}", id);
}
`)
		writeRustFixture(filepath.Join(root, "src", "lib_utils.rs"), `
pub const DEFAULT_MAX: u32 = 64;

pub static mut GLOBAL_MAX: u32 = 64;

pub const fn default_worker_id() -> u32 {
    0
}

macro_rules! bark {
    () => { println!("woof"); }
}

#[test]
fn it_works() {
    assert_eq!(2 + 2, 4);
}
`)

		idx, err := BuildRustIndex(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).NotTo(BeNil())
		Expect(idx.Files).To(HaveLen(3))

		mainFile := idx.FileByPath("src/main.rs")
		Expect(mainFile).NotTo(BeNil())
		Expect(mainFile.HasMain).To(BeTrue())
		Expect(mainFile.IsBinRoot).To(BeTrue())
		Expect(mainFile.Modules).To(HaveLen(2))

		workerFile := idx.FileByPath("src/worker.rs")
		Expect(workerFile).NotTo(BeNil())
		Expect(workerFile.Impls).To(BeNumerically(">", 0))
		Expect(workerFile.Traits).To(BeNumerically(">", 0))
		Expect(workerFile.Methods).To(BeNumerically(">", 0))

		utilsFile := idx.FileByPath("src/lib_utils.rs")
		Expect(utilsFile).NotTo(BeNil())
		Expect(utilsFile.Macros).To(BeNumerically(">", 0))
		Expect(utilsFile.HasTest).To(BeTrue())
		Expect(idx.FindSymbols("Worker", 8)).NotTo(BeEmpty())

		for query, expected := range map[string]string{
			"pub struct Worker":               "Worker",
			"fn helper":                       "helper",
			"pub const fn default_worker_id":  "default_worker_id",
			"pub unsafe trait UnsafeRunnable": "UnsafeRunnable",
			"pub type WorkerId":               "WorkerId",
			"pub static mut GLOBAL_MAX":       "GLOBAL_MAX",
			"macro_rules! bark":               "bark",
		} {
			matches := idx.FindSymbols(query, 8)
			Expect(matches).NotTo(BeEmpty(), "query %q", query)
			Expect(matches[0].Name).To(Equal(expected))
		}

		implementations := idx.TraitImplementers("Runnable", 8)
		Expect(implementations).NotTo(BeEmpty())
		Expect(implementations[0].Qualified).To(ContainSubstring("Runnable for Worker"))
		Expect(idx.FindCallsFrom("main", 16)).NotTo(BeEmpty())
		Expect(idx.FindCallsInto("helper", 16)).NotTo(BeEmpty())
		entrypoints := idx.EntrypointFiles(4)
		Expect(entrypoints).NotTo(BeEmpty())
		Expect(entrypoints[0]).To(Equal("src/main.rs"))
		Expect(idx.ModuleSiblings("src/main.rs", 8)).NotTo(BeEmpty())
	})

	It("promotes test and benchmark functions according to their attributes", func() {
		root := GinkgoT().TempDir()
		writeRustFixture(filepath.Join(root, "src", "lib.rs"), `
#[test]
fn alpha() {}

#[bench]
fn beta(b: &mut test::Bencher) {}

fn plain() {}
`)

		idx, err := BuildRustIndex(root)
		Expect(err).NotTo(HaveOccurred())
		file := idx.FileByPath("src/lib.rs")
		Expect(file).NotTo(BeNil())

		kinds := map[string]string{}
		for _, symbol := range file.Symbols {
			kinds[symbol.Name] = symbol.Kind
		}
		Expect(kinds).To(HaveKeyWithValue("alpha", "test"))
		Expect(kinds).To(HaveKeyWithValue("beta", "bench"))
		Expect(kinds).To(HaveKeyWithValue("plain", "func"))
	})
})
