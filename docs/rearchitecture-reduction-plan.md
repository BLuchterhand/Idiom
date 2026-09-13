# Rearchitecture Closeout

Status: historical record. The maintainability review closed in July 2026.
[Architecture](architecture.md) owns the current component and lifecycle model.

The review replaced duplicated runtime and workflow authority with explicit
owners. Its main decisions were:

- Keep invocation state in `runExecution`, with explicit shared dependencies
  and snapshots of mutable inputs.
- Run TUI work through a detached execution session and owned events.
- Keep permission grants, cancellation, and execution identity invocation-scoped.
- Project tool and source evidence from typed producer facts rather than
  parsing presentation text or maintaining parallel path registries.
- Keep workflow durability, leases, transaction records, and recovery under one
  persistence authority.
- Remove forwarding wrappers and aliases when moving callers to the real owner.

The original plans mixed intermediate measurements, rejected approaches, stale
test selectors, and developer-local commands. They are preserved in Git history:
`docs/consolidation-overhaul-design.md` and earlier revisions of this file. Their
line-count estimates and claims about a particular working tree are not current
release evidence.

## Further Changes

The runtime loop and product workflow packages remain large. A further split
should follow a demonstrated ownership or correctness problem, backed by behavior
coverage. File size alone does not justify moving a state machine across files
or introducing another layer.

Characterize permission, evidence, recovery, and cancellation behavior before
changing those boundaries. Run focused package tests, `go test ./...`, and
`go build ./cmd/idiom` for shared runtime changes. Current release evidence
belongs with the [release checklist](release.md), not this historical record.
