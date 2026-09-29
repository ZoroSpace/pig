# TEST-SUITE-RUNTIME：验收门 `go test ./... -race` 从 106s 压到 55s（用户插入小批，2026-09-30）

- **日期**：2026-09-30
- **状态**：✅ 完成。全量 `go test ./... -race -count=1 -timeout 600s` 单次 rc=0，wall **55s**（改前 106s），30 包全绿；日志 `/tmp/pig-test-out-perf.log`
- **动机**：`AGENTS.md` 规定「改完代码、commit 前跑一遍全量 -race」，实测每次 106s，日常迭代成本高。目标：**压缩 wall，但不削弱检查力**（该 fail 的必须 fail，断言的执行集合一个不少）
- **涉及文件**：
  - 生产代码：`codingagent/core/session_manager.go`（1 行替换 + 删掉手写扫描函数）
  - 测试基建：`tui/cjk_segmenter_test.go`、`ai/api/anthropic_empty_thinking_signature_compat_test.go`、`ai/providers/volcengine_plan_test.go` → 新建 `ai/providers/volcengine_plan_smoke_test.go`
  - 新增回归：`codingagent/core/session_line_reader_test.go`

## 与 pi 的关系

- ① 是**生产代码**性能缺陷修复（`sessionLineReader.next` 的扫描原语），pig 的 Go 实现形态问题，pi（TS `Buffer.indexOf`）不存在——行为零变化（`bytes.IndexByte` 与手写循环逐字节等价），只把逐字节循环换成 memchr。
- ②③ 是 **pig-only 测试基建**：pi 的 vitest 侧没有「词典实例每测重建」的形状（ICU 在 JS 运行时里）。断言、期望值、被测路径**一行未改**。
- ④（`TestVolcenginePlanRealAPI` 移入 `smoke` tag）与仓里 `ai/api/*_smoke_test.go` 既有约定一致。

## 一、改前测量（2026-09-30，全量 `-race -count=1 -json`，wall = 106s）

wall 由「串行巨包」决定：tui 与 core 同时在跑，tui 先结束，其余包塞不满 12 线程。

| 包 | 包内测试耗时合计 | 元凶 |
|---|---|---|
| `tui` | **99.8s**（315 个测试） | 9 个 CJK 分词测试吃 **84s**；其余 306 个测试合计仅 ~16s |
| `codingagent/core` | **70.4s**（475 个测试） | `TestLoadEntriesFromFile` **35.72s** + reftable 8.02s；其余 473 个 ~26s |
| `codingagent/utils` | 23.9s | 图片 resize 两个测试 15s |
| `ai/api` | 11.8s | `TestAnthropicForceAdaptiveThinkingPayload` **10.37s** |
| `ai/providers` | 9.3s | `TestVolcenginePlanRealAPI` **9.25s**（真机调用） |
| 其余 | 各 ≤ 8s | — |

三条独立的「与断言无关的耗时」：

1. **手写逐字节扫描**（`session_manager.go` 的 `indexByte`）：`sessionLineReader.next` 每读满一块就从 pending 起点重扫，长行因此二次方增长。512MB 稀疏 fixture 全程压在它上面。
2. **每测重建分词器**：`cjk_segmenter_test.go` 的 `useCJKSegmenter` 每次都 `new(cjkSegmenter)` 并重新解析词典。单测实测：`TestWordSegmentDataCJKBigram`（不解析）**1s** vs `SST`/`GSE`（解析）**7s** → 每次词典解析 ≈ **6s**；全仓 6 处 GSE + 3 处 SST 调用 ≈ 54s。
3. **空转等待**：`captureSimplePayload` 指向不可达端口（`127.0.0.1:9`）却用默认重试策略，等连接拒绝退避；`TestVolcenginePlanRealAPI` 把真机 API 调用放在默认门禁里（还会读 `~/.pi/agent/auth.json` 拿 key，因此**不会 skip**）。

## 二、本批交付

### ① 生产修复：扫描原语换成 memchr（收益 30s）

`codingagent/core/session_manager.go:1130` 的 `indexByte(r.pending, '\n')` → `bytes.IndexByte(r.pending, '\n')`，删除手写函数。

独立探针（512MB 缓冲区全零最坏情况，同机实测）：

| | 无 `-race` | `-race` |
|---|---|---|
| 手写逐字节 | 139.7ms | **3.19s** |
| `bytes.IndexByte` | 5.0ms | **0.005s** |

`-race` 下差 **640x**——这正是 512MB fixture 的主要成本来源。修后实测：

| | 改前 | 改后 |
|---|---|---|
| `TestLoadEntriesFromFile`（隔离跑） | 35.72s | **5.54s** |
| `codingagent/core` 包内合计 | 70.4s | **45.1s** |

残留的 5.5s 是该 fixture 的固有成本（写 512MB + 逐行 `string(...)` 拷贝 + 33 次 16MB 片段的 JSON 解析尝试），不是扫描。**行为逐字节等价**，真实收益是：超大会话文件（>512MB）加载不再走二次方路径。

### ② tui 词典按模式 memoize（收益 54s）

`useCJKSegmenter` 保留原签名与「换全局实例 + `t.Cleanup` 还原」语义，内部改为从 `loadedCJKSegmenters`（`sync.Map`，键 = 模式）取已加载实例；`loadCJKSegmenter` 负责首次加载并等待 ready。

- 复用安全性：实例加载后只读（`mode` 固定、词典指针 `atomic.Pointer` 发布后不变、`cut` 由实例 mutex 串行），测试观察到的切分结果不变。
- 只有**专门测加载路径**的测试不用这个 helper：`TestSetCJKSegmenterMode`（自己 `SetCJKSegmenterMode` + 等 sst dict）、`TestWordSegmentDataCJKFallbackBeforeLoad`（自建未加载实例）——它们本来就没走 `useCJKSegmenter`，行为原样。
- 代价：进程内常驻一份 GSE 词典（CJK-SEG-MODE 实测 ~206MB），直到测试二进制退出（原先峰值也有一份，只是会随 GC 释放）。

| | 改前 | 改后 |
|---|---|---|
| CJK 家族子集（`-run 'CJK\|WordBackward\|WordSegment\|WordNav\|KillRing\|UnicodeTextEditing'`） | ~84s | **33.7s** |
| `tui` 包内合计 | 99.8s | **46.1s** |

残留 33.7s 中 ~15s 是 `TestEditorAutocompleteCJKBoundaries` 的真实 CPU（92+ 轮编辑器交互），另 ~12s 是「每种模式仍要解析一次词典」。

### ③ 去掉与断言无关的等待（收益 8s）

- `ai/api`：`captureSimplePayload` 增加 `options.MaxRetries = lo.ToPtr(0)`（该 helper 只验证 OnPayload 捕获的请求体，不需要响应；字段 `SimpleStreamOptions.MaxRetries` 由 anthropic adapter 读取）。`TestAnthropicForceAdaptiveThinkingPayload` **10.37s → 1.95s**。
- `ai/providers`：`TestVolcenginePlanRealAPI` + `readPiAuthVolcengineKey` 移入新建 `ai/providers/volcengine_plan_smoke_test.go`（`//go:build smoke`），默认门禁不再发真机请求、不再依赖 `~/.pi/agent/auth.json` 与网络。整包 **9.3s → 1.11s**。真机跑法（README「验证与恢复」口径不变）：

  ```bash
  VOLCENGINE_API_KEY=ark-xxx go test -tags=smoke ./ai/providers/ -race -run TestVolcenginePlanRealAPI -v
  ```

## 三、验证

- 全量：`go test ./... -race -count=1 -timeout 600s` 单次 **rc=0，wall 55s**（30 包全绿；日志 `/tmp/pig-test-out-perf.log`）。
- 覆盖不变：改动只涉及「扫描原语 / 词典实例复用 / 重试次数 / 测试归档位置」，**没有新增或删除任何断言**；④ 唯一的语义变化是 volcengine 真机测试从默认门禁移到 `-tags=smoke`（声明于上）。
- 定向 before/after（同一命令、同一机、隔离跑）：见 §二 各表。
- `go build ./...`、`go vet ./ai/... ./tui/... ./codingagent/...`、`go vet -tags=smoke ./ai/providers/`、`gofmt -l` 全干净。

## 四、回归锁定

- `codingagent/core/session_line_reader_test.go`（新建）：
  - `TestSessionLineReaderSplitsLinesLargerThanTheReadBuffer`：4MiB 单行（远大于读缓冲 1MiB）跨多次 refill 后必须原样取回，且 `hadTrailing` 语义不变——锁住长行正确性。
  - `BenchmarkSessionLineReaderLongLine`：本机 553 MB/s（`go test ./codingagent/core -run '^$' -bench '^BenchmarkSessionLineReaderLongLine$'`）。改回逐字节扫描会掉 ~3 个数量级 / 512MB fixture 重新变成 ~35s。
- **为什么不出时间断言**：CI/本地负载波动会让 `elapsed < N` 变 flaky（本批就抓到并发的假红，见 §五-1），所以门槛放在 benchmark + 本节记录的 before/after 数值上，由 review 与人工复测兜底。
- ②③ 是测试基建的等价重写：既有断言本身就是回归（改后全量 rc=0 即证明期望值未变）。

## 五、其他手段（本批**不做**，留档备取）

### 1. 分片并行（本期实测 106s → 49s，但有两个真代价）

做法：按上轮 `-json` 的逐测耗时做 LPT 均衡，把重包切成 K 片、每片一个进程（仍是 `-race -count=1`，每个测试恰好跑一次），其余包一次跑。实测（12 分片 + rest，12 线程 / 6 物理核）：

| 配置 | wall | 结果 |
|---|---|---|
| 全量单进程（改前） | 106s | rc=0 |
| tui/core/utils 各切 4 片 + rest | **49s** | rc=0；回执：基线 1850 个测试 / 分片 1850 个，漏跑 0，包级 55/55 一致 |
| 同配置 + 生产修复① 后预计 | ~33s | 分片下限被 512MB fixture（改后 ~5.5s，改前 ~42s）与 tui 最大测试决定 |

两个必须知道的坑（本批实测）：

1. **假红**：13 个 race 进程挤 6 物理核时，`codingagent/modes/interactive` 的 `TestBugReportPreservesMultilineDescription`（5s 轮询超时）报 `summary selector never appeared`；隔离单跑 1.1s 绿。这是「不该 fail 的 fail」，与「该 fail 的就 fail」相反，需限并发（如 rest 用 `-p 4`）或接受偶发重跑。
2. **假绿**：第一版脚本的排除清单正则出现空 alternation，`rest` 变成空包列表，`go test` 退化成「对当前目录跑」并快速失败/或漏跑 core+utils——**分片方案必须自带覆盖率回执**（跑了多少测试 / 多少包，并与上轮全量基线对比），只看 rc 会静默漏测。

脚本（LPT 均衡 + `-run` 分片 + rc 汇总 + 回执）当时放在 `/tmp/race-check.sh`，未入库；要用的话建议落 `scripts/race-check.sh` 并在 AGENTS.md 说明「分片跑等价于一次全量，但需看回执」。

### 2. 缓存替代 `-count=1`（日常迭代可用）

`-race` 的测试结果**可以缓存**（实测同命令第三次跑直接打印 `(cached)`）；`-count=1` 是 `go help test` 里「显式禁用缓存」的惯用写法。所以：

- 日常/迭代：`go test ./... -race -timeout 600s`（不带 `-count=1`）→ 只有改动过的包真跑，其余 ~0s。
- 最终门禁：仍用 `-count=1` 强制全跑（缓存不追踪网络端口等外部状态，`ai/auth` 这类起 loopback 的包有陈旧结果风险）。

### 3. 剩余固有热点与进一步手段（未做）

| 热点 | 现状 | 可选手段 |
|---|---|---|
| `tui/TestEditorAutocompleteCJKBoundaries` | 15s 真实 CPU | 子测 `t.Parallel`（该测试不碰全局分词器）/ 精简轮次（会削弱覆盖，不推荐） |
| `codingagent/utils` 图片 resize 两个测试 | 15s | 减少 fixture 尺寸（会改变被测路径的规模，需权衡） |
| `codingagent/core` reftable 分支探测 | 8s | 查是否可用 fake clock/seam |
| `tui` 其余 306 个测试 | 合计 16s，但**全串行**（包内 0 处 `t.Parallel`） | 给纯渲染/字符串测试加 `t.Parallel`，理论上可再压 3-5x |
| 包级并行度 | 默认 `-p=12`（12 线程 / 6 物理核，超订） | `-p 6` 可能小幅改善尾部；收益有限，未测出稳定差异 |

## 六、边界与偏差

- ①不改任何行为，pi 对齐不变；②只改测试 helper 的实例生命周期；③只改测试的重试次数与归档位置（`smoke` tag 是有意为之的门禁收窄，已声明）。
- ②会让 `tui` 测试二进制常驻一份 GSE 词典（~206MB）直到退出。
- ⑤（分片）与缓存策略**本批未启用**，默认门禁仍是「单进程 + `-count=1` + 全 `-race`」。
- 用户可见面（CLI/provider/配置/线格式）零变化 → README 与 `docs/handoffs/verify-matrix.md` 无需同步。
