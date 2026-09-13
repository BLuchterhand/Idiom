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
