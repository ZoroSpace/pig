# S20 收口 — pi v1.3.0 → main@73f8de80b（pre-v1.4.0）

- **日期**：2026-10-20
- **状态**：✅ 完成（未打 tag：pi 未发新正式版本，`codingagent.Version` 保持 `1.3.0`）
- **基线**：`v1.3.0..73f8de80bc87021375f8b525292ac07492253002`（43 commit，实移 13）
- **计划稿**：`docs/handoffs/SYNC-S20-pre-digest.md`
- **commit**：`b1b85ad`（计划）、`0784fb5`（S20-1）、`d8c76f3`（S20-2）、`dfdec90`（S20-3）

## 1. 本批完成

### S20-1 ai（`0784fb5`）
- `ai/utils/retry.go`：retryable 正则增 `upstream.?request.?failed`、`upstream_stream_read_error`、`stream was interrupted`、`http/?2 stream failed`、`request to \S+ failed, reason:`；`retry_test.go` 对应测试。
- `ai/utils/overflow.go`：任意 error 态 413 视为 overflow（`^(?:[\w -]+ API error \(413\):|413(?:\s|:|$))`）；`overflow_test.go` 正反例，Cerebras bodyless 收窄为 400。
- `ai/api/openai_completions_convert.go` / `openai_responses_shared.go` / `google_shared.go`：纯空白 tool result 用 `strings.TrimSpace` 判空；google 保持 pi 的 text/image/fallback 三元语义；三类测试各补。
- `ai/scripts/generatemodels/process.go`：MiniMax allowlist → denylist（隐藏 M2/M2.1/M2.5/M2.5-highspeed），effort 模型写 `ThinkingLevelMap` + `ForceAdaptiveThinking`；`main.go` 删 allowlist 过滤；新增 `minimax_models_test.go`。
- `ai/models_generated.go`：定点补 `MiniMax-M3.1-Flash`（minimax/minimax-cn）与 `nvidia/nemotron-3.5-super-vl-120b-a12b`；未整文件重生成。
- 测试模型常量 `mimo-v2.5*` → `mimo-v2.6*`（api/utils/providers 若干 `_test.go`）。

### S20-2 coding-agent（`d8c76f3`）
- `codingagent/core/compaction.go`、`branch_summarization.go`：摘要请求不再设 maxTokens（删 0.8/0.5×reserveTokens 与 4096 cap）；`GenerateSummary*` 的 reserveTokens 保留但标 unused；`generateTurnPrefixSummary` 删参数；测试改断言「不下发 maxTokens」并把 clamp 测试改为 usage 合并测试。
- `codingagent/utils/shell.go` + 新增 `shell_path_exists_{unix,windows}.go`：`os.Stat` → `shellPathExists`（Unix `syscall.Access(path, 0)`；Windows `os.OpenFile(O_RDONLY)`），接受 Windows Store alias。
- `codingagent/utils/shell.go`：`isLegacyWslBashPath` 按 `SystemRoot\System32|Sysnative\bash.exe` 判定（空/非法回退常规路径）；`GetPowerShellConfig` 先 PATH，再 `ProgramFiles\PowerShell\7\pwsh.exe`，再 Windows PowerShell，最后 `SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe`；抽 `shellLookup` 便于 Linux 单测。
- `codingagent/utils/tools_manager.go`：`getWindowsTarCommand` 只用 `SystemRoot`（删 `WINDIR`）。
- `codingagent/core/model_resolver.go`：xiaomi 四 provider → `mimo-v2.6-pro`；nvidia → `nvidia/nemotron-3.5-super-vl-120b-a12b`；`model_resolver_test.go` 增默认值守卫。
- 新增测试：`powershell_config_test.go`、`windows_shell_config_test.go`、`shell_path_exists_test.go`。

### S20-3 tui（`dfdec90`）
- `tui/terminal_image.go`：`TERM_PROGRAM=rex` → truecolor true、hyperlinks true、images nil；分支置于 herdr 后、outer-terminal 检测前。
- `tui/terminal_image_test.go`：Rex + 泄漏 `KITTY_WINDOW_ID` 测试、`PI_HYPERLINKS=0` 覆盖测试。

### 验证
- `go build ./...`、`go vet ./...`、`gofmt -l` 干净。
- 全量 `go test ./... -race -timeout 600s -count=1`：rc=0，33 个 ok 包（日志 `/tmp/pig-s20-test.log`）。

### 本批偏差/决策
- 摘要调用点传 `0` 代替 pi 的 `undefined`（参数已忽略，Go 无 undefined）。
- `shellPathExists` Unix 用 `syscall.Access` 而非 `os.OpenFile`，与 Node `access(F_OK)` 语义严格一致。
- `482c1381a`（GLM-4.6V cacheRead）、`e86b05475`（OpenRouter deepseek-r1-0528）核对 pig 无对应测试引用 → 零操作。
- 目录未整文件重生成（沿用 S14/S16-1/S18/S19 策略）。

## 2. 下一批定义

用户尚未拍板下一批，候选与已知落点：

1. **pi v1.4.0 正式 tag 释放后**：`git -C ~/Projects/pi fetch --tags`，按 `v1.3.0..v1.4.0`（或 `73f8de80b..v1.4.0`）重复本批流程；先核对 `core/session.ts`、`core/messages.ts`、`core/transcript.ts`、RPC 线格式。
2. **Phase 8 余量**（需用户排序）：telemetry 真实现（对应跳过族 `48909d987`/`1402973a9`/`2ec55d808`）、photon/wazero、goreleaser、`/share`、terminal-image（Rex 只做了 capability 检测，图像管线仍未移植）。
3. **Dogfooding 插批**：按 `docs/fix/` 流程登记。

## 3. 风险与坑

- **MiniMax 生成器**：`processMinimaxModels` 的 denylist 与 adaptive thinking 必须成对；重生成 models_generated 会带来大漂移，仍按定点补条目策略。
- **Windows shell**：`shellPathExists` 的 Windows 分支用 CreateFile 打开 alias；真机 Windows 建议跑一次 `pwsh.exe` 发现冒烟（Linux CI 只能覆盖路径解析）。
- **摘要 maxTokens**：provider 层现在自行按上下文裁剪输出；若某些 provider 无默认输出上限，需在真机冒烟里确认 compaction 不会无界。
- **Rex**：`PI_HYPERLINKS`/`PI_TRUE_COLOR` 覆盖仍最后生效；images 保持 nil，不要被 herdr kitty 分支误带。

## 4. 明确不要读的

`packages/durable/`、`packages/chord/`、`packages/env/`、`packages/telemetry/`、`packages/ai/src/telemetry.ts`、`scripts/*.mjs`、`packages/coding-agent/docs/extensions.md`。
