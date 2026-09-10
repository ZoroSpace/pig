# LATEX：latex.ts 数学渲染移植（renderLatex）

**状态**：✅ 完成（2026-09-10）。**归档位置**：`docs/feat/`（非批次性功能交付记录）。被后续迭代取代时在此标注。
**范围**：非 §4 计划批次，Phase 6 延后项清单第一条（原 T3 关键决策："latex.ts 延后（renderLatexFn seam）"）。

## 本批完成

### 新建/改动

| 文件 | 内容 |
|---|---|
| `tui/latex.go` | 新建 ~1180 行：`latex.ts`（pi 1394 行）全量移植——233 条符号表、4 个命令集合、`LatexParser`（递归下降 + supported 门控）、display 模式多行 layout 引擎（fraction 竖排/operator 上下限/matrix 三族 + marker 机制）、`RenderLatex` 导出；`init()` 接通 `markdown.go` 的 `renderLatexFn` seam（对齐 pi 的直接 import） |
| `tui/latex_test.go` | 新建：`latex.test.ts`（505 行）test-for-test 移植，24 个测试函数全量（Jacobian/Satellite/Stress 三大会话 + 符号/重音/关系代数/定界符/cases/matrix/display 全覆盖 + unsupported/malformed 门控） |
| `tui/markdown_latex_test.go` | 新建：markdown.test.ts "LaTeX math" describe 的 **7 个曾因 renderLatex 缺席而延后的渲染测试**（inline dollar/display dollar/display bracket/matrix 对齐/lower limits/lists+tables 内数学/raw→rendered 切换） |
| `tui/markdown_test.go` | 文件头延后声明更新（LaTeX 段已解锁，raw-fallback 子集测试保留有效） |

### 测试

全仓 **1118 测试函数**（+31），`go test ./... -race -count=1` 26 包全绿；`go build`/`go vet`/`gofmt -l` 干净。markdown 全部既有 raw-fallback 测试在 seam 激活后无需改动即通过（unsupported/incomplete 数学仍回落 raw）。

## 本批新增决策与偏差（相对 pi）

1. **marker 正则的手工扫描**：两个 JS lookaround spacing pattern（`NAMED_OPERATOR_LEFT/RIGHT_SPACING_PATTERN`）RE2 无等价物 → 改手写 rune 扫描（`latexNamedOperatorSpacingLeft/Right`）；`\b` 边界（cases 条件前缀）→ 显式非单词字符类；`\limits|\nolimits` modifier lookahead → 前缀匹配 + 下一字符手验（含 end-of-string 语义对齐）。
2. **parser 内部统一 rune 索引**（TS UTF-16 → pig 惯例），marker 常量 U+F0000–F0005 均单 rune；layoutNodes 共享经 `*[]latexLayoutNode` 指针（TS 对象引用突变语义 → 值类型 + 切片回写）。
3. **`RenderLatex(source, display)`**：pi 的 `RenderLatexOptions` 仅一个 `display` 选项，折叠为 bool 参数（与 markdown seam 签名一致）。
4. **测试字面量陷阱**（移植时踩过）：`\hat{y}` 的期望输出是 y + **组合用** U+0302，不是预组合 ŷ——pi 测试文件用 NFD 形式，Go 字面量照抄会字节级不等。

## 遗留/关联

- pi 侧 `terminal-image.ts` / photon-wazero / WordSegmenter CJK 三项延后不变（见 AGENTS.md）。
- 下一个延后项若启动 markdown 流式 "switches from raw..." 已随本批收口，markdown_test 头部仅余 VT fg-color 两测（随模拟器 fg 追踪）。
