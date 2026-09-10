// pi: src/harness/tools/write.ts — the write tool.
//
// Segment deviations:
//   - content.length (UTF-16 code units) → len(content) bytes; the tool
//     operates on byte content (see truncate.go's UTF-8 world note).
//   - signal?.aborted checks → ctx.Err() probes.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
)

// writeSchemaJSON is the write tool's parameter schema (pi: writeSchema via
// TypeBox).
const writeSchemaJSON = `{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Path to the file to write (relative or absolute)"
    },
    "content": {
      "type": "string",
      "description": "Content to write to the file"
    }
  },
  "required": ["path", "content"]
}`

// WriteToolInput is the write tool's parameter shape (pi: WriteToolInput).
type WriteToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// CreateWriteTool builds the write tool (pi: createWriteTool).
func CreateWriteTool() *harness.AgentHarnessTool {
	return &harness.AgentHarnessTool{
		Name:        "write",
		Label:       "write",
		Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
		Parameters:  json.RawMessage(writeSchemaJSON),
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, context any) (agent.AgentToolResult, error) {
			input, err := decodeParams[WriteToolInput](params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			executionContext, err := executionContextOf(context)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			env := executionContext.Env

			absolutePath, err := ResolveToolPath(ctx, env, input.Path)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			return WithFileMutationQueue(ctx, env, absolutePath, func() (agent.AgentToolResult, error) {
				if ctx.Err() != nil {
					return agent.AgentToolResult{}, errors.New("Operation aborted")
				}
				if err := env.WriteFile(ctx, absolutePath, []byte(input.Content)); err != nil {
					return agent.AgentToolResult{}, err
				}
				if ctx.Err() != nil {
					return agent.AgentToolResult{}, errors.New("Operation aborted")
				}
				return agent.AgentToolResult{
					Content: []types.Content{
						&types.TextContent{Kind: types.KindText, Text: fmt.Sprintf("Successfully wrote %d bytes to %s", len(input.Content), input.Path)},
					},
				}, nil
			})
		},
	}
}
