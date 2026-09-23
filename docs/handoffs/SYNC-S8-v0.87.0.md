# S8：pi v0.86.1 → v0.87.0 基线同步计划（✅ 全部收口，2026-09-23）

**状态**：✅ **全部收口**。spec 基线从 v0.86.1（S7 收口态）推进到 v0.87.0（16787ad5b，18 commits，+7526/-810）。
**范围裁决（用户已拍板）**：chord（10d1ad621 transactional replicated state + pico3/chord.ts）**不迁**；experimental/services 四文件**不迁**；各包 CHANGELOG/README/docs 不迁。其余全部消化。

## 0.1 实际交付（收尾批后终稿，2026-09-23）

| 批次 | 状态 | handoff |
|---|---|---|
| S8-1 agent 回调面重构 | ✅ | `S8-1.md` |
| S8-2 session-manager projection + context_edit | ✅ | `S8-2.md` |
| S8-3 compaction projection 化 | ✅ | `S8-3.md` |
| S8-4 agent-session boundaries + extensions 事件面 | ✅ | `S8-4.md` |
| S8-5 interactive + UI + export-html + crash-log | ✅ | `S8-5.md` |
| S8-6 ai/catalog strict 翻转 + image limits | ✅ | `S8-6.md` |
| S8-7 杂项小修（frontmatter 诊断/cache-warmer deadline/bug offline/GIF/jump-to-end） | ✅ | `S8-7.md` |
| 收尾（Version bump + 双向兼容 4 门 + 文档） | ✅ | 本节 |

### 收尾批交付明细

1. **Version bump 0.86.1→0.87.0**（`codingagent/config.go` + `config_test.go` 期望 tag 同步，`TestVersionTracksPiBaseline` 绿）。
2. **双向兼容实测（v3 层四向全通，隔离目录 `/tmp/s8-final` + 本地 mock OpenAI 端点 + 独立安装的 `@earendil-works/pi-coding-agent@0.87.0` dist）**：
   - pig 读 pi v0.87.0 写的真实 v3 会话（`-p` 真跑落盘）→ ListSessions/OpenSession/8 类条目/GetBranch/BuildSessionProjection 全通；pi 的 user content 是 blocks 数组形态，pig 解入 UserContent.Parts 正常；
   - pi v0.87.0 读 pig 写的 v3 会话：node API `SessionManager.open + getEntries + buildSessionProjection` 全对，且 **pi CLI `--continue -p` 真跑把 pig 的历史带进模型上下文**（mock 捕获请求实证），pi 追加的条目 pig 再读全通；
   - **context_edit 专项双向**：pig 写（replacement blocks + null omit）→ pi v0.87.0 dist 读回投影语义一致；pi 写（replacement string 归一为 text block + null）→ pig 读回投影语义一致；
   - **v4 门（harness jsonl）声明沿用 S1 实测**：`packages/agent/src/harness/session/jsonl/{codec,types}.ts` 在 v0.85.1..v0.87.0 零 diff、pig v4 写入器（冻结区）零改动，线格式不变 ⇒ S1 的真实代码互操作结论对 v0.87.0 成立。
3. **全 catalog 对照**：S8-6 已做同刻逐字段对照（41 家 0 diff），S8-7 未碰 catalog，结论有效；residential golden 由 `TestBuiltinCatalogMatchesPiData` 在全量回归内持续锁定。
4. **实验教训（门 4 首跑误报）**：pi `continueRecent` 按 cwd 子目录（`sessions/<encoded-cwd>/`）找最近会话——在 `~/Projects/pig` cwd 下跑 pi `--continue` 找的是 `--home-lee-Projects-pig--/` 而非隔离目录里的会话；必须与被续会话同 cwd 运行。pig 同语义（两侧 1:1）。
5. 探针不留文件：driver/备份/mock 已删（S1 先例）；pi@0.87.0 独立安装在 `/tmp`（临时）。

**验收**：全量 `-race -count=1 -timeout 600s` 单次 rc=0（29 包，`/tmp/pig-test-out-s8-final.log`）。**S8 至此全部收口**。

## 0. 增量盘点（18 commits → pig 工作项）

| commit | 主题 | pig 归属 |
|---|---|---|
| 466db0fec | **canonical session context boundaries**（主体 +3632/-304） | S8-1/2/3/4/5 |
| f5c946480 | image input limits（#9631，+415） | S8-4（agent_session 半区）+ S8-6（ai/catalog/外围） |
| 890f92088 + 8bfef4de8 + eaf72ed4d | strict 默认翻转 + 测试期望 | S8-6 |
| aef5fc429 | #9846 context handlers 保留 prompt/tool state | S8-4 |
| 63787ee6b | crash stacks 识别扩展 | S8-5 |
| b6419322e | #9830 prompt frontmatter 诊断 | S8-7 |
| 3390bd936 | skip late cache warming refreshes | S8-7 |
| c7cdb460a + 1e0fe2049 | /bug offline（净效果：仅 upload 分支拦 offline） | S8-7 |
| 47a18e37b | GIF 签名完整性（2 行） | S8-7 |
| 8c7279378 | #9842 jump-to-end label 随 scrollbar 隐藏不漂移 | S8-7 |
| 10d1ad621 | chord | ❌ 不做 |
| 19451accd / 9ac95c715 / 7f06f9cf1 / 16787ad5b | changelog/release | ❌ 不迁 |

## 1. 三条硬变化（先读，决定全局姿态）

1. **JSONL 新 entry 类型 `context_edit`（线格式，硬约束）**：`{type:"context_edit", targetId, replacement: {content} | null}`——null=从模型上下文省略，非 null=仅替换 content（assistant/toolResult 的 string 归一为单 text block）。append-only、branch-relative、同目标 latest-wins。**retain-none compaction**：`firstKeptEntryId` 可为 compaction 自身 ID。**无扩展路径也会写**（overflow/length recovery 的 omissions）→ 双向兼容门必须重跑（pig↔pi v0.87.0 互读 context_edit；老版本 pi 读 pig 新会话会不识别，handoff 记录声明）。
2. **strict 默认翻转（#9816，用户可见行为变化）**：openai-completions `detectCompat` 的 `supportsStrictMode` 兜底从「排除少数已知」改为**恒 false**，capable 模型由生成器显式开启 → catalog 全量重生成，自定义 provider（models.json 无 compat 声明）工具调用从 strict 变非 strict。
3. **agent-loop 回调面重构（破坏性 API 变化）**：`shouldStopAfterTurn` → `finishTurn`（返回 `{action:"continue"|"end"}` decision；**turn_end 的 emit 时点移到 finishTurn 之后**；error/aborted 路径也构造 lastCompletedTurn 并调用 finishTurn）+ 新增 `prepareRequest`（每次 conversational 请求前、含首轮，pendingMessages 已 append；返回 AgentRequestUpdate 替换 context/model/thinkingLevel）。`prepareNextTurn` 保留。**pig 全仓 grep 旧名零残留**（重命名整体改规则）。

## 2. 批次拆分（依赖序；单批 ≤6000 pig 行；上下文过 25% 做中点检查）

依赖图：S8-1、S8-2、S8-6、S8-7 相互独立可并行；S8-3 依赖 S8-2；S8-4 依赖 1+2+3；S8-5 依赖 4。

### S8-1：agent 包回调面重构（~1800 pig 行）
- pi 源：`packages/agent/src/types.ts`（回调面段 +67）、`agent-loop.ts`（+47）、`agent.ts`（+48）
- pi 测试：`packages/agent/test/agent-loop.test.ts`（+464）、`agent.test.ts`（+277）
- pig：`agent/types.go` / `agent/agent_loop.go` / `agent/agent.go` + 对应 _test
- 要点：ShouldStopAfterTurnContext→AgentTurnContext 改名（PrepareNextTurnContext 别名跟着换）；FinishTurn/AgentTurnDecision/PrepareRequestContext/AgentRequestUpdate/PrepareRequest 新类型；explicitContinuation 与 hasMoreToolCalls/steering 的交互逐行对齐 runLoop 新形态；S6-3a 对齐过的「prepareNextTurn 时点 + 准备期补拉 steering」按新循环重新核对。

### S8-2：session-manager projection + context_edit 线格式（~1800 pig 行）
- pi 源：`packages/coding-agent/src/core/session-manager.ts`（+157）
- pi 测试：`packages/coding-agent/test/session-context-edit.test.ts`（+364）+ session-manager 既有 7 份测试期望更新
- pig：`codingagent/core/session_manager.go`（`BuildSessionContext` → `BuildSessionProjection` 全仓改名，注意 `agent/harness/session/context.go` 的同名 v4 函数**不动**）+ 新 `session_manager_context_edit_test.go` + SessionEntry 解码 union 加 context_edit 分支
- 类型面：ContextEditEntry/ContextEditableContent/ProjectedSessionEntry/SessionProjection/projectContextEntry（edits map + branch-relative + latest-wins）/AppendContextEdit。**改名涟漪全量清点**：compaction、session_export、tree navigation、S5a 边界重映射等全部消费点同批改齐。

### S8-3：compaction projection 化 + recovery 原语（~1200 pig 行）
- pi 源：`packages/coding-agent/src/core/compaction/compaction.ts`（+215）
- pi 测试：本次无独立 compaction 测试增量（行为由 S8-4 的 boundaries e2e 覆盖），pig 侧 compaction 既有测试期望更新（buildSessionContext→projection）
- pig：`codingagent/core/compaction.go` + `compaction_utils.go`
- 要点：`EstimateProjectedContextTokens`（usage 失效检查：lastUsage 对应 entry 之后若出现 context_edit/compaction 则弃用 usage 改逐条估算；system sections 计入）；cut point/summarization 走 projected entries；retain-none（firstKeptEntryId=自身）；cut point 规则扩展（omitted assistant attempt 后缀可推进 kept boundary）；recovery ordering 语义（docs/compaction.md 新段）落注释。recovery 编排本体在 S8-4。

### S8-4：agent-session boundaries + extensions 事件面（~3800 pig 行，最大批）
- pi 源：`core/agent-session.ts`（466 的 +525 + f5c9 的 +69，同文件一次开合）、`core/extensions/types.ts`（+101）、`core/extensions/runner.ts`（+150）、`core/extensions/index.ts`、`core/index.ts`/`src/index.ts` 导出桶、`core/sdk.ts`（+8）、`core/agent-session-runtime.ts`（±2）
- pi 测试：`test/suite/agent-session-boundaries.test.ts`（+1062）、`test/extensions-runner.test.ts`（+143）、`test/suite/regressions/9789-context-handler-system-messages.test.ts`（+226，归属 aef5fc429 勿漏）、`test/suite/harness.ts`（+1）、`agent-session-concurrent.test.ts`（+1）
- pig：`core/agent_session.go`、`core/extensions_types.go`、`core/extensions_runner.go`、`core/sdk.go`、`core/doc.go`
- 要点：
  - 扩展面：SessionBoundaryDraft 四族（custom/custom_message/context_edit/compaction，compaction draft 的 firstKeptEntryId 可 null）、BoundaryState/BoundaryContextPreview/AgentActivityOutcome、新事件 `agent_before_settle`、`turn_end`/`agent_end` 扩展 BoundaryState + messageEntryId/toolResultEntryIds；runner EmitBoundary 收敛语义 + #9846 context handlers 保留 prompt/tool state
  - agent_session：_installAgentRequestProjection（provider 请求吃 SessionManager projection + agent.state.tools 可执行实现）、_dispatchTurnEndBoundary、_entryIdsByMessage（**WeakMap→pig map 是声明偏差**：注意 state.messages 浅拷贝快照破坏指针身份，S6-3a DeepEqual 先例）、_boundaryDispatchedMessages、agent_before_settle 三态（_isBeforeSettle/_abortDuringBeforeSettle/_deferredSettledActions）、**recovery 编排**（persist → turn_end → agent_end → append omissions → compaction → fresh run retry；失败保留 omissions 不重试）
  - image limits 消费半区（+69：贴图入会话前的限额检查）
  - **AgentSessionEvent 的 turn_end/agent_end 新字段 + MarshalJSON 同步**（--mode json 线格式）
  - sdk.go：Agent 构造期 initialState.messages（原 hasExistingSession 后装删除）
  - panic resilience：emitBoundary 全路径套现有收容

### S8-5：interactive + UI + export-html + crash-log（~700 pig 行）
- pi 源：`modes/interactive/interactive-mode.ts`（466 +38 + 63787 +38）、`modes/interactive/components/tree-selector.ts`（+7）、`core/crash-log.ts`（findExtensionStackMatches +77）、`core/export-html/template.js`（+7）
- pi 测试：`test/tree-selector.test.ts`（+37）、`test/crash-log.test.ts`（+89）
- pig：`modes/interactive/interactive_mode.go`、`modes/interactive/components/tree_selector.go`、`core/crash_log.go`、`core/exporthtml/` 模板**逐字节重 embed**（v0.87.0 template.js）+ golden 重跑
- 要点：tree-selector 三处 context_edit（search parts/树渲染/dim 标签）与 export-html 三处同构；crash 扩展识别的路径归一/大小写（盘符）规则逐条移植。

### S8-6：ai/catalog：strict 翻转 + image limits 上游（~1500 pig 行）
- pi 源：`packages/ai/src/types.ts`（ModelImageResizeOptions/ModelImageInputLimits/ModelInputLimits + strict 注释翻转）、`api/openai-completions.ts`（detectCompat 恒 false）、`providers/faux.ts`（+2）、`scripts/generate-models.ts`（applyImageInputMetadata + DEFAULT_IMAGE_RESIZE +39）
- pi 测试：`ai/test/providers.test.ts`（+49）、`openai-completions-tool-choice.test.ts`（±78）、`cache-retention.test.ts`（±2）、`fireworks-models.test.ts`（+1）
- pig：`ai/types/types.go`、`ai/api/openai_completions*.go`、`ai/providers/faux.go`、`ai/scripts/generatemodels`、**catalog 全量重生成**（worktree node 直跑 pi 生成器 + pig generatemodels 对照，S7 流程留痕）+ core 涟漪：`core/model_config.go`（inputLimits override 深合并 +18——**检查 mergeOverride 家族别漏新字段，S7 教训**）、`core/provider_composer.go`（+23）、`core/tools/read.go`（+12）、`codingagent/utils/tool_result_images.go`（+9）、`cli/file-processor.go`、`cmd/pig/main.go`（-10 挪位置）；测试：`core/model_registry_test.go`（+48 + eaf72 的 opus-4→4.1 期望）、image-resize-callers 对应（+44）、agent-session-prompt 限额（+54）、tool-result-images（+10）。

### S8-7：杂项小修（~900 pig 行，随时可插）
- b6419322e：`core/prompt_templates.go` loadTemplateFromFile 改返回 diagnostics（ResourceDiagnostic 复用）+ `core/resource_loader_default.go`（+8）+ pi 测试 prompt-templates（+42）/resource-loader（+22）
- 3390bd936：`core/cache_warmer.go`（+17，S6-3a 的 fake clock seam 上加）+ pi 测试（+40）
- c7cdb460a+1e0fe2049：`modes/interactive/bug_report.go`——仅 upload 分支拦 offline；**env 名裁决**：pi 用 `PI_OFFLINE`，pig 倾向读 `PIG_OFFLINE` 回落 `PI_OFFLINE`（批次内定，写偏差）
- 47a18e37b：`agent/harness/tools/image.go` + `codingagent/utils/mime.go` 各 1 行 GIF 签名 + harness tools 测试（+5）
- 8c7279378：`tui/tui_alt_screen.go`（+6/-3）+ pi 测试（+47）

## 3. 风险与坑（按批次会撞的）

1. **S8-1 事件顺序变化**：turn_end 移到 finishTurn 之后、error/aborted 也调 finishTurn——pig 既有事件序列测试（S6-3a/#9340 等）期望要动，先跑 agent 包测试看红面再改。
2. **S8-2 改名涟漪**：BuildSessionContext 在 pig 有多个消费层（compaction/session_export/tree navigation/sdk），一次 grep 清点后整体改；`agent/harness/session/context.go` 同名 v4 函数不动。
3. **S8-4 双形态陷阱重灾区**：`_findPersistedMessageEntryId` 走 WeakMap→pig 指针 map，但 agent 消息流经值/指针双形态（09-11 fix 教训）+ 浅拷贝快照；boundary dispatch 的去重（_boundaryDispatchedMessages）同理。**每处身份判断先用真机形态探针验证再定 key**。
4. **context_edit 是用户可见格式演进**：pig 写出的会话需要 pi ≥ v0.87.0 才能读；双向兼容 4 门重跑 + handoff 声明。
5. **S8-6 catalog 重生成**：inputLimits 进 catalog 后 merge 家族（mergeOverrideNonNil 等）必须带上新字段深合并；OpenRouter 上游漂移按 S7 先例甄别（跑两次生成器对照）；strict 翻转会改大量模型的 catalog 字段——golden 对照时预期大面积 diff，逐字段确认属于翻转本身。
6. **大文件纪律**：interactive_mode.go（7k+ 行）与 agent_session.go 的改动合并为最少 edit 次数；测试一次读齐 API 签名再动笔（S6-3b 复盘三条全适用）。
7. **pi 测试移植惯用陷阱**：非 ASCII 字面量先 hexdump（S6-2 韩文字符教训）；fake timers 用例走 pig seam（deviceCodeNow/cache-warner fake clock 先例）；suite/harness.ts 的 fake 会话基建 +1 行变化先看再写 boundaries e2e。
8. **export-html 模板**：逐字节替换后 golden 对照基准（P8-EH1 的 131 行会话 payload 零差异）重跑一次。

## 4. 收尾动作（S8 全部收口时）

1. `codingagent.Version` bump 0.87.0（TestVersionTracksPiBaseline 会红着提醒）
2. 双向兼容实测 4 门重跑（pig↔pi v0.87.0，含 context_edit 专项）
3. 全 catalog 对照（S7 的 dumpcatalog 探针流程；可顺手固化 dump+compare 脚本——S7 建议，非必须）
4. AGENTS.md 进度快照 + 本 handoff 补「实际交付/偏差」段；README 无 provider 增量不动，strict 行为变化在 handoff 记录即可
5. verify-matrix：无新 provider，不动

## 5. 明确不要读的

`packages/chord/**`、`packages/agent/src/harness/pico3/**`、`packages/coding-agent/src/experimental/**`、`packages/durable|protocol|server|client|session-backends|telemetry|evals/**`、各包 CHANGELOG/package.json、lock 文件。
