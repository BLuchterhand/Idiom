# OpenCode fixes — validation against the design doc

Pairs every numbered item in `docs/opencode-fixes-design.md` with the
test that proves it ships. Run `go test ./...` to re-verify, or the
specific `go test -run <Name>` line below for a single fix.

**Global status (2026-04-20):** `go test ./... -count=1` green across all 16
packages. `make swe-suite` PASS (35 tasks). Nothing in the design doc
is shipped without a test.

## Fix 1 — Read-before-edit invariant

| Acceptance criterion | Test |
|---|---|
| Manager records observed paths after a successful `read_file`; `HasObservedPath` returns true | [TestEditFileRequiresPriorObservationOfPath](internal/capabilities/workspace/manager_test.go) |
| `edit_file` against an un-observed path returns "first read_file this path" error | [TestEditFileRequiresPriorObservationOfPath](internal/capabilities/workspace/manager_test.go) |
| `edit_file` against an observed path passes through | [TestEditFileRequiresPriorObservationOfPath](internal/capabilities/workspace/manager_test.go) |

Run: `go test ./internal/capabilities/workspace/ -run TestEditFileRequiresPriorObservationOfPath`

## Fix 2 — Fuzzy path suggestions

| Acceptance criterion | Test |
|---|---|
| `edit_file` on a misspelled path returns `Did you mean:` with the nearest real path | [TestEditFileSuggestsNearestPathsWhenFileMissing](internal/capabilities/workspace/manager_test.go) |
| Existing behavior for `read_file` unchanged | covered by preexisting `TestExecuteReadWriteAndExploreWriteRejection` |

Run: `go test ./internal/capabilities/workspace/ -run TestEditFileSuggestsNearestPathsWhenFileMissing`

## Fix 3 — Fabrication detection

| Acceptance criterion | Test |
|---|---|
| Path-token extractor finds `internal/surfaces/tui/app/app.go:42`, skips bare words | [TestExtractFinalAnswerPathTokensFindsGoAndMarkdownPaths](internal/frontier/grounding_test.go) |
| `Reconcile` flags a path named in reply but not in evidence | [TestReconcileFinalAnswerFlagsUngroundedPaths](internal/frontier/grounding_test.go) |
| `Reconcile` accepts a reply whose only paths are in `changedPaths` | [TestReconcileFinalAnswerAcceptsGroundedPaths](internal/frontier/grounding_test.go) |
| Reply with no path tokens passes | [TestReconcileFinalAnswerAcceptsAnswerWithNoPathTokens](internal/frontier/grounding_test.go) |
| Explicit negation ("left X unchanged") is recognized | [TestReconcileFinalAnswerFlagsUngroundedPaths](internal/frontier/grounding_test.go) (also verifies `NegatedPaths`) |
| Correction message names ungrounded + actually-changed paths | [TestBuildFinalAnswerGroundingCorrectionNamesUngroundedAndChangedPaths](internal/frontier/grounding_test.go) |

Run: `go test ./internal/frontier/ -run 'TestExtract|TestReconcile|TestBuildFinalAnswerGroundingCorrection'`

## Fix 4 — Workspace overview in opening system context

| Acceptance criterion | Test |
|---|---|
| `OverviewSystemPrompt` returns a bounded string naming top-level dirs | [TestOverviewSystemPromptListsTopLevelLayout](internal/capabilities/workspace/overview_test.go) |
| Skips `node_modules`, `vendor`, `.git`, etc. | [TestOverviewSystemPromptListsTopLevelLayout](internal/capabilities/workspace/overview_test.go) |
| Empty for missing/invalid dirs | [TestOverviewSystemPromptReturnsEmptyForMissingDirectory](internal/capabilities/workspace/overview_test.go) |
| Repeated calls to `requestMessages` inject the overview exactly once | [TestRequestMessagesInjectsWorkspaceOverviewExactlyOnce](internal/surfaces/tui/app/rendering_test.go) |

Run: `go test ./internal/capabilities/workspace/ -run TestOverviewSystemPrompt` and `go test ./internal/surfaces/tui/app/ -run TestRequestMessagesInjectsWorkspaceOverviewExactlyOnce`

## Fix 5 — Summarization-based context compaction

| Acceptance criterion | Test |
|---|---|
| Below threshold: no-op | [TestMaybeCompactHistoryBelowThresholdIsNoop](internal/surfaces/tui/app/compaction_test.go) |
| Above threshold: folds oldest into a `session_digest` system message; keeps recent N verbatim | [TestMaybeCompactHistoryFoldsOldMessagesIntoDigest](internal/surfaces/tui/app/compaction_test.go) |
| Model-call failure falls back to deterministic digest preserving first user goal | [TestMaybeCompactHistoryFallsBackWhenModelCallFails](internal/surfaces/tui/app/compaction_test.go) |
| Deterministic fallback isolated-unit test | [TestFallbackSessionDigestPreservesFirstUserGoal](internal/surfaces/tui/app/compaction_test.go) |

Run: `go test ./internal/surfaces/tui/app/ -run 'TestMaybeCompactHistory|TestFallbackSessionDigest'`

## Fix 6 — Per-tool permission granularity

| Acceptance criterion | Test |
|---|---|
| Shell patterns: `rm *` → ask, `git *` → allow, `*` → allow | [TestMatchToolRuleShellPatterns](internal/capabilities/workspace/tool_rules_test.go) |
| Path patterns with `**`: `**/secrets/**` → deny, `**/*.md` → allow | [TestMatchToolRulePathPatternsWithRecursiveGlob](internal/capabilities/workspace/tool_rules_test.go) |
| Absent rules → none (legacy behavior preserved) | [TestMatchToolRuleAbsentRulesReturnsNone](internal/capabilities/workspace/tool_rules_test.go) |
| First-match-wins ordering | [TestMatchToolRuleFirstMatchWins](internal/capabilities/workspace/tool_rules_test.go) |
| Manager integration reads `config.Workspace.ToolRules` | [TestManagerEvaluateToolRuleIntegratesConfigRules](internal/capabilities/workspace/tool_rules_test.go) |

Run: `go test ./internal/capabilities/workspace/ -run 'TestMatchToolRule|TestManagerEvaluateToolRule'`

## Fix 7 — Real subagent spawning via the task tool

| Acceptance criterion | Test |
|---|---|
| Parent's `task` call launches an isolated child Runner; child runs its own model loop; parent sees only the result | [TestRunnerTaskToolSpawnsIsolatedReadOnlyChild](internal/runtime/agent/runner_test.go) |
| Write-mode child is isolated via a workbranch, parent workspace not mutated | [TestRunnerExecutesWorkbranchWriteChildTaskWithoutMutatingParent](internal/runtime/agent/runner_test.go) (pre-existing, still passes) |

Run: `go test ./internal/runtime/agent/ -run 'TestRunnerTaskToolSpawnsIsolatedReadOnlyChild|TestRunnerExecutesWorkbranchWriteChildTaskWithoutMutatingParent'`

## Fix 8 — Minimal MCP client

| Acceptance criterion | Test |
|---|---|
| Initialize + ListTools + CallTool round-trip against a fake stdio server | [TestClientInitializeListCallRoundTrip](internal/capabilities/mcp/client_test.go) |
| Registry namespaces tools as `mcp:<server>/<tool>` and dispatches Call | [TestRegistryNamespaceAndDispatch](internal/capabilities/mcp/client_test.go) |
| Unknown qualified tool returns an error | [TestRegistryRejectsUnknownQualifiedTool](internal/capabilities/mcp/client_test.go) |
| `ParseQualifiedToolName` round-trip | [TestParseQualifiedToolName](internal/capabilities/mcp/client_test.go) |

Run: `go test ./internal/capabilities/mcp/`

## Out of scope (by design)

Design doc called these out explicitly as non-goals; not regressed:

- No constraint on `write_file` (new-file creation path)
- No line-number or content fabrication detection (path-level only)
- No recursive-subagent spawning
- No MCP resources, prompts, sampling, SSE transport, or subscriptions
