// Package tools corresponds to pi's packages/coding-agent/src/core/tools —
// the coding-agent built-in tools (read/bash/powershell/edit/write/grep/
// find/ls), their shared path/mutation/diff helpers, the external search
// binary manager, and the tool factory surface.
//
// File mapping: bash.ts → bash.go, edit.ts → edit.go, edit-diff.ts →
// edit_diff.go (shared algorithm imported from agent/harness/tools), file-
// mutation-queue.ts → file_mutation_queue.go, find.ts → find.go, grep.ts →
// grep.go, index.ts → index.go, ls.ts → ls.go, output-accumulator.ts →
// output_accumulator.go, path-utils.ts → path_utils.go, powershell.ts →
// powershell.go, read.ts → read.go, render-utils.ts → (pure text helpers in
// shared.go; TUI-dependent helpers arrive with the theme batch),
// tool-definition-wrapper.ts → tool_context.go's wrapToolDefinition,
// truncate.ts → (byte-identical duplicate of the agent package's
// utils/truncate.ts; pig imports agent/harness/utils).
//
// Batch scope and placeholders are declared in tool_context.go.
package tools
