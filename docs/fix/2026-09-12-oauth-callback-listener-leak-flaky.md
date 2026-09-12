# 2026-09-12 OAuth 回调服务器端口泄漏 flaky（bind: address already in use）

- **日期**：2026-09-12（修复落盘时间，取自文件 mtime 校正）
- **状态**：✅ 已修复收口（C2 批内发现并顺带修复）
- **涉及文件**：`ai/auth/oauth_anthropic.go`、`ai/auth/oauth_openai_codex.go`、回归测试 `ai/auth/oauth_openai_codex_test.go`（`TestCodexOAuthCallbackServerRestart`）

## 现象

C2 批全仓 `-race` 回归中 `ai/auth` 包偶发失败：

```
--- FAIL: TestAnthropicOAuthManualCodePromptPath (0.00s)
    oauth_anthropic_test.go:155: login failed: listen tcp 127.0.0.1:53692: bind: address already in use
```

前一个测试（`TestAnthropicOAuthManualCallbackLogin`）已正常退出，端口却仍被占用。A2 交付以来潜伏，C2 新增测试改变了包内调度时序后首次暴露。

## 根因

两处 OAuth 回调服务器（anthropic `startAnthropicCallbackServer`、codex `startCodexLocalOAuthServer`）都是同一模式：

```go
listener, _ := net.Listen("tcp", host+":"+port)
go func() { _ = httpServer.Serve(listener) }()
...
// 关闭时只调：
_ = httpServer.Close()
```

`http.Server.Close()` 只会关闭 **Serve 已经注册到 `srv.listeners` 的 listener**。注册发生在 `Serve()` 进入后的 `srv.trackListener(&l, true)`——而 `Serve` 是另一个 goroutine。当 close 与 Serve 启动竞速（close 先到），`Close()` 找不到任何已注册 listener、立即返回，**原始 `net.Listener` 永远没人关闭**，端口保持 bind 状态，同进程内下一次同端口 bind 即 EADDRINUSE（anthropic 的登录重试、codex 二次登录、以及测试套件里的下一个用例都会撞上）。

附带问题：`loginAnthropic` 里还有个 `<-ctx.Done()` 时才 close 的 watcher goroutine，ctx 用的是 context.Background() 时永久泄漏（同修复顺带收敛：watcher 改走 `server.close()`）。

## 修复

两个 server 各加 `close()` 方法：**先关原始 listener（保证端口释放），再 `http.Server.Close()`**（收尾活跃连接）；生产流程 `loginAnthropic` 的 defer、ctx watcher 与测试断言全部改走该方法。codex server 结构体补 `listener` 字段。

## 回归测试

- `TestCodexOAuthCallbackServerRestart`（`ai/auth/oauth_openai_codex_test.go`）：启动 → 立即 close → 再启动，第二次必须拿到活 listener。
- **stash 验证**：把 `close()` 临时退回旧实现（只调 `httpServer.Close()`），该测试循环 40 次全部失败（40/40 `restarting the callback server failed: port 1455 not released`）；恢复修复后 PASS。因是竞态窗口，旧代码下失败概率近确定（close 与 Serve 的调度间隔极短）。
