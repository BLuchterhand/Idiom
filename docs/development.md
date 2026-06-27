# Development

## Build

```bash
make build
go build ./cmd/idiom
```

## Test

```bash
go test ./...
```

For localized changes, run the focused package tests first, then the full suite
when the change touches shared runtime, prompts, workspace tools, installers, or
release behavior.

## Installer Checks

```bash
./scripts/test-install-dependencies.sh --manifest-only
bash -n scripts/test-install-macos.sh
python3 -m py_compile scripts/test-post-install-harness.py
python3 -m py_compile scripts/test-discovery-tools-e2e.py
```

Run host-specific installer smoke tests only where the target dependency set is
available.

## Local Hygiene

Ignored local state can be large and sensitive. Before preparing a release,
remove generated logs, transcripts, indexes, build outputs, local model files,
private notes, and one-off working directories. Keep only small sanitized
fixtures that tests require.
