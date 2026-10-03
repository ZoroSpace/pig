# BUILTIN-THEME-PALETTES：新增内置主题 One Dark Pro / GitHub Light（用户插入小批，2026-10-03）

> **⚠️ 已撤销（2026-10-03 同日，用户拍板）**：两个主题整体删除——`one-dark-pro` / `github-light` 不再是内置主题，内置集合回归 pi 原样 `dark` / `light`（默认仍为 system）。`BuiltinThemeNames` 回落两项、first_time_setup 选项恢复 System→Dark→Light、assets/testdata JSON 已删。pig-only 可选 token 机制（HasFgToken/FgOptional）保留为通用能力：规则线 token（userMessageRule/toolSuccessRule/toolErrorRule）现无内置主题定义，全部经 FgOptional 回落状态色；用户此前对两主题的微调（accent/mdListBullet 等）随之失效。存量 settings 里 `"theme": "one-dark-pro"` / `"github-light"` 会静默回落 system（InitTheme 既有语义）。本文其余内容仅存档。

## 需求

用户要求：dark 主题改为 One Dark Pro 配色、light 主题改为 GitHub Light 配色；随后改口——**不替换**现有 `dark.json` / `light.json`（保留 pi 原色板），新建两份主题 JSON，代码里接入新的。

## 交付

### 新色板（`codingagent/assets/theme/`，go:embed 自动收录；testdata/theme/ 同步副本）

- **`one-dark-pro.json`**（appearance dark）：One Dark Pro 标准色——fg `#abb2bf` / bg 族 `#282c34` 系、red `#e06c75`、green `#98c379`、yellow `#e5c07b`、blue `#61afef`、purple `#c678dd`、cyan `#56b6c2`、comment `#5c6370`。
- **`github-light.json`**（appearance light）：GitHub Light（Primer）标准色——fg `#1f2328`、muted `#59636e`、accent `#0969da`、success `#1a7f37`、danger `#cf222e`、attention `#9a6700`、border `#d0d7de`。

### token 映射原则（pig-only 裁决，非 pi 上游）

- 结构与 pi 内置主题同构（同名 token 全覆盖），值尽量直接引用标准色；
- 深色底族（userMessageBg / customMessageBg / toolPendingBg / toolSuccessBg / toolErrorBg）取对应标准色与底色 25% 混合（如 blueBg `#364d63` = `#61afef` 25% 上 `#282c34`）；searchMatchBg 取 VS Code One Dark find-match `#42557b` / GitHub 搜索黄 `#fff8c5`；
- syntax 族按各家主题语义映射（One Dark：keyword=purple、function=blue、string=green、number/type=orange/yellow、operator=cyan；GitHub Light：keyword=red、function=purple、string=深蓝 `#0a3069`、type=绿 `#116329`）——与 pi 内置主题的 syntax 映射（keyword=blue 等）**有意不同**，这是「用目标主题配色」的本意；
- thinking 等级阶梯 off→max 保持「冷→热」渐进：One Dark 走 gray→cyan→blue→purple→red→orange→tomato（`#5c6370`→`#ff6347`），GitHub Light 走 gray→blue→深紫→紫→粉→橙→red（`#8c959f`→`#cf222e`）；
- `customMessageBg` / `blueBg` / 背景族为自配近似值（目标主题无直接对应物），非逐字标准。

### 代码接线

1. `theme.go`：新增导出 `BuiltinThemeNames = ["dark", "light", "one-dark-pro", "github-light"]`；`getBuiltinThemes` 循环装载（原 dark/light 两个字面量展开）；`GetAvailableThemesWithPaths` 按该表列出（system 仍钉首）；`startThemeWatcher` 的跳过名单由 dark/light 字面量改为 `isBuiltinThemeName`（新增 helper），新主题不会被当 custom theme 误 watch。
2. `components/first_time_setup.go`：首次安装主题选项插为 System → **One Dark Pro → GitHub Light** → Dark → Light（新色板置于 pi 原色板之前，即「promoted」位）。
3. `/settings` 主题子菜单与 `/theme` 选择器走 `GetAvailableThemes()`，自动收录，无需改。

### 默认主题说明

默认主题仍为 **system**（pi v0.99.1+ 语义，生成式配色）；新主题改变的是：可选列表、首次安装推荐项、以及显式选择 `one-dark-pro` / `github-light` 时的渲染。settings 值即主题名（`{"theme": "one-dark-pro"}`），磁盘 theme 目录（`~/.pig/agent/themes/`…）优先于 embedded，同名可覆盖。

## 测试

- 新增 `theme_builtin_palettes_test.go` 3 函数：两新主题经 `GetThemeByName` 装载（embedded 路径）、appearance 断言（dark/light）、accent/text 逐 hex 钉值（`#c678dd` / `#0969da` / `#abb2bf` / `#1f2328`）、GetAvailableThemes 收录；dark/light 存量装载回归。
- 全量 `go test ./... -race -count=1 -timeout 600s` 单次 rc=0 31 包（/tmp/pig-test-out-palettes.log）。
- tmux 真彩冒烟（`COLORTERM=truecolor` 注入，mock 无需 provider）：settings 写 `{"theme":"one-dark-pro"}` 启动 → capture-pane -e 出现 `38;2;75;82;99`（`#4b5263` dim）/ `38;2;92;99;112`（`#5c6370` muted）；切 `github-light` → `38;2;140;149;159`（`#8c959f`）/ `38;2;89;99;110`（`#59636e`）/ `38;2;154;103;0`（`#9a6700`）/ `38;2;5;80;174`（`#0550ae`）；两主题启动渲染正常、/exit 干净退出。tmux 无 COLORTERM 时落 256 色量化（既有能力检测语义，非本批范围）。

## BLOCK-BORDERS：消息/工具块改「上下边框、去填充」（同日追加，用户拍板方案 A）

用户反馈新主题下用户消息块（整条蓝）与工具调用块（整块灰/绿/红）「填满整块」难看，要求只留边框。盘点（2026-10-03）：pig 整块铺底的渲染共三族——① 用户消息（`userMessageBg`，user_message.go Markdown BgColor）；② 工具块（`toolPendingBg`/`toolSuccessBg`/`toolErrorBg`，tool_execution.go contentBox/contentText + core/tools/renderers_edit.go getEditHeaderBg）；③ 自定义消息族（`customMessageBg`，custom_message/custom_entry/branch_summary/compaction_summary/skill_invocation 五组件）。`selectedBg`/`searchMatchBg` 为选中/命中高亮，保留。仓库先例：`!` bash 块（bash_execution.go）本就是上下 DynamicBorder 无填充。

改造（用户拍板方案 A：渲染层改，主题 JSON 不动，老 dark/light/system 同样受益）：

1. **tool_execution.go**：contentBox/contentText 的 bgFn 铺底删除（Box/Text nil bgFn 只留 padding），新增 `topBorder`/`bottomBorder`（DynamicBorder）+ `borderStateKey` 字段——pending=muted、settled success=success、settled error=error，色键随 updateDisplay 更新、渲染期读取（与组件既有线程模型一致：updateDisplay 无锁改、Render 持锁读，色闭包不加锁）。仅 default shell 与 fallback shell 有边框；self-render shell（工具自带框架）与图片半区不变。`getEditHeaderBg` 删除（pi 映射以注释留档），EditCallRenderComponent SetBgFn(nil)。
2. **user_message.go**：Markdown BgColor 删除，rebuild 改为 Clear + [上 border 规则线, Markdown, 下 border 规则线]（色键 "border"）；OSC 133 shell-zone 包首尾行的语义不变（zone start 落在上规则线、end 落在下规则线）。
3. **自定义消息族**：custom_message.go 重构为 rebuild 全量重建（custom renderer 自带样式时不套边框，default 路径 spacer+上下边框+无填充 Box，色键 "customMessageLabel"）；branch/compaction/skill 三组件由「值内嵌 tui.Box」改「内嵌 tui.Container + 内部无填充 contentBox + 上下边框」，点击展开的 MouseRegion 移入 contentBox；custom_entry.go 失败兜底 Box 去填充。
4. **github-light 紫改淡蓝**（用户追加反馈）：文件名/路径走 `accent` token、输入框边框走 thinking 等级色（interactive_mode.go updateEditorBorderColor → GetThinkingBorderColor），github-light 原紫色来源即 thinking 阶梯紫/粉系——accent `#0969da`→`#218bff`（淡蓝）、thinking 阶梯改蓝色渐进、mdCode violet→blue。one-dark-pro 未动。**同日二调（用户反馈输入框蓝太亮）**：thinking 阶梯整条去饱和调淡——off `#8c959f` → minimal `#a9c6e8` → low `#8db4dd` → medium `#6f9fd0` → high `#5889c2` → xhigh `#4674b3` → max `#3560a5`。**同日三调（用户反馈文件名/路径太亮）**：accent `#218bff`→`#5889c2`（与 thinking 阶梯同族的柔和蓝）。

**声明 pig-only 行为偏差**：pi 上游消息/工具块为全宽背景填充设计（user-message.test.ts 钉 3 行 + bg reset；tool-execution bgFn 语义），pig 改为边框框架，后续 pi 同步批遇到这些面一律跳过（同 /bug 先例）。测试：user_message_test 首子测按 5 行边框形态重写（pi 3 行填充形态声明作废）；新增 tool_execution_border_test.go（规则线状态色 + 全帧无 `48;2;`/`48;5;` 背景 SGR 断言）；collapsible 测试 `&component.Box` 改传组件本体。

验收：全量 -race 单次 rc=0 31 包（/tmp/pig-test-out-borders.log）；tmux 真彩 + memleak mock server（models.json memprobe provider）全链路：用户消息块上下规则线 `38;2;208;215;222`（`#d0d7de` border）+ 正文 `#1f2328` 无背景，全帧 0 个背景转义、0 个 `#8250df` 紫转义，mock 回合正常完成。

**高度追平（同日二批，用户反馈「块比之前高」）**：加规则线后块内部还留着原纵向 padding（用户消息 Markdown paddingY=1、工具块 contentBox/contentText、custom_message box、branch/compaction/skill contentBox），单行内容块比填充时代多 2 行。四处纵向 padding 统一归零——总高度恰与填充形态持平（原上下空白行被两条规则线 1:1 取代），对齐 bash 块的紧凑先例。user_message 首子测钉 3 行形态；全量 -race rc=0 31 包（/tmp/pig-test-out-blockheight.log）。

**规则线醒目化（同日三批，用户反馈用户块规则线不显眼）**：`NewUserMessageComponent` 新增 pig-only `ruleColor` 参数（nil = 回落 theme "border" 灰），interactive_mode 经 `userMessageRuleColor()` 传入发送瞬间的 thinking 等级边框色——即输入框当时的颜色，用户块与输入框视觉匹配；probe 测试主题 fixture 补 `thinkingOff` token（off 等级规则线）。回归：tool_execution_border_test 断言注入色/回落色两路；全量 -race rc=0 31 包（/tmp/pig-test-out-rulecolor.log）。

**可选规则线 token + 固定蓝（同日四批，用户三连反馈收敛）**：① theme 包新增 pig-only `HasFgToken`/`Theme.FgOptional`（token 在主题里定义则用之，否则回落另一个 token；`ThemeColor = string`，任意新 key 直通 fgAnsi，无需白名单）+ 包级 `FgOptional`（走 Current）。② 用户块规则线**固定蓝**：`userMessageRuleColor` 不再随 thinking 等级，改 `FgOptional("userMessageRule", "border")`——github-light `userMessageRule: #6f9fd0`（与输入框 medium 档同色），one-dark-pro `userMessageRule: blue`（#61afef）。③ 文件名/路径 accent 第四调 `#5889c2`→`#6f9fd0`（与输入框同蓝，用户拍板「类似这样的颜色」）。④ 工具框规则线可选调暗 token：`toolSuccessRule: #85a893`（柔绿）/ `toolErrorRule: #c68f8f`（柔红）落 github-light，pending 仍走 muted；tool_execution 规则色改 `FgOptional(ruleToken, stateToken)`，无 token 的主题（含 pi dark fixture）回落原状态色。回归：theme_builtin_palettes_test 增 token 存在性/取值/FgOptional 两路断言；全量 -race rc=0 31 包（/tmp/pig-test-out-ruletokens.log）。

**列表标记改黑（同日五批，用户要求）**：`github-light` 的 `mdListBullet` 由 `violet` 改 `text`（`#1f2328`，Primer 正文黑）——pi/pig 的**有序列表数字与无序列表 `-` 共用同一 token**，故两者一起变黑；assets 与 theme 包 testdata 两份 json 同步（diff 为空）。回归：`theme_builtin_palettes_test.go` 新增子测 `list markers use the body text color (pig-only)`，stash 红→绿（改回 violet 报 `mdListBullet = "#8250df", want #1f2328`）。若后续只想让数字变黑而保留 `-` 的颜色，需另拆 pig-only `mdListNumber` token（`FgOptional` 回落 `mdListBullet`），届时再做。

**同日顺带修复（非本特性）**：`pig --continue` 恢复会话后工具卡片框色恒为 pending/muted（浅色主题下近黑）、卡片内工具输出不显示——恢复路径 `renderSessionItems` 与 `addMessageToChat` 的指针-only 断言与值形态解码不匹配（双形态家族第五例）。根因、修法与验证见 `docs/fix/2026-10-03-restored-tool-results-dropped.md`。
