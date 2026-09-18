# DEFERRED-UI-CLEANUP — 清交互面最后的声明延后（下一批定义）

状态链：本批的上批 = **SLASH-COMMANDS-COMPLETION（2026-09-18，已交付）**，决策见 `docs/feat/SLASH-COMMANDS-COMPLETION.md`，两份 fix 见 `docs/fix/2026-09-18-reload-command-leaks-to-model.md`、`docs/fix/2026-09-18-autocomplete-stale-accept-mangles-text.md`（commits 6c9d086、82c4d07）。本批完成后交互面对 pi 的缺口只剩 `/export` HTML 路径与 `/share`（等 P8 / gist API）。

## 1. 上批完成（一句话级，详情见上述文档）

8 命令接线 1:1 + /reload 漏接线修复 + stale autocomplete accept 守卫（pig-only hardening）+ `--resume` 解封；测试 +6 函数；tmux 冒烟 12 项全通；全仓 28 包 -race rc=0。

## 2. 本批范围（三件，无外部依赖）

### a. daxnuts 彩蛋（选模型触发，非命令）

pi：选中模型 provider=opencode 且 id 含 `kimi-k2.5` → `checkDaxnutsEasterEgg` 弹 DaxnutsComponent。pig 三处对应点有声明延后注释（`interactive_mode.go:6178`、`completeProviderAuthentication` 内 `:7123` 附近）。

### b. maybeWarnAboutAnthropicSubscriptionAuth（anthropic 订阅警告）

pig 完全未实现（同样声明延后）。逻辑：settings `warnings.anthropicExtraUsage === false` → 跳过；shown-once 标志；`model.provider === "anthropic"`；`CheckAuth("anthropic")` 为 oauth **或** apiKey 前缀 `sk-ant-oat` → 警告（文案逐字，含 URL 与 "Disable this warning in /settings."）。

### c. `/export <path>.jsonl` 半接线

`ExportSessionToJsonl` 基建已在（core/session_export.go:21）。带 `.jsonl` 参数走真导出；裸 `/export` 与 `.html` 保持 deferred 文案（HTML 等 P8）。

## 3. 要读的 pi 源文件（精确路径，全部在 packages/coding-agent/src/modes/interactive/）

| 文件:行 | 内容 |
|---|---|
| `components/daxnuts.ts` | 164 行全量：DAX_HEX 32×32 RGB（3 字节/像素）→ `▄` 半块渲染（bg=top/fg=bottom）；扫描线 reveal（80ms ticker × maxTicks 25）；文案按 tick 淡入（accent/success/muted/dim/mdLink 主题色 + 裸 truecolor 扫描线）；dispose |
| `interactive-mode.ts:6470` 附近 | `checkDaxnutsEasterEgg`（provider/id 判定 + handleDaxnuts） |
| `interactive-mode.ts:4854 / 5000 / 5716` | 三个调用点：模型选择器选中、scoped/持久化选择、completeProviderAuthentication（login 后选模型） |
| `interactive-mode.ts:4908-4936` | `maybeWarnAboutAnthropicSubscriptionAuth` 全体 |
| `interactive-mode.ts:253-258` | 警告文案常量 + `isAnthropicSubscriptionAuthKey`（`sk-ant-oat` 前缀） |
| `interactive-mode.ts:6032-6048` | `handleExportCommand` 的 `.jsonl` 分支（半接线样板） |

## 4. Go 目标文件

- 新建 `codingagent/modes/interactive/components/daxnuts.go`（← daxnuts.ts 1:1；DAX_HEX 逐字节校验，同 armin.go 先例用脚本比对）
- `codingagent/modes/interactive/interactive_mode.go`：清 `:6178`/`:7123` 两处延后注释并接线三调用点 + warning helper + `/export` submit 分支改造
- `core/session_export.go` 的包装确认：pi 走 `this.session.exportToJsonl(outputPath)`，pig 确认 AgentSession 侧包装（无则在 AgentSession 补薄包装，勿改包级函数签名）

## 5. pig 侧现成依赖（锚点）

- 调用点：`interactive_mode.go:6177`（cycleModel 路径）、`:6251`（选择器 persist 路径）、`completeProviderAuthentication`（login 后）——pi 三点 ↔ pig 三点一一对应
- `ModelRuntime.CheckAuth(ctx, providerID) (*auth.AuthCheck, error)`（core/model_runtime.go:667，`Type == auth.AuthTypeOAuth`）
- settings：core/settings_manager.go:2837 已处理 `anthropicExtraUsage` 迁移/映射；确认 GetWarnings 读取面，缺则补 accessor；`/settings` 选择器若无该开关条目，对照 pi settings submenu 补（先查 pi 是否暴露）
- `getPathCommandArgument`（SLASH-COMPLETION 批已移植）直接复用
- 动画 goroutine 模式：套 `components/armin.go`（utils.RecoverPanic + ticker + RequestRender + stopAnimation + Dispose）
- 测试主题 mdLink token 已在 `interactive_memleak_probe_test.go` 补过，daxnuts 文案测试可复用 probeThemeColors

## 6. 风险与坑

1. **`/export` 判定移位**：`.jsonl` 放行后，拦截判定必须从 `isDeferredInteractiveCommand` 清单挪进 submit 分支（先取参数判后缀再决定拦截或放行），`deferredCommandReason` 的 export 文案只对默认/.html 生效；`TestIsDeferredInteractiveCommandBoundaries` 与 `TestDeferredInteractiveCommandsStillWarn` 需同步改（`/export x.jsonl` 进 no 清单）。
2. **shown-once 标志并发面**：三调用点分属 submit worker / 选择器回调 / login goroutine——`anthropicSubscriptionWarningShown` 用 mutex 或 atomic（pig 并发面新增，注释声明）。
3. **daxnuts 裸 truecolor**：扫描线用裸 ANSI RGB（非 theme token），照搬勿"规范化"；文本行才是 theme.fg。居中计算用 `visibleWidth`（剥 ANSI 后），勿用字节长。
4. **half-block 渲染**：`▄` bg=top/fg=bottom，末行 odd-height 用 bottom=top 兜底（pi `pixels[row+1]?.[x] ?? top`）。
5. DAX_HEX 是 3072 hex 字符 = 1024 像素 × 3 字节，移植后跑脚本与 TS 原文比对（armin.go BITS 先例）。

## 7. 明确不要读的

- pi：`experimental/`、`runtime/`、`harness/`、`core/export-html/`（P8）、`utils/share.ts`（gist 不做）、`session-backends/`
- pig：`ai/`、`agent/` 包内部、`docs/batches/2026-09.md`（历史归档）、`tui/` 内部（上批 stale-accept 守卫已收口，不碰编辑器）

## 8. 给人类的建议

无计划变更。本批完成后，交互面与 CLI 命令面相对 pi 的唯一已知缺口 = `/export` HTML 默认路径 + `--export`（同根，P8 export-html）与 `/share`（gist API）。
