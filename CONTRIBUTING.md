# Contributing

## Development Setup

Read [AGENTS.md](AGENTS.md) for repository policy and
[the architecture reference](docs/architecture.md) for ownership boundaries.
Use the Go toolchain declared in `go.mod`. For a CLI build and the default
test suite:

```bash
go build ./cmd/idiom
go test ./...
```

For optional discovery tools and the installed development harness, use the
[source installer](README.md#source-install). On Windows:

```powershell
.\scripts\install-requirements.ps1 -All
```

## Pull Request Expectations

- Keep changes focused and explain the behavioral or architectural reason.
- Add or update tests for user-visible behavior, contracts, and regression
  fixes.
- Keep generated logs, transcripts, indexes, model files, private notes, and
  local artifacts out of commits.
- Do not commit real secrets. Use environment-variable names and sanitized
  fixtures.
- Run `go test ./...` before requesting review.
- Run focused package tests and build the CLI for shared runtime, workspace,
  prompt, routing, installer, or release changes. Installer and live-model
  checks have separate prerequisites documented in [development](docs/development.md)
  and [release](docs/release.md).

## Architecture Expectations

The harness enforces contracts; the model chooses strategy. Runtime gates,
prompts, tests, telemetry, and audit output should use the same contract names
and failure semantics.

## License

Submit only contributions you have the right to distribute under Idiom's
[GPL-3.0 terms](LICENSE). Preserve third-party copyright and license notices
when contributing code or dependencies.
