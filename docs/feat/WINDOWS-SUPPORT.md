# WINDOWS-SUPPORT：Windows 编译补齐（用户插入批，2026-09-14，README 承诺兑现）

**状态**：⏳ 待执行（总约束：**零 cgo、静态单二进制、pi 行为对齐不变**；完成后本行销账 ✅ 并按惯例补 AGENTS.md「已交付」条目）

## 一、背景与动机

- `README.md:87` 宣称："编译产物为**单个静态二进制**，拷到任何 Linux / macOS / Windows 机器上就能跑"——**目前对 Windows 是超售**：`GOOS=windows go build ./cmd/pig` 当场失败。
- 失败根因不是架构性不可行，而是三处 POSIX 专用调用未做平台拆分（见 §二）。代码里 Windows 意识其实不少：`utils/shell.go` 已有 `taskkill.exe /F /T /PID` 杀进程树分支（pi 对齐）、shell 列表有 `powershell`、`refreshTerminalDimensions` 有 `if GOOS == "windows" { return }` 运行时分支——只是编译期没接住。
- 本批目标：**让 `GOOS=windows` 编译通过且 unix 路径行为零变化**。Windows 终端的 TUI 渲染体验是真机验证项（见 §四），不在本批承诺内。

## 二、现状盘点（2026-09-14 实测）

### 编译断点（`GOOS=windows go build ./cmd/pig` 逐个消）

1. **`tui/terminal.go:223`** — 包级 var `killSelfSignal = func() error { return syscall.Kill(os.Getpid(), syscall.SIGWINCH) }`。
   - 语义：给**自己**发 SIGWINCH 触发尺寸刷新（pi `process.kill` monkey-patch seam 的 Go 化，`terminalPlatform`/`killSelfSignal` 是测试 seam）。
   - 运行时守卫已存在：`refreshTerminalDimensions` 开头 `if terminalPlatform == "windows" { return }`——所以 windows 上这条路径**整体不需要**，纯属 var 定义在 windows 编译不过。
2. **`tui/terminal.go:276`** — `signal.Notify(winch, syscall.SIGWINCH)` + winch goroutine（resize 监听）。windows 无 SIGWINCH。
3. **`codingagent/utils/shell.go:300`** — 进程组杀 `syscall.Kill(-pid, syscall.SIGKILL)` + 单杀 fallback；**同一函数上方已有 taskkill.exe 的 windows 分支**，但同函数全平台编译。

### 好消息（非断点）

- raw mode 用 `golang.org/x/term` MakeRaw/Restore（go.mod v0.28.0 直依赖）——x/term 的 windows 实现走 console API，可移植 ✓。
- 平台拆分有现成先例：`codingagent/utils/spawn_attr_unix.go` / `spawn_attr_windows.go`（spawnSysProcAttr）。
- `golang.org/x/sys v0.29.0` 已在 go.sum（indirect，经 x/term 带入）——windows 代码 import 后 tidy 移 direct 即可，无新依赖。
- `resource_stats_windows.go` 编译桩已就位（2026-09-14，资源统计批），真实现路径写在文件头注释（§二.5）。

### 既有边界（不属本批）

- components 包传递依赖 tui → `GOOS=windows` 下 `./...` 编不过的根因全部在 tui 层；修完 tui 后预期全仓通过。资源采样代码三平台编译已用独立临时包验证过（见 `docs/feat/FOOTER-RESOURCE-STATS.md` 末节）。

## 三、任务分解

### 1. tui/terminal.go 的 SIGWINCH 三处（核心工作量）

**方案**：按 spawn_attr 先例拆文件——
- `tui/terminal_unix.go`（`//go:build !windows`）：`killSelfSignal` var + `signal.Notify(SIGWINCH)` 注册（现实现原样搬入，unix 行为零变化）；
- `tui/terminal_windows.go`（`//go:build windows`）：windows 尺寸刷新路径。

**windows 尺寸刷新的三个选项**（按推荐排序）：
- **a) 绘制前比对（推荐）**：渲染 tick 里用 `x/sys/windows` 的 `GetConsoleScreenBufferInfo`（或 `GetConsoleScreenBufferInfoEx`）读当前尺寸，与缓存尺寸不同才触发 relayout——无新 goroutine、无事件流，与轮询语义等价；pi 在 windows 的 Node 'resize' 事件底层也是 libuv 轮询，语义最接近。
- b) `ReadConsoleInput` 事件流（WINDOW_BUFFER_SIZE_EVENT）——要维护 console 输入句柄，复杂度不值首批。
- c) 不刷新（启动定尺寸）——兜底方案，体验差，仅当 a) 有阻碍。

### 2. utils/shell.go killProcessGroup 拆分

- 按 spawn_attr 先例：`shell_kill_unix.go`（现 `syscall.Kill(-pid, SIGKILL)` 组杀 + 单杀 fallback 原样搬入）/ windows 侧把现有 taskkill 分支挪进 `shell_kill_windows.go`（逻辑已对齐 pi，仅挪位置）。
- 注意主流程里对 windows 分支的选择方式随拆分调整（现在的运行时判断改成编译期各自实现）。

### 3. 全仓断点清扫

修完上述后 `GOOS=windows go build ./...` 逐个消（我盘点到的断点仅这三处 + tui 传递依赖，编译器可能暴露漏网）。验收命令：

```bash
GOOS=windows go build ./... && GOOS=windows go vet ./...
GOOS=darwin go build ./...   # 回归
go build ./... && go test ./... -race -count=1 -timeout 600s   # unix 行为零变化
```

### 4. CI 矩阵（.github/workflows/go.yml）

现单 ubuntu job（build/vet/gofmt/test -race）。加 cross-compile build 步（test 仍 linux-only，windows runner 贵且无 -race 收益）：

```yaml
- name: Cross-compile build
  run: |
    GOOS=darwin go build ./...
    GOOS=windows go build ./...
```

### 5. resource_stats_windows.go 真实现（顺路，小批内完成）

桩已在位（返回不可用 → footer 资源段隐藏），真实现路径写在文件头注释：
- **RSS**：`x/sys/windows.GetProcessMemoryInfo` → `WorkingSetSize`（Windows 的"当前内存"语义，与 Linux statm 对齐）；
- **CPU**：`GetProcessTimes` → kernel+user FILETIMEs（100ns 单位 → µs，对齐共享接口 `readProcessCPUMicros` 的契约）；
- **FD**：无 /proc 等价物。`GetProcessHandleCount` 报句柄数——**不同货币**：windows 典型进程持有数百句柄，现有 `fdThreshold = 200` 会天天误报。建议 windows 暂不启用 FD 告警（返回不可用）或另立平台专属阈值，二选一在实现时定；
- x/sys import 后 `go mod tidy`（indirect → direct，无新外部依赖）。

### 6. README 与 verify-matrix 同步（AGENTS.md README 同步义务）

- README:87 兑现后原文可保留；建议按实况加注「Windows：编译通过，终端体验未真机验证」（措辞随 §四 实况调整）。
- `docs/handoffs/verify-matrix.md` 增「平台」节（现矩阵全是 provider 维度）：Windows Terminal / conhost × TUI 渲染（ANSI/VT、CJK 宽度、鼠标事件、resize）、powershell shell 执行、taskkill 进程树清理、资源段显示——全部 ❌ 待真机销账。

## 四、风险与未决项

1. **TUI 渲染体验未知（本批最大不确定性）**：Windows Terminal 对 ANSI/VT 支持好，conhost 老；CJK 宽度在 windows 终端的 wcwidth 行为差异；鼠标事件协议。编译批不背体验——真机销账走 verify-matrix。
2. **bash 工具的默认 shell**：settings 已列 powershell，但 bash 工具的 shellPath 解析在 windows 的行为未盘点（本批顺带盘点，发现问题另立 fix 单，不扩大战线）。
3. **无 windows 真机环境**：执行 agent 大概率只能交付「编译通过 + CI 矩阵 + 代码审查」，真机项留给用户（有 Windows 机器/CI windows runner 时销账）。
4. **并行开发注意**：`tui/terminal.go` 是热文件，开工前先 pull；go.mod 变更仅 x/sys indirect→direct，与 provider 批无冲突。

## 五、验收清单

- [ ] `GOOS=windows go build ./... && GOOS=windows go vet ./...` 通过
- [ ] `GOOS=darwin go build ./...` 通过（回归）
- [ ] Linux 全量 `go test ./... -race -count=1 -timeout 600s` 全绿（unix 路径行为零变化的证据）
- [ ] CI 加 cross-compile build 步并跑绿
- [ ] resource_stats_windows.go 真实现（或明确留桩的决策记录）
- [ ] README / verify-matrix 同步
- [ ] AGENTS.md「已交付」补 WINDOWS-SUPPORT 条目
