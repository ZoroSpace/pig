# 2026-09-11 工具调用标题流式期间显示 [invalid arg]（write/edit/read/ls）

- **日期**：2026-09-11 13:11 CST（取自修复落盘的文件 mtime）
- **状态**：✅ 已修复，回归测试锁定
- **涉及文件**：
  - `codingagent/core/tools/render_utils.go`（修复 `rawPathFromArgs`：str() 语义对齐）
  - `codingagent/core/tools/render_path_args_test.go`（新增，1 个测试函数 / 9 子用例 + 3 渲染级断言）
- **触发场景**：dogfooding 中观察 agent 调用 write/edit 工具时，调用行标题流式期间显示红色 `[invalid arg]`，工具执行成功后变为真实文件名。pi 同场景显示 `...`（path 到达即变文件名）。
- **根因定位协作**：用户并行让 pi 会话排查，pi 定位到 `str()` 语义偏差；pig 侧验证后按此修复。

## 现象

流式调用 write/edit（以及 read/ls）时，TUI 调用行标题闪现或滞留 `[invalid arg]`；工具照常执行成功，args 完整后标题才变成文件名。pi 在同链路显示 `...`。

## 根因：`str()` 三分语义被压扁成两分

pi 各 renderer（`renderers/{write,edit,read,ls}.ts`）取路径参数的统一表达式是：

```ts
const rawPath = str(renderArgs?.file_path ?? renderArgs?.path);
```

`str()`（`render-utils.ts:25`）是**三分**：

```ts
if (typeof value === "string") return value;
if (value == null) return "";   // undefined（键缺失）或 null → "" → renderToolPath 走 emptyFallback（"..."）
return null;                     // 存在但非字符串 → null → "[invalid arg]"
```

流式早期 partial args 还没有 `path`/`file_path` 键（或 args 整体还是 undefined）时，pi 渲染 `...`，**只有「键存在但值非字符串」才是 invalid**。

pig 的 `rawPathFromArgs`（四个 renderer 共用的 helper，对应上面表达式）把三处都返回了 nil（→ `[invalid arg]`）：① args 非 map/nil；② 键缺失；③ 值非 string 非 nil。①②本是流式常态，于是标题在 path 到达前一直显示 invalid。

pig 的 provider/agent 链路本身没问题是本次排查的排除项：`openai_completions_stream.go` 每个 toolcall delta 都 `ParseStreamingJSON` 更新 `block.content.Arguments`（对齐 pi `openai-completions.ts:455` 的 `parseStreamingJson` 每 delta 解析），`agent_loop`/`interactive_mode` 的 message_update → pendingTools.UpdateArgs 链路完整——只是渲染层把「还没到」当成了「无效」。

## 修复

`rawPathFromArgs` 对齐 `str()` 三分语义（指针形态承载区分）：

| 输入 | pi `str()` | pig 修后 |
|---|---|---|
| args 非 map / nil（流式最早） | `""` | 空串指针 → `...` |
| 无 `path`/`file_path` 键 | `""` | 空串指针 → `...` |
| 值为 null | `""` | 空串指针 → `...` |
| 值为 string | 值 | 值指针 |
| 值非 string 非 null | `null` | **nil → `[invalid arg]`**（保留） |

`file_path ?? path` 的 `??`（跳过 null/undefined）语义不变；edit/write/read 的 emptyFallback（""→`...`）与 ls 的（"."）各调用点原已与 pi 一致，无需动。

## 回归测试锁定

`render_path_args_test.go`：`TestRawPathFromArgsStrParity`（9 子用例覆盖上表全部输入 + `??` 回落语义）+ 3 个渲染级断言（nil args / 空 args 渲染不出 `[invalid arg]`，非 string path 仍渲染 `[invalid arg]`）。stash 验证：修复前 4 个子用例 FAIL（nil args / non-map / empty object / missing key），修复后全 PASS。

## 复现与验证

```bash
go test ./codingagent/core/tools/ -run 'TestRawPathFromArgs' -count=1 -v
go build ./... && go vet ./... && gofmt -l . && go test ./... -race -count=1
```

（交互侧肉眼验证：流式调 write/edit，标题应从 `...` 变文件名，不再闪 `[invalid arg]`。）
