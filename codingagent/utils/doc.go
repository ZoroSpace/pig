// Package utils corresponds to pi's packages/coding-agent/src/utils —
// coding-agent-level helpers shared by the core tools and higher layers.
//
// Segment deviations:
//   - child-process.ts wraps node's spawn/cross-spawn; pig uses os/exec
//     directly (crossSpawn's Windows argv quirks have no Go counterpart —
//     go buildmode handles Windows process creation natively). The
//     waitForChildProcess grace semantics are ported in shell.go/exec land.
//   - text.ts/ansi.ts are tiny string helpers ported as-is.
package utils
