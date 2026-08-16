# Model Capability Profile

Status: accepted for implementation

## Purpose

The runtime must select request shaping, tool transport, context budgets, and
reasoning controls from declared model capabilities. Backend kind is an
infrastructure detail and is not sufficient to describe a model's actual
contract.

## Authority and boundaries

- `internal/runtime/llama` owns provider/model capability discovery and the
  normalized capability profile.
- `internal/runtime/agent` consumes the profile; it must not infer model
  behavior from provider strings or model-name substrings.
- `internal/evidence` records the profile used by a run.
- A profile is advisory only until readiness has verified the required fields.

## Contract

`ModelCapabilityProfile` has a stable schema version and contains model ID,
provider, protocol, context and output limits, native-tool and structured-output
support, reasoning controls, chat-template constraints, multimodal support,
prompt-cache accounting, and server lifecycle support.

Unknown capability values are represented as `unknown`, never as `false`.
Request construction may use a capability only when it is `supported`.

The profile is immutable after readiness. A backend change starts a new run
profile and cannot mutate the profile of an active invocation.

## Discovery

1. Start with configured backend defaults.
2. A readiness or integration caller may explicitly query the backend model
   endpoint when available via `DiscoverCapabilityProfile`.
3. Apply only provider-specific facts that are documented by the adapter.
4. Validate required limits and protocol compatibility.
5. Emit the resulting profile as metadata-only evidence.

Discovery failure is fatal when the configured run requires a capability that is
unknown; it is non-fatal for optional capabilities.

## Invariants

- Native tools are never selected when the profile does not support them.
- A context budget never exceeds the effective profile limit.
- Chat-history normalization is selected by a declared template constraint.
- Profile evidence contains no prompts, file bodies, credentials, or raw model
  responses.
- Every model call records the profile schema version and profile fingerprint.

## Tests and end-to-end acceptance

- Table tests cover supported, unsupported, and unknown capabilities.
- Adapter tests verify MLX/Qwen system-message constraints are represented by
  the profile rather than an agent-layer special case.
- A fake model server reports a model profile; `idiom run --json` must record
  the same fingerprint and use the declared tool transport.
- A server advertising no native tools must receive a textual-tool request.
- A context limit smaller than configured policy must produce a bounded request
  and a recorded readiness fact.

## Failure and rollback

Profile construction is read-only. If it fails, no model request is sent. The
existing backend fallback remains available only for explicitly configured
legacy adapters and is recorded as `profile_source=configured_default`. The
normal run path does not perform a hidden network preflight; callers opt into
endpoint discovery when they need live model-list evidence.
