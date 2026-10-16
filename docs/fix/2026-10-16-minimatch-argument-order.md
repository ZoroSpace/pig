# 2026-10-16 Minimatch 参数顺序反置——设置模式过滤与 --model 通配全失效

- **日期**：2026-10-16 14:05
- **状态**：已修复收口（回归测试两处全绿）
- **涉及文件**：`codingagent/core/package_manager_paths.go`（`matchesAnyPattern` 六处调用改序）、`codingagent/core/model_resolver.go`（glob 分支两处调用改序）、`codingagent/core/package_manager_paths_test.go`（新增）、`codingagent/core/model_resolver_test.go`（新增 `TestResolveModelScopeMatchesGlobPatternsAgainstProviderQualifiedIDs`）

## 现象

移植 pi v1.2.0 `6a51a39d4`（设置项 `!~/`、`+/`、`-/` 模式先展开 `~`）时，为新增的 `~` 展开写了回归测试，测试红了——但根因不是 `~` 展开，而是**所有**模式匹配都不工作：`!` 排除、`+`/`-` 强制、glob 通配（`*?[`）、以及 `--model <pattern>` 的 glob 分支全部匹配不到任何东西。

## 根因

`codingagent/utils/minimatch.go` 的签名/实现/自测三处一致，都是 `Minimatch(pattern, text)`（第一参为 glob，与 npm `minimatch(path, pattern)` 的参数顺序相反，`minimatch_test.go` 锁定此约定）。但两个调用点的移植照抄了 pi 的 `minimatch(value, pattern)` 顺序：

- `package_manager_paths.go:matchesAnyPattern`：`utils.Minimatch(rel, normalizedPattern)` 等六处 → 实际把文件路径当 glob、把模式当被匹配文本，恒不匹配。
- `model_resolver.go:414`：`utils.Minimatch(fullID, globPattern)` → 同上。

pi 的 `resolvers`/`package-manager` 测试里的模式过滤用例（`describe("pattern filtering in top-level arrays")` 等）在 pig 从未移植，故该反置自 Phase 6 起一直潜伏、无测试暴露。

## 修复

把八处调用改为 `Minimatch(pattern, value)`：`matchesAnyPattern` 六处 → `utils.Minimatch(normalizedPattern, rel/name/filePathPosix/parentRel/parentName/parentDirPosix)`；`model_resolver.go` → `utils.Minimatch(globPattern, fullID/model.ID)`。`matchesAnyExactPattern` 走字符串相等比较、无 Minimatch，不受影响。

## 回归测试

- `codingagent/core/package_manager_paths_test.go`：`TestMatchesAnyPatternMatchesGlobAgainstFilePath`（相对 glob 命中 + 非命中对照）、`TestMatchesAnyPatternExpandsTildeInPattern`、`TestMatchesAnyExactPatternExpandsTildeInPattern`（后两者覆盖同批 `6a51a39d4` 的 `~` 展开）。
- `codingagent/core/model_resolver_test.go`：`TestResolveModelScopeMatchesGlobPatternsAgainstProviderQualifiedIDs`（`anthropic/claude-*` 命中 `claude-sonnet-4-5`，证明 glob 分支恢复）。

验证：改前上述新测试红（`expected tilde glob pattern to match`），改后 `go test ./codingagent/core/` 全绿。
