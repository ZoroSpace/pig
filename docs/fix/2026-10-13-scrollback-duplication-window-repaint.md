# 2026-10-13 TUI scrollback 重复文字——shrink-spanning 滑窗重绘的物理必然副作用

- **日期**：2026-10-13 12:30
- **状态**：已修复收口（阈值方案 + 回归测试全绿）
- **涉及文件**：`tui/tui_main_screen.go`（shrink-spanning 分支加 `maxShrinkWindowSlide` 阈值）、`tui/virtual_terminal.go`（3J 仿真修正）、`tui/tui_main_screen_viewport_test.go`（collapse 测试修订为两档）、`tui/tui_main_screen_scrollback_test.go`（新增 turn-shape 回归）、`codingagent/modes/interactive/components/scrollback_duplication_integration_test.go`（新增真组件集成守卫）

## 现象

用户报告（某目录下的长会话）：TUI 渲染出现重复文字——一段「工具卡片 → 助手文本 → 工具卡片 → 长回答」在滚动历史里整段出现两遍。粘贴样本中重复区域 ≈ 100+ 行，起点恰好在 turn 的第一个 toolCall 卡片。

## 排查分叉：写入 vs 渲染

- JSONL 核对：`Now I have everything for question 10`、`sed -n '84,110p'` 等重复样本在会话文件中**均只有一份** → 纯渲染层 bug，排除写入侧。
- 事件双投递排除：`AgentSessionEventEntryAppended` 处理器只渲染 Custom/Usage/CustomMessage/Compaction 条目，普通消息全部走 MessageStart/Update/End 流式路径，无第二渲染源。
- 流式路径实证排除：真 `AssistantMessageComponent`（60 行 thinking 流式 + 30 行 text 流式 + 完成）与真 bash 工具卡（heredoc 长命令流式 + partial 输出 + 完成）在 40 行视口下逐帧断言 scrollback，**零重复**（clamp 路径只重绘可见窗口、视口不滑动，不产生重写重叠）。

## 根因

2026-09-13 #9255 修复第二轮（commit `1b73bd2`）把「收缩跨视口」分支从 `fullRender(true)` 改为 `repaintVisibleWindow`（滑窗重绘），换取工具折叠不再跳顶重放。但滑窗的语义是：**视口向上滑 k 行**（k = 收缩量），把新 buffer 底部 h 行重写到屏幕。被重写的物理行中，最上面 k 行的新内容**在 scrollback 里已经存在一份旧副本**——终端无法「反卷回」scrollback，于是重叠区在滚动历史里出现两次。

用 raw testComponent 精确复现（8 行视口，22→12 行折叠，k=10）：折叠后终端缓冲 = `buf[00..09]=row-00..09, buf[10]=card-grep, buf[11..14]=panel1-00..03(残留), buf[15..20]=row-04..09(重复!), buf[21]=card-grep(重复!), buf[22]=result-grep`。与用户粘贴的「grep 卡片 → 回答整段 ×2」同构。触发事件即 compaction 重建（本次会话 compaction 发生在提问前 9 秒，k = 整个 transcript 的收缩量）与 ctrl+o 折叠高卡片——都是 k 巨大的收缩。

这是物理必然，不是实现疏漏：原生 scrollback TUI 里「视口上滑」与「scrollback 无重复」不可兼得，唯一的出路是**不让大收缩发生**（pi 上游的答案是 `fullRender(true)` 清掉 scrollback 全量重印——无重复但视觉暴力且 #9255 风暴）。

## 修复：滑动量阈值

`tui/tui_main_screen.go` shrink-spanning 分支：

```
slide = prevViewportTop - (len(newLines) - height)
slide ≤ maxShrinkWindowSlide(8) → repaintVisibleWindow()（平滑，≤8 行边界重复不可感知）
slide > 8                        → fullRender(true)（pi 同行为；清屏+清 scrollback+全量重印，scrollback 逐行精确）
```

- 大收缩的现实来源只剩 compaction 重建 / ctrl+o / thinking 点击折叠——全部罕见且多为用户/系统一次性事件，单次重放可接受（这正是 pi 上游今天的行为）。
- 流式路径已被两个集成守卫证明不收缩（见下），不会回到 round-1 的逐帧风暴。
- `fullRender(true)` 重印整个 buffer，transcript 内容零丢失，损失仅视觉（一次跳顶重放）+ 终端滚动位置。

## 回归测试（stash 验证红→绿）

1. `tui/tui_main_screen_scrollback_test.go` `TestMainScreenTurnShapeNoScrollbackDuplication`：完整 turn 形态逐帧断言 scrollback 每行恰好一份。**修复前 FAIL**（collapse1 时 `buf[11]="panel1-00"` 重复残留），修复后 PASS。
2. `tui/tui_main_screen_viewport_test.go`：原 `TestMainScreenToolCollapseRepaintsWindowNotFullRender`（k=17 collapse 断言滑窗）拆为两档——`TestMainScreenLargeCollapseFullRendersScrollbackExact`（k=17 → fullRender，断言缓冲逐行等于新 buffer，重复杀手断言）+ `TestMainScreenSmallCollapseStillRepaintsWindow`（k=7 → 保留滑窗，断言无 2J/3J/H）。`TestMainScreenShrinkSpanningViewportRepaintsWindow`（k=3）不受影响原样通过。
3. `codingagent/modes/interactive/components/scrollback_duplication_integration_test.go`：真组件 × 真主屏集成守卫（流式消息 + bash 工具卡两条路径），防止未来流式组件学会「收缩跨视口」。测试内组件变更 + RenderNow 必须包在 `screen.RunExclusive` 里——生产由 UI 派发纪律串行化，测试直调绕过它会被 `-race` 抓（throttled 渲染 goroutine 与测试 goroutine 竞态，本轮实测撞到后加锁转绿）。
4. `tui/virtual_terminal.go`：3J 仿真从「等同 2J（保留 scrollback）」改为真 xterm 语义（清 scrollback）。pig 只在 `2J H 3J` 组合里发 3J，对 pig 用法无可观察差异；不改则 fullRender 路径的逐行断言无法成立。

验收：`go test ./... -race -count=1 -timeout 600s` 单次 rc=0，32 包全绿（`/tmp/pig-test-out-fix-dup.log`）；`gofmt -l`、`go vet` 干净。

## 行为取舍（对用户的说明）

- 折叠/compaction 后**滚上去看历史**：不再有重复文字；大收缩后 scrollback 被 fullRender 重建，内容与 buffer 逐行一致。
- 大收缩发生**当场**：一次跳顶+重放（pi 上游一直如此）；小收缩（≤8 行）保持滑窗平滑。
- 与 #9255 修复（`docs/fix/2026-09-13-main-screen-viewport-redraw-jump.md`）的关系：三分支结构保留，第三分支从「无条件滑窗」改为「小滑窗 / 大 fullRender」两档；该文档第二段的窗口重绘说明以此为准补丁。

## 附带观察（未定案，独立排查）

用户粘贴中 `└───┘` 后紧跟的字面 `[118;1:3u` 是 **kitty keyboard 协议 key release 事件**（key 118='v'，modifiers=1，event-type=3=release；pig 的 `desiredKittyKeyboardProtocolFlags=7` 含 release 上报）。pig 的分发层有 `IsKeyRelease` 过滤（`tui/tui.go:1681`），前提是序列完整到达。屏幕上出现裸 `[118;1:3u`（无 ESC 前缀）指向**序列被拆散**：ESC 被当作独立 Escape 键消费、剩余字节当文本落屏/落编辑器——疑似 stdin 分块边界的 ESC 前缀处理问题。本次未复现，不并入本 fix；再次撞到时按独立 fix 登记（抓 `PI_TUI_DEBUG`/复现按键时序）。
