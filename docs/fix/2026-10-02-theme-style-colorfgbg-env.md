# 2026-10-02 TestThemeStyles 受宿主 COLORFGBG 环境变量影响（测试隔离加固）

- **日期**：2026-10-02（排查时刻，见文件 mtime）
- **状态**：✅ 已修复（pig-only 测试隔离加固；产品代码零改动）
- **涉及文件**：`codingagent/modes/interactive/theme/theme_style_test.go`（TestThemeStyles 顶部 `t.Setenv("COLORFGBG", "")`）

## 现象

S11 收尾批的全量 `-race` 回归（rc=1）在 `theme` 包抓到 `TestThemeStyles/detects_the_appearance_unless_it_is_declared` 失败：`palette-only appearance = "light"`。同批 5 小时前上一会话的全量是绿的（/tmp/pig-test-out-s11-final.log），包内代码零改动。

## 根因

**环境漂移，非产品 bug**：本会话的 shell 环境多了 `COLORFGBG=0;15`（fg=0/bg=15 → Vim 索引分类为 light）。`Theme.Appearance()` 未声明 appearance 且主题是 palette-only（全部 token 是 0-15 索引、跟随终端背景）时，走 `GetTerminalColors 未设 → DetectTerminalTheme 回落链（背景 → 2031 报告 → COLORFGBG → dark）`，于是读到宿主的 light 分类。pi 的 `theme-style.test.ts` 同样存在此潜在敏感（pi 的 CI 恰好无 COLORFGBG 才绿），pig 产品代码与 pi 一致，不改产品。

## 修法

测试加固：TestThemeStyles 顶部 `t.Setenv("COLORFGBG", "")`（沿用 `theme_controller_test.go:186` 先例），断言语义回归本意「无终端信息 → 跟随主题自身 base」。

## 验证

- 修复前：`go test ./codingagent/modes/interactive/theme/ -run TestThemeStyles`（含仅跑该子测）稳定 FAIL；修复后 PASS。
- 全量 `-race` 单次 rc=0（/tmp/pig-test-out-s11-final2.log）。

## 附注

- 排查手法：`-run 'TestThemeStyles/detects_...'` 单子测仍挂 → 排除包内其他测试污染；`env | grep -i color` 直接命中。
