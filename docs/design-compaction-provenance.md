# Compaction and Resume Provenance

Status: implemented provenance record and optional resume validation.

The runtime owns context selection and assembly.
[`internal/evidence/compaction`](../internal/evidence/compaction) owns the
`Provenance` record: schema version, source/retained/evicted message counts,
source/retained/summary hashes, retained and required paths, strategy, reason,
and reconstruction status.

## Current Contract

Construction normalizes path lists and rejects missing required paths. A record
is acceptable when it uses the current schema, reports verified reconstruction,
and represents actual message eviction. The record summarizes path retention;
it does not prove that every approval, obligation, or source body survived.

The runtime creates provenance while compacting its context. A caller may pass
`SeedContextCompactionProvenance` into `Runner`; a supplied unacceptable record
blocks initialization. The seed is optional. The API does not itself load the
latest durable record or require one for every resumed invocation.

Persistence and recovery remain owned by the existing workflow/session callers.
The provenance type does not introduce another session store. Message IDs,
profile fingerprints, and budget-before/after fields from the original design
are not part of the current record.

## Verification

```bash
go test ./internal/evidence/compaction
go test ./internal/runtime/agent
```

The focused provenance package covers retention and acceptance. Runtime coverage
includes rejection of an invalid resume seed. Durable resume integration should
be tested at the caller that loads and supplies session evidence.
