# Python discovery tooling

idiom exposes a Python-specific toolset to the LLM alongside the existing
Go and C/C++ surfaces. The shape mirrors `cpp_*` — deterministic
capabilities for file outline, symbol lookup, references, call hierarchy,
entrypoints, and diagnostics.

All thirteen canonical capabilities from `internal/repository/semantic/language/registry.go`
are exposed under the `python_` prefix:

The implementation lives in `internal/repository/semantic/languagetools/python_tools.go`
and `internal/repository/semantic/languagetools/pylsp_lsp.go`; `internal/capabilities/workspace`
only adapts workspace paths, dispatch, and tool result contracts.

| Capability         | Tool                      | Backing                             |
| ------------------ | ------------------------- | ----------------------------------- |
| file_outline       | `python_file_outline`     | tree-sitter-style index (pure Go)   |
| find_symbol        | `python_find_symbol`      | index                               |
| workspace_symbol   | `python_workspace_symbol` | LSP first, then index               |
| definition         | `python_definition`       | LSP first, then index               |
| references         | `python_references`       | LSP first, then rg fallback         |
| calls_from         | `python_calls_from`       | LSP call hierarchy, then index      |
| calls_into         | `python_calls_into`       | LSP call hierarchy, then index      |
| read_symbol        | `python_read_symbol`      | index + `read_file`                 |
| related_files      | `python_related_files`    | index                               |
| implementations    | `python_implementations`  | LSP first, then subclass heuristic  |
| diagnostics        | `python_diagnostics`      | LSP (pyright or pylsp)              |
| overview           | `python_overview`         | index                               |
| entrypoints        | `python_entrypoints`      | index                               |

## Depth: LSP vs tree-sitter

- **Without a Python LSP installed**, the semantic-index tools
  (`python_file_outline`, `python_find_symbol`, `python_workspace_symbol`,
  `python_overview`, `python_entrypoints`, `python_read_symbol`,
  `python_related_files`, `python_calls_from`, `python_calls_into`,
  `python_implementations`) all still work by reading files off disk and
  running a line-oriented parser. Tree-sitter-level accuracy is not
  required for symbol discovery; the parser handles classes, async/sync
  functions, methods, decorators, indentation-based block ends, and
  import resolution (relative and absolute, including `src/`-rooted
  packages).
- **With pyright or pylsp installed**, `python_definition`,
  `python_references`, `python_implementations`, `python_calls_from`,
  `python_calls_into`, `python_workspace_symbol`, and
  `python_diagnostics` upgrade transparently: the tool tries the LSP
  first and falls back to the index only if the server fails or is
  absent.
- **`python_diagnostics` requires an LSP.** Without one, it returns a
  clear `install …` error rather than guessing.

## Installing a Python LSP (not bundled)

idiom does not ship a Python language server. Install one of these:

### Option 1 — pyright (recommended, faster)

```
# Via npm (preferred):
npm install -g pyright

# Or via pip:
pip install pyright
```

This provides `pyright-langserver` on your `$PATH`. idiom discovers it
via `exec.LookPath("pyright-langserver")`.

### Option 2 — pylsp (pure-Python fallback)

```
pip install python-lsp-server[all]
```

This installs the `pylsp` binary. idiom uses it automatically when
`pyright-langserver` is not on `$PATH`.

## Detection

A directory is considered a Python workspace if any of these markers
exist at the root:

- `pyproject.toml`
- `setup.py`
- `setup.cfg`
- `requirements.txt`
- `Pipfile`
- `poetry.lock`

If none of those exist but the tree contains any `.py` or `.pyi` file
(outside the usual `.venv`, `venv`, `__pycache__`, `.mypy_cache`,
`.pytest_cache`, `.tox`, `site-packages`, `.eggs`, `node_modules`,
`build` directories), the workspace still counts as Python.

Supported extensions: `.py`, `.pyi`.

## Decision guide (when to use which tool)

```
Orient a .py file:             python_file_outline (path)
Find a symbol anywhere:        python_workspace_symbol (pattern)
Jump to a definition:          python_definition (pattern, path scope)
Who calls this / uses this:    python_references, python_calls_into
What does this call:           python_calls_from
Read one symbol body:          python_read_symbol (pattern, path)
Files related to a concept:    python_related_files (pattern, path)
Subclasses of a base class:    python_implementations (pattern)
Type / lint errors:            python_diagnostics (path) — needs LSP
Project overview:              python_overview
Likely entrypoints:            python_entrypoints
```

Fallback to `read_file` with explicit `start_line`/`end_line` only when a
semantic tool returns nothing.
