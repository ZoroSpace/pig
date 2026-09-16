# FOOTER-RESOURCE-STATS：footer 资源段（MEM）+ showResourceStats 设置（用户插入小批，2026-09-14）

- **日期**：2026-09-14（晚）
- **状态**：✅ 完成。components / interactive `-race` 全绿；core `-race` 除 `TestModelRuntimeProjectsProviderOwnedMethodsNamesAndStatus`（另一并行 agent 的 copilot WIP：第 5 个 oauth provider 已进 BuiltinProviders、其测试未同步）外全绿，与本批无关
- **动机**：dogfooding 中用户关注 pig 自身资源占用（前有同日 `docs/fix/2026-09-14-tui-turn-memory-leak.md` 的逐轮内存整案排查，排查手段全是临时探针）。需求：footer 左下角常驻可见内存，但**默认零干扰**——做成 `/settings` 可开关、默认关。
- **涉及文件**：`codingagent/modes/interactive/components/rss.go`（新建）、`footer.go`、`footer_mem_test.go`（新建）、`settings_selector.go`、`codingagent/core/settings_manager.go`、`codingagent/modes/interactive/interactive_mode.go`

## 与 pi 的关系

**pig-only，pi 无对应物**（pi footer 只有 token 统计）。设置键命名经用户纠正定为通用的 **`showResourceStats`**（"Resource stats"）而非 `showMem`：MEM 只是第一个成员，后续可能扩展 CPU / goroutine / FD 等指标（已评估：CPU 需跨时间窗采样、空闲期信噪比低，适合"异常才显示"而非常驻；VSZ 在 Go 运行时下无诊断价值）。

## 设计

### 显示形态

stats line 末尾以 `|` 与 token 统计隔开：

```
↑58k ↓2.9k R32k W1.0k CH35.2% $0.050 3.0%/1.0M (auto) | MEM52MB
```

- 格式 `FormatMemBytes`：`52MB` 而非 `52M`（用户拍板，"M" 易与 token 的 M 混淆）——<1MB 用 KB（向上取整）、1~10MB 一位小数、≥10MB 整数、≥1GB 一位小数。
- 追加在 stats line 末尾（xp 标记之后），`|` 恒紧贴 MEM 段，与既有段的拼接/截断逻辑零冲突。

### 采样（rss.go）

- **绝不每帧采样**：footer 每帧全量重渲（差分渲染设计），采样必须离帧。5s ticker 后台协程读 `/proc/self/statm`（field 2 resident 页 × `os.Getpagesize()`，stdlib 零依赖，不引 gopsutil），结果存 `atomic.Int64`，渲染读缓存值。RSS 每轮增长 ~100KB 量级（fix 文档实测），5s 响应足够。
- 读失败（非 procfs 平台，如 macOS）即停 ticker，值保持哨兵 0 → 段隐藏。
- `startRSSPoller(interval, sink)` 的 stop 幂等（once + close）；首个采样在第一个 tick 落地，不立即采（构造即渲染的测试确定性）。

### 生命周期与门控

- sampler 由 `FooterComponent` 持有（`stopRSSPoller func()` + `rssBytes atomic.Int64`），**仅设置开启时运行**——不为不可见的段轮询。开启时同步先采一次（段在下一帧即出现，不等 tick），关闭时 stop 并清零。`Dispose` 兜底停。
- 双重门控：设置关 → 段绝不渲染（即使缓存有值）；设置开但未采样（无 procfs）→ 也不渲染。`0 = 未采样`。
- 测试 seam：`NewFooterComponentWithView` 不启动 sampler（现有 usage/width 测试零改动），测试直接操作未导出字段注入确定值。

### 设置链

`settings.json` 的 pig-only 键 `showResourceStats`（默认 false；`Get/Set` 沿 `QuietStartup` 模式，Settings 清单注释同步）→ `/settings` 面板新增 "Resource stats" 项（splice 模式插在 terminal-progress 之后，pig-only 注释声明）→ 回调 `SetShowResourceStats`（持久化 + footer 即时生效）→ interactive 两处初始接线（构造后 + 7090 处 session 重绑后，紧邻 `SetAutoCompactEnabled` 既有模式）。

## 回归测试（初版 8 函数；追加批后 16 函数、文件更名 `footer_resource_stats_test.go`、显示格式去空格，见下 RESOURCE-ALERTS 节）

- `TestFooterMemHiddenByDefault`（设置关时即使有采样值也不显示——门控即本批语义核心）/ `TestFooterMemShownWhenEnabledAndSampled`（断言 `3.0%/1.0M (auto) | MEM52MB` 全串）/ `TestFooterMemHiddenWhenEnabledButUnsampled`
- `TestSetShowResourceStatsLifecycle`（开→sampler 启动 + 即时采样；关→停止 + 清零；无 procfs 平台 skip）
- `TestStartResourcePollerSamplesIntoSink` / `TestStartResourcePollerStopIsIdempotent`（初版名 TestStartRSSPoller*，追加批更名）
- `TestReadRSSBytesSanity`（1MB~64GB 合理区间）、`TestFormatMemBytes`（格式钉死：`512KB`/`1024KB`/`1.0MB`/`8.4MB`/`52MB`/`1.2GB`）

## 过程记录

- Go 常量转换坑：`int64(8.4 * mb)`（非整型浮点常量）编译不过，测试改写死字节数（`8_808_038` 等）。
- 并行开发教训：本批开发期间同工作区有另一 agent 在改 `ai/*`（provider 批）。验证"既有失败归属"时用过一次 `git stash`/`pop`——**并行场景禁用 stash**（会把对方 tracked 改动卷走两分钟，纯属侥幸完整往返）；改用 `-skip` 定向验证。提交时显式按路径 `git add`，只含本批 6 文件。

## RESOURCE-ALERTS：堆 + G/FD/CPU 异常告警（2026-09-14 同日追加批）

用户插入批：资源段从单一 MEM 扩为**两项常驻 + 三项异常浮出**。显示格式经用户两次纠正定稿：项间用 `·` 分隔（不加空格）、HEAP 用英文标签、单位保持 MB：

```
↑58k ↓2.9k … 3.0%/1.0M (auto) | MEM52MB·HEAP14MB·!G387·!FD210·!CPU97%
```

- **常态**：`MEM`（RSS）+ `HEAP`（`runtime.HeapAlloc` 活堆）。配对的意义：活堆涨 = transcript 成比例（设计内，见 2026-09-14 内存整案），RSS 涨 = 运行时 arena 保留（无害），比值定性泄漏 vs churn。
- **异常浮出**（主题 warning 色，`!` 前缀）：`!G`（goroutine 数）、`!FD`（描述符数）、`!CPU`（窗口速率 %）。**符号用 ASCII `!` 而非 ⚠️——emoji（U+26A0+FE0F）的实际占格由终端/字体决定，pig 宽度表按规范算 2、终端可能按 1 推进，字形右半压到后续文本产生重叠**（用户真机实证）；告警语义由颜色 + `!` 承担，零宽度歧义。
- **采样**：单 poller 每 5s 采齐（`resource_stats.go`，由 rss.go 更名）：RSS（statm）、HeapAlloc（`ReadMemStats`，STW 微秒级、5s 节奏可忽略）、`NumGoroutine`、`/proc/self/fd` 计数、`/proc/self/stat` utime+stime（USER_HZ=100，同 gopsutil 假设；窗口速率 = Δticks/100 ÷ Δ墙钟，首窗无速率）。开启路径 `sampleStatic` 即时填静态值。
- **告警判据**（`resourcePoller.apply` 纯函数，脱离时间/procfs 可测）：
  - G：**趋势优先**——连续 10 窗口（50s）单调递增即告警（健康会话空闲期振荡、泄漏棘轮式爬升；任何非递增重置），叠加绝对阈值 ≥500 兜已平台期的存量泄漏；
  - FD：绝对阈值 ≥200（健康 O(10)；oauth listener 泄漏前科无界增长）；
  - CPU：窗口速率 ≥30% 连续 2 窗口（~10s）——单窗口的流式渲染高峰不算异常，事件循环空转才算。
- **生命周期不变**：仅开启时运行；statm 读取失败（非 procfs 平台）→ 全量清零 + 停止；关闭时停止 + 清零。
- 回归测试 16 函数（`footer_resource_stats_test.go`）：渲染门控 6（MEM 默认隐藏/显示/HEAP 拼接/未采样隐藏/告警显示/告警隐藏）+ 生命周期 1 + poller 冒烟 2 + procfs 读数 3（RSS/CPU ticks 单调/FD）+ apply 纯函数 3（G 趋势与播种语义、CPU 双窗连续、FD 阈值与无读数）+ `FormatMemBytes` 1。

过程 bug（本批）：G 趋势测试首版少算一次播种（N 次递增产生 streak=N-1），10 次递增才触发——按实现语义修正测试而非放松判据；HEAP 测试的斜杠断言被 context 段的 `3.0%/1.0M` 误触发，改为钉 `MB/` 子串。

## MEM-ONLY + macOS：单内存项 + darwin 全指标（2026-09-14 同日第三批，本节为显示终态）

用户插入批，两个决策：

1. **显示再次收敛：去 HEAP**——「用户其实不用知道 RSS 和 HEAP」，常态就一个数 `MEM22MB`。9/14 整案里的 RSS/活堆配对诊断法转入本文档存档，footer 不再承载（未来 /debug 面板可复活）。`ReadMemStats`（STW）同步移出采样路径，poller 更轻。
2. **macOS 全指标支持**（此前 procfs 缺失 → 整段隐藏）：
   - MEM：spawn `ps -o rss= -p <pid>`（当前 RSS，与 Linux 同语义；~1-2ms/5s，保住零 cgo 静态构建——mach `task_info` 需要 cgo，不可接受）；
   - CPU：改用跨平台 `getrusage(RUSAGE_SELF)`（µs 级），**删除 /proc/self/stat 解析与 USER_HZ=100 假设**；
   - FD：darwin 数 `/dev/fd`（fdesc），Linux 仍走 `/proc/self/fd`；
   - G：`NumGoroutine` 本就跨平台。

平台差异收敛为两个小文件：`resource_stats_linux.go`（statm + /proc/self/fd）、`resource_stats_darwin.go`（ps + /dev/fd），CPU 的 getrusage 归入 `resource_stats_unix.go`（//go:build unix）；其余全在共享的 `resource_stats.go`。

**Windows：编译桩，非支持平台**。pig 本体本就编不过 windows（tui 依赖 POSIX 信号 syscall.Kill/SIGWINCH，tui/terminal.go），本批仅为资源采样代码提供 `resource_stats_windows.go` 编译桩（三采样器返回不可用 → 段隐藏），未来接 windows 支持时的真实现路径已写在文件头注释：GetProcessMemoryInfo（WorkingSetSize）/ GetProcessTimes / 句柄数（FD 阈值需 windows 专属基线，典型进程持有数百句柄）。三平台 `go build` 验证经独立临时包（components 传递依赖 tui，windows 下整包编不过为既有边界）。当前显示形态：

```
↑58k ↓2.9k … 3.0%/1.0M (auto) | MEM22MB·!G387·!FD210·!CPU97%
```

回归测试 15 函数（合并去 HEAP 后）：HEAP 渲染用例删除，CPU 纯函数用例改 µs 语义，新增 `TestReadProcessCPUMicrosSanity`（单调性）；darwin 运行时路径无 CI 覆盖（Linux CI），以 `GOOS=darwin go build` 保证编译 + 代码审查级核对。

## MEM 口径对齐活动监视器：ps RSS → phys_footprint（2026-09-16，本节为显示终态）

dogfooding 发现 footer `MEM44MB` 与活动监视器「内存」列 `24MB` 差 ~20MB——两个统计口径不同：ps 报 RSS（全部驻留页，含 pig 自身 ~39MB 二进制的干净 file-backed 页：代码段、嵌入 CJK 词典等），活动监视器是 **phys_footprint**（只记真正记在进程账上的内存：匿名 + 脏 file-backed + 压缩页，Apple TN2439）。实测 pig 交互模式：ps RSS 41.6MB vs footprint 23.3MB，差额正是二进制映射的干净页（vmmap：`__DATA_CONST` 驻留 18.3MB 中脏页仅 2.4MB 等）。真机端到端验收：footer 由 `MEM41MB` 级变为 `MEM22MB`，与同进程 `footprint` 工具读数一致。

- **darwin 采样改为 `/usr/bin/footprint -f bytes <pid>`**：Apple 自家工具，输出的 `Footprint: <N> B` 与 vmmap `Physical footprint` 实测一致（50MB 探针：56164880 B vs 53.8M）；查自己 pid 无需 root，~15ms/次（ps 的 ~10 倍，5s 节奏可忽略）。`footprint` 缺失/解析失败回落 `ps -o rss=`（老 macOS/沙箱环境），回落即回到旧语义。
- **纯 Go 裸 syscall 无解（2026-09-16 实证）**：`proc_info(SYS_PROC_INFO=336)` 的 PIDINFO 族只有 virtual/resident（proc_taskinfo 96B 无 phys_footprint 字段）；`PROC_INFO_CALL_PIDRUSAGE(8)` 裸调返回全零（copyout 走 libproc 包装的私有路径）；mach `task_info(TASK_VM_INFO)` 是 MIG 消息需 cgo/手搓 mach_msg——footprint 子进程是零 cgo 下唯一同口径路径。
- **顺带排除头号嫌疑 MADV_FREE**：实验分配 50MB 再释放（含 `debug.FreeOSMemory`、`GODEBUG=madvdontneed=1` 两形态），RSS 与 footprint 都纹丝不动——footprint 口径同样计入 MADV_FREE 页，二者差额与它无关。
- **Linux 不变**（statm resident）：procfs 无 footprint 等价物，RSS 即本地监视器口径；跨平台语义差异在两平台文件头注释声明。
- 字段改名 `rssBytes`→`memBytes`（`resourceReadings.rss`→`.mem`、`readRSSBytes`→`readMemBytes`，darwin 侧 `readRSSBytes` 保留为 ps 回落、`footerRSSInterval`→`footerSampleInterval`）。
- 回归测试 18 函数（+3）：`TestParseFootprintOutput`（`Footprint: <N> B` 解析钉死 + 畸形输出回落）+ `TestReadFootprintBytesSanity`（darwin，真工具读数合理区间）+ `TestReadMemBytesSanity`（原 TestReadRSSBytesSanity 更名，双平台）；真机验收：交互模式 footer 显示 22MB 与 `footprint` 工具/活动监视器一致，同进程 ps RSS 仍 41MB。
- **显示格式同日追加：MB 恒一位小数**（用户要求与活动监视器「内存」列同款）：`MEM20.0MB` 而非 `MEM20MB`（`FormatMemBytes` 的 MB 分支去掉 <10MB 才带小数的旧规则；KB 整数、GB 原本就一位小数不变）；真机验收 `MEM20.0MB`；`TestFormatMemBytes` 与 footer 渲染两用例同步（`MEM52.0MB`）。
