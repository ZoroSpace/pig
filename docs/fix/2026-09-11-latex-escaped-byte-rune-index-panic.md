# 2026-09-11 行内数学 `latexIsEscaped` byte/rune 索引混用 panic

- **日期**：2026-09-11 12:52 CST（取自修复落盘的文件 mtime）
- **状态**：✅ 已修复，回归测试锁定，全仓 `go test ./... -race` 全绿（fix 文档由助手 session 依据修复代码注释补写归档）
- **涉及文件**：
  - `tui/markdown.go`（修复 `latexIsEscaped`：改字节向后扫描）
  - `tui/markdown_latex_test.go`（新增，4 个回归测试函数）
- **触发场景**：dogfooding 中助手消息含「中文段落 + 行内公式 `$面积$`」的普通 markdown，渲染时 panic（index out of range）。

## 现象

C 端普通消息（CJK 文字紧邻行内数学）渲染即 panic，`latexIsEscaped` 处 index out of range。

## 根因：byte offset 被当作 []rune 索引

`latexFindClosingDelimiter` 用 `strings.Index` 找闭合定界符，返回的是**字节偏移**；而 `latexIsEscaped` 把它直接当下标访问 `[]rune(source)`：

```go
runes := []rune(source)
for position := index - 1; position >= 0 && runes[position] == '\\'; position-- {
```

CJK 内容使闭合 `$` 的字节偏移（如 `$面积$` 中末尾 `$` 为字节 7）**超过 rune 数量**（4），`runes[position]` 越界 panic。

与 2026-09-10「CJK 相邻数学不 tokenize」是同一 UTF-16↔rune↔byte 索引家族，但**方向相反**：那次是提示函数产出字节索引、消费端按 rune 切片，修法是 byte→rune 转换；这次扫描必须停留在 byte 世界。

## 修复

`latexIsEscaped` 改为**字节向后扫描**：反斜杠是 ASCII 0x5C，永不是 UTF-8 续字节，按字节向后数等价于按 rune 数；越界保护 `position < len(source)` 对应 pi `markdown.ts isEscaped`（UTF-16 世界越界索引得 undefined）的 parity 语义。

## 回归测试锁定

`markdown_latex_test.go` 4 个函数：

- `TestInlineLatexEscapeScanWithCJKDoesNotPanic` —— 修复前必 panic 的最小用例（`$面积$`，字节 7 > rune 4）
- `TestInlineLatexEscapedDollarAfterCJK` —— CJK 后 `\$` 不闭合、真闭合符更靠后，双分支都过多字节内容
- `TestInlineLatexDoubleBackslashAfterCJKNotEscaped` —— 偶数反斜杠 → 不转义（parity 语义）
- `TestMarkdownRendersCJKMathSentenceEndToEnd` —— 真实 panic 路径：完整 marked lexer 渲染「中文段落含 `$面积$`」不 panic 且渲染正确

## 复现与验证

```bash
go test ./tui/ -run 'TestInlineLatex|TestMarkdownRendersCJKMath' -count=1 -v
go test ./... -race -count=1
```
