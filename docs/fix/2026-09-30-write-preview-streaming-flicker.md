# 2026-09-30 write 工具流式预览高频重绘 → 区域闪屏（tmux 无 DEC 2026 放大）

- **日期**：2026-09-30
- **状态**：已修复收口
- **涉及文件**：`codingagent/core/tools/renderers_write.go`（pig-only 节流门）、`codingagent/core/tools/renderers_write_throttle_test.go`（新增回归）
- **定性**：dogfooding 插批 fix（用户报告，非 pi 上游 bug——pi 单线程同构存在同样渲染序列，但 pig 按 fix 协议先行处理）

## 现象

用户报告（pig，tmux 会话）：模型输出较快时，**只有 write 工具调用那一块区域**持续闪屏；输出慢时不明显。不是 2026-09-13 修过的「整屏跳顶回卷」（`2026-09-13-main-screen-viewport-redraw-jump.md`）——本轮无全量重绘，纯区域级频闪。

## 根因

两层机制叠加：

1. **write 预览随流式参数逐 delta 重绘**。pig 的工具调用参数流式到达，每个 args delta 走 `ToolExecutionComponent.UpdateArgs` → `updateDisplay` → `writeRenderCall` → `SetText`（全预览文本重建）。流式期间 write 块内有两类行几乎每个 delta 都变：正在增长的最后一行内容、`(N more lines, M total, to expand)` 计数行（N/M 每个 delta +1）。
2. **主屏差分渲染对变更行做「`\x1b[2K` 清行 → 重写」**（`tui/tui_main_screen.go` doRender）。该序列包在 `\x1b[?2026h/l` 同步输出里，支持 DEC 2026 的终端原子显示一帧、不可见；**tmux 不透传 2026**（13 日 fix 文档已实证），每帧的擦除+重写裸放。输出慢时 delta 间隔大、肉眼无感；输出快时 delta 密度打满渲染节流（16ms，~60fps），write 区域以最高帧率持续闪烁。屏幕其余区域逐帧内容不变、差分后零重绘——与「只有 write 区域闪」完全吻合。

次要来源：args 流完瞬间 `ArgsComplete` 走 `rebuildWriteHighlightCacheFull` 整块重建高亮，与增量高亮结果可能有细微差异，整个预览块重画一帧（tmux 里可见一次整块闪）。本轮不处理（一次性，节流后体感主导项已消除）。

## 修复

`writeRenderCall` 加 **pig-only 节流门**：args 仍在流式（`!ArgsComplete`）时，距上次已应用预览不足 `writePreviewThrottleInterval`（200ms）则**整跳 cache 更新 + SetText**，直接返回原组件——文本未变，差分渲染对该区域零输出。三类路径绕过节流：

- `ArgsComplete`（最终预览含完整重高亮，立即出现）；
- 首次渲染（`hasPreview == false`）；
- `Expanded` 翻转（否则 ctrl+o 展开会有最长一个窗口期的「按键失灵」手感）。

节流被跳过的帧不推进 `WriteHighlightCache`，下次应用时既有增量路径（`HasPrefix` 检查 + append）从缓存原状继续，语义无损。效果：区域重绘上限从 ~60fps 压到 ~5fps。

**与 pi 的偏差声明**：pi 的 `writeRenderers.renderCall` 无任何节流，本修复为 pig 独立行为偏差（结构保真豁免，动机与 13 日视口修复同族——tmux 环境下的可见性）。若后续 pi 侧对预览做节流或改流式渲染策略，基线同步批需比对。

## 回归测试

`renderers_write_throttle_test.go`（3 函数，seam：`writePreviewThrottleInterval` / `writePreviewClock` 包级 var，测试内换装 + Cleanup 恢复）：

- `TestWritePreviewPartialUpdatesThrottled`：窗口内 +50ms 的 delta 不改预览文本（`Render` 输出逐字节一致）；越过窗口后下一个 delta 正常应用；
- `TestWritePreviewArgsCompleteBypassesThrottle`：窗口内 +10ms 的 ArgsComplete 立即应用；
- `TestWritePreviewExpandBypassesThrottle`：窗口内展开翻转立即应用（计数行消失、尾部内容出现）。

**红→绿验证**：stash 源文件为编译红（测试引用 fix 引入的 seam，属预期）；另做**行为级红**——把节流门比较临时改为 `* 0`（窗口归零、节流永不触发、可编译），`TestWritePreviewPartialUpdatesThrottled` 恰在「节流帧不该更新预览」断言上 FAIL（line3 提前出现），恢复后 3/3 绿。

## 验收

- `go test ./codingagent/core/tools/ -run TestWritePreview -count=1 -timeout 60s` 3/3 PASS（修复后）+ 行为级红如上
- 全量 `go test ./... -race -count=1 -timeout 600s` 只跑一次 rc=0，30 包全绿（输出 `/tmp/pig-test-out-writeflicker.log`）
- `gofmt -l`、`go vet`、`go build ./...` 干净

**声明跳过 tmux 真 TTY 冒烟**：节流行为是时间性的，组件级 fake-clock 测试已逐帧锁定；可视效果需 tmux + 高速流式输出实时观察，留用户 dogfooding 复验（若仍有可感闪烁，下一步候选：把 read/fallback 预览的 `more lines` 计数行同节流处理，或把 200ms 调大）。

## 后续候选（未做）

- `renderers_edit.go` 的 preview 路与 `tool_execution.go` 的 `createResultFallback`（bash/read 等）有同型「计数行每帧重写」形态，本轮未观察到用户报告，不动；若 dogfooding 撞到再按本先例处理。
- ArgsComplete 整块重画一帧的消除（增量高亮终态与全量高亮对齐）收益小，不做。
