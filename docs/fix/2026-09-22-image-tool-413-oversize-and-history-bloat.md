# 2026-09-22 image 工具 413 超大请求与会话历史无界膨胀

- **日期**：2026-09-22 17:16
- **状态**：已修复（回归测试锁定，stash 红→绿验证）
- **涉及文件**：`codingagent/utils/image_process.go`、`agent/harness/image_elision.go`（新增）、`agent/harness/messages.go`、`codingagent/utils/image_process_test.go`（新增）、`codingagent/utils/tool_result_images_test.go`、`agent/harness/image_elision_test.go`（新增）、`codingagent/core/tools/tools_test.go`、`go.mod`/`go.sum`（+`golang.org/x/image` v0.46.0，连带 x/sys/x/text 补丁级升级）

## 现象

用户在图片密集型工作流（漫剧工作量拆解，手机翻拍屏幕照片逐张转录）中：

1. `read` 读取 3.1MB JPEG 后，下一次 LLM 请求被网关拒绝：`Error: 413 Request Entity Too Large`（openresty 页面，`api.deepseek.com` 边缘）。
2. 单进程内存峰值高达 ~800MB（`ps`/监控观察值）。
3. 会话 JSONL 膨胀到 84MB（`~/.pig/agent/sessions/--Users-lee-Desktop-Job--/2026-09-22T06-56-58…jsonl`，15 个 image block、70.7MB 纯 base64），且此后**该会话每发必 413**。

单图基线（修复前实测）：无图 41MB、单图 92MB、双图 156MB peak RSS——单图本身正常，问题全部来自**累积**。

## 根因（三层叠加）

1. **resize 管线是 Phase 8 占位 stub**（`codingagent/utils/image_process.go`）：`resizeImage` 不缩放不重编码、原样 base64 透传（声明偏差 "until the wazero runtime lands"），`convertImageBytesToPng` 恒失败（BMP 读取一律 "[Image omitted]"）。`AutoResizeImages=true` 是空操作。2736×3648 的 3.1MB 手机照 → 4.3MB base64 进入历史。真实 pi 走 photon 会缩到 2000px 内（数百 KB）。
2. **tool result 图片只进不出**（pi 1:1 行为）：图片 block 永久留在会话历史，之后**每一次** LLM 请求全量重发。N 张图 = N×4.3MB 请求体；15 张 ≈ 70MB body → 413。内存端：70MB 常驻 + 每轮全量 JSON 序列化/请求体副本 × GC 放大 → 800MB 量级。
3. **compaction 兜底失效**：`ai/utils/estimate.go` 的 `estimatedImageChars = 4800`（≈1200 token/图）不反映 base64 实际体积，15 张图估算 1.8 万 token，远低于 1M 窗口阈值，压缩永不触发。

413 不在 `retryableProviderErrorPattern`（`ai/utils/retry.go`）中，fail-fast 不重试——重试风暴不存在，但历史已带 70MB 图片的会话从此不可用。

## 修复

1. **resize 管线 Go 原生实现**（`image_process.go`，退役 photon 占位）：
   - 解码：stdlib jpeg/png/gif + `golang.org/x/image` bmp/webp/tiff（magic 嗅探，非声明 mime）；动画 GIF（多帧）透传不压扁。
   - `resizeImage`：仅缩小、fit 2000×2000 默认（pi 语义）；在限内字节级透传不重编码；重编码 jpeg q80、png/gif 同格式；webp 无 Go 编码器 → 缩后转 png（声明偏差）。解码失败透传（比 pi 的 null→omit 更保守：read 嗅探认定的文件对模型在手总比静默丢弃有用）。显式 `MaxEncodedBytes` 预算不可达时维持 pi 契约返回 null → "[Image omitted: could not be resized …]"（jpeg 一次 q55 重试）。
   - `convertImageBytesToPng` 真实现：BMP/WebP/TIFF/静态 GIF → PNG（`normalizeImage` 随之打通，BMP 读取从 "omitted" 变为转换成功）。
   - **采样器选 ApproxBiLinear 而非 CatmullRom**（实测）：CatmullRom 大倍率缩小的 distributor 临时缓冲在 10MP 照片上产生 168MB 分配churn，ApproxBiLinear 仅 1MB，编码尺寸差异 302KB vs 247KB——内存目标优先。
2. **tool-result 图片 elision**（pig 新增行为，`agent/harness/image_elision.go`，挂 `ConvertToLlm` 出口单收口）：最后一条 assistant 消息**之前**的 toolResult 图片 block 替换为 `[Image elided from context …use the read tool on the same file…]` 文本。每张图恰好存活到模型对它产出回复为止（之后可随时重读文件）；新 toolResult、用户消息图片（剪贴板粘贴不可从盘重读）永不 elide；copy-on-write 不改输入切片；会话文件/transcript/export 不受影响。单点覆盖 agent loop、compaction、branch summary、bug report 全部 ConvertToLlm 调用面。value/指针两种消息形态都处理（对齐 sdk.go blockImages 过滤族）。
3. estimate 低估（第 3 层）本批不改：resize 后图片 ~300-400KB，加 elision 后历史请求体有界，估算误差不再构成实际风险；留作后续观察项。

## 验证

- **回归测试（stash 红→绿）**：
  - stash `codingagent/utils/image_process.go`（回到 stub）→ `TestNormalizeToolResultImagesResizesOversizedImagesWithDimensionNote`、`TestProcessImageResizesOversizedJpegWithDimensionNote`、`TestProcessImageConvertsBmpToPng`、`TestProcessImageOmitsWhenMaxEncodedBytesBudgetUnreachable` FAIL；恢复后 PASS（所在文件：`codingagent/utils/tool_result_images_test.go`、`codingagent/utils/image_process_test.go`）。
  - stash `agent/harness/messages.go`（摘除 elision 调用）→ `TestConvertToLlmElidesImagesTheModelAlreadyRespondedTo`、`TestConvertToLlmElisionDoesNotMutateInput` FAIL；恢复后 PASS（`agent/harness/image_elision_test.go`）。
- 占位测试退役：`tool_result_images_test.go` 的 "LeavesOversizedImagesAloneUntilPhotonArrives" 改为 pi 对齐断言（resize+维度注记）；`tools_test.go` 的 BMP "photon-unavailable path" 用例改为断言转换成功（`[Image converted from image/bmp to image/png.]` + PNG 附件）。
- 全仓 `go test ./... -race -count=1` 全绿。
- 真机冒烟（deepseek-flash，`-p --no-session`）：单图 3.1MB JPEG 成功描述（修复前必 413 的量级之上）；peak RSS 单图 92→52-80MB、双图 156→92MB；重编码后单图编码体 ~300KB 级。双图请求曾遇一次上游 >300s 停滞，重试 24s 正常返回（与本次改动无关的偶发上游延迟）。

## 后续（未排期）

- `codingagent/utils/clipboard_image.go` 的 photon 残留（剪贴板不常见格式 → PNG）可直接复用本次 `convertImageBytesToPng` 接通。
- P8 余量收窄后剩：terminal-image 检测 + tui Image 组件、telemetry 上报面、goreleaser、/share gist（见 `docs/handoffs/P8-EH1.md` 增补）。
- `ai/utils/estimate.go` 图片 token 估算若未来取消 elision 或恢复大图透传，需同步修正。
