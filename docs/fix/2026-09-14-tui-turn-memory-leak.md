# 交互模式逐轮内存泄漏（排查交接，未修复）

- **日期**：2026-09-14 18:32
- **状态**：**排查中，未修复**。现象确认 + 排除面收敛 + 泄漏对象指认到函数行已完成；「谁持有」的引用链终审未完成。本文档为下一个 session 的交接记录。
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
