# 2026-09-22 启动 logo / --version 停留在 v0.86.0（S7 漏 bump）

**日期**：2026-09-22 11:13
**状态**：已修复收口
**涉及文件**：`codingagent/config.go`（`Version` 常量）、`codingagent/config_test.go`（回归锁定）

## 现象

用户 dogfooding 发现交互模式启动 logo 版本号显示 `v0.86.0`，而 S7 批（2026-09-21）已完成 pi v0.86.0 → v0.86.1 基线同步。

## 根因

不是移植偏差：logo（`buildPigLogo(currentTheme, m.version)`）、`--version`、changelog、telemetry、bug report 全部消费唯一的 `codingagent.Version` 常量，一处带动全部——问题只是 **S7 批的增量清单里没有「bump Version 常量」这一项**，验收门（build/vet/gofmt/-race）对此无感知，于是 0.86.0 静默存活。

## 修复

- `codingagent/config.go`：`const Version = "0.86.0"` → `"0.86.1"`，注释补 bump 纪律（每次 pi 基线同步同批必改）。
- `codingagent/config_test.go` 新增 `TestVersionTracksPiBaseline`：把 `Version` pin 到当前跟踪的 pi tag（`0.86.1`）。下次同步批若再漏 bump，全量回归直接红，错误信息指明动作。

## 回归验证

- `go test ./codingagent/ -run TestVersionTracksPiBaseline -count=1`：修复后 PASS。
- stash 验证（`git stash push -- codingagent/config.go` 回到 0.86.0）：FAIL，错误信息 `codingagent.Version = "0.86.0", want "0.86.1"`，随后 stash pop 恢复。

## 边界

- pig 的 `Version` 是手写常量（pi 从 package.json 读），ldflags 注入随 packaging 批落地；本测试在 ldflags 化后需同步调整 pin 方式。
- 会话 JSONL、线格式不含版本号，此漏 bump 无兼容性影响，仅显示面。
