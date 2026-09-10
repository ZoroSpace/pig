// pi: src/core/tools/edit.ts — the coding-agent edit tool.
//
// Segment deviations:
//   - The render functions and preview component plumbing depend on
//     Theme/pi-tui and arrive with the theme batch.
//   - signal wiring → context.Context (the throwIfAborted probes observe the
//     same aborts while keeping the mutation queue locked, per the TS
//     comment).
//   - editAccessError formats node errno codes; pig surfaces the os error's
//     wrapped errno.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// replaceEditSchemaJSON is one targeted replacement (pi: replaceEditSchema).
const replaceEditSchemaJSON = `{
  "type": "object",
  "properties": {
    "oldText": {
      "type": "string",
      "description": "Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."
    },
    "newText": {
      "type": "string",
      "description": "Replacement text for this targeted edit."
    }
  },
  "required": ["oldText", "newText"]
}`

// editSchemaJSON is the edit tool's parameter schema (pi: editSchema).
const editSchemaJSON = `{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Path to the file to edit (relative or absolute)"
    },
    "edits": {
      "type": "array",
      "items": ` + replaceEditSchemaJSON + `,
      "description": "One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead."
    }
  },
  "required": ["path", "edits"]
}`

// EditToolSystemPromptContribution is the edit tool's system prompt metadata
// (pi: editToolSystemPromptContribution).
var EditToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet: "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
	Guidelines: []string{
		"Use edit for precise changes (edits[].oldText must match exactly)",
		"When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
		"Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
		"Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
	},
}

// EditToolInput is the edit tool's parameter shape (pi: EditToolInput).
type EditToolInput struct {
	Path  string `json:"path"`
	Edits []Edit `json:"edits"`
}

// EditToolDetails carries the diff artifacts (pi: EditToolDetails).
type EditToolDetails struct {
	// Diff is the display-oriented diff of the changes made.
	Diff string
	// Patch is the standard unified patch of the changes made.
	Patch string
	// FirstChangedLine is the line number of the first change in the new
	// file (for editor navigation); nil = pi's undefined.
	FirstChangedLine *int
}

// EditOperations are pluggable operations for the edit tool; override these
// to delegate file editing to remote systems (pi: EditOperations).
type EditOperations struct {
	// ReadFile reads file contents (pi: readFile; Buffer → []byte).
	ReadFile func(ctx context.Context, absolutePath string) ([]byte, error)
	// WriteFile writes content to a file.
	WriteFile func(ctx context.Context, absolutePath string, content string) error
	// Access checks the file is readable and writable (pi: access).
	Access func(ctx context.Context, absolutePath string) error
}

func defaultEditOperations() EditOperations {
	return EditOperations{
		ReadFile: func(ctx context.Context, path string) ([]byte, error) { return os.ReadFile(path) },
		WriteFile: func(ctx context.Context, path string, content string) error {
			return os.WriteFile(path, []byte(content), 0o644)
		},
		Access: func(ctx context.Context, path string) error {
			if _, err := os.Stat(path); err != nil {
				return codingagentutils.FSAccessMessage(err, path)
			}
			// pi: fsAccess(R_OK | W_OK) — probe the write permission the
			// way access(2) would.
			file, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				if code := codingagentutils.NodeErrCode(err); code != "" {
					return &codingagentutils.NodeErr{Code: code, Message: fmt.Sprintf("%s: %s, open '%s'", code, underlyingErrText(err), path), Err: err}
				}
				return err
			}
			file.Close()
			return nil
		},
	}
}

// EditToolOptions parameterize the edit tool (pi: EditToolOptions).
type EditToolOptions struct {
	// Operations customizes file editing. Default: local filesystem.
	Operations *EditOperations
}

// ============================================================================
// Render segment (pi: edit.ts render declarations)
// ============================================================================

// EditPreview is the preview union (pi: EditPreview = EditDiffResult |
// EditDiffError).
type EditPreview struct {
	// Diff-result form (DiffString; ignored in error form).
	Diff             string
	FirstChangedLine *int
	// Error form (pi: "error" in preview).
	IsErrorForm bool
	Error       string
}

// EditRenderState is the renderer state (pi: EditRenderState).
type EditRenderState struct {
	CallComponent *EditCallRenderComponent
}

// EditCallRenderComponent is the call component (pi: EditCallRenderComponent
// = Box & { preview?, previewArgsKey?, previewPending?, settledError? }).
// The preview fields are guarded: renderCall and the async preview
// goroutine touch them from different goroutines.
type EditCallRenderComponent struct {
	*tui.Box
	mu             sync.Mutex
	preview        *EditPreview
	previewArgsKey *string
	previewPending bool
	settledError   bool
}

func (c *EditCallRenderComponent) snapshot() (preview *EditPreview, argsKey *string, pending bool, settled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.preview, c.previewArgsKey, c.previewPending, c.settledError
}

func newEditCallRenderComponent() *EditCallRenderComponent {
	return &EditCallRenderComponent{Box: tui.NewBox(1, 1, func(text string) string { return text })}
}

// getEditCallRenderComponent resolves the call component (pi:
// getEditCallRenderComponent; pi's `lastComponent instanceof Box` check
// becomes a concrete-type check since pig wraps the Box instead of
// Object.assign-ing onto it).
func getEditCallRenderComponent(state *EditRenderState, lastComponent tui.Component) *EditCallRenderComponent {
	if c, ok := lastComponent.(*EditCallRenderComponent); ok {
		state.CallComponent = c
		return c
	}
	if state.CallComponent != nil {
		return state.CallComponent
	}
	component := newEditCallRenderComponent()
	state.CallComponent = component
	return component
}

// renderableEditInput is the args subset the preview can act on (pi:
// RenderableEditArgs).
type renderableEditInput struct {
	path  string
	edits []Edit
}

// getRenderablePreviewInput extracts the preview input from raw args (pi:
// getRenderablePreviewInput; null → nil).
func getRenderablePreviewInput(args any) *renderableEditInput {
	m, ok := args.(map[string]any)
	if !ok {
		return nil
	}

	pathValue, hasPath := m["path"]
	if filePathValue, has := m["file_path"]; has {
		pathValue, hasPath = filePathValue, has
	}
	path, pathIsString := pathValue.(string)
	if !hasPath || !pathIsString || path == "" {
		return nil
	}

	// edits: array of {oldText, newText} objects (raw JSON or decoded).
	if rawEdits, isArray := m["edits"].([]any); isArray && len(rawEdits) > 0 {
		edits := make([]Edit, 0, len(rawEdits))
		valid := true
		for _, rawEdit := range rawEdits {
			editMap, isMap := rawEdit.(map[string]any)
			if !isMap {
				valid = false
				break
			}
			oldText, oldOk := editMap["oldText"].(string)
			newText, newOk := editMap["newText"].(string)
			if !oldOk || !newOk {
				valid = false
				break
			}
			edits = append(edits, Edit{OldText: oldText, NewText: newText})
		}
		if valid {
			return &renderableEditInput{path: path, edits: edits}
		}
	}

	// Legacy single-edit shape (pi: oldText/newText top-level).
	if oldText, oldOk := m["oldText"].(string); oldOk {
		if newText, newOk := m["newText"].(string); newOk {
			return &renderableEditInput{path: path, edits: []Edit{{OldText: oldText, NewText: newText}}}
		}
	}

	return nil
}

// formatEditCall renders the call header (pi: formatEditCall).
func formatEditCall(theme UITheme, args any, cwd string) string {
	pathDisplay := renderToolPathFromArgs(theme, args, cwd)
	return theme.Fg("toolTitle", theme.Bold("edit")) + " " + pathDisplay
}

// rawPathFromArgs is str(args?.file_path ?? args?.path) over raw args.
func rawPathFromArgs(args any) *string {
	m, ok := args.(map[string]any)
	if !ok {
		return nil
	}
	value, has := m["path"]
	if filePathValue, is := m["file_path"]; is && filePathValue != nil {
		// pi: args?.file_path ?? args?.path (?? skips null/undefined)
		value, has = filePathValue, true
	}
	if !has {
		return nil
	}
	if s, isString := value.(string); isString {
		return &s
	}
	if value == nil {
		empty := ""
		return &empty
	}
	return nil
}

// renderToolPathFromArgs is renderToolPath(str(args?.file_path ??
// args?.path), theme, cwd).
func renderToolPathFromArgs(theme UITheme, args any, cwd string) string {
	return RenderToolPath(rawPathFromArgs(args), theme, cwd, "")
}

// formatEditResult renders the settled result body (pi: formatEditResult;
// "" = undefined).
func formatEditResult(theme UITheme, args any, preview *EditPreview, result ToolRenderResult, isError bool) string {
	rawPath := rawPathFromArgs(args)
	var previewDiff string
	var previewError string
	if preview != nil {
		if preview.IsErrorForm {
			previewError = preview.Error
		} else {
			previewDiff = preview.Diff
		}
	}
	if isError {
		var errorTexts []string
		for _, c := range result.Content {
			if t, ok := c.(types.TextContent); ok {
				errorTexts = append(errorTexts, t.Text)
			}
		}
		errorText := strings.Join(errorTexts, "\n")
		if errorText == "" || errorText == previewError {
			return ""
		}
		return theme.Fg("error", errorText)
	}

	resultDiff := ""
	if !isError && result.Details != nil {
		if details, ok := result.Details.(*EditToolDetails); ok && details.Diff != "" {
			resultDiff = details.Diff
		}
	}
	filePath := ""
	if rawPath != nil {
		filePath = *rawPath
	}
	if resultDiff != "" && resultDiff != previewDiff {
		return RenderDiff(theme, resultDiff, RenderDiffOptions{FilePath: filePath})
	}

	return ""
}

// getEditHeaderBg resolves the header background (pi: getEditHeaderBg).
func getEditHeaderBg(theme UITheme, preview *EditPreview, settledError bool) func(string) string {
	if preview != nil {
		if preview.IsErrorForm {
			return func(text string) string { return theme.Bg("toolErrorBg", text) }
		}
		return func(text string) string { return theme.Bg("toolSuccessBg", text) }
	}
	if settledError {
		return func(text string) string { return theme.Bg("toolErrorBg", text) }
	}
	return func(text string) string { return theme.Bg("toolPendingBg", text) }
}

// buildEditCallComponent repopulates the call component (pi:
// buildEditCallComponent).
func buildEditCallComponent(theme UITheme, component *EditCallRenderComponent, args any, cwd string) *EditCallRenderComponent {
	preview, _, _, settled := component.snapshot()
	component.SetBgFn(getEditHeaderBg(theme, preview, settled))
	component.Clear()
	component.AddChild(tui.NewText(formatEditCall(theme, args, cwd), 0, 0))

	if preview == nil {
		return component
	}

	var body string
	if preview.IsErrorForm {
		body = theme.Fg("error", preview.Error)
	} else {
		body = RenderDiff(theme, preview.Diff, RenderDiffOptions{})
	}
	component.AddChild(tui.NewSpacer(1))
	component.AddChild(tui.NewText(body, 0, 0))
	return component
}

// setEditPreview installs a preview and reports whether the content changed
// (pi: setEditPreview).
func setEditPreview(component *EditCallRenderComponent, preview EditPreview, argsKey *string) bool {
	component.mu.Lock()
	defer component.mu.Unlock()
	current := component.preview
	changed := false
	if current == nil {
		changed = true
	} else if current.IsErrorForm && preview.IsErrorForm {
		changed = current.Error != preview.Error
	} else if current.IsErrorForm != preview.IsErrorForm {
		changed = true
	} else if !current.IsErrorForm && !preview.IsErrorForm {
		changed = current.Diff != preview.Diff || !sameIntPtr(current.FirstChangedLine, preview.FirstChangedLine)
	}
	component.preview = &preview
	component.previewArgsKey = argsKey
	component.previewPending = false
	return changed
}

// sameIntPtr compares optional line numbers (pi: === on number | undefined).
func sameIntPtr(a *int, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// previewArgsKeyFor computes the args identity key (pi: JSON.stringify({path,
// edits})); nil = undefined.
func previewArgsKeyFor(previewInput *renderableEditInput) *string {
	if previewInput == nil {
		return nil
	}
	key, err := json.Marshal(struct {
		Path  string `json:"path"`
		Edits []Edit `json:"edits"`
	}{previewInput.path, previewInput.edits})
	if err != nil {
		return nil
	}
	s := string(key)
	return &s
}

// sameStringPtr compares optional keys (pi: !== on string | undefined).
func sameStringPtr(a *string, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// isSingleEditInput reports whether the value is one {oldText, newText}
// object (pi: isSingleEditInput).
func isSingleEditInput(value map[string]any) bool {
	if value == nil {
		return false
	}
	_, oldOK := value["oldText"].(string)
	_, newOK := value["newText"].(string)
	return oldOK && newOK
}

// prepareEditArguments coerces legacy/JSON-string argument shapes (pi:
// prepareEditArguments).
func prepareEditArguments(input any) any {
	args, ok := input.(map[string]any)
	if !ok {
		return input
	}
	// Some models (Opus 4.6, GLM-5.1) send edits as a JSON string instead of
	// an array. Others send a single edit object instead of a one-element
	// edits array.
	if editsRaw, present := args["edits"]; present {
		switch v := editsRaw.(type) {
		case string:
			var parsed any
			if err := json.Unmarshal([]byte(v), &parsed); err == nil {
				if arr, ok := parsed.([]any); ok {
					args["edits"] = arr
				} else if single, ok := parsed.(map[string]any); ok && isSingleEditInput(single) {
					args["edits"] = []any{single}
				}
			}
		case map[string]any:
			if isSingleEditInput(v) {
				args["edits"] = []any{v}
			}
		}
	}

	oldText, oldOK := args["oldText"].(string)
	newText, newOK := args["newText"].(string)
	if !oldOK || !newOK {
		return args
	}
	edits := []any{}
	if arr, ok := args["edits"].([]any); ok {
		edits = append(edits, arr...)
	}
	edits = append(edits, map[string]any{"oldText": oldText, "newText": newText})
	rest := map[string]any{}
	for key, value := range args {
		if key == "oldText" || key == "newText" {
			continue
		}
		rest[key] = value
	}
	rest["edits"] = edits
	return rest
}

// validateEditInput checks the edits array (pi: validateEditInput).
func validateEditInput(input EditToolInput) (string, []Edit, error) {
	if len(input.Edits) == 0 {
		return "", nil, errors.New("Edit tool input is invalid. edits must contain at least one replacement.")
	}
	return input.Path, input.Edits, nil
}

// editAccessError wraps file access failures with node-style error codes
// (pi: `error instanceof Error && "code" in error ? "Error code: " +
// error.code : String(error)`).
func editAccessError(path string, err error) error {
	if code := codingagentutils.NodeErrCodeOf(err); code != "" {
		return fmt.Errorf("Could not edit file: %s. Error code: %s.", path, code)
	}
	return fmt.Errorf("Could not edit file: %s. Error: %v.", path, err)
}

// underlyingErrText extracts the errno text from a path error.
func underlyingErrText(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

// CreateEditToolDefinition builds the edit tool definition (pi:
// createEditToolDefinition; renderCall/renderResult arrive with the theme
// batch).
func CreateEditToolDefinition(cwd string, options *EditToolOptions) *ToolDefinition {
	// bg is the ctx for async previews (pi: computeEditsDiff takes no
	// signal); captured here because the renderer's ToolRenderContext
	// parameter shadows the context package.
	bg := context.Background()
	ops := defaultEditOperations()
	if options != nil && options.Operations != nil {
		ops = *options.Operations
	}
	return &ToolDefinition{
		Name:             "edit",
		Label:            "edit",
		Description:      "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.",
		PromptSnippet:    EditToolSystemPromptContribution.Snippet,
		PromptGuidelines: append([]string{}, EditToolSystemPromptContribution.Guidelines...),
		Parameters:       json.RawMessage(editSchemaJSON),
		PrepareArguments: prepareEditArguments,
		RenderShell:      "self",
		NewRenderState:   func() any { return &EditRenderState{} },
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx ToolExecuteContext) (agent.AgentToolResult, error) {
			input, err := decodeToolParams[EditToolInput](params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			path, edits, err := validateEditInput(input)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			absolutePath := ResolveToCwd(path, cwd)

			// Do not reject from an abort event listener here: that would
			// release the mutation queue while an in-flight filesystem
			// operation may still finish. Checking signal.aborted after each
			// await observes the same aborts while keeping the queue locked
			// until the current operation has settled.
			throwIfAborted := func() error {
				if ctx.Err() != nil {
					return errors.New("Operation aborted")
				}
				return nil
			}

			return WithFileMutationQueue(ctx, absolutePath, func() (agent.AgentToolResult, error) {
				if err := throwIfAborted(); err != nil {
					return agent.AgentToolResult{}, err
				}
				// Check if file exists.
				if err := ops.Access(ctx, absolutePath); err != nil {
					if abortErr := throwIfAborted(); abortErr != nil {
						return agent.AgentToolResult{}, abortErr
					}
					return agent.AgentToolResult{}, editAccessError(path, err)
				}
				if err := throwIfAborted(); err != nil {
					return agent.AgentToolResult{}, err
				}

				// Read the file.
				buffer, err := ops.ReadFile(ctx, absolutePath)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				rawContent := string(buffer)
				if err := throwIfAborted(); err != nil {
					return agent.AgentToolResult{}, err
				}

				// Strip BOM before matching. The model will not include an
				// invisible BOM in oldText.
				bom, content := codingagentutils.SplitBom(rawContent)
				originalEnding := DetectLineEnding(content)
				normalizedContent := NormalizeToLF(content)
				applied, err := ApplyEditsToNormalizedContent(normalizedContent, edits, path)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				if err := throwIfAborted(); err != nil {
					return agent.AgentToolResult{}, err
				}

				finalContent := bom + RestoreLineEndings(applied.NewContent, originalEnding)
				if err := ops.WriteFile(ctx, absolutePath, finalContent); err != nil {
					return agent.AgentToolResult{}, err
				}
				if err := throwIfAborted(); err != nil {
					return agent.AgentToolResult{}, err
				}

				diffResult := generateDiffStringResult(applied.BaseContent, applied.NewContent)
				patch := GenerateUnifiedPatch(path, applied.BaseContent, applied.NewContent)
				var firstChangedLine *int
				if diffResult.HasFirstChangedLine {
					line := diffResult.FirstChangedLine
					firstChangedLine = &line
				}
				return agent.AgentToolResult{
					Content: []types.Content{asTextContent(fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), path))},
					Details: &EditToolDetails{
						Diff:             diffResult.Diff,
						Patch:            patch,
						FirstChangedLine: firstChangedLine,
					},
				}, nil
			})
		},
		RenderCall: func(args any, theme UITheme, context ToolRenderContext) tui.Component {
			state := context.State.(*EditRenderState)
			component := getEditCallRenderComponent(state, context.LastComponent)
			previewInput := getRenderablePreviewInput(args)
			argsKey := previewArgsKeyFor(previewInput)

			launch := false
			component.mu.Lock()
			if !sameStringPtr(component.previewArgsKey, argsKey) {
				component.preview = nil
				component.previewArgsKey = argsKey
				component.previewPending = false
				component.settledError = false
			}
			if context.ArgsComplete && previewInput != nil && component.preview == nil && !component.previewPending {
				component.previewPending = true
				launch = true
			}
			component.mu.Unlock()

			if launch {
				requestKey := argsKey
				input := *previewInput
				go func() {
					result, errResult := ComputeEditsDiff(bg, input.path, input.edits, context.Cwd)
					var preview EditPreview
					if errResult.Error != "" {
						preview = EditPreview{IsErrorForm: true, Error: errResult.Error}
					} else {
						preview = EditPreview{Diff: result.Diff}
						if result.HasFirstChangedLine {
							line := result.FirstChangedLine
							preview.FirstChangedLine = &line
						}
					}
					component.mu.Lock()
					match := sameStringPtr(component.previewArgsKey, requestKey)
					component.mu.Unlock()
					if match {
						setEditPreview(component, preview, requestKey)
						context.Invalidate()
					}
				}()
			}

			return buildEditCallComponent(theme, component, args, context.Cwd)
		},
		RenderResult: func(result ToolRenderResult, _ ToolRenderResultOptions, theme UITheme, context ToolRenderContext) tui.Component {
			state := context.State.(*EditRenderState)
			previewInput := getRenderablePreviewInput(context.Args)
			argsKey := previewArgsKeyFor(previewInput)
			var resultDiff string
			var firstChangedLine *int
			if !context.IsError && result.Details != nil {
				if details, ok := result.Details.(*EditToolDetails); ok {
					resultDiff = details.Diff
					firstChangedLine = details.FirstChangedLine
				}
			}
			changed := false
			callComponent := state.CallComponent
			if callComponent != nil {
				if resultDiff != "" {
					if setEditPreview(callComponent, EditPreview{Diff: resultDiff, FirstChangedLine: firstChangedLine}, argsKey) {
						changed = true
					}
				}
				callComponent.mu.Lock()
				if callComponent.settledError != context.IsError {
					callComponent.settledError = context.IsError
					changed = true
				}
				callComponent.mu.Unlock()
				if changed {
					buildEditCallComponent(theme, callComponent, context.Args, context.Cwd)
				}
			}

			var preview *EditPreview
			if callComponent != nil {
				preview, _, _, _ = callComponent.snapshot()
			}
			output := formatEditResult(theme, context.Args, preview, result, context.IsError)
			var component *tui.Container
			if c, ok := context.LastComponent.(*tui.Container); ok {
				component = c
			} else {
				component = &tui.Container{}
			}
			component.Clear()
			if output == "" {
				return component
			}
			component.AddChild(tui.NewSpacer(1))
			component.AddChild(tui.NewText(output, 1, 0))
			return component
		},
	}
}

// CreateEditTool wraps the edit definition into an AgentTool (pi:
// createEditTool).
func CreateEditTool(cwd string, options *EditToolOptions) Tool {
	return WrapToolDefinition(CreateEditToolDefinition(cwd, options), nil)
}
