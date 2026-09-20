# tmux 下主题自动检测全链路调查：pig / opencode / tmux（2026-09-19）

**日期**：2026-09-19 18:17
**状态**：已收口——**pig 无缺陷**；根因为 tmux（≤3.7c 最新正式版）既不查询也不转发终端主题，应用自动探测在 tmux 内注定失败；tmux **3.8（未发布）**才补全链路，届时 pig 零改动即可自动跟随
**涉及文件**（pig 侧只读调查，未改动 pig 代码）：
- `codingagent/modes/interactive/theme/theme.go` / `theme_controller.go`（探测链）
- `tui/tui.go`（OSC 11 / 996n 查询 + 2031 订阅实现）
- `codingagent/modes/interactive/interactive_mode.go`（`buildPigLogo`）
- `codingagent/assets/theme/dark.json` / `light.json`（tool 卡片底色 token）
- 外部规格书：sst/opencode、sst/opentui、tmux/tmux（源码 + CHANGES；克隆查阅后已删除，路径以 GitHub 为准）

## 起因（三个观察，逐层收敛）

1. pig 在 **tmux 里**启动 logo 是**浅色**，**tmux 外**不是；
2. 用户以为当时是"白色主题"，但 **tool 框背景是黑色**；
3. 打开 **opencode 验证：同样在 tmux 里变黑**，而系统（外层终端）主题是白色——说明不是 pig 一家的问题。

三个观察最终指向同一个根因，且各自对应一层证据（pig 代码 → opencode 源码 → tmux 源码 + 实测）。

---

## 第一层：pig 的主题自动探测链（为什么"浅色 logo"= dark 主题）

源码位置：`codingagent/modes/interactive/theme/theme_controller.go:153-183`（`ApplyFromSettings`）、`theme/theme.go:1093-1130`（`DetectTerminalBackgroundTheme` / `DetectTerminalThemeForAuto`）、`theme/theme.go:1052-1057`（`GetThemeForRgbColor`）

主题设置为 automatic（默认）时，启动探测链（串行，各 100ms 超时）：

1. **DSR `?996n`**：新式 color-scheme 查询（kitty 0.42+ / ghostty / WezTerm 支持），应答 `997;1n`(dark) / `997;2n`(light)；
2. **OSC 11**：直接问终端背景色（`tui.go:1701` `QueryTerminalBackgroundColor`，写 `\x1b]11;?\x07`）；
3. **`COLORFGBG`** 环境变量回落；
4. 全部失败 → **默认 dark**（`theme_detection_test.go:40` 锁定："defaults to dark without terminal background hints"，confidence low）。

判定规则：背景色亮度 `≥ 0.5 → light`。

**关键等式**：logo 颜色就是主题 `text` 色（`interactive_mode.go:8152` `buildPigLogo` 注释明写 white on dark / black on light；dark.json `text=#d4d4d4` 浅白，light.json `text=#1f2328` 近黑）。所以 **"logo 浅色" ⇔ pig 正在跑 dark 主题**——它不是白主题下画了浅字，而是压根没进白主题。

**"tool 框背景黑色"的真相**：tool 卡片内容 Box 全程刷底色 token（`components/tool_execution.go:128,400-405`），dark.json 三值为 `#303235 / #2a2c2f / #3a3c3f`（深灰近黑），白底终端上看起来就是黑框。light.json 对应值是 `#e8e8f0 / #e8f0e8 / #f0e8e8`（浅紫/浅绿/浅粉），256 色量化也仍是 `48;5;189` 一类浅色——**"白主题画黑底"不存在，是当时根本就是 dark 主题**。

**tmux 3.3a 内实测**（python 探针读写 `/dev/tty`，注意 stdout 重定向到文件时必须用 `/dev/tty` 才能拿到 pty）：

```
OSC11 reply: b''      ← 零应答
996n  reply: b''      ← 零应答
TERM=tmux-256color COLORTERM=None
```

顺带发现：**COLORTERM 过不了 tmux server**（默认 `update-environment` 不含它，server 环境冻结在首次启动时）→ pig 的 TrueColor 检测（目前是 settings 覆盖制，自动探测 Phase 8 才做）拿不到 truecolor → tmux 内还会退化到 256 色模式。与主题误判无关，但颜色保真度略降。

---

## 第二层：opencode 同因（源码级实证，不是巧合也不是抄袭）

本地二进制（Bun 编译产物，内嵌 JS 可直接 grep）找到 3 处 `]11;?`——opencode 同样发 OSC 11 查询。源码链条：

- `sst/opencode` `packages/tui/src/context/theme.tsx`：`resolveSystemTheme()` → `renderer.getPalette()` → `terminalMode(colors)` 判 dark/light；store 默认 `mode: "dark"`（:94）；探测失败回退默认。
- 渲染器在 `sst/opentui` `packages/core/src/lib/terminal-palette.ts`：向终端**裸发** `\x1b]10;?\x07` `\x1b]11;?\x07` `\x1b]12;?\x07`。注释明说：

  ```ts
  // tmux handles plain OSC 10/11/12 only; it has no OSC 13-17/19 handlers or reply routing.
  // OSC 10/11/12 are plain tmux handlers. DCS passthrough does not help other special colors.
  ```

  即 opentui **知情且绕不开**：tmux 会截获这三条；只有 OSC 4（16 色调色板）走 DCS `ESC Ptmux;` 包裹透传（`wrapForTmux`，:83-89）。
- 判定阈值同款：`packages/tui/src/theme/index.ts:353` `terminalMode` 用 `0.299r + 0.587g + 0.114b > 0.5 ? light : dark`，与 pig `GetThemeForRgbColor` 一致。
- 兜底同向：`RGBA.ts:6` `DEFAULT_BACKGROUND_RGB = [0,0,0]`（黑）+ 默认 dark 主题。

**结论**：两家实现细节有差异（opencode 多 OSC 4 调色板 + DCS 透传 + 997 通知监听；pig 多 996n 查询 + COLORFGBG 回落），但探测手段同源（OSC 11 + 0.5 亮度阈值）、兜底方向相同（dark）、被 tmux 挡住的方式相同。pi 放 tmux 里同样会黑。

---

## 第三层：tmux 的真相（3.7c 源码编译 + 假终端实测，本次调查的核心增量）

### 实验设计

"外层终端"不可控，就**造一个说真话的**：

1. 源码编译最新正式版 tmux 3.7c（`apt install libevent-dev libncurses-dev bison` → `configure --prefix && make`；本机 Debian 12 仓库的 tmux 3.3a 太老）；
2. python pty **假终端**：fork tmux 客户端挂载其上（winsize 120x40、`TERM=xterm-256color`、`COLORTERM=truecolor`），自动应答 OSC 11 查询为 `rgb:ffff/ffff/ffff`（**白**）、按需应答 `?996n` 为 `997;2n`（**light**），并在 t=6s/12s 主动推送 unsolicited `\x1b[?997;2n` 主题通知；
3. 在 tmux 3.7c 的 pane 里跑探针：发 996n、设 `\x1b[?2031h` 订阅主题通知、发 OSC 11，各等 2-4s 收应答。

### 四条路径全负（tmux 3.7c 实测）

| 探测路径 | 结果 |
|---|---|
| tmux 向外层终端查询背景/主题 | **从不查询**（假终端日志无一条查询） |
| pane 内 OSC 11 查背景色 | **无应答** |
| pane 内 DSR `?996n` | **无应答** |
| 外层推送 `997;2n` 主题通知 ×2 | tmux **收到了**（假终端日志有记录），但**不转发**给已设 `2031h` 的 pane（pane 零收到） |

即：应用在 tmux 里无论主动问（OSC 11/996n）还是订阅（2031），都拿不到真实主题。**这一步把"opencode 变黑"从推测升级为实测定论。**

### 实验坑（复现时注意）

- 假终端 env 里 `TMUX=""`（空字符串）仍算"已设"→ tmux 客户端拒绝嵌套启动（"sessions should be nested with care"），表现为 pty 日志全空。必须 `env.pop("TMUX")`。
- 二进制 grep 陷阱：`grep ']11;'` 不加 `-F` 时是正则，会撞上 `x[11];` 之类的数组下标产生海量误报；用 `grep -aoF ']11;?'` 精确匹配。
- tmux 3.3a（本机系统版）连 pane 查询都不应答；3.7c 也不应答——**不应答不是版本太老的问题，是正式版压根没实现**。

### 版本时间线（tmux CHANGES 实证，GitHub master）

- **≤3.3a**：完全没有主题概念。
- **3.4**：开始向外层终端查询 OSC 10/11（`client->tty.fg/bg`），但只供自用（如 OSC 52/复制场景），不下发。
- **3.6**：解析 **mode 2031** 主题通知（Jonathan Slenders / prompt-toolkit 作者贡献，issue 4353），"automatically report dark or light theme"——但实测**不转发给 pane**（见上表）。
- **3.7 / 3.7a-c**：theme 处理时序修复若干，无质变。
- **3.8（未发布，master）**：才真正解决，CHANGES 原文三连：
  - "*a new **theme option** controls whether tmux detects the terminal theme, uses terminal colours, or forces light or dark*"；
  - 内置亮暗主题（256 色以上终端）；
  - "***the terminal's own theme is reported to panes** instead of being guessed from the background*"（Ayman Bagabas, issue 5343）。
- **master 源码佐证 3.8 行为**（`input.c:3117` `input_osc_11`）：pane 的 OSC 11 查询将走 `window_pane_get_bg` → pane palette（应用还可用 OSC 11 **set** 反向告知 tmux 背景，带 `PANE_THEMECHANGED`）→ `tty_default_colours` → `window_get_bg_client`（client->tty.bg，3.4 起从外层查得）；`window_pane_get_theme` 优先采信 2031/996 上报的主题。整条链路在 master 已就位，等发版。

---

## 对 pig 的影响与结论

1. **pig 无缺陷、无移植偏差**：探测链忠实 pi；tmux ≤3.7 内探测失败 → 默认 dark 是规范行为，pi 同场景同结果。无需登记 fix。
2. **现阶段唯一可靠方案：固定主题**。pig 用 `/theme`；注意 settings 的 `"theme": "<light>/<dark>"` 双主题形式在 tmux 里探测恒 dark，等价于固定 dark，想白底就别用 automatic。
3. **tmux 3.8 发布后**：pig **零改动**即可自动跟随——应用侧协议已全实现（`tui.go:1329` `SetTerminalColorSchemeNotifications` 在 auto sync 开启时写 `\x1b[?2031h` 订阅、:1701 起的 996n 查询 + 997 监听），tmux 3.8 把真实主题转发进 pane 后整条链自然打通。opencode 同理（监听 997）。
   - **留一个观察项**：3.8 发布后用本文件的假终端实验复跑一遍，验证转发链路（996n 应答 / 2031 通知转发 / OSC 11 应答走 tty.bg）逐条转绿。
4. **opencode 配置留档**（本机已配好 github light）：
   - `~/.config/opencode/tui.json`：`{"$schema": "https://opencode.ai/tui.json", "theme": "github"}`（新键位；opencode.json 顶层 theme 是 legacy，会被自动迁移）；
   - `~/.local/state/opencode/kv.json`：`{"theme_mode_lock": "light"}`——**路径坑：state 是 `~/.local/state`（xdgState），不是 `~/.local/share`**（`Global.Path.state = xdgState + "opencode"`）；mode 没有配置键，锁只能走 kv；
   - github 主题是双变体（`darkBg #0d1117` / `lightBg #ffffff`，token 各带 `{dark, light}`），不锁 light 在 tmux 里拿到的是暗变体；
   - `/themes` 选择器的确认写 kv，而 `config.theme` 优先级更高——换主题要改 tui.json，光在 picker 里选重启会弹回。
   - UI 等价操作：`Ctrl+P` 命令面板 → "Switch to light mode" / "Lock theme mode"。

## 状态链

- 2026-09-19：调查收口，本文档写入；opencode/opentui 克隆与 tmux 源码残留已从 /tmp 清除（含 tmux 3.7c 编译产物）。
- 待办观察项：tmux 3.8 发布 → 复跑假终端实验 → pig 在 tmux 内自动主题跟随实测。
