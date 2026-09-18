# 2026-09-18 autocomplete stale accept 拼坏提交文本（/hhotkeys）

- **日期**：2026-09-18（SLASH-COMMANDS-COMPLETION 批 tmux 冒烟中发现，当批修复；文件 mtime 为准）
- **状态**：已修复收口（pig-only hardening，pi 上游仍带同型竞态）
- **涉及文件**：`tui/editor.go`（三处 accept 点 + 新 `autocompleteAcceptIsCurrent`）、`tui/editor_stale_accept_test.go`（回归测试）

## 现象

tmux 冒烟中 `send-keys '/hotkeys' Enter`（瞬时连发）后，会话 JSONL 出现发给模型的用户消息 `/hhotkeys`——autocomplete 接受路径把 `item.Value` 拼进了已键入的 `/hotkeys` 中间。慢速键入（真人节奏）不可复现。

## 根因（机制链，pig 与 pi 同构）

1. 编辑器每次键入发起一次异步补全请求；请求完成时有 snapshot 校验（`isAutocompleteRequestCurrent`：文本/光标没变才落地 apply），但**已落地的 dropdown 状态在后续键入下不清除**——新请求 apply 被 snapshot 判废后，旧的「prefix + items」一直挂着（前缀停留在 `/hotke`，而文本已是 `/hotkeys`）；
2. Enter 在 slash 前缀下 = accept + fall-through 提交一次完成（pi editor.ts:781-800 逐字相同）；
3. accept 调 `ApplyCompletion(lines, cursorCol, item, e.autocompletePrefix)`，所有分支隐含前提 `line[cursorCol-len(prefix):cursorCol] == prefix`——stale prefix 使该反推错位：slash 分支判定失败落入 fallback，把 `item.Value`（`hotkeys`）拼在 `line[:2]`（`/h`）之后 → `/hhotkeys` → 提交。

**pi 上游同型竞态**：pi 的 accept 路径、snapshot 校验、stale dropdown 保留逻辑与 pig 逐字对应（T2/T3 test-for-test 移植），JS 微任务时序窗口更窄但窗口存在；pig 的 goroutine 调度使 apply 落地点更自由，窗口更宽、更易踩。属 pi 潜伏 bug 的忠实移植，非 pig 移植偏差。

**危害评估（为何要修）**：slash 命令只是拼坏命令名；`@文件` 补全走同一 accept 路径，stale accept 可能把错误路径静默拼进发给模型的消息——文本损坏类缺陷，且用户不可感知。

## 修复（pig-only hardening，声明偏差）

新 `Editor.autocompleteAcceptIsCurrent()`：accept 前校验 `line[cursorCol-len(prefix):cursorCol] == prefix`（各 ApplyCompletion 分支的隐含前提的显式化），stale 则拒绝本次 accept：

- **Tab**（`tui.input.tab` 分支）：拒绝后 `cancelAutocomplete`，文本原样；
- **Enter**（`tui.select.confirm` 分支）：拒绝后跳过 accept、自然落出 autocomplete 块 → 走正常 Enter 处理（chat 编辑器 = 原文提交；`DisableSubmit` 场景 = 换行，文本同样完好）；
- **OnSelect**（下拉点击，editor.go createAutocompleteList）：拒绝后 cancel + return。

正常路径（apply 时 snapshot 校验已过、文本未再变）恒满足不变式，守卫零影响——tui 全量与 pi 对齐的 autocomplete/editor 测试全部照旧通过。pi 上游未修（v0.85.1 editor.ts 无此守卫），pig 偏差已在 editor.go 注释与本文件声明。

## 回归测试

`Tui/editor_stale_accept_test.go` 两个测试函数，**确定性注入竞态后状态**（不等时序）：

- 构造：逐键键入 `/hotkeys` + flush；再手动 `applyAutocompleteSuggestions(Prefix:"/hotke")` 注入 stale 状态（正是竞态落地后的状态）；
- `TestEditorEnterWithStaleAutocompleteSubmitsRawText`：Enter 后断言提交的是原文 `/hotkeys`；
- `TestEditorTabWithStaleAutocompleteLeavesTextIntact`：Tab 后断言文本未动且 dropdown 已取消。

**红→绿验证**：修复前两测试 FAIL，且 Enter 用例失败信息正是 `submitted "/hhotkeys", want the raw typed "/hotkeys"`——与冒烟观测逐字符吻合，机制诊断坐实；加守卫后 PASS。全仓 28 包 `-race` rc=0（/tmp/pig-test-out2.log）。
