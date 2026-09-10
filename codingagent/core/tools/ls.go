// pi: src/core/tools/ls.ts — the ls tool (directory listing with entry
// limits and byte truncation).
//
// Segment deviations:
//   - The render functions live in renderers_*.go (S5a: pi moved them to
//     core/tools/renderers/; pig keeps package tools, see the file header
//     there).
//   - signal wiring → context.Context (abort-listener pattern collapses to
//     ctx probes; the entries loop is synchronous so there is nothing to
//     interrupt mid-flight).
//   - pi sorts with localeCompare (lowercased); pig sorts with
//     strings.ToLower + < byte comparison (ASCII filenames sort identically,
//     declared per the truncation-module convention).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ZoroSpace/pig/agent"
	harnessutils "github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/ai/types"
)

// lsSchemaJSON is the ls tool's parameter schema (pi: lsSchema).
const lsSchemaJSON = `{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Directory to list (default: current directory)"
    },
    "limit": {
      "type": "number",
      "description": "Maximum number of entries to return (default: 500)"
    }
  },
  "required": []
}`

// LsToolSystemPromptContribution is the ls tool's system prompt metadata
// (pi: lsToolSystemPromptContribution).
var LsToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet:    "List directory contents",
	Guidelines: nil,
}

// LsToolInput is the ls tool's parameter shape (pi: LsToolInput).
type LsToolInput struct {
	Path  string   `json:"path,omitempty"`
	Limit *float64 `json:"limit,omitempty"`
}

// LsToolDetails carries the ls outcome metadata (pi: LsToolDetails).
type LsToolDetails struct {
	Truncation        *harnessutils.TruncationResult `json:"truncation,omitempty"`
	EntryLimitReached *int                           `json:"entryLimitReached,omitempty"`
}

// lsDefaultLimit is pi's DEFAULT_LIMIT.
const lsDefaultLimit = 500

// LsOperations are pluggable operations for the ls tool; override these to
// delegate directory listing to remote systems (pi: LsOperations).
type LsOperations struct {
	// Exists checks if path exists.
	Exists func(ctx context.Context, absolutePath string) (bool, error)
	// Stat gets file or directory stats. Throws if not found.
	Stat func(ctx context.Context, absolutePath string) (os.FileInfo, error)
	// Readdir reads directory entries.
	Readdir func(ctx context.Context, absolutePath string) ([]string, error)
}

func defaultLsOperations() LsOperations {
	return LsOperations{
		Exists: func(ctx context.Context, path string) (bool, error) {
			return fileExists(path), nil
		},
		Stat: func(ctx context.Context, path string) (os.FileInfo, error) { return os.Stat(path) },
		Readdir: func(ctx context.Context, path string) ([]string, error) {
			entries, err := os.ReadDir(path)
			if err != nil {
				return nil, err
			}
			names := make([]string, len(entries))
			for i, entry := range entries {
				names[i] = entry.Name()
			}
			return names, nil
		},
	}
}

// LsToolOptions parameterize the ls tool (pi: LsToolOptions).
type LsToolOptions struct {
	// Operations customizes directory listing. Default: local filesystem.
	Operations *LsOperations
}

// CreateLsToolDefinition builds the ls tool definition (pi:
// createLsToolDefinition; renderCall/renderResult arrive with the theme
// batch).
func CreateLsToolDefinition(cwd string, options *LsToolOptions) *ToolDefinition {
	ops := defaultLsOperations()
	if options != nil && options.Operations != nil {
		ops = *options.Operations
	}
	return &ToolDefinition{
		Name:          "ls",
		Label:         "ls",
		Description:   fmt.Sprintf("List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to %d entries or %dKB (whichever is hit first).", lsDefaultLimit, harnessutils.DefaultMaxBytes/1024),
		PromptSnippet: LsToolSystemPromptContribution.Snippet,
		Parameters:    json.RawMessage(lsSchemaJSON),
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx ToolExecuteContext) (agent.AgentToolResult, error) {
			input, err := decodeToolParams[LsToolInput](params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			if ctx.Err() != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Operation aborted")
			}
			dirPath := ResolveToCwd(orDot(input.Path), executeCwd(toolCtx, cwd))
			effectiveLimit := lsDefaultLimit
			if input.Limit != nil {
				effectiveLimit = int(*input.Limit)
			}

			// Check if path exists.
			exists, err := ops.Exists(ctx, dirPath)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			if !exists {
				return agent.AgentToolResult{}, fmt.Errorf("Path not found: %s", dirPath)
			}

			// Check if path is a directory.
			stat, err := ops.Stat(ctx, dirPath)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			if !stat.IsDir() {
				return agent.AgentToolResult{}, fmt.Errorf("Not a directory: %s", dirPath)
			}

			// Read directory entries.
			entries, err := ops.Readdir(ctx, dirPath)
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Cannot read directory: %s", err.Error())
			}

			// Sort alphabetically, case-insensitive.
			sort.Slice(entries, func(i, j int) bool {
				return strings.ToLower(entries[i]) < strings.ToLower(entries[j])
			})

			// Format entries with directory indicators.
			var results []string
			entryLimitReached := false
			for _, entry := range entries {
				if len(results) >= effectiveLimit {
					entryLimitReached = true
					break
				}
				fullPath := filepath.Join(dirPath, entry)
				suffix := ""
				entryStat, err := ops.Stat(ctx, fullPath)
				if err != nil {
					// Skip entries we cannot stat.
					continue
				}
				if entryStat.IsDir() {
					suffix = "/"
				}
				results = append(results, entry+suffix)
			}

			if len(results) == 0 {
				return resultOf([]types.Content{asTextContent("(empty directory)")}, nil), nil
			}

			rawOutput := strings.Join(results, "\n")
			// Apply byte truncation. There is no separate line limit because
			// entry count is already capped.
			maxLines := int(^uint(0) >> 1)
			truncation := harnessutils.TruncateHead(rawOutput, &harnessutils.TruncationOptions{MaxLines: &maxLines})
			output := truncation.Content
			var details LsToolDetails
			// Build actionable notices for truncation and entry limits.
			var notices []string
			if entryLimitReached {
				notices = append(notices, fmt.Sprintf("%d entries limit reached. Use limit=%d for more", effectiveLimit, effectiveLimit*2))
				details.EntryLimitReached = &effectiveLimit
			}
			if truncation.Truncated {
				notices = append(notices, fmt.Sprintf("%s limit reached", harnessutils.FormatSize(harnessutils.DefaultMaxBytes)))
				details.Truncation = &truncation
			}
			if len(notices) > 0 {
				output += "\n\n[" + strings.Join(notices, ". ") + "]"
			}

			var detailsValue any
			if details.EntryLimitReached != nil || details.Truncation != nil {
				detailsValue = &details
			}
			return resultOf([]types.Content{asTextContent(output)}, detailsValue), nil
		},
		// pi: ...lsRenderers.
		RenderCall:   LsRenderers.RenderCall,
		RenderResult: LsRenderers.RenderResult,
	}
}

// CreateLsTool wraps the ls definition into an AgentTool (pi: createLsTool).
func CreateLsTool(cwd string, options *LsToolOptions) Tool {
	return WrapToolDefinition(CreateLsToolDefinition(cwd, options), nil)
}
