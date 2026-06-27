# Getting Started

Idiom is a local coding-agent harness for repository work. It can run local
model servers or hosted OpenAI-compatible backends, expose repository discovery
tools, and record audit evidence for each run.

## Install

Before the first tagged release exists, use the source installer:

```bash
./scripts/install-requirements.sh --all
make build
```

On Windows:

```powershell
.\scripts\install-requirements.ps1 -All
```

After release assets are available, use the installers described in
[README.md](../README.md).

## Configure a Backend

The checked-in `.idiom/backends.json` uses placeholder configuration and
environment-variable names. Do not commit real API keys or private model paths.

Hosted example:

```bash
export DEEPSEEK_API_KEY=...
idiom backend status --profile deepseek-v4-flash
```

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
