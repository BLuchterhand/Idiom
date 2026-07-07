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

## Coding Rules

Projects can codify local idioms in `.idiom/coding_rules.json`. The first
runtime rule checks Go pass-through wrappers: one-statement functions that only
forward the same arguments to another call without validation, translation,
error context, observability, or a permission check.

```bash
cp coding_rules.example.json .idiom/coding_rules.json
idiom coding-rules check --project-root . --fail-on blocking
idiom tool coding_rules_check --input '{"path":"."}'
```

Use advisory enforcement while tuning a repository. Switch a rule to `blocking`
only when the team agrees the pattern is a real invariant. Do not suppress
wrapper-only migration steps, compatibility aliases, or functions kept only so
callers can be moved later. Move callers to the owning API and delete the old
surface. Inline suppressions are for imposed boundary/interface adapters only
and must carry that rationale:

```go
// idiom:allow go.no_pass_through_wrapper -- boundary adapter required by external Host interface
```

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
