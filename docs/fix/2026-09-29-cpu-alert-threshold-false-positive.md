# CPU 告警阈值过低导致误报（单核口径 vs 整机口径）

- **日期**: 2026-09-29 23:05
- **状态**: 已修复
- **涉及文件**: `codingagent/modes/interactive/components/resource_stats.go`、`codingagent/modes/interactive/components/footer_resource_stats_test.go`、`docs/feat/FOOTER-RESOURCE-STATS.md`

## 现象

用户 dogfooding 反馈：footer 资源段偶尔弹出 `!CPU80%` / `!CPU90%` 告警，但对照 1panel 面板，整机 CPU 并不高，看起来是误报。

## 排查

**先确认公式本身**：`resource_stats.go` 的 CPU 速率

```go
percent := float64(r.cpuMicros-p.prevCPUMicros) / 1e6 / elapsed.Seconds() * 100
```

即 `Δ进程 CPU 时间 / Δ墙钟 × 100%`，100% = **吃满一个核**（进程级口径，等同 `top` 的 `%CPU`、`ps -o %cpu`）。在本机（12 核）用探针对照验证：

```
elapsed=10.001s
getrusage delta = 3401745 us -> 34.02%   (per-core)
proc/self/stat delta = 340 ticks -> 34.00%
NumCPU=12, system-wide equivalent=2.83%
```

getrusage 与 `/proc/self/stat` 逐位一致 → 公式、单位换算、跨线程累计（`RUSAGE_SELF` 含全部线程）全部正确，**没有计算 bug**。

**真正的误报来源**是告警阈值与展示口径不匹配：阈值 `cpuThresholdPercent = 30`，判据是「**单核** 30% 持续 2 窗口（间隔 10s，约 20s）」。在多核服务器上，流式渲染吃掉小半个核是常态工作负载，整机看只有 30%/N，于是「本机 1panel 不高、pig 告警」——阈值没有为「整机多核」这一语境留余量。

## 处置

`cpuThresholdPercent` 30 → **70**（用户拍板区间 60~80，取中值）。语义仍是单核口径，但只在进程真的接近吃满一个核（事件循环空转的典型特征）时才告警；普通的流式渲染高峰不再触发。`cpuAlertStreakTicks = 2`（连续 2 窗口）不变。

选择「不缩放核数、只抬阈值」而非「除以 `runtime.NumCPU()` 改成整机口径」：容器 cgroup 限额下 `NumCPU()` 不反映实际配额，改口径会引入新的失真；抬阈值改动面最小且语义清晰。

## 回归测试

`TestResourcePollerCPUAlertStreak`（`codingagent/modes/interactive/components/footer_resource_stats_test.go`）扩展为三段：

1. 首窗播种无速率（不告警）；
2. **60% 连续 2 窗口 → 不告警**（低于阈值；这是新阈值的行为锁）;
3. 70% 连续 2 窗口 → 告警（且 60% 那段不得为 streak 预热），随后 10% 冷却窗清除告警。

验证方式（stash 等价，原地改常量再复原）：把 `cpuThresholdPercent` 临时改回 `30` 跑该测试 →

```
--- FAIL: TestResourcePollerCPUAlertStreak
    footer_resource_stats_test.go:357: CPU alert fired at 60% sustained, below the 30% threshold
```

改回 `70` →

```
ok   github.com/ZoroSpace/pig/codingagent/modes/interactive/components
```

## 边界说明

阈值只是缓解误报，**没有改变 CPU 数值本身的含义**：footer 的 `!CPU80%` = 占用一个核的 80%，与 1panel 的整机 CPU% 本就不是同一口径，不能直接比较。想核对真实占用请用 `top -p <pid>`（同为单核口径）。若仍觉误报偏多，可继续上调阈值或把告警改为按核数缩放。
