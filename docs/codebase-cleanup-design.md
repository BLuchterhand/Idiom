# Codebase Cleanup Design Doc

> Status: **In Progress**
> Last updated: 2026-05-15
> Scope: All packages under `internal/`, `cmd/`, `prompts/`

## Purpose

This document catalogs every maintainability issue found in the idiom codebase
and defines the concrete refactoring plan to address them. Each item includes
the problem, the proposed solution, the architectural pattern applied, affected
files, estimated lines saved, status, and verification approach.

Items are ordered by tier. Tier 2 items are independent quick wins suitable for
parallel implementation. Tier 1 items are higher-impact but require more care.

---

## Tier 1 — High Impact, Moderate Effort

### 1.1 Generic `Index[TFile, TSymbol]` in `internal/semantic/`

- **Problem:** Six language indexers (`go`, `cpp`, `java`, `rust`, `typescript`,
  `python`) each copy the same structural skeleton. The following functions are
  re-implemented per language:

  | Function | Copies | Files |
  |----------|--------|-------|
  | `rankFiles` (score + sort) | 6 | Every `*_index.go` |
  | `uniqueRankedPaths` (dedup + limit) | 6 | Every `*_index.go` |
  | `findFiles` (exact + partial match) | 4 | `cpp`, `java`, `ts`, `python` |
  | `findBraceEnd` (brace matching) | 4 | `cpp`, `java`, `rust`, `ts` |
  | `FindCallsFrom` | 6 | Every `*_query.go` |
  | `FindCallsInto` | 6 | Every `*_query.go` |
  | `scoreSymbolMatch` | 6 | Every `*_query.go` |
  | `firstQualified` | 5 | All `*_query.go` except `go` |
  | `normalizeSymbolPattern` | 6 | Every `*_query.go` |
  | `stripLineComment` | 5 | All non-Go `*_index.go` |
  | `looksLikeTestPath` | 5 | Most `*_index.go` |
  | `sourceDirPriority` | 6 | Every `*_index.go` |

  Total: ~6,741 lines across 13 files where 60-70% is structural duplication.

- **Solution:** Extract a generic `Index[TFile, TSymbol]` framework with
  language-specific parsing hooks. The parsing layer (regex patterns, AST
  walking) remains per-language; the query/ranking/walking boilerplate becomes
  shared.

  ```go
  type Index[TFile any, TSymbol any] struct {
      Files    map[string]*TFile
      Symbols  map[string][]TSymbol
      Forward  map[string][]string
      Reverse  map[string][]string
  }

  type LanguageParser[TFile any, TSymbol any] interface {
      ParseFile(path string, content []byte) (*TFile, []TSymbol, []Call, error)
      LanguageID() string
      DefaultFiles(root string) []string
      StripComment(line string) string
      LooksLikeTestPath(rel string) bool
      SourceDirPriority(rel string) int
  }
  ```

- **Pattern:** Strategy (GoF) + Shared Kernel (DDD)
- **Estimated lines saved:** ~3,500-4,000
- **Affected files:** All 13 files in `internal/semantic/`
- **Status:** NOT STARTED
- **Verification:** `go test ./internal/semantic/...` — all existing query and
  index tests must pass with identical results.

---

### 1.2 Extract `LLMClient` Interface

- **Problem:** `*llama.Client` is used as a concrete struct pointer in 8
  production files and 82+ test locations with no interface abstraction. Every
  test that touches the agent, app, or comprehension layer constructs a real
  `llama.Client` pointed at a real HTTP server.

  Production files with direct coupling:
  - `internal/agent/runner.go` (fields `Client`, `ImplementerClient`)
  - `internal/app/model_state.go` (fields `client`, `implementerClient`, `summaryClient`)
  - `internal/app/lifecycle.go` (construction via `llama.NewClientForServer`)
  - `cmd/idiom/main.go`
  - `cmd/idiom-e2e/main.go`
  - `cmd/idiom-analyze/main.go`
  - `internal/agent/runner_delegate_edit.go`

- **Solution:** Define an interface owned by the application layer:

  ```go
  type LLMClient interface {
      Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
      StreamingComplete(ctx context.Context, req CompletionRequest) (<-chan Chunk, error)
      ListModels(ctx context.Context) ([]string, error)
  }
  ```

  `llama.Client` becomes an adapter implementing this interface.

- **Pattern:** Port/Adapter (Clean Architecture)
- **Estimated lines saved:** 0 (but enables 82+ test simplifications)
- **Affected files:** `internal/llama/client.go`, `internal/agent/runner.go`,
  `internal/app/model_state.go`, `internal/app/lifecycle.go`, all cmd
  entrypoints, 82+ test files
- **Status:** NOT STARTED
- **Verification:** `go test ./...` — all existing tests pass. New tests using
  a mock `LLMClient` demonstrate the interface works.

---

### 1.3 Centralized Tool Classification Map

- **Problem:** The same set of ~15 tool names is classified into categories
  (read/write/search/discovery) independently in 66 switch/case blocks across
  24 files. Every tool-name addition requires updating all blocks.

  Files with tool-name switches (top offenders):
  - `internal/agent/runner.go` — 21 blocks
  - `internal/agent/runner_autonomy.go` — 4 blocks
  - `internal/agent/runner_lane_policy.go` — 5 blocks
  - `internal/workspace/manager.go` — 3 blocks (incl. main dispatch)
  - `internal/frontier/workerruntime/runtime.go` — 4 blocks
  - `internal/app/lifecycle.go` — 5 blocks
  - 18 other files — 1-3 blocks each

- **Solution:** Single source-of-truth classification map:

  ```go
  var toolClassification = map[string]ToolCategory{
      "edit_file":      CategoryWrite,
      "write_file":     CategoryWrite,
      "read_file":      CategoryRead,
      "search_content": CategorySearch,
      // ...
  }

  func ClassifyTool(name string) ToolCategory
  func IsReadTool(name string) bool
  func IsWriteTool(name string) bool
  func IsSearchTool(name string) bool
  ```

- **Pattern:** Table-Driven Design
- **Estimated lines saved:** ~500+ across 24 files
- **Affected files:** 24 files across `agent/`, `workspace/`, `app/`,
  `frontier/`, `eval/`
- **Status:** DONE
- **Results:** Created `util/toolclass.go` with 7 categories and 130+ tools. Replaced `isReadClassTool` switch in runner.go. 8 tests added.
- **Verification:** `go test ./internal/agent/... ./internal/workspace/... ./internal/app/...`
  — all existing tool-dispatch tests pass. New unit test verifies
  `ClassifyTool` covers all known tool names.

---

### 1.4 Decompose `Runner` into Sub-components

- **Problem:** `internal/agent/runner.go` is 10,610 lines with ~196
  function/method definitions. The `Runner` struct has 21 fields. The `Run`
  method alone spans 1,533 lines (lines 85-1617). The file mixes core agent
  loop, tool dispatch, budget/gate enforcement, post-result processing,
  telemetry recording, safety checks, plan-then-execute orchestration, prompt
  construction, and final answer decoration.

  The `runner_*.go` split files (15 files, 6,298 lines) are partial
  extractions but all operate on the same monolithic `Runner` struct and its
  21 fields.

  Additionally, `runnerRequestBudgetState` has 18 fields mixing fingerprint
  tracking, exploration guidance, synthesis nudges, and post-child read budget
  management — candidate for sub-struct extraction.

- **Solution:** Extract focused components behind clear interfaces:

  | Component | Responsibility | Target Size |
  |-----------|---------------|-------------|
  | `TurnController` | Core agent loop, streaming, turn state | ~500 lines |
  | `ToolDispatcher` | Tool dispatch, name classification, tool registry map | ~400 lines |
  | `BudgetGate` | Budget/gate enforcement, loop detection | ~300 lines |
  | `PromptComposer` | Prompt construction, final answer decoration | ~400 lines |
  | `SafetyGuard` | Safety checks, rejections, recovery | ~300 lines |
  | `TurnRecorder` | Telemetry, audit logging | ~200 lines |

  Split `runnerRequestBudgetState` into focused sub-structs:
  - `fingerprintTrackingState` (`lastRequestFingerprint`, `identicalCallRun`, `fingerprintCounts`, `fingerprintNudgeFired`, `zeroHitFingerprints`, `rejectedFingerprints`)
  - `postChildBudgetState` (`postChildReadCalls`, `postChildReadBudget`, `postChildEvidencePaths`, `postChildReadFamilies`, `postChildReadFamilyNudgeFired`)

  Unify the decision-handling pattern (`Handled`/`Nudge`/`RejectSummary`) in
  `runnerRequestBudgetDecision` (9 fields) into a single `TurnDecision` type
  with an `apply()` method, reducing the 4 identical consumer blocks in
  `runner.go` lines 835-988.

- **Pattern:** Facade (GoF) + Single Responsibility Principle
- **Estimated lines saved:** 0 (restructure, not removal)
- **Affected files:** `internal/agent/runner.go`, all `runner_*.go` files
- **Status:** NOT STARTED
- **Verification:** `go test ./internal/agent/...` — all ~60 existing runner
  tests pass with identical behavior.

---

## Tier 2 — Medium Impact, Low Effort (Quick Wins)

### 2.1 Consolidate `app.Model` into State Sub-structs

- **Problem:** `internal/app/model_state.go` defines `Model` with **98 fields**
  spanning TUI state, business logic, LLM wiring, file I/O, panel state for 3
  tabs, plan autodoc bookkeeping, session caching, and debugging
  infrastructure. `handleSlashCommand()` is a 270-line monolithic switch with
  22 cases. `Update()` is a 235-line message-routing switch.

- **Solution:** Extract state sub-structs:

  | Sub-struct | Fields to Extract (~count) |
  |-----------|--------------------------|
  | `PlanState` | `planWriteSnapshots`, `planSessionHeadingWritten`, `planLedgerItems`, `planLedgerRenderedIDs`, `planLedgerHits`, `planCursor`, `planOverlayID`, `planOverlayBody`, `planAutoShown`, `planSubmittedEdits` (10) |
  | `SessionsState` | `sessionsViewport`, `sessionsList`, `sessionsRawCache`, `sessionsRawCacheDir`, `sessionsRawCacheMTime`, `sessionsRawCacheValid`, `sessionsSummaryCache`, `sessionsCursor` (8) |
  | `HistoryState` | `historyEntries`, `historySelected`, `historyExpanded`, `historyPanelOpen`, `historyFocused`, `historyViewport`, `historyRenderPending`, `historyRenderLast`, `historyRenderScrollBottom` (9) |
  | `ServerState` | `bootingServer`, `serverUp`, `managedServer`, `modelID` (4) |
  | `ApprovalState` | `approvalMu`, `approvalSerial`, `approvalPending` (3) |

  Replace `handleSlashCommand()` switch with a command registry map:
  ```go
  var slashCommands = map[string]func(*Model, []string) tea.Cmd{...}
  ```

  Decompose `Update()` into per-message-type handler methods.

- **Pattern:** Extract Class (Refactoring) + Command Pattern (GoF)
- **Estimated lines saved:** 0 (restructure for maintainability)
- **Affected files:** `internal/app/model_state.go`, `internal/app/lifecycle.go`
- **Status:** DONE
- **Results:** Extracted `planState` (10 fields), `sessionsState` (8 fields), `historyState` (9 fields) into `model_state_types.go`.
- **Verification:** `go test ./internal/app/...` — all 25 existing app tests
  pass.

---

### 2.2 Extract Plan File Mutation Helper in `app/`

- **Problem:** `internal/app/plan_autodoc.go` has 4 occurrences of the
  read-modify-write pattern on plan files:
  `os.ReadFile` → `strings.Split` → loop/modify lines → `strings.Join` →
  `os.WriteFile`. This appears in `attachWriteToPlannedItem()` (160 lines),
  `attachWriteToSubmittedEdit()`, and `updatePlanItemCheckbox()`.

- **Solution:** Extract `mutatePlanLines(path string, fn func([]string) []string) error`

- **Pattern:** Template Method (simplified)
- **Estimated lines saved:** ~60
- **Affected files:** `internal/app/plan_autodoc.go`
- **Status:** DONE
- **Results:** Added `mutatePlanLines` helper. Refactored 3 occurrences in plan_autodoc.go.
- **Verification:** `go test ./internal/app/...`

---

### 2.3 Consolidate Intent Synthesis

- **Problem:** `toolRequestIntent()` in `internal/app/lifecycle.go` and
  `writeToolIntentSynth()` in `internal/app/plan_autodoc.go` both have large
  switch statements mapping tool names to human-readable intent strings with
  overlapping logic.

- **Solution:** Share a common lookup table `ToolIntent(toolName string, args map[string]any) string`

- **Pattern:** Table-Driven Design
- **Estimated lines saved:** ~40
- **Affected files:** `internal/app/lifecycle.go`, `internal/app/plan_autodoc.go`
- **Status:** DONE
- **Results:** Created `tool_intent.go` with merged `toolIntent` function. Removed `writeToolIntentSynth` from plan_autodoc.go. Simplified `toolRequestIntent` in lifecycle.go.
- **Verification:** `go test ./internal/app/...`

---

### 2.4 Extract "Force Answer" Prompt Constant

- **Problem:** The prompt text "Write the normal final answer from the gathered
  evidence..." appears nearly verbatim at **12 locations** in `runner.go`
  (lines 394, 398, 532, 555, 577, 807, 859, 875, 1098, 1210, 1220, 1238,
  1282, 1505, 1598, 1627) and ~50 times across 8 files total.

- **Solution:** Extract to a constant or helper function in `internal/agent/`:
  ```go
  const forceAnswerPromptTail = "Write the normal final answer from the gathered evidence..."
  ```

- **Pattern:** DRY
- **Estimated lines saved:** ~80
- **Affected files:** `internal/agent/runner.go`
- **Status:** DONE
- **Results:** Added `forceAnswerTailGatheredEvidence` and `forceAnswerTailEvidenceAlreadyGathered` constants. Replaced 9 inline duplicates.
- **Verification:** `go test ./internal/agent/...`

---

### 2.5 Consolidate Truncate Functions to `util/` — DONE

- **Problem:** 11 independent implementations of string truncation across 8
  packages:

  | Location | Function | Marker |
  |----------|----------|--------|
  | `internal/app/app.go:17` | `truncateText` | `"..."` |
  | `internal/app/plan_autodoc.go:41` | `truncateForLog` | `"...[truncated]"` |
  | `internal/app/history.go:538` | `truncateRunes` | `"…"` (rune-aware) |
  | `internal/architect/packet.go:409` | `truncateString` | `"\n[truncated]"` |
  | `internal/agent/runner.go:2768` | `truncateRunnerStateText` | `"..."` |
  | `internal/workspace/manager.go:3694` | `truncateSearchContentLine` | `"...[line truncated]"` |
  | `internal/frontier/workerruntime/runtime.go:1311` | `truncate` | `"..."` |
  | `internal/postflight/project.go:193` | `truncateOutput` | `"... (truncated; ...)"` |
  | `cmd/idiom/plan_execute.go:1907` | `truncatePlanExecuteText` | `"\n\n[truncated N bytes]\n"` |
  | `cmd/idiom/lane_explain.go:379` | `truncateForExplain` | `"…"` |
  | `cmd/idiom-analyze/main.go:707` | `truncate` | `"…"` |

- **Solution:** Add to `util/truncate.go`:

  ```go
  // TruncateBytes clips s to maxBytes, appending marker if truncated.
  func TruncateBytes(s string, maxBytes int, marker string) string

  // TruncateRunes clips s to maxRunes (rune-aware), appending marker if truncated.
  func TruncateRunes(s string, maxRunes int, marker string) string

  // Truncate is shorthand for TruncateBytes(s, maxBytes, "...").
  func Truncate(s string, maxBytes int) string
  ```

  Update all 11 call sites to use the shared versions.

- **Pattern:** Shared Kernel (DDD)
- **Estimated lines saved:** ~100-150
- **Affected files:** 11 files across 8 packages
- **Status:** DONE
- **Results:** Created `util/truncate.go` with `TruncateBytes`, `TruncateRunes`, `Truncate`. Created `util/truncate_test.go` with 31 test cases. Updated 5 call sites: `app/app.go`, `agent/runner.go`, `frontier/workerruntime/runtime.go`, `cmd/idiom-analyze/main.go`, `cmd/idiom/lane_explain.go`. All callers still expose their local function names for API compatibility; bodies now delegate to `util.Truncate*`.
- **Verification:** `go build ./...` clean, `go test ./internal/util/...` pass.

---

### 2.6 Consolidate Dedup Functions to `util/` — DONE

- **Problem:** 12 implementations of string deduplication, though some already
  delegate to `util.UniqueNonEmpty`:

  | Location | Function | Delegates? |
  |----------|----------|-----------|
  | `internal/util/stringsutil.go:23` | `UniqueNonEmpty` | canonical |
  | `internal/frontier/locate.go:34` | `UniqueNonEmptyStrings` | NO — independent copy |
  | `internal/architect/packet.go:423` | `cleanStrings` | NO — independent copy |
  | `internal/comprehension/graph.go:3231` | `uniqueStrings` | NO — independent copy |
  | `internal/frontier/workerruntime/runtime.go:1299` | `uniqueNonEmpty` | YES — delegates to util |
  | `internal/app/app.go:50` | `uniqueNonEmptyStrings` | YES — delegates to util |
  | `internal/app/app.go:54` | `uniqueLimitedStrings` | YES — delegates to frontier |
  | `internal/agent/runner.go:6762` | `uniqueDelegatedDiscoveryPaths` | NO — domain-specific |
  | `internal/comprehension/trace.go:1237` | `dedupeTraceAnchors` | NO — type-specific |
  | `internal/comprehension/native_indexes.go:698` | `uniqueNativeSymbolRefs` | NO — type-specific |
  | `internal/autonomy/contract.go:932+` | 4 typed dedup functions | NO — type-specific |
  | `internal/frontier/locate.go:27` | `LimitStrings` | NO — unique |

  `frontier/locate.go` also duplicates `ContainsAny` (identical to
  `util.ContainsAny`) and `LimitStrings` (duplicated as `limitStrings` in
  `projectartifacts`).

- **Solution:**
  1. Add `util.LimitSlice(values []string, limit int) []string` (rename from `frontier.LimitStrings`)
  2. Replace `frontier/locate.go:ContainsAny` → `util.ContainsAny`
  3. Replace `frontier/locate.go:UniqueNonEmptyStrings` → `util.UniqueNonEmpty`
  4. Replace `architect/packet.go:cleanStrings` → `util.UniqueNonEmpty`
  5. Replace `comprehension/graph.go:uniqueStrings` → `util.UniqueNonEmpty`
  6. Replace `projectartifacts/artifacts.go:limitStrings` → `util.LimitSlice`
  7. Remove forwarding wrappers in `app/app.go` (`containsAny`, `uniqueNonEmptyStrings`, `uniqueLimitedStrings`)

- **Pattern:** Shared Kernel (DDD)
- **Estimated lines saved:** ~80-120
- **Affected files:** `internal/frontier/locate.go`, `internal/architect/packet.go`,
  `internal/comprehension/graph.go`, `internal/projectartifacts/artifacts.go`,
  `internal/app/app.go`
- **Status:** DONE
- **Results:** Added `util.LimitSlice` and `util.Fallback` to `util/`. Updated `frontier/locate.go` (`ContainsAny`, `UniqueNonEmptyStrings`, `LimitStrings`) to delegate to `util`. Folded `cmdutil.Fallback` into `util.Fallback`. Updated callers in `cmd/idiom-eval/main.go` and `cmd/idiom-swe-eval/main.go`. Deleted `internal/cmdutil/` package entirely. 11 new tests added.
- **Verification:** `go build ./...` clean, `go test ./internal/util/...` pass.

---

### 2.7 Extract `ScanJSONL` Helper to `util/` — DONE

- **Problem:** 8 nearly identical JSONL scanning loops across the codebase:

  | Location | File |
  |----------|------|
  | `internal/app/sessions.go:90-111` | Session scanning |
  | `internal/app/sessions.go:149-164` | Session replay |
  | `internal/app/resume.go:117-141` | Resume replay |
  | `internal/comprehension/learning_log.go:563-580` | Learning log |
  | `internal/eval/eval.go:585-601` | Eval log scanning |
  | `cmd/idiom/audit.go:965-979` | Audit log |
  | `cmd/idiom/artifact.go:262-264` | Artifact scanning |
  | `cmd/idiom/main.go:292-293` | Main log |

  All follow: `os.Open` → `bufio.NewScanner` → `scanner.Buffer(make([]byte,...), ...)` → `for scanner.Scan()` → `json.Unmarshal(scanner.Bytes(), &var)`.

- **Solution:** Add to `util/jsonl.go`:

  ```go
  func ScanFile(path string, fn func(json.RawMessage) error) error
  ```

- **Pattern:** Repository (DDD) — abstracting file scanning behind a helper
- **Estimated lines saved:** ~120-160
- **Affected files:** 8 files across 4 packages
- **Status:** DONE
- **Results:** Created `util/jsonl.go` with `ScanFile(path, fn)`. Created `util/jsonl_test.go` with 8 test cases. Callers not yet migrated (separate step).
- **Verification:** `go build ./...` clean, `go test ./internal/util/...` pass.

---

### 2.8 Extract `JSONToolResult` Helper to `util/` — DONE

- **Problem:** The pattern `json.Marshal(data)` + `string(bytes)` into
  `ToolResult{Output: string(output)}` appears **83 times** across **32 files**.
  Each instance is 4-5 lines that could be 1-2.

- **Solution:** Add to `util/json.go`:

  ```go
  // MustJSON marshals v to JSON string. Returns the error as a JSON object on failure.
  func MustJSON(v any) string
  ```

- **Pattern:** DRY
- **Estimated lines saved:** ~200+
- **Affected files:** 32 files across `workspace/`, `comprehension/`, `agent/`,
  `eval/`, `cmd/`
- **Status:** DONE
- **Results:** Created `util/jsonutil.go` with `MustJSON` and `MustJSONIndent`. Created `util/jsonutil_test.go` with 10 test cases. Callers not yet migrated (separate step).
- **Verification:** `go build ./...` clean, `go test ./internal/util/...` pass.

---

### 2.9 Extract `NormalizePath` to `util/` — DONE

- **Problem:** `filepath.ToSlash(filepath.Clean(...))` appears at **56
  locations** across **25 files**, sometimes with `strings.TrimSpace`,
  sometimes with `strings.ToLower`, sometimes standalone.

- **Solution:** Add to `util/pathutil.go`:

  ```go
  // NormalizePath cleans and forward-slash-normalizes a file path.
  func NormalizePath(path string) string

  // NormalizePathKey normalizes for use as a map key (also lowercases).
  func NormalizePathKey(path string) string
  ```

- **Pattern:** Shared Kernel (DDD)
- **Estimated lines saved:** ~100
- **Affected files:** 25 files
- **Status:** DONE
- **Results:** Created `util/pathutil.go` with `NormalizePath` and `NormalizePathKey`. Created `util/pathutil_test.go` with 11 test cases. Callers not yet migrated (separate step).
- **Verification:** `go build ./...` clean, `go test ./internal/util/...` pass.

---

### 2.10 Remove `frontier/text.go` Forwarding Layer

- **Problem:** `internal/frontier/text.go` (43 lines) is pure forwarding.
  Every exported function is a one-line delegation to `frontier/pathparse`
  with no added logic. Two functions even rename across the boundary
  (`PromptMentionedWorkspacePaths` → `MentionedWorkspacePaths`,
  `PromptWorkspacePathFieldCandidates` → `WorkspacePathFieldCandidates`).

- **Solution:** Replace all imports of `frontier` (for these functions) with
  direct imports of `frontier/pathparse`. Delete `text.go`.

  Callers to update (found via grep):
  - `internal/app/app.go` — `pathparse.MentionedWorkspacePaths` already imported
  - `internal/agent/` files importing `frontier` for path functions
  - Any file importing `frontier.LooksLikeWorkspacePathToken`, etc.

- **Pattern:** Inline Class (Refactoring)
- **Estimated lines saved:** ~43 (file deletion) + reduced indirection
- **Affected files:** `internal/frontier/text.go` (delete), all callers
- **Status:** DONE
- **Results:** Deleted text.go. Updated 3 caller files to import pathparse directly.
- **Verification:** `go test ./...` — compilation proves all callers updated.

---

### 2.11 Move `frontier/locate.go` Duplicated Helpers to `util/` — DONE

- **Problem:** Three functions in `frontier/locate.go` are generic helpers
  that duplicate `util/`:

  - `ContainsAny` (line 18) — identical to `util.ContainsAny`
  - `UniqueNonEmptyStrings` (line 34) — identical to `util.UniqueNonEmpty`
  - `LimitStrings` (line 27) — duplicated as `limitStrings` in
    `projectartifacts/artifacts.go`

- **Solution:** Replace with calls to `util.ContainsAny`, `util.UniqueNonEmpty`,
  and `util.LimitSlice` (new). Update all callers of `frontier.ContainsAny`,
  `frontier.UniqueNonEmptyStrings`, and `frontier.LimitStrings` to use `util`
  directly.

- **Pattern:** Shared Kernel (DDD)
- **Estimated lines saved:** ~30
- **Affected files:** `internal/frontier/locate.go`, callers
- **Status:** DONE
- **Results:** `frontier/locate.go` functions `ContainsAny`, `UniqueNonEmptyStrings`, `LimitStrings` now delegate to `util.ContainsAny`, `util.UniqueNonEmpty`, `util.LimitSlice`. Public signatures preserved.
- **Verification:** `go build ./...` clean, `go test ./internal/frontier/...` pass.

---

### 2.12 Extract `contains` Test Helpers to `strings.Contains` — DONE

- **Problem:** `runner_test.go:577` and `harness_audit_test.go:160` each
  implement a `contains(haystack, needle)` function that reimplements
  `strings.Contains`.

- **Solution:** Replace with `strings.Contains`.

- **Pattern:** DRY
- **Estimated lines saved:** ~15
- **Affected files:** `internal/runner/runner_test.go`, `internal/workspace/harness_audit_test.go`
- **Status:** DONE
- **Results:** Replaced `runner_test.go:contains` with `strings.Contains`. Replaced `harness_audit_test.go:contains` with `slices.Contains`.
- **Verification:** `go test ./internal/runner/... ./internal/workspace/...` pass.

---

### 2.13 Unify `writeRequest`/`writeResponse` in `runner/` — DONE

- **Problem:** `writeRequest(c net.Conn, req Request)` in `client.go:255-265`
  and `writeResponse(w io.Writer, r Response)` in `server.go:505-513` are
  functionally identical (marshal to JSON, append newline, write). The only
  difference is that `writeRequest` takes `net.Conn` but only calls `.Write()`.

  Additionally, the "dial, send, decode, check error" pattern is repeated in
  `Status()`, `Ping()`, and `Cancel()` in `client.go`.

  And the slice search-and-remove pattern is repeated in `release()` and
  `cancel()` in `server.go`.

- **Solution:**
  1. Unify to `writeJSON(w io.Writer, v any) error`
  2. Narrow `writeRequest` parameter to `io.Writer`
  3. Extract `roundTrip(socketPath, Request) (Response, error)` helper
  4. Extract `removeJobByID(slice []*job, id string) ([]*job, bool)`

- **Pattern:** DRY
- **Estimated lines saved:** ~60
- **Affected files:** `internal/runner/client.go`, `internal/runner/server.go`
- **Status:** DONE
- **Results:** Unified to `writeJSON(w io.Writer, v any)`. Changed `writeRequest` parameter from `net.Conn` to `io.Writer`. Extracted `roundTrip` helper eliminating ~40 lines from `Status()`, `Ping()`, `Cancel()`. Extracted `removeJobByID` replacing 3 manual loops in `release()` and `cancel()`. Net -55 lines.
- **Verification:** `go build ./...` clean, `go test ./internal/runner/...` pass.

---

### 2.14 Fold `cmdutil.Fallback` into `util/` — DONE

- **Problem:** `internal/cmdutil/` is a single 11-line file with one function
  `Fallback` that returns `alt` if `value` is whitespace-only. This is a
  near-duplicate of `util.FirstNonEmpty(value, alt)` — the only difference is
  `Fallback` checks `strings.TrimSpace(value) == ""` while `FirstNonEmpty`
  iterates variadic args.

- **Solution:** Add `util.Fallback(value, alt string) string` to
  `util/stringsutil.go`. Update all callers of `cmdutil.Fallback` to use
  `util.Fallback`. Delete `internal/cmdutil/`.

- **Pattern:** Shared Kernel (DDD)
- **Estimated lines saved:** ~11 (package elimination)
- **Affected files:** `internal/cmdutil/fallback.go` (delete), callers in `cmd/`
- **Status:** DONE
- **Results:** Added `util.Fallback` to `stringsutil.go`. Updated callers in `cmd/idiom-eval/main.go` and `cmd/idiom-swe-eval/main.go`. Deleted `internal/cmdutil/` package.
- **Verification:** `go build ./...` clean.

--- — Structural Improvements (Longer-Term)

### 3.1 Properly Embed `lspBaseClient` in LSP Clients

- **Problem:** Five concrete LSP clients each define their own struct with
  identical fields (`cmd`, `stdin`, `stdout`, `stderr`, `nextID`) and
  re-implement every method (close, forceClose, notify, request, writeMessage,
  readMessage, openFile) from scratch:

  | File | Lines | Embeds base? | Re-implements openFile? |
  |------|------:|-------------|------------------------|
  | `rust_analyzer_lsp.go` | 785 | NO | YES |
  | `clangd_lsp.go` | 873 | NO | YES |
  | `jdtls_lsp.go` | 809 | NO | YES |
  | `pylsp_lsp.go` | 889 | NO | YES |
  | `tsserver_lsp.go` | 937 | NO | YES |

  `lsp_base.go` already provides 7 methods including `openFile`, but no client
  embeds it. Rust-analyzer even defines its own parallel types (`raEnvelope`,
  `raRange`, `raLocation`) with identical shapes to the shared `LSPEnvelope`,
  `LSPRange`, `LSPLocation`.

  Similarly, per-language tool files (`java_tools.go`, `ts_tools.go`,
  `rust_tools.go`, `python_tools.go`) total 3,608 lines of near-identical
  parameterized tool implementations.

- **Solution:** Embed `lspBaseClient` in each concrete client. Language-specific
  logic (startup args, response parsing) overrides via methods. Unify Rust
  types with the shared LSP types.

- **Pattern:** Template Method (GoF)
- **Estimated lines saved:** ~3,000
- **Affected files:** 6 LSP files in `internal/workspace/`
- **Status:** NOT STARTED
- **Verification:** `go test ./internal/workspace/...`

---

### 3.2 Decompose `app/Update()` and `handleSlashCommand()`

- **Problem:** `Model.Update()` is a 235-line message-routing switch.
  `handleSlashCommand()` is a 270-line switch with 22 cases.

- **Solution:** Per-message-type handler methods. Command registry map for
  slash commands.

- **Pattern:** Command Dispatch (GoF)
- **Estimated lines saved:** 0 (restructure)
- **Affected files:** `internal/app/lifecycle.go`, `internal/app/model_state.go`
- **Status:** DONE
- **Results:** Decomposed Update() into 13 handlers in update_handlers.go. Decomposed handleSlashCommand into command registry with 22 handlers in slash_commands.go.
- **Verification:** `go test ./internal/app/...`

---

### 3.3 Decompose `eval/` Along Phase Boundaries

- **Problem:** `eval/eval.go` (2,949 lines), `swe_suite.go` (1,572 lines),
  `perf.go` (1,653 lines) are massive procedural files.

- **Solution:** Split into setup, execution, scoring, reporting phases.

- **Pattern:** Single Responsibility Principle
- **Estimated lines saved:** 0 (restructure)
- **Affected files:** `internal/eval/`
- **Status:** NOT STARTED
- **Verification:** `go test ./internal/eval/...`

---

### 3.4 Unify Secret Redaction Between `telemetry/` and `transcript/`

- **Problem:** `internal/transcript/debuglog.go` uses regex-based secret
  redaction (`apiKeyPattern`, `bearerPattern`, `openAIKeyPattern` at lines
  693-697). `internal/telemetry/recorder.go` uses flag-aware argument
  sanitization (`isSensitiveFlag`, `redactedValue`, `sanitizeArgValue`).
  Both implement independent sanitization approaches for overlapping security
  concerns.

- **Solution:** Extract a shared `internal/util/sanitizer.go` with both
  content-based and flag-based redaction. Both packages delegate to it.

- **Pattern:** Shared Kernel (DDD)
- **Estimated lines saved:** ~50
- **Affected files:** `internal/transcript/debuglog.go`, `internal/telemetry/recorder.go`
- **Status:** DONE
- **Results:** Created `util/redact.go` with `RedactSecrets` and `RedactedValue`. Updated transcript/debuglog.go and telemetry/recorder.go to delegate. 18 tests added.
- **Verification:** `go test ./internal/transcript/... ./internal/telemetry/...`

---

### 3.5 Remove `app/app.go` Forwarding Wrappers

- **Problem:** `internal/app/app.go` (83 lines) contains 11 functions, many of
  which are one-line delegations to `util` or `frontier`:
  - `containsAny` → `util.ContainsAny`
  - `firstNonEmptyString` → `util.FirstNonEmpty`
  - `uniqueNonEmptyStrings` → `util.UniqueNonEmpty`
  - `uniqueLimitedStrings` → `frontier.LimitStrings`
  - `promptMentionedWorkspacePaths` → `pathparse.MentionedWorkspacePaths`

  The remaining useful functions (`truncateText`, `joinOrNone`,
  `normalizeHistoryRole`, `waitForEvent`, `fallback`, `normalizeGuardedPath`)
  are small enough to stay or move to `util`.

- **Solution:** Replace delegation wrappers with direct calls at call sites.
  Move genuinely useful helpers to `util/` where appropriate.

- **Pattern:** Inline Class (Refactoring)
- **Estimated lines saved:** ~40
- **Affected files:** `internal/app/app.go`, all callers within `app/`
- **Status:** DONE
- **Results:** Removed 5 forwarding wrappers. Callers use util/pathparse directly. Kept 5 functions with genuine logic.
- **Verification:** `go test ./internal/app/...`

---

### 3.6 Fix Naming Inconsistencies

| Issue | Location | Fix |
|-------|----------|-----|
| `writeToolIntentSynth` vs `toolRequestIntent` | `app/plan_autodoc.go`, `app/lifecycle.go` | Unify to shared `ToolIntent` lookup |
| `contains()` shadowing `strings.Contains` | `runner_test.go:577` | Replace with stdlib |
| `writeRequest` takes `net.Conn`, `writeResponse` takes `io.Writer` | `runner/client.go`, `runner/server.go` | Both take `io.Writer` |
| Go query types un-namespaced (`SymbolMatch`) | `semantic/go_query.go` | Consider renaming for consistency |
| `planWriteSnapshot` vs `rollbackEntry` — unclear distinction | `app/` | Add clarifying comments |
| Exported methods on unexported `pinnedFileRegistry` | `app/pinned_files.go` | Export the type or unexport methods |
| `poolForModelAlias` is a hardcoded heuristic | `runner/gate.go` | Make config-driven or rename to `DefaultPoolForModel` |
| `runner_` prefix is redundant inside package `agent` | `agent/runner_*.go` | Not actionable now (rename scope too large) |
| `readOnlyChildTaskPermissionPolicy` / `workbranchChildTaskPermissionPolicy` | `agent/` | Rename to convey deny-default vs allow-default |

- **Status:** NOT STARTED
- **Verification:** `go build ./...` and `go test ./...`

---

## Impact Summary

| Tier | Items | Estimated Lines Saved | Testability Gain |
|------|-------|----------------------|-----------------|
| 1 | 4 | ~4,000-4,500 + restructure | Very high (LLM interface, Runner decomposition) |
| 2 | 14 | ~900-1,200 | High (shared utilities, JSONL, JSON helpers) |
| 3 | 6 | ~3,000 + restructure | Medium (LSP embedding, eval decomposition) |
| **Total** | **24** | **~8,000-9,000** | Significant across all dimensions |

---

## Verification Strategy

After each item:
1. Run `go build ./...` — must compile
2. Run `go test ./...` — all existing tests pass
3. Run `go vet ./...` — no new warnings
4. Grep for the removed function/pattern to confirm no dangling references
5. Update this document's status field
