# CJK-WORD-SEG：编辑器中文词典切词（gse 混合对齐）

- **日期**：2026-09-12（实施完成于 20:30 前后，见文件 mtime）
- **状态**：✅ 已交付（用户插入批；用户选定 gse 方案并给出「单实例 + 锁」建议）
- **涉及文件**：`tui/cjk_segmenter.go`（新增）、`tui/assets/dict/{zh_s,zh_t}.txt` + `SOURCE.md`（新增，vendor 自 gse）、`tui/word_navigation.go`、`tui/editor.go`、`tui/word_navigation_test.go`、`tui/editor_test.go`、`tui/input_test.go`、`tui/cjk_segmenter_test.go`（新增）、`go.mod`/`go.sum`（+`go-ego/gse`）

## 背景与规格发现

pi 的 `Intl.Segmenter` word 粒度自带 CJ 词典（`你好世界` → `[你好][世界]`）；Go 侧 UAX #29（rivo/uniseg）无词典，Han 逐字切，导致编辑器 Ctrl+←/→ 与词删除在中文里逐字移动。原移植批把它登记为 AGENTS.md Phase 6 已知缺口，留了 `WordNavigationOptions.Segment` 注入 seam。

**规格澄清（本批实证）**：pi 的 `word-navigation.test.ts` CJK 用例注释写「Intl.Segmenter treats each CJK char as a separate word-like segment」，但其数值 `5→2→0`（UTF-16）实际证明的是词典分组——5→2 跳过整段 `世界`。**注释错、数值对**。pig 初版移植按「逐字」适配了这三处期望，本批全部恢复 pi 精确值。

## 方案：混合对齐，不是整体替换

**绝不能用 gse 整体替换 `wordSegmentData`**：gse 的分词器会**把 ASCII 小写化**（探针实证 `English` → `english`），且对 `_`、路径、标点的切法与 ICU/UAX #29 不同——整体替换会破坏全部英文/代码行为与既有 2100 行移植测试。

实际实现（`mergeHanRunsWith`）：

1. UAX #29 打底（`uaxWordSegmentData`，原实现更名）；
2. 仅把「连续 ≥2 个纯 Han 段」的 run 拼接后喂给 gse `Cut`，得到词典分组；
3. 输出经 **tiling 校验**（token 串联必须逐字节还原 run）才采纳，否则回退原段——防御 gse 任何输出变换（如 ASCII 小写化）漏进导航代码；
4. 其余段（拉丁/数字/标点/空白/单个 Han 字）原样通过，边界与 UAX #29 逐字节一致。

IsWordLike 沿用 `segmentIsWordLike`（Han 属字母类 → true）。

## 异步初始化（用户核心关切：不卡按键）

gse 词典解析 ~15MB 文本耗时 **~0.9s**（zh_s 单独 ~0.5s），同步首用 = 按键卡顿，故：

1. **预热触发**：① `Editor.HandleInput` 首个 Han 键入（`containsHanRune` 对 ASCII 转义序列几个字节即拒绝）；② `mergeHanRunsWith` 遇到首个 Han run 时兜底触发（覆盖 SetText 等路径）。二者幂等（`sync.Once`）。
2. **就绪前**：`wordSegmentData` 走原子指针检查，未就绪直接返回 UAX #29 逐字结果（即原行为），**零阻塞**。用户从打字到第一次 Ctrl+跳词的时间远大于加载窗口。
3. **发布**：加载完成经 `atomic.Pointer[gse.Segmenter]` 原子发布；失败置 `loadFailed`（永久回退，goroutine 内 recover + `utils.LogPanic`，遵循 panic-resilience 约定）。
4. **并发**：单实例 + `sync.Mutex` 串行 `Cut`（实测 ~1.5µs/次，按键速率下锁竞争为零）——即用户建议的「一个实例 + 一把锁」。
5. **纯英文会话零付费**：无 Han run 不触发加载、不碰词典。

## 内存与体积实测（探针 + 实构建）

| 指标 | 数值 | 备注 |
|---|---|---|
| 稳态 RSS | **+15MB**（zh_s+zh_t）/ +10MB（zh_s 单独） | `debug.FreeOSMemory` 后 `/proc/self/status VmRSS`；与用户当初 5-10MB 估计同量级 |
| 加载瞬间峰值 | ~286MB（zh）/ ~202MB（zh_s） | 解析期临时垃圾（千万级碎片串），加载完回落；VmHWM 不回退但无实感 |
| 加载耗时 | ~0.91s（zh）/ ~0.53s（zh_s） | 异步化的决定性因素 |
| Cut 延迟 | ~1.5µs/次 | 1000 次典型行长 1.5ms |
| 二进制 | 基线 25.7MB → **34.5MB**（+8.8MB） | 见下节 vendor 方案；直接用 gse `NewEmbed` 会到 57.1MB |

**坑：gse 包级 `go:embed` 连坐**。gse 包内还 embed 了 22.6MB 日文词典与 5.9MB IDF 数据，Go 链接器**不会剔除**未引用的 embed 变量——直接 `gse.NewEmbed("zh")` 二进制 25.7→57.1MB（+31.4MB，其中 ~28MB 是死重）。处置：把 `zh_s.txt`/`zh_t.txt`（8.3MB，未修改）vendor 进 `tui/assets/dict/`（`SOURCE.md` 注明 Apache-2.0 来源），自 `//go:embed` + `gse.Segmenter.LoadDictStr(zhS+zhT)` 加载——与 gse 内部 `loadZh()` 完全同路径，行为逐字节等价。

## 行为偏差声明（vs pi/ICU）

1. **日文 kana 仍逐字切**（UAX #29）——gse zh 词典不覆盖 kana，ICU 连日文也是词典分词。留待后续（seam 在，换个词典源即可）。
2. **分组粒度偶尔与 ICU 不同**：gse 按其词典词频，如 `今天天气` 为一个 token（ICU 倾向 `今天/天气`）。目标是词级手感，不追求 token 逐一相同；测试按 gse v1.0.2 词典 pin 值并在文件头声明。

## 测试

- **恢复 pi 精确期望**（原为 UAX #29 逐字适配，现与 pi 数值一致）：
  - `word_navigation_test.go` backward CJK：13→**6**→**0**（原 13→9→3）
  - `editor_test.go` #4972 全角标点：15→9→6→0→6→9→**15**（pi cols 5→3→2→0→2→3→5）；混排 CJK+ASCII：backward 25→19→14→11→**5**→0、forward 5→11→14→19→**25**（pi UTF-16 序列逐点对应）
  - `input_test.go` Ctrl+W / Alt+D Unicode 词边界：各 10 次逐字删除 → **6 次整词删除**（pi 原值）
- **新增**（`cjk_segmenter_test.go`，5 函数）：词典精确分组（含 Latin 不受污染/全角标点独立/生僻字回退/tiling+Index 连续性）、未加载与加载失败回退、backward/forward 词典化 walk、恶意混合输入 tiling 性质、`containsHanRune`。

## 验收

- `go build && go vet && gofmt -l` 干净；全仓 28 包 `go test ./... -race -count=1 -timeout 600s` **rc=0**（1346 测试函数）。
- 过程事故（非代码 bug）：量基线二进制时 `git stash && go build && git stash pop` 因 build 失败断链未 pop，改动滞留 stash 被误判丢失；`git stash pop` 恢复，无损失。教训：stash 链式命令不要把破坏性步骤放 `&&` 中段。
