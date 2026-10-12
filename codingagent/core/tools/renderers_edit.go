// renderers_edit.go (pi: src/core/tools/renderers/edit.ts) — presentation
// for the edit tool.
//
// S5a note: pi hosts the renderers in core/tools/renderers/; pig keeps them
// in package tools (files named renderers_*.go) because the renderers
// depend on package-tools values (render-utils, edit-diff, detail types)
// while the tool files spread them back into their definitions — a Go
// subpackage would be an import cycle. Grep mapping: pi renderers/edit.ts ↔
// renderers_edit.go.
package tools

import (
	stdcontext "context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/tui"
	pigutils "github.com/ZoroSpace/pig/utils"
)

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
	// pig-only deviation (2026-10-10): pi uses Box(1, 1) because edit
	// self-renders its frame; pig renders edit in the default shell where the
	// ToolExecutionComponent's contentBox already pads (see the RenderShell
	// note in edit.go), so the box padding is zero to avoid a double indent.
	return &EditCallRenderComponent{Box: tui.NewBox(0, 0, func(text string) string { return text })}
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

// renderToolPathFromArgs is renderToolPath(str(args?.file_path ??
// args?.path), theme, cwd).
func renderToolPathFromArgs(theme UITheme, args any, cwd string) string {
	return RenderToolPath(rawPathFromArgs(args), theme, cwd, "")
}

// editDetailsDiff extracts the diff artifacts from result details (pi reads
// properties off the plain details object). Double-form family: live events
// carry *EditToolDetails (agent_loop createToolResultMessage passes the
// tool's struct through), session restore decodes the JSONL `details` field
// into map[string]any (types.ToolResultMessage.Details any) — a pointer-only
// assertion dropped the diff on restored sessions
// (docs/fix/2026-10-10-edit-details-map-form.md).
func editDetailsDiff(details any) (string, *int) {
	switch d := details.(type) {
	case *EditToolDetails:
		return d.Diff, d.FirstChangedLine
	case EditToolDetails:
		return d.Diff, d.FirstChangedLine
	case map[string]any:
		// Wire keys are camelCase (pi parity; pig sessions written after the
		// json-tag fix). Sessions written before the fix carried capitalized
		// keys — accept both when reading (docs/fix/2026-10-10-edit-details-map-form.md).
		diff, _ := d["diff"].(string)
		if diff == "" {
			diff, _ = d["Diff"].(string)
		}
		var firstChangedLine *int
		line, ok := d["firstChangedLine"].(float64)
		if !ok {
			line, ok = d["FirstChangedLine"].(float64)
		}
		if ok {
			lineInt := int(line)
			firstChangedLine = &lineInt
		}
		return diff, firstChangedLine
	}
	return "", nil
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
			switch t := c.(type) {
			case types.TextContent:
				errorTexts = append(errorTexts, t.Text)
			case *types.TextContent:
				// Builtin tools emit pointer blocks (asTextContent); values
				// come from the decoders
				// (docs/fix/2026-09-11-tool-execution-live-output-empty.md).
				if t != nil {
					errorTexts = append(errorTexts, t.Text)
				}
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
		resultDiff, _ = editDetailsDiff(result.Details)
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

// getEditHeaderBg resolved the header background (pi: getEditHeaderBg).
// pig-only (2026-10-03): the edit call header no longer fills its background
// — the outer tool-execution frame carries the state color instead
// (docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS), so this is kept only
// as documentation of the pi mapping: preview error form / settled error →
// toolErrorBg, preview ok → toolSuccessBg, pending → toolPendingBg.

// buildEditCallComponent repopulates the call component (pi:
// buildEditCallComponent).
func buildEditCallComponent(theme UITheme, component *EditCallRenderComponent, args any, cwd string) *EditCallRenderComponent {
	preview, _, _, _ := component.snapshot()
	component.SetBgFn(nil)
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

// editRenderCall is the edit call renderer (pi: editRenderers.renderCall).
// bg is the ctx for async previews (pi: computeEditsDiff takes no signal);
// pig needs a concrete context for the goroutine.
func editRenderCall(args any, theme UITheme, context ToolRenderContext) tui.Component {
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
			// pig panic containment: the async diff preview must not kill
			// the process on unexpected input (see docs/feat/PANIC-RESILIENCE.md).
			defer pigutils.RecoverPanic("tools.renderEdit.preview")
			result, errResult := ComputeEditsDiff(stdcontext.Background(), input.path, input.edits, context.Cwd)
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
}

// editRenderResult is the edit result renderer (pi:
// editRenderers.renderResult).
func editRenderResult(result ToolRenderResult, _ ToolRenderResultOptions, theme UITheme, context ToolRenderContext) tui.Component {
	state := context.State.(*EditRenderState)
	previewInput := getRenderablePreviewInput(context.Args)
	argsKey := previewArgsKeyFor(previewInput)
	var resultDiff string
	var firstChangedLine *int
	if !context.IsError && result.Details != nil {
		resultDiff, firstChangedLine = editDetailsDiff(result.Details)
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
	// pig-only deviation (2026-10-10): pi pads the text because the result
	// container self-renders; the default shell's contentBox pads instead.
	component.AddChild(tui.NewText(output, 0, 0))
	return component
}

// EditRenderers is the edit renderer pair (pi: editRenderers; edit.ts
// spreads it into its definition).
var EditRenderers = ToolRenderers{
	NewRenderState: func() any { return &EditRenderState{} },
	RenderCall:     editRenderCall,
	RenderResult:   editRenderResult,
}
