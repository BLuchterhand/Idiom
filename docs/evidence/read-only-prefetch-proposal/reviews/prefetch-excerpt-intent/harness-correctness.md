[2m  ▎ thinking[0m
  · 17016 tok · in 16841 (16640 cached / 201 new) · out 175 (31 reasoning) · ¥0.0009
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md"}
  -> read_file {"path": "internal/capabilities/workspace/read_prefetch.go"}
  -> read_file {"path": "internal/runtime/agent/read_only_prefetch.go"}
[2m  ▎ thinking[0m
Now let me check one more detail — the `read_file` tool implementation for comparison, and how `resolveReadTarget` is used:
  · 23708 tok · in 21612 (17152 cached / 4460 new) · out 2096 (2007 reasoning) · ¥0.0090 · cache prefix changed: tools
  -> codegraph_context {"task": "resolveReadTarget method in read_prefetch.go and the normal readFile tool implementation for comparison"}
  ⊘ codegraph_context unknown tool "codegraph_context"
[2m  ▎ thinking[0m
  · 23835 tok · in 23729 (23680 cached / 49 new) · out 106 (21 reasoning) · ¥0.0007
  -> mcp__codegraph__search {"query": "resolveReadTarget"}
  -> grep {"pattern": "func.*resolveReadTarget"}
[2m  ▎ thinking[0m
  · 24132 tok · in 23997 (23808 cached / 189 new) · out 135 (64 reasoning) · ¥0.0009
  -> grep {"pattern": "func.*readFile\\b", "path": "internal/capabilities/workspace"}
[2m  ▎ thinking[0m
  · 24362 tok · in 24184 (24064 cached / 120 new) · out 178 (20 reasoning) · ¥0.0010
  -> read_file {"path": "internal/capabilities/workspace/manager.go", "offset": 3824, "limit": 80}
  -> read_file {"path": "internal/capabilities/workspace/manager.go", "offset": 5292, "limit": 30}
[2m  ▎ thinking[0m
我的分析已完成。以下是审查结果。

---

## 审查：Prefetch 模型面向摘录意图

**文件审查：**
- `docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md` — 意图文档
- `internal/capabilities/workspace/read_prefetch.go` — 工作区层实现
- `internal/runtime/agent/read_only_prefetch.go` — 运行时编排

---

### 结论：发现待解决 — 不批准

核心前提是正确的（仅限 prefetch 缩小范围，`read_file` 不变，相同的路径/安全检查，通过遥测报告截断），但实现与意图在关键方面存在偏差，削弱了源证据合约。

---

### 发现 #1 — 严重：大型文件被跳过，而不是被截断为摘录

**意图说：**
> 发出一个带行号的摘录，由新的面向模型的 prefetch 限制上限。
> 在摘要中说明截断情况，并注明原始行/字节数。

**实现做了：**

`read_prefetch.go:142-144`：
```go
if info.Size() > int64(limit) {
    return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "too_large"}
}
```

超过 `maxBytesPerFile`（默认 64KB）的文件会被完全静默跳过。第 152-158 行的截断代码因此成为**死代码**——在跳过检查之后，任何进入该路径的文件大小都保证 ≤ `limit`，所以 `if outputBytes > limit` 永远不会成立。

**影响：** 意图希望 prefetch 为大型源文件提供部分证据并带有截断元数据，以便模型仍然获得上下文，并可以请求 `read_file` 获取缺失的范围。相反，模型得到的证据是 *零*——大型文件被静默省略，没有任何摘录。如果 planner 的目标是这些文件，这种证据缺口可能会误导模型，使其以为路径不存在，或者在文件恰好超过 64KB 门槛时触发不必要的重试。

**修复：** 移除跳过大文件的检查（第 142-144 行），让第 152-158 行的截断生效。然后在摘要中附加原始 total_lines / total_bytes。

---

### 发现 #2 — 中等：缺少模型面向的 `read_file` 跟进提示

**意图说：**
> 附加一个显式的模型面向提示，即可以针对省略的范围进行有界限的 `read_file` 跟进。

**实现做了：**

`read_prefetch.go:168` 处的摘要模板是：
```
Read %s lines 1-%d (total lines: %d; source: prefetch)
```
带有可选的 `(truncated)`。**没有**提示告诉模型它可以请求完整的内容。

**影响：** 没有这个提示，一个不太有能力的模型（尤其是在 plan-execute 计划阶段运作的较小 Qwen 模型）可能不会意识到 `read_file` 仍然可用，或者可能假设 prefetch 内容是完整的，尽管有截断标记。意图明确要求一个可见的模型面向"安全阀"。

**修复：** 当 `truncated` 为 true 时，在摘要或输出后附加类似以下内容：
> Note: prefetch returned a partial excerpt (omitted N lines). Use read_file with a specific line range to request the full content.

---

### 发现 #3 — 低：摘要缺少原始总数

**意图说：**
> 在摘要中说明截断情况，并注明**原始行/字节数**。

**实现做了：**

摘要只报告了摘录部分的行数和行范围（"lines 1-%d (total lines: %d)"），其中 `total lines` 是摘录中的行数，而不是原始文件中的行数。当 `truncated` 为 true 时，模型不知道自原始文件被截断以来丢失了多少。

**修复：** 在摘要中包含原始总数：`"... (excerpt: %d lines, original: %d lines)"`。报告也已填充（`TruncatedFiles` 计数），但只是单个文件，没有显示规模。

---

### 观察：执行正确的部分

| 合约点 | 状态 |
|---|---|
| 通过现有的 `resolveReadTarget` 进行路径解析 | ✓ 与 `read_file` 工具相同 |
| 生成/日志文件排除 (`classifySearchFilePath`) | ✓ 与搜索工具相同的分类 |
| 二进制检测 (`bytes.IndexByte(data, 0)`) | ✓ |
| 文件计数、总字节数、每文件字节数限制 | ✓ 默认值合理（6 个文件 / 160KB / 64KB） |
| 子代理排除 (`readOnlyNamedPathPrefetchChildAgent`) | ✓ |
| 仅限只读车道门控 (`readOnlyPrefetchLane`) | ✓ |
| 通过 `TruncatedFiles` 的遥测 | ✓ 模式正确（但由于发现 #1 始终为 0） |
| `read_file`、搜索工具、路由、补全天数未改变 | ✓ |
| 通过 `MarkPathObserved` 更新路径观测状态 | ✓ |

---

### 建议的合约修复（摘要）

1. 删除 `prefetchReadFile` 中的 `info.Size() > limit` 跳过（第 142-144 行中的死截断代码）。截断应当产生摘录，而不是空值。
2. 在截断的 prefetch 摘要中添加原始行/字节总数。
3. 在截断的 prefetch 结果中添加模型面向的提示，提醒它可以发出 `read_file` 获取缺失范围。
4. 考虑添加一个回归测试，用超过默认每文件字节限制的文件来断言：(a) 返回了摘录，(b) `TruncatedFiles` 被递增，(c) 原始总大小出现在元数据中，(d) `read_file` 跟进提示出现。
  · 27615 tok · in 26024 (24320 cached / 1704 new) · out 1591 (302 reasoning) · ¥0.0054
