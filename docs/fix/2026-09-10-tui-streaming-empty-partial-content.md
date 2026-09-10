# 2026-09-10：TUI 流式输出不生效（adapter 空 Content + loader 幽灵副本）

日期：2026-09-10 CST（排查 12:30 起，修复约 13:30 完成）
状态：✅ 两项均已修复并经 tmux 真 TTY 冒烟验证，全量 `-race` 回归绿
涉及批次：Phase 2 openai-completions adapter（根因）+ T5a-1/T5b status-indicator（次生）

用户报告：pig 交互模式（TUI）下模型回复**不是流式输出的**——整个流式期间屏幕完全静止，文本在消息结束时一次性出现。print 模式（`-p`）无感（文本本来就在 message_end 才输出），所以此前 T5b 冒烟"多轮流式回复全通"只验证了最终文本出现、没验证中间帧，bug 一直未暴露。

---

## 一、openai-completions adapter 流式期间 `output.Content` 恒为空（根因）

### 现象

- 交互模式发消息后，spinner 之外屏幕零变化；回答在 ~7s（消息结束）时整段出现。
- tmux 逐秒采样 + `doRender` 临时插桩实证：流式期间 110/116 次渲染是 **no-change**，渲染行中**从未出现**流式文本（只在 message_end 后出现）。
- print 模式直连同端点完全正常 → 排除 provider 层。

### 排查路径（排除法留档）

1. TUI 渲染节流（`tui.go` RequestRender/scheduleRender/16ms throttle）→ 插桩证明 timer 正常触发（9s 内 229 次）；
2. `TuiMainScreen.doRender` diff 逻辑 → 正常，110 次 no-change 说明**组件树本身没变**；
3. agent loop / AgentSession / interactive_mode 的 `message_update` 全链路 → 接通，事件每 300ms 到达；
4. adapter 直连探针（`StreamSimple` 逐事件计时）→ delta 每 300ms 准时到达，**但 `Partial.Content` 全程为空**；
5. 对照 pi 源码 → 确认移植偏差。

### 根因

pi（`packages/ai/src/api/openai-completions.ts:359`）：

```ts
const blocks = output.content as StreamingBlock[];  // 同一个数组引用！
```

`blocks` 就是 `output.content` 本身：每个 `ensureTextBlock()` 把 block push 进去时 partial 即携带内容，后续 delta 原地改 block 对象，`output.content` **增量生长**——每个事件的 `partial` 都带已累积文本。

pig（`ai/api/openai_completions_stream.go`）移植时把 `blocks` 变成独立局部切片，`output.Content` **只在结尾（EventDone 前）和 fail 路径（assembleContent）赋值**。于是流式过程中每个事件的 `Partial.Content` 都是空 → `AssistantMessageComponent.UpdateContent` 渲染出空消息 → diff 渲染判定无变化 → 屏幕冻结。

顺带暴露的潜伏错误：happy path 的 `!hasFinishReason` 分支读 `output.Content` 判断是否有 toolUse——修复前恒为空，toolUse 永远判不出来（修好后自动正确，与 pi 一致）。

### 修复

**方案取舍**：pi 语义的 Go 直译是把 block content 全改指针、创建即入 `output.Content`——但那会波及 agent loop / reducer / compaction / transform_messages 等十几处值类型断言（`.(types.ToolCall)` 等），风险面过大。采用**等价且局部**的方案：保持仓内"Content 存值类型"约定，每次 push 事件前把 blocks 快照进 `output.Content`（每事件 O(blocks) 浅拷贝，代价可忽略）。

`ai/api/openai_completions_stream.go`：

1. 新增 `syncContent()`：把 `blocks` 重建为 `output.Content` 快照；
2. 新增 `push()` 包装：`syncContent()` + `stream.Push(event)`，goroutine 内全部 12 处事件 push 改走 `push`（start/text/thinking/toolcall 的 start/delta/end + done）；fail 路径保持 `assembleContent()`（含 finalize，对应 pi catch 块的 scratch 剥离）；
3. 删除收尾处原有的手工重建（done 走 `push` 时已快照），替换为一行注释；
4. 顶部注释声明偏差：**事件携带 push 时刻的内容快照，非活引用**（pi 的 partial 是同一活对象；Go 值语义下的等价收敛）。

### 连带修复：事件 Partial 快照化（data race）

首版修复后被 `-race` 抓到：事件携带的 `Partial` 是生产者持续改写的同一 `*AssistantMessage`，消费方（agent loop / TUI）在另一 goroutine 读取 → race。pi 靠单线程事件循环天然安全，pig 必须显式切断共享。

`push()` 内对 `event.Partial` / `event.Message` 做浅拷贝快照。浅拷贝即安全的原因（已核实）：`syncContent` 每次 push 都新分配 Content 底层数组（旧数组不可变）；`AssistantMessage` 其余字段全部是"整体赋值"而非"透过共享引用原地改写"（Usage 整结构体赋值、Diagnostics append 重赋值等）。

---

## 二、StatusIndicator 按值拷贝 Loader → spinner 动画跑在"幽灵副本"上（次生）

### 现象

流式期间 spinner 冻结在第一帧 `⠋`，但渲染节流器仍以 ~80ms（= loader 默认帧间隔）稳定触发 no-change 渲染——动画 goroutine 活着，变化却不可见。

### 根因

`components/status_indicator.go`：

```go
Loader: *tui.NewLoader(...)   // 按值拷贝！
```

`NewLoader` 构造时 `SetIndicator → Start` **已经启动了动画 ticker goroutine**（捕获堆上原始 `*Loader`），随后 `*tui.NewLoader(...)` 把整个 Loader 按值拷贝进 StatusIndicator。结果：

- ticker 每帧更新的是被丢弃的**原件**（其 `ui` 引用还在触发 RequestRender → 解释了 ~80ms 的 no-change 渲染节奏）；
- 挂在组件树里的**副本**永远停在第一帧；
- `NewRetryStatusIndicator` 的 `StatusIndicator: *NewStatusIndicator(...)` 二次拷贝同理。

顺带：现有测试 `disposes retry countdown updates` 能过纯属巧合——stopCh 通道值随拷贝共享，`close` 恰好停掉了幽灵 goroutine；而 countdown 的 `SetMessage` 之所以可见，是因为它改的是树内副本自身。

### 修复

`StatusIndicator` 改为**指针嵌入** `*tui.Loader`（构造处去掉 `*` 解引用），动画 goroutine 与渲染实例归一；`NewRetryStatusIndicator` 的 StatusIndicator 值拷贝现在只拷指针+Kind，安全。文件头注释声明该偏差缘由（pi 的 class extends → Go 共享实例）。

---

## 验证

- adapter 探针（mock 端点逐 token 300ms）：`text_delta` 事件数逐 300ms 到达，`partialContentBytes` 6→9→15→…→78 增量生长 ✓
- ai/api 全部测试（含 `openai_completions_test.go` 的 final message Content 值类型断言）`-race` 全绿 ✓（线格式不变：Content 仍存值类型，JSON 序列化逐字节一致）
- tmux 真 TTY 冒烟（mock SSE + `PIG_CODING_AGENT_DIR` 隔离目录）：
  - 文本逐段出现：t=1s「你好，这是一段」→ t=2s「…用来测试流式输出」→ … → t=7s 全文 ✓
  - spinner 帧轮转：⠸ ⠦ ⠏ ⠹ ⠦ ⠏ ⠹ ⠴ ✓
- 全仓 `go test ./... -race -count=1` 两轮：第二轮零 FAIL ✓；`go build`/`go vet`/`gofmt` 干净 ✓

## 遗留观察

- **`TestBashTool` 单次 flaky race**（`tools_test.go:821` vs `bash.go:637/662`）：全量跑中出现一次，单独 `-count=5` 及后续全量均不复现。与本次改动无依赖关系（core/tools 不消费 adapter 流事件），疑为 bash 工具真实进程 spawn 的时序 flake，留观；再现时单独排查。

## 改动文件

- `ai/api/openai_completions_stream.go`（syncContent/push 快照化 + 12 处 push 点替换 + 收尾重建删除 + 注释）
- `codingagent/modes/interactive/components/status_indicator.go`（Loader 指针嵌入）

## 复现方法备忘

```bash
# mock 端点（逐 token 300ms 的 SSE）见 /tmp/pig_streamtest/mock_server.py 模式：
# ThreadingHTTPServer，POST /v1/chat/completions，stream=true 时逐块写
# data: {...chat.completion.chunk...}\n\n 并 flush，sleep(0.3)

# 隔离配置目录
mkdir -p /tmp/pig_streamtest/agent
cat > /tmp/pig_streamtest/agent/models.json   # custom provider, baseUrl=http://127.0.0.1:18923/v1, api=openai-completions
cat > /tmp/pig_streamtest/agent/auth.json     # {"<provider>": {"type": "api_key", "key": "sk-mock"}}  ← 注意 type 是 api_key 不是 api
cat > /tmp/pig_streamtest/agent/trust.json    # {"/tmp/pig_streamtest/proj": true}

# 冒烟：tmux 专属 session 里跑交互模式，逐 300ms capture-pane 采样文本增长与 spinner 帧
tmux new-session -d -s pigstream -x 120 -y 40 "cd /tmp/pig_streamtest/proj && env PIG_CODING_AGENT_DIR=/tmp/pig_streamtest/agent ./pig"
```

排查方法论留档：**"不流式"这类问题要在三层分别取证**——① adapter 直连探针（隔离 provider 层）；② 渲染层插桩（no-change 计数证明是组件树没变而非渲染坏了）；③ pi/pig 源码逐行对照（TS 引用共享 vs Go 值语义的语义落差是本项目最高频的移植偏差源）。
