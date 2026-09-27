# find/grep 工具 stderr 收集 goroutine data race（CI flaky）

- **日期**: 2026-09-27（CI run 36299090645 / job 108563272760 首次暴露）
- **状态**: 已修复
- **涉及文件**: `codingagent/core/tools/find.go`、`codingagent/core/tools/grep.go`

## 现象

GitHub Actions 全量 `-race` 红，race 报告指向 `TestFindTool/should_surface_fd_glob_parse_errors`：

- 主测试 goroutine（经 `mustFail` → `WrapToolDefinition` 执行链）在 `find.go:342` 读 `stderr`（`strings.TrimSpace(stderr)`，fd 退出码非零分支）。
- stderr 收集 goroutine 在 `find.go:297` 写 `stderr`（`stderr += scanner.Text()`）。

race 是否报出取决于 stderr goroutine 的调度时序，属 CI flaky；本地复现概率低但同样存在。

## 根因

**移植时漏掉 pi 的流终结语义**。pi `find.ts` 用 node `spawn`，`child.on("close")` 只在**进程退出且 stdout/stderr 两个 stdio 流都已结束**后才触发，所以 close 回调里读 `stderr.trim()` 天然安全。pig 移植为手写 goroutine 后：

- `childDone` 通道只在 stdout 扫描结束 + `cmd.Wait()` 返回后关闭；`cmd.Stderr` 直接挂 `*os.File`（`utils.SpawnProcess` 手工管道），`cmd.Wait()` 不等该管道数据被读尽。
- stderr 收集 goroutine 完全没有被 join，主执行流在 `childDone` 后立即读 `stderr` → 无 happens-before 的裸 race。

`grep.go` 的 ripgrep 路径是同型代码（`stderr +=` 无 join，`grep.go:299` 裸读），本次 CI 只烧到 find 是测试路径巧合，两处一起修。

## 修复

两文件同法：stderr goroutine 补 `defer close(stderrDone)`，主执行流在 select `childDone` 之后、`pipes.CloseReadEnds()` 之前 `<-stderrDone` join——**对齐 pi 语义**（close 事件等两个流都结束）。join 放在 `CloseReadEnds()` 之前保证关闭读端前管道里的数据已被收完，不丢 stderr 内容；子进程退出 + 父端写口已在 `StartChild` 关闭，EOF 必然到达，join 不会悬置（fd/rg 均不孳生持有 stderr 的长命孙进程；即便孳生，pi 的 node close 事件也是同样的等待语义）。

abort 路径（`ctx.Done()`）维持原样：不读 `stderr`，goroutine 在 kill 后因管道 EOF 自行退出，无泄漏。

## 回归验证

- **红→绿**：race 锁定即既有测试本体——`TestFindTool/should_surface_fd_glob_parse_errors` 在 `-race` 下覆盖「fd 退出码非零 → 读 stderr」路径；修复后该测试及 `TestGrepTool` 全绿。
- 全仓 `go test ./... -race -count=1 -timeout 600s` 单次 30 包 rc=0（`/tmp/pig-test-out-findrace.log`）。

## 教训

- pi 的 node 事件回调（`close`/`exit`/`end`）自带流终结时序，移植成 Go goroutine 时每个被回调读写的局部变量都要找到对应的 happens-before 通道；「进程退出」≠「输出收集完毕」。
- 同型手写 spawn+scanner 模式新增时要先 grep 既有实现（`grep -rn 'stderr +='`），race 属结构性的不会只烧一处。
