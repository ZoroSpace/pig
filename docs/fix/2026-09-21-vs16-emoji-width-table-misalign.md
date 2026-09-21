# VS16 emoji 宽度算宽一格 → markdown 表格边框错位

日期：2026-09-21 15:26（文件 mtime）；状态：已收口；涉及：tui/utils.go、tui/grapheme_width_vs16_test.go

## 现象

用户 dogfooding 中发现 agent 回复里的 markdown 表格有一行与其他行边框错位（右边界偏移 1 格）。该行是全表唯一含 `⏸️`（U+23F8 U+FE0F）的行；含 `❌`（U+274C）的行正常。

## 根因

`tui/utils.go` 的 `graphemeWidth` 对所有 RGI emoji 恒返回 2（pi string-width 启发式的忠实移植）。但 `⏸️`/`⚠️`/`❤️`/`☀️` 这类「EAW neutral/ambiguous 基字符 + VS16」的 emoji 呈现序列，在严格 EAW 终端（tmux/VTE 默认 ambiguous=narrow）下只渲染 **1** 格：

- pig 表格渲染按 `VisibleWidth` 补空格，`⏸️` 算 2 → 多补 1 格 → 该行比分隔线宽 1 格，错位。
- 旁证：`─`（U+2500，EAW=ambiguous）在用户终端按 1 格渲染（分隔线行对齐），`❌` 基字符 EAW=wide 两类终端都渲染 2 格（该行对齐）——与「用户终端 = strict-EAW/ambiguous-narrow」完全自洽。
- pi 同型行为（`rgiEmojiRegex` 命中即 2），属 pig-only 加固点而非移植偏差。

## 修复

`tui/utils.go`：emoji 分支新增 `isNarrowEmojiPresentation` 判定——簇恰好为 `[base, U+FE0F]` 两码位且 `eastAsianWidth(base)==1` 时落入常规 EAW 宽度（=1）；宽基（❌）、keycap（#️⃣）、ZWJ 序列（👨‍👩‍👧）、RI 旗帜对（🇺🇸）不受影响仍为 2。代价：在 emoji-aware 宽渲染终端（kitty/iTerm2 类）这类字符会反向差 1 格，取舍以本机终端为准（pig 唯一真实消费者），已注释声明。

## 回归测试

`tui/grapheme_width_vs16_test.go` `TestGraphemeWidthVS16EmojiPresentation`：10 个 graphemeWidth 单簇用例 + 1 个 VisibleWidth 复合串用例。修复前 4 例红（2 != 1），修复后全绿；`go test ./tui/ -count=1` 全包绿（36s，无既有测试 pin 旧值）。另以临时探针验证 markdown 表格端到端渲染各行 VisibleWidth 全等。

## 涉及文件

- `tui/utils.go`（graphemeWidth + isNarrowEmojiPresentation）
- `tui/grapheme_width_vs16_test.go`（新增）
