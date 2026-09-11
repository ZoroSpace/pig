# 2026-09-11 TestFindTool 失败排查（fd 8.6.0 无 `--no-require-git`）+ 测试能力探测

- **日期**：2026-09-11 13:35 CST（取自修复落盘的文件 mtime）
- **状态**：✅ 已解决（用户升级 fd 10.5.0 后 26 包全绿；测试补能力探测 skip 兜底老 fd）
- **涉及文件**：
  - `codingagent/core/tools/tools_test.go`（新增 `fdSupportsNoRequireGit` 探测 + `skipIfFdLacksNoRequireGit`，4 个用例挂接）
- **性质**：环境问题 + 测试鲁棒性，**非 pig bug**——pig 与 pi 逐字对齐。

## 现象

全仓 `go test ./... -race` 中 `TestFindTool` 4 个子测试失败：`Found argument '--no-require-git' which wasn't expected`（fd 报错）。

## 排查结论

1. **pig 与 pi 一致**：`find.go:262` 逐字对齐 pi `find.ts:198`——仅**非 git 仓库**搜索路径时追加 `--no-require-git`（语义：fd 默认只在 git 仓库内尊重 .gitignore，git 外需要该 flag 才应用，见 pi#5960 注释）。
2. **`--no-require-git` 是 fd 8.7.0 引入的 flag**；测试用例跑在 `t.TempDir()`（非 git 目录）→ 全部走该分支。
3. **本机（Debian 12 Bookworm）官方仓库的 fd-find 就是 8.6.0-3**（`/home/lee/.local/bin/fd -> /usr/bin/fdfind`），不认识该 flag。
4. **pi 在同环境同样会挂**（pi 不做 fd 版本探测）——非移植偏差。

## 处置

**测试层**（已落地）：`fdSupportsNoRequireGit()` 以 `exec.LookPath("fd")` + `fd --help` 输出探测（`sync.Once` 缓存），缺 flag 则 4 个用例 `t.Skip`（fd ≥ 8.7 的机器照常真测；fd 不在 PATH 不 skip——find 工具会走下载 fallback，那是被测行为的一部分）。skip 措辞注明 fd 8.7.0 / Debian 12 背景。

**环境层**（用户处置）：安装 fd 10.5.0 至 `~/.local/bin/fd` 后 4 用例真跑全 PASS，全仓 26 包 `-race` 全绿（首次零环境失败）。

## 运行时行为备注（保持对齐 pi，未做探测）

pig 的 find 工具运行时仍**照传** `--no-require-git`（对齐 pi）：老 fd 环境下 find 在非 git 目录会报 fd 的错误，与 pi 行为一致。若将来想让老 fd 环境也能用（降级为"git 外不尊重 .gitignore"而非报错），需引入运行时能力探测——那是对 pi 的行为偏离，按 §3.4 须用户确认后另起批次；升级 fd（≥ 8.7.0）即可保持两边一致。

## 复现与验证

```bash
# 探测 + skip 生效性：fd < 8.7 时 4 用例 SKIP、测试绿；fd >= 8.7 真跑 PASS
go test ./codingagent/core/tools/ -run 'TestFindTool' -count=1 -v
go test ./... -race -count=1   # 26 包全绿
```
