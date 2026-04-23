# Rust tooling

idiom ships the same 13-capability deterministic surface for Rust that it
already exposes for Go and C/C++. The tools are prefixed `rust_` and are
automatically offered to the model when a workspace contains a
`Cargo.toml` or any `.rs` files.

## Installing the LSP (optional, strongly recommended)

Tools that don't require an LSP work out of the box from the semantic
index. Tools that benefit from precise type resolution (`references`,
`definition`, `implementations`, `calls_from`, `calls_into`,
`workspace_symbol`) will transparently upgrade when rust-analyzer is on
your `PATH`.

Install rust-analyzer via one of:

- `rustup component add rust-analyzer` (preferred — pinned to your
  toolchain and updated with `rustup update`)
- standalone binary from https://rust-analyzer.github.io/

idiom does not bundle rust-analyzer. If it is not available the tools
gracefully fall back to the semantic index (tree-walker) and — for
`rust_references` — an `rg` regex fallback. `rust_diagnostics` requires
`cargo` in `PATH`; if it is missing the tool returns a clear install
hint pointing at https://rustup.rs/.

## Capabilities

| Capability | Tool | Source |
|---|---|---|
| file_outline       | `rust_file_outline`       | semantic index |
| find_symbol        | `rust_find_symbol`        | semantic index |
| workspace_symbol   | `rust_workspace_symbol`   | rust-analyzer first, semantic fallback |
| definition         | `rust_definition`         | rust-analyzer first, semantic fallback |
| references         | `rust_references`         | rust-analyzer first, rg fallback |
| calls_from         | `rust_calls_from`         | rust-analyzer first, semantic fallback |
| calls_into         | `rust_calls_into`         | rust-analyzer first, semantic fallback |
| read_symbol        | `rust_read_symbol`        | semantic index |
| related_files      | `rust_related_files`      | semantic index (score + module tree) |
| implementations    | `rust_implementations`    | rust-analyzer first, trait-impl fallback |
| diagnostics        | `rust_diagnostics`        | `cargo check --message-format=short` |
| overview           | `rust_overview`           | semantic index |
| entrypoints        | `rust_entrypoints`        | semantic index |

## Rust-specific semantic notes

- **Impl blocks** are first-class. `rust_find_symbol` returns them as
  kind `impl`; inherent impls are qualified by the target type (`impl
  Foo`) and trait impls carry the `Trait for Type` qualified name.
  `rust_file_outline` lists them distinctly from free functions.
- **Traits** are cataloged as `trait`. `rust_implementations` runs through
  rust-analyzer when available; without it, the fallback walks all
  `impl Trait for Type` blocks in the workspace. This is a purely
  syntactic match — it will miss blanket impls and associated types the
  way a tree-walker always does.
- **Macros** — `macro_rules!` definitions are indexed as kind `macro`.
  Procedural macros (the `#[proc_macro]`, `#[proc_macro_attribute]`, and
  `#[proc_macro_derive]` family) are *partially* supported: rust-analyzer
  expands them on-the-fly but the built-in semantic index only records
  their declaration, not the expanded tokens. If you depend on
  proc-macro-generated symbols, install rust-analyzer.
- **Modules** — `mod foo;` declarations are followed by
  `rust_related_files`, which pairs same-directory siblings with the
  `foo.rs` / `foo/mod.rs` targets the declaration points at.
- **Entrypoints** — `rust_entrypoints` promotes `fn main`, the
  `src/lib.rs` lib root, and any file carrying `#[test]` or `#[bench]`
  attributes. `cargo`-defined examples/benches under `examples/` and
  `benches/` are surfaced too.

## Excluded directories

The Rust walker skips the usual `target/`, `node_modules/`, `.git/`,
`build/`, `dist/`, `logs/`, `tmp/`, `bin/`, `models/`, `transcripts/`,
`.cache/`, `.hg/`, `.svn/` dirs.

## When rust-analyzer is slow to start

rust-analyzer needs to crate-graph your workspace before answering most
queries. On cold start (big monorepo, no prior cache) the first LSP
call can take 10–30 seconds. We launch and tear down a fresh client per
query, so there's no persistent daemon in-process — if you see slow
tools, that's rust-analyzer reading the dep graph, not idiom spinning.
For casual exploration you can opt out entirely by removing
rust-analyzer from `PATH`; the semantic-index fallbacks are
sub-millisecond for most workspaces.
