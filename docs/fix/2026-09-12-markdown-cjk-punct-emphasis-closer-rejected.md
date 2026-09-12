# markdown：CJK 标点夹持的斜体/粗体收尾定界符被整段拒绝

- **日期**：2026-09-12 19:28（取自 `tui/marked_tokenizer.go` 修复落盘 mtime）
- **状态**：✅ 已修复收口
- **涉及文件**：`tui/marked_tokenizer.go`（修复点）；`tui/markdown_cjk_punct_emphasis_test.go`（回归测试，新建）

## 现象

用户 dogfooding 中发现：对话回复里 `**粗体**` 正常渲染，而单星斜体 `*……*` 在特定句子里整段原样输出（星号字面保留、无斜体样式）。同一回复里其他斜体/粗体不受影响。

最小复现：`结论：*技术（公开）*。需要动代码。` —— pig 整段塌成一个纯 text token；去掉 `（公开）` 即恢复正常。

## 根因

marked 的 em/strong 收尾扫描有一个 CommonMark multiple-of-3 分支（候选收尾定界符落在 group 5/6 时，opener 长度 i 与 ender 长度 u 之和是 3 的倍数则拒绝配对）。pig 移植时把该条件写反：

- **上游 marked**（`marked.esm.js` 反混淆）：`else if ((s[5] || s[6]) && i % 3 && !((i + u) % 3)) { c += u; continue; }` —— 仅当 `(i+u) % 3 == 0` 时跳过该候选；
- **pig**（`tui/marked_tokenizer.go` emStrong）：`... && (delimRuneLen+endersLen)%3 != 0` —— 恰好相反。

该分支只在**收尾定界符左右两侧都紧贴 Unicode 标点**时才会走到（group 5：`punct(\*+)(?=punct)`）。英文场景收尾 `*` 前几乎总是字母（group 2 分支，两条实现路径一致），所以长期未暴露；中文全角标点 `）*。` 正好命中——真正的收尾候选被错误计入 counter 并跳过，扫描再也找不到收尾星，em/strong 整体失配，段落回退纯文本。

`del()`（删除线）无此问题（marked 的 del 本就没有 %3 分支，pig 同样没有）。

## 为什么测试没拦住

T3 批整体移植的 markdown.test.ts 中，%3 规则用例全部是拉丁字母相邻形态（group 2 路径），没有"收尾定界符两侧都是标点"的用例——上游盲区，移植照单全收。

## 修复

一行：`(delimRuneLen+endersLen)%3 != 0` → `== 0`，并在分支前补 marked 原式注释。

## 回归测试与验证

- **测试**：`tui/markdown_cjk_punct_emphasis_test.go`
  - `TestMarkdownEmphasisCloseFlankedByPunctuation`（4 子测试）：最小复现（em）、strong 变体、%3 规则正方向（`x *a（b）**。y` 必须无 em——锁"不修过头"方向）、group 1 对照（`x *a（b + c + d）* y` 一直正常）；
  - `TestMarkdownEmphasisCloseFlankedByPunctuationRealSentence`：锁定 2026-09-12 dogfooding 原句。
- **真值来源**：全部用例经 pi 自带 marked@18.0.5（node 直跑 Lexer）逐例确认为上游行为。
- **stash 验证**：先写测试后修复——修复前 4 用例 FAIL / 对照用例 PASS（证明测试确实抓到 bug），修复后全 PASS。
- **全量**：`go test ./... -race -count=1 -timeout 600s` 单次执行 rc=0，28 包全绿（输出存 `/tmp/pig-test-out.log`）；`go build && go vet && gofmt -l` 干净。

## 同族排查结论

`emStrongRDelimUnd`（`_` 斜体）共用同一分支同一段代码，本修复同时覆盖；`del()` 结构核对过，无同类偏差。
