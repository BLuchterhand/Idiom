# CLI Reference

This page lists the common public CLI surfaces. Run `idiom help` or
`idiom <command> --help` for the complete flag set.

## Agent Runs

```bash
idiom run --prompt "inspect this repository" --json
```

## Lanes

```bash
idiom lane plan-execute --title fix-parser path/to/brief.md
idiom lane implement --title small-change path/to/brief.md
```

`plan-execute` separates planning from execution. The planner produces a
bounded edit plan; the executor applies accepted edits through workspace tools.

## Workspace Tools

```bash
idiom tool list
idiom tool context_pack --input '{"task":"find parser code","mode":"plan","limit":8}'
idiom tool context_status --input '{"refresh":true,"limit":20}'
idiom tool coding_rules_check --input '{"path":"internal/workflows"}'
```

Useful discovery tools include `context_pack`, `context_query`,
`context_status`, `dependency_graph`, `history_range`, `history_symbol`,
`blame_range`, `semgrep_scan`, `coding_rules_check`, and `harness_audit`.

## Optional CLI Backends on Windows

The CLI checks these programs on `PATH`. Install only the tools needed for the
queries you use. The commands below install under the current Windows user and
do not require elevation. They assume Node/npm or Python is already available
for the package-based installs.

| CLI tool | Backend | Behavior without the backend |
| --- | --- | --- |
| `structural_search`; the structural layer in `discovery_query` when its pattern contains ast-grep metavariables such as `$NODE` | ast-grep (`ast-grep` or `sg`) | `structural_search` reports unavailable; other discovery layers still run. |
| `ctags_symbols`; the fallback symbol layer in `discovery_query` | Universal Ctags (`ctags`) | `ctags_symbols` reports unavailable; language-specific indexes remain available. |
| `semgrep_scan`; Semgrep-backed security layer in `discovery_query` | Semgrep (`semgrep`) | The direct scan reports unavailable; other discovery layers still run. |
| `cpp_diagnostics` | clangd plus a workspace `compile_commands.json` | Reports unavailable if either is missing. |
| `cpp_definition`, `cpp_declaration`, `cpp_implementations`, `cpp_references`, `cpp_calls_from`, `cpp_calls_into`, `cpp_workspace_symbol`, and `cpp_dead_symbol` | clangd for LSP precision | Several have local syntax or text fallbacks; `cpp_implementations` can report unavailable when its local fallback cannot resolve the implementation. |
| `dependency_graph` for TypeScript/JavaScript | madge (`madge`) | Uses local import extraction. |
| `dependency_graph` for Python | pydeps (`pydeps`) | Uses local import extraction. |
| `history_range`, `history_symbol`, `blame_range` | Git (`git`) | These history tools require Git; Ctags is not their backend. |

The repo's `scripts/install-requirements.ps1` uses Chocolatey for several
Windows dependencies, including Universal Ctags and LLVM/clangd. That route
may install outside the user profile or require elevation. For a per-user
setup, use the portable downloads or package commands below instead.

To keep executables under the current user's profile and add a folder to the
user PATH without elevation, use this PowerShell helper once per folder:

```powershell
$toolRoot = Join-Path $env:LOCALAPPDATA 'Idiom\tools'
New-Item -ItemType Directory -Force -Path $toolRoot | Out-Null

function Add-UserToolPath([string] $Directory) {
  $Directory = [IO.Path]::GetFullPath($Directory)
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $entries = @($userPath -split ';' | Where-Object { $_ })
  if (-not ($entries | Where-Object { $_.TrimEnd([char]'\') -ieq $Directory.TrimEnd([char]'\') })) {
    $entries += $Directory
    [Environment]::SetEnvironmentVariable('Path', ($entries -join ';'), 'User')
  }
  if (-not ($env:Path -split ';' | Where-Object { $_.TrimEnd([char]'\') -ieq $Directory.TrimEnd([char]'\') })) {
    $env:Path = "$Directory;$env:Path"
  }
}
```

### ast-grep

The [ast-grep CLI](https://ast-grep.github.io/guide/quick-start) is published
on npm. Install it under the profile-local npm prefix:

```powershell
$npmPrefix = Join-Path $toolRoot 'npm'
npm install --global --prefix $npmPrefix @ast-grep/cli
Add-UserToolPath $npmPrefix
ast-grep --version
```

### Universal Ctags

Download the current x64 ZIP from the
[Universal Ctags Windows releases](https://github.com/universal-ctags/ctags-win32/releases),
extract it under `$toolRoot\ctags`, then add the directory containing
`ctags.exe`:

```powershell
$ctagsRoot = Join-Path $toolRoot 'ctags'
Expand-Archive -Path (Join-Path $env:USERPROFILE 'Downloads\<downloaded-ctags-x64.zip') -DestinationPath $ctagsRoot -Force
$ctagsBin = (Get-ChildItem -LiteralPath $ctagsRoot -Filter ctags.exe -Recurse | Select-Object -First 1).Directory.FullName
Add-UserToolPath $ctagsBin
ctags --version
```

### Semgrep

The [Semgrep Windows instructions](https://semgrep.dev/products/community-edition/)
use Python and pip. `--user` keeps the package under the current profile.
Add Python's user Scripts directory to PATH:

```powershell
py -m pip install --user semgrep
$pythonUserScripts = (py -c "import sysconfig; print(sysconfig.get_path('scripts', 'user'))").Trim()
Add-UserToolPath $pythonUserScripts
semgrep --version
```

### C++ syntax-index tools: identity, budget and exact results

`cpp_overview`, `cpp_calls_into`, `cpp_calls_from` and `cpp_blast_radius` answer
from an in-process C++-only syntax index (no Python, GDScript or other
collectors, no `.idiom/comprehension` graph, no standing indexed worktree). It
is built from the live working tree on each `idiom tool` call: about 0.3-0.5 s
for the 237 C++ files of Undermarch's `native/` tree, so a fresh `git worktree
add` answers immediately with nothing to prepare. To build and inspect it for a
checkout (the W145 "build the indexes" step), run from that checkout:

```bash
idiom tool cpp_overview --input '{}'
```

Every result carries `sidecar.freshness` (`idiom.cpp_index.v1`):

| Field | Meaning |
|---|---|
| `index_key` | Hash of collector version, Git common-dir identity, `HEAD^{tree}` and the content of dirty C++ files. Equal keys mean equal index content, across worktrees of one repository. |
| `commit`, `tree`, `git_common_dir`, `root` | What was indexed and where. |
| `working_tree` / `dirty_cpp_paths` / `dirty_cpp_count` | Dirty and untracked C++ files are included (the index reads the working tree), and listed. |
| `phase_ms`, `query_ms`, `budget_ms`, `files`, `symbols`, `index_build` | Cost: Git identity, directory walk, parse, link, the query itself. |
| `fresh` | `false` when the index budget (10 s) was exceeded or Git identity could not be read. |

No on-disk index cache is kept: measured on Undermarch, parsing is about 0.1 s
of a 0.3-0.4 s build and the directory walk about 0.1 s, so a cache cannot beat
a rebuild. The `index_key` is the cache identity if one is ever added.

Failures are structured in `sidecar.error.code`: `cpp_index_budget_exceeded`
(with the phase that was running), `ambiguous_symbol` (the name belongs to more
than one owner; candidates are in `Output`; pass `Owner::name` or a `path`) and
`symbol_not_found` (nothing is named exactly that; nearest names are in
`Output`). Summaries echo the requested name and the resolved owner. Call
queries match callee names only (the syntax index does not resolve receiver
types) and say so in `sidecar.warnings`; the optional clangd call hierarchy is
attempted within a 5 s budget. `cpp_blast_radius` takes a file name or a symbol:
a symbol resolves to its defining files and reports every translation unit that
includes them.

### clangd and C++ compile database

Download the Windows x64 standalone ZIP from the
[clangd releases](https://github.com/clangd/clangd/releases), extract the
complete archive under `$toolRoot\clangd`, then add the directory containing
`clangd.exe` to PATH:

```powershell
$clangdRoot = Join-Path $toolRoot 'clangd'
Expand-Archive -Path (Join-Path $env:USERPROFILE 'Downloads\<downloaded-clangd-windows-x64.zip') -DestinationPath $clangdRoot -Force
$clangdBin = (Get-ChildItem -LiteralPath $clangdRoot -Filter clangd.exe -Recurse | Select-Object -First 1).Directory.FullName
Add-UserToolPath $clangdBin
clangd --version
```

clangd also needs the real compile flags for the files it analyzes. It reads a
`compile_commands.json` in the source directory or an ancestor; the Idiom C++
diagnostics tool searches the workspace root, `build`, `out`, `native`, and
`native/build`.

For the Undermarch `native/` extension, put the database at
`native/compile_commands.json`. It must contain one compile entry per native
extension `.cpp` translation unit, with the actual SCons working directory,
MSVC compiler command (`cl.exe`), `/std:c++17` and the project's strict flags,
and the include paths and defines supplied by the pinned `.deps/godot-cpp`
SCons environment plus `native/include` and `native/src`. The project builds
through `native/SConstruct` and the pinned Godot C++ SCons file with
`api_version=4.6`; it does not use CMake, so a CMake-generated database will
not describe this target. The Windows SCons configuration uses `cl`, `link`,
and `lib`. A database without the generated Godot binding include paths will
leave clangd unable to resolve Godot types even if the native source paths are
present. The checked-in `native/SConstruct` does not enable SCons's compilation
database builder, and there is no database in the evaluated worktree; generating
one requires a local SCons hook or actual-build capture that records the real
commands. SCons documents its [compilation database builder](https://scons.org/doc/latest/HTML/scons-user/ch27.html).
See the [clangd compile-command guide](https://clangd.llvm.org/design/compile-commands)
for the database fields and search behavior.

For optional dependency-graph precision, the same npm prefix can install
madge, and the Python user install can install pydeps:

```powershell
npm install --global --prefix $npmPrefix madge
py -m pip install --user pydeps
```

No optional backend was installed as part of this CLI fix.

## Coding Rules

```bash
idiom coding-rules check --project-root . --json
idiom coding-rules check --project-root . --fail-on blocking
```

Project-local rules live at `.idiom/coding_rules.json`. Reports are typed JSON
with paths, symbols, hashes, severities, and evidence facts; they do not include
raw source bodies. Advisory findings inform the model, while blocking findings
can fail `coding-rules check` or lane postflight.

## MCP

```bash
idiom mcp serve --stdio --profile discovery
```

## Queue

```bash
idiom queue serve
idiom queue status
idiom queue run --pool default -- idiom lane plan-execute path/to/brief.md
```

The queue is useful when several local jobs share limited backend capacity.
