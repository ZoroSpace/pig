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
↑58k ↓2.9k R32k W1.0k CH35.2% $0.050 3.0%/1.0M (auto) | MEM 52MB
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

## 回归测试（8 函数，`components/footer_mem_test.go`）

- `TestFooterMemHiddenByDefault`（设置关时即使有采样值也不显示——门控即本批语义核心）/ `TestFooterMemShownWhenEnabledAndSampled`（断言 `3.0%/1.0M (auto) | MEM 52MB` 全串）/ `TestFooterMemHiddenWhenEnabledButUnsampled`
- `TestSetShowResourceStatsLifecycle`（开→sampler 启动 + 即时采样；关→停止 + 清零；无 procfs 平台 skip）
- `TestStartRSSPollerSamplesIntoSink` / `TestStartRSSPollerStopIsIdempotent`
- `TestReadRSSBytesSanity`（1MB~64GB 合理区间）、`TestFormatMemBytes`（格式钉死：`512KB`/`1024KB`/`1.0MB`/`8.4MB`/`52MB`/`1.2GB`）

## 过程记录

- Go 常量转换坑：`int64(8.4 * mb)`（非整型浮点常量）编译不过，测试改写死字节数（`8_808_038` 等）。
- 并行开发教训：本批开发期间同工作区有另一 agent 在改 `ai/*`（provider 批）。验证"既有失败归属"时用过一次 `git stash`/`pop`——**并行场景禁用 stash**（会把对方 tracked 改动卷走两分钟，纯属侥幸完整往返）；改用 `-skip` 定向验证。提交时显式按路径 `git add`，只含本批 6 文件。
