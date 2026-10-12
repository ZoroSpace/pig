# edit 工具 diff 在恢复会话中不显示（Details 双形态 + json tag 缺失）

- **日期**：2026-10-10
- **状态**：已修复（回归测试锁定）
- **涉及文件**：`codingagent/core/tools/edit.go`、`codingagent/core/tools/renderers_edit.go`、`codingagent/core/tools/edit_test.go`、`codingagent/modes/interactive/components/tool_execution_border_test.go`
- **家族**：双形态家族第七例（前例：`2026-10-03-restored-tool-results-dropped.md` 第五例、`2026-10-10-fork-nil-options-panic.md` 第六例）

## 现象

EDIT-FRAME 批（edit 加入 default shell 整框，见 `docs/feat/BUILTIN-THEME-PALETTES.md` §BLOCK-BORDERS 八批）收口后用户实测：edit 卡片有框了，但框里只有 header 行，diff 内容消失。live 会话不复现，`--continue` 恢复的会话必现。首次修复（map 形态兜底）后用户复测**仍复现**，二次定位发现真正元凶。

## 根因

两层：

1. **restore 路径 Details 形态**：live 路径 agent loop `createToolResultMessage`（`agent/agent_loop.go:912`）把 edit 工具返回的 `Details`（`*EditToolDetails` 指针）原样透传，`editRenderResult` 的 `result.Details.(*EditToolDetails)` 断言命中 → diff 正常；restore 路径 session JSONL 解码 `types.ToolResultMessage.Details any` 字段，标准 `encoding/json` 产出 **`map[string]any`**，指针断言失败 → `resultDiff` 恒空。pi 上游等价代码读 `details?.diff` 属性（无类型断言），解码对象天然可用——pig 的类型化断言是移植引入的偏差面。该 bug 与整框改造无关（self-render 时代 restore 同样丢 diff），属存量 bug 顺势曝光。
2. **`EditToolDetails` 缺 json tag（真正元凶）**：其余工具 Details 结构体全带 camelCase tag（`truncation`/`fullOutputPath`/`matchLimitReached`…），唯独 `EditToolDetails` 漏了——pig 会话写出 `"details":{"Diff":...}` 大写键（用户会话 JSONL 实证），既违反线格式硬约束（pi 规格 `edit.ts` `EditToolDetails { diff, patch, firstChangedLine? }`），也使首次修复读 `d["diff"]` 匹配不上存量会话。

## 修法

- `edit.go`：`EditToolDetails` 补 tag——`Diff json:"diff"`、`Patch json:"patch"`、`FirstChangedLine json:"firstChangedLine,omitempty"`（新会话写 camelCase，与 pi 双向兼容）。
- `renderers_edit.go`：新增 `editDetailsDiff(details any) (string, *int)`，三形态兜底：`*EditToolDetails`（live）/ `EditToolDetails`（防御）/ `map[string]any`（restore，camelCase `diff`/`firstChangedLine` + 存量 pig 会话大写 `Diff`/`FirstChangedLine` 兼容读）。`formatEditResult` 与 `editRenderResult` 两处断言全部替换为该 helper。

**同族已知边界（本批不修）**：`renderers_bash.go` / `renderers_read.go` / `renderers_grep.go` / `renderers_find.go` / `renderers_ls.go` 的 details 断言同为指针-only，restore 后只影响截断脚注/限额提示（TruncationResult 嵌套 map 重建；这五个结构体 tag 齐全），主体输出走 content 不受影响；如需对齐再开一批。

## 回归测试

- `tool_execution_border_test.go` `TestEditToolResultDetailsMapForm`：四形态子测（pointer / map camelCase / map legacy 大写键 / value），restore 形态端到端经 `ToolExecutionComponent.UpdateResult` → `editRenderResult`，断言 diff 行出现在整框渲染内。stash 验证：仅回滚 `renderers_edit.go` 时 map/value 三子测红；恢复后全绿。
- `edit_test.go` `TestEditToolDetailsWireKeys`：线格式守卫——marshal 键名 camelCase 钉值（`{"diff":...,"patch":...,"firstChangedLine":5}`）、nil `FirstChangedLine` omitempty、pi 形 JSON 反序列化。stash 验证：仅回滚 tag 时红（`wire keys drifted: got {"Diff":...}`，与用户会话实证完全一致）；恢复后绿。

## 验证

- 定向：`go test ./codingagent/core/tools/ -run TestEditToolDetailsWireKeys` + `go test ./codingagent/modes/interactive/components/ -run 'TestEditToolResultDetailsMapForm|TestEditToolFramedShell'` 全绿。
- 全量：`go test ./... -race -count=1 -timeout 600s` 单次 rc=0 32 包。
- 真机端到端（2026-10-12 补，起因：首次修复后用户复测仍红而单测全绿——教训：渲染类修复必须真机冒烟，不能停在单测层）：从用户真实会话 JSONL 提取 header + edit toolCall + toolResult（大写 `Diff` 键）三行构造最小会话，`PIG_CODING_AGENT_DIR` 隔离 + tmux `env -i` 清凭证启动新编译二进制 `--continue`，capture-pane 确认 diff 全文渲染（含 `+166` 增行与 `...` 折叠）；旧条目经 legacy 键回退可读。用户复测仍红的根因是**运行中进程早于重编译**（进程 10:40 启动、二进制 10:43 重建），重启 pig --continue 即恢复。
