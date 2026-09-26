# GitHub Copilot 登录重试测试 data race（CI flaky）

- **日期**: 2026-09-26 23:03（问题在 2026-09-24 的 CI run 首次暴露：run 36000455222 / job 107635545022）
- **状态**: 已修复
- **涉及文件**: `ai/auth/oauth_github_copilot_test.go`、`ai/auth/oauth_github_copilot.go`（只读参照）、`ai/auth/oauth_openai_codex_test.go`（fake clock，只读参照）

## 现象

GitHub Actions 全量 `-race` 偶发红，两份 race 报告均指向 `TestGitHubCopilotLoginRetriesThrottledPolicyAfterRetryAfter`：

1. 主测试 goroutine 在 `oauth_github_copilot_test.go:365` 读 `policyRequests` × login goroutine 在 `:345`（policy stub 闭包内 `policyRequests++`，经由重试路径 `githubCopilotFetchWithRateLimitRetry` oauth_github_copilot.go:286）。
2. 主 goroutine `t.Fatalf`（:366）触发 `t.Cleanup` 恢复 `githubCopilotOAuthDo` 包级变量 × login goroutine 仍在重试请求中读同一变量（oauth_github_copilot.go:286）。

本地 macOS `-race` 反复跑（含全量）不复现；CI 的 ubuntu 2 核 runner 必现。

## 根因

**移植笔误：stub 的首个 429 响应丢了 pi 原测试的 `Retry-After: 1` header**（pi `packages/ai/test/github-copilot-oauth.test.ts:393` 是 `jsonResponse({...}, 429, { "Retry-After": "1" })`，pig 移植时写成无 header 的 `copilotTestJSONResponse(429, ...)`）。

header 缺失 → `githubCopilotFetchWithRateLimitRetry` 回落默认指数退避 `500 * (1 << retry)` = **500ms**，而非 header 指定的 1000ms。测试编排是按 1000ms 设计的：

- `interval:1` → 首轮 poll 前 sleep 1000ms，注册于 t0+1s；`awaitCopilotSleep` #1 + `advance(1s)` 触发 ✓
- policy #1 → 429 → 退避 sleep **1000ms** 应注册于 t0+2s；`awaitCopilotSleep` #2 同步读到 1 ✓（sleepCalls 的 send 建立了 happens-before）
- `advance(999ms)` 应停在定时器触发前 1ms（login goroutine 仍停在 `deviceCodeSleep` 里，读 `policyRequests` 安全）
- `advance(1ms)` → 触发重试 → `<-done` 同步后读 2 ✓

实际退避只有 500ms → 定时器注册于 t0+1.5s → `advance(999ms)` 把它提前 499ms 触发，**login goroutine 被放出来发重试请求（写 `policyRequests`），与主 goroutine 的无同步读构成 data race**。值读到 2 时 `t.Fatalf`，cleanup 再与在途请求竞写传输 seam 全局变量 → 第二份报告。

## 为什么本地不复现、CI 必现

race 是否被报出取决于两个 goroutine 在 `advance(999ms)` 释放后的 mu 竞争：

- **本地（Case B，消毒）**：主 goroutine 在释放 `advance(999ms)` 的 `clock.mu` 后几纳秒内就到 `advance(1ms)` 的 `mu.Lock()`；login goroutine 从 channel 唤醒到 `deviceCodeNow()` 拿同一把 `clock.mu` 需要 ~µs 级调度延迟 → 主 goroutine 恒先拿锁 → mutex 释放→获取边恰好把「read@365 → write@345」排序，TSan 视为有序，不报。
- **CI（Case A，报出）**：2 核 runner 高负载，login goroutine 抢先拿 mu 并跑完整个 stub 往返完成写入 → 主 goroutine 读到 2 → Fatalf + race 报告；随后 cleanup 竞写全局变量再报一份。

## 修复

- `TestGitHubCopilotLoginRetriesThrottledPolicyAfterRetryAfter` 的首个 429 stub 补上 `Retry-After: 1`（对齐 pi），退避回到 1000ms，测试编排恢复设计时的确定性时序：`advance(999ms)` 时 login goroutine 停在 sleep 里，所有读都经 sleepCalls/done 通道与写排序，race 窗口整体关闭。
- 顺带修复同文件 `TestGitHubCopilotRefreshDoesNotRetryCatalogThrottling` 的同类笔误：pi 原stub 是 `{ "Retry-After": "0" }`（test 行 243），移植时也丢了。该测试 retryPolicy 为零值（Refresh 路径 maxRetries=0）首次 429 即返回、行为本就相同，属保真性修复，无 race 隐患。

## 回归验证

- **红**：修复前在 `advance(999ms)` 与读之间临时注入 `time.Sleep(50ms)`（非同步原语，不产生 happens-before，只放大 CI 上的调度形态）→ `go test ./ai/auth/ -race -run 'TestGitHubCopilotLoginRetriesThrottledPolicyAfterRetryAfter'` 复现 `WARNING: DATA RACE` + `policy requests after 999ms = 2, want 1`，与 CI 输出一致。
- **绿**：恢复 header 后移除注入，同命令 `-count=10` 全绿；全仓 `go test ./... -race -count=1 -timeout 600s` 29 包 rc=0（`/tmp/pig-test-out-20260924-ci-fix.log`）。
- 本修复的回归锁定即测试本体自身：header 在则时序确定，header 丢则红（stash 验证如上）。

## 教训

- fake-clock 测试的安全模式是「advance 不触发定时器 → 被测 goroutine 停在 `clock.sleep` 里（受上一条 sleepCalls send 排序）→ 再读共享状态」；一旦 advance 意外触发了定时器，被测 goroutine 就变成活跃写者，之后的所有无同步读都是 race。写这类测试时先核对每个 advance 与定时器 deadline 的相对关系。
- 同文件其他 fake-clock 测试（slow_down 的 4999/6999ms 推进、transport-failure 的 `<-done` 后断言）均已是安全模式，无需改动。
