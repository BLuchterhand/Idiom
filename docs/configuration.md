# Configuration

Idiom reads project-local configuration from `.idiom/` and user-global
configuration from `~/.idiom/`. Installers create missing global files without
overwriting existing user files.

## Files

| File | Purpose |
| --- | --- |
| `.idiom/config.json` | Project-local harness defaults. |
| `.idiom/backends.json` | Backend profiles and model routing defaults. |
| `~/.idiom/config.json` | User-global defaults created by install/init flows. |
| `~/.idiom/backends.json` | User-global backend profiles. |

## Secrets

Keep secrets in environment variables or a private secret manager. Do not store
API keys, private endpoints, token files, or machine-local model paths in
tracked config.

Backend profiles should reference environment variable names, for example:

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
