# edit 工具失败率对比调查：pig vs pi（2026-09-18）

**日期**：2026-09-18 18:07
**状态**：已收口——无移植缺陷，无需改代码；结论为操作纪律问题
**涉及文件**（只读调查，未改动）：
- `codingagent/core/tools/edit.go` / `edit_diff.go`（别名转发层）
- `agent/harness/tools/edit_diff.go`（匹配逻辑真身）
- `~/Projects/pi/packages/coding-agent/src/core/tools/edit.ts` / `edit-diff.ts`（规格基线 v0.85.1）

## 起因

用户观察：pig 会话中 agent 的 edit 工具调用频繁报 `Could not find edits[N] … oldText must match exactly`，体感 pi 少得多，怀疑 pig 的 edit 移植与 pi 有行为差异。

## 查证结论

**pig 完整移植了 pi 的全部匹配语义，包括 fuzzy 兜底层。**

- pi 的匹配链：`applyEditsToNormalizedContent` → `fuzzyFindText`（先精确 `indexOf`，失败后两边过 `normalizeForFuzzyMatch` 再匹配）→ `countOccurrences`（fuzzy 空间计数）→ 命中时在规范化空间替换 + 行级 overlay（未改动行块保留原字节）。
- `normalizeForFuzzyMatch` 的容错面：NFKC 规范化 + 逐行剥离**行尾**空白 + 智能引号（`''""‚„‟`）→ ASCII + 各类 dash（U+2010-2015、U+2212）→ `-` + 特殊空格（NBSP 等）→ 普通空格。**不含行首缩进容错**。
- pig 侧全部存在于 `agent/harness/tools/edit_diff.go`（`NormalizeForFuzzyMatch`/`FuzzyFindText`/`countOccurrences`/fuzzy 空间替换 + overlay），`codingagent/core/tools/edit_diff.go` 只是别名转发。
- **调查陷阱记录**：在别名转发层 grep "fuzzy" 得零命中，差点误判为「漏移植缺口」；转发层不能作为实现判定依据，必须下钻到真身。
- 两边唯一声明偏差：harness 层逐行 trimEnd 用 Go `unicode.IsSpace`（覆盖 JS trimEnd 字符集 + Unicode 空白；仅奇异分隔符可见，文件头已声明），与本调查无关。
- 回归测试在位且全绿：`TestEditToolFuzzyMatching`（`codingagent/core/tools/tools_test.go`，fuzzy 生效 / 精确优先 / 兜底后仍无匹配才报错三段）+ harness 层 7 个 edit 测试。

## 本 session 三次失败的逐一归因

| 失败 | 实际原因 | pi 在同输入下 |
|---|---|---|
| `edits[4].oldText must not be empty` | agent 传了空 oldText | **同样报错**（`getEmptyOldTextError` 1:1 移植，两边都显式拒绝） |
| `Could not find edits[4]`（resource_stats.go） | agent **凭记忆**拼 oldText，缩进层级写错一层 | **同样失败**——fuzzy 不兜行首缩进 |
| `Could not find edits[0]`（footer_resource_stats_test.go） | agent 把 resource_stats.go 的编辑块错放进 test 文件的编辑调用 | **同样失败**——文件指错 |

## 处置

- pig 无需改代码；pi 同输入同样失败，不构成移植缺口。
- **操作纪律修正（agent 侧）**：连续多轮重写同一文件时，禁止凭对上一轮编辑的印象拼 oldText；每次编辑前以文件当前真实内容为准（编辑轮次之间重新 read 目标块）。这是 pi 系统提示同样要求的（"oldText must match exactly"）。
