[2m  ▎ thinking[0m
  · 17233 tok · in 16952 (12032 cached / 4920 new) · out 281 (104 reasoning) · ¥0.0057
  -> read_file {"path": "docs/design-direct-answer-efficiency.md"}
  -> read_file {"path": "docs/evidence/idiom-efficiency-optimization/ledger.md"}
  -> read_file {"path": "internal/runtime/agent/direct_answer_surface.go"}
  -> read_file {"path": "internal/runtime/agent/direct_answer_surface_test.go"}
[2m  ▎ thinking[0m
Now let me inspect the runner for the A6 hook and check for existing uncommitted work:
  · 32185 tok · in 32016 (12032 cached / 19984 new) · out 169 (29 reasoning) · ¥0.0206 · cache prefix changed: tools
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD --stat"}
  -> grep {"pattern": "DirectAnswerSurface|direct_answer_surface", "path": "internal/runtime/agent/runner.go"}
[2m  ▎ thinking[0m
  · 32551 tok · in 32396 (32128 cached / 268 new) · out 155 (18 reasoning) · ¥0.0012
  -> read_file {"path": "internal/runtime/agent/runner.go", "offset": 310, "limit": 30}
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- internal/runtime/agent/runner.go"}
[2m  ▎ thinking[0m
Let me also check the other dirty files and get the full context around the A6 hook:
  · 34120 tok · in 33762 (32512 cached / 1250 new) · out 358 (93 reasoning) · ¥0.0026
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- internal/runtime/agent/runner_plan_then_execute_gu...
  -> read_file {"path": "internal/runtime/agent/runner.go", "offset": 220, "limit": 15}
  -> read_file {"path": "internal/runtime/agent/runner.go", "offset": 500, "limit": 25}
[2m  ▎ thinking[0m
Let me check for the `readOnlyDirectAnswerLane` helper and verify the test works:
  · 35349 tok · in 35137 (34048 cached / 1089 new) · out 212 (56 reasoning) · ¥0.0022
  -> grep {"path": "internal/runtime/agent/", "pattern": "func readOnlyDirectAnswerLane"}
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && go test ./internal/runtime/agent -run 'DirectAnswer' -v 2>&1 | head...
[2m  ▎ thinking[0m
Let me verify the gate and tool composition:
  · 36750 tok · in 36491 (35328 cached / 1163 new) · out 259 (49 reasoning) · ¥0.0024
  -> read_file {"path": "internal/runtime/agent/final_answer_acceptance.go", "offset": 130, "limit": 20}
  -> grep {"path": "internal/capabilities/workspace", "pattern": "func WorkspaceWriteToolNames"}
  -> grep {"path": "internal/capabilities/workspace", "pattern": "func RuntimeSourceInspectionToolNames"}
  · 37296 tok · in 37135 (36736 cached / 399 new) · out 161 · ¥0.0015
  -> read_file {"limit": 30, "offset": 137, "path": "internal/capabilities/workspace/tool_manifest.go"}
  -> read_file {"limit": 30, "offset": 218, "path": "internal/capabilities/workspace/tool_surface_metadata.go"}
[2m  ▎ thinking[0m
Let me look at one of the new DirectAnswer tests to understand how the existing tests interact with the A6 surface:
  · 38571 tok · in 38317 (37120 cached / 1197 new) · out 254 (81 reasoning) · ¥0.0024
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && go test ./internal/runtime/agent -run 'TestRunnerDoesNotExposeNoteT...
  -> grep {"path": "internal/runtime/agent", "pattern": "func TestRunnerDoesNotExposeNoteToolForDirectAnswer"}
[2m  ▎ thinking[0m
Let me find the test that asserts task delegation works in direct-answer mode:
  · 38874 tok · in 38688 (38528 cached / 160 new) · out 186 (25 reasoning) · ¥0.0013
  -> grep {"path": "internal/runtime/agent", "pattern": "test.*task.*direct.*answer\\|direct.*answer.*task.*child.*packet"}
  -> grep {"path": "internal/runtime/agent", "pattern": "func Test.*DirectAnswer.*ChildPacket\\|func Test.*DirectAnswer.*Task"}
[2m  ▎ thinking[0m
  · 38993 tok · in 38898 (38784 cached / 114 new) · out 95 (16 reasoning) · ¥0.0011
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && grep -n \"func Test.*DirectAnswer\" internal/runtime/agent/*_test.g...
[2m  ▎ thinking[0m
  · 39909 tok · in 39805 (38912 cached / 893 new) · out 104 (19 reasoning) · ¥0.0019
  -> read_file {"limit": 70, "offset": 16566, "path": "internal/runtime/agent/runner_test.go"}
[2m  ▎ thinking[0m
Let me check all dirty files and the full picture of the A6 diff:
  · 42106 tok · in 40989 (39808 cached / 1181 new) · out 1117 (929 reasoning) · ¥0.0042
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD --name-only && echo \"---STAGED---\" && git diff --ca...
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git diff HEAD -- prompts/assets.go internal/runtime/agent/lane_poli...
[2m  ▎ thinking[0m
Let me now verify the complete A1-on-A6 dependency: confirming A1 prefetch is disabled when the surface doesn't advertise the capability:
  · 45491 tok · in 42344 (41984 cached / 360 new) · out 3147 (2962 reasoning) · ¥0.0075
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && grep -n \"source_body_prefetch\\|DirectAnswerSurfaceCapSourceBodyPr...
  -> read_file {"limit": 15, "offset": 128, "path": "internal/runtime/agent/direct_answer_surface.go"}
[2m  ▎ thinking[0m
Let me check one more thing — whether the runner's allowed-tools override order could cause issues, and whether there are any existing tests that might break due to the surface override:
  · 46042 tok · in 45824 (45440 cached / 384 new) · out 218 (53 reasoning) · ¥0.0017
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && go test ./internal/runtime/agent -count=1 2>&1 | tail -10"}
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && go test ./... 2>&1 | tail -30"}
[2m  ▎ thinking[0m
  · 46973 tok · in 46756 (45952 cached / 804 new) · out 217 (119 reasoning) · ¥0.0022
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git stash list && git stash && go test ./internal/runtime/agent -ru...
[2m  ▎ thinking[0m
  · 47326 tok · in 47232 (46848 cached / 384 new) · out 94 (35 reasoning) · ¥0.0015
  -> bash {"command": "cd /Users/ben/Documents/GitHub/idiom && git stash pop"}
[2m  ▎ thinking[0m
Let me verify one more important detail — how the runner uses the allowed tools and whether pre-existing brief.AllowedTools interacts with the A6 override:
  · 48409 tok · in 47667 (47104 cached / 563 new) · out 742 (630 reasoning) · ¥0.0030
  -> read_file {"limit": 60, "offset": 295, "path": "internal/runtime/agent/runner.go"}
[2m  ▎ thinking[0m
现在我已获得所有证据。以下是完整的 A6 审查。

---

## A6 审查报告：轻量级直接回答表面清单

**审查者：** A6 审查者
**审核内容：** `internal/runtime/agent/direct_answer_surface.go`、`internal/runtime/agent/direct_answer_surface_test.go`、`internal/runtime/agent/runner.go` 中的 A6 钩子
**设计依据：** `docs/design-direct-answer-efficiency.md`（第 0 节 + 第 3 节）
**台账：** `docs/evidence/idiom-efficiency-optimization/ledger.md`
**脏范围被保留：** 是 — `runner_plan_then_execute_guard.go`、`prompts/assets.go`、`lane_policy_test.go`、`runner_runstate.go` 未被 A6 触及，且传递的脏数据已被保留。

---

### 裁决：PASS_WITH_RISKS

所有关键合同均已按照设计实施。A1 预取已禁用，并包含类型化缺失原因。现存测试（26 个 DirectAnswer 测试）全部通过。有两个值得注意的风险，均非阻塞性问题，记录如下。

---

### 1. 正确性

**工具表面过滤：** 正确。

- `DirectAnswerSurface` 结构体携带 `schema_version`、`version`、`capabilities`、`tool_names` 和 `terminal_states`。 ✅
- `defaultDirectAnswerSurfaceTools()` 包含只读工具：`read_file`、`search_files`、`search_content`、`code_navigate`、`context_pack`、`context_query`、`context_zoom`、`discovery_query`、`structural_search`、`file_stats`、`harness_audit`、语言运行时工具、git 历史工具、`read_only_capability` 和 `task`。 ✅
- 通过 `WorkspaceWriteToolNames()` 排除写入工具：`write_file`、`edit_file`、`append_file`、`apply_patch`、`delegate_edit`、`go_move_declarations`、`mkdir`。 ✅
- 额外排除：`submit_plan`、`task_ledger`、`note`、`shell`。 ✅
- 通过 `defaultDirectAnswerSurfaceTools()` 中的集合和去重逻辑消除重复项。 ✅
- 空白工具名称被修剪并跳过。 ✅

**闸门：** 正确。

- `DirectAnswerSurfaceForBrief()` 委托给 `readOnlyDirectAnswerLane()`，该函数检查 `PermissionReadOnlyWorkspace`、`TaskFamilyConversation` 或 `TaskFamilyDirectAnswer`，以及 `ContractDirectAnswer`（第 133-138 行 `final_answer_acceptance.go`）。 ✅
- `TestDirectAnswerSurfaceNotReturnedForWriteBrief` 验证写入权限配置文件不会获得该表面。 ✅
- 组合中的空调用：`surface.SchemaVersion == "" || surface.Version == ""` → 缺失状态（第 99 行）。 ✅

**A1/A6 预取合同：** 正确。

- `DirectAnswerSurfaceCapSourceBodyPrefetch = "source_body_prefetch"` 已定义（第 36 行），但故意**未**在 `directAnswerSurfaceCapabilities()`（第 131-137 行）中声明。注释指出“A1 必须保持禁用状态，直到在此处添加并更新版本”。 ✅
- `DirectAnswerSurfacePrefetchEligibility()` 返回三个类型化的缺失原因：
  - `direct_answer_surface_missing` — 空版本/模式版本。 ✅
  - `direct_answer_surface_stale: version=X expected=2026-06-11` — 版本不匹配。 ✅
  - `direct_answer_surface_no_source_body_prefetch_capability` — 功能不在列表中。 ✅
- 这些都在 `TestDirectAnswerSurfacePrefetchEligibilityTypedMisses` 中通过表驱动测试覆盖。 ✅
- 肯定案例在 `TestDirectAnswerSurfacePrefetchEligibilityRequiresAdvertisedCapability` 中测试。 ✅

**运行器中的 A6 钩子（`runner.go` 第 319-330 行）：** 正确。

- 钩子位于计划执行合同阶段规则之后（第 296-318 行）和 `turnHiddenTools` 过滤之前（第 331-339 行）。顺序正确：表面是工具可见性的最终授权方，然后是隐藏工具过滤。 ✅
- 调用 `DirectAnswerSurfaceForBriefTools()`；如果返回非 nil，则完全在 `allowed` 上执行 `append([]string(nil), surfaceTools...)`。这会替换，而不是合并 — 这是本意。 ✅
- 表面遥测事件每会话恰好触发一次（通过 `directAnswerSurfaceEmitted bool` 保护，第 226、324 行）。 ✅
- 仅 A6 代码更改了 `runner.go` 中的 5 行（第 226、319-330 行）。不相关的工作（`transportRetries`，第 511、740+ 行）在功能上是独立的，并作为脏范围被保留。 ✅

**表面遥测（`EmitDirectAnswerSurfaceEvent`）：** 正确。

- 触发格式为 `"direct_answer_surface"` 的事件，包含 `schema_version`、`version`、`capabilities`、`tool_count` 和 `terminal_states`。 ✅
- `TestEmitDirectAnswerSurfaceEvent` 验证形状、模式版本、表面版本、工具计数和终端状态。 ✅
- `emitFn` 参数上的 nil 保护。 ✅

---

### 2. 北极星边界

- **设计原则 1（保留策略所有权）：** 表面是类型化的运行时状态，控制工具可见性和终端状态。它不会写入答案或限制模型合成。 ✅
- **设计原则 2（使确定性状态变得廉价）：** 表面清单构成消耗该清单的 A1 预取的基础。它本身不会进行推理或读取。 ✅
- **设计原则 5（不要使提示文本成为授权方）：** 所有优化都放在类型化的运行时状态（结构体、版本常量、功能字符串）中，而不是提示提醒中。 ✅
- **第 0 节要求：** 工具表面过滤 ✅，写入工具移除 ✅，终端状态（`answerable_evidence`、`uncertainty_prose`）✅，遥测标识符 ✅。
- **“非目标”：** 未削弱证据门控，未合成答案，未使用魔法提示短语。 ✅

---

### 3. A1 预取禁用检查

设计 doc 第 0 节要求：“如果轻量级表面清单缺失、过时或未声明支持 `source_body_prefetch`，则精确路径预取必须发出类型化的禁用/缺失状态，并且正常的模型主导调查必须保持可用。”

- `source_body_prefetch` 未在 `directAnswerSurfaceCapabilities()` 中声明。 ✅
- `DirectAnswerSurfacePrefetchEligibility()` 对所有三种情况返回 `(false, typedMissReason)`。 ✅
- 三条负面测试用例全部通过。 ✅
- 肯定案例确认，当声明该功能时，预取变为合格（以便在未来的原子中启用）。 ✅

**现状：** A1 无法激活。正常的模型主导调查完全可用（该表面包含所有只读工具）。 ✅

---

### 4. 测试覆盖率

| 测试 | 覆盖 | 状态 |
|------|----------|--------|
| `TestDirectAnswerSurfaceForBriefFiltersProtocolAndMutationTools` | 工具包含/排除、只读调查功能 | ✅ |
| `TestDirectAnswerSurfaceNotReturnedForWriteBrief` | 写入权限配置文件门控 | ✅ |
| `TestDirectAnswerSurfacePrefetchEligibilityTypedMisses`（3 个子测试） | 缺失、过时、无功能 | ✅ |
| `TestDirectAnswerSurfacePrefetchEligibilityRequiresAdvertisedCapability` | 肯定预取合格 | ✅ |
| `TestEmitDirectAnswerSurfaceEvent` | 遥测事件形状 | ✅ |
| 21 个现有 DirectAnswer 测试（`runner_test.go`、`read_only_answerability_test.go`、`note_tool_test.go`、`phase_contract_test.go`、`evidence_telemetry_regression_test.go`） | 集成：遥测、任务委派、最终答案接受、阶段合同、门控 | ✅ 全部通过 |

现存失败：`TestRunnerRoutesWorkbranchChildOntoImplementerClient` — 预先存在的（使用 `git stash` + `go test` 确认），与 A6 无关，并且与 `transportRetries` 脏工作无关。不是 A6 的责任。

**任务工具是可接受的吗？** 是的。设计 doc 第 0 节没有明确禁止 `task`，并且台账记录了现有的运行时合同依赖于子包任务委派。`defaultDirectAnswerSurfaceTools()` 第 196 行包含一条注释：“保留只读任务委派用于返回子包的 direct-answer 通道；现有运行时合同依赖于它。”测试 `TestRunnerDirectAnswerReturnsChildPacketWithoutParentFollowups` 验证此行为并传递。故意允许多于禁止。

---

### 5. 发现的风险

#### 风险 1（中）：协议文本未被剥离

**发现：** A6 实现覆盖了工具清单，但**未**剥离模型面向指令中的计划者/执行者/审查者角色协议文本。设计 doc 第 0 节说“从 direct-answer 模型面向指令中移除规划者/执行者/审查者角色协议文本”。原子计划将 A6 范围列为“协议/工具表面精简”。实现只做了工具部分。

**证据：** 运行器中的 A6 钩子（`runner.go` 第 319-330 行）仅覆盖 `allowed` 工具切片。它不修改提示选择或系统消息组合。`prompts/assets.go`（清单中列为脏现有工作）未被触及。由于 `source_body_prefetch` 未声明，A1 保持禁用状态，但模型可能会收到描述 `submit_plan`、`task_ledger` 等的系统指令，即使它们已被从清单中移除。

**缓解措施：** 工具已从清单中移除；拒绝 `submit_plan` 等是不可行的调用，而不是合同违规。工具移除是北极星式的（类型化运行时状态），而协议文本更改将更具建议性。可以在后续原子（A6.1 或通过 `prompts/assets.go` 的提示精简）中处理。

**影响：** 在 A1 激活之前（该表面有正常模型主导调查），未剥离的协议文本不会导致错误输出。可能会浪费少量上下文标记。不阻塞。

#### 风险 2（低）：负面测试未覆盖非 DA 任务族

**发现：** `TestDirectAnswerSurfaceNotReturnedForWriteBrief` 更改了权限配置文件，但未测试非只读任务族（例如 `plan_execute`、`implementation`）在具有只读权限配置文件时是否会意外获得该表面。

**证据：** `readOnlyDirectAnswerLane()` 同时检查任务族和权限配置文件。使用只读权限配置文件和 `plan_execute` 任务族的 Brief 应无法通过 `readOnlyDirectAnswerTaskFamily` 检查并返回 `false`。该逻辑是正确的；只是没有测试。

**缓解措施：** 通过在测试中添加一行来低成本和低风险覆盖：`brief.TaskFamily = "plan_execute"` → `DirectAnswerSurfaceForBrief` 返回 `false`。

**影响：** 无功能风险。代码是正确的。测试覆盖缺口。

---

### 6. 脏范围审计

台账中列出的现有脏文件未受影响：

| 文件 | 状态 |
|------|--------|
| `internal/runtime/agent/runner.go` | 已为此审查准确隔离 A6 更改（5 行 + 1 行 `directAnswerSurfaceEmitted` 尾部声明）。不相关的 dirty `transportRetries` 代码（第 511、740+ 行）保持未被触及。 |
| `internal/runtime/agent/runner_plan_then_execute_guard.go` | 未被 A6 触及。 |
| `internal/runtime/agent/lane_policy_test.go` | 未被 A6 触及（新文件）。 |
| `internal/runtime/agent/runner_runstate.go` | 未被 A6 触及（新文件）。 |
| `prompts/assets.go` | 未被 A6 触及（新文件）。 |
| `.codex/README.md`、`AGENTS.md` | 未被 A6 触及。 |

---

### 7. 需要验证的命令

A6 代码的直接验证：

```
go test ./internal/runtime/agent -run 'DirectAnswer'   → PASS (5 tests)
go test ./internal/runtime/agent                       → 1 pre-existing failure unrelated to A6
```

设计第 0 节中要求的清单的正确性均已确认。A1 预期预取资格的关闭条件包括：缺失表面、版本不匹配、未声明 `source_body_prefetch` 功能 — 均已实现和测试。
  · 52765 tok · in 49237 (48384 cached / 853 new) · out 3528 (628 reasoning) · ¥0.0089
