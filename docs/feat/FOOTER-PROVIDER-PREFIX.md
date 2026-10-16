# Footer 右下角始终显示 provider 前缀（pig-only 偏差）

- **日期**：2026-10-16
- **状态**：已完成
- **类型**：feat（pig-only 偏差，pi 无对应物）
- **涉及文件**：
  - `codingagent/modes/interactive/components/footer.go`（显示规则）
  - `codingagent/modes/interactive/components/footer_width_test.go`（回归测试）

## 背景

pi 上游 footer 右下角的模型名只有在**可用 provider 数 ≥ 2** 时才加 `(provider)` 前缀（`getAvailableProviderCount() > 1`，`packages/coding-agent/src/modes/interactive/components/footer.ts:249`）。单 provider 用户（如只配了 volcengine-plan 一把 key）右下角永远只看到模型名，看不到当前走的是哪家 provider。

## 用户需求

即使只有一个 provider 也显示 `(volcengine)`——右下角始终可见当前 provider。

## 实现

`footer.go` 的 `Render` 中去掉 `> 1` 门控：只要 `state.Model != nil` 就把右侧行改为 `(provider) model [• thinking level]`。**宽度回退保持不变**：左侧统计 + 前缀放不下时仍回退为纯模型名（窄终端无回归）。

可用 provider 计数本身（`FooterDataProvider.SetAvailableProviderCount` / `updateAvailableProviderCount`，pi parity 面 + 扩展视图接口）原样保留，只是 footer 不再用它做显示门控。

## 回归测试

`footer_width_test.go` 新增 `TestFooterProviderPrefixAlwaysShown`，三个子测试：

1. **单 provider 也显示前缀**（本偏差核心，stash 验证红→绿：还原 `> 1` 门控后此用例变红）
2. 多 provider 同样显示（pi 原行为保持）
3. 过宽回退：前缀放不下时丢弃前缀、保留模型名

## 偏差登记

AGENTS.md「pig-only 偏差清单」第 13 条。同步批遇到 footer 右侧渲染面一律跳过上游回改。
