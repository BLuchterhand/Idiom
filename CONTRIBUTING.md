# Contributing

## Development Setup

```bash
./scripts/install-requirements.sh --all
make build
go test ./...
```

On Windows, use:

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

## Architecture Expectations

The harness enforces contracts; the model chooses strategy. Runtime gates,
prompts, tests, telemetry, and audit output should use the same contract names
and failure semantics.
