# pig

<p align="center">
  <img src="assets/pig-logo.png" alt="pig" width="200"/>
</p>

> **AI coding agent in Go · a behavior-compatible rewrite of pi**

[![Go 1.26+](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](#license)
[![Status: Dogfooding](https://img.shields.io/badge/status-dogfooding-orange.svg)](#provider-verification-status)
[![Go](https://github.com/ZoroSpace/pig/actions/workflows/go.yml/badge.svg)](https://github.com/ZoroSpace/pig/actions/workflows/go.yml)
[![codecov](https://codecov.io/gh/ZoroSpace/pig/graph/badge.svg)](https://codecov.io/gh/ZoroSpace/pig)
[![Go Report Card](https://goreportcard.com/badge/github.com/ZoroSpace/pig)](https://goreportcard.com/report/github.com/ZoroSpace/pig)

<p align="center">English | <a href="README.md">中文</a></p>

pig is a Go implementation of the [pi coding agent](https://github.com/earendil-works/pi) — an AI coding agent that lives in your terminal: it reads code, edits code, runs commands, streams replies, and lets you branch and rewind your sessions. pi's TypeScript source is the spec for pig: provider event sequences, CLI arguments, session JSONL format, and TUI key bindings are aligned item by item.

> [!WARNING]
> pig is in dogfooding stage; the CLI and internal structure may change. Only DeepSeek, Volcengine Ark Coding Plan, and Google (partial) have been verified against real services. For everything else, assume it isn't ready for real use — see the [verification status](#provider-verification-status).

## Background

This project began as a learning exercise: while studying the pi agent, I was not familiar with TypeScript, but pi was clearly structured with well-defined packages, so I translated the logic I read into Go section by section. Once I finished, my understanding became much smoother, and I immediately decided to translate and reimplement the whole thing.

After that, I developed it with the help of pi and glm-5.3-flash and built pig from scratch in 10 days. From the low-level session storage format, the mid-level agent loop and tool execution, and the streaming event protocol, to the top-level keypress feel and interaction experience—everything was aligned with pi v0.84.3. Since 2026-09-11 ([b5ff964](https://github.com/ZoroSpace/pig/commit/b5ff964d1b6a70fbfca25220cff7812b6cdb751b)), I have been using pig to develop pig and maintaining it long-term. Each time pi releases a new version, I align pig with it, and it is now aligned up to v1.4.0.

## Quick Start

### 1. Build

Requires Go 1.26+. Either way works:

**Recommended: go install** — if you already have a Go toolchain, one command installs to `$GOPATH/bin`:

```bash
go install github.com/ZoroSpace/pig/cmd/pig@latest
```

**Alternative: clone + build** — produces a single static stripped binary (smaller, no embedded build paths, fully static on Linux); use this if you want to hack on the code or copy the binary to another machine:

```bash
git clone https://github.com/ZoroSpace/pig.git && cd pig
scripts/build.sh
```

### 2. Launch and sign in

```bash
./pig
```

This enters the interactive TUI. Type `/login` to pick a provider: for API-key providers, paste the key and press Enter (credentials are stored in `~/.pig/agent/auth.json`); for OAuth providers (Anthropic / Kimi / OpenAI Codex / xAI), follow the browser or device-code flow.

### 3. Start a conversation

After signing in, pig auto-selects an available model (use `/model` to override) and you can ask directly:

> Skim this repo and tell me the most worthwhile thing to fix first

For scripting and CI, skip the interactive UI and drive it with environment variables + arguments:

```bash
DEEPSEEK_API_KEY=your-api-key \
  ./pig --provider deepseek --model deepseek-v4-flash -p "Explain the overall structure of this project"
```

See `./pig --help` for the full list of environment variables.

## Everyday Usage

### Interactive mode

```bash
./pig
```

Common interactive commands:

| Command | Purpose |
| --- | --- |
| `/model` `/thinking` | Pick a model, set thinking level |
| `/settings` | Change runtime settings |
| `/tree` `/resume` `/fork` | Session-tree navigation, resume old sessions, fork from any message |
| `/login` `/logout` | Manage provider auth |
| `/trust` | Manage project-level trust |
| `/compact` | Compact the current session context |
| `/session` `/copy` | View session stats, copy the last reply |
| `/import` `/export` | Import JSONL sessions, export HTML (a `.jsonl` suffix exports JSONL) |
| `/new` `/reload` `/hotkeys` | New session, reload config, view key bindings |

Press `Ctrl+P` to quickly cycle through configured models.

### Scripting & CI

```bash
./pig -p "List all Go files and summarize what each is responsible for"
./pig --mode json -p "Find all TODO comments in this repo"
git diff | ./pig -p "Review this diff and point out potential correctness issues"
./pig @AGENTS.md "Summarize this project's commit conventions per the repo guidelines"
./pig --export ~/.pig/agent/sessions/<project>/<session>.jsonl session.html   # share a session offline
```

Pipe input and `@file` references are supported; session-related flags such as `--continue` / `--resume` / `--no-session` / `--session-dir` are documented in `./pig --help`.

### Migrating from pi

pig and pi use separate config directories (`~/.pig/` vs `~/.pi/`), so existing data can be copied directly — session JSONL is bidirectional-compatible:

```bash
cp -a ~/.pi/agent ~/.pig/agent
```

## Highlights

### 🔥 Pure Go: one file, works out of the box

The build produces a **single static binary** (38 MB) with no Node dependency — copy it to Linux / macOS / Windows and it runs. Measured side by side with pi (npm install, Node runtime + dependency tree): 38 MB disk vs 436 MB + 115 MB Node, 0.025s startup vs ~1.0s, ~40 MB peak memory vs ~150 MB.

### 🔁 Seamless interop with pi

- **Bidirectional-compatible session records**: JSONL written by pig reads in pi and vice versa (verified against pi v1.4.0); the model catalog matches pi field by field with zero diffs
- **Isolated config**: pig uses `~/.pig/`, pi uses `~/.pi/`, no stepping on each other

### 🌐 41 providers, 7 API protocols

Providers differ wildly in wire format (OpenAI, Anthropic, and Gemini look nothing alike), so pig ports a dedicated adapter for each protocol, aligning event semantics (usage, tool-call boundaries, thinking blocks, cache hits, retries) per protocol: openai-completions / openai-responses / azure-openai-responses / openai-codex-responses (with WebSocket and zstd) / anthropic-messages / google-generative-ai / google-vertex. About 1,500 models are built in and usable out of the box; non-bundled endpoints can be added via `models.json`.

### 🛠 Built-in tools

`read` / `bash` / `powershell` / `edit` / `write` / `grep` / `find` / `ls`, with diff preview and file mutual-exclusion locking; `--tools` supports `+name`/`-name` add-remove and `*` wildcards. Project-level `AGENTS.md` / `CLAUDE.md` context files, SKILL.md skills, prompt templates, and a project trust mechanism are all in place.

### 🔌 Built-in MCP client

pi needs a community extension (pi-mcp-adapter) for MCP; pig ships it in the binary. Config reuses the ecosystem-standard `mcpServers` JSON (Claude Code / Cursor `.mcp.json` files work directly), with both stdio and Streamable HTTP; context cost stays constant (only one resident proxy tool, servers lazy-start and are reaped when idle); the `/mcp` panel shows status, reconnect, and start/stop.

### ⌨️ pi-tui, fully ported — the terminal feel

Diff renderer, editor engine, and completion system — all included: emacs key bindings, streaming Markdown / LaTeX / Mermaid, session-tree selector, mouse support, OSC 7501 program-status reporting, and a theme system. **Especially friendly to Chinese users**: a built-in Chinese word dictionary (gse), Ctrl+arrow word jumps, a whole class of pi upstream CJK rendering bugs fixed, and a built-in Volcengine Ark Coding Plan provider.

### 🌳 Session tree

Sessions branch naturally: re-walk from any historical node, tree navigation, auto-compaction (summarize and trim when context is nearly full), auto-retry (exponential backoff).

### 🧪 Engineering quality

**230k lines of Go source + 110k lines of tests, 2100+ test functions, 44 packages, `-race` clean**. Most of pi's tests were ported test-for-test; every fix ships with a regression test; any goroutine panic is dumped to `~/.pig/agent/logs/` with best-effort recovery.

### ✅ Done

- **Three run modes**: interactive, print, and JSON modes are all supported.
- **APIs and providers**: 7 API protocols, 41 providers, plus a model catalog.
- **Sessions**: session tree, branching, compaction, and HTML export all work.
- **TUI**: fully ported.
- **RPC mode**: `--mode rpc` supported.
- **Image handling**: replaced photon/WASM with a native Go implementation.
- **MCP**: built-in MCP client.

### ⏳ Not following yet

- **durable**: pi extracted/migrated the experimental Pico harness into the standalone package `packages/durable`. It's a fast-iterating package with possible breaking changes at any time; we'll port it once it stabilizes.
- **pi's experimental code**: experimental architectures like remote orchestration change quickly and may be dropped; not following for now.
- **Extension system (Node sidecar, package manager)**: the hook points are reserved in the code and the design is archived in [EXTENSION-PLAN](docs/handoffs/EXTENSION-PLAN.md); not implemented for now.

### ⛔ Explicitly out of scope

- **/bug reporting pipeline**: no separate reporting channel — pig users open an [issue](https://github.com/ZoroSpace/pig/issues) directly.
- **sqlite session backend / client-server stack**: upstream removed it; pig doesn't follow.

## Provider Verification Status

Note: **some providers have not been verified against a real service yet.** The table below marks each by how far it has been verified:

- **Implemented**: the code and local unit tests are in place
- **Protocol-verified**: request, event, and parsing logic verified via DeepSeek's compatible endpoints or mocks
- **Verified against a real service**: actually hit the target provider's real API and completed a minimal working path

| Status | Scope | Notes |
| --- | --- | --- |
| ✅ Verified against real endpoints | DeepSeek, Volcengine Ark Coding Plan, Google (partial) | Real API streaming conversations; Volcengine includes thinking scenarios; Google is the gemini-3.x flash base path + thinking-disable probe (some models limited by account tier/region — see the verification checklist) |
| 🔶 Verified against compatible endpoints | Anthropic Messages, OpenAI Responses family | Protocol layer verified via DeepSeek's `/anthropic` and Responses compatible endpoints; the official Anthropic / OpenAI endpoints are not yet tested |
| 🧪 Mock / unit-test verified | Everyone else, OAuth, provider-specific semantics | Not yet verified against a real service or a real login path |

Current verification status of common providers:

| Provider | Env var | Verification status |
| --- | --- | --- |
| DeepSeek | `DEEPSEEK_API_KEY` | ✅ Real endpoint |
| Volcengine Ark Coding Plan | `VOLCENGINE_API_KEY` | ✅ Real endpoint |
| Google Gemini | `GEMINI_API_KEY` | ✅ Partial (gemini-3.x flash base path + thinking disable; some models limited by account tier/region) |
| Anthropic | `ANTHROPIC_API_KEY` | 🔶 Protocol layer (via DeepSeek `/anthropic` compatible endpoint); official endpoint not yet tested |
| OpenAI | `OPENAI_API_KEY` | 🔶 Protocol layer (via DeepSeek Responses compatible endpoint); official endpoint not yet tested |
| xAI | `XAI_API_KEY` | 🔶 Protocol layer (openai-responses family); OAuth device flow not yet tested |
| OpenRouter | `OPENROUTER_API_KEY` | 🔶 Protocol layer; OAuth not yet tested |
| Amazon Bedrock | `AWS_BEARER_TOKEN_BEDROCK` or AWS credential chain (`AWS_PROFILE` / env keys / IAM role) | 🧪 Mock (local e2e with real SDK transport, no real AWS) |

The full boundary, pending-verification conditions, and ready-to-run probes live in the [provider verification checklist](docs/handoffs/verify-matrix.md). Help from anyone with a key for another provider is welcome; file an issue or PR for any discrepancy, and fixes will be merged with a regression test per repo convention.

## Project Structure

```
cmd/pig          CLI assembly entry point
codingagent/     agent runtime: session management, model resolution, settings, tools, interactive mode
├─ core/         AgentSession, credentials, compaction, session tree, built-in tools
├─ modes/        print / json / interactive modes
agent/           agent loop: steering/follow-up queue, tool execution, lifecycle
ai/              provider layer: 7 API protocol adapters, model catalog, OAuth
tui/             pi-tui port: diff renderer, editor engine, component library
```

## Development

Requirements: Go 1.26+; to run against real requests you need credentials for at least one provider.

```bash
gofmt -w <changed-files>
go build ./cmd/pig
go vet ./...
go test ./... -timeout 120s                  # unit tests (explicit timeout)
go test ./... -race -count=1 -timeout 600s   # full regression
```

Conventions:

- The full `-race` regression runs **exactly once**; reuse that same output when you need pass counts or failure details
- Tests that hit real services need real credentials and are not run in the default test suite
- Important fixes and migration decisions are recorded in `docs/fix/`, `docs/feat/`, and `docs/handoffs/`; every fix must ship with a regression test

## Contributing

Bug fixes and focused improvements are welcome. For anything that changes behavior:

1. Add or update a regression test (verify the test actually catches the original bug via a stash first)
2. Run narrow tests for the affected packages first
3. Run the full `-race` suite before delivering
4. Record fixes and decisions in `docs/fix/`, `docs/feat/`, or `docs/handoffs/`

## License

This project is a behavior-spec rewrite of [pi](https://github.com/earendil-works/pi) (MIT) and is licensed under the MIT License.
