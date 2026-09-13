# Development

## Build

Install the Go toolchain declared in `go.mod`. A local development build does
not require model weights or credentials:

```bash
go build ./cmd/idiom
```

Use `make build` when you want to update the installed development binary and
local entrypoints as well. See [getting started](getting-started.md) for the
full dependency installer.

## Test

```bash
go test ./...
```

For localized changes, run the focused package tests first, then the full suite
when the change touches shared runtime, prompts, workspace tools, installers, or
release behavior.

## Coding Rules

Projects can codify local idioms in `.idiom/coding_rules.json`. This repository
tracks that file and treats Go pass-through wrappers, including wrappers in test
files, as a blocking error. The rule checks one-statement functions that only
forward the same arguments to another call without owning a real interface or
boundary-adapter contract.

```bash
idiom coding-rules check --project-root . --fail-on blocking
idiom tool coding_rules_check --input '{"path":"."}'
```

Use `coding_rules.example.json` as an advisory starting point for other
repositories. Do not suppress wrapper-only migration steps, compatibility
aliases, test-only facades, or functions kept only so callers can be moved later.
Move callers to the owning API and delete the old surface. Inline suppressions
are for imposed boundary/interface adapters only and must carry that rationale:

```go
// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by external Host interface
```

## Installer Checks

```bash
make test-system-hermetic
```

Run host-specific installer smoke tests only where the target dependency set is
available. Use the corresponding `make test-install-*` target so Ginkgo remains
the assertion entrypoint and reports fixture stdout and stderr on failure.

## Local Hygiene

Ignored local state can be large and sensitive. Before preparing a release,
inspect the tracked source and release archives for generated logs, transcripts,
indexes, model files, and private notes. Build release artifacts from a clean
checkout; do not delete a developer's ignored local data as a release step.
Keep only small sanitized fixtures that tests require.
