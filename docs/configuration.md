# Configuration

Idiom reads one active `config.json` from the project or, when the project file
is absent, from `~/.idiom/`. Installers create missing global files without
overwriting existing user files. Backend profiles belong in that same config
under `backends`; the loaded config is the authority used by the TUI, headless
runtime, and readiness checks.

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
    "binary": "/path/to/mlx_vlm.server",
    "model": "/path/to/Qwen3.8-27B-4bit"
  },
  "backends": {
    "default_profile": "qwen38",
    "profiles": {
      "qwen38": {
        "models": [{
          "alias": "qwen38",
          "kind": "mlx",
          "binary": "/path/to/mlx_vlm.server",
          "model_path": "/path/to/Qwen3.8-27B-4bit",
          "roles": ["planner", "executor", "classifier"]
        }]
      }
    }
  }
}
```

If `backends` is present, an older `.idiom/backends.json` does not override it.

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
