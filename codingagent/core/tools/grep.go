// pi: src/core/tools/grep.ts — the grep tool (streaming ripgrep with JSON
// events, match limits, context blocks, and byte truncation).
//
// Segment deviations:
//   - The render functions depend on Theme/pi-tui and arrive with the theme
//     batch.
//   - node child_process + readline streaming → the shared
//     codingagent/utils spawn helpers; the JSON event lines are read with a
//     bufio scanner on the child's stdout pipe.
//   - signal wiring → context.Context (abort kills the child and rejects
//     with "Operation aborted").
//   - ensureTool's onStatus progress is silent in execute (pi passes no
//     onStatus here either).
package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ZoroSpace/pig/agent"
	harnessutils "github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// grepSchemaJSON is the grep tool's parameter schema (pi: grepSchema).
const grepSchemaJSON = `{
  "type": "object",
  "properties": {
    "pattern": {
      "type": "string",
      "description": "Search pattern (regex or literal string)"
    },
    "path": {
      "type": "string",
      "description": "Directory or file to search (default: current directory)"
    },
    "glob": {
      "type": "string",
      "description": "Filter files by glob pattern, e.g. '*.ts' or '**/*.spec.ts'"
    },
    "ignoreCase": {
      "type": "boolean",
      "description": "Case-insensitive search (default: false)"
    },
    "literal": {
      "type": "boolean",
      "description": "Treat pattern as literal string instead of regex (default: false)"
    },
    "context": {
      "type": "number",
      "description": "Number of lines to show before and after each match (default: 0)"
    },
    "limit": {
      "type": "number",
      "description": "Maximum number of matches to return (default: 100)"
    }
  },
  "required": ["pattern"]
}`

// GrepToolSystemPromptContribution is the grep tool's system prompt metadata
// (pi: grepToolSystemPromptContribution).
var GrepToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet:    "Search file contents for patterns (respects .gitignore)",
	Guidelines: nil,
}

// GrepToolInput is the grep tool's parameter shape (pi: GrepToolInput).
type GrepToolInput struct {
	Pattern    string   `json:"pattern"`
	Path       string   `json:"path,omitempty"`
	Glob       string   `json:"glob,omitempty"`
	IgnoreCase *bool    `json:"ignoreCase,omitempty"`
	Literal    *bool    `json:"literal,omitempty"`
	Context    *float64 `json:"context,omitempty"`
	Limit      *float64 `json:"limit,omitempty"`
}

// GrepToolDetails carries the grep outcome metadata (pi: GrepToolDetails).
type GrepToolDetails struct {
	Truncation        *harnessutils.TruncationResult `json:"truncation,omitempty"`
	MatchLimitReached *int                           `json:"matchLimitReached,omitempty"`
	LinesTruncated    *bool                          `json:"linesTruncated,omitempty"`
}

// grepDefaultLimit is pi's DEFAULT_LIMIT.
const grepDefaultLimit = 100

// GrepOperations are pluggable operations for the grep tool; override these
// to delegate search to remote systems (pi: GrepOperations).
type GrepOperations struct {
	// IsDirectory checks if path is a directory. Throws if the path does
	// not exist.
	IsDirectory func(ctx context.Context, absolutePath string) (bool, error)
	// ReadFile reads file contents for context lines.
	ReadFile func(ctx context.Context, absolutePath string) (string, error)
}

func defaultGrepOperations() GrepOperations {
	return GrepOperations{
		IsDirectory: func(ctx context.Context, path string) (bool, error) {
			info, err := os.Stat(path)
			if err != nil {
				return false, err
			}
			return info.IsDir(), nil
		},
		ReadFile: func(ctx context.Context, path string) (string, error) {
			content, err := os.ReadFile(path)
			return string(content), err
		},
	}
}

// rgMatchEvent is the subset of ripgrep's --json event format consumed here
// (pi: event.data.path.text / event.data.line_number / event.data.lines.text).
type rgMatchEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		LineNumber int `json:"line_number"`
		Lines      struct {
			Text string `json:"text"`
		} `json:"lines"`
	} `json:"data"`
}

// CreateGrepToolDefinition builds the grep tool definition (pi:
// createGrepToolDefinition; renderCall/renderResult arrive with the theme
// batch).
func CreateGrepToolDefinition(cwd string, options *GrepToolOptions) *ToolDefinition {
	var customOps *GrepOperations
	if options != nil && options.Operations != nil {
		customOps = options.Operations
	}
	return &ToolDefinition{
		Name:          "grep",
		Label:         "grep",
		Description:   fmt.Sprintf("Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Output is truncated to %d matches or %dKB (whichever is hit first). Long lines are truncated to %d chars.", grepDefaultLimit, harnessutils.DefaultMaxBytes/1024, harnessutils.GrepMaxLineLength),
		PromptSnippet: GrepToolSystemPromptContribution.Snippet,
		Parameters:    json.RawMessage(grepSchemaJSON),
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx ToolExecuteContext) (agent.AgentToolResult, error) {
			input, err := decodeToolParams[GrepToolInput](params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			if ctx.Err() != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Operation aborted")
			}

			rgPath := utils.EnsureTool(ctx, "rg", nil)
			if rgPath == "" {
				return agent.AgentToolResult{}, fmt.Errorf("ripgrep (rg) is not available and could not be downloaded")
			}

			searchPath := ResolveToCwd(orDot(input.Path), cwd)
			ops := defaultGrepOperations()
			if customOps != nil {
				ops = *customOps
			}
			isDirectory, err := ops.IsDirectory(ctx, searchPath)
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Path not found: %s", searchPath)
			}

			contextValue := 0
			if input.Context != nil && *input.Context > 0 {
				contextValue = int(*input.Context)
			}
			effectiveLimit := grepDefaultLimit
			if input.Limit != nil {
				effectiveLimit = int(*input.Limit)
				if effectiveLimit < 1 {
					effectiveLimit = 1
				}
			}
			formatPath := func(filePath string) string {
				if isDirectory {
					rel, err := filepath.Rel(searchPath, filePath)
					if err == nil && rel != "" && !strings.HasPrefix(rel, "..") {
						return filepath.ToSlash(rel)
					}
				}
				return filepath.Base(filePath)
			}

			var fileCacheMu syncMap
			getFileLines := func(filePath string) []string {
				if lines, ok := fileCacheMu.Load(filePath); ok {
					return lines
				}
				lines := []string{}
				content, err := ops.ReadFile(ctx, filePath)
				if err == nil {
					content = strings.ReplaceAll(content, "\r\n", "\n")
					content = strings.ReplaceAll(content, "\r", "\n")
					lines = strings.Split(content, "\n")
				}
				fileCacheMu.Store(filePath, lines)
				return lines
			}

			args := []string{"--json", "--line-number", "--color=never", "--hidden"}
			if input.IgnoreCase != nil && *input.IgnoreCase {
				args = append(args, "--ignore-case")
			}
			if input.Literal != nil && *input.Literal {
				args = append(args, "--fixed-strings")
			}
			if input.Glob != "" {
				args = append(args, "--glob", input.Glob)
			}
			args = append(args, "--", input.Pattern, searchPath)

			cmd, pipes, err := utils.SpawnProcess(rgPath, args, "", os.Environ())
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Failed to run ripgrep: %s", err.Error())
			}
			// pi's spawn starts the child immediately; StartChild wraps
			// cmd.Start plus the parent write-end release.
			if _, err := utils.StartChild(cmd, pipes); err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Failed to run ripgrep: %s", err.Error())
			}
			stderr := ""
			go func() {
				scanner := bufio.NewScanner(pipes.StderrRead)
				scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
				for scanner.Scan() {
					stderr += scanner.Text()
				}
			}()

			matchCount := 0
			matchLimitReached := false
			killedDueToLimit := false
			var matches []rgMatchEvent
			var outputLines []string
			linesTruncated := false

			childDone := make(chan struct{})
			var closeCode int
			var haveCloseCode bool
			go func() {
				defer close(childDone)
				scanner := bufio.NewScanner(pipes.StdoutRead)
				scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
				for scanner.Scan() {
					line := scanner.Text()
					if strings.TrimSpace(line) == "" || matchCount >= effectiveLimit {
						continue
					}
					var event rgMatchEvent
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						continue
					}
					if event.Type == "match" {
						matchCount++
						if event.Data.Path.Text != "" && event.Data.LineNumber > 0 {
							matches = append(matches, event)
						}
						if matchCount >= effectiveLimit {
							matchLimitReached = true
							killChild(cmd)
							killedDueToLimit = true
						}
					}
				}
				_ = cmd.Wait()
				if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
					closeCode = cmd.ProcessState.ExitCode()
					haveCloseCode = true
				}
			}()

			select {
			case <-childDone:
			case <-ctx.Done():
				killChild(cmd)
				<-childDone
				return agent.AgentToolResult{}, fmt.Errorf("Operation aborted")
			}
			pipes.CloseReadEnds()

			if !killedDueToLimit && haveCloseCode && closeCode != 0 && closeCode != 1 {
				errorMsg := strings.TrimSpace(stderr)
				if errorMsg == "" {
					errorMsg = fmt.Sprintf("ripgrep exited with code %d", closeCode)
				}
				return agent.AgentToolResult{}, fmt.Errorf("%s", errorMsg)
			}
			if matchCount == 0 {
				return resultOf([]types.Content{asTextContent("No matches found")}, nil), nil
			}

			// Format matches after streaming finishes so custom readFile()
			// backends can be async.
			truncateLineFn := func(line string) (string, bool) {
				text, wasTruncated := harnessutils.TruncateLine(line, harnessutils.GrepMaxLineLength)
				return text, wasTruncated
			}
			formatBlock := func(filePath string, lineNumber int) []string {
				relativePath := formatPath(filePath)
				lines := getFileLines(filePath)
				if len(lines) == 0 {
					return []string{fmt.Sprintf("%s:%d: (unable to read file)", relativePath, lineNumber)}
				}
				var block []string
				start := lineNumber
				end := lineNumber
				if contextValue > 0 {
					start = lineNumber - contextValue
					if start < 1 {
						start = 1
					}
					end = lineNumber + contextValue
					if end > len(lines) {
						end = len(lines)
					}
				}
				for current := start; current <= end; current++ {
					lineText := ""
					if current-1 < len(lines) {
						lineText = lines[current-1]
					}
					sanitized := strings.ReplaceAll(lineText, "\r", "")
					isMatchLine := current == lineNumber
					truncatedText, wasTruncated := truncateLineFn(sanitized)
					if wasTruncated {
						linesTruncated = true
					}
					if isMatchLine {
						block = append(block, fmt.Sprintf("%s:%d: %s", relativePath, current, truncatedText))
					} else {
						block = append(block, fmt.Sprintf("%s-%d- %s", relativePath, current, truncatedText))
					}
				}
				return block
			}

			for _, match := range matches {
				if contextValue == 0 && match.Data.Lines.Text != "" {
					relativePath := formatPath(match.Data.Path.Text)
					sanitized := match.Data.Lines.Text
					sanitized = strings.ReplaceAll(sanitized, "\r\n", "\n")
					sanitized = strings.ReplaceAll(sanitized, "\r", "")
					sanitized = strings.TrimRight(sanitized, "\n")
					truncatedText, wasTruncated := truncateLineFn(sanitized)
					if wasTruncated {
						linesTruncated = true
					}
					outputLines = append(outputLines, fmt.Sprintf("%s:%d: %s", relativePath, match.Data.LineNumber, truncatedText))
				} else {
					block := formatBlock(match.Data.Path.Text, match.Data.LineNumber)
					outputLines = append(outputLines, block...)
				}
			}

			rawOutput := strings.Join(outputLines, "\n")
			// Apply byte truncation. There is no line limit here because the
			// match limit already capped rows.
			maxLines := int(^uint(0) >> 1)
			truncation := harnessutils.TruncateHead(rawOutput, &harnessutils.TruncationOptions{MaxLines: &maxLines})
			output := truncation.Content
			var details GrepToolDetails
			// Build actionable notices for truncation and match limits.
			var notices []string
			if matchLimitReached {
				notices = append(notices, fmt.Sprintf("%d matches limit reached. Use limit=%d for more, or refine pattern", effectiveLimit, effectiveLimit*2))
				details.MatchLimitReached = &effectiveLimit
			}
			if truncation.Truncated {
				notices = append(notices, fmt.Sprintf("%s limit reached", harnessutils.FormatSize(harnessutils.DefaultMaxBytes)))
				details.Truncation = &truncation
			}
			if linesTruncated {
				notices = append(notices, fmt.Sprintf("Some lines truncated to %d chars. Use read tool to see full lines", harnessutils.GrepMaxLineLength))
				details.LinesTruncated = &linesTruncated
			}
			if len(notices) > 0 {
				output += "\n\n[" + strings.Join(notices, ". ") + "]"
			}
			var detailsValue any
			if details.MatchLimitReached != nil || details.Truncation != nil || details.LinesTruncated != nil {
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
			text.SetText(formatGrepCall(themeArg, m))
			return text
		},
		RenderResult: func(result ToolRenderResult, options ToolRenderResultOptions, themeArg UITheme, context ToolRenderContext) tui.Component {
			var text *tui.Text
			if c, ok := context.LastComponent.(*tui.Text); ok {
				text = c
			} else {
				text = tui.NewText("", 0, 0)
			}
			text.SetText(formatGrepResult(themeArg, result, options, context.ShowImages))
			return text
		},
	}
}

// orDot defaults an empty path to "." (pi: searchDir || ".").
func orDot(path string) string {
	if path == "" {
		return "."
	}
	return path
}

// killChild terminates the child process (pi: child.kill()).
func killChild(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// syncMap is a minimal string-keyed cache with mutex protection (pi: Map +
// event loop).
type syncMap struct {
	mu   sync.Mutex
	data map[string][]string
}

func (m *syncMap) Load(key string) ([]string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		return nil, false
	}
	v, ok := m.data[key]
	return v, ok
}

func (m *syncMap) Store(key string, value []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string][]string{}
	}
	m.data[key] = value
}

// ============================================================================
// Render segment (pi: grep.ts render declarations)
// ============================================================================

// formatGrepCall renders the call row (pi: formatGrepCall).
func formatGrepCall(theme UITheme, args map[string]any) string {
	pattern, patternNull := stringArg(args, "pattern")
	rawPathValue, hasPath := args["path"]
	pathNull := false
	path := ""
	if !hasPath || rawPathValue == nil {
		path = ShortenPath(".")
	} else if s, isStr := rawPathValue.(string); isStr {
		if s == "" {
			path = ShortenPath(".")
		} else {
			path = ShortenPath(s)
		}
	} else {
		pathNull = true
	}
	glob, _ := stringArg(args, "glob")
	var limit *float64
	if v, ok := args["limit"].(float64); ok {
		limit = &v
	}
	invalidArg := InvalidArgText(theme)
	text := theme.Fg("toolTitle", theme.Bold("grep")) + " "
	if patternNull {
		text += invalidArg
	} else {
		text += theme.Fg("accent", "/"+pattern+"/")
	}
	if pathNull {
		text += theme.Fg("toolOutput", " in "+invalidArg)
	} else {
		text += theme.Fg("toolOutput", " in "+path)
	}
	if glob != "" {
		text += theme.Fg("toolOutput", " ("+glob+")")
	}
	if limit != nil {
		text += theme.Fg("toolOutput", fmt.Sprintf(" limit %d", int64(*limit)))
	}
	return text
}

// formatGrepResult renders the result body (pi: formatGrepResult).
func formatGrepResult(theme UITheme, result ToolRenderResult, options ToolRenderResultOptions, showImages bool) string {
	output := strings.Trim(GetTextOutput(&result, showImages), " \t\n\r")
	text := ""
	if output != "" {
		lines := strings.Split(output, "\n")
		maxLines := 15
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

	var details *GrepToolDetails
	if result.Details != nil {
		details, _ = result.Details.(*GrepToolDetails)
	}
	var matchLimit *int
	var truncation *harnessutils.TruncationResult
	linesTruncated := false
	if details != nil {
		matchLimit = details.MatchLimitReached
		truncation = details.Truncation
		if details.LinesTruncated != nil {
			linesTruncated = *details.LinesTruncated
		}
	}
	if matchLimit != nil || (truncation != nil && truncation.Truncated) || linesTruncated {
		var warnings []string
		if matchLimit != nil {
			warnings = append(warnings, fmt.Sprintf("%d matches limit", *matchLimit))
		}
		if truncation != nil && truncation.Truncated {
			maxBytes := int64(harnessutils.DefaultMaxBytes)
			if truncation.MaxBytes != 0 {
				maxBytes = int64(truncation.MaxBytes)
			}
			warnings = append(warnings, harnessutils.FormatSize(maxBytes)+" limit")
		}
		if linesTruncated {
			warnings = append(warnings, "some lines truncated")
		}
		text += "\n" + theme.Fg("warning", "[Truncated: "+strings.Join(warnings, ", ")+"]")
	}
	return text
}

// GrepToolOptions parameterize the grep tool (pi: GrepToolOptions).
type GrepToolOptions struct {
	// Operations customizes grep. Default: local filesystem plus ripgrep.
	Operations *GrepOperations
}

// CreateGrepTool wraps the grep definition into an AgentTool (pi:
// createGrepTool).
func CreateGrepTool(cwd string, options *GrepToolOptions) Tool {
	return WrapToolDefinition(CreateGrepToolDefinition(cwd, options), nil)
}
