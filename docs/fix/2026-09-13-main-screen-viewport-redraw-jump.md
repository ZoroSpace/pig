# 2026-09-13 主屏渲染视口上方变更触发全量重绘 → 终端跳顶回卷（pi #9255）

- **日期**：2026-09-13 10:40
- **状态**：已修复收口（commit `2326d62`，2026-09-13 推送 master）
- **涉及文件**：`tui/tui_main_screen.go`（doRender 的 `firstChanged < prevViewportTop` guard）、`tui/tui_main_screen_viewport_test.go`（新增回归测试）
- **上游对照**：pi issue [#9255](https://github.com/earendil-works/pi/issues/9255)（2026-09-06 立，open）；pi v0.85.1 之后 `packages/tui/src/tui-main-screen.ts` 零改动（`git log d981de122..origin/main` 实证），无官方修复可移植，本修复为 **pig 独立偏差**（结构保真规则的显式豁免，见下）

## 现象

用户报告（pi 与 pig 均有）：运行期间终端有时整个跳到会话最上面，然后快速滚回最下面；tmux 里更容易触发。同族历史反馈：pi #5023（2026-05-26，auto-close 后讨论延续）、#6702（tmux zoom 全量回放）、#5748/#5749（/reload 回放）。

## 根因

`TuiMainScreen.doRender()` 差分渲染路径的守卫：

```go
if firstChanged < prevViewportTop {
    fullRender(true)  // ESC[2J ESC[H ESC[3J + 重绘整个 newLines
    return nil
}
```

该守卫假设「首个变更行在视口上方 ⇒ 增量绘制不可能 ⇒ 全量重绘」。但视口上方的行位于 scrollback（物理上不可见），重绘它们既不可能也无必要。一旦流式 live 块（thinking 尾部/工具输出）高度超过视口，其顶边（= firstChanged）恒在视口上方，于是**几乎每帧**都走 `fullRender(true)`：清屏 + 清 scrollback + 从头重印整个 buffer。pi #9255 的实测：长会话 6273/6279 次全量重绘（99.9%）走此路径，3–5 次/秒。

**可见效果**即「跳顶 + 快速回卷」：cursor 先被 `\x1b[H` 归顶（看到会话开头），随后整段内容带 `\r\n` 重印、超出视口的部分逐行滚动，直到回到底部。

**tmux 更易触发的原因**：① tmux 不支持 DEC 2026 同步输出（pi 的全量重绘包在 `\x1b[?2026h/l` 里，支持的终端会隐藏中间帧；tmux 直接裸放，跳顶回卷全程可见，另见 #5023 下用户反馈升级 tmux 后缓解）；② tmux 的 pane zoom/分割/attach/detach/状态栏变化频繁产生宽高变化，各触发一次全量重绘（widthChanged/heightChanged 路径），加剧体感。

## 修复（按 #9255 提案适配 pig，三分支）

替换原守卫为：

1. **`lastChanged < prevViewportTop`（全部变更都在 scrollback）**：屏幕零差异 → 完全跳过绘制，仅推进簿记（`previousLines`/kitty ids/`cursorRow`/`maxLinesRendered`/`previousViewportTop`）。若变更区间含完整 kitty 图像块，先按 id 删除（delete-by-id 不依赖 cursor，块各行全在 scrollback 故不影响屏幕），其余情况零输出。
2. **`len(newLines) >= len(previousLines)`（内容稳定或增长、变更跨入视口）**：`firstChanged = prevViewportTop`，只重绘可见窗口。视口上方的行在 scrollback 中保持旧内容——**优于 pi 现状**（pi 的 `3J` 会把整个 scrollback 清空）。`appendStart` 在此分支不可达（纯 append 时 `firstChanged == len(previousLines) > prevViewportTop`，矛盾），注释已声明。
3. **内容收缩且变更始于视口上方**：保留 `fullRender(true)`（视口需上滑，相对移动无法表达；罕见：大块折叠，如工具结果完成）。防止修过头，由专门测试锁定。

跳过分支的安全性论证（写测试时推演过）：`lastChanged < prevViewportTop` ⇒ 视口范围内所有行 old == new，物理屏幕内容与新 buffer 的 `[prevViewportTop..]` 区段逐行一致，`previousViewportTop` 不动则不变式保持；该条件下若 buffer 收缩，被删的尾部行必为空串（diff 语义推导），空白行位置无关，仍成立。

## 回归测试

`tui/tui_main_screen_viewport_test.go`（pig 独有，4 函数，8 行视口 + 20 行 buffer 的人工场景，`RenderNow` 同步驱动 + `FullRedraws()` 计数 + `VirtualTerminal.GetWrites()/GetViewport()` 断言）：

- `TestMainScreenNoFullRedrawStormWhenChangesStartAboveViewport`：#9255 风暴形态（live 块顶边在视口上方，每帧改顶边 + 追加一行，连续 5 帧）——断言零全量重绘、零 `ESC[2J`、最终视口显示最新 8 行
- `TestMainScreenScrollbackOnlyChangeSkipsPainting`：变更全在 scrollback → 零绘制（无 `2J`/`2K`）、屏幕不动；后续视口内变更仍按新簿记正确重绘
- `TestMainScreenBlockRebuildAboveViewportRepaintsVisibleRange`：等长 rebuild → 钳位重绘，视口内容逐行断言
- `TestMainScreenShrinkSpanningViewportStillFullRenders`：收缩场景保留全量重绘（防修过头）

**stash 验证**：修复落盘前先跑测试，前三个用例 FAIL（storm：count 1→2；scrollback-only：count 1→2；clamp：count 1→2）、防过修用例 PASS；落修复后 4/4 PASS。修复方式即 stash 该文件 diff 复跑，非事后补测。

## 验收

- `go test ./tui/ -run TestMainScreen -race` 4/4 PASS
- 全仓 `go test ./... -race -count=1 -timeout 600s` rc=0，28 包全绿（输出存 `/tmp/pig-test-out.log`）
- `gofmt -l tui/`、`go vet ./tui/` 干净

## 与 pi 的偏差声明

- `tui/tui_main_screen.go` 此 guard 的行为与 pi 源码**有意分叉**（pi 上游未修）：pig 三分支化，pi 单分支 `fullRender(true)`。pi 侧若后续合并 #9255 修复（或其变体），基线同步批应对齐本文件——pi 采纳的写法与本实现的语义差异需在同步时比对（预期 pi 会采纳同方向的 clamp/skip 方案）。
- 跳过分支中 kitty 图像的按 id 删除为 pig 侧补充细节（#9255 提案未涉及 kitty 簿记）；普通文本路径零输出。

## 第二轮：收缩跨视口分支仍在风暴（工具折叠触发，同日补修）

第一轮收口后真机验收时，长工具输出（heredoc commit message，40 行 live 面板）完成折叠后再次风暴。定位：第一轮刻意保留的分支 3（内容收缩且变更始于视口上方 → `fullRender(true)`）恰恰是**工具完成的必经路径**——live 输出面板高于视口时，其顶边（firstChanged）恒在视口上方，折叠即触发全量重绘。第一轮的两个纯文本长流测试（纯增长）探不到它；`clearOnShrink` 已排除（settings/env 默认均不开）。

**修法：`repaintVisibleWindow`（窗口重绘）替代分支 3 的 `fullRender(true)`**——只重绘可见视口各行（`\x1b[2K` + 内容），窗口滑到收缩后 buffer 底部（编辑器可见），相对移动定位（不归顶）、不清屏不清 scrollback（无 `2J/3J`）、不重印上方历史。关键洞察：整窗重绘可以任意重定位视口（包括上滑），无需真正“反向滚动”。kitty 图像行为：视口内含图像行则回退 `fullRender(true)`（Image 组件 Phase 8 前罕见）；视口上方 scrollback 中的图像 id 保留在簿记中以免孤儿化。尾部纯删除（firstChanged 落在视口内）仍走既有增量删除路径，无需滑窗。

测试更新：`TestMainScreenToolCollapseRepaintsWindowNotFullRender`（40 行面板折叠为 3 行卡片，断言无 `2J/3J/H`、scrollback 逐行保留、视口 = 新 buffer 底部 8 行、后续追加仍增量）+ `TestMainScreenShrinkSpanningViewportRepaintsWindow`（替代原“收缩保留全量”防过修测试，新语义：收缩改走窗口重绘）。两者在修复前复现 FAIL（`2J+H+3J` 后从 row-00 整段重印，与用户所见一致），修复后 PASS。

修复后流路径上仅余的正当全量重绘：首次渲染、宽高变化、clearOnShrink（默认关）、kitty 图像视口溢出回退。

## 用户验证与行为取舍

- **验证**：`cd ~/Projects/pig && git pull && go build -o /tmp/pig ./cmd/pig && /tmp/pig`。此前易触发场景——tmux 小 pane 里跑长会话 + 流式输出——不再跳顶回卷。**真机验收记录**（2026-09-13，两轮）：
- 第一轮修复后：两段纯增长长流式输出（1x/3x commit message 长度）无跳顶，但随后一次含 40 行工具输出的折叠再次风暴——暴露分支 3（见第二轮段），也证明纯增长探针探不到收缩路径。
- **二轮修复后通过**：重建二进制后用 4 个超长 heredoc commit 探针复测（message 50/75/110/25 行，命令面板最高约 3 个视口高，与当时风暴形态同源且更强），流式增长 + 完成折叠 + 连续折叠 + 折叠后追加全程无跳顶回卷，用户确认「全程稳住没有跳顶」。探针 commit（`7bee776`..`c771e91`）按用户决策保留在 master 历史中，作为迭代记录。
- **刻意取舍**：钳位分支下，视口上方 scrollback 中的已变更行保持旧渲染（滚上去看时那一小段可能是旧内容）。该区域物理不可见，正常使用几乎不可感知；且严格优于 pi 现状（`3J` 把整个 scrollback 清空重放，历史浏览位置尽失）。
- **pi 本体用户**：pi 上游 #9255 仍 open，pi 里此问题依旧存在；临时规避 `pi --tui-mode fullscreen`（alternate screen 无 scrollback 回放，#5023 下有用户实测确认）。
- **溯源致谢**：修复方案直接基于 #9255 报告者的根因实测（99.9% 全量重绘路径统计）与三分支提案；pig 侧十几分钟内落地并锁定回归，依赖 T1-T4 移植时打下的 VirtualTerminal/`FullRedraws()` 测试基建。
- **排查入口备忘**：疑似再次撞到同类跳屏时，先看 `PI_TUI_DEBUG_REDRAW=1` 下的 `pi-tui-debug.log`（`fullRender:` 原因逐条记录，本修复后应只剩 first render / resize / clearOnShrink / shrink 四类正当原因；若重现高频 `clamping` 或新增原因，按新 bug 另行登记）。

## 全链闭环（本起 fix 的意义）

这个 bug 至此全链闭环：**现象 → 上游考古（#5023/#9255）→ 根因 → 三分支修复 → 回归测试 stash 锁定 → 全量 -race → 真机 dogfooding 验收 → 落档**。pi 上游还开着的问题，pig 侧已经根治。

这也是 AGENTS.md 里「dogfooding 切换」决策（2026-09-11）的第一次完整兑现——用户日常自用撞到的 bug，走 fix 协议当天闭环，pig 比 pi 多修一个坑。后续 dogfooding 撞到的 bug 照此流程处理。
