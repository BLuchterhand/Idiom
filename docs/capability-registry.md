# Capability Registry

P3.1 introduced a read-only capability vocabulary for callers that want a
stable contract instead of binding directly to tool names. P3.5 routes the
first read-only discovery slice through the `read_only_capability` tool while
leaving direct tool names available as fallback.

Stable read-only capability IDs:

- `file-outline`
- `inspect-symbol`
- `symbol-lookup`
- `definition`
- `references`
- `calls-from`
- `calls-into`
- `related-files`

The model or caller asks for a capability. The harness registry resolves that
ID to existing read-only adapters such as `code_navigate`, language-specific
symbol tools, and other read-only discovery adapters. Runtime execution now
routes these IDs through `code_navigate` first, preserving the adapter's
language routing, generic fallback, uncertainty, and result evidence. The
response is a thin wrapper with capability metadata, the selected adapter, any
fallback reason, and the underlying tool result.

`read_only_capability` is intentionally narrower than `code_navigate`.
`code_navigate` may expose newer runtime navigation affordances such as
`implementations` and `extract_body` before they become stable product
capability IDs. Promote those into `read_only_capability` only when the product
contract, resolver adapters, schema enum, audit metadata, and regression tests
are all updated together.

The wrapper keeps `schema_version: idiom.read_only_capability_result.v1` and
adds an additive `status` object:

- `status.state`: `routed`, `fallback`, or `absent`.
- `status.selected_adapter`: selected tool, capability, language, registry
  source, provenance, and side-effect class.
- `status.precision_tier` and `status.confidence`: selected route precision
  (`lsp`, `syntax-index`, `rg-fallback`, or `unavailable`) and derived
  reviewer confidence.
- `status.fallback`: machine-readable fallback state and normalized reason;
  source adapter reason is included separately when it differs.
- `status.absence_confidence`: reported when a search or lookup returns no
  result, with level, reason, precision tier, and source.
- `status.unresolved_gaps`: bounded uncertainty details such as unmapped
  capability, unavailable adapter, unsupported language route, degraded
  fallback, or empty result evidence.

Runtime disable path:

- Set `IDIOM_READ_ONLY_CAPABILITY_RESOLVER=0` (also accepts `false`, `off`,
  `disabled`, `disable`, or `no`) to disable capability execution.
- When disabled, `read_only_capability` returns a structured
  `resolver_runtime_disabled` fallback result.
- Direct adapter tools such as `code_navigate`, `go_definition`, and
  `go_references` remain visible and callable.
- Set `IDIOM_READ_ONLY_CAPABILITY_DISABLED_ROUTES` to a comma-, semicolon-, or
  whitespace-separated list of capability IDs to disable individual routes
  without disabling the whole resolver. Aliases normalize the same way as
  requests, so `definition, related_files` disables `definition` and
  `related-files`.
- An individually disabled route returns a structured
  `capability_route_disabled` fallback result. Direct adapter tools remain
  available through the legacy tool surface.

Public status rollback path:

- Set `IDIOM_READ_ONLY_CAPABILITY_PUBLIC_STATUS=0` (also accepts `false`,
  `off`, `disabled`, `disable`, `no`, `hidden`, `hide`, `suppressed`, or
  `suppress`) to omit the public `status` object from `read_only_capability`
  result payloads.
- This switch is distinct from `IDIOM_READ_ONLY_CAPABILITY_RESOLVER=0`: the
  resolver still routes when enabled, and disabled/unmapped fallback payloads
  remain structured.
- Internal telemetry and `harness_audit` coverage still derive status,
  fallback, absence-confidence, and field-count metrics from the structured
  wrapper and registry even when the public `status` object is suppressed.

Deferred capability:

- `source-sanity` remains available as the direct `source_sanity` tool, but it
  is not part of the initial read-only-only capability vocabulary. Its schema
  truth is `read_only` plus `unknown` because it can dispatch to diagnostics
  that run build/typecheck/compiler commands. A later registry can reintroduce
  it only with honest mixed/unknown status or a read-only-only adapter.
- Requests for `source-sanity` through `read_only_capability` return a
  structured `capability_deferred` fallback. The payload names `source_sanity`
  as the deferred direct tool and reports the non-read-only-only schema reason
  instead of pretending the route is read-only.

`harness_audit` with `purpose=tool_surface` reports capability coverage:

- `capability_count`
- `public_status_enabled`
- `status_field_count`
- `fallback_state_count`
- `fallback_states`
- `routed_capability_count`
- `routed_capabilities`
- `fallback_reason_count`
- `fallback_reasons`
- `route_state_counts`
- `route_states`
- `disabled_capability_count`
- `disabled_capabilities`
- `mapped_adapter_count`
- `read_only_adapter_count`
- `mapped_read_only_adapter_count`
- `coverage`
- `unmapped_adapter_count`
- `unmapped_adapters`
- `unsafe_mapped_adapter_count`
- `unsafe_mapped_adapters`
- `deferred_adapter_count`
- `deferred_adapters`

These metrics make it possible to track how much of the read-only adapter
surface has a stable capability ID and how much of that vocabulary is routed at
runtime. Unsafe mapped adapters are named directly when a read-only capability
points at a tool whose schema side effects are not read-only-only.
