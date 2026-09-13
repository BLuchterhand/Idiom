# Model Capability Profile

Status: implemented foundation with limited discovery.

[`internal/runtime/llama/model_profile.go`](../internal/runtime/llama/model_profile.go)
owns `ModelCapabilityProfile`. It records backend, protocol, model identity,
source, structured-output capabilities, context/output limits, chat-template
constraints, prompt-cache support, vision, server lifecycle, and a fingerprint.
Unknown optional capabilities remain `unknown`.

## Selection and Discovery

`BackendModelCapabilityProfile` projects adapter facts into a profile with
`source=configured_default`. Context and output limits remain unknown unless
explicitly supplied. `EffectiveContextLimit` caps a positive configured budget
when a supported profile limit is present.

`DiscoverCapabilityProfile` explicitly calls the model-list endpoint and checks
that the requested model is advertised. It changes the source to
`models_endpoint`; it does not discover context limits or other optional
capabilities from that response. Normal runs do not perform this network probe
implicitly.

The agent consumes the profile for request shaping and records profile metadata.
A fingerprint identifies the profile fields used; it is not a certification of
live provider behavior. Backend-specific adapters still own their protocol
behavior.

## Verification

```bash
go test ./internal/runtime/llama
```

Profile tests cover unknown capabilities, context-limit bounding, MLX template
constraints, and explicit discovery against a fake model endpoint. Live provider
compatibility and model quality require separate release evidence.
