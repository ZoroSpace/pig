# 2026-09-11 Markdown 表格 CJK 渲染三连 bug（错位边框 / 字符丢失 / 幻影空行）

- **日期**：2026-09-11 12:16-12:20 CST（取自各修复落盘的文件 mtime）
- **状态**：✅ 已修复，回归测试锁定，全仓 `go test ./... -race` 26 包全绿
- **涉及文件**：
  - `tui/marked.go`（修复 `reReplaceAll`，核心根因）
  - `tui/markdown.go`（修复 `renderTable` 分隔线缺一条 `─`）
  - `tui/markdown_table_regression_test.go`（新增，4 个回归测试函数）
- **触发场景**：用户在交互模式让模型「列一个表格，说每天坚持写日记的 5 个好处」，CJK 表格渲染出现三处异常（会话 JSONL 中原文完好，纯属渲染侧 bug）。

## 现象（用户报告）

1. **表格边界没对齐**：每条行分隔线 `├─…─┤` 比顶边框 `┌─…─┐` / 底边框 `└─…─┘` **短一列**（最后一列少一条 `─`），右边缘 `┤` 与 `┐`/`┘` 错位一格。
2. **两个字没显示**：`有助于放松心态` 渲染成 `有助于放??心态`（`松` 变成两个 U+FFFD 替换符）。
3. **多出一行空白行**：5 个好处渲染出 6 行——表格末尾多出一条分隔线 + 一行全空内容行。

## 根因

### 根因 1（bug 2 与 bug 3 的共同源头）：`reReplaceAll` 混用 rune 索引与字节索引

`tui/marked.go` 的 `reReplaceAll`（JS `String.replace(re, repl)` 的 /g 等价物）直接把 regexp2 的 `m.Index` / `m.Length` 当 **字节偏移** 做 Go 字符串切片：

```go
b.WriteString(s[pos:m.Index])   // m.Index 是 rune 索引！
pos = m.Index + m.Length
```

但 **regexp2 的 `Match.Index`/`Length` 是 rune 索引**（内部把输入转成 `[]rune` 处理）。实证：输入 `"abc你好世界\nend\n"`（20 字节 / 12 rune），匹配末尾 `\n` 返回 `Index=11`（rune 偏移），而字节偏移是 19——`s[11:]` 直接切进 `世` 的字节中间。

于是：任何匹配点前面出现过多字节字符时，切片就切在多字节字符内部 → 产生非法 UTF-8；后续 `for range` 迭代把非法字节转成 U+FFFD → 用户看到 `放??心态`。

**bug 3（幻影空行）由此派生**：marked 的表格 tokenizer 在拆行前用 `other.tableRowBlankLine`（`/\n[ \t]*$/`）剥掉组 3 末尾的换行。该 replace 在 CJK 行上失败并错位删字——被破坏的重组串**保留了末尾 `\n`**（删的是串中间的字节），`strings.Split(…, "\n")` 因此多出一个空串 → `markedSplitCells("")` 补齐成 3 个空 cell → 一行全空的幻影行。同时 cell 文本在 lexer 层就已损坏（`情绪��式`），这就是为什么窄终端 wrap 后 `�` 出现在行首。

注意：em/strong/del 路径用 `s.Index` 配 `jsSlice`（rune 切片）是**一致且正确**的；只有 `reReplaceAll` 一处混用。pi 侧免疫——JS 字符串索引天然就是码元单位。该函数被 21+ 处调用（每个表格 cell 的 `SlashPipe`、list 的 `ListReplaceTask`、codespan 的换行替换等），全是 CJK 下的潜在损坏点，一处修复全部消除。

### 根因 2（bug 1）：分隔线移植时丢了一条 `─`

pi 的 markdown.ts：

```ts
const separatorLine = `├─${separatorCells.join("─┼─")}─┤`;   // 注意 ─┤
```

pig 的移植写成了 `"├─" + strings.Join(separatorCells, "─┼─") + "┤"`——**少了 `┤` 前的 `─`**。顶/底边框（`─┐`/`─┘`）没丢，所以分隔线恒比它们短一列；渲染管线随后把短行右补空格到全宽，补齐的是尾部空格，`┤` 的位置仍偏左一格。

## 修复

1. **`tui/marked.go` `reReplaceAll`**：先从字符串本身构建 rune→byte 偏移表（`for i := range s`，非法字节按 1 rune 计，与 regexp2 内部 `[]rune` 转换语义一致），每个匹配的起止 rune 索引经 `byteIndexOf` 转成字节偏移后再切片；零长匹配前进分支同步改为字节制（直接 `utf8.DecodeRuneInString`）。纯 ASCII 行为不变（rune 偏移 == 字节偏移）。
2. **`tui/markdown.go` `renderTable`**：分隔线改为 `"├─" + join + "─┤"`，与 pi 逐字对齐。
3. bug 3 无需独立改动——根因 1 修复后 `tableRowBlankLine` 正确剥尾换行，行数恢复正确。

## 回归测试（`tui/markdown_table_regression_test.go`）

- `TestReReplaceAllRuneOffsets`：根因 1 的聚焦单测——`tableRowBlankLine` 剥 CJK 尾换行、CJK 后转义管道（SlashPipe）、CJK 后全局替换、CJK 后 `$1` 组引用，4 个子测试均断言结果正确且 UTF-8 合法。
- `TestMarkdownTableNoPhantomEmptyRow`：bug 3 锁定——表格末行带 `\n` 时 lexer 行数恰为写入行数（ASCII / CJK / 表格后接段落三形态），且无空 cell。
- `TestMarkdownTableBorderWidthsAligned`：bug 1 锁定——宽度 40/60/80/120 下所有边框线（顶/分隔/底）可见宽度与 `─` run 结构完全一致，且最后一条分隔线之后紧跟非空内容行（顺带覆盖 bug 3 的渲染面）。
- `TestMarkdownTableCJKCellsIntact`：bug 2 锁定——宽度 80（触发 wrap）与 200（不 wrap）下输出无 U+FFFD、UTF-8 全合法；宽终端下关键短语逐字在场。

## 排查过程中的误导点（备忘）

- 首次复现时曾怀疑 `WrapTextWithAnsi` 在 wrap 处切坏 rune——实际损坏在 lexer 阶段就已发生，wrap 只是把它暴露出来。
- 行分隔线的 `─` run 计数（`[6 14 55]` vs 顶边框 `[6 14 56]`）一开始看似 `columnWidths` 不一致，实为顶边框 `─┐` 的尾 `─` 并入末 run、分隔线没有所致——计数差恰好是缺失的那条 `─`。
