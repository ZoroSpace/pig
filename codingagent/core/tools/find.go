// pi: src/core/tools/find.ts — the find tool (streaming fd with glob
// patterns, gitignore-aware behavior, and result limits).
//
// Segment deviations:
//   - The render functions live in renderers_*.go (S5a: pi moved them to
//     core/tools/renderers/; pig keeps package tools, see the file header
//     there).
//   - node child_process + readline streaming → the shared
//     codingagent/utils spawn helpers with a bufio scanner.
//   - signal wiring → context.Context (abort kills the child and rejects
//     with "Operation aborted").
//   - The windows full-path separator rewrite ([/\\]) applies only on
//     runtime.GOOS == "windows" (pi: process.platform).
package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ZoroSpace/pig/agent"
	harnessutils "github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// findSchemaJSON is the find tool's parameter schema (pi: findSchema).
const findSchemaJSON = `{
  "type": "object",
  "properties": {
    "pattern": {
      "type": "string",
      "description": "Glob pattern to match files, e.g. '*.ts', '**/*.json', or 'src/**/*.spec.ts'"
    },
    "path": {
      "type": "string",
      "description": "Directory to search in (default: current directory)"
    },
    "limit": {
      "type": "number",
      "description": "Maximum number of results (default: 1000)"
    }
  },
  "required": ["pattern"]
}`

// FindToolSystemPromptContribution is the find tool's system prompt metadata
// (pi: findToolSystemPromptContribution).
var FindToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet:    "Find files by glob pattern (respects .gitignore)",
	Guidelines: nil,
}

// FindToolInput is the find tool's parameter shape (pi: FindToolInput).
type FindToolInput struct {
	Pattern string   `json:"pattern"`
	Path    string   `json:"path,omitempty"`
	Limit   *float64 `json:"limit,omitempty"`
}

// FindToolDetails carries the find outcome metadata (pi: FindToolDetails).
type FindToolDetails struct {
	Truncation         *harnessutils.TruncationResult `json:"truncation,omitempty"`
	ResultLimitReached *int                           `json:"resultLimitReached,omitempty"`
}

// findDefaultLimit is pi's DEFAULT_LIMIT.
const findDefaultLimit = 1000

// RelativizeFindResultPath relativizes a find result against the search root
// and normalizes it to posix separators (pi: relativizeFindResultPath).
func RelativizeFindResultPath(resultPath string, searchPath string) string {
	hadTrailingSeparator := strings.HasSuffix(resultPath, string(filepath.Separator)) ||
		(filepath.Separator == '\\' && strings.HasSuffix(resultPath, "/"))
	relativePath := resultPath
	if filepath.IsAbs(resultPath) {
		if rel, err := filepath.Rel(searchPath, resultPath); err == nil {
			relativePath = rel
		}
	}
	posixPath := filepath.ToSlash(relativePath)
	if hadTrailingSeparator && !strings.HasSuffix(posixPath, "/") {
		return posixPath + "/"
	}
	return posixPath
}

// FindOperations are pluggable operations for the find tool; override these
// to delegate file search to remote systems (pi: FindOperations).
type FindOperations struct {
	// Exists checks if path exists.
	Exists func(ctx context.Context, absolutePath string) (bool, error)
	// Glob finds files matching the glob pattern. Returns relative or
	// absolute paths.
	Glob func(ctx context.Context, pattern string, cwd string, options FindGlobOptions) ([]string, error)
}

// FindGlobOptions carries the glob invocation options (pi: the options
// argument of FindOperations.glob).
type FindGlobOptions struct {
	Ignore []string
	Limit  int
}

func defaultFindOperations() FindOperations {
	return FindOperations{
		Exists: func(ctx context.Context, path string) (bool, error) {
			return fileExists(path), nil
		},
		// This is a placeholder. Actual fd execution happens in execute()
		// when no custom glob is provided.
		Glob: func(ctx context.Context, pattern string, cwd string, options FindGlobOptions) ([]string, error) {
			return nil, nil
		},
	}
}

// FindToolOptions parameterize the find tool (pi: FindToolOptions).
type FindToolOptions struct {
	// Operations customizes find. Default: local filesystem plus fd.
	Operations *FindOperations
}

// buildFindNotices assembles the shared limit/truncation notices and details
// (pi: the two inline notice blocks).
func buildFindNotices(rawOutput string, effectiveLimit int, resultLimitReached bool, limitSuffix string) (string, *FindToolDetails) {
	maxLines := int(^uint(0) >> 1)
	truncation := harnessutils.TruncateHead(rawOutput, &harnessutils.TruncationOptions{MaxLines: &maxLines})
	resultOutput := truncation.Content
	var details FindToolDetails
	var notices []string
	if resultLimitReached {
		notices = append(notices, fmt.Sprintf("%d results limit reached%s", effectiveLimit, limitSuffix))
		details.ResultLimitReached = &effectiveLimit
	}
	if truncation.Truncated {
		notices = append(notices, fmt.Sprintf("%s limit reached", harnessutils.FormatSize(harnessutils.DefaultMaxBytes)))
		details.Truncation = &truncation
	}
	if len(notices) > 0 {
		resultOutput += "\n\n[" + strings.Join(notices, ". ") + "]"
	}
	var detailsValue *FindToolDetails
	if details.ResultLimitReached != nil || details.Truncation != nil {
		detailsValue = &details
	}
	return resultOutput, detailsValue
}

// CreateFindToolDefinition builds the find tool definition (pi:
// createFindToolDefinition; renderCall/renderResult arrive with the theme
// batch).
func CreateFindToolDefinition(cwd string, options *FindToolOptions) *ToolDefinition {
	var customOps *FindOperations
	if options != nil && options.Operations != nil {
		customOps = options.Operations
	}
	return &ToolDefinition{
		Name:          "find",
		Label:         "find",
		Description:   fmt.Sprintf("Search for files by glob pattern. Returns matching file paths relative to the search directory. Respects .gitignore. Output is truncated to %d results or %dKB (whichever is hit first).", findDefaultLimit, harnessutils.DefaultMaxBytes/1024),
		PromptSnippet: FindToolSystemPromptContribution.Snippet,
		Parameters:    json.RawMessage(findSchemaJSON),
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx ToolExecuteContext) (agent.AgentToolResult, error) {
			input, err := decodeToolParams[FindToolInput](params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			if ctx.Err() != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Operation aborted")
			}
			abortErr := func() error {
				if ctx.Err() != nil {
					return fmt.Errorf("Operation aborted")
				}
				return nil
			}

			searchPath := ResolveToCwd(orDot(input.Path), executeCwd(toolCtx, cwd))
			effectiveLimit := findDefaultLimit
			if input.Limit != nil {
				effectiveLimit = int(*input.Limit)
			}
			ops := defaultFindOperations()
			if customOps != nil {
				ops = *customOps
			}

			// If custom operations provide glob(), use that instead of fd.
			if customOps != nil && customOps.Glob != nil {
				exists, err := ops.Exists(ctx, searchPath)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				if !exists {
					return agent.AgentToolResult{}, fmt.Errorf("Path not found: %s", searchPath)
				}
				if err := abortErr(); err != nil {
					return agent.AgentToolResult{}, err
				}
				results, err := ops.Glob(ctx, input.Pattern, searchPath, FindGlobOptions{
					Ignore: []string{"**/node_modules/**", "**/.git/**"},
					Limit:  effectiveLimit,
				})
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				if err := abortErr(); err != nil {
					return agent.AgentToolResult{}, err
				}
				if len(results) == 0 {
					return resultOf([]types.Content{asTextContent("No files found matching pattern")}, nil), nil
				}

				// Relativize paths against the search root for stable output.
				relativized := make([]string, len(results))
				for i, p := range results {
					relativized[i] = RelativizeFindResultPath(p, searchPath)
				}
				resultLimitReached := len(relativized) >= effectiveLimit
				rawOutput := strings.Join(relativized, "\n")
				resultOutput, detailsValue := buildFindNotices(rawOutput, effectiveLimit, resultLimitReached, "")
				return resultOf([]types.Content{asTextContent(resultOutput)}, detailsValue), nil
			}

			// Default implementation uses fd.
			fdPath := utils.EnsureTool(ctx, "fd", nil)
			if err := abortErr(); err != nil {
				return agent.AgentToolResult{}, err
			}
			if fdPath == "" {
				return agent.AgentToolResult{}, fmt.Errorf("fd is not available and could not be downloaded")
			}

			args := []string{"--glob", "--color=never", "--hidden"}

			// fd normally ignores .gitignore outside git repos, so keep
			// --no-require-git there. Inside repos, use fd's default
			// git-aware behavior so parent .gitignore rules stop at nested
			// repo boundaries (pi#5960).
			insideGitRepo := false
			for current := searchPath; ; {
				if fileExists(filepath.Join(current, ".git")) {
					insideGitRepo = true
					break
				}
				parent := filepath.Dir(current)
				if parent == current {
					break
				}
				current = parent
			}
			if !insideGitRepo {
				args = append(args, "--no-require-git")
			}
			args = append(args, "--max-results", fmt.Sprintf("%d", effectiveLimit))

			// fd --glob matches against the basename unless --full-path is
			// set; in --full-path mode it matches against the absolute
			// candidate path, so a path-containing pattern like
			// 'src/**/*.spec.ts' needs a leading '**/' to match anything.
			effectivePattern := input.Pattern
			if strings.Contains(input.Pattern, "/") {
				args = append(args, "--full-path")
				if !strings.HasPrefix(input.Pattern, "/") && !strings.HasPrefix(input.Pattern, "**/") && input.Pattern != "**" {
					effectivePattern = "**/" + input.Pattern
				}
				// fd matches full paths using native separators on Windows.
				if runtime.GOOS == "windows" {
					effectivePattern = strings.ReplaceAll(effectivePattern, "/", `[/\\]`)
				}
			}
			args = append(args, "--", effectivePattern, searchPath)

			cmd, pipes, err := utils.SpawnProcess(fdPath, args, "", os.Environ())
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Failed to run fd: %s", err.Error())
			}
			// pi's spawn starts the child immediately; StartChild wraps
			// cmd.Start plus the parent write-end release.
			if _, err := utils.StartChild(cmd, pipes); err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Failed to run fd: %s", err.Error())
			}
			stderr := ""
			stderrDone := make(chan struct{})
			go func() {
				defer close(stderrDone)
				scanner := bufio.NewScanner(pipes.StderrRead)
				scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
				for scanner.Scan() {
					stderr += scanner.Text()
				}
			}()

			var lines []string
			childDone := make(chan struct{})
			var closeCode int
			go func() {
				defer close(childDone)
				scanner := bufio.NewScanner(pipes.StdoutRead)
				scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
				for scanner.Scan() {
					lines = append(lines, scanner.Text())
				}
				_ = cmd.Wait()
				if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
					closeCode = cmd.ProcessState.ExitCode()
				}
			}()

			select {
			case <-childDone:
			case <-ctx.Done():
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				<-childDone
				return agent.AgentToolResult{}, fmt.Errorf("Operation aborted")
			}
			// pi's child 'close' event fires only after both stdio streams
			// have ended, so stderr is fully collected before it is read;
			// join the stderr reader to reproduce that guarantee.
			<-stderrDone
			pipes.CloseReadEnds()

			if err := abortErr(); err != nil {
				return agent.AgentToolResult{}, err
			}
			var rawLines []string
			for _, rawLine := range lines {
				line := strings.TrimRight(rawLine, "\r")
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				rawLines = append(rawLines, line)
			}
			output := strings.Join(rawLines, "\n")
			if closeCode != 0 {
				errorMsg := strings.TrimSpace(stderr)
				if errorMsg == "" {
					errorMsg = fmt.Sprintf("fd exited with code %d", closeCode)
				}
				if output == "" {
					return agent.AgentToolResult{}, fmt.Errorf("%s", errorMsg)
				}
			}
			if output == "" {
				return resultOf([]types.Content{asTextContent("No files found matching pattern")}, nil), nil
			}

			relativized := make([]string, 0, len(rawLines))
			for _, line := range rawLines {
				relativized = append(relativized, RelativizeFindResultPath(line, searchPath))
			}
			resultLimitReached := len(relativized) >= effectiveLimit
			rawOutput := strings.Join(relativized, "\n")
			resultOutput, detailsValue := buildFindNotices(rawOutput, effectiveLimit, resultLimitReached,
				fmt.Sprintf(". Use limit=%d for more, or refine pattern", effectiveLimit*2))
			return resultOf([]types.Content{asTextContent(resultOutput)}, detailsValue), nil
		},
		// pi: ...findRenderers.
		RenderCall:   FindRenderers.RenderCall,
		RenderResult: FindRenderers.RenderResult,
	}
}

// CreateFindTool wraps the find definition into an AgentTool (pi:
// createFindTool).
func CreateFindTool(cwd string, options *FindToolOptions) Tool {
	return WrapToolDefinition(CreateFindToolDefinition(cwd, options), nil)
}
