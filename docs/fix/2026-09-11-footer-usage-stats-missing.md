# 2026-09-11 Footer 累计 usage 统计整段消失（↑↓R W CH $ 全缺）

- **日期**：2026-09-11 12:53 CST（取自修复落盘的文件 mtime）
- **状态**：✅ 已修复，回归测试锁定，全仓 `go test ./... -race` 全绿
- **涉及文件**：
  - `codingagent/modes/interactive/components/footer.go`（修复：单边指针断言 → 双形态兼容）
  - `codingagent/modes/interactive/components/footer_usage_test.go`（新增，4 个回归测试函数）
- **触发场景**：用户日常自用（dogfooding）发现交互模式 footer 左下角缺 token 统计——pi 显示 `↑58k ↓2.9k R32k CH95.5% 3.1%/1.0M (auto)`，pig 只显示 `3.0%/1.0M (auto)`；右下角模型名 `• medium` 正常。

## 现象（用户报告）

- footer 第二行（stats line）里，**累计 usage 统计的 `↑input ↓output RcacheRead WcacheWrite CH% $cost` 全部不显示**，只剩上下文百分比段 `3.0%/1.0M (auto)` 和右对齐的模型名。
- 新会话第一条消息后即复现，与 provider（volcengine-plan glm-5-3-flash）无关；且会话里 `3.0%` 有值说明 usage 数据本身在——只有 footer 的遍历统计拿不到。

## 根因：transcript 消息双形态 vs footer 单边指针断言

pig 的 transcript（`SessionManager` entries）里 `SessionMessageEntry.Message` 存在**两种形态**：

1. **值形态** `types.AssistantMessage` —— runtime 主路径：`agent_session.go handleAgentEvent` 用 `event.Message.(types.AssistantMessage)`（值断言）确认角色后，把 `event.Message` **原样**交给 `AppendMessage`（`session_manager.go:1513`），装进接口的就是值拷贝。
2. **指针形态** `*types.AssistantMessage` —— print mode / json 往返等路径。

这一事实在 `core/usage_totals.go` 的 `GetUsageCostBreakdown` 注释里有明文（"pig's transcript holds both value messages (runtime appends) and pointer messages (jsonl decode), so both shapes are accepted"——该函数因此双形态都断言），`print_mode.go`、`json_event.go` 也都做了双形态兼容。

而 `footer.go` 的 `Render` 在遍历 entries 算 usageTotals 时，移植自 TS 的动态属性访问被错误地映射成了**只认指针**的类型断言：

```go
if assistant, ok := messageEntry.Message.(*types.AssistantMessage); ok {  // 永远失败！
    core.AddUsageToTotals(&usageTotals, assistant.Usage)
    ...
}
case types.RoleToolResult:
    if toolResult, ok := messageEntry.Message.(*types.ToolResultMessage); ok && toolResult.Usage != nil {  // 同样永远失败
```

→ 值形态消息全部漏算 → `usageTotals` 恒零 → `Input/Output/CacheRead/CacheWrite > 0` 与 `Cost > 0` 的显示条件全不成立 → `↑↓R W CH $` 整段消失。

**旁证（为什么 `3.0%` 还有值）**：`GetContextUsage`（`agent_session.go:4486` 附近）走的路径与 `handleAgentEvent` 同为**值断言**，所以上下文百分比正常——同一批消息，值断言的地方有数据、指针断言的地方没数据，现象完全咬合。

这是同族问题的第三例：T5b 时 `assistant_message.go` 值类型 Content 断言失败渲染空（见 `2026-09-10-tui-streaming-empty-partial-content.md` 例 ②）、`GetUsageCostBreakdown` 移植时踩过并修过（注释为证），footer 是漏网的第四处单边断言。全仓排查结论见下「同类点位排查」。

## 修复

`footer.go` 两处断言改为先指针后值的**双形态兼容**（与 `GetUsageCostBreakdown`/`print_mode.go` 同一写法），顺带对齐 pi 的一处语义：pi 的 `latestCacheHitRate` 是**每条 assistant 都重新赋值**（`latestPromptTokens > 0 ? rate : undefined`），pig 原代码只在 >0 时赋值、不清零——补了 `else { latestCacheHitRate = nil }`（零 prompt tokens 的最后一条 assistant 会隐藏 CH 段，pi 语义）。

## 回归测试锁定

`footer_usage_test.go`（4 个测试函数）：

- `TestFooterUsageStatsFromValueTypedMessages` —— **本 bug 的直接回归**：值形态 assistant（input 58k / output 2.9k / cacheRead 32k / cacheWrite 1k / $0.05）→ stats line 断言含 `↑58k ↓2.9k R32k W1.0k CH35.2% $0.050 3.0%/1.0M (auto)`。修复前跑此测试精确复现用户症状（`missing "↑58k"`）。
- `TestFooterUsageStatsFromPointerTypedMessages` —— 指针形态同断言（守住既有路径不回退）。
- `TestFooterUsageStatsMixedMessageShapes` —— 值 + 指针 + 值形态 toolResult 混合，跨形态累加正确（`↑1.6k ↓160 $0.060`）。
- `TestFooterCacheHitRateClearedByZeroPromptAssistant` —— 锁 latestCacheHitRate 的 pi 语义：最后一条零 prompt tokens 的 assistant 清掉 CH，cache 总量段（R500/W500）仍在。

注：既有 `footer_width_test.go` 的 helper `createFooterSession` 造的全是**指针形态** entries，与指针断言恰好匹配——这就是 bug 存活至今、测试全绿的原因。新测试不再复用该 helper，显式按形态构造。

## 同类点位排查（全仓 grep `Message.(types.` / `Message.(*types.`，非测试代码）

| 点位 | 形态 | 结论 |
|---|---|---|
| `components/footer.go:191/201` | 指针 | **本 bug，已修双形态** |
| `modes/print_mode.go:259-261` | 双形态 | ✅ 已兼容 |
| `modes/json_event.go:68-73` | 双形态 | ✅ 已兼容 |
| `core/agent_session_runtime.go:403` | 指针 + fallback | ✅ 指针失败回落整体 `extractUserMessageText`，功能不丢 |
| `core/agent_session.go:977`（lastAssistantMessage 追踪） | 值 | ✅ agent 事件流消息恒为值形态 |
| `core/agent_session.go:4486`（GetContextUsage） | 值 | ✅ 当前 decode 亦产出值形态（`types.DecodeMessage` 返回值），成立 |
| `core/session_manager.go:551/1450/2156` | 值 | ✅ 同上（fileEntries 经 `DecodeMessage`，值形态） |
| `tree_selector.go:377/481` | 值 | ✅ 同上 |

防御性说明：`ai/types.DecodeMessage` 当前返回**值形态**（`var m AssistantMessage; return m, ...`），所以读 fileEntries 的值断言现状成立；但若未来 decode 改指针形态，上表「值断言」点位会集体翻车。双形态兼容是既定方针（见 §3.4 线格式/形态一致性实践），新增读 transcript 的代码一律按「先指针后值」或 switch 双 case 处理。

## 复现与验证

```bash
# 回归测试（含修复前复现：git stash footer.go 后跑第一条应 FAIL missing "↑58k"）
go test ./codingagent/modes/interactive/components/ -run 'TestFooter' -count=1 -v

# 全仓验收
go build ./... && go vet ./... && gofmt -l . && go test ./... -race -count=1
```
