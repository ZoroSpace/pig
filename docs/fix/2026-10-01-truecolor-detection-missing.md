# truecolor 探测缺失：全部主题色被量化到 256 色板

- 日期：2026-10-01 15:11
- 状态：已修复
- 涉及文件：`tui/terminal_image.go`（修复）、`tui/terminal_image_test.go`（新增回归）、`codingagent/modes/interactive/theme/theme_detection_test.go`（新增回归）、`README.md`、`AGENTS.md`

## 现象

用户报告：同一台 iTerm2 里，pi 的用户消息底色是浅绿灰，pig 的是浅紫——整块消息气泡颜色不同。

## 复现与取证

同一主题（`light/dark` 设置）、同一终端（tmux + `COLORTERM=truecolor`），在两边各提交一条消息后抓取光标处的 SGR 序列：

| | 用户消息底色（dark 主题） | 用户消息底色（light 主题） |
| --- | --- | --- |
| pi | `\x1b[48;2;33;59;73m`（`#213b49`，精确色） | `\x1b[48;2;223;231;236m`（`#dfe7ec`） |
| pig（修复前） | `\x1b[48;5;23m`（ANSI 256 索引 23 = `#005f5f` 青绿） | `\x1b[48;5;189m`（索引 189 = `#d7d7ff` 浅紫罗兰） |

取证方式（tmux 专属会话，用隔离的 `PIG_CODING_AGENT_DIR`/`PIG_CODING_AGENT_SESSION_DIR`，跑完即清理）：

```
tmux new-session -d -s <专属名> -x 110 -y 40
tmux send-keys -t <专属名> 'cd <tmp> && PIG_CODING_AGENT_DIR=... pig' Enter
tmux send-keys -t <专属名> 'PROBE' Enter
tmux capture-pane -t <专属名> -e -p | grep -n '48;'
```

（探针环境另需 `env -u PI_SESSION_FILE -u PI_SESSION_ID`，否则 pig 会按继承的 `PI_SESSION_FILE` 恢复调用方会话。）

## 根因

`codingagent/modes/interactive/theme/theme.go` 的 `createTheme`/`createSystemTheme` 在未显式指定颜色模式时按 `tui.GetCapabilities().TrueColor` 选 `truecolor` 或 `256color`。而 `tui/terminal_image.go` 的 `GetCapabilities()` 当时是 Phase 8 占位桩，直接返回零值——`TrueColor` 恒为 `false`。于是 pig 无条件走 256 色分支，**每个主题色都被就近量化到 xterm-256 调色板**；pi 侧 `detectCapabilitiesFromEnvironment` 在 iTerm2 下判定 truecolor，输出精确色。

这条缺口影响的是整个调色板（用户消息气泡只是最显眼的一处），light 主题下 `#dfe7ec → #d7d7ff` 的偏移最明显，故被最先注意到。

## 修复

在 `tui/terminal_image.go` 落地 pi `detectCapabilitiesFromEnvironment` 的 **`trueColor` 分支**：

- 新增 `detectTrueColorCapability()`，逐分支对齐 pi：多路复用器（`TMUX`/`TERM=tmux*`）只看 `$COLORTERM`/`$TERM` 提示 → `screen*` 同规则 → kitty / ghostty / wezterm / Warp / iTerm2 / Windows Terminal / alacritty·vscode·zed / JetBrains JediTerm / Windows 控制台各自为 true → 未知终端回落 `hasTrueColorHint`（`COLORTERM=truecolor|24bit` 或 `TERM` 以 `-direct` 结尾）。
- 新增 `parseBooleanCapabilityOverride()` 与 `PI_TRUE_COLOR` 支持（`1`/`0` 强制，其余含 `auto` 保持探测；pig 沿用 pi 的 `PI_*` env 命名，见 S8-7 先例）。
- 覆盖优先级与 pi 一致：自动探测 → `PI_TRUE_COLOR` → `SetCapabilityOverrides`（设置里的 `terminal.trueColor`）。

**声明偏差/未移植**：`detectCapabilitiesFromEnvironment` 的 `images`/`hyperlinks` 分支仍延后 Phase 8——hyperlinks 需要 pi 的 tmux OSC 8 转发探测（`probeTmuxHyperlinks`），images 需要 Phase 8 图像管线。二者保持零值，行为与修复前一致（`capabilitiesHaveImages`/alt-screen 对 nil 本来就是安全降级）。`tui/terminal_image.go` 文件头注释已同步改写。

## 回归测试

| 测试函数 | 文件 | 覆盖 |
| --- | --- | --- |
| `TestGetCapabilitiesDetectsTrueColor` | `tui/terminal_image_test.go` | 19 个子测：pi 的终端矩阵（iTerm2/kitty/ghostty/wezterm/Warp×3/Zed/Alacritty/WT/JediTerm/direct TERM/COLORTERM hint）+ 三个负例（未知终端、tmux 不继承外层真彩、screen 不继承）+ tmux 信任显式提示 |
| `TestGetCapabilitiesTrueColorOverrides` | 同上 | `PI_TRUE_COLOR` 双向强制 / `auto` 与未知值回落探测 / 设置覆盖优先且清除后回到探测 |
| `TestThemeColorModeFollowsDetectedCapabilities` | `codingagent/modes/interactive/theme/theme_detection_test.go` | 端到端：iTerm2 → dark `#213b49`、light `#dfe7ec` 的 `48;2;` 精确色；256 终端 → 索引 23 / 189 |

仅用公开 API（`GetCapabilities`/`SetCapabilityOverrides`/`ResetCapabilitiesCache`）书写，故 stash 掉源码修复后仍可编译。红→绿验证：

```
git stash push tui/terminal_image.go
go test ./tui/ -run TestGetCapabilities -count=1      # FAIL（全部 truecolor 用例）
go test ./codingagent/modes/interactive/theme/ -run TestThemeColorModeFollowsDetectedCapabilities -count=1
#   FAIL: dark userMessageBg = "\x1b[48;5;23m", want truecolor #213b49
git stash pop
```

同一环境的实测亦证实修复：修复后 pig 与 pi 在 light/dark 两套主题下输出的用户消息底色逐字节一致。

## 影响面核对

`TrueColor` 只被主题颜色模式消费，故无需其它改动；顺带确认 hyperlinks/images 消费点（`markdown.go`、`core/tools/render_utils.go`、`tui_alt_screen.go`、`settings_selector.go`）不受影响。修复后在本机（`TERM_PROGRAM=iTerm.app` + `COLORTERM=truecolor`，即 truecolor 路径）跑 `go test ./tui/... ./codingagent/... -count=1` 全绿，无测试因输出从 `38;5;` 变 `38;2;` 而失配。
