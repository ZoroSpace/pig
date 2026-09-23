# addMessageToChat 值/指针双形态断言丢弃 compaction summary / custom message / branch summary

- **日期**: 2026-09-22 23:15(修复落盘 mtime)
- **状态**: 已修复,回归测试锁定
- **涉及文件**: `codingagent/modes/interactive/interactive_mode.go`(`addMessageToChat`)、`codingagent/modes/interactive/interactive_mode_entry_appended_test.go`(回归)

## 现象

`addMessageToChat` 的 `RoleCustomMessage` / `RoleCompactionSummary` / `RoleBranchSummary` 三个分支只匹配**指针**形态(`*harness.CustomMessage` 等),而消息构造器 `harness.CreateCustomMessage` / `harness.CreateCompactionSummaryMessage` / `harness.CreateBranchSummaryMessage` 与 `core.SessionEntryToContextMessages`(会话重建路径)返回的都是**值**形态 → 类型断言 `!ok` → 静默 `return`,消息不渲染。

受影响路径(均为 pig 用户可见):

1. **会话重建/resume**(renderSessionEntries → SessionEntryToContextMessages → 值):压缩摘要、分支摘要、custom message 在 transcript 里整条消失;
2. **S8-5 新增的 boundary compaction entry_appended 渲染**(本批 S8-5 开工时写测试当场爆红暴露);
3. **message_start 实时事件**(appendCustomMessage 存值形态)。

本批之前的 compaction_end 手动压缩路径恰好传值也丢——但 S5b 冒烟未覆盖压缩场景,故潜伏至今。与 docs/fix/2026-09-11-tool-execution-live-output-empty.md 同族(第四例双形态)。

## 修复

三个分支各补值形态回退:`assert 指针 → fail → assert 值 → 取址`(取址副本仅供渲染,不改写存储)。与 2026-09-11 修复的「消费端双形态兼容」先例一致。

## 回归测试与验证方式

- `TestEntryAppendedBoundaryCompaction` / `TestEntryAppendedCustomMessageDisplay`(`codingagent/modes/interactive/interactive_mode_entry_appended_test.go`):修复前双红(summary 与 custom message 均缺失)、修复后全绿——即 stash 验证(测试先于修复落盘,红→绿已实证)。
- 全仓 `go test ./... -race` 由 S8-5 收口批统一执行。

## 备注

- pi 无此问题(JS 对象无值/指针二分);pig 双形态源自「AgentMessage 构造器值接收者 + 部分路径存指针」的历史约定,消费端双形态兼容是既定处置。
- entry_appended 的 nil-map 惰性初始化(`entriesRenderedByBoundaryCompaction`)为同批顺手加固:newSeamTestMode 等直构路径绕过 NewInteractiveMode 时,pi 字段初始化器语义由惰性分配补齐。
