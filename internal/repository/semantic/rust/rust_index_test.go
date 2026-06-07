package rust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRustFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestBuildRustIndexEmpty(t *testing.T) {
	root := t.TempDir()
	idx, err := BuildRustIndex(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != nil {
		t.Fatalf("expected nil index when no .rs files are present, got %#v", idx)
	}
}

func TestBuildRustIndexBasics(t *testing.T) {
	root := t.TempDir()
	writeRustFixture(t, filepath.Join(root, "Cargo.toml"), `
[package]
name = "fixture"
version = "0.1.0"
edition = "2021"
`)
	writeRustFixture(t, filepath.Join(root, "src", "main.rs"), `
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
	writeRustFixture(t, filepath.Join(root, "src", "worker.rs"), `
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
	writeRustFixture(t, filepath.Join(root, "src", "lib_utils.rs"), `
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx == nil {
		t.Fatalf("expected index, got nil")
	}
	if len(idx.Files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(idx.Files))
	}

	mainFile := idx.FileByPath("src/main.rs")
	if mainFile == nil {
		t.Fatalf("src/main.rs not indexed")
	}
	if !mainFile.HasMain {
		t.Fatalf("src/main.rs should be marked HasMain")
	}
	if !mainFile.IsBinRoot {
		t.Fatalf("src/main.rs should be marked IsBinRoot")
	}
	if len(mainFile.Modules) != 2 {
		t.Fatalf("expected 2 mod decls in main.rs, got %v", mainFile.Modules)
	}

	workerFile := idx.FileByPath("src/worker.rs")
	if workerFile == nil {
		t.Fatalf("src/worker.rs not indexed")
	}
	if workerFile.Impls == 0 {
		t.Fatalf("worker.rs should have impl blocks, got %d", workerFile.Impls)
	}
	if workerFile.Traits == 0 {
		t.Fatalf("worker.rs should have trait blocks")
	}
	if workerFile.Methods == 0 {
		t.Fatalf("worker.rs should have methods, got %d", workerFile.Methods)
	}

	utilsFile := idx.FileByPath("src/lib_utils.rs")
	if utilsFile == nil {
		t.Fatalf("src/lib_utils.rs not indexed")
	}
	if utilsFile.Macros == 0 {
		t.Fatalf("utils.rs should have at least one macro_rules!")
	}
	if !utilsFile.HasTest {
		t.Fatalf("utils.rs should be marked HasTest")
	}

	// FindSymbols matches by name substring.
	matches := idx.FindSymbols("Worker", 8)
	if len(matches) == 0 {
		t.Fatalf("expected FindSymbols to find Worker")
	}
	if matches := idx.FindSymbols("pub struct Worker", 8); len(matches) == 0 || matches[0].Name != "Worker" {
		t.Fatalf("expected declaration-shaped struct search to find Worker, got %#v", matches)
	}
	if matches := idx.FindSymbols("fn helper", 8); len(matches) == 0 || matches[0].Name != "helper" {
		t.Fatalf("expected declaration-shaped function search to find helper, got %#v", matches)
	}
	if matches := idx.FindSymbols("pub const fn default_worker_id", 8); len(matches) == 0 || matches[0].Name != "default_worker_id" {
		t.Fatalf("expected declaration-shaped const fn search to find default_worker_id, got %#v", matches)
	}
	if matches := idx.FindSymbols("pub unsafe trait UnsafeRunnable", 8); len(matches) == 0 || matches[0].Name != "UnsafeRunnable" {
		t.Fatalf("expected declaration-shaped unsafe trait search to find UnsafeRunnable, got %#v", matches)
	}
	if matches := idx.FindSymbols("pub type WorkerId", 8); len(matches) == 0 || matches[0].Name != "WorkerId" {
		t.Fatalf("expected declaration-shaped type alias search to find WorkerId, got %#v", matches)
	}
	if matches := idx.FindSymbols("pub static mut GLOBAL_MAX", 8); len(matches) == 0 || matches[0].Name != "GLOBAL_MAX" {
		t.Fatalf("expected declaration-shaped static search to find GLOBAL_MAX, got %#v", matches)
	}
	if matches := idx.FindSymbols("macro_rules! bark", 8); len(matches) == 0 || matches[0].Name != "bark" {
		t.Fatalf("expected declaration-shaped macro search to find bark, got %#v", matches)
	}

	// Trait implementer lookup.
	impls := idx.TraitImplementers("Runnable", 8)
	if len(impls) == 0 {
		t.Fatalf("expected Runnable trait implementer, got none")
	}
	if !strings.Contains(impls[0].Qualified, "Runnable for Worker") {
		t.Fatalf("unexpected trait impl qualified: %q", impls[0].Qualified)
	}

	// Calls-from on main() should see helper + spawn.
	callsOut := idx.FindCallsFrom("main", 16)
	if len(callsOut) == 0 {
		t.Fatalf("expected FindCallsFrom(main) to return calls")
	}

	// Calls-into on helper should see main.
	callsIn := idx.FindCallsInto("helper", 16)
	if len(callsIn) == 0 {
		t.Fatalf("expected FindCallsInto(helper) to return callers")
	}

	// Entrypoint files should surface main.rs first.
	entries := idx.EntrypointFiles(4)
	if len(entries) == 0 || entries[0] != "src/main.rs" {
		t.Fatalf("expected main.rs as top entrypoint, got %v", entries)
	}

	// Module siblings: main.rs declares `mod worker;` + `mod lib_utils;`.
	siblings := idx.ModuleSiblings("src/main.rs", 8)
	if len(siblings) == 0 {
		t.Fatalf("expected module siblings, got none")
	}
}

func TestRustIndexTestAttributesPromoteKind(t *testing.T) {
	root := t.TempDir()
	writeRustFixture(t, filepath.Join(root, "src", "lib.rs"), `
#[test]
fn alpha() {}

#[bench]
fn beta(b: &mut test::Bencher) {}

fn plain() {}
`)
	idx, err := BuildRustIndex(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	file := idx.FileByPath("src/lib.rs")
	if file == nil {
		t.Fatalf("src/lib.rs not indexed")
	}
	kinds := map[string]string{}
	for _, sym := range file.Symbols {
		kinds[sym.Name] = sym.Kind
	}
	if kinds["alpha"] != "test" {
		t.Fatalf("#[test] fn should be kind=test, got %q", kinds["alpha"])
	}
	if kinds["beta"] != "bench" {
		t.Fatalf("#[bench] fn should be kind=bench, got %q", kinds["beta"])
	}
	if kinds["plain"] != "func" {
		t.Fatalf("plain fn should be kind=func, got %q", kinds["plain"])
	}
}
