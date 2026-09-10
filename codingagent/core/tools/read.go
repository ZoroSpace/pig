// pi: src/core/tools/read.ts — the coding-agent read tool.
//
// Segment deviations:
//   - The render functions (formatReadCall/formatCompactReadCall/
//     formatReadResult, getCompactReadClassification and its pi-docs
//     classification via config.getReadmePath) depend on Theme/pi-tui and
//     arrive with the theme batch; their source positions are noted.
//   - execute's ExtensionContext → ToolExecuteContext (see tool_context.go).
//   - processImage → codingagent/utils (photon-backed resize takes pi's
//     photon-unavailable path until Phase 8).
//   - signal wiring → context.Context; the pre-abort check collapses to a
//     ctx.Err() probe, and the abort-listener pattern collapses to ctx
//     cancellation.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ZoroSpace/pig/agent"
	harnessutils "github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/ai/types"
	codingagent "github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// readSchemaJSON is the read tool's parameter schema (pi: readSchema).
const readSchemaJSON = `{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Path to the file to read (relative or absolute)"
    },
    "offset": {
      "type": "number",
      "description": "Line number to start reading from (1-indexed)"
    },
    "limit": {
      "type": "number",
      "description": "Maximum number of lines to read"
    }
  },
  "required": ["path"]
}`

// ReadToolSystemPromptContribution is the read tool's system prompt metadata
// (pi: readToolSystemPromptContribution).
var ReadToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet:    "Read file contents",
	Guidelines: []string{"Use read to examine files instead of cat or sed."},
}

// ReadToolInput is the read tool's parameter shape (pi: ReadToolInput).
type ReadToolInput struct {
	Path   string   `json:"path"`
	Offset *float64 `json:"offset,omitempty"`
	Limit  *float64 `json:"limit,omitempty"`
}

// ReadToolDetails carries the truncation outcome (pi: ReadToolDetails).
type ReadToolDetails struct {
	Truncation *harnessutils.TruncationResult `json:"truncation,omitempty"`
}

// ReadOperations are pluggable operations for the read tool; override these
// to delegate file reading to remote systems (pi: ReadOperations).
type ReadOperations struct {
	// ReadFile reads file contents (pi: readFile; Buffer → []byte).
	ReadFile func(ctx context.Context, absolutePath string) ([]byte, error)
	// Access checks the file is readable (pi: access; throws if not).
	Access func(ctx context.Context, absolutePath string) error
	// DetectImageMimeType detects the image MIME type; "" for non-images
	// (pi: detectImageMimeType; null/undefined → "").
	DetectImageMimeType func(ctx context.Context, absolutePath string) string
}

func defaultReadOperations() ReadOperations {
	return ReadOperations{
		ReadFile: func(ctx context.Context, path string) ([]byte, error) { return os.ReadFile(path) },
		Access: func(ctx context.Context, path string) error {
			info, err := os.Stat(path)
			if err != nil {
				return utils.FSAccessMessage(err, path)
			}
			if info.Mode().Perm()&0o400 == 0 {
				return fmt.Errorf("EACCES: permission denied, access '%s'", path)
			}
			return nil
		},
		DetectImageMimeType: func(ctx context.Context, absolutePath string) string {
			return utils.DetectSupportedImageMimeTypeFromFile(absolutePath)
		},
	}
}

// ReadToolOptions parameterize the read tool (pi: ReadToolOptions).
type ReadToolOptions struct {
	// AutoResizeImages auto-resizes images to 2000x2000 max. Default: true.
	AutoResizeImages *bool
	// Operations customizes file reading. Default: local filesystem.
	Operations *ReadOperations
}

// getNonVisionImageNote builds the note for models without image input (pi:
// getNonVisionImageNote); "" = undefined.
func getNonVisionImageNote(model *types.Model) string {
	if model == nil {
		return ""
	}
	for _, input := range model.Input {
		if input == types.ModelInputImage {
			return ""
		}
	}
	return "[Current model does not support images. The image will be omitted from this request.]"
}

// ============================================================================
// Render segment (pi: read.ts render declarations)
// ============================================================================

// compactReadClassification is pi's CompactReadClassification.
type compactReadClassification struct {
	kind  string // "docs" | "resource" | "skill"
	label string
}

// compactResourceFileNames is pi's COMPACT_RESOURCE_FILE_NAMES.
var compactResourceFileNames = map[string]bool{
	"AGENTS.override.md": true,
	"AGENTS.md":          true,
	"AGENTS.MD":          true,
	"CLAUDE.md":          true,
	"CLAUDE.MD":          true,
}

// readRenderArgs is pi's ReadRenderArgs over raw args.
type readRenderArgs struct {
	path   *string
	offset *float64
	limit  *float64
}

// readRenderArgsFromArgs extracts the render args (pi: args as
// ReadRenderArgs).
func readRenderArgsFromArgs(args any) readRenderArgs {
	out := readRenderArgs{}
	m, ok := args.(map[string]any)
	if !ok {
		return out
	}
	out.path = rawPathFromArgs(args)
	if v, has := m["offset"]; has && v != nil {
		if f, isNum := v.(float64); isNum {
			out.offset = &f
		}
	}
	if v, has := m["limit"]; has && v != nil {
		if f, isNum := v.(float64); isNum {
			out.limit = &f
		}
	}
	return out
}

// formatReadLineRange renders the :start-end suffix (pi: formatReadLineRange).
func formatReadLineRange(theme UITheme, args readRenderArgs) string {
	if args.offset == nil && args.limit == nil {
		return ""
	}
	startLine := 1.0
	if args.offset != nil {
		startLine = *args.offset
	}
	if args.limit == nil {
		return theme.Fg("warning", fmt.Sprintf(":%d", int64(startLine)))
	}
	endLine := startLine + *args.limit - 1
	return theme.Fg("warning", fmt.Sprintf(":%d-%d", int64(startLine), int64(endLine)))
}

// formatReadCall renders the call row (pi: formatReadCall).
func formatReadCall(theme UITheme, args readRenderArgs, cwd string) string {
	pathDisplay := RenderToolPath(args.path, theme, cwd, "")
	return theme.Fg("toolTitle", theme.Bold("read")) + " " + pathDisplay + formatReadLineRange(theme, args)
}

// trimTrailingEmptyLinesRead is read.ts's local trimTrailingEmptyLines
// (shared with write.ts in pi; pig reuses the write.go copy).

// toPosixPath converts separators to posix (pi: toPosixPath).
func toPosixPath(filePath string) string {
	return filepath.ToSlash(filePath)
}

// getPiDocsClassification classifies pi's own documentation files (pi:
// getPiDocsClassification; nil = undefined).
func getPiDocsClassification(absolutePath string) *compactReadClassification {
	packageRoot := filepath.Dir(codingagent.GetReadmePath())
	rootAbs, err := filepath.Abs(packageRoot)
	if err != nil {
		rootAbs = packageRoot
	}
	pathAbs, err := filepath.Abs(absolutePath)
	if err != nil {
		pathAbs = absolutePath
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return nil
	}
	rel = filepath.ToSlash(rel)
	if rel == "" || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return nil
	}

	if rel == "README.md" || strings.HasPrefix(rel, "docs/") || strings.HasPrefix(rel, "examples/") {
		return &compactReadClassification{kind: "docs", label: rel}
	}
	return nil
}

// getCompactReadClassification resolves the compact call display (pi:
// getCompactReadClassification; nil = undefined).
func getCompactReadClassification(args readRenderArgs, cwd string) *compactReadClassification {
	if args.path == nil || *args.path == "" {
		return nil
	}
	rawPath := *args.path

	absolutePath := ResolveToCwd(rawPath, cwd)
	fileName := filepath.Base(absolutePath)
	if fileName == "SKILL.md" {
		label := filepath.Base(filepath.Dir(absolutePath))
		if label == "" || label == "." || label == string(filepath.Separator) {
			label = fileName
		}
		return &compactReadClassification{kind: "skill", label: label}
	}

	if docsClassification := getPiDocsClassification(absolutePath); docsClassification != nil {
		return docsClassification
	}

	if compactResourceFileNames[fileName] {
		return &compactReadClassification{kind: "resource", label: utils.FormatPathRelativeToCwdOrAbsolute(absolutePath, cwd)}
	}

	return nil
}

// formatCompactReadCall renders the collapsed call row (pi:
// formatCompactReadCall).
func formatCompactReadCall(theme UITheme, classification compactReadClassification, args readRenderArgs) string {
	expandHint := theme.Fg("dim", " ("+KeyText("app.tools.expand")+" to expand)")
	if classification.kind == "skill" {
		return theme.Fg("customMessageLabel", "\x1b[1m[skill]\x1b[22m ") +
			theme.Fg("customMessageText", classification.label) +
			formatReadLineRange(theme, args) +
			expandHint
	}

	return theme.Fg("toolTitle", theme.Bold("read "+classification.kind)) +
		" " +
		theme.Fg("accent", classification.label) +
		formatReadLineRange(theme, args) +
		expandHint
}

// formatReadResult renders the result body (pi: formatReadResult; "" =
// collapsed).
func formatReadResult(
	theme UITheme,
	args readRenderArgs,
	result ToolRenderResult,
	options ToolRenderResultOptions,
	showImages bool,
	isError bool,
) string {
	if !options.Expanded && !isError {
		return ""
	}

	var rawPath *string
	if args.path != nil {
		rawPath = args.path
	}
	output := GetTextOutput(&result, showImages)
	lang := ""
	if !isError && rawPath != nil {
		lang = themeGetLanguageFromPath(*rawPath)
	}
	var renderedLines []string
	if lang != "" {
		renderedLines = themeHighlightCode(ReplaceTabs(output), lang)
	} else {
		renderedLines = strings.Split(output, "\n")
	}
	lines := trimTrailingEmptyLines(renderedLines)
	maxLines := 10
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
		if lang != "" {
			styled = append(styled, ReplaceTabs(line))
		} else {
			styled = append(styled, theme.Fg("toolOutput", ReplaceTabs(line)))
		}
	}
	text := "\n" + strings.Join(styled, "\n")
	if remaining > 0 {
		text += theme.Fg("muted", fmt.Sprintf("\n... (%d more lines,", remaining)) +
			" " + KeyHint("app.tools.expand", "to expand") + theme.Fg("muted", ")")
	}

	var details *ReadToolDetails
	if result.Details != nil {
		details, _ = result.Details.(*ReadToolDetails)
	}
	if details != nil && details.Truncation != nil && details.Truncation.Truncated {
		truncation := details.Truncation
		if truncation.FirstLineExceedsLimit {
			maxBytes := int64(harnessutils.DefaultMaxBytes)
			if truncation.MaxBytes != 0 {
				maxBytes = int64(truncation.MaxBytes)
			}
			text += "\n" + theme.Fg("warning", fmt.Sprintf("[First line exceeds %s limit]", harnessutils.FormatSize(maxBytes)))
		} else if truncation.TruncatedBy == "lines" {
			maxLinesLimit := harnessutils.DefaultMaxLines
			if truncation.MaxLines != 0 {
				maxLinesLimit = truncation.MaxLines
			}
			text += "\n" + theme.Fg("warning", fmt.Sprintf("[Truncated: showing %d of %d lines (%d line limit)]", truncation.OutputLines, truncation.TotalLines, maxLinesLimit))
		} else {
			maxBytes := int64(harnessutils.DefaultMaxBytes)
			if truncation.MaxBytes != 0 {
				maxBytes = int64(truncation.MaxBytes)
			}
			text += "\n" + theme.Fg("warning", fmt.Sprintf("[Truncated: %d lines shown (%s limit)]", truncation.OutputLines, harnessutils.FormatSize(maxBytes)))
		}
	}
	return text
}

// CreateReadToolDefinition builds the read tool definition (pi:
// createReadToolDefinition).
func CreateReadToolDefinition(cwd string, options *ReadToolOptions) *ToolDefinition {
	autoResizeImages := options == nil || options.AutoResizeImages == nil || *options.AutoResizeImages
	ops := defaultReadOperations()
	if options != nil && options.Operations != nil {
		ops = *options.Operations
	}
	return &ToolDefinition{
		Name:             "read",
		Label:            "read",
		Description:      fmt.Sprintf("Read the contents of a file. Supports text files and images (jpg, png, gif, webp, bmp). Images are sent as attachments. For text files, output is truncated to %d lines or %dKB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.", harnessutils.DefaultMaxLines, harnessutils.DefaultMaxBytes/1024),
		PromptSnippet:    ReadToolSystemPromptContribution.Snippet,
		PromptGuidelines: append([]string{}, ReadToolSystemPromptContribution.Guidelines...),
		Parameters:       json.RawMessage(readSchemaJSON),
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx ToolExecuteContext) (agent.AgentToolResult, error) {
			input, err := decodeToolParams[ReadToolInput](params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			if ctx.Err() != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Operation aborted")
			}

			absolutePath := ResolveReadPathAsync(ctx, input.Path, cwd)
			if ctx.Err() != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Operation aborted")
			}
			// Check if file exists and is readable.
			if err := ops.Access(ctx, absolutePath); err != nil {
				return agent.AgentToolResult{}, err
			}
			if ctx.Err() != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Operation aborted")
			}
			var mimeType string
			if ops.DetectImageMimeType != nil {
				mimeType = ops.DetectImageMimeType(ctx, absolutePath)
			}
			var content []types.Content
			var details *ReadToolDetails
			var currentModel *types.Model
			if toolCtx != nil {
				currentModel = toolCtx.GetModel()
			}
			nonVisionImageNote := getNonVisionImageNote(currentModel)
			if mimeType != "" {
				// Read image as binary.
				buffer, err := ops.ReadFile(ctx, absolutePath)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				processed := utils.ProcessImage(buffer, mimeType, &utils.ProcessImageOptions{AutoResizeImages: &autoResizeImages})
				if !processed.OK {
					textNote := fmt.Sprintf("Read image file [%s]\n%s", mimeType, processed.Message)
					if nonVisionImageNote != "" {
						textNote += "\n" + nonVisionImageNote
					}
					content = []types.Content{&types.TextContent{Kind: types.KindText, Text: textNote}}
				} else {
					textNote := fmt.Sprintf("Read image file [%s]", processed.MimeType)
					if len(processed.Hints) > 0 {
						for _, hint := range processed.Hints {
							textNote += "\n" + hint
						}
					}
					if nonVisionImageNote != "" {
						textNote += "\n" + nonVisionImageNote
					}
					content = []types.Content{
						&types.TextContent{Kind: types.KindText, Text: textNote},
						&types.ImageContent{Kind: types.KindImage, Data: processed.Data, MimeType: processed.MimeType},
					}
				}
			} else {
				// Read text content.
				buffer, err := ops.ReadFile(ctx, absolutePath)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				textContent := string(buffer)
				allLines := splitLines(textContent)
				totalFileLines := len(allLines)
				// Apply offset if specified. Convert from 1-indexed input to
				// 0-indexed array access.
				startLine := 0
				if input.Offset != nil && *input.Offset != 0 {
					startLine = int(*input.Offset) - 1
					if startLine < 0 {
						startLine = 0
					}
				}
				startLineDisplay := startLine + 1
				// Check if offset is out of bounds.
				if startLine >= len(allLines) {
					return agent.AgentToolResult{}, fmt.Errorf("Offset %s is beyond end of file (%d lines total)", formatOptionalNumber(input.Offset), len(allLines))
				}
				var selectedContent string
				userLimitedLines := -1
				// If limit is specified by the user, honor it first.
				// Otherwise truncateHead decides.
				if input.Limit != nil {
					endLine := startLine + int(*input.Limit)
					if endLine > len(allLines) {
						endLine = len(allLines)
					}
					selectedContent = joinLines(allLines[startLine:endLine])
					userLimitedLines = endLine - startLine
				} else {
					selectedContent = joinLines(allLines[startLine:])
				}
				// Apply truncation, respecting both line and byte limits.
				truncation := harnessutils.TruncateHead(selectedContent, nil)
				var outputText string
				if truncation.FirstLineExceedsLimit {
					// First line alone exceeds the byte limit. Point the
					// model at a bash fallback.
					firstLineSize := harnessutils.FormatSize(int64(len(allLines[startLine])))
					outputText = fmt.Sprintf("[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]",
						startLineDisplay, firstLineSize, harnessutils.FormatSize(harnessutils.DefaultMaxBytes), startLineDisplay, input.Path, harnessutils.DefaultMaxBytes)
					details = &ReadToolDetails{Truncation: &truncation}
				} else if truncation.Truncated {
					// Truncation occurred. Build an actionable continuation
					// notice.
					endLineDisplay := startLineDisplay + truncation.OutputLines - 1
					nextOffset := endLineDisplay + 1
					outputText = truncation.Content
					if truncation.TruncatedBy == harnessutils.TruncatedByLines {
						outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]",
							startLineDisplay, endLineDisplay, totalFileLines, nextOffset)
					} else {
						outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue.]",
							startLineDisplay, endLineDisplay, totalFileLines, harnessutils.FormatSize(harnessutils.DefaultMaxBytes), nextOffset)
					}
					details = &ReadToolDetails{Truncation: &truncation}
				} else if userLimitedLines != -1 && startLine+userLimitedLines < len(allLines) {
					// User-specified limit stopped early, but the file still
					// has more content.
					remaining := len(allLines) - (startLine + userLimitedLines)
					nextOffset := startLine + userLimitedLines + 1
					outputText = fmt.Sprintf("%s\n\n[%d more lines in file. Use offset=%d to continue.]", truncation.Content, remaining, nextOffset)
				} else {
					// No truncation and no remaining user-limited content.
					outputText = truncation.Content
				}
				content = []types.Content{&types.TextContent{Kind: types.KindText, Text: outputText}}
			}

			if ctx.Err() != nil {
				return agent.AgentToolResult{}, fmt.Errorf("Operation aborted")
			}
			var detailsValue any
			if details != nil {
				detailsValue = details
			}
			return agent.AgentToolResult{Content: content, Details: detailsValue}, nil
		},
		RenderCall: func(args any, themeArg UITheme, context ToolRenderContext) tui.Component {
			var text *tui.Text
			if c, ok := context.LastComponent.(*tui.Text); ok {
				text = c
			} else {
				text = tui.NewText("", 0, 0)
			}
			renderArgs := readRenderArgsFromArgs(args)
			var classification *compactReadClassification
			if !context.Expanded {
				classification = getCompactReadClassification(renderArgs, context.Cwd)
			}
			if classification != nil {
				text.SetText(formatCompactReadCall(themeArg, *classification, renderArgs))
			} else {
				text.SetText(formatReadCall(themeArg, renderArgs, context.Cwd))
			}
			return text
		},
		RenderResult: func(result ToolRenderResult, options ToolRenderResultOptions, themeArg UITheme, context ToolRenderContext) tui.Component {
			var text *tui.Text
			if c, ok := context.LastComponent.(*tui.Text); ok {
				text = c
			} else {
				text = tui.NewText("", 0, 0)
			}
			text.SetText(formatReadResult(themeArg, readRenderArgsFromArgs(context.Args), result, options, context.ShowImages, context.IsError))
			return text
		},
	}
}

// CreateReadTool wraps the read definition into an AgentTool (pi:
// createReadTool).
func CreateReadTool(cwd string, options *ReadToolOptions) Tool {
	return WrapToolDefinition(CreateReadToolDefinition(cwd, options), nil)
}
