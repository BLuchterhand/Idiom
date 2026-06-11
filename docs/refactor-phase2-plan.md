# Wire runner_runstate.go into runner.go

## Current state

`runner_runstate.go` (336 lines, builds clean) has:
- `planExecuteRunState` struct (30+ fields)
- 12 methods on `*planExecuteRunState`

`runner.go` still has all 13 closures inline — the methods are dead code.

## Plan

Replace each closure body with a delegate call to the corresponding method.
Add a `syncLocalsToState` bridge (declared before closures, populated after state vars).

### Step 1: Add state declaration + syncLocalsToState

After the first `defer` block (line ~221), add:
```go
state := &planExecuteRunState{brief: r.RuntimeBrief}
var syncLocalsToState func()
```

### Step 2: Replace 13 closure bodies

Each closure becomes a thin wrapper:
```go
rebuildTools := func() {
    if syncLocalsToState != nil { syncLocalsToState() }
    r.rebuildRunTools(state)
    opts = state.opts  // sync-back
}
```

Closures that need sync-back:
- `rebuildTools` → `opts = state.opts`
- `syncPlanThenExecuteRecoveryAllowedTools` → `currentAllowedTools = state.currentAllowedTools`
- `refreshPlanThenExecuteTerminalSurface` → `turnMessages = state.turnMessages`
- `injectPlanThenExecuteRecoveryTail` → `opts = state.opts; *turnMessages = state.turnMessages`
- `forcePlanThenExecuteTerminalSurface` → `turnMessages = state.turnMessages`
- `emitCompletionStatus` → `completionStatusEmitted/L/ = state.completion...`
- `emitTerminalAnswerSignalsOnce` → `acceptedAnswerSignalsEmitted = state.acceptedAnswerSignalsEmitted`
- `finish` → `stopReason = state.stopReason; budgetExhausted = state.budgetExhausted`
- `retryRejectedFinalAnswer` → `completionGateRejections = state.completionGateRejections`

Closures that only read state (no sync-back needed):
- `setCurrentAllowedTools` (writes Runner field directly)
- `syncCurrentAllowedToolsFromOptions`
- `terminalSubmitPlanNativeTools`

### Step 3: State population + syncLocalsToState definition

After `autonomyRecorder.emitContractStarted(visibleRuntimeBrief)`, add:
```go
state.opts = opts
state.collectedResults = collectedResults
// ... all other fields ...

syncLocalsToState = func() {
    state.opts = opts
    state.collectedResults = collectedResults
    // ... all fields ...
}
```

### Step 4: Move early rebuildTools calls

Replace both early `rebuildTools()` calls with one after state population.

## Verification

After each closure:
- `go build ./internal/runtime/agent/...`
- `go test -run 'TestPlanThenExecute|TestPlanExecute|TestRecovery|TestSubmitPlan' ./internal/runtime/agent/...`

After all closures:
- Full test suite
- Review by all 3 reviewers

## Risk areas

1. `syncLocalsToState` must copy ALL fields that methods read — missed fields cause stale-data bugs
2. Sync-backs must copy ALL fields that methods write — missed sync-backs cause silent divergence (the bug that broke tests before)
3. `controller.rejectCompletion` writes to local `turnMessages` via pointer — `retryRejectedFinalAnswer` wrapper must NOT overwrite with `state.turnMessages`
4. Early `rebuildTools()` calls must be moved to after state population
