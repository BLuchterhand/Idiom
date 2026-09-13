# Getting Started

Idiom is a local coding-agent harness for repository work. It can run local
model servers or hosted OpenAI-compatible backends, expose repository discovery
tools, and record audit evidence for each run.

## Install

Before the first tagged release exists, use the source installer:

```bash
git clone https://github.com/BLuchterhand/idiom.git
cd idiom
./scripts/install-requirements.sh --all
```

On Windows:

```powershell
.\scripts\install-requirements.ps1 -All
```

After release assets are available, use the installers described in
[README.md](../README.md).

## Configure a Backend

The checked-in `.idiom/backends.json` is a legacy example for explicit backend
commands. Active model selection comes from the `llama_server` and `backends`
blocks in `config.json`. Do not commit real API keys or private model paths.

Hosted example:

```bash
export DEEPSEEK_API_KEY=...
```

In the active `config.json`, set `llama_server.kind` to `deepseek`,
`llama_server.model` to a model available to your account, and
`llama_server.api_key_env` to `DEEPSEEK_API_KEY`. Set
`chat.auto_start_server` to `false`. Updating only the legacy backend example
does not change an ordinary run's selected model.

The source checkout includes a placeholder local-model configuration. Set up
your private configuration before the first model run. For a separate target
repository, run `idiom init` there to create project configuration, or use the
installer-created global configuration when no project file exists. See
[configuration](configuration.md) for selection rules and path handling.

Local examples need a compatible model server and local model files outside the
repository.

## First Commands

```bash
idiom doctor
idiom run --prompt "summarize this repository" --json
idiom tool context_status --input '{"refresh":true,"limit":20}'
```

For implementation work, write a short brief and run:

```bash
idiom lane plan-execute --title my-task path/to/brief.md
```
