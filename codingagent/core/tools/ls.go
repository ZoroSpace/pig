// pi: src/core/tools/ls.ts — the ls tool (directory listing with entry
// limits and byte truncation).
//
// Segment deviations:
//   - The render functions depend on Theme/pi-tui and arrive with the theme
//     batch.
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
	"github.com/ZoroSpace/pig/tui"
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
			dirPath := ResolveToCwd(orDot(input.Path), cwd)
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
		RenderCall: func(args any, themeArg UITheme, context ToolRenderContext) tui.Component {
			var text *tui.Text
			if c, ok := context.LastComponent.(*tui.Text); ok {
				text = c
			} else {
				text = tui.NewText("", 0, 0)
			}
			m, _ := args.(map[string]any)
			if m == nil {
				m = map[string]any{}
			}
			text.SetText(formatLsCall(themeArg, m, context.Cwd))
			return text
		},
		RenderResult: func(result ToolRenderResult, options ToolRenderResultOptions, themeArg UITheme, context ToolRenderContext) tui.Component {
			var text *tui.Text
			if c, ok := context.LastComponent.(*tui.Text); ok {
				text = c
			} else {
				text = tui.NewText("", 0, 0)
			}
			text.SetText(formatLsResult(themeArg, result, options, context.ShowImages))
			return text
		},
	}
}

// ============================================================================
// Render segment (pi: ls.ts render declarations)
// ============================================================================

// formatLsCall renders the call row (pi: formatLsCall).
func formatLsCall(theme UITheme, args map[string]any, cwd string) string {
	var limit *float64
	if v, ok := args["limit"].(float64); ok {
		limit = &v
	}
	pathDisplay := RenderToolPath(rawPathFromArgs(args), theme, cwd, ".")
	text := theme.Fg("toolTitle", theme.Bold("ls")) + " " + pathDisplay
	if limit != nil {
		text += theme.Fg("toolOutput", fmt.Sprintf(" (limit %d)", int64(*limit)))
	}
	return text
}

// formatLsResult renders the result body (pi: formatLsResult).
func formatLsResult(theme UITheme, result ToolRenderResult, options ToolRenderResultOptions, showImages bool) string {
	output := strings.Trim(GetTextOutput(&result, showImages), " \t\n\r")
	text := ""
	if output != "" {
		lines := strings.Split(output, "\n")
		maxLines := 20
		if options.Expanded {
			maxLines = len(lines)
		}
		displayEnd := maxLines
		if displayEnd > len(lines) {
			displayEnd = len(lines)
		}
		displayLines := lines[:displayEnd]
		remaining := len(lines) - maxLines
		styled := make([]string, 0, len(displayLines))
		for _, line := range displayLines {
			styled = append(styled, theme.Fg("toolOutput", line))
		}
		text += "\n" + strings.Join(styled, "\n")
		if remaining > 0 {
			text += theme.Fg("muted", fmt.Sprintf("\n... (%d more lines,", remaining)) +
				" " + KeyHint("app.tools.expand", "to expand") + theme.Fg("muted", ")")
		}
	}

	var details *LsToolDetails
	if result.Details != nil {
		details, _ = result.Details.(*LsToolDetails)
	}
	var entryLimit *int
	var truncation *harnessutils.TruncationResult
	if details != nil {
		entryLimit = details.EntryLimitReached
		truncation = details.Truncation
	}
	if entryLimit != nil || (truncation != nil && truncation.Truncated) {
		var warnings []string
		if entryLimit != nil {
			warnings = append(warnings, fmt.Sprintf("%d entries limit", *entryLimit))
		}
		if truncation != nil && truncation.Truncated {
			maxBytes := int64(harnessutils.DefaultMaxBytes)
			if truncation.MaxBytes != 0 {
				maxBytes = int64(truncation.MaxBytes)
			}
			warnings = append(warnings, harnessutils.FormatSize(maxBytes)+" limit")
		}
		text += "\n" + theme.Fg("warning", "[Truncated: "+strings.Join(warnings, ", ")+"]")
	}
	return text
}

// CreateLsTool wraps the ls definition into an AgentTool (pi: createLsTool).
func CreateLsTool(cwd string, options *LsToolOptions) Tool {
	return WrapToolDefinition(CreateLsToolDefinition(cwd, options), nil)
}
