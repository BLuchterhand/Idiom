# Optional OS Process Sandbox

Status: process adapter implemented; not enabled by normal CLI configuration.

[`internal/platform/commandexec`](../internal/platform/commandexec) owns
`SandboxProfile` and the platform adapters. A caller can attach a profile to
`commandexec.Spec`. Workspace policy separately determines whether a command
may run. Current production call sites do not attach sandbox profiles, so
ordinary Idiom commands execute with the launching user's host permissions.

## Current Contract

A profile contains enforcement mode, read roots, write roots, network mode, and
a temporary-directory field. Enforcement modes are `disabled`, `advisory`, and
`enforced`. Active profiles validate absolute roots and known network modes.
Network defaults to denied for an active profile.

An enforced profile fails before execution if its launcher is unavailable.
Advisory mode falls back to ordinary process execution when the platform adapter
is unavailable. Callers must account for that weaker behavior.

macOS uses `sandbox-exec` with a generated Seatbelt policy. It restricts declared
write roots and network access but permits broad runtime reads; declared read
roots are not a read-isolation guarantee. Linux uses bubblewrap and binds read
roots read-only. Other platforms have no enforcement adapter.

The profile does not contain an environment allowlist. Process environment
handling follows `commandexec.Spec`; callers must select inherited variables
explicitly if they need secret isolation. The temporary-directory field is not
a general guarantee of isolated temporary storage.

## Verification

```bash
go test ./internal/platform/commandexec
```

Tests cover profile validation, unavailable-launcher failure, and a host sandbox
smoke when available. A successful smoke is narrower evidence than a complete
filesystem, network, environment, and cancellation security review. Host-specific
sandbox coverage is required before exposing this as a supported user feature.
