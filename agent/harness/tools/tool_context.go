// Package tools corresponds to pi's packages/agent/src/harness/tools — the
// built-in execution tools (bash/edit/read/write), their shared diff
// utilities, path resolution, and the per-path file mutation queue. pi's
// tool-context.ts, path-utils.ts, file-mutation-queue.ts, image.ts,
// edit-diff.ts, read.ts, write.ts, edit.ts, and bash.ts map to the same-named
// snake_case files; the npm `diff` dependency is vendored in jsdiff.go.
//
// Segment deviations (package-wide):
//   - tools' execute(toolCallId, params, signal, onUpdate, context) → Execute
//     with a leading context.Context (AbortSignal → context rule).
//   - toolCallId: TS methods prefix the id with _ when unused; Go parameter
//     names are uniform.
//   - TypeBox schemas → hand-written JSON Schema literals (the harness sends
//     Parameters as json.RawMessage; required/optional members match
//     TypeBox's output).
//   - pi's createXTool returns AgentHarnessTool<TContext, Schema, Details>;
//     pig drops the generics (see agent/harness/types.go) and returns
//     *harness.AgentHarnessTool. The generic TContext on options types
//     collapses to any.
package tools

import (
	"encoding/json"
	"fmt"

	"github.com/ZoroSpace/pig/agent/harness"
)

// ExecutionToolContext is the filesystem and shell context required by the
// built-in execution tools (pi: interface ExecutionToolContext).
type ExecutionToolContext struct {
	Env harness.ExecutionEnv
}

// decodeParams converts the raw tool-call params (schema-validated
// map[string]any) into the typed input struct (pi: Static<TParameters> —
// TS validates at the type level; pig re-decodes through the JSON data
// model, which the schema has already gated).
func decodeParams[T any](params any) (T, error) {
	var input T
	data, err := json.Marshal(params)
	if err != nil {
		return input, err
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return input, err
	}
	return input, nil
}

// executionContextOf casts the turn context (pi: the TContext execute
// parameter; harness tools require ExecutionToolContext).
func executionContextOf(context any) (ExecutionToolContext, error) {
	if ctx, ok := context.(ExecutionToolContext); ok {
		return ctx, nil
	}
	return ExecutionToolContext{}, fmt.Errorf("Tool context is invalid. Expected ExecutionToolContext.")
}
