# PROV-VOLC-PLAN：火山引擎 Coding Plan builtin provider

**状态**：✅ 完成（2026-09-10）。**归档位置**：`docs/feat/`（非批次性功能交付记录，不属 handoffs/ 批次交接协议）。被后续迭代取代时在此标注。
**范围**：非 §4 计划批次，用户临时插入（"把火山 coding plan 做成 builtin provider"）。

## 本批完成

### 新建/改动

| 文件 | 内容 |
|---|---|
| `ai/providers/volcengine_plan.go` | 新建：`VolcenginePlanProvider()` + 手写模型表 `volcenginePlanModels()`（9 模型） |
| `ai/providers/all.go` | `BuiltinProviders()` 注册（together 与 zai 之间，字母序）→ 16 家 |
| `ai/types/types.go` | ① `ProviderVolcenginePlan = "volcengine-plan"`；② **pig-only** `ThinkingFormatVolcengine = "volcengine"`（含探测结论注释） |
| `ai/index.go` | 上述两个常量的再导出别名 |
| `ai/api/openai_completions_stream.go` | `buildParams` 新增 volcengine thinking 分支（紧跟 zai 分支之后；**未动任何 pi 既有分支**） |
| `ai/utils/overflow.go` | 追加 2 条 ark 溢出 pattern（pig-only 段，注释声明） |
| `ai/providers/volcengine_plan_test.go` | 新建 4 测试函数（表锁定 / 请求形状 / thinking 编码 4 子测试 / 真 API 冒烟） |
| `ai/utils/overflow_test.go` | 追加 2 个 ark pattern 测试 |
| `ai/providers/all_test.go` | want 列表 ×2、provider 计数 15→16、TestProviderMetadata 加一行 |

### 测试

全仓 1087 测试函数，`go test ./... -race -count=1` 26 包全绿；`go build`/`go vet`/`gofmt -l` 干净。

### 真 API 冒烟（用户自用 Coding Plan，key 走 env 或 `~/.pi/agent/auth.json` 回落读取，不落盘）

- `deepseek-v4-flash`：52 事件，thinking+text，stopReason=stop ✓
- `glm-5-3-flash`（修复后）：173 事件，thinking 441 字级别流式 ✓

## 本批新增决策与偏差（相对 pi）

1. **手写模型表先例**：volcengine-plan 模型不在 models.dev catalog，是 pig 第一个 `models_generated.go` 之外的 builtin 模型表。**砍掉扩展的启动期远程 registry fetch**（jsDelivr/GitHub 清单）——builtin 随二进制发布，方舟新模型走用户 models.json 逃生门。
2. **provider id = `volcengine-plan`**（不占 `volcengine`）：留空间给将来 catalog 的基础 endpoint volcengine，且 Coding Plan 计费路径显式化。
3. **溢出归一化落 `ai/utils/overflow.go`** 替代扩展的 message_end 钩子（auto-compaction 直接消费 IsContextOverflow）。pattern 收窄为 `OutofContextError` + `exceed max message tokens` 两条，**不照搬扩展的过宽 `exceed.*context`**。
4. **pig-only `ThinkingFormatVolcengine`**（重要，见下"坑"）：ark 对 9 个模型实测的 thinking 编码 = zai 式 `thinking:{type:enabled,clear_thinking:false}`，但与 zai 有两处不同：`reasoning_effort` 会被端点忽略且**在 GLM 上抑制 thinking**（实测 effort=high → 0 reasoning）；glm-5-3-flash / glm-5.3 / kimi-k2.7-code 对 `thinking.type:"disabled"` 直接 HTTP 400。因此：永不发 reasoning_effort（compat `SupportsReasoningEffort=false`）；off 时若模型不允许 disabled（ThinkingLevelMap off=nil）则整个省略 thinking 参数（hybrid 回落自带默认）。三个不认 disabled 的模型表内标 `off: nil`。
5. **未改 pi 的 zai 分支**：pi 的 zai 分支 off 时无条件发 `disabled`（其 catalog 的 glm-5.3 族虽标 off=nil 但 pi 仍发——疑似 pi 潜伏 bug，z.ai 端未验证）。为不改变任何 pi 既有模型行为，pig 走独立格式常量而非给 zai 分支加 `thinkingLevelOffAllowed` 守卫。**给人类的建议**：此发现值得在上游 pi 提 issue（zai glm-5.3 off → 疑似 400）。

## 批次定位

无后续——本批为独立插入批。回到 §4 计划：当前无未收口批次（Phase 6 T6 ✅、S1-S5 ✅、Phase 7/8 未开始）。下一批由人类指定（Phase 7 扩展系统，或剩余 adapter 按需启动）。

## 明确不要读的

- `codingagent/`（本批纯 ai 层，无消费面改动；agent-session 对新 provider 无感知）
- `docs/handoffs/T*`、`docs/batches/`（与本批无关）
