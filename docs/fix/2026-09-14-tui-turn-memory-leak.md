# 交互模式逐轮内存泄漏（排查交接，未修复）

- **日期**：2026-09-14 18:32
- **状态**：**排查中，未修复（独立复查已收敛方向）**。现象确认 + 排除面收敛 + 泄漏对象指认到函数行已完成；本日深夜第二 session 独立复现并新增：① 忠实帧探针实证**组件+渲染管线层无罪**（10KB/轮干净、chat.Clear() 全回收）→ 留存点在组件树之外；② 新指认两个 context 注册表泄漏嫌疑（`agent.go` finishRun 漏 cancel、`ai/models.go` refreshCtx 成功路径漏 cancel）。详见文末「独立复查附录」。
- **涉及文件**：
  - 泄漏主现场：`codingagent/modes/interactive/components/assistant_message.go`（UpdateContent）、`tui/markdown.go`（Render/cachedLines）、`tui/tui.go`（applyLineResets）、`ai/api/openai_completions_stream.go:665`
  - 本批新增诊断资产（见文末清单）：`cmd/pig/main.go`（PIG_PPROF 端点）+ 5 个探针测试文件

## 现象

用户报告（dogfooding）：交互模式**每问一句话内存涨几 MB**，持续增长不回落。真机探针复现：mock 端点 4KB 回复/轮，RSS 600 轮从 33MB 涨到 143.7MB（~150-190KB/轮，线性不收敛），泄漏量为消息内容的 ~40 倍。

## 结论（已定论部分）

**存在真实内存泄漏：GC 后活堆以 ~25-40KB/轮 线性增长**（heap profile `gc=1` 强制 GC 后采样，排除 Go RSS/MADV_FREE 假象）。泄漏主体 = 「每轮渲染/流式产物被多份长寿命结构持有」。

## 复现方式

```bash
# 1. mock 端点（`codingagent/modes/interactive/testdata/memleak_mock_server/`（build 见资产清单），SINGLE_CHUNK=1 切单 chunk 模式）
/tmp/pig-mem-probe/mockserver &
# 2. 配置：/tmp/pig-mem-probe/agent/{models.json,auth.json,settings.json}
#    models.json 自定义 provider memprobe，baseUrl http://127.0.0.1:18923/v1，api openai-completions
# 3. tmux 真 TTY 跑 pig（PIG_PPROF 开 heap profile 端点）
tmux new-session -d -s pig-mem-probe -x 220 -y 50
tmux send-keys -t pig-mem-probe "env PIG_CODING_AGENT_DIR=/tmp/pig-mem-probe/agent PIG_PPROF=127.0.0.1:18924 /tmp/pig-mem-probe/pig --model memprobe/mock-model" Enter
# 4. send-keys 发消息 ×N，采样 RSS（ps -o rss= -p $PID）与 heap profile：
curl -s "http://127.0.0.1:18924/debug/pprof/heap?gc=1" -o heapX.pb.gz
go tool pprof -top -sample_index=inuse_space heapX.pb.gz
go tool pprof -top -sample_index=inuse_space -diff_base heapA.pb.gz heapB.pb.gz   # 增量 diff
```

## 证据链

### 1. 现象层（真机 RSS）
- 0→200 轮：33→68.9MB；200→600 轮：66→143.7MB。线性，无收敛。

### 2. 进程内活堆（heap profile，gc=1）
- heapA(200轮)=19.74MB → heapB(600轮)=36.39MB：**41.6KB/轮**（多 chunk mock，9 帧/轮）
- heapC(单chunk 100轮)=10.43MB → heapD(200轮)=12.99MB：**25.6KB/轮**（单 chunk mock，~2 帧/轮）
- **主体泄漏与流式帧数无关**（帧数减 4/5 泄漏仅降 40%）

### 3. 排除清单（勿重查）

| 嫌疑 | 实验 | 结果 |
|---|---|---|
| Go RSS/MADV_FREE 假象 | gctrace + gc=1 heap profile | GC 后活堆仍线性涨，真泄漏 |
| goroutine 泄漏 | SIGQUIT dump | 恒定 35 个（runtime 固有），无同质增长栈 |
| 线程/M 泄漏 | ps -M | 恒定 26 |
| AgentSession 核心 | 进程内探针（含增量流式 mock）40 轮 | +48KB ≈ 消息历史正常量，**干净** |
| 包级全局注册表 | 组件探针 drop 组（丢引用） | GC 全回收（200 轮 +59KB），**干净** |
| 流式中间帧本身 | 组件探针模拟 9 帧 UpdateContent | 5.76 vs 5.63KB/轮，**不是放大器** |
| 纯重渲染 | headless fixed 变体（固定内容 200 帧） | ≈0 泄漏；泄漏 ∝ 内容增长 |
| SSE 调度链（renderTimer/goroutine） | 代码审查 tui.go:1368-1446 | 配对正确，无积累 |
| kitty image 跟踪 / previousLines | 代码审查 tui_main_screen.go | map/切片整体替换，不增长 |

### 4. 分层对账（每轮留存）

```
core.AgentSession（含流式）                    ~1.2 KB/轮（消息历史，正常）
组件树持有（裸 Container = transcript 语义）   5.6 KB/轮（设计内：历史要显示）
+ 真实渲染管线（discard-terminal headless）    10.1 KB/轮（管线 +4.5KB/轮）
真机实测                                      25.6-41.6 KB/轮  ← 差距 = 泄漏主体
```

### 5. 被持有的对象已指认（pprof diff + -list，多 chunk 400 轮净增）

| 存活对象 | 分配点 | 量级 |
|---|---|---|
| 流式拼接串 | `openai_completions_stream.go:665` `block.content.Text += *delta.Content` | **6.55MB（31%），~16KB/轮** |
| Spacer | `AssistantMessageComponent.UpdateContent`（每帧 Clear 重建却净留存 82 个/轮） | 32768 个（39%） |
| 渲染缓存行 | `tui.(*Markdown).Render` | 16384 个（19%） |
| normalize 行 | `TuiBase.applyLineResets` | 15523 个（18%） |
| SSE payload 串 | `encoding/json.literalStore`（**单 chunk 模式主犯**，~1KB/对象，15.4KB/轮） | 单 chunk 净增的 60% |

四个持有点指向同一机制：**transcript 渲染树/事件链对每轮帧产物存在多余引用**（多份副本：原文、AST/渲染行、applyLineResets 包装行、payload 串）。

## 剩余工作（给下一个 session）

> **2026-09-14 深夜更新**：独立复查附录（见文末）已新增两个代码级嫌疑点与新探针；下面第 1 条已过时，以附录「给下一个 agent」为准。

1. **引用链终审**（pprof 只有分配栈没有引用图，需代码审计/二分）：
   - 首要嫌疑：`tui/markdown.go` 的 `cachedLines`/`cachedText` 生命周期——`Container.Clear()` 只清 `Children`（tui.go:411），markdown 实例被替换后其渲染缓存是否经由某容器/快照仍可达；多宽度缓存是否累积
   - 次要嫌疑：text-delta 事件 `Delta`/`Partial` 字段（`AgentEventMessageUpdate`）在 interactive/core 两层的持有链；`AssistantMessageComponent.UpdateContent` 里 `message.Content = normalized` 对 adapter partial 的就地改写（assistant_message.go）是否延长 payload 串引用
   - `applyLineResets` 存活行的持有者（渲染输出被哪个长寿命结构引用）
2. 修复后按流程落地回归测试（stash 验证 FAIL→PASS），并更新本文档状态 + AGENTS.md 行
3. 决定 `PIG_PPROF` 端点去留（建议保留：诊断价值类似 panic-resilience，env 门控零开销）
4. 用户真机场景为 google provider（GEMINI_API_KEY），真机验证可参考 docs/handoffs/verify-matrix.md

## 坑与教训（探针 artifact，防踩）

- **headless 探针的 VirtualTerminal 是污染源**：其 `buf` 为全量行缓冲（scrollback 隐式存储），「新增内容」探针测出 104KB/轮 大部分是模拟终端自身增长，**不可作数**。已用 discard-terminal（输出丢弃）替代——真实管线 + 无 artifact。
- SIGQUIT 栈 dump 会被 TUI 渲染混排，走 stderr 重定向文件。
- pprof diff 模式的 "total" 是绝对值不是净增，净增用两次采样的 inuse total 相减。
- tmux 冒烟 env 用 `PIG_CODING_AGENT_DIR`（一个 G），勿重蹈 T6-2 覆辙。

## 诊断资产清单

| 资产 | 路径 |
|---|---|
| PIG_PPROF 端点（env 门控 pprof，诊断用） | `cmd/pig/main.go` `startPprofEndpointIfRequested` |
| session 层探针（含增量流式 mock） | `codingagent/core/agent_session_memleak_probe_test.go` |
| 组件层探针（dropped/retained/streaming 三变体） | `codingagent/modes/interactive/interactive_memleak_probe_test.go`、`interactive_memleak_streaming_test.go` |
| headless tui 探针（VirtualTerminal 变体，**已知污染**） | `codingagent/modes/interactive/interactive_memleak_tui_test.go` |
| discard-terminal 探针（干净） | `codingagent/modes/interactive/interactive_memleak_discard_test.go` |
| mock server（SSE，SINGLE_CHUNK 开关，**已入库**） | `codingagent/modes/interactive/testdata/memleak_mock_server/main.go`（testdata 不参与构建；`go build -o /tmp/mockserver ./codingagent/modes/interactive/testdata/memleak_mock_server/`） |
| 探针配置与 heap profiles | `/tmp/pig-mem-probe/agent/`、`/tmp/pig-mem-probe/heap{A,B,C,D}.pb.gz`（heapA/B=多chunk 第200/600轮；C/D=单chunk 第100/200轮） |

/tmp 下的探针配置与 heap profiles 重启即失；mock server 已入库，heap profiles 需要时按「复现方式」节重采（profile 是采样快照，重跑 200/600 轮即可复得）。

---

## 独立复查附录（2026-09-14 深夜，第二 session）

第二个 session 从零独立复现并推进排查。**未完成修复**；本轮结论与新增证据如下，下接「给下一个 agent」。

### 复查 1：现象独立复现 ✅（数值可信）

- f1c6501 全新构建（pig + mock server），全新 /tmp/pig-mem-probe 配置，tmux 真 TTY 220×50。
- 400 轮：RSS 35.6MB → 77.6MB，线性 ≈100KB/轮（与前 session 的 150-190KB/轮同量级，差异来自轮间隔/终端尺寸）。
- 活堆裁决（heap profile `gc=1`）：基线 ~6.2MB（大头 `runtime.allocm` 3MB 恒定不增长 + chroma/init 常量）→ 400 轮后 22.3MB = **净增 ~40KB/轮，而真实内容仅 4KB/轮 = ~10 倍冗余留存。泄漏成立。**
- **坑（新）**：端口 18923 被前 session 残留 python3 进程占用，首发请求返回非流式假响应差点误判 mock 失效——开工先 `ss -tlnp | grep 18923`。

### 复查 2：inuse 剖面对象级证据（heapA/B，均 400 轮，总活堆 22.3MB）

| 位点 | inuse_space | inuse_objects | 解读 |
|---|---|---|---|
| `openai_completions_stream.go:665` 拼接点（func1.15） | **5.68MB** | 709 | 实际内容 400×4KB=1.6MB → **3.5 倍中间累积串存活** |
| `tui.(*Markdown).Render` | 4.10MB | 23525 | 缓存行（1638 实例 × ~14 行/实例） |
| `tui.(*TuiBase).applyLineResets` | 3.07MB | 17628 | ≈ 恰好一代全 transcript 行数 → **符合设计 1 倍，无罪** |
| `runtime.allocm` | 3.08MB | 1538 | 基线即有，不增长，无罪 |
| `context.withCancel`（cum） | ~1MB | **9075（≈22/轮）** | 见复查 4，异常 |
| `NewMarkdown` | — | **1638（≈4/轮存活，应 1/轮）** | 中间帧 Markdown 实例被外部扣住 |
| `NewAssistantMessageComponent`（handleEvent 分配） | — | **2048（≈5/轮存活，应 1/轮）** | 旧流式组件代被外部扣住 |

**机制链**：`UpdateContent` 里 `NewMarkdown(strings.TrimSpace(content.Text), …)`——TrimSpace 是子串，**共享该帧累积串的底层数组**；只要一个中间帧的 Markdown 实例活着，那一帧的完整中间串（不只是 4KB 最终串）就被钉住 → 解释拼接点 3.5 倍超量。所以真正的谜题收敛为：**谁让 ~4/轮的旧 Markdown / ~5/轮的旧 AssistantMessageComponent 可达**（每帧 UpdateContent 都 Clear+重建 children，`Container.Clear()` 即 `Children = nil`，干净）。

### 复查 3：忠实帧探针二分 —— 组件+渲染管线层无罪（关键排除）

新增 `interactive_memleak_frames_test.go`（`TestMemLeakProbeFrames` + 控制组 `TestMemLeakProbeFramesNoRender`），补上前一批探针的两个结构盲区：① 旧 streaming 探针每帧 partial 是同一 string 的切片（substring 共享底层数组，结构上不可能测出中间串留存），本探针每帧**全新累积串**（忠实模拟 `s += delta`）；② 补上真实 `TuiMainScreen.RenderNow` 渲染帧。

结果（120 轮）：**10.15 KiB/轮（带每帧渲染）≈ 10.00 KiB/轮（不带）≈ 设计内预算**；`chat.Clear()` 后全部回收；丢 screen 回收 ≈0。**结论：组件树 + TuiMainScreen 渲染管线不是泄漏现场**；真实应用里被扣住的旧组件代必须从组件树**之外**可达（事件链 / ctx 树 / adapter 层）。注意真实应用 40KB/轮与本探针 10KB/轮的缺口 ≈ 30KB/轮，方向即上文组件树之外的层。

### 复查 4：新指认的两个代码级嫌疑（Go context 注册表泄漏形态，未终审）

1. **`agent/agent.go` `runWithLifecycle`（:693）→ `finishRun()` 漏 cancel**：`runCtx, cancel := context.WithCancel(ctx)` 后，正常完成路径只 `close(done)` + `activeRun = nil`，**从不调 cancel（只有 Abort 路径 cancel）** → 每轮 runCtx 永久注册在父 session ctx 的 children map，其整棵子树（本轮 streamFn 链上派生的所有 ctx/闭包）随之存活。修复语义安全：finishRun 后 `Signal()` 返回 nil，无人再观察该 ctx（pi TS 的 AbortController 出作用域即 GC，Go 必须显式 cancel）。
2. **`ai/models.go` `beginProviderRefresh`（:407）refreshCtx 成功路径漏 cancel**：只有 supersede 会 cancel 并 delete，正常完成从不 cancel。inuse 实证：`beginProviderRefresh` 位点存活 512KB / 6554 对象（**≈16/轮**，分配栈 `modelsImpl.Refresh.func1`）；另有 `net/http roundTrip` 的 `WithCancelCause` 存活 512KB / 2521 对象（≈6/轮，下游持有者未查——嫌疑方向：http.Request/Response 被泄漏 ctx 钉住，或 SSE 响应体某路径未完全排干）。

**量化诚实声明**：两点 ctx 自身空间合计 ~2.6KB/轮，不足以独力解释 40KB/轮——若它们钉住的闭包子树进而钉住中间串/组件，即为「谁持有」的答案；此步未验证。16/轮的刷新触发频率也未解释（每轮哪个层触发全 provider refresh 待查）。对象数是 pprof 小对象采样估计（噪声大），空间值更可信。

### 复查 5：本轮新增排除清单（在前文档基础上追加）

| 嫌疑 | 结论 |
|---|---|
| `Container.Clear()` 泄旧 children | `Children = nil`，干净（tui.go:411） |
| chatContainer 整体替换/每轮重建 | 无；构造一次（interactive_mode.go:480），Clear 仅 reload/fullscreen 等路径 |
| `Container.mouseLayout` 扣旧帧 | 每次 Render 整体重建、只指当前 children（tui.go:492），仅存一代 |
| MouseRegion 全局注册表 | 纯 struct（mouse_region.go），无 |
| `AgentSession.handleAgentEvent` 每事件 append | 无留存（agent_session.go:942 全读）；`lastAssistantMessage` 单指针覆盖 |
| `TuiMainScreen.previousLines` 多代累积 | 每帧整体替换（tui_main_screen.go:433），一代，符合设计 |
| 每帧真实渲染是否放大器 | 忠实探针实证：10.15 vs 10.00 KiB/轮，不是 |

### 给下一个 agent（按优先序）

1. 修 `agent/agent.go` `finishRun`：补 `cancel()`（在 close(done) 后调用，或 defer 但须在锁外语义清晰）+ 回归测试（stash 验证 FAIL→PASS；可用 runtime/PPROF 或 ctx.Done 断言）。同式修 `ai/models.go` Refresh 成功路径（publish 返回后 cancel refreshCtx，注意勿与 supersede 竞态：cancel 应在 refreshControllers 取出/失效本代之后）。
2. 修完重跑本档「复现方式」节真机探针（先清 18923 端口残留），heap diff 看 40KB/轮降幅与 roundTrip 位点是否消失。
3. 若 ctx 修复后仍有大头残留：回查 `NewMarkdown`/`NewAssistantMessageComponent` 存活 4-5/轮的外部持有者——建议直接在 `assistant_message.go` UpdateContent 加临时 `runtime.SetFinalizer` 探针（finalizer 触发即证无外部持有，不触发即有），比 pprof 猜引用链快。
4. 解释 `beginProviderRefresh` 16/轮：查每轮触发 `modelsImpl.Refresh` 的调用链（model-runtime prepareRequest？每 prompt 一次还是每请求一次），确认是否设计内。

### 本轮新增诊断资产

| 资产 | 路径 |
|---|---|
| 忠实帧探针（每帧全新累积串 + 真实渲染 + 二分回收） | `codingagent/modes/interactive/interactive_memleak_frames_test.go`（TestMemLeakProbeFrames / …NoRender） |
| 第二 session heap profiles | `/tmp/pig-mem-probe/heap0.pb.gz`（基线）、`heapA.pb.gz`（401 轮, 18:47）、`heapB.pb.gz`（同轮, 19:00）；heapA/B 内容一致（两次采样间无新轮次） |
| 环境坑 | 18923 端口被前 session 残留 python3 占用过；/tmp/pig-mem-probe 进程已清（tmux session 已 kill），文件与 profiles 留存 |
