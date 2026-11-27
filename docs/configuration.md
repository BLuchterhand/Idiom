# Configuration

Idiom reads one active `config.json` from the project or, when the project file
is absent, from `~/.idiom/`. Installers create missing global files without
overwriting existing user files. Backend profiles belong in that same config
under `backends`; the loaded config is the authority used by the TUI, headless
runtime, and readiness checks.

Project and global files are alternatives, not merged layers. If neither exists,
the loader uses compiled defaults. To select a separate private configuration,
pass `--config /path/to/config.json` to the command. A missing explicit file can
still fall back to the global file; check `idiom doctor` output when diagnosing
which backend is active.

## Files

| File | Purpose |
| --- | --- |
| `.idiom/config.json` | Project-local harness defaults and the consolidated `backends` block. |
| `~/.idiom/config.json` | User-global defaults and the consolidated `backends` block. |
| `.idiom/backends.json` | Legacy compatibility input for explicit `backend`/`profile` commands. It is not an implicit TUI override. |

Example consolidated local backend configuration:

```json
{
  "llama_server": {
    "kind": "mlx",
    "binary": "/path/to/mlx_lm.server",
    "model": "/path/to/local-model"
  },
  "backends": {
    "default_profile": "local-coder",
    "profiles": {
      "local-coder": {
        "models": [{
          "alias": "local-coder",
          "kind": "mlx",
          "binary": "/path/to/mlx_lm.server",
          "model_path": "/path/to/local-model",
          "roles": ["planner", "executor", "classifier"]
        }]
      }
    }
  }
}
```

If `backends` is present, an older `.idiom/backends.json` does not override it.
Replace example paths before running. Paths in configuration are resolved by
the configuration loader; set `workspace.root` and `chat.project_root` to the
intended repository when using a configuration stored elsewhere.

## Workspace Shell

Leave `workspace.shell` empty or omit it to use `/bin/sh` on macOS and Linux,
or the built-in `powershell.exe` on Windows. Set an executable name or path to
choose another shell, such as `/bin/zsh`, `pwsh`, or `cmd.exe`. Existing explicit
settings are preserved; an unavailable configured shell produces an error.

Commands use the selected shell's syntax. Idiom does not translate Unix shell
commands into PowerShell commands. Plain shell execution skips PowerShell
profiles and cmd AutoRun; login shell execution permits them. POSIX shells
receive `-c` for plain execution and `-lc` for login execution.

## Secrets

Keep secrets in environment variables or a private secret manager. Do not store
API keys, private endpoints, token files, or machine-local model paths in
tracked config.

Hosted backend entries should reference environment variable names, for example:

```json
{
  "api_key_env": "DEEPSEEK_API_KEY"
}
```

Hosted backend kinds include `deepseek`, `zai`, `glm`, `minimax`,
`openai_responses`, `codex`, and `anthropic`. Anthropic Claude uses the native
Messages API, not the OpenAI-compatible shim:

```json
{
  "kind": "anthropic",
  "model": "claude-sonnet-4-6",
  "api_key_env": "ANTHROPIC_API_KEY"
}
```

## Local State

Runtime logs, telemetry, transcripts, indexes, audits, model files, and
generated artifacts are local state. They should remain ignored unless a
specific test fixture intentionally requires a small sanitized sample.

### C++ Syntax Cache

C++ discovery tools share a per-user syntax cache at `~/.idiom/cache/cpp`.
`IDIOM_CACHE_DIR` overrides the cache base directory (entries go in its `cpp`
subdirectory). Entries contain relative paths, includes, symbols, calls, content
hashes, and build metadata; they do not contain source file bodies. Treat symbol
names and workspace paths as private repository data. Directories use mode
`0700` and files `0600` where supported; Windows uses the user's filesystem ACLs.

The identity includes the collector version, Git common directory, tree, and
dirty/untracked/ignored C++ content that the collector visits. Worktrees of the
same repository and content share entries. Repositories without an available Git
identity still build, but cannot use the persistent cache. Skipped discovery
directories such as `.claude`, `build`, and `vendor` do not affect the identity.

Changed snapshots reuse the nearest cached index, using `git diff` between trees
and content hashes for dirty and ignored files. Only changed files are parsed;
includes are relinked against the current path set so header additions, removals,
and moves are reflected. A build verifies its snapshot again before publication.
Corrupt, incompatible, and oversized entries are ignored and rebuilt. Publication
uses a synced temporary file and atomic rename. Concurrent builders may duplicate
work; an OS lock serializes publication and eviction, and a partial build cannot
replace a completed entry. Cache entries are limited to 64 MiB each, 64 entries,
and 256 MiB total, evicting the least recently used entries first.
`IDIOM_CPP_CACHE_MAX_BYTES` overrides the total byte bound.

`IDIOM_CPP_INDEX_BUDGET` accepts a positive Go duration such as `15s` or `2m`.
The default is **20 seconds**: a measured BenTech build exceeded the old 10-second
limit, while the tool envelope is 30 seconds. The default leaves time for atomic
checkpoint IO and optional clangd queries. A larger configured budget is capped
by the caller's deadline with four seconds reserved for checkpointing. Invalid
values use the default. Optional clangd call/reference attempts have a 500 ms
budget before falling back to syntax or text evidence.
`IDIOM_CPP_CLANGD_BUDGET` overrides that attempt budget when the operator prefers
to wait longer for precision; the enclosing tool deadline still applies.

Budget-limited builds return `cpp_index_budget_exceeded` with phase timings and
`checkpoint_saved`. Completed file parses and a completed discovery manifest are
persisted so the next process resumes instead of repeating them. Partial indexes
never answer queries. A very slow cold build can require several calls; process
exit does not leave a background builder running. Freshness reports distinguish
`disk_hit`, `incremental`, `resumed`, and in-process `memoized` results, and expose
cache errors, parsed/reused file counts, and cache load time. Read/search permission
contracts remain enforced by the workspace tool boundary.
