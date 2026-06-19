# Design: Intent-Loop Mutation Cycle

**Status:** revised v4 after boundary audit and external source review;
North-Star/Architecture reviewer accepted the pre-external-review draft on
2026-06-18; post-update `idiom` reviewer session
`20260618-171432.600` ended without a usable verdict after its completion gate
rejected the attempted final answer, so it is not counted as approval
**Target:** extend mutation lanes with an outer intent-loop that iterates
planner → executor → review until the full contract terminal evidence is satisfied

## 1. Problem

Today `plan-execute` runs one linear cycle:

```
Planner → Executor → Review/Repair → Terminal Completion (success or failure)
```

After review/repair passes, the workflow exits — even when intent-level requirements
remain unmet. For example:

- `go test ./...` passes but `go vet ./...` fails
- Build succeeds but coverage is below threshold
- A second required test command (from the contract) was never run
- An acceptance evidence ID declared in the `AtomExecutionContract` was not satisfied

The model gets one shot at review/repair and then the harness closes out. There is
no outer loop that says: "The intent is not yet fulfilled — feed the remaining
failures back to the planner and try again."

## 2. Goals

1. **Intent-level iteration:** after review/repair, check all contract terminal
   evidence requirements; if any fail, feed the failure evidence into a *new*
   planner phase as the basis for a fresh `plan-execute` cycle.
2. **Rich failure context:** the new planner receives the original intent, a
   summary of prior cycles (what changed, what passed, what failed), and an
   explicit list of remaining requirements with expected outcomes.
3. **Convergence detection:** if any failure set repeats across cycles (not
   just consecutive), terminate with `no_progress`. This catches both
   stagnation (A→A) and oscillation (A→B→A).
4. **Budget exhaustion:** track token/time budget across cycles; terminate
   when exhausted.
5. **Persistable and resumable:** cycle state is durable so a long-running
   intent loop can be resumed after interruption, including mid-cycle crashes.

Non-goals for this design:
- Changing the internal phase contracts of plan-execute (they stay as-is)
- Adding intent-loop support to `idiom implement` (single-shot mutation lane)
- Real-time coverage instrumentation (coverage is a verification command)
- Multi-atom orchestration (one atom per intent loop invocation)

## 3. Architecture

### 3.1 New orchestrator: `internal/workflows/intentloop/`

A new package wraps `planexecute.Run()` in an outer loop. It does not modify
the plan-execute internals; it calls `planexecute.Run()` as a sub-routine,
inspecting the terminal completion record after each cycle to decide whether
to start another.

```
                    ┌─────────────────────────────────────────┐
                    │           intentloop.Run()               │
                    │                                          │
                    │  ┌──────────────────────────────────┐    │
                    │  │   for cycle := 1..maxCycles:     │    │
                    │  │    1. Build cycle prompt         │    │
                    │  │    2. planexecute.Run()          │    │
                    │  │    3. Inspect terminal record    │    │
                    │  │    4. Check all contract reqs    │    │
                    │  │    5. Convergence / budget check │    │
                    │  │    6. Persist loop state         │    │
                    │  └──────────────────────────────────┘    │
                    └─────────────────────────────────────────┘
```

### 3.2 Why a new orchestrator, not a loop inside planexecute

- `planexecute.Run()` already has a clear contract: one cycle, returns a
  `RunResult` with a `PlanExecuteTerminalCompletionRecord`. Looping inside it
  would muddy phase tracking, progress state, and the terminal completion
  validation (which currently validates one cycle, not many).
- The recovery system (`internal/workflows/recovery/`) already models
  "decompose failure into residual work → feed into next atom." The intent
  loop extends that pattern: the residual work is the remaining contract
  evidence, and the "next atom" is another planner phase.
- Separation lets us evolve the loop policy (budgets, convergence,
  prompt construction) independently of the single-cycle executor contract.

### 3.3 Integration with the CLI

The `idiom lane plan-execute` command gains an `--intent-loop` flag (default
`false`). When set, the lane dispatches to `intentloop.Run()` instead of
`planexecute.Run()`. The lane layer remains thin: it just picks the entrypoint.

When `--intent-loop` is *not* set, behavior is unchanged.

### 3.4 Boundary audit: user inputs and loop authority

The loop is safe only if each boundary has one authority source. The
implementation must preserve this matrix:

| Boundary | Inputs | Frozen once | Recomputed per cycle | Hard blocker |
| --- | --- | --- | --- | --- |
| Intake | prompt or prompt file, typed contract JSON, CLI `--required-edit-path`, `--allowed-edit-path`, write envelope, authorized new files/roots, edit-set obligations, required tests, extra verification commands, `--max-cycles`, token/time budget, `--skip-review-repair`, resume ID, model/profile overrides | original prompt bytes, contract hash, scope contract, write envelope, edit-set obligations, required tests, acceptance obligations, loop budget | nothing from cycle prompt may change authority | invalid scope, protected path, outside-root path, unsupported required command, dirty scoped files before first dispatch |
| Cycle prompt | original intent, compact prior-cycle evidence, latest remaining gaps | no authority; prompt is model-facing evidence only | prompt body for the next planner | prompt builder cannot recreate the exact saved prompt for a resumed in-flight cycle |
| Plan-execute sub-run | frozen scope, explicit verification commands, authorized new-file set, current workspace source | child session ID, child prompt hash, pre-run scoped fingerprint | source-body evidence, plan artifact, changed paths, source sanity, verification, review, terminal completion | missing/invalid plan artifact, missing source-body evidence for edited files, protected write, outside write envelope, unsupported verification command, planner blocker |
| Gap analysis | frozen contract plus child terminal completion | contract used for validation | remaining evidence gaps, gap fingerprints, next recoverable/nonrecoverable classification | terminal completion missing, malformed, or not tied to the child session |
| Resume | persisted loop state, saved cycle prompt, child progress state, current scoped workspace fingerprint | original prompt and cycle prompt for any dispatched child | incomplete child result or restart decision | scoped files changed outside the loop since the last saved fingerprint |

The cycle prompt is never authority for mutation scope, required tests,
acceptance obligations, budget, or write permissions. Those values come from
the original intake contract and are carried as typed options into every child
`planexecute.Run()`.

## 4. Key Data Structures

### 4.1 IntentLoopContract

Extracted once from the prompt. Mirrors the contract fields that the loop
must enforce:

```go
// internal/workflows/intentloop/contract.go

type IntentLoopContract struct {
    AtomID                string
    AtomTitle             string

    // From AtomExecutionContract.TerminalEvidence (normalized)
    RequireSourceSanity   bool
    RequireVerification   bool
    RequireReview         bool
    RequireRequiredTests  bool

    // From AtomExecutionContract.RequiredTests
    RequiredTestCommands  []RequiredTest  // commands that must pass (with IDs)

    // From AtomExecutionContract.AcceptanceObligations
    AcceptanceObligations []AcceptanceEvidenceRequired  // full obligations including Threshold

    // Frozen scope contract from the original prompt and CLI scope flags.
    // planexecute must receive this with prompt-scope extraction disabled for
    // child cycles; otherwise fallback mentioned-path extraction can promote
    // history or remaining-requirement paths into allowed scope when the
    // original prompt did not declare explicit paths.
    ScopeContract PlanScopeContract
}
```

Extraction reuses the existing prompt-contract parsing machinery:

- **Acceptance evidence IDs:** `planExecuteRequiredAcceptanceEvidenceIDsFromPrompt`
  in `acceptance_requirements.go` is currently unexported. Slice 0 exports it as
  `ExtractRequiredAcceptanceEvidenceIDs` (or renames the existing function) so
  `intentloop` can call it across packages. The extraction logic itself is reused
  verbatim — only the visibility changes.

- **Verification commands:** `ExtractRequestedVerificationCommands` (exported at
  `edit_set_obligations.go:39`) extracts commands from prompt prose. However,
  the intent loop primarily uses the *typed contract* (`RequiredTestCommands`
  from structured JSON) — these are injected as `ExplicitCommands` into the
  `VerificationPlan` (Slice 0). Prose-extracted commands become a secondary
  source, matching existing plan-execute behavior.

### 4.2 IntentLoopState

Persisted to `.idiom/intent-loop/sessions/<session_id>.json`. Tracks all
cycles:

```go
// internal/workflows/intentloop/state.go

type IntentLoopState struct {
    SchemaVersion    string              `json:"schema_version"`
    SessionID        string              `json:"session_id"`
    OriginalPrompt   string              `json:"original_prompt"`
    PromptSHA256     string              `json:"prompt_sha256"`
    CreatedAt        string              `json:"created_at"`
    UpdatedAt        string              `json:"updated_at"`

    Contract         IntentLoopContract  `json:"contract"`

    Cycles           []CycleRecord       `json:"cycles"`
    CurrentCycle     int                 `json:"current_cycle"`
    MaxCycles        int                 `json:"max_cycles"`

    // Convergence tracking — set of all gap fingerprints seen in prior
    // cycles. Any repeat (not just consecutive) triggers no_progress.
    SeenFailureSets    map[string]bool    `json:"seen_failure_sets,omitempty"`

    // Budget (accumulated across cycles)
    BudgetTokens     int                 `json:"budget_tokens"`
    TokensUsed       int                 `json:"tokens_used"`
    BudgetExhausted  bool                `json:"budget_exhausted,omitempty"`

    // Terminal
    Status           string              `json:"status"`
    // "running" | "completed" | "blocked" | "no_progress" |
    // "budget_exhausted" | "max_cycles_exceeded" | "failed"
    TerminationReason string             `json:"termination_reason,omitempty"`
}

type CycleRecord struct {
    CycleNumber          int                 `json:"cycle_number"`
    PlanExecuteSessionID string              `json:"plan_execute_session_id"`
    CyclePromptPath      string              `json:"cycle_prompt_path,omitempty"`
    CyclePromptSHA256    string              `json:"cycle_prompt_sha256,omitempty"`
    Phase                string              `json:"phase"`
    // "dispatched" (pre-run, for crash recovery) | "completed" | "failed"
    Status               string              `json:"status"`

    // Snapshot of plan-execute terminal completion (populated after completion)
    TerminalCompletionPath string             `json:"terminal_completion_path,omitempty"`
    ExitCode              int                 `json:"exit_code"`
    FailureKind           string              `json:"failure_kind,omitempty"`
    FailureReason         string              `json:"failure_reason,omitempty"`

    // Evidence snapshot
    ChangedPaths          []string            `json:"changed_paths,omitempty"`
    MutationDeltas        []CycleMutationDelta `json:"mutation_deltas,omitempty"`
    SourceSanityPassed    bool                `json:"source_sanity_passed"`
    VerificationPassed    bool                `json:"verification_passed"`
    ReviewPassed          bool                `json:"review_passed"`

    // Gap analysis: what requirements are still unmet after this cycle
    RemainingFailures     []EvidenceGap       `json:"remaining_failures,omitempty"`

    // Budget consumed by this cycle
    TokensUsed            int                 `json:"tokens_used,omitempty"`

    // Resumption support
    ResumeStatePath       string              `json:"resume_state_path,omitempty"`

    // Scoped workspace fingerprints protect resume from silently continuing
    // after human or external-tool edits outside the loop.
    PreRunScopedFingerprint  string            `json:"pre_run_scoped_fingerprint,omitempty"`
    PostRunScopedFingerprint string            `json:"post_run_scoped_fingerprint,omitempty"`
    ChildIdentity            *ChildRunIdentity `json:"child_identity,omitempty"`
}

type EvidenceGap struct {
    Requirement string `json:"requirement"` // "source_sanity", "verification:<cmd>", "acceptance:<id>"
    Status      string `json:"status"`      // "failed", "missing", "untested"
    Detail      string `json:"detail"`      // prompt-safe truncated detail
    DetailHash  string `json:"detail_hash,omitempty"` // SHA-256 of the full untruncated detail
    Path        string `json:"path,omitempty"`

    // For acceptance obligations with thresholds: the structured claim the
    // model made, plus the harness-computed comparison result.
    Claim       *AcceptanceEvidenceClaim `json:"claim,omitempty"`
    ThresholdResult string `json:"threshold_result,omitempty"` // "met" | "not_met" | "unparseable" | "missing"
}

type CycleMutationDelta struct {
    Path        string `json:"path"`
    Kind        string `json:"kind"` // "create" | "modify" | "delete" | "unknown"
    DiffSHA256  string `json:"diff_sha256,omitempty"`
    Additions   int    `json:"additions,omitempty"`
    Deletions   int    `json:"deletions,omitempty"`
}

type ChildRunIdentity struct {
    WorkspaceRootSHA256          string `json:"workspace_root_sha256,omitempty"`
    ScopeContractSHA256          string `json:"scope_contract_sha256,omitempty"`
    VerificationCommandsSHA256   string `json:"verification_commands_sha256,omitempty"`
    ToolManifestSHA256           string `json:"tool_manifest_sha256,omitempty"`
    BackendProfile               string `json:"backend_profile,omitempty"`
    PlannerModel                 string `json:"planner_model,omitempty"`
    ExecutorModel                string `json:"executor_model,omitempty"`
}
```

### 4.3 IntentLoopOptions

Extends the existing `planexecute.Options`:

```go
type IntentLoopOptions struct {
    // Original prompt, never modified. Used for contract extraction and
    // scope freezing. Distinct from the per-cycle prompt passed to
    // planexecute.Run().
    OriginalPrompt string

    EnableIntentLoop   bool        // true when --intent-loop is set
    MaxCycles          int         // default 5; CLI overridable with --max-cycles
    CycleBudgetTokens  int         // default 0 (no budget); if >0, total across all cycles

    // Extra commands injected into every cycle's VerificationPlan as
    // ExplicitCommands. The contract's RequiredTestCommands are always
    // injected; this field is for operator-specified extras.
    ExtraVerificationCommands []string

    // Captures scoped file fingerprints before the loop begins and after each
    // child cycle. Resume blocks when scoped files differ from the last saved
    // loop-owned fingerprint.
    DetectExternalScopedChanges bool
}
```

`planexecute.Options` also needs two new bridge fields before the orchestrator
is useful:

```go
type ScopeExtractionPolicy string

const (
    ScopeExtractionFromPrompt ScopeExtractionPolicy = "from_prompt"
    ScopeExtractionFrozen     ScopeExtractionPolicy = "frozen"
)

type Options struct {
    // Existing fields omitted.

    // Defaults to ScopeExtractionFromPrompt to preserve existing plan-execute
    // behavior. Intent-loop child cycles set ScopeExtractionFrozen so
    // planExecuteScopeContract uses ScopeContract and explicit option paths
    // only; it must not parse required/allowed/mentioned paths from the prompt
    // passed to the child cycle.
    ScopeExtractionPolicy ScopeExtractionPolicy

    // Passed to VerificationPlannerInput.ExplicitCommands on every
    // verification-plan construction in Run(), including the post-plan path
    // where changed paths are known.
    VerificationPlanExplicitCommands []string
}
```

Without these fields the loop is not actually frozen or self-healing: the
cycle prompt can alter scope, and contract-required commands can be absent from
the child verification plan.

## 5. Cycle Prompt Construction

The most important design element: how does the *n*th planner know what to do?

Each cycle's planner prompt is built from three layers:

### Layer 1: Original Intent (immutable)

The user's original prompt, preserved verbatim. Prefixed with a stable header:

```
## Original Intent

<original prompt text>
```

### Layer 2: Cycle History (compacted)

A compact summary of prior cycles — not a full per-cycle narrative, to avoid
context bloat and planner confusion. Generated from `IntentLoopState.Cycles`:

```
## Prior Cycles

2 prior cycles completed.

Cumulative changed paths: `internal/foo/bar.go`, `internal/foo/baz.go`, `internal/foo/qux_test.go`

### Previously passing evidence
- Source sanity: passed (cycle 1, cycle 2)
- `go test ./internal/foo/...`: passed (cycle 1, cycle 2)
- Review: passed, no corrective edits (cycle 1, cycle 2)

### Previously failing evidence
- `go vet ./internal/foo/...`: FAILED in cycles 1 and 2
  - Cycle 1: `bar.go:42:2: unreachable code`
  - Cycle 2: `bar.go:57:5: undefined: helperFunc`
```

The history is grouped by evidence kind (passing vs. failing), not by cycle
number, so the planner sees a consolidated view of what has and hasn't worked.
Full per-cycle details remain in the persisted state for debugging but are not
fed into the prompt.

### Layer 3: Remaining Requirements (findings, not prescriptions)

The explicit list of what still fails, extracted from the most recent cycle's
`RemainingFailures`. This section reports evidence — it does not prescribe
which edits to make. Error output is truncated to 2000 characters per gap.

```
## Remaining Requirements

The following contract requirements are still unmet after 2 cycles:

### Failing verification commands
- `go vet ./internal/foo/...` — exit code 1
  ```
  internal/foo/bar.go:57:5: undefined: helperFunc
  ```

### Unsatisfied acceptance evidence
- `coverage-threshold` — model declared coverage at 72% (threshold is 80%)
  detail_hash: `sha256:...`
```

Note: the harness does not parse raw command output to determine these gaps.
For threshold obligations, the model may extract a measured value from command
output into a structured `AcceptanceEvidenceClaim`, but the harness validates
that structured value against the threshold grammar. A `Met: true` declaration
is diagnostic only; it is not sufficient when the value is missing,
unparseable, or below threshold.

### Prompt composition order

```
<system addendum (plan-execute planner contract)>

## Original Intent
...

## Prior Cycles
...

## Remaining Requirements
...

## Instructions
Produce a plan (submit_plan) that satisfies the remaining contract
requirements listed above. Edits from prior cycles already cover part
of the contract — target only the remaining gaps.

The harness will reject any planned edit that lacks current source-body
evidence for the file being edited. Prior-cycle summaries describe what
changed but are not a substitute for source-body evidence.

If the remaining requirements cannot be resolved with the current scope
contract, report a blocker with `<<QUESTION>>:` and the exact obstacle.
```

Key design principle: this block reports evidence constraints and hard gates
("planned edits require current source-body evidence", "target remaining
gaps"), not edit prescriptions. It does not tell the model *how* to fix the
problem — that remains the model's strategy. The `## Remaining Requirements`
section above reports findings (failed commands, error snippets, unmet
thresholds) without prescribing the next tool call or edit.

## 6. Flow (detailed)

### 6.1 `intentloop.Run()` pseudocode

```go
func Run(cfg config.Config, opts IntentLoopOptions, ...) IntentLoopResult {
    // 1. Load or create loop state
    state := loadOrCreateState(cfg, opts)

    // 2. Freeze contract and scope from the ORIGINAL prompt (once, never from
    //    cycle prompts — prevents history/requirement paths leaking into scope)
    if state.Contract.isEmpty() {
        state.Contract = extractContract(opts.OriginalPrompt)
        state.Contract.ScopeContract = freezeScopeContract(opts.OriginalPrompt, opts)
    }

    // 3. Check for in-progress cycle from a prior crash (mid-cycle resume)
    if lastCycle := lastDispatchedCycle(state); lastCycle != nil {
        result := resumeOrRestartDispatchedCycle(cfg, opts, state, lastCycle)
        if result.terminal {
            return result
        }
    }

    // 4. Main loop
    for cycle := state.CurrentCycle + 1; cycle <= opts.MaxCycles; cycle++ {
        state.CurrentCycle = cycle

        // 4a. Pre-dispatch: record cycle as "dispatched" BEFORE calling
        //     planexecute.Run(), so a crash mid-cycle leaves a resumable trace.
        sessionID := newSessionID()
        cycleRecord := newDispatchedCycleRecord(cycle, sessionID)
        cycleIndex := len(state.Cycles)
        state.Cycles = append(state.Cycles, cycleRecord)
        saveState(cfg, state)  // crash-safe: state persisted pre-run

        // 4b. Build and persist the exact cycle prompt before dispatch.
        // Resume of an in-flight child must reuse this saved prompt byte-for-byte
        // because planexecute resume rejects prompt mismatches.
        cyclePrompt := buildCyclePrompt(opts.OriginalPrompt, state)
        cyclePromptPath := saveCyclePrompt(cfg, state.SessionID, cycle, cyclePrompt)
        state.Cycles[cycleIndex].CyclePromptPath = cyclePromptPath
        state.Cycles[cycleIndex].CyclePromptSHA256 = sha256(cyclePrompt)
        saveState(cfg, state)

        // 4c. Prepare plan-execute options with FROZEN scope contract
        peOpts := toPlanExecuteOptions(opts, cyclePrompt, state.Contract.ScopeContract)
        peOpts.Prompt = cyclePrompt
        peOpts.ScopeExtractionPolicy = planexecute.ScopeExtractionFrozen
        // Inject RequiredTestCommands as explicit verification commands so
        // PlanVerification runs exactly the contract-required commands.
        peOpts.VerificationPlanExplicitCommands = state.Contract.RequiredTestCommandStrings()

        // 4d. Run plan-execute
        result := planexecute.Run(cfg, peOpts, promptRun, rec, stdout, stderr)

        // 4e. Load terminal completion record
        terminalRecord := loadTerminalCompletion(result.CompletionPath)

        // 4f. Analyze remaining gaps
        gaps := analyzeEvidenceGaps(state.Contract, terminalRecord)

        // 4g. Update the pre-dispatched record with results
        finalizeCycleRecord(&state.Cycles[cycleIndex], result, terminalRecord, gaps)
        state.TokensUsed += state.Cycles[cycleIndex].TokensUsed

        // 4h. Terminal checks
        if len(gaps) == 0 {
            state.Status = "completed"
            compactChildArtifacts(cfg, state)
            return success(state)
        }

        // Only recoverable evidence gaps loop. Permission/scope/protocol
        // failures, planner blockers, unsupported verification commands,
        // missing terminal records, and external scoped changes stop as typed
        // blockers instead of being fed back as another model attempt.
        cycleOutcome := classifyCycleOutcome(result, terminalRecord, gaps)
        if cycleOutcome.Blocking {
            state.Status = "blocked"
            state.TerminationReason = cycleOutcome.Reason
            return blocked(state)
        }

        // 4i. Convergence check: has this EXACT failure set been seen before
        //     in ANY prior cycle? (not just the immediately previous one)
        failureSet := gapFingerprint(gaps)
        if state.SeenFailureSets[failureSet] {
            state.Status = "no_progress"
            state.TerminationReason = "failure set " + failureSet + " seen in prior cycle"
            return noProgress(state)
        }
        state.SeenFailureSets[failureSet] = true

        // 4j. Budget check
        if opts.CycleBudgetTokens > 0 && state.TokensUsed >= opts.CycleBudgetTokens {
            state.Status = "budget_exhausted"
            state.BudgetExhausted = true
            return budgetExhausted(state)
        }

        // 4k. Persist state for resume
        saveState(cfg, state)
    }

    state.Status = "max_cycles_exceeded"
    return maxCyclesExceeded(state)
}
```

### 6.2 Evidence gap analysis

`analyzeEvidenceGaps` checks the terminal completion record against the
contract and returns `[]EvidenceGap`:

1. **Source sanity gap:** if `RequireSourceSanity` and `SourceSanityPassed == false`.
2. **Verification gaps:** for each `RequiredTestCommand`, check if it passed
   in the verification evidence. A command that was never run is "missing."
3. **Acceptance evidence gaps:** for each `RequiredAcceptanceID`, check if
   it appears in `AcceptanceEvidenceIDs`.
4. **Required path coverage:** if changed paths don't cover all required paths
   (unless source-satisfied).

Gap analysis cannot rely only on `plancontract.ValidateAtomTerminalEvidenceInput`
because that validator intentionally returns no violations for non-success
terminal evidence. The loop first inspects the mechanical child terminal record
for failed or missing source sanity, verification, review, required-test,
required-path, and acceptance evidence. When a child claims success or
source-satisfied, the loop also runs `ValidateAtomTerminalEvidenceInput`
against the frozen contract and converts any violations into `EvidenceGap`
structs. This keeps failed-cycle recovery grounded in concrete terminal fields
while preserving the existing terminal success contract.

For acceptance obligations with thresholds, the terminal record carries
`AcceptanceEvidenceClaims`: structured measured values declared by the model.
The harness validates the structured claim value against a small deterministic
threshold grammar (`>=`, `>`, `<=`, `<`, `==` over numbers or percentages).
For example, `claim.Value = "72%"` against `Threshold = ">=80%"` fails even if
the model also says `Met: true`. The harness still does **not** parse raw
command output; it validates only the structured value that the model supplied.
If the value is missing or cannot be parsed with the same unit as the threshold,
the obligation remains unsatisfied.

### 6.3 Convergence detection

`gapFingerprint` produces a stable string from the gap set, including a hash
of each gap's full untruncated detail body. `EvidenceGap.Detail` is prompt-safe
and truncated; `EvidenceGap.DetailHash` is the convergence key. This prevents
two large failures with the same first 2000 prompt-visible characters from
being falsely classified as identical:

```go
func gapFingerprint(gaps []EvidenceGap) string {
    ids := make([]string, len(gaps))
    for i, g := range gaps {
        detailHash := g.DetailHash
        if detailHash == "" {
            detailHash = sha256hash(g.Detail)
        }
        detailHash = detailHash[:8]  // first 8 chars of SHA-256
        ids[i] = g.Requirement + ":" + g.Status + ":" + detailHash
    }
    sort.Strings(ids)
    return strings.Join(ids, "\n")
}
```

Convergence uses a **seen set** — a map of all fingerprints observed across
all prior cycles. If the current fingerprint has ever been seen before (not
just in the immediately prior cycle), the loop terminates with `no_progress`.
This catches:
- Stagnation: A → A (same failures, same error content)
- Oscillation: A → B → A (alternating between two failure states)
- Return: A → B → C → A (returning to an earlier failure after drift)

If the error content changes (different line numbers, different messages)
but the requirement+status pair is the same, the fingerprint differs
(because the detail hash differs) and the loop continues. This is correct:
the model made a different error, which is evidence of a different edit,
which may eventually converge. Near-matches are intentionally not collapsed in
v1; they remain observable through structured warning events and tests, not
through fuzzy convergence authority.

## 7. Persistence and Resumption

### 7.1 State file

Stored at `.idiom/intent-loop/sessions/<session_id>.json`. Same pattern as
`planexecute.ProgressState`.

### 7.2 Resume (including mid-cycle crash recovery)

`idiom lane plan-execute --resume <session_id> --intent-loop` reloads the
`IntentLoopState` and checks for incomplete cycles:

1. **No incomplete cycles:** the last cycle has status `"completed"` or
   `"failed"`. Continue from `state.CurrentCycle + 1`. The cycle prompt is
   rebuilt from the persisted state.

2. **Incomplete cycle found:** the last cycle has status `"dispatched"`
   (pre-run recording happened, but the process died before the cycle
   completed). The loop reloads `CycleRecord.CyclePromptPath` and reuses that
   exact prompt body. It does not rebuild the prompt for an in-flight child,
   because `planexecute.Run()` resume validates that the prompt matches the
   child progress state. The pre-dispatched `CycleRecord` is updated in-place
   with the new result.

   If the incomplete cycle is a review/repair phase (plan-execute resume
   state exists), the loop passes `opts.Resume` to `planexecute.Run()`
   to resume from the review/repair phase rather than restarting the
   full planner→executor→review chain.

3. **Scoped files changed outside the loop:** if the current scoped fingerprint
   differs from the last saved `PostRunScopedFingerprint`, resume stops with a
   typed `blocked_external_scoped_change` result. The operator can restart from
   a fresh intent-loop session after reconciling the external edits.

### 7.3 Cleanup

- Stale intent-loop sessions (>7 days) may be compacted on startup, matching
  the existing stale-artifact pattern, but compaction must preserve the parent
  loop state, terminal summaries, child session IDs, prompt hashes, completion
  record hashes, changed paths, failure kinds, and verification evidence
  summaries.
- When an intent loop session completes (success or terminal failure), child
  plan-execute artifacts are not blindly deleted. The parent state first
  snapshots each child terminal completion and stores content hashes for any
  plan/progress artifacts it references. Only bulky spill files or duplicate
  transient artifacts may be removed immediately.
- Full child progress and terminal completion records remain available for the
  normal retention window so `harness_audit`, recovery, and future regression
  generation can replay why the loop stopped.

## 8. JSON Events

New event types emitted by the intent loop:

| Event | When | Key fields |
|-------|------|------------|
| `idiom.intent_loop.start` | Loop begins | `session_id`, `max_cycles`, `budget_tokens`, `contract_id`, `contract_hash`, bounded contract summary |
| `idiom.intent_loop.cycle_start` | Each cycle begins | `cycle_number`, `cycle_prompt_sha256` |
| `idiom.intent_loop.cycle_complete` | Each cycle ends | `cycle_number`, `remaining_gaps`, `gap_count`, `tokens_used` |
| `idiom.intent_loop.convergence_detected` | No progress | `failure_set`, `previous_failure_set` |
| `idiom.intent_loop.cycle_budget_warning` | Cycle spends more than the advisory per-cycle share | `cycle_number`, `cycle_tokens`, `soft_cap_tokens`, `tokens_used`, `budget_tokens` |
| `idiom.intent_loop.budget_exhausted` | Budget hit | `tokens_used`, `budget_tokens` |
| `idiom.intent_loop.complete` | All requirements met | `total_cycles`, `total_tokens`, `changed_paths` |
| `idiom.intent_loop.blocked` | Planner blocked | `cycle_number`, `blocker_summary` |

Existing plan-execute events (`plan_artifact_loaded`, `executor_verification_repair_required`,
`review_repair_retry`, `terminal_completion`) continue to fire inside each cycle.
Intent-loop events must not include raw prompt bodies, raw transcript excerpts,
raw file bodies, or full command output. They may include bounded gap summaries,
paths, command strings, hashes, IDs, status names, and sanitized failure
reasons.

## 9. Prompt Contract Additions

### 9.1 Prompt markers for intent-loop contracts

Users can embed a typed contract in their prompt to declare intent-level
requirements:

```
Typed plan execution contract:
{
  "atoms": [{
    "atom_id": "impl",
    "required_tests": [
      {"command": "go test ./...", "id": "unit-tests"},
      {"command": "go vet ./...", "id": "vet"},
      {"command": "go test -cover ./...", "id": "coverage"}
    ],
    "acceptance_obligations": [
      {"id": "coverage-threshold", "description": "coverage >= 80%", "required": true, "threshold": ">=80%"}
    ],
    "terminal_evidence_requirements": {
      "explicit": true,
      "require_source_sanity": true,
      "require_verification": true,
      "require_required_tests": true
    }
  }]
}
```

The intent loop extracts `required_tests` and `acceptance_obligations` from
this contract. If no explicit contract is present, it falls back to the
existing prompt-embedded verification commands and acceptance IDs.

### 9.2 Threshold acceptance evidence — model extracts, harness computes

The existing `AcceptanceEvidenceRequired` gains an optional `Threshold` field
that declares the contract requirement (e.g., `">=80%"`, `">=100"`).

**The harness never parses raw command output.** Instead, the model inspects
verification output and declares a structured `AcceptanceEvidenceClaim` in the
terminal evidence. The harness parses only that structured value:

```go
// In terminal evidence input (model-declared):
type AcceptanceEvidenceClaim struct {
    ID    string `json:"id"`              // matches obligation ID
    Value string `json:"value,omitempty"` // e.g. "78.5%", "142"
    Met   bool   `json:"met,omitempty"`   // model's declaration, audit-only
}

// In contract (harness-enforced):
type AcceptanceEvidenceRequired struct {
    ID          string  `json:"id,omitempty"`
    Description string  `json:"description,omitempty"`
    Required    bool    `json:"required,omitempty"`
    Source      string  `json:"source,omitempty"`
    Threshold   string  `json:"threshold,omitempty"` // e.g. ">=80%", ">=100"
}
```

The harness validates:
1. For each required obligation with a `Threshold`, there must be a
   corresponding claim with `ID` matching the obligation.
2. If `Threshold` is set, `Value` must parse with the same unit family as the
   threshold (`%` with `%`, plain number with plain number).
3. The harness computes the comparison. `Met` is accepted only when it matches
   the computed result; a false `Met: true` is a validation failure, not a
   successful acceptance claim.

This keeps the model-strategy/harness-contract boundary intact: the model owns
extracting the relevant measurement from verification output, while the harness
owns deterministic comparison against the typed threshold. A model that
declares `Met: true` for `Value: "60%"` against `Threshold: ">=80%"` produces a
failed acceptance obligation and another recoverable evidence gap.

For obligations **without** a threshold (simple presence checks), the
existing ID-membership check suffices: the obligation ID must appear in
`AcceptanceEvidenceIDs`.

## 10. External Source Review: OpenCode and Reasonix

This pass inspected actual source code, not public docs:

- OpenCode `anomalyco/opencode` at
  `355a0bcf5bb5e6c7baa271a4b2439a40f286e55d`.
- Reasonix `esengine/DeepSeek-Reasonix` at
  `ba7a50b2d1d129b1b7eb7009ee36f8efb1f17135`.

The designs are different from Idiom's contract-first plan-execute lane, but
they surface concrete implementation hazards that the intent loop must handle
before coding starts.

### 10.1 OpenCode findings

OpenCode models context clearing as session history lineage, not as an
unstructured chat summary. `SessionContextEpoch.prepare` reconciles or replaces
the current system context snapshot and records a `baselineSeq`; when history is
loaded for the runner, `SessionHistory.loadForRunner` starts from the latest
compaction and the active baseline sequence. Source:
<https://github.com/anomalyco/opencode/blob/355a0bcf5bb5e6c7baa271a4b2439a40f286e55d/packages/core/src/session/context-epoch.ts#L67-L110>
and
<https://github.com/anomalyco/opencode/blob/355a0bcf5bb5e6c7baa271a4b2439a40f286e55d/packages/core/src/session/history.ts#L66-L99>.

Its compaction prompt is schema-shaped around durable task facts: goal,
constraints, progress, blockers, decisions, next steps, critical context, and
files. Tool results are truncated for summarization, and compaction emits
started/ended events with a retained recent tail. Source:
<https://github.com/anomalyco/opencode/blob/355a0bcf5bb5e6c7baa271a4b2439a40f286e55d/packages/core/src/session/compaction.ts#L16-L51>
and
<https://github.com/anomalyco/opencode/blob/355a0bcf5bb5e6c7baa271a4b2439a40f286e55d/packages/core/src/session/compaction.ts#L166-L228>.

OpenCode subagents are real child sessions. The `task` tool creates or resumes
a child session, derives child permissions from the parent session and the
selected subagent, supports `task_id` continuation, and returns a structured
`<task>` result. Background tasks inject a synthetic result back into the parent
session when they finish. Source:
<https://github.com/anomalyco/opencode/blob/355a0bcf5bb5e6c7baa271a4b2439a40f286e55d/packages/opencode/src/tool/task.ts#L121-L158>
and
<https://github.com/anomalyco/opencode/blob/355a0bcf5bb5e6c7baa271a4b2439a40f286e55d/packages/opencode/src/tool/task.ts#L202-L239>.

OpenCode mutations are host-mediated at the tool boundary. `edit` and `write`
compute a diff, ask permission against the path and diff, then write, format,
publish file events, and surface LSP diagnostics. The edit path also locks by
resolved file path so concurrent edits to the same file cannot interleave.
Source:
<https://github.com/anomalyco/opencode/blob/355a0bcf5bb5e6c7baa271a4b2439a40f286e55d/packages/opencode/src/tool/edit.ts#L35-L45>,
<https://github.com/anomalyco/opencode/blob/355a0bcf5bb5e6c7baa271a4b2439a40f286e55d/packages/opencode/src/tool/edit.ts#L137-L211>,
and
<https://github.com/anomalyco/opencode/blob/355a0bcf5bb5e6c7baa271a4b2439a40f286e55d/packages/opencode/src/tool/write.ts#L53-L100>.

### 10.2 Reasonix findings

Reasonix treats compaction as cache-sensitive state maintenance. It avoids
early compaction to preserve the prefix cache, prunes stale tool results before
paid summarization, archives folded messages, keeps small user turns verbatim,
wraps summaries in an explicit tag, and pauses auto-compaction when repeated
compactions cannot get below the trigger. Source:
<https://github.com/esengine/DeepSeek-Reasonix/blob/ba7a50b2d1d129b1b7eb7009ee36f8efb1f17135/internal/agent/compact.go#L81-L135>
and
<https://github.com/esengine/DeepSeek-Reasonix/blob/ba7a50b2d1d129b1b7eb7009ee36f8efb1f17135/internal/agent/compact.go#L179-L271>.

Its compaction summary prompt explicitly preserves standing user facts and
constraints, goal, decisions, files/code facts, commands/outcomes, errors/fixes,
and the next action. This is the closest external match to our cycle-prompt
history needs, but Reasonix keeps user turns verbatim while folding assistant
and tool work. Source:
<https://github.com/esengine/DeepSeek-Reasonix/blob/ba7a50b2d1d129b1b7eb7009ee36f8efb1f17135/internal/agent/compact.go#L48-L79>.

Reasonix subagents are isolated sessions whose tool surface is filtered. The
parent prompt must be self-contained because the subagent does not see parent
conversation history. Recursive agent/skill tools and unsupported background
job tools are removed, and bash is wrapped as foreground-only. Source:
<https://github.com/esengine/DeepSeek-Reasonix/blob/ba7a50b2d1d129b1b7eb7009ee36f8efb1f17135/internal/agent/task.go#L18-L68>
and
<https://github.com/esengine/DeepSeek-Reasonix/blob/ba7a50b2d1d129b1b7eb7009ee36f8efb1f17135/internal/agent/task.go#L503-L519>.

Persisted Reasonix subagent continuation is guarded by identity metadata:
workspace root, parent session, system prompt hash, tool scope, tool schema
hash, model, and effort. Continuation/fork refuses running, failed,
interrupted, mismatched, or cross-owner references. Source:
<https://github.com/esengine/DeepSeek-Reasonix/blob/ba7a50b2d1d129b1b7eb7009ee36f8efb1f17135/internal/agent/subagent_store.go#L30-L47>
and
<https://github.com/esengine/DeepSeek-Reasonix/blob/ba7a50b2d1d129b1b7eb7009ee36f8efb1f17135/internal/agent/subagent_store.go#L422-L449>.

Reasonix mutations expose a preview/diff contract before execution. Preview
mirrors execute semantics and is tested to match, so approval cards and
checkpoints do not lie about the write. Before an approved writer runs, the
agent snapshots the pre-edit state for rewind. Writer calls run serially; only
contiguous read-only calls parallelize. Source:
<https://github.com/esengine/DeepSeek-Reasonix/blob/ba7a50b2d1d129b1b7eb7009ee36f8efb1f17135/internal/tool/builtin/preview.go#L13-L22>,
<https://github.com/esengine/DeepSeek-Reasonix/blob/ba7a50b2d1d129b1b7eb7009ee36f8efb1f17135/internal/agent/agent.go#L1260-L1304>,
and
<https://github.com/esengine/DeepSeek-Reasonix/blob/ba7a50b2d1d129b1b7eb7009ee36f8efb1f17135/internal/agent/agent.go#L1553-L1563>.

### 10.3 Design implications for Idiom

These reviews change the implementation plan in four ways:

1. The loop must preserve original intent and the typed contract outside every
   compacted summary. Cycle history is evidence only. Any future compaction of
   cycle history must keep user constraints, contract IDs, required commands,
   changed paths, blockers, and terminal evidence hashes as structured state.

2. Child cycle continuation must be identity-checked. A saved child prompt hash
   is necessary but not sufficient; resume must also compare frozen scope,
   verification commands, tool manifest/schema identity, backend/profile, model
   role routing, and workspace root. A mismatch is a typed resume blocker, not
   a recoverable model retry.

3. Mutation deltas must be retained as structured artifacts. Changed paths are
   too weak for debugging self-healing loops; each cycle should store path,
   kind, additions/deletions, and diff hash. Full diffs remain in child
   artifacts under retention, while the parent loop stores hashes and bounded
   summaries.

4. Writer ordering and authority failures are not self-healing evidence gaps.
   Permission denials, plan-mode/read-only denials, protected-path denials,
   unsupported commands, unknown tools, and tool-schema mismatches stop as typed
   blockers. Recoverable loops are reserved for failed source sanity,
   verification, review, required-test, required-path, and acceptance evidence
   where the child ran under valid authority.

These changes keep Idiom different from OpenCode and Reasonix: we do not adopt
their open-ended chat loop or their subagent strategy. We borrow the generic
contract lessons: context clears need lineage, child continuation needs stable
identity, mutation needs preview/diff evidence, and retries must not treat
authority failures as mere planner input.

## 11. Implementation Plan (slices)

All slices are ordered by contract dependency, not by apparent code size. Slice
6 must not start until Slices 0-5 are merged, because the orchestrator relies
on the frozen-scope bridge, explicit verification bridge, terminal claim
schema, state persistence, gap analysis, and prompt persistence.

### Slice 0: Plan-execute bridge fields and exported helpers

**Files:**
- `internal/workflows/planexecute/types.go` — add
  `ScopeExtractionPolicy` and `VerificationPlanExplicitCommands`
- `internal/workflows/planexecute/scope_contract.go` — honor frozen scope by
  skipping prompt-derived required/allowed/mentioned path extraction
- `internal/workflows/planexecute/run.go` — pass explicit verification
  commands into every `PlanVerification` call
- `internal/workflows/planexecute/acceptance_requirements.go` — export
  `planExecuteRequiredAcceptanceEvidenceIDsFromPrompt` as
  `ExtractRequiredAcceptanceEvidenceIDs`
- matching tests in `internal/workflows/planexecute`

**What:** make the existing single-cycle lane callable as a child cycle without
letting child prompt text mutate scope, and without losing contract-required
verification commands.

**Verification:** tests prove frozen scope ignores paths mentioned only in a
cycle-history prompt, normal plan-execute prompt extraction is unchanged when
the flag is false, and an explicit `go vet ./...` command runs even when prompt
prose does not mention it. Add a negative test proving `submit_plan` is
rejected when an edited existing file lacks accepted current source-body
evidence.

### Slice 1: Terminal acceptance claims and threshold grammar

**Files:**
- `internal/contracts/plan/execution_contract.go` — add `Threshold` to
  `AcceptanceEvidenceRequired`, add `AcceptanceEvidenceClaim` to
  `TerminalEvidenceInput`, and validate claim values against thresholds
- `internal/workflows/planexecute/terminal_completion.go` — carry
  `AcceptanceEvidenceClaims` into terminal completion records and evidence
  input conversion
- `internal/workflows/planexecute/acceptance_requirements.go` — preserve full
  acceptance obligations, not only IDs, where terminal validation needs them
- matching contract and terminal-completion tests

**What:** make threshold acceptance deterministic. The model extracts a
structured value; the harness computes whether that value satisfies the typed
threshold.

**Verification:** missing claim, unparseable value, unit mismatch, below
threshold, at threshold, above threshold, and contradictory `Met` declarations
are covered by positive and negative tests.

### Slice 2: `IntentLoopContract` extraction

**Files:**
- `internal/workflows/intentloop/contract.go` — `IntentLoopContract`,
  `extractContract()`, `freezeScopeContract()`
- `internal/workflows/intentloop/contract_test.go`

**What:** parse the original prompt for `PlanExecutionContract` /
`AtomExecutionContract` JSON, extract required tests, full acceptance
obligations, terminal evidence requirements, authorized new-file authority, and
scope. Merge CLI scope flags into the frozen scope once.

**Verification:** unit tests with contract-embedded prompts, CLI-only scope,
mixed prompt/CLI scope, and a cycle-history prompt proving no history path is
accepted as authority.

### Slice 3: `IntentLoopState` persistence

**Files:**
- `internal/workflows/intentloop/state.go` — `IntentLoopState`,
  `CycleRecord`, `EvidenceGap`, save/load, prompt artifact paths, scoped
  fingerprints
- `internal/workflows/intentloop/state_test.go`

**What:** persist the loop state, cycle prompt paths/hashes, child session IDs,
terminal summary snapshots, mutation deltas, child run identity, budget use,
and scoped workspace fingerprints. `ChildRunIdentity` records the frozen scope,
verification-command set, tool manifest/schema identity, backend/profile, role
models, and workspace root used by the dispatched child.

**Verification:** round-trip save/load tests, session ID validation, exact
cycle-prompt reload, mutation-delta hash retention, child identity
round-tripping, and external scoped-change detection.

### Slice 4: Gap analysis and convergence classification

**Files:**
- `internal/workflows/intentloop/gap_analysis.go` —
  `analyzeEvidenceGaps()`, `gapFingerprint()`, `classifyCycleOutcome()`
- `internal/workflows/intentloop/gap_analysis_test.go`

**What:** compare the frozen contract with a child terminal completion record.
Recoverable gaps become next-cycle evidence; nonrecoverable permission,
scope, protocol, unsupported-command, unknown-tool, tool-schema-mismatch,
missing-terminal, and external-change failures become typed blockers.

**Verification:** table-driven tests cover source sanity, required test
missing/failed, acceptance ID missing, threshold claim failure, required path
coverage, missing source-body evidence for edited files, source-satisfied with
remaining obligations, unsupported command, planner blocker, malformed terminal
record, child identity mismatch, repeated full fingerprint, same truncated
detail with different full-detail hashes, and coarse requirement/status
thrashing.

### Slice 5: Cycle prompt construction

**Files:**
- `internal/workflows/intentloop/cycle_prompt.go` — `buildCyclePrompt()`,
  `saveCyclePrompt()`, prompt summary helpers
- `internal/workflows/intentloop/cycle_prompt_test.go`

**What:** compose original intent, compact prior evidence, and latest remaining
requirements into a planner prompt. Persist the exact prompt before child
dispatch. Original intent and typed contract text are preserved outside the
compacted history block; compacted cycle history can summarize only prior
assistant/tool work and bounded evidence summaries.

**Verification:** golden-file tests verify structure, bounded output, no raw
file bodies, no authority-looking required-path headings in history, stable
prompt hashes, and preservation of original contract text across multiple
cycle-history compactions.

### Slice 6: Main loop orchestrator

**Files:**
- `internal/workflows/intentloop/run.go` — `Run()`, `IntentLoopOptions`,
  `IntentLoopResult`
- `internal/workflows/intentloop/run_test.go`

**What:** load state, extract/freeze the contract, snapshot scoped workspace
state, persist dispatched cycles, call `planexecute.Run()` with frozen scope
and explicit verification commands, capture child identity and mutation deltas,
analyze gaps, classify blockers, check budget/convergence, and persist after
every transition.

**Verification:** integration tests with a mock `PromptRunner` cover happy path
across two cycles, recoverable verification failure, threshold gap then pass,
planner blocker, unsupported verification command, repeated failure set,
same truncated detail with different full-detail hashes, coarse thrash warning,
budget warning, budget exhaustion, max cycles, crash after dispatched state,
external scoped change on resume, and child identity mismatch on resume.

### Slice 7: CLI integration

**Files:**
- `internal/surfaces/cli/planexecute/plan_execute.go` — add `--intent-loop`,
  `--max-cycles`, and total budget flags
- `internal/surfaces/cli/lane/lane.go` — route explicit intent-loop requests
  to `intentloop.Run()`
- matching CLI tests

**What:** wire typed operator input through the existing lane surface without
changing default `plan-execute` behavior.

**Verification:** CLI tests prove default behavior is unchanged, `--intent-loop`
selects the outer loop, resume reloads the intent-loop state, invalid budgets
fail preflight, and JSON output emits intent-loop events without raw prompts.

### Slice 8: Resume, retention, and audit-preserving cleanup

**Files:**
- `internal/workflows/intentloop/resume.go`
- `internal/workflows/intentloop/cleanup.go` — `compactChildArtifacts()`,
  stale-session compaction helpers
- `internal/workflows/intentloop/cleanup_test.go`

**What:** resume incomplete child cycles using saved prompt bodies, block on
external scoped changes or child identity mismatch, compact stale loop sessions
without deleting audit evidence, and remove only duplicate/bulky transient
artifacts after terminal summaries, mutation-delta hashes, child identity, and
completion hashes are stored in the parent state.

**Verification:** tests cover in-flight review resume, prompt mismatch
prevention, child identity mismatch prevention, stale session compaction,
child terminal evidence retention, and safe cleanup of transient spill files.

### Slice 9: Audit and end-to-end regression

**Files:**
- `internal/capabilities/workspace/harness_audit.go` and tests, if audit needs
  new intent-loop counters
- CLI e2e fixtures under the existing plan-execute test surface

**What:** expose sanitized intent-loop evidence in audit output and add an e2e
regression that exercises the real CLI contract: first cycle leaves a
recoverable verification or threshold gap, second cycle closes it, terminal
state is completed, and retained evidence explains both cycles.

**Verification:** focused package tests, CLI e2e fixture, `harness_audit`
behavior output checks for cycle counts/gap classes without raw prompt or file
body leakage.

## 12. Edge Cases and Safeguards

| Scenario | Handling |
|----------|----------|
| Planner returns source-satisfied but requirements still fail | Source-satisfied only applies within a single plan-execute cycle. If contract requirements remain unmet, the loop continues. The next planner sees the remaining requirements and must either produce edits or report a blocker. The cycle prompt's `## Instructions` block clarifies: "source-satisfied from a prior cycle does not satisfy remaining contract requirements." |
| Planner reports a blocker | The loop terminates with `blocked` status. The blocker message is preserved. |
| Executor fails entirely (no edits applied) | The cycle is recorded with the failure. If the failure is a source-sanity or verification failure, it becomes a gap for the next cycle. If it's a plan coverage failure (missing required paths), the next planner sees it as a remaining requirement. |
| Review/repair produces corrective edits that introduce new failures | The new failures appear in the terminal record; gap analysis picks them up. They become the next cycle's remaining requirements. |
| Changed paths from cycle N overlap with cycle N+1 | The compacted cycle history shows cumulative changed paths. The instructions state: "target only the remaining gaps." The planner has read tools available and must re-read file bodies before editing — summaries are not a substitute. |
| Empty cycle (no edits, no failures, but requirements unmet) | Gap analysis detects the unmet requirements. If the failure set matches a prior cycle, convergence detection triggers. If the planner keeps producing empty plans, the seen-set catches it on the second occurrence. |
| Budget exhausted mid-cycle | The orchestrator checks budget after each cycle, not during. A cycle that starts with remaining budget completes, then the loop checks. A per-cycle soft cap (`total_budget / remaining_cycles`) emits `idiom.intent_loop.cycle_budget_warning` when exceeded; it is advisory in v1 because killing an in-flight child would require a separate cancellation contract. |
| Process dies mid-cycle (crash during plan-execute) | Pre-dispatch recording writes the cycle as `"dispatched"` with the plan-execute session ID before calling `planexecute.Run()`. On resume, the loop detects the incomplete cycle, reloads the saved cycle prompt byte-for-byte, and re-runs the cycle (or resumes from the plan-execute review/repair phase if a progress state exists). |
| Scope contract drift from cycle prompt text | The scope contract is frozen once from the ORIGINAL prompt plus typed CLI scope flags and passed to every cycle via `opts.ScopeContract` with `ScopeExtractionPolicy=ScopeExtractionFrozen`. The cycle prompt, including prior history and remaining requirement paths, is not parsed for scope. |
| Verification commands not in PlanVerification | `RequiredTestCommands` from the contract are injected as `ExplicitCommands` into `VerificationPlannerInput`, ensuring plan-execute always runs contract-required commands even when prose extraction and path inference miss them. |
| Acceptance evidence IDs nil on cycle failure | When a cycle fails, `finalizePlanExecuteRunResult` nils `AcceptanceEvidenceIDs`. Gap analysis treats all `RequiredAcceptanceIDs` as `"missing"` in this case, which is correct — a failed cycle cannot have satisfied acceptance obligations. |
| Threshold claim says `Met: true` but value is below threshold | The harness parses the structured claim value and computes the threshold comparison. The claim fails and becomes a recoverable acceptance gap. |
| Oscillating failure sets (A→B→A→B) | The seen-set convergence check catches any repeat, not just consecutive repeats. A→B→A triggers when the second A appears. |
| Changing error content across cycles | The convergence fingerprint includes a hash of each gap's `Detail` (error body). Different line numbers or error messages produce different fingerprints, so genuine progress (different errors) is not falsely flagged as stagnation. |
| Large error output in remaining requirements | Error output in `EvidenceGap.Detail` is truncated to 2000 characters per gap for prompt display, while `EvidenceGap.DetailHash` preserves a full-detail SHA-256 for convergence. The cycle prompt's `## Remaining Requirements` section shows only the most recent cycle's gaps, not the full cumulative history. |
| Child artifact accumulation across cycles | When the intent loop completes, the parent state snapshots and hashes child evidence, then removes only duplicate/bulky transient artifacts. Terminal completion and progress evidence remain available for the retention window. |
| User edits scoped files between cycles or before resume | The loop compares scoped fingerprints and stops with `blocked_external_scoped_change` instead of continuing against mixed authority. |
| Child cycle resumes under different tools, model profile, scope, or workspace root | Resume compares `ChildRunIdentity` fields against the current dispatch identity and stops with `blocked_child_identity_mismatch`. It does not rebuild a "close enough" child because that would mix evidence across different authority boundaries. |
| Cycle history grows large enough to need compaction | Only prior-cycle assistant/tool evidence summaries are compacted. Original intent, typed contract text, frozen scope, required commands, acceptance obligations, child IDs, mutation deltas, and evidence hashes remain structured state and are re-rendered verbatim or from typed fields. |
| Multiple writer calls are produced in one child cycle | Plan-execute remains responsible for its internal writer ordering, but the outer loop records mutation deltas in child-emitted order and treats permission/scope/write-authority failures as blockers rather than recoverable gaps. |

## 13. Open Questions

1. **Should the intent loop also cover `idiom implement` (direct mutation)?**
   Not in this design. `implement` is single-shot by contract (the model owns
   the edit sequence). An intent loop around `implement` would be a different
   design because there's no plan artifact to inspect between cycles.

2. **How do coverage thresholds interact with the verification plan?**
   Coverage is modeled as a verification command (`go test -cover ./...`) plus
   an acceptance obligation with a threshold (`">=80%"`). The verification
   command must exit 0 (the test suite passes). The model inspects the coverage
   output and declares an `AcceptanceEvidenceClaim` with the measured value
   (e.g., `Value: "72.4%"`). The harness parses that structured value and
   computes whether it satisfies the contract `Threshold`. The harness never
   parses the raw `go test -cover` output.

3. **Should the loop allow the scope contract to expand between cycles?**
   Initially no — the scope contract is fixed from the prompt. A future
   extension could allow the planner to request authorized-new-files or
   expanded paths, but this adds significant complexity to the contract
   validation.

4. **Interaction with `--skip-review-repair`?**
   When review-repair is skipped, the loop still checks verification and
   acceptance evidence. Skipping review is a per-cycle decision that doesn't
   affect the outer loop's requirements.

5. **Per-cycle token budget guard?**
   The budget is checked after each cycle. To prevent a single cycle from
   consuming all remaining budget, a per-cycle soft cap is computed as
   `total_budget / remaining_cycles`. If a cycle exceeds this cap by >2×,
   `idiom.intent_loop.cycle_budget_warning` is emitted. This is advisory, not
   a hard kill — the cycle completes, but the operator and audit output can
   detect disproportionate spend without reading raw logs. Hard cancellation
   can be added later as a separate child-run cancellation contract.

6. **What if the model declares `Met: true` but the value clearly doesn't
   meet the threshold?**
   The harness rejects the claim for threshold purposes. `Met` is audit context,
   not authority. The authoritative result is the deterministic comparison of
   `Value` against `Threshold`.
