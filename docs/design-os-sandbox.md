# Optional OS Process Sandbox

Status: accepted for implementation

## Purpose

Logical workspace policy prevents unauthorized Idiom actions. An optional OS
profile adds defense in depth when a command executes untrusted repository code.

## Authority and boundaries

- `internal/platform/commandexec` owns platform-specific process isolation.
- `internal/contracts` owns the portable sandbox capability and policy model.
- Workspace policy decides whether a command may run; the OS sandbox restricts
  what an admitted command can reach.
- Sandbox setup must not be hidden in shell strings or prompt instructions.

## Contract

`SandboxProfile` contains filesystem read roots, write roots, network mode,
temporary-directory policy, inherited environment policy, and enforcement mode.
Modes are `disabled`, `advisory`, and `enforced`.

The profile is attached to `commandexec.Spec`. An enforced profile that cannot
be installed fails before process start. Advisory mode records why enforcement
was unavailable and continues only when the caller explicitly permits it.

macOS uses a generated Seatbelt profile passed to `sandbox-exec` when available.
Because current macOS `sandbox-exec` requires a broad runtime read closure for
ordinary dynamically linked tools, the current adapter enforces declared write
roots and network policy while recording read roots as capability metadata.
Linux uses a generated bubblewrap invocation when available and binds declared
read roots read-only. Unsupported platforms return a typed unsupported result.
The generated policy is derived from canonical paths and never interpolates
untrusted shell text.

## Security invariants

- The workspace root is not implicitly writable unless declared.
- Network is denied by default in enforced mode.
- Secrets are not copied into the sandbox environment by the adapter.
- The command and sandbox child process share the existing managed process-tree
  lifecycle and cancellation behavior.
- Generated profiles are temporary artifacts and are removed after execution.

## Tests and end-to-end acceptance

- Unit tests cover path canonicalization, escaping, network modes, unsupported
  platforms, and profile validation.
- macOS E2E executes a harmless command that can read an allowed file but cannot
  read a denied file or write outside the declared root.
- Linux E2E performs the equivalent check when bubblewrap is installed and is
  skipped with an explicit capability result otherwise.
- Cancellation and timeout tests verify no sandbox child remains.
