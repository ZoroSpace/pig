# CI Go 工具链版本漂移（compile version does not match go tool version）

- **日期**: 2026-09-26 23:03（问题在 2026-09-24 的 CI run 首次暴露：run 36000455222 / job 107635545022）
- **状态**: 已修复
- **涉及文件**: `.github/workflows/go.yml`、`go.mod`（只读参照）、`codingagent/config_test.go`

## 现象

CI 部分包构建失败，报错形如：

```
# internal/coverage/calloc
compile: version "go1.26.0" does not match go tool version "go1.25.14"
```

失败包集中在 `-coverprofile` 需要重编的 std 包（internal/coverage/*、math/bits、unicode 等）；同 job 里另一些包（如 `codingagent/core`）正常跑完，race 报告的栈也显示测试二进制跑在 `golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64` 下。

## 根因

**CI 钉的 Go 版本与 go.mod 的 go 指令脱节**：

- commit `de1afbd`（2026-09-22，image 413 修复批）中 `golang.org/x/text`、`x/sys`、`x/image` 依赖升级把 go.mod 的 go 指令抬到 **1.26.0**；
- `.github/workflows/go.yml` 仍钉 `go-version: "1.25"`（装到 go1.25.14）。

结果：CI 上每个 go 命令都发现 go.mod 要求 ≥1.26.0，`GOTOOLCHAIN=auto` 从模块代理下载 go1.26.0 工具链——部分构建路径经工具链切换正常（race 栈即证据），另一些路径在 runner 环境下出现 **go 命令（1.25.14）与 compile 二进制（1.26.0）来自不同安装** 的混用，compile 工具的 `-goversion` 自检失败即报此错。哪条路径中招取决于 runner 的 GOROOT/PATH 解析与构建缓存命中分布，故表现为「部分包失败、部分包通过」。

## 修复

- `.github/workflows/go.yml`：`go-version: "1.25"` → `"1.26"`，与 go.mod 对齐（AGENTS.md 既定原则「go.mod floor 不设 toolchain 指令，CI 里钉版本」）。
- 新增守卫测试 `TestCIWorkflowGoVersionTracksGoMod`（`codingagent/config_test.go`）：解析 go.mod 的 `go <major.minor>` 指令与 workflow 的 `go-version: "<major.minor>"` 并断言一致——今后依赖再抬 go 指令时忘改 CI 会在全量回归直接红。

## 回归验证

- **红**：`git stash` 恢复 workflow 旧版（"1.25"）→ `go test ./codingagent/ -run TestCIWorkflowGoVersionTracksGoMod` 报 `CI go-version "1.25" does not match go.mod go directive "1.26"`。
- **绿**：恢复修复后同命令通过；全仓 `go test ./... -race -count=1 -timeout 600s` 29 包 rc=0（`/tmp/pig-test-out-20260924-ci-fix.log`）。

## 教训

go.mod 的 go 指令会被依赖升级（`go mod tidy` / `go get`）静默抬升，CI 钉版是手工维护点——二者必须同 commit 联动；守卫测试把这条联动固化进全量回归。
