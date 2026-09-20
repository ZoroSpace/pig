// pi: src/core/tools/write.ts — the coding-agent write tool.
//
// Segment deviations:
//   - The render functions live in renderers_write.go (S5a: pi moved them
//     to core/tools/renderers/write.ts; pig keeps package tools, see the
//     file header there).
//   - signal wiring → context.Context (throwIfAborted probes).
//   - content.length (UTF-16 code units) → len(content) bytes.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

// writeSchemaJSON is the write tool's parameter schema (pi: writeSchema).
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

// WriteToolSystemPromptContribution is the write tool's system prompt
// metadata (pi: writeToolSystemPromptContribution).
var WriteToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet:    "Create or overwrite files",
	Guidelines: []string{"Use write only for new files or complete rewrites."},
}

// WriteToolInput is the write tool's parameter shape (pi: WriteToolInput).
type WriteToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WriteOperations are pluggable operations for the write tool; override
// these to delegate file writing to remote systems (pi: WriteOperations).
type WriteOperations struct {
	// WriteFile writes content to a file.
	WriteFile func(ctx context.Context, absolutePath string, content string) error
	// Mkdir creates a directory recursively.
	Mkdir func(ctx context.Context, dir string) error
}

func defaultWriteOperations() WriteOperations {
	return WriteOperations{
		WriteFile: func(ctx context.Context, path string, content string) error {
			return os.WriteFile(path, []byte(content), 0o644)
		},
		Mkdir: func(ctx context.Context, dir string) error { return os.MkdirAll(dir, 0o755) },
	}
}

// WriteToolOptions parameterize the write tool (pi: WriteToolOptions).
type WriteToolOptions struct {
	// Operations customizes file writing. Default: local filesystem.
	Operations *WriteOperations
}

// CreateWriteToolDefinition builds the write tool definition (pi:
// createWriteToolDefinition; renderCall/renderResult arrive with the theme
// batch).
func CreateWriteToolDefinition(cwd string, options *WriteToolOptions) *ToolDefinition {
	ops := defaultWriteOperations()
	if options != nil && options.Operations != nil {
		ops = *options.Operations
	}
	return &ToolDefinition{
		Name:             "write",
		Label:            "write",
		Description:      "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
		PromptSnippet:    WriteToolSystemPromptContribution.Snippet,
		PromptGuidelines: append([]string{}, WriteToolSystemPromptContribution.Guidelines...),
		Parameters:       json.RawMessage(writeSchemaJSON),
		// pi v0.86.0: strict-mode tool sampling is hardcoded (was
		// getExperimentalToolSampling()).
		ConstrainedSampling: types.ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"},
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx ToolExecuteContext) (agent.AgentToolResult, error) {
			input, err := decodeToolParams[WriteToolInput](params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			absolutePath := ResolveToCwd(input.Path, executeCwd(toolCtx, cwd))
			dir := filepath.Dir(absolutePath)

			// Do not reject from an abort event listener here: that would
			// release the mutation queue while an in-flight filesystem
			// operation may still finish. (pi comment; throwIfAborted
			// probes.)
			throwIfAborted := func() error {
				if ctx.Err() != nil {
					return fmt.Errorf("Operation aborted")
				}
				return nil
			}

			return WithFileMutationQueue(ctx, absolutePath, func() (agent.AgentToolResult, error) {
				if err := throwIfAborted(); err != nil {
					return agent.AgentToolResult{}, err
				}
				// Create parent directories if needed.
				if err := ops.Mkdir(ctx, dir); err != nil {
					return agent.AgentToolResult{}, err
				}
				if err := throwIfAborted(); err != nil {
					return agent.AgentToolResult{}, err
				}

				// Write the file contents.
				if err := ops.WriteFile(ctx, absolutePath, input.Content); err != nil {
					return agent.AgentToolResult{}, err
				}
				if err := throwIfAborted(); err != nil {
					return agent.AgentToolResult{}, err
				}

				return agent.AgentToolResult{
					// pi (v0.85.1): the byte count was dropped from the success
					// message.
					Content: []types.Content{asTextContent(fmt.Sprintf("Successfully wrote to %s", input.Path))},
				}, nil
			})
		},
		// pi: ...writeRenderers.
		RenderCall:   WriteRenderers.RenderCall,
		RenderResult: WriteRenderers.RenderResult,
	}
}

// CreateWriteTool wraps the write definition into an AgentTool (pi:
// createWriteTool).
func CreateWriteTool(cwd string, options *WriteToolOptions) Tool {
	return WrapToolDefinition(CreateWriteToolDefinition(cwd, options), nil)
}
