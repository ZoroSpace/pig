# 2026-09-10 CJK 相邻行内/块级数学不渲染（StartInline/StartBlock 字节-rune 索引错位）

**日期**：2026-09-10（修复落盘 mtime 为准）
**状态**：✅ 已修复
**涉及文件**：`tui/markdown.go`（`newMarkdownExtensions` 的 StartBlock/StartInline 提示函数）；回归测试 `tui/markdown_latex_test.go`

## 现象

用户在交互模式让模型写 LaTeX，发现**中文字符紧挨数学定界符时数学不渲染**（显示 raw 源码）：

- `公式：$y = x^2 + 2$` → 不渲染（raw）
- `$y = x^2 + 2$`（无前缀或 ASCII 前缀）→ 正常渲染
- CJK 段落之后的 `$$...$$` / `\[...\]` 块级数学同样不渲染
- pi 无此问题

## 根因

marked 的 inline 扩展调度机制：inlineText 文本扫描不把 `$` 当停止符，扩展靠 `startInline` 提示裁剪文本候选片段（`cutSrc = e.substring(0, p+1)`），使下一轮循环正好停在定界符上让扩展触发。

- **pi**：`source.indexOf("$")` 返回 UTF-16 索引，消费端 `substring` 同单位 → 一致，正常。
- **pig**：`strings.Index` / `FindStringIndex` 返回**字节索引**，消费端 `jsSlice(src, 0, minIdx+1)` 按 **rune** 切片。多字节前缀（如 `公式：` = 3 rune / 9 byte）使候选片段越过定界符若干字符，`$y =` 被当纯文本吞掉，扩展永不触发。

StartInline（`$`/`\(`/`\[`）与 StartBlock（`$$`/`\[`）两处同病。该错位自 markdown 批（T3，2026-09-10）引入——扩展注册与 `renderLatexFn` 无关，一直在生效；此前测试全用 ASCII 文本故未暴露。

## 修复

两个提示函数内把 `strings.Index`/`FindStringIndex` 的字节索引经 `utf8RuneCount(src[:i])` 转为 rune 索引（pi 的 UTF-16 单位在此语境与 rune 等价，符合 pig 全仓 UTF-16→rune 约定）。

## 复现

```go
md := tui.NewMarkdown(`公式：$y = x^2 + 2$`, 0, 0, theme, nil, nil)
md.Render(80) // 修复前: `公式：$y = x^2 + 2$`（raw）；修复后: `公式：y = x² + 2`
```

回归测试：`TestMarkdownRendersInlineMathAfterCJKText`（3 子场景）+ `TestMarkdownRendersBlockMathAfterCJKParagraph`。
