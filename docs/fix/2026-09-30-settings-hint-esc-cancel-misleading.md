# /settings 面板提示「Esc to cancel」误导（Esc 不撤销任何改动）

- **日期**: 2026-09-30 11:48
- **状态**: 已修复（2026-10-01 文案再收为 `Esc to confirm and go back`，见文末追加）
- **涉及文件**: `tui/settings_list.go`、`tui/settings_list_test.go`

## 现象

用户报：`/settings` 面板底部提示 `Type to search · Enter to change · Esc to cancel` 文案有问题。Esc 的实际行为是「返回 / 关闭面板」，而 `cancel`（取消）会被用户理解为「本次进入面板做的改动全部作废」——但事实并非如此。

## 根因

提示文案直接照搬 pi（`packages/tui/src/components/settings-list.ts` 的 `"… Esc to cancel"`），而 `SettingsList` 的语义与「取消」不符：

- 每次切换值都立刻经 `onChange` 落到 `settingsManager.SetX(...)` 并持久化到 `settings.json`，面板里**没有待确认状态**；
- Esc 走 `onCancel()`，在 `/settings` 里就是 `done()` + `RequestRender`（`interactive_mode.go` 的 `OnCancel`），即**关掉面板回到聊天**，不回收已写入的设置；
- 提示行只在**没有子菜单打开时**显示（`Render` 在 `submenuComponent != nil` 时直接委托子菜单），所以它描述的不是「子菜单里按 Esc 放弃本次选择」那一层；
- 同一面板的子菜单提示（`settings_submenu.go`，pi 原文）早已写作 `Esc to go back`，顶层列表却写 `cancel`，自身就不一致。

## 处置

`tui/settings_list.go` 的 `addHintLine` 两种形态都改为 `Esc to go back`，并在函数上补注释说明偏差理由（立即生效无待确认状态、与子菜单提示一致、仅在无子菜单时显示）。这是相对 pi 的有意偏差（pig 原文案更贴近真实语义），与 2026-09-29 把 `Enter/Space to change` 收成 `Enter to change` 同属提示文案对齐行为。

```go
hint := "  Enter to change · Esc to go back"
if l.searchEnabled {
    hint = "  Type to search · Enter to change · Esc to go back"
}
```

## 回归测试

`tui/settings_list_test.go` 新增 `TestSettingsListHintSaysGoBackNotCancel`：启用/禁用搜索两种形态各自断言渲染结果含 `Esc to go back`、且不含 `Esc to cancel`。

验证方式（原地把文案改回 `Esc to cancel` 再复原，即 stash 等价验证）：

```
--- FAIL: TestSettingsListHintSaysGoBackNotCancel (0.00s)
    --- FAIL: .../without-search  render = "...Enter to change · Esc to cancel"
    --- FAIL: .../with-search     render = "...Enter to change · Esc to cancel"
```

改回 `Esc to go back` → 全绿。

## 追加（2026-10-01）：文案再收为「Esc to confirm and go back」

用户报：`Esc to go back` 仍不够明确——Esc 会**保留并落盘**本次改动再返回，应在文案里体现「确认」语义。

处置：`tui/settings_list.go` 两种形态改为 `Esc to confirm and go back`，注释同步（并说明与子菜单 `Esc to go back` 的差异：子菜单的 Esc 可能放弃预览，故不跟改）。这是对 09-30 那次的措辞收紧，不是行为变更。

```go
hint := "  Enter to change · Esc to confirm and go back"
if l.searchEnabled {
    hint = "  Type to search · Enter to change · Esc to confirm and go back"
}
```

回归测试 `TestSettingsListHintSaysConfirmAndGoBackNotCancel`（原 `TestSettingsListHintSaysGoBackNotCancel` 重命名）断言两种形态含新文案、不含 `Esc to cancel`。stash 等价验证（把文案改回 `Esc to go back` 再复原）红→绿。
