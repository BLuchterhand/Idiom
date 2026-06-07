# Brief: Atom-Scoped Ledger Coverage via Commit Hashes

## Task ID
impl-0019-phase-2-atom-scoped-ledger-coverage-via-commit-hashes

## Problem
The current ledger coverage guard validates that the UNION of all atoms'
changed_paths covers the git diff for the effort. This means atom B can
omit a changed path if atom A already lists it (piggybacking). The guard
cannot verify that each atom independently records its own changes.

## Solution
Add an optional `commit_hash` field to `ImplementationLedgerRecord`. When
set on an `implementation_completed` or `fallback_completion` record, the
lint can validate that atom's changed_paths against the actual git diff
for that specific commit.

## Exact Changes

### 1. `internal/workflows/productworkflow/types.go`
Add field to `ImplementationLedgerRecord`:
```go
CommitHash string `json:"commit_hash,omitempty"`
```

### 2. `internal/workflows/productworkflow/validate.go`
In `validateImplementationLedger`, when `rec.CommitHash != ""`:
- Validate it matches `^[0-9a-f]{40}$`
- Only on `implementation_completed`, `fallback_completion`, `partial_implementation_recorded`

### 3. `internal/workflows/productworkflow/scope.go`
Add new pure function:
```go
type AtomCommitCoverage struct {
    TaskID       string
    CommitHash   string
    ChangedPaths []string
}

func ValidatePerAtomCoverage(atoms []AtomCommitCoverage, commitPaths map[string][]string) []LedgerCoverageFinding
```
- For each atom with non-empty CommitHash:
  - Look up `commitPaths[atom.CommitHash]`
  - If not found, emit `commit_hash_not_found` finding
  - Compare atom.ChangedPaths against the commit's paths
  - Emit `atom_under_reports_path` for each commit path not in atom.ChangedPaths
- Skip atoms without CommitHash (backward compatible)

### 4. `internal/surfaces/cli/product/product_workflow.go`
Add `gitCommitPaths` helper:
- Takes a slice of commit hashes
- Runs `git show --name-only --format="" <hash>` for each
- Returns `map[string][]string` (commit hash -> changed paths)

Wire into lint:
- Build `[]AtomCommitCoverage` from parsed ledger
- Call `gitCommitPaths` for atoms with commit hashes
- Call `ValidatePerAtomCoverage` and append findings

### 5. Tests
- `validate_test.go`: test commit_hash format validation (valid, empty, invalid hex, too short)
- `scope_test.go`: test `ValidatePerAtomCoverage` with matching, under-reporting, missing commit, backward-compatible skip
- `product_workflow_test.go`: test lint integration with commit hashes

## Forbidden
- Do NOT add git/os/exec/flag imports to scope.go (purity constraint)
- Do NOT change existing `ValidateLedgerCoverage` union check (keep for atoms without commit_hash)
- Do NOT modify research_profile_store.go, transition.go, status.go

## Stop Condition
All 5 TDD obligations pass, product lint passes on the durable fixture,
full `go test ./...` passes, and the scope.go purity test passes.
