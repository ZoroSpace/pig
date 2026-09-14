# 交互模式逐轮内存增长（已收口：非泄漏，设计内留存 + Go 运行时行为；顺带修复两个 ctx 注册表泄漏）

- **日期**：2026-09-14 18:32（首记）；2026-09-14 深夜 定稿
- **状态**：**已收口**。用户报告的「每轮涨几 MB、不回落」复现成立，但**终审为非泄漏**：活堆增长全部与 transcript 内容成比例（架构设计内，pi 同构），RSS 增长的大头是 Go 运行时的堆保留行为（分配 churn → 空闲 arena 惰性归还）。排查过程中顺带修复两个真实的 Go context 注册表泄漏（每轮 runCtx、Refresh 成功路径 refreshCtx 漏 cancel）与探针测试自身的数据竞争。
- **涉及文件**：
  - 修复：`agent/agent.go`（finishRun 补 cancel）、`ai/models.go`（Refresh 收尾补 cancel）
  - 回归测试：`agent/agent_test.go`（TestAgentCancelsRunContextAfterRunFinishes）、`ai/models_test.go`（TestModelsRefreshCancelsPerProviderRefreshContext + stubRefreshingProvider）
  - 探针资产：见「诊断资产清单」（mock server 改入 test 文件、探针 env 门控）

## 现象

用户报告（dogfooding）：交互模式每问一句话内存涨几 MB，持续不回落。真机探针：mock 端点 7.4KB 回复/轮，600 轮 RSS 33→143.7MB（~150-190KB/轮，线性）。**现象本身可复现，本批 300/450 轮实测 RSS 斜率 ~140-160KB/轮，线性。**

## 终审结论

**1. 活堆增长 ≈37.5KB/轮，全部是 transcript 成比例的设计内留存，不是泄漏。**

关键方法：放弃 pprof 采样（512KB 采样量子下小对象计数噪声极大，前两轮排查的「NewMarkdown 存活 4/轮」「组件 5/轮」「:665 拼接串 3.5 倍超量」均为采样误读），改用 **finalizer 精确计数**（对 Markdown / AssistantMessageComponent 挂 SetFinalizer + expvar 计数器，强制双 GC 后读数）：

```
450 轮：markdown_alive = 450（恰好 1/轮）  component_alive = 450（恰好 1/轮）
每轮存活的 1 个 = transcript 里要显示的最终消息组件（设计内）
中间帧的 Markdown/组件全部正常回收，零多余留存
```

活堆斜率精确测量（expvar 读数时双 GC + ReadMemStats，无采样误差）：

```
2.78MB → 14.00MB / 300 轮 = 37.5KB/轮，线性
除以内容 7.4KB/轮 ≈ 5.0x
而此前被判「干净」的忠实帧探针：10KB/轮 ÷ 1.9KB 内容 ≈ 5.3x —— 同比率
```

前次 handoff 的「40KB/轮 vs 10KB/轮 = 30KB/轮泄漏」对比是**苹果比橘子**：探针内容只有真实场景的 1/4。构成全部与 transcript 成比例：`previousLines` 全量虚拟缓冲（差分渲染设计，一代）、最终 Markdown 的渲染缓存（`Markdown.Render` cachedLines）、agent 消息历史、用户消息组件。pi 的架构（虚拟缓冲 + 渲染缓存 + 历史）同样线性。

**2. RSS 增长（~140-160KB/轮）>> 活堆（37.5KB/轮），差值是 Go 运行时的堆保留行为。**

450 轮实测：heap_alloc（活）19.5MB，heap_idle **41.3MB**（已释放、未归还 OS），heap_sys 67MB ≈ RSS 60MB。根因：每帧全量重渲整个 transcript（差分渲染器设计，pi 同构）→ 会话越长每轮分配量越大（二次方 churn，实测 ~5 次 GC/轮）→ 分配churn推高 arena 高水位 + GOGC 翻倍 + 后台 scavenger 惰性归还 → RSS 线性涨但不主动回落。**这是分配churn的堆高水位，不是对象被扣住。**

**3. pi 对照（终审设计等同性）**：同 mock 场景 180 轮，pi RSS 218→294MB，40 轮窗口斜率 **189KB/轮**；pig 240 轮斜率 **~140KB/轮**。同量级，且 pig 绝对占用低 4-5 倍（66MB vs 294MB）。

**4. 真实缺陷（量级小，解释不了 40KB/轮，但语义正确性问题，已修复）**：

| 缺陷 | 修复 | 回归测试 |
|---|---|---|
| `agent/agent.go` finishRun 正常完成路径漏 `cancel()`——每轮 runCtx 永久挂在父 ctx children map（pi 的 AbortController 出作用域即 GC，Go 必须显式 cancel） | finishRun 收尾 `close(done)` 后 `run.cancel()`（activeRun 已清、Signal() 返回 nil，无人再观察该 ctx；新 run 的 ctx 是兄弟节点不受影响） | `TestAgentCancelsRunContextAfterRunFinishes`（streamFn 捕获 runCtx，turn 结束后断言 Done 关闭） |
| `ai/models.go` Refresh 成功路径漏 cancel——每 provider 每 refresh 泄一个注册（实测全量 Refresh 仅 startup 触发 3 次 × 26 provider，固定成本非逐轮；前次「16/轮」是固定成本÷轮数的算术假象） | identity-checked delete 之后补 `controller.cancel()`（waitOperation 已返回 ⇒ 操作已 settle 或 ctx 已被 supersede/parent 取消，cancel 幂等） | `TestModelsRefreshCancelsPerProviderRefreshContext`（provider 回调捕获 refreshCtx，Refresh 返回后断言 Done 关闭） |

两个测试均经 stash 验证（暂存修复 FAIL → 恢复 PASS）。修后真机复验：pprof `beginProviderRefresh`/`runWithLifecycle`/`roundTrip` 三个 ctx 位点存活归零，活堆斜率不变（37.3KB/轮，设计完好），md/comp 仍恰好 1/轮。

## 复现方式（探针工作流）

```bash
# 1. mock server（已改为 in-process test，env 门控；另开终端跑，Ctrl+C 停）
PIG_MEMLEAK_MOCK=1 go test ./codingagent/modes/interactive/ -run TestMemLeakMockServer -v -timeout 60m
#    SINGLE_CHUNK=1 前缀可切单 chunk 模式

# 2. 配置：/tmp/pig-mem-probe/agent/{models.json,auth.json}
#    models.json 自定义 provider memprobe，baseUrl http://127.0.0.1:18923/v1，api openai-completions
#    auth.json：{"memprobe":{"type":"api_key","key":"sk-mock"}}

# 3. tmux 真 TTY 跑 pig（PIG_PPROF 开诊断端点）
tmux new-session -d -s pig-mem-probe -x 220 -y 50
tmux send-keys -t pig-mem-probe "env PIG_CODING_AGENT_DIR=/tmp/pig-mem-probe/agent PIG_PPROF=127.0.0.1:18924 <pig 二进制> --model memprobe/mock-model" Enter

# 4. send-keys 发消息 ×N；RSS（ps -o rss=）、精确活堆与对象计数（/debug/vars）、heap profile：
curl -s "http://127.0.0.1:18924/debug/pprof/heap?gc=1" -o heapX.pb.gz
go tool pprof -top -sample_index=inuse_space -diff_base heapA.pb.gz heapB.pb.gz
```

## 诊断资产清单

| 资产 | 路径 / 用法 |
|---|---|
| PIG_PPROF 端点（env 门控，**保留**） | `cmd/pig/main.go` `startPprofEndpointIfRequested` |
| mock server（**已改 in-process test**，原 testdata 独立 main 包删除） | `codingagent/modes/interactive/memleak_mock_server_test.go`；`PIG_MEMLEAK_MOCK=1 go test ... -run TestMemLeakMockServer -v -timeout 60m`，SINGLE_CHUNK=1 切单 chunk |
| 探针测试 6 件（components/discard/frames×2/streaming/tui，**env 门控默认跳过**） | `codingagent/modes/interactive/interactive_memleak_{probe,discard,frames,streaming,tui}_test.go`；`PIG_MEMLEAK_PROBE=1 go test ./codingagent/modes/interactive/ -run 'TestMemLeakProbe' -race -v`（约 127s） |
| session 层探针（始终随套件跑，仅 1.3s） | `codingagent/core/agent_session_memleak_probe_test.go` |
| finalizer 精确计数方法论 | 曾以临时插桩（expvar）实测 md/comp 存活数；已随收口移除，需要时按本节思路重挂 |

## 本批其他修正

- **探针测试自身的数据竞争**（全量 `-race` 两处 FAIL）：探针在测试 goroutine 直接 `AddChild`/`UpdateContent`，与 `screen.Start()` 的渲染 goroutine 并发（真实 app 走 mutateUI 串行化，探针漏了这层）。修：容器/组件变更包进 `screen.RunExclusive`（`mutateChat` helper，tui/discard/frames 三文件 6 处）。
- **探针耗时**：interactive 包默认 `-race` 从 126.9s 回落到 6.6s（探针 env 门控）。逐测归因（-race / 无 race）：Streaming 36.5/6.3s、TuiFrames 33.1/24.9s、Frames 27.6/6.5s、Components 15.1/2.6s、Discard 8.6/2.0s、FramesNoRender 5.0/1.0s——慢的主因是 O(N²) 全量重渲 × 每帧 sleep × 双 GC+FreeOSMemory 采样 × race instrumentation 2-6 倍放大。

## 坑与教训

- **pprof 小对象计数不可作数**：MemProfileRate 512KB 采样下，inuse_objects 对 KB 级对象是重量化估计；前两轮据此指认的 4-5/轮组件留存全是假象。判「谁活着」用 finalizer 精确计数，pprof 只看大块空间分布。
- **同比率检验**：判断探针「干净」与真实 app「泄漏」是否矛盾，先对齐单位（留存/内容比），再比绝对值。
- 探针驱动 TUI 组件必须过 `RunExclusive`，否则与渲染 goroutine 竞争（-race 必炸）。
- mock server 从 testdata 独立 main 包改为 env 门控 test：常驻服务型 harness 用 `t.Skip` 门控，勿进全量 suite，也勿单独起构建目标。
- `pkill -f` 模式匹配自身命令行会杀掉执行中的 shell（本批实证两次）：模式用 `^/absolute/path` 锚定或避开。
- tmux 冒烟 env 用 `PIG_CODING_AGENT_DIR`（一个 G）。

## 历史排查记录（2026-09-14 白天 + 深夜，结论已被终审修正，详情见 git 历史 e0d4d15/f1c6501）

- 现象确认、排除面收敛（goroutine/线程/RSS 假象/AgentSession 核心/全局注册表/纯重渲染均排除）、pprof 分配位点指认——其中「组件/渲染管线无罪」（忠实帧探针）结论被终审维持，「组件 4-5/轮存活」「拼接串 3.5 倍」「ctx 泄漏 = 40KB/轮主犯」三个指认被终审修正（前两个为采样误读，第三个量级仅 ~2KB/轮且 refresh 侧为固定成本）。
