# G626 round 2 Windows test inventory

Date: 2026-11-23
Branch: `g626-warm`

These are pre-existing Windows/environment failures observed while verifying
the round-2 changes. They were not repaired as part of this brief.

## `internal/platform/safepath`

The package ran 31 specs: 18 passed, 4 failed, and 9 skipped. The four
failures are all the existing `roots spelled through a symlinked ancestor`
setup in `internal/platform/safepath/rebase_test.go:26`:

- `opens and walks the root when both are given through alias`
- `still rejects a symlink inside root that escapes it`
- `treats an in-root symlink alias same through canonical/aliased root`
- `rejects a path outside both spellings root`

Each fails while creating the test symlink with Windows error
`ERROR_PRIVILEGE_NOT_HELD` (`syscall.Errno 0x522`, “A required privilege is not
held by the client”). The new symlink-target, symlink-ancestor, and FIFO tests
skip cleanly when the Windows account cannot create those fixtures.

## `internal/capabilities/workspace` (`TestWorkspace`)

The focused suite ran 297 of 1084 specs before its two-minute suite timeout:
279 passed, 18 failed, and 787 were skipped.

Five discovery-tools harness-audit cases fail because the fixture telemetry
contains the malformed JSON escape `U` in
`logs/telemetry/active-implement.jsonl`; the decoder reports
`invalid character 'U' in string escape code` and the expected lifecycle or
session-summary evidence is absent:

- `harness audit classifies active implement lifecycle as in progress`
- `harness audit classifies active tui implementation conversation as in progress`
- `harness audit links stopped tui implementation conversation to telemetry lifecycle`
- `harness audit accepts tui model closeout telemetry as session summary evidence`
- `harness audit surfaces local ops degraded telemetry`

Two Go semantic fallback tests fail only on expected summary wording: they
expect `semantic fallback after gopls failure` or `semantic fallback after
empty gopls result`, while the implementation reports
`semantic fallback; gopls unavailable`:

- `go workspace symbol falls back after gopls failure`
- `go workspace symbol falls back after empty gopls result`

Eight external-tool tests fail because the configured helper command is absent
from `PATH` or is not executable in this Windows environment:

- `manager does not advertise unavailable external tools`
- `manager executes external tools from root when cwd empty`
- `tool surface snapshot copies external tool view`
- `manager bounds external tool output`
- `manager enforces external tool declared timeout`
- `manager external tool uses canonical pwd overlay`
- `manager external tool merges stdout and stderr`
- `manager passes external tool input on stdin`

Two shell tests fail because `/bin/sh` is unavailable on Windows:

- `shell rejects zero selected doctest command`
- `manager shell rejects directory only commands: allows a directory change followed by a command`

One discovery-query case reaches the suite timeout while writing its
checkpoint artifact through Windows root-handle I/O:

- `discovery query builds missing context index for orientation`

The full `go test ./...` command was also attempted, but it produced no
completion result after more than six minutes and was stopped; the focused
package inventory above is the actionable evidence.

## Other existing issues

`readMessageWithin` still uses the pre-existing goroutine-based bounded-read
shape and can leave a reader goroutine behind when the read deadline wins. It
was intentionally not changed in this round because the brief explicitly
excluded that unrelated leak fix.
