# /settings 面板空格键不生效、选中项跳回第一项（Kitty CSI-u 空格未识别）

- **日期**: 2026-09-29 23:30
- **状态**: 已修复
- **涉及文件**: `tui/settings_list.go`、`tui/settings_list_test.go`

## 现象

用户报：在 `/settings` 面板里**回车能改设置，空格不能**——按空格当前项不变更，反而跳回列表第一项（“跳到其他选项去了”）。

## 根因

`SettingsList.HandleInput` 里空格分支用的是**裸字节比较**，照搬 pi 的 `data === " "`：

```go
case kb.Matches(data, "tui.select.confirm") ||
    (data == " " && (!l.searchEnabled || l.searchInput.GetValue() == "")):
    l.activateItem()
```

但空格会以三种编码到达组件：

| 终端情形 | stdin 字节 |
|---|---|
| 传统终端 / tmux | `" "`（0x20） |
| Kitty 键盘协议（disambiguate） | `\x1b[32u` |
| xterm modifyOtherKeys 回落（pig 在 `terminal.go` 里主动 `ESC[>4;2m` 打开） | `\x1b[27;1;32~` |

后两种都**不等于** `" "`，于是落到最后一个 `case`（搜索输入分支）：

```go
case l.searchEnabled && l.searchInput != nil:
    l.searchInput.HandleInput(data)   // Input 用 DecodeKittyPrintable 把 CSI-u 空格还原成 " "
    l.applyFilter(l.searchInput.GetValue())
```

`applyFilter` 里 `l.selectedIndex = 0`。所以按空格的实际效果是：把空格塞进搜索框 → 重新过滤 → 光标归零。若用户已用方向键下移到第 N 项，视觉上就是“跳回第一项且没改设置”，与报告完全一致（搜索框只有 `>`，看不出多了个空格）。

回车不受影响，因为 `kb.Matches` 走的是编码感知的 `MatchesKey`。

**这是 pi 同源的潜伏缺陷**（pi `settings-list.ts` 同样写 `data === " "`），但在 pig 上更易触发：pig 的 modifyOtherKeys 回落会在更多终端上把普通键编码成转义序列。

## 处置

空格判据改用编码感知的 `MatchesKey(data, "space")`（同包，覆盖裸字节 / Kitty CSI-u / modifyOtherKeys 三种形态，且只在无修饰键时匹配）：

```go
case kb.Matches(data, "tui.select.confirm") ||
    (MatchesKey(data, "space") && (!l.searchEnabled || l.searchInput.GetValue() == "")):
    l.activateItem()
```

判据语义不变：搜索框为空时空格=确认，一旦有查询内容空格仍是搜索文本（pi 语义保留）。代码内已注释声明这是相对 pi 的有意偏差。

## 回归测试

`tui/settings_list_test.go` 新增两函数：

- `TestSettingsListSpaceShortcutAcceptsAllKeyEncodings`：三种编码（`" "` / `\x1b[32u` / `\x1b[27;1;32~`）都必须触发 `onChange`（值 regular→fullscreen），且不得写进搜索框。
- `TestSettingsListSpaceWithActiveQueryDoesNotChangeTheSetting`：查询非空时三种编码都不得改设置（pi 语义）。

验证方式（原地改判据再复原）：把空格判据还原成 pi 的 `data == " "` →

```
--- FAIL: TestSettingsListSpaceShortcutAcceptsAllKeyEncodings
    --- FAIL: .../kitty-csi-u      changes = [], want [{tui-mode fullscreen}]
    --- FAIL: .../modify-other-keys changes = [], want [{tui-mode fullscreen}]
```

改回 `MatchesKey(data, "space")` → 全绿。

## 真机验证

tmux + mock 端点真 TTY：下移到第 5 项「Show hardware cursor」，注入 Kitty 空格 `\x1b[32u`——

- 修复前：选中项跳回第 1 项（Auto-compact），设置未变（复现用户报告）；
- 修复后：`Show hardware cursor` 原地 false→true，不跳项。
