# /fork 后发消息没反应：Fork nil options panic 吞掉会话替换的 UI 重绑

- **日期**：2026-10-10 17:00 CST
- **状态**：✅ 完成。全量 `go test ./... -race -count=1 -timeout 600s` rc=0，32 包全绿（`/tmp/pig-test-out.log`）
- **现象**（用户 dogfooding 报告）：`/fork` 从消息选择器选中一条消息后，新会话里发消息完全没反应——无 Working 指示、无回复、无报错；界面停留在 fork 前的 transcript。
- **涉及文件**：
  - 生产代码：`codingagent/core/agent_session_runtime.go`（Fork 入口归一化 + selectedText 提取两处）
  - 回归测试：`codingagent/core/agent_session_runtime_test.go`（新增 2 个测试函数）

## 根因（两层）

### 第一层（主因）：`Fork` 缺 nil options 归一化 → panic

- `/fork` 选择器调用 `m.runtimeHost.Fork(context.Background(), entryID, nil)`（`interactive_mode.go:6679`），**options 传 nil**。
- pi 的 TS 语义是 `options?.position` / `options?.withSession` 全程可选链；pig 移植时 `SwitchSession`（:238）与 `NewSession`（:301）都在入口做了 `if options == nil { options = &...{} }` 归一化，**唯独 `Fork` 漏了**——`options.Position` 处有守卫，但三个替换分支（persisted 空 target :439、persisted branched :474、非持久化 :511）的 `finishSessionReplacement(ctx, options.WithSession)` 参数求值全部裸解引用。
- panic 发生在 `r.apply(result)` **之后**：runtime 已指向新 session、旧 session 已 teardown，但 `rebindSession`（interactive 的 `rebindCurrentSession`）从未执行。fork goroutine 的 `pigutils.RecoverPanic("interactive.fork")` 把 panic 静默吞掉（只写 `~/.pig/agent/logs/panic-*.log`）。
- **后果链**：UI 订阅仍挂在已 Dispose 的旧 session 上、transcript/footer 未重建；用户再发消息时 `m.session()` 返回新 session、agent run 正常执行且写盘，但所有事件渲染（subscribeToAgent）都订阅在死 session 上 → 用户看到「发消息没反应」。
- **实锤**：用户真机 panic 报告 `panic-20261010-164207.843015832-1.log`，component `interactive.fork`，栈帧与探针逐帧一致（`agent_session_runtime.go:474`，`Fork(..., 0x0)`）。

### 第二层（次因）：selectedText 双形态丢失（双形态家族第六例）

fork 后 pi 会把选中消息文本回填编辑器（`result.SelectedText`）。pig 的提取链有两处值/指针形态断言只兜一边，运行时构造的消息全 miss：

1. `extractUserMessageText` 对 `types.UserContent` 只读 `String` 字段（**decode-only**，运行时构造填的是 `Parts`）；且 `[]types.Content` 分支断言 `*types.TextContent`（指针形态），而 `NewTextContent` 返回**值形态**。
2. `Fork` 内 `messageEntry.Message.(*types.UserMessage)` 指针断言 miss（运行时为值形态）→ 走 fallback 传整个 message（恒提取不出文本）。pi 原文只有一行 `extractUserMessageText(selectedEntry.message.content)`，无双分支。

两层修完后 selectedText 才真正生效。仓库教训「所有 type switch 断言必须双形态兜底」第六次应验——前五例见 AGENTS.md 移植教训速查。

## 修复

1. `Fork` 入口归一化（对齐 SwitchSession/NewSession 既有 idiom）：
   ```go
   if options == nil {
       options = &ForkOptions{}
   }
   ```
   `options != nil && options.Position != ""` 随之简化为 `options.Position != ""`（行为不变）；函数注释声明 pi 可选链语义与 nil 调用方。
2. `extractUserMessageText` 重写为与 `messageTextContent` 的 UserMessage 分支同语义：`Parts == nil → String`，否则 `ai.ContentText(Parts, "")`（值形态断言）。
3. `Fork` 的 UserMessage 断言改为 switch 双形态（`types.UserMessage` / `*types.UserMessage`），fallback 分支保留。

## 回归测试（stash 验证：先红后绿）

`codingagent/core/agent_session_runtime_test.go`，均走生产形状 harness `newRuntimeHostForTest`（真实 createRuntime 工厂 + mock streamFn）：

- **`TestForkNilOptionsReplacesSessionAndRebinds`**（主回归，复刻用户路径）：persisted 会话 → prompt 一次 → `Fork(ctx, entry, nil)`。断言：不 panic、`result.SelectedText == "first message"`、`rebindSession` 被调用、分支产生**新** session 文件、fork 后 prompt 正常跑完。修复前在 ：474 panic 红（与用户 panic 逐帧一致）。
- **`TestForkNilOptionsInMemorySession`**：同一 nil-options 契约在非持久化分支（session manager 复用路径）。修复前同样 panic 红。

验证顺序：测试先落在未修代码上跑出 panic（红）→ 修复 → 两测绿 → 全量 `-race` 32 包 rc=0。

## 已知面（本次不动）

- fork goroutine 内 `m.editor.SetText(result.SelectedText)` / `showStatus` 为裸 UI 突变（未走 `mutateUI`）——与 submit worker 的现状并发约定一致（`interactive_mode.go` submit 路径同样直接突变），属既有模型灰区，不扩大本次改动面；panic 收容 `RecoverPanic("interactive.fork")` 已在位。
- `targetLeafID == ""` 分支（persisted 空 target）与 branched 分支共享同一处归一化，行为由编译器保证，未单测覆盖（实践中 fork 候选恒有 parent——session header entry 是首 entry）。

## 同批登记

- AGENTS.md `docs/fix/` 现有文件列表补一行。
- 用户可见面无文档变化（bug 修复），README / verify-matrix 不动。
