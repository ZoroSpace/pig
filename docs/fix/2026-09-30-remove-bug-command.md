# 彻底删除 `/bug` 命令与 pi 的 bug-report 上报链（pig-only 行为偏差定论）

- **日期**: 2026-09-30
- **状态**: 已完成（删除 + 文案改向 + 回归锁定）
- **性质**: **有意与 pi 分叉**（pig-only 偏差），后续 pi 基线同步批遇到该面一律跳过，不再重新判断

## 决策与理由

pig 此前 1:1 移植了 pi 的 `/bug`：交互式采集 bug 描述 → 可选附带会话 transcript 或让模型写摘要 → 上传 Pi 官方 Radius gateway（`https://radius.pi.dev/v1/bug-reports`）或导出 `pi-bug-report-<id>.zip`。

用户决策（2026-09-30）：**pig 整体移除 `/bug` 命令及其全部支撑链。** `/bug` 是 pi 面向其用户的 bug 上报通道（采集现场并上传到 Pi 官方 gateway），对 pig 没有意义——pig 的问题反馈走自己的 GitHub issue 跟踪，打包上传到 pi 的上报渠道既无用也不合适。

因此：

- pig 不再提供 `/bug`；autocomplete（由 `BuiltinSlashCommands` 驱动）自动不再提示；
- `/bug ...` 文本按 pi 的未知命令语义**作为普通消息提交给模型**（与 `/foo bar` 同路，`isDeferredInteractiveCommand` 不收编，仍只含 `/share`）；
- 崩溃/异常提示改指 **pig 的 GitHub issue 跟踪**（新常量 `codingagent.IssuesURL = "https://github.com/ZoroSpace/pig/issues"`）+ 本机崩溃记录路径（新导出访问器 `core.CrashLogPath()`，即 `~/.pig/agent/crashes.json`）；
- 历史会话读兼容保持：旧版 `/bug` 写入的 `{"type":"custom","customType":"pi.bug-report"}` 条目走泛型 custom 分支照常可读（新增测试钉住，防止将来被当未知类型清理）。

## 删除面（`git rm`，共 7 文件 1828 行）

| 文件 | 行数 | 说明 |
|---|---|---|
| `codingagent/core/bug_report.go` | 778 | 采集/脱敏/打包/摘要 prompt，唯一消费者是 /bug |
| `codingagent/core/bug_report_upload.go` | 98 | 上传 Radius gateway |
| `codingagent/core/bug_report_test.go` | 62 | `TestBugReportRedactURL`/`TestBugReportRedactJsonValue` 随实现删除（脱敏逻辑无剩余消费者） |
| `codingagent/modes/interactive/bug_report.go` | 435 | /bug consent 对话框流程 |
| `codingagent/modes/interactive/interactive_mode_bug_report_test.go` | 299 | /bug 多行描述 + offline 拦截测试 |
| `codingagent/core/radius.go` | 47 | `RADIUS_PROVIDER_ID`/`GetRadiusGatewayUrl`/`ENV_RADIUS_GATEWAY`（radius provider 本身 pig 未移植，Phase 2 已声明延后且现在永不需要） |
| `codingagent/utils/zip.go` | 109 | `ZipEntry`/`WriteZipArchive`，全仓唯一消费者是 bug report 打包（pi 侧 zip 还给包管理器用，但 Phase 7 扩展系统已定不做） |

`go.mod`/`go.sum` 零变化（被删文件只用既有依赖与 stdlib，`git diff go.mod go.sum` 为空）。

## 保留面（不要顺手删）

- **crash-log 子系统**：`codingagent/core/crash_log.go` + `crash_log_test.go`、`TakeUnnotifiedCrash`、`RecordCrash`、`FindExtensionStackMatches`、`FormatCrashExtensionHint`、`getCrashExtensionHint`、`recordCrash` —— 本机崩溃留档 + 扩展归因，与 /bug 解耦后仍有独立价值（提 issue 时可直接附 `~/.pig/agent/crashes.json`）；文件头注释已改为 pig 语义。
- `codingagent/core/session_export.go` 的 `SerializeSessionBranch` 与 `modes/interactive/session_share.go`（`/share` 共用）。
- `codingagent/utils/panic_log.go`（PANIC-RESILIENCE 的 panic 落盘，与 crash-log 无关）。
- interactive_mode.go 中 pi 名函数/字段**保留原名**便于 grep 对照：`maybeSuggestBugReport`/`suggestBugReport`/`bugReportCancelPattern`/`bugReportHintShown`，只改注释与文案。

## 接线点改动

1. `codingagent/core/slash_commands.go`：删除 `{Name: "bug", ...}` 条目，原位留缺失注释（含 fix 文档指针）。
2. `codingagent/modes/interactive/interactive_mode.go`：
   - 删除 `case text == "/bug" || strings.HasPrefix(text, "/bug ")` 提交分支，原位留同一条缺失注释（`/bug hello` 落普通消息，pi 未知命令语义）；
   - 启动崩溃提示（`TakeUnnotifiedCrash` 消费处）文案改写（见下）；
   - `crashReportInstructions()`/`suggestBugReport()` 文案改写（见下）；`maybeSuggestBugReport`/`bugReportCancelPattern`/`bugReportHintShown` 保留原名，仅注释去掉 `/bug` 描述。
3. `codingagent/core/agent_session.go`：删除 `SummarizeForBugReport` 与 `BugReportSummaryOptions`（唯一消费者是 /bug），原位留缺失注释；`getSummarizationRequestAuth`/`retryPolicyFromSettings` 有 compaction/branch-summary 等其余消费者，保留。
4. `codingagent/core/crash_log.go`：文件头注释改 pig 语义；新增 pig-only 导出访问器 `CrashLogPath()`（注释写明 pi 的唯一消费者曾是 /bug bundle，现在由崩溃提示消费）。
5. `codingagent/config.go`：新增 pig-only 常量 `IssuesURL`（注释写明来源与用途）。

## 用户可见文案（原文 → 新文）

1. `crashReportInstructions()`：
   - 原：`To report this crash: <resume> run /bug. The crash details are attached automatically.`
   - 新：`To report this crash: <resume> open an issue at https://github.com/ZoroSpace/pig/issues. The crash record is saved in ~/.pig/agent/crashes.json.`
   - `<resume>` 逻辑不变：有会话文件 → ``run `pig -r` to resume the session, then``；无 → `start pig and`。
2. 启动崩溃提示：
   - 原：`pig crashed on <time> (<message>). Run /bug to report it; the crash details are attached automatically.`
   - 新：`pig crashed on <time> (<message>). A crash record is saved in <crashLogPath>; please open an issue at <IssuesURL>.`
3. `suggestBugReport()`：
   - 原：`If this looks like a pig bug, /bug sends a report to the developers.`
   - 新：`If this looks like a pig bug, please open an issue at https://github.com/ZoroSpace/pig/issues.`

三处拼接一律用 `codingagent.IssuesURL` 与 `core.CrashLogPath()`，不散落字面量。

## 回归测试（先写测试 → 验证红 → 再删实现 → 验证绿）

| 测试函数 | 所在文件 | 锁定内容 |
|---|---|---|
| `TestBuiltinSlashCommandsExcludeBugReportCommand` | `codingagent/core/agent_session_prework_test.go` | `BuiltinSlashCommands` 中不存在 `Name == "bug"` 条目 |
| `TestClipboardPathSubmissionReachesModel` 新增表行 `{"removed /bug command is ordinary text", "/bug hello"}` | `codingagent/modes/interactive/interactive_mode_submit_test.go` | `/bug hello` 与 `/foo bar` 同路：原文进入提交链、不弹 Warning |
| `TestMaybeSuggestBugReport` 新增子测 `"hints point at the issue tracker, not the removed command"` | `codingagent/modes/interactive/interactive_mode_bug_hint_test.go` | `suggestBugReport()` 渲染与 `crashReportInstructions()` 均含 `codingagent.IssuesURL`（后者还含 `core.CrashLogPath()`）且不含 `/bug` |
| `TestSessionManagerReadsLegacyBugReportEntries`（pig-only） | `codingagent/core/session_manager_bug_report_compat_test.go` | 含 `pi.bug-report` custom 条目的 v3 JSONL 仍可 `OpenSession` 并枚举（泛型解码天然通过，用测试钉住防误清理） |

**stash 红→绿证据**（测试先落盘、实现未删时跑）：

```
--- FAIL: TestBuiltinSlashCommandsExcludeBugReportCommand (0.00s)
    agent_session_prework_test.go:135: /bug is removed in pig but still registered: {Name:bug Description:Report a bug to the Pi developers ArgumentHint:<description>}

--- FAIL: TestMaybeSuggestBugReport (0.01s)
        interactive_mode_bug_hint_test.go:93: bug hint missing issue tracker URL:

--- FAIL: TestClipboardPathSubmissionReachesModel (0.02s)
    （/bug hello 用例 panic 于 bug_report.go:355 ← interactive_mode.go 提交分支 ← handleBugCommand
      —— 旧 /bug 分支拦截文本进入对话框流程，正是被删除的拦截行为，测试 FAIL 形态为 panic 非断言）
```

删除实现后同批测试全 PASS（两包 `ok`）；`TestSessionManagerReadsLegacyBugReportEntries` 落盘时即 PASS（预期天然通过，属 pin 非红测）。

## 验收

- `go build ./... && go vet ./... && gofmt -l .` 零输出；
- 窄范围 `go test ./codingagent/... -count=1` rc=0；
- 全量 `go test ./... -race -count=1 -timeout 600s` **只跑一次**，rc=0、30 包全 ok（日志 `/tmp/pig-test-out-remove-bug.log`）。

## 冒烟（tmux 真机，已按规则清理）

专属会话 `pig-rmbug-x7`（`PIG_CODING_AGENT_DIR=/tmp/pig-rmbug/agent` + 本地 mock OpenAI 端点 :18557，临时目录已删）：

1. 输入 `/bu`：autocomplete **无任何提示**（删除前应提示 `bug`）；
2. 对照 `/c`：正常弹 8 项补全（copy/changelog/clone/compact…），证明 autocomplete 本身无恙且列表中无 bug；
3. 输入 `/bug hello` + Enter：**作为普通用户消息进入提交链**——transcript 显示原文并发起真实 HTTP 请求（mock 未起时报 connection refused，恰好证明请求发往了模型端点而非对话框）；补起 mock 后同会话普通消息正常收到 `pong` 回复。

结束 `tmux kill-session -t pig-rmbug-x7`，未动 kill-server。

## 声明

**pi 的 bug-report 上报链（Radius 上传 / zip 打包 / `SummarizeForBugReport` / crash-log 的 /bug 指针）pig 不跟进；后续 pi 上游对 /bug / bug-report 面的任何改动**永不迁移**（用户 2026-09-30 追加确认），基线同步批遇到该面（含 pi 对 `bug-report.ts`/`bug-report-upload.ts`/`crash-log.ts` 中 `/bug` 相关的任何上游改动）一律跳过，不再重新判断，也不得把 /bug 相关能力重新加回 pig。** `docs/feat/SLASH-COMMANDS-COMPLETION.md` 的「pi 22 条内置命令」是当时事实，不改，以本文档为准。
