# fix：`TestPathsCanonicalize` 在 macOS 恒失败 —— 测试期望值写法偏离 pi（非环境问题，非实现 bug）

- **日期**：2026-09-16 22:40
- **状态**：已修复收口（此前被误诊为「存量环境问题」，本批更正诊断并修复）
- **涉及文件**：`codingagent/core/tools/path_utils_test.go`（仅测试；`codingagent/utils/paths.go` 的 `CanonicalizePath` 实现不动）

## 现象

全仓 `-race` 回归中 `codingagent/core/tools` 的 `TestPathsCanonicalize` 两个子测试在 macOS 恒失败：

```
--- FAIL: TestPathsCanonicalize/returns_the_real_path_for_a_regular_file
    path_utils_test.go:151: got "/private/var/folders/.../coding-agent-test-.../file.txt"
--- FAIL: TestPathsCanonicalize/resolves_symlinks_to_their_targets
    path_utils_test.go:163: got "/private/var/folders/.../coding-agent-test-.../target.txt"
```

2026-09-14（CJK-SEG-MODE 批）曾登记为「存量 macOS /private 前缀失败，stash 验证与本批无关」——当时只排除了与本批的关联，未深挖根因。

## 根因：实现忠实 pi，测试期望值写法偏离 pi

macOS 上 `/var` 是指向 `/private/var` 的符号链接（`/tmp` → `/private/tmp` 同理），`os.TempDir()`（`$TMPDIR`，形如 `/var/folders/...`）经 realpath 解析后必变为 `/private/var/folders/...`。

- **pi 的断言**（`test/paths.test.ts:34`）：

  ```ts
  expect(canonicalizePath(file)).toBe(realpathSync(file));
  ```

  **期望值本身也过了 realpath** —— 两边自洽，macOS 上恒过（成功路径下 LHS 与 RHS 是同一函数同一输入，恒等；测试的真正牙齿在 symlink case 与 fallback case）。

- **pig 的移植**（修复前）：

  ```go
  if got := utils.CanonicalizePath(file); got != file { ... }
  ```

  期望写成了**原始拼接路径**（`/var/folders/.../file.txt`），丢了「期望值也要 realpath」这一层；实现忠实返回 `/private/...` → 断言失败。Linux 上 `/tmp` 不是符号链接，两种写法等价，移植期未暴露。

实现侧核对：pig `utils.CanonicalizePath` = `filepath.EvalSymlinks` 成功即返回、失败回原路径，与 pi `canonicalizePath`（`realpathSync` + catch 回落）逐语义一致，**无移植偏差**。

## 修复

仅改测试（`path_utils_test.go`）：

1. 两条失败断言改回 pi 的自洽写法：期望值经 `filepath.EvalSymlinks`（Go 版 realpathSync，包内 `realpath` helper）；
2. 补齐 pi 同 describe 下漏移植的 2 个 case：`resolves directory symlinks`、`falls back to the raw path for a dangling symlink`（后者锁定「realpath 失败回原路径」语义，pi 注释原样带上）；
3. 测试头注释写明期望值过 realpath 的原因与本文档链接，防止回退。

## 回归测试

`TestPathsCanonicalize` 自身即回归锁定：修复前每次全量必 FAIL（红证据：`/tmp/pig-test-out.log` 2026-09-16 全量日志 + AGENTS.md 2026-09-14/09-16 多处登记）；修复后 6 子测试全绿。此前「红」已在多批全量回归中反复复现，无需另行注回验证。

## 教训

移植 pi 测试时，**断言的期望值表达式也是移植内容**——pi 用 `realpathSync(file)` 而非 `file` 做期望是有意的环境无关写法（作者在 macOS 开发），简化成字面量就把平台差异焊死进了测试。
