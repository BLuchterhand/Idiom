# Idiom

Idiom is a local coding-agent harness for running local or hosted models against
a repository with controlled workspace tools, repo memory, lane contracts,
audit evidence, and release/eval checks.

The central design rule is: the model owns strategy; the harness owns
contracts, evidence, permissions, and verification.

## Current Status

Idiom is currently strongest as a headless CLI harness. The terminal UI exists
and is useful for interactive work, but release-critical workflows should be
driven through the CLI until the TUI reaches the same coverage level.

The project supports:

- local models through `llama.cpp` and MLX-compatible servers
- hosted OpenAI-compatible model providers
- plan/execute and implementation lanes
- repository context packets and language-aware discovery tools
- external tool sidecar and MCP stdio discovery surfaces
- local telemetry, transcript logs, harness audit, and eval tooling
- source and release installer paths for supported platforms

Model weights and hosted API keys are not bundled. A working install still needs
either local model files or the credentials for a hosted backend.

## Supported Platforms

| Platform | Release CLI | Full install | Dependency manager | CI gate |
| --- | --- | --- | --- | --- |
| macOS `amd64` | yes | yes | Homebrew | configured |
| macOS `arm64` | yes | yes | Homebrew | configured |
| Ubuntu Linux `amd64` | yes | yes | `apt` plus language installers | configured |
| Ubuntu Linux `arm64` | yes | yes | `apt` plus language installers | configured |
| Windows `amd64` | yes | yes | PowerShell, Chocolatey, npm, pip, Go, Rust, upstream archives | configured |
| Windows `arm64` | no | no | not shipped | not covered |
| Other Linux distributions | build from source | manual dependencies | distro-specific | not covered |

The full installer path is intended to bootstrap the CLI, tool launchers, and
declared discovery dependencies. The cross-platform CI jobs are configured to
install from release snapshot artifacts and then smoke every discovery tool
declared in `scripts/install/manifest.json`.

All installer profiles also initialize missing user-global config artifacts
under `~/.idiom`. Existing files are preserved.

Do not treat unlisted platforms as release-supported until they have an
installer path and an executable CI gate.

## Install

### Release Install

Use the release installers after a tagged GitHub Release has uploaded assets.
Before the first release tag exists, use the source install path below.

macOS or Linux CLI only:

```bash
curl -fsSL https://raw.githubusercontent.com/BLuchterhand/idiom/main/install.sh | sh
```

macOS or Linux full harness:

```bash
curl -fsSL https://raw.githubusercontent.com/BLuchterhand/idiom/main/install.sh | sh -s -- --profile full
```

Windows CLI only:

```powershell
irm https://raw.githubusercontent.com/BLuchterhand/idiom/main/install.ps1 | iex
```

Windows full harness:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/BLuchterhand/idiom/main/install.ps1))) -Profile Full
```

The default installers download the matching release archive, verify
`SHA256SUMS`, install the `idiom` binary, initialize missing global config
under `~/.idiom`, and verify that the binary runs.

The `full` profile also installs tool launchers and the discovery dependency
set. Full install does not install model weights and does not create hosted API
keys.

Global initialization creates missing files such as `~/.idiom/config.json`,
`~/.idiom/backends.json`, prompt templates, external-tool placeholders, and
postflight-check placeholders. It is merge-only: if a file already exists, the
installer leaves it untouched.

### Installer Profiles

Unix release installer profiles:

| Profile | Purpose |
| --- | --- |
| `cli` | Install the `idiom` CLI only. This is the default. |
| `tools` | Install `idiom-tool` and `idiom-mcp-discovery` launchers only. |
| `full` | Install the CLI, tool launchers, and full discovery dependency set. |
| `discovery-deps` | Install the CLI and default discovery dependencies. |
| `doctor-only` | Install the CLI, verify it, and run doctor diagnostics. |

Windows release installer profiles:

| Profile | Purpose |
| --- | --- |
| `Cli` | Install `idiom.exe` only. This is the default. |
| `Tools` | Install `idiom-tool.cmd` and `idiom-mcp-discovery.cmd` only. |
| `Full` | Install the CLI, tool launchers, and full discovery dependency set. |
| `DiscoveryDeps` | Install the CLI and default discovery dependencies. |
| `DoctorOnly` | Install the CLI, verify it, and run doctor diagnostics. |

Shared release installer environment variables:

| Variable | Purpose |
| --- | --- |
| `IDIOM_VERSION` | Pin a release tag, for example `v0.1.0`. |
| `IDIOM_INSTALL_DIR` | Override the binary destination. |
| `IDIOM_REPO` | Install from a fork instead of `BLuchterhand/idiom`. |
| `IDIOM_RELEASE_URL_BASE` | Use a custom asset base URL. |

### Source Install

Use source install when developing Idiom or before release assets exist.

macOS with Homebrew or Ubuntu Linux with `apt`:

```bash
./scripts/install-requirements.sh --all
```

Windows:

```powershell
./scripts/install-requirements.ps1 -All
```

Useful source installer flags:

| Flag | Purpose |
| --- | --- |
| `--no-build` / `-NoBuild` | Install or check dependencies without building Idiom. |
| `--no-audit` / `-NoAudit` | Skip the final harness audit check. |
| `--no-queue` / `-NoQueue` | Skip queue daemon setup where applicable. |

The source installer builds the local binary with `make build`. The build target
keeps the canonical source-built binary at `~/.idiom/bin/idiom` and refreshes
the repo-local and `~/.local/bin` entrypoints. Source install also initializes
missing global config artifacts under `~/.idiom` without overwriting existing
files.

## Configure A Model

Idiom can use either hosted backends or local model servers.

For the checked-in hosted example, set the provider API key:

```bash
export DEEPSEEK_API_KEY=...
```

The tracked `.idiom/backends.json` uses `deepseek-v4-flash` as the default
hosted profile and includes a local `llama.cpp` example that points at the
ignored placeholder path `models/local-model.gguf`.

The installer creates user-global config. For a repository that needs its own
project-specific config, initialize project-local Idiom config:

```bash
idiom init
```

Then edit `.idiom/backends.json` or `.idiom/config.json` for your backend:

- hosted backends need a model name and an API-key environment variable
- local `llama.cpp` backends need `llama-server` and a local GGUF model path
- MLX backends need `mlx_lm.server` and an MLX model name or local model path

Keep real model files, API keys, and machine-local absolute paths out of
version control.

## First Run

Check host readiness:

```bash
idiom doctor
```

Run a headless prompt:

```bash
idiom run --prompt "summarize this repository" --json
```

Create a small implementation brief and run a lane:

```bash
idiom lane plan-execute --title my-task path/to/brief.md
```

Use a workspace discovery tool directly:

```bash
idiom tool context_status --input '{"refresh":true,"limit":20}'
```

List the discovery sidecar tools:

```bash
idiom tool list --profile discovery
```

Serve the MCP discovery surface:

```bash
idiom mcp serve --stdio --profile discovery
```

## Core Workflows

### Headless Agent Runs

`idiom run` executes a prompt against the configured backend and can emit JSONL
events for scripts, diagnostics, or replay.

```bash
idiom run --prompt "inspect internal/runtime/agent for recent failure modes" --json
```

### Plan/Execute Lanes

`idiom lane plan-execute` is the main implementation workflow. The planner
creates a bounded edit plan; the executor applies accepted edits through the
harness workspace contract.

```bash
idiom lane plan-execute --title fix-parser path/to/brief.md
```

See [docs/cli.md](docs/cli.md) and [docs/architecture.md](docs/architecture.md).

### Repository Memory And Discovery

Idiom builds context packets and graph-backed views so agents can localize work
without repeatedly scanning the whole repository.

Common direct tools:

| Tool | Use |
| --- | --- |
| `context_pack` | Rank files and symbols for a task. |
| `context_status` | Inspect index freshness and collector coverage. |
| `context_query` | Ask graph-shaped questions. |
| `dependency_graph` | Inspect package and module relationships. |
| `history_range`, `history_symbol`, `blame_range` | Inspect git history. |
| `semgrep_scan`, `joern_scan` | Run optional security/dataflow discovery. |

`read_only_capability` exposes only stable contracts with exact adapters.
`harness_audit` reports the remaining read-only tools as documented direct-only
surfaces when their narrower, broader, or owner-specific contract is not an
exact capability match; those tools are not unresolved routing gaps. Optional
precision backends such as `typescript-language-server` and `madge` remain
visible as `optional_unavailable` when absent, but their built-in fallbacks mean
that expected absence is not an action-required finding.

The full installer path installs the declared discovery backends and CI smokes
the installed CLI against the manifest. See [docs/release.md](docs/release.md).

### Tool Sidecar

External callers can use Idiom's repository evidence tools without running a
model session:

```bash
idiom tool <tool-name> --profile discovery --input '<json>'
```

The sidecar launchers installed by the `tools` or `full` profile are:

- `idiom-tool`
- `idiom-mcp-discovery`

See [docs/cli.md](docs/cli.md).

### Audit And Eval

Idiom writes local telemetry and conversation logs, then exposes audit and eval
commands for behavior review.

Ranker learning is opt-in workspace state. `harness_audit` reports
`idle_expected` when both ranker artifacts are absent, `initialized_idle` when
the artifacts are present but valid and empty, `active` when valid weights or
learning events exist, and `degraded` with `action_required` when either
artifact is malformed, unreadable, oversized, or has an unsafe filesystem
shape, and also when more than three quarters of valid mutable learning events
are no-ops. Integrity-degraded evidence does not drive learning-quality or
trigger-wiring advice. Eval, ranker CLI, and recovery reconciliation are the current state
producers; verified-edit, `harness_feedback`, and audit-watch remain documented
candidates until they are wired.

```bash
idiom tool harness_audit --input '{"purpose":"tool_surface"}'
idiom audit watch --once --review-existing
make swe-live-subset
```

See [docs/development.md](docs/development.md) and
[docs/release.md](docs/release.md).

### Local Queue

`idiom queue` gates concurrent local model work against small per-pool caps.
This is useful when multiple lanes or projects share the same local backend.

```bash
idiom queue serve
idiom queue status
idiom queue run --pool default -- idiom lane plan-execute path/to/brief.md
```

On macOS source installs, the queue daemon can be installed as a per-user
launchd agent.

## Architecture

Idiom separates model strategy from harness authority.

- The model chooses how to reason, localize, and propose work.
- The harness enforces tool schemas, permissions, protected paths, evidence
  freshness, lane contracts, verification, and final-answer acceptance.
- Repository memory and discovery tools provide evidence without dictating the
  model's next step.
- Audit and eval surfaces make behavior inspectable after the run.

The maintained architecture reference is [docs/architecture.md](docs/architecture.md).
Start with [docs/README.md](docs/README.md) for the full public doc index.

## Repository Layout

| Path | Purpose |
| --- | --- |
| `cmd/idiom` | CLI entrypoint. |
| `internal/runtime` | Agent runtime, backend routing, readiness, runner logic. |
| `internal/capabilities/workspace` | Workspace tools and tool policy. |
| `internal/repository` | Discovery, semantic indexing, repo memory. |
| `internal/surfaces/cli` | CLI subcommands. |
| `internal/surfaces/tui` | Terminal UI. |
| `prompts/` | Baseline prompts and profiles. |
| `.idiom/` | Project-local Idiom config for this repository. |
| `docs/` | Public architecture, CLI, development, release, and security notes. |
| `scripts/install/manifest.json` | Installer dependency manifest. |

Generated logs, transcripts, local model files, and local `.idiom` state should
not be committed unless a specific release artifact calls for them.

## Development

Build:

```bash
make build
```

Run tests:

```bash
go test ./...
```

Common development commands:

```bash
make run
make test
make coverage
make live-contracts
make swe-live-subset
```

Run install coverage checks that do not require host dependency installation:

```bash
make test-system-hermetic
```

The build-tagged system suite owns assertions for the external shell, Python,
and PowerShell fixtures. Direct targets select one fixture and fail when its
required platform or toolchain is unavailable.

Run the post-install harness smoke after an installer has initialized
`~/.idiom`:

```bash
IDIOM_BIN=/path/to/idiom make test-post-install-harness
```

Run the macOS release-installer smoke on a macOS host:

```bash
make test-install-macos
```

Run the installed-tool smoke only on a host where the full dependency set is
installed:

```bash
IDIOM_BIN=/path/to/idiom IDIOM_TOOL_BIN=/path/to/idiom-tool make test-install-discovery-tools
```

## Release Gates

Release prep should include:

- `go test ./...`
- GoReleaser snapshot build
- release archive install smoke for supported OS/architecture pairs
- post-install harness smoke from installer-created global config
- full installer dependency smoke on supported platforms
- installed-CLI discovery smoke for every manifest-declared tool
- release evidence bundle
- README, release notes, and installer documentation review

The maintained release checklist is [docs/release.md](docs/release.md).

## Security And Privacy

Idiom is local-first, but it can run shell commands, read and write workspace
files, call hosted model APIs, and invoke external discovery tools. Treat it as
an automation harness with the same filesystem and network permissions as the
process that launched it.

Operational defaults:

- debug logs are redacted by default
- model files and caches should stay outside the target repository
- committed config should not contain real API keys or private model paths
- workspace writes and shell access can be disabled in config
- local audit exports should avoid prompt bodies, file bodies, secrets, and
  customer-identifying content unless explicitly needed for private debugging

See [docs/security-privacy-posture.md](docs/security-privacy-posture.md).

## License

No open-source license has been selected yet. Until a license is added, the
published source is available for inspection only and is not licensed for reuse,
redistribution, or modification.
