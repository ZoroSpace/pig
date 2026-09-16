# CLIPBOARD-IMAGE-PLACEHOLDER：粘贴图片显示 `[Image N]` 占位符（opencode 风格）

- **日期**：2026-09-16（用户插入小批）
- **状态**：已交付，回归测试锁定
- **涉及文件**：`codingagent/modes/interactive/interactive_mode.go`、`codingagent/modes/interactive/interactive_mode_submit_test.go`（新增）

## 需求

用户（对照 opencode 的交互）：粘贴图片后，编辑器里显示 `[Image 1]` 而不是 `/var/folders/.../pi-clipboard-*.png` 完整临时路径；同一消息里继续粘贴则依次 `[Image 2]`、`[Image 3]`……**发给大模型的仍是完整路径**（pi 语义：路径作为文本进入消息，模型用 read 工具读图），TUI 只在显示层用占位符。

## 实现（pig-only，pi 无对应物）

三个环节：

1. **粘贴**（`handleClipboardPaste`）：落盘临时文件照旧（pi 语义不变），编辑器插入的文本从原始路径换为 `[Image N]`。路径登记进 `pendingClipboardImages`（当前消息内按粘贴序）；同时登记 `clipboardImageLabels[path] = "[Image N]"` 供 transcript 显示（会话级，首标号优先）。**N 按消息计数：每次提交后 pending 列表清零，下一条消息从 `[Image 1]` 重新计数**（opencode 语义）。
2. **提交**（`processSubmitText` 三个出消息分支——普通提交 / streaming steer / compaction 队列）：`expandImagePlaceholders` 把 `[Image N]` 还原为真实路径后再进入 inputGate / `Prompt` / 队列。**模型收到与 JSONL 持久化的都是真实路径**——会话 resume 后信息不丢失；编辑器历史记录的是占位符文本（回忆时看到的还是 `[Image 1]`）。占位符编号越界（如手打 `[Image 9]`）原样保留不展开。
3. **transcript 显示**（`addMessageToChat` 的 RoleUser 分支）：`renderClipboardImageText` 把消息文本中的已登记路径替换回 `` `[Image N]` `` 标签（**反引号包裹**，markdown 渲染为行内代码样式，与正文区分；不往 markdown 管线埋 ANSI），聊天记录里看到的也是占位符而非长路径。仅显示层替换，存储内容不动；**重启/恢复会话后标签映射为空，历史消息显示原始路径**（声明的行为边界）。
4. **编辑器着色**（用户需求「占位符换个颜色」→ 后续收敛为「用文件路径链接同色」）：`CustomEditor` 新增 pig-only `TextDecorator func(line string) string` 装饰器 seam + `Render` 覆盖（逐行后处理，SGR 零宽不碰宽度计算）；interactive 侧接线 `colorizeImagePlaceholders`，把渲染行中的 `[Image N]` 用主题 token **`imagePlaceholder`** 包裹——pig-only 可选 token（加入 `optionalThemeColors`，缺省回落 `accent`），内置 dark/light 主题取值与 `mdLink` 完全同色（dark `#81a2be` / light `blue`→`#547da7`），即用户要的「文件路径链接那种颜色」；装饰器看到的是完整渲染行（伪光标 span 已内嵌），正则为纯字面量模式，被光标 span 切开的占位符不匹配、保持原色（导航光标恰好落在占位符内部的瞬态边角，声明接受）；编辑器缓冲区始终是纯文本。

**并发面（pig 新增）**：`clipboardImagesMu` 保护两个字段——粘贴跑在独立 goroutine（`go m.handleClipboardPaste()`）、提交在 submit worker、transcript 渲染在事件分发 goroutine，三方并发访问；pi 单线程事件循环无此问题。

**测试 seam（pig 新增）**：包级 var `clipboardImageReaderFn` / `clipboardTextReaderFn`（调用时解析，先例 `utils.clipboardRunCommandFn`），测试换装 + `t.Cleanup` 还原；pi 测试 mock 模块函数的等价物。

## 与 pi 的偏差声明

- pi 粘贴图片直接插入原始路径（`interactive-mode.ts` `handleClipboardPaste` 的 `insertTextAtCursor(filePath)`）；pig 插入占位符。用户显式要求 opencode 行为。
- pi 无 pending/labels 状态、无 mutex、无提交期展开——pig-only 状态机，见上。

## 回归测试

`interactive_mode_submit_test.go`：

- `TestClipboardPasteInsertsImagePlaceholder`：粘贴 → 编辑器 `[Image 1]`（无路径字面量）+ 临时文件真实落盘 + 二次粘贴 `[Image 1][Image 2]`；
- `TestClipboardPlaceholderExpandsToPathOnSubmit`：`[Image 2] 对比一下 [Image 1]` 提交 → inputGate 收到按位展开的路径；提交后编号重启；未知编号原样透传；
- `TestTranscriptRendersImagePlaceholderLabel`：transcript 渲染 `[Image 1] 这张图里是什么`（行内代码样式）且不含原始路径；
- `TestImagePlaceholdersAreColorized`：编辑器装饰器给占位符上 accent 色（可见文本不变）；被伪光标 span 切开的占位符保持原色；`CustomEditor.Render` 集成（渲染行有色、缓冲区纯文本）。

红→绿验证：注回原实现（插原始路径 / 展开恒等 / 渲染替换恒等）分别 FAIL，修复后 PASS（与 `docs/fix/2026-09-16-clipboard-path-eaten-by-slash-command.md` 同批同法）。

## 关联

- 本批顺带修复了「`/` 开头文本被兜底分支吞掉」的存量 bug（粘贴路径必触发），见 `docs/fix/2026-09-16-clipboard-path-eaten-by-slash-command.md`。
- `MAC-CLIPBOARD-IMAGE`（macOS Ctrl+V 读剪贴板贴图）是本功能的上游：读图→落盘链路不变，只改插入文本与提交展开。
