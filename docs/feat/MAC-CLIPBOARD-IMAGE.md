# MAC-CLIPBOARD-IMAGE：macOS Ctrl+V 剪贴板贴图（用户插入小批，2026-09-16）

- **日期**：2026-09-16 21:30
- **状态**：✅ 完成。utils 包新增 6 测试函数 -race 全绿 + 全仓 `-race` 回归通过；真机探针验证（生成的测试 PNG 经真实剪贴板往返，字节级一致）
- **动机**：dogfooding 排查用户「Ctrl+V 贴图贴不上」——根因两层：① SSH 场景是原理性限制（本地剪贴板不经 SSH 传输，OSC 52 只回文本 flavor，所有 CLI agent 同限，pig 不追）；② **本地 macOS 场景是真实缺口**：`ReadClipboardImage` 非 Linux 路径恒 nil（pi 在此走 `@mariozechner/clipboard` native addon，无 Go 等价物，原文件头声明为偏差），Ctrl+V 静默回落贴文本。用户拍板先修 mac 本地版；SSH 静默失败的提示 UX 另批（顺带议定 OSC 52 读文本兜底不做——终端粘贴键已覆盖文本场景，性价比为负）
- **涉及文件**：`codingagent/utils/clipboard_image.go`（darwin 路径 + 测试 seam）、`codingagent/utils/clipboard_image_test.go`（新建）

## 实现

pi 的非 Linux 读图路径 = native addon（`clipboard.hasImage`/`getImageBinary`，返回 `image/png`）。pig 以外部工具替代（与 Linux 路同一精神，零依赖）：

1. **`readClipboardImageViaOsascript`（主路）**：`osascript -e 'the clipboard as «class PNGf»'`——AppleScript 把 PNGf flavor 渲染为二进制字面量 `«data PNGf<hex>»`，`decodeOsascriptPngData` 剥壳 + hex decode 得 PNG 字节。macOS 恒有 osascript、图像复制场景恒有 PNGf flavor（clipboard info 实证：macOS 还自动生成 AVIF/8BPS/GIF/JPEG 替身）。专设 10s 超时（`clipboardDefaultOsascriptTimeoutMs`）——hex dump 是 2 倍体积，多 MB 截图下 3s 默认读超时太紧。
2. **`readClipboardImageViaPngpaste`（兜底）**：`pngpaste -`（brew 装可选）——覆盖 **TIFF-only 剪贴板**（少数 app 只放 TIFF flavor，PNGf 路看不到，pngpaste 能做 TIFF→PNG 转换）；未安装时失败回落 nil（声明边界）。
3. **接线**：`ReadClipboardImage` 的 `else if platform == "darwin"` 分支，osascript → pngpaste → nil。产出 mime 恒 `image/png`（与 pi native addon 语义一致）。Windows 仍为 native-addon-only 缺口（声明保留）。
4. **测试 seam**：`clipboardRunCommandFn` 包级 var 间接层（pig 惯例，同 footer-data-provider 的 child_process seam）。UI 层零改动——`handleClipboardPaste` 落盘临时文件 + 路径插入编辑器是现成的（T5b-2）。

## 边界（不做/等后续）

- **TIFF-only 且未装 pngpaste**：该 app 的图贴不了（osascript PNGf 失败 → pngpaste 缺席 → nil），声明边界。
- **macOS Sequoia 剪贴板权限弹窗**：首次读取可能弹一次「允许访问剪贴板」（归因终端 app），一次性。
- **photon 半区不碰**：贴出的 PNG 直接进会话；冷门格式转 PNG / 超大图缩放仍等 Phase 8（与 Linux 现状一致）。
- **SSH/tmux**：图像原理性无解（见上）；文本走终端粘贴键（bracketed paste，已支持）。

## 回归测试（`clipboard_image_test.go`，全部 seam 打桩不真起进程，linux CI 同样可跑）

`TestDecodeOsascriptPngData`（8 子测：valid / 尾随换行 / 缺前缀 / 缺后缀 / 奇数 hex / 空 payload / 坏 hex / 空 stdout）+ `TestReadClipboardImageDarwinOsascriptPath`（主路 + pngpaste 不被调用 + 超时值）+ `TestReadClipboardImageDarwinPngpasteFallback`（回落顺序 + `[-]` 参数）+ `TestReadClipboardImageDarwinUnavailable`（双双失败 → nil）+ `TestReadClipboardImagePngpasteEmptyOutput`（exit 0 空输出 → nil）+ `TestReadClipboardImageTermuxShortCircuit`（TERMUX 短路先于平台分发）。

**stash 验证**：`git stash push -- codingagent/utils/clipboard_image.go` 后测试 build failed（测试引用的 seam/解码函数不存在）= 修复前红；pop 后全绿。

**真机探针**（临时测试文件，已删）：Go 生成 40×30 PNG → `osascript set the clipboard to «data PNGf…»` 写入真实剪贴板 → `ReadClipboardImage(nil)` 读回 → 落盘文件与源 PNG `cmp` 字节级一致（探针曾因用户剪贴板当时装的是文本而正确返回 nil，顺带验证了空图分支的真机行为）；用户剪贴板已备份还原。

## 过程坑

- 测试桩的 Go 闭包陷阱：`stubClipboardRunCommand` 初版返回 slice 值——闭包内 `append` 重分配后测试持有的是旧 header，调用记录恒空（且镜像断言通过、看起来"桩没被调却返回了样例字节"，极具迷惑性）。改为返回 `*[]clipboardCommandCall` 指针，断言时解引用。
