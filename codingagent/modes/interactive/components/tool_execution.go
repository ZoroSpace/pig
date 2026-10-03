// tool_execution.go (pi: src/modes/interactive/components/tool-execution.ts)
// — the tool-execution component: call/result renderer dispatch, default and
// self render shells, fallback rendering, and the image half.
package components

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// fallbackPreviewLines is the collapsed fallback result limit (pi:
// FALLBACK_PREVIEW_LINES).
const fallbackPreviewLines = 10

// ToolExecutionOptions carries the component options (pi:
// ToolExecutionOptions).
type ToolExecutionOptions struct {
	ShowImages      *bool
	ImageWidthCells *int
}

// ToolExecutionResult is the updateResult argument shape (pi: the inline
// { content, details?, isError } object).
type ToolExecutionResult struct {
	Content []types.Content
	Details any
	IsError bool
}

// ToolExecutionComponent renders one tool execution row (pi: class
// ToolExecutionComponent extends Container). updateDisplay mutates the child
// tree while Render reads it, so the pi single-thread assumptions are
// guarded with a mutex (repo precedent: editor components).
type ToolExecutionComponent struct {
	tui.Container

	contentBox        *tui.Box
	contentText       *tui.Text
	contentTextRegion *tui.MouseRegion
	// topBorder/bottomBorder frame the default and fallback shells with a
	// state-colored rule instead of the pi full-width background fill
	// (pig-only deviation, 2026-10-03: border-only blocks, see
	// docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS).
	topBorder    *DynamicBorder
	bottomBorder *DynamicBorder
	// borderStateKey is the theme token coloring both borders ("muted"
	// while streaming, "success"/"error" once settled). Read at render time
	// without locking — same threading model as the child-tree mutations in
	// updateDisplay.
	borderStateKey      string
	selfRenderContainer *tui.Container
	// selfRenderHeight is the last rendered height of the self-render
	// container, used to route mouse events (pi: selfRenderHeight).
	selfRenderHeight        int
	callRendererComponent   tui.Component
	resultRendererComponent tui.Component
	rendererState           any
	// imageComponents/imageSpacers stay empty until the Phase 8 image
	// detection lands (pi: Image[]/Spacer[]; getCapabilities().images is
	// always falsy until then, so the branches are unreachable — structure
	// kept at its source position).
	imageComponents  []tui.Component
	imageSpacers     []tui.Component
	toolName         string
	toolCallID       string
	args             any
	expanded         bool
	showImages       bool
	imageWidthCells  int
	isPartial        bool
	toolDefinition   *tools.ToolDefinition
	ui               tui.TUI
	cwd              string
	executionStarted bool
	argsComplete     bool
	result           *ToolExecutionResult
	convertedImages  map[int]imageConversion
	hideComponent    bool

	mu sync.Mutex
}

// imageConversion is pi v0.86.0's convertedImages value shape
// { sourceData, sourceMimeType, data, mimeType }; populated by the Phase 8
// Kitty transcoder. The source fields key the cache: a conversion that
// finishes after the image was replaced must be dropped (#8577).
type imageConversion struct {
	SourceData     string
	SourceMimeType string
	Data           string
	MimeType       string
}

// NewToolExecutionComponent builds the component (pi: constructor).
func NewToolExecutionComponent(
	toolName string,
	toolCallID string,
	args any,
	options ToolExecutionOptions,
	toolDefinition *tools.ToolDefinition,
	ui tui.TUI,
	cwd string,
) *ToolExecutionComponent {
	c := &ToolExecutionComponent{
		toolName:        toolName,
		toolCallID:      toolCallID,
		args:            args,
		toolDefinition:  toolDefinition,
		showImages:      options.ShowImages == nil || *options.ShowImages,
		imageWidthCells: 60,
		isPartial:       true,
		ui:              ui,
		cwd:             cwd,
		rendererState:   map[string]any{},
		convertedImages: map[int]imageConversion{},
	}
	if options.ImageWidthCells != nil {
		c.imageWidthCells = *options.ImageWidthCells
	}

	// Resolve the shared renderer state (pig addition: pi initializes a
	// plain {} object; a definition with typed state gets its struct,
	// extension tools keep the map). The built-in fallback lookup is gone in
	// v0.85.1: the caller merges built-in renderers via tools.
	// WithBuiltInRenderers (pi: withBuiltInRenderers).
	if c.toolDefinition != nil && c.toolDefinition.NewRenderState != nil {
		c.rendererState = c.toolDefinition.NewRenderState()
	}

	c.AddChild(tui.NewSpacer(1))

	// Always create all shell variants. contentBox is used for default
	// renderer-based composition. selfRenderContainer is used when the tool
	// renders its own framing. contentText is reserved for generic fallback
	// rendering when no tool definition exists. The default and fallback
	// shells get state-colored top/bottom borders (pig-only; pi fills the
	// box background instead). Vertical padding 0: the rules give the frame,
	// keeping the pre-border block height (pi: 1).
	c.contentBox = tui.NewBox(1, 0, nil)
	c.contentText = tui.NewText("", 1, 0)
	c.contentTextRegion = c.createResultRegion(c.contentText)
	c.selfRenderContainer = &tui.Container{}
	c.borderStateKey = "muted"
	// Rule tokens are optional per-theme pig-only tokens
	// (docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS); themes without
	// them fall back to the state tokens.
	ruleTokens := map[string]string{
		"muted":   "toolPendingRule",
		"success": "toolSuccessRule",
		"error":   "toolErrorRule",
	}
	borderColor := func(s string) string {
		return theme.FgOptional(ruleTokens[c.borderStateKey], c.borderStateKey, s)
	}
	c.topBorder = NewDynamicBorderCorner(borderColor, '┌')
	c.bottomBorder = NewDynamicBorderCorner(borderColor, '└')
	contentRail := func(child tui.Component) tui.Component { return NewLeftRail(child, borderColor) }

	if c.hasRendererDefinition() {
		if c.getRenderShell() == "self" {
			c.AddChild(c.selfRenderContainer)
		} else {
			c.AddChild(c.topBorder)
			c.AddChild(contentRail(c.contentBox))
			c.AddChild(c.bottomBorder)
		}
	} else {
		c.AddChild(c.topBorder)
		c.AddChild(contentRail(c.contentTextRegion))
		c.AddChild(c.bottomBorder)
	}

	c.updateDisplay()
	return c
}

// getCallRenderer resolves the call renderer (pi: getCallRenderer; v0.85.1
// dropped the built-in fallback — the caller merges via
// tools.WithBuiltInRenderers).
func (c *ToolExecutionComponent) getCallRenderer() func(any, tools.UITheme, tools.ToolRenderContext) tui.Component {
	if c.toolDefinition == nil {
		return nil
	}
	return c.toolDefinition.RenderCall
}

// getResultRenderer resolves the result renderer (pi: getResultRenderer).
func (c *ToolExecutionComponent) getResultRenderer() func(tools.ToolRenderResult, tools.ToolRenderResultOptions, tools.UITheme, tools.ToolRenderContext) tui.Component {
	if c.toolDefinition == nil {
		return nil
	}
	return c.toolDefinition.RenderResult
}

// hasRendererDefinition reports whether any definition is in play (pi:
// hasRendererDefinition).
func (c *ToolExecutionComponent) hasRendererDefinition() bool {
	return c.toolDefinition != nil
}

// getRenderShell resolves the shell mode (pi: getRenderShell).
func (c *ToolExecutionComponent) getRenderShell() string {
	if c.toolDefinition != nil && c.toolDefinition.RenderShell != "" {
		return c.toolDefinition.RenderShell
	}
	return "default"
}

// getRenderContext builds the render context (pi: getRenderContext;
// lastComponent is the caller's previous component for the slot).
func (c *ToolExecutionComponent) getRenderContext(lastComponent tui.Component, isError bool) tools.ToolRenderContext {
	return tools.ToolRenderContext{
		Args:             c.args,
		ToolCallID:       c.toolCallID,
		Invalidate:       c.invalidateAndRequestRender,
		LastComponent:    lastComponent,
		State:            c.rendererState,
		Cwd:              c.cwd,
		ExecutionStarted: c.executionStarted,
		ArgsComplete:     c.argsComplete,
		IsPartial:        c.isPartial,
		Expanded:         c.expanded,
		ShowImages:       c.showImages,
		IsError:          isError,
	}
}

// invalidateAndRequestRender is pi's inline invalidate closure.
func (c *ToolExecutionComponent) invalidateAndRequestRender() {
	c.Invalidate()
	c.ui.RequestRender(false)
}

// createResultRegion wraps a child with click-to-expand mouse handling (pi:
// createResultRegion).
func (c *ToolExecutionComponent) createResultRegion(component tui.Component) *tui.MouseRegion {
	return tui.NewMouseRegion(component, func(event *tui.TuiMouseEvent) *tui.TuiMouseEventResult {
		c.mu.Lock()
		hasResult := c.result != nil
		c.mu.Unlock()
		if !hasResult || event.Type != tui.MouseEventTypeClick || event.Button != tui.MouseButtonLeft {
			return nil
		}
		c.SetExpanded(!c.expandedLocked())
		return &tui.TuiMouseEventResult{Handled: true}
	})
}

// expandedLocked reads the expanded flag (pig: c.expanded is mutex-guarded).
func (c *ToolExecutionComponent) expandedLocked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expanded
}

// createCallFallback builds the no-renderer call row: the tool name followed
// by the streamed arguments (pi: createCallFallback, v0.99.1).
func (c *ToolExecutionComponent) createCallFallback() tui.Component {
	return tui.NewText(tools.FormatToolCallWithArgs(c.toolName, c.args, theme.Current(), c.expandedLocked()), 0, 0)
}

// createResultFallback builds the generic result preview (pi:
// createResultFallback; nil = undefined).
func (c *ToolExecutionComponent) createResultFallback() tui.Component {
	output := c.getTextOutput()
	if output == "" {
		return nil
	}

	lines := strings.Split(output, "\n")
	displayLines := lines
	if !c.expanded {
		if len(lines) > fallbackPreviewLines {
			displayLines = lines[:fallbackPreviewLines]
		}
	}
	remaining := len(lines) - len(displayLines)
	styled := make([]string, 0, len(displayLines))
	for _, line := range displayLines {
		styled = append(styled, theme.Fg("toolOutput", line))
	}
	text := strings.Join(styled, "\n")
	if remaining > 0 {
		text += theme.Fg("muted", "\n... ("+itoa(remaining)+" more lines,") +
			" " + tools.KeyHint("app.tools.expand", "to expand") + theme.Fg("muted", ")")
	}
	return tui.NewText(text, 0, 0)
}

// UpdateArgs replaces the streamed arguments (pi: updateArgs).
func (c *ToolExecutionComponent) UpdateArgs(args any) {
	c.mu.Lock()
	c.args = args
	c.mu.Unlock()
	c.updateDisplay()
}

// MarkExecutionStarted flags the execution start (pi: markExecutionStarted).
func (c *ToolExecutionComponent) MarkExecutionStarted() {
	c.mu.Lock()
	c.executionStarted = true
	c.mu.Unlock()
	c.updateDisplay()
	c.ui.RequestRender(false)
}

// SetArgsComplete flags complete streamed args (pi: setArgsComplete).
func (c *ToolExecutionComponent) SetArgsComplete() {
	c.mu.Lock()
	c.argsComplete = true
	c.mu.Unlock()
	c.updateDisplay()
	c.ui.RequestRender(false)
}

// UpdateResult sets the (possibly partial) result (pi: updateResult).
func (c *ToolExecutionComponent) UpdateResult(result ToolExecutionResult, isPartial bool) {
	c.mu.Lock()
	c.result = &result
	c.isPartial = isPartial
	c.mu.Unlock()
	c.updateDisplay()
	c.maybeConvertImagesForKitty()
}

// maybeConvertImagesForKitty converts non-PNG images for the Kitty
// protocol (pi: maybeConvertImagesForKitty). convertToPng (WASM photon)
// arrives with Phase 8; until then getCapabilities().images is never
// "kitty" and this is a structured no-op.
func (c *ToolExecutionComponent) maybeConvertImagesForKitty() {
	caps := tui.GetCapabilities()
	if caps.Images != "kitty" {
		return
	}
	// Phase 8: convertToPng(sourceData, sourceMimeType).then(converted =>
	// ...) per image block, filling c.convertedImages and re-rendering. pi
	// v0.86.0 (#8577) adds the stale-conversion guard here: only record the
	// conversion when the block at the same index still carries the source
	// data/mimeType the conversion started from; the cache entry stores the
	// source fields and the read side re-validates them.
}

// SetExpanded toggles the full-output view (pi: setExpanded).
func (c *ToolExecutionComponent) SetExpanded(expanded bool) {
	c.mu.Lock()
	c.expanded = expanded
	c.mu.Unlock()
	c.updateDisplay()
}

// SetShowImages toggles inline image rendering (pi: setShowImages).
func (c *ToolExecutionComponent) SetShowImages(show bool) {
	c.mu.Lock()
	c.showImages = show
	c.mu.Unlock()
	c.updateDisplay()
}

// SetImageWidthCells sets the image cell width (pi: setImageWidthCells).
func (c *ToolExecutionComponent) SetImageWidthCells(width int) {
	c.mu.Lock()
	if width < 1 {
		width = 1
	}
	c.imageWidthCells = width
	c.mu.Unlock()
	c.updateDisplay()
}

// Invalidate invalidates the component and rebuilds it (pi: invalidate
// override).
func (c *ToolExecutionComponent) Invalidate() {
	c.Container.Invalidate()
	c.updateDisplay()
}

// Render renders the component (pi: render override).
func (c *ToolExecutionComponent) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hideComponent {
		return nil
	}

	if c.hasRendererDefinition() && c.getRenderShell() == "self" {
		contentLines := c.selfRenderContainer.Render(width)
		c.selfRenderHeight = len(contentLines)
		if len(contentLines) == 0 && len(c.imageComponents) == 0 {
			return nil
		}

		var lines []string
		if len(contentLines) > 0 {
			lines = append(lines, "")
			lines = append(lines, contentLines...)
		}
		for i := range c.imageComponents {
			if spacer := c.imageSpacers[i]; spacer != nil {
				lines = append(lines, spacer.Render(width)...)
			}
			if imageComponent := c.imageComponents[i]; imageComponent != nil {
				lines = append(lines, imageComponent.Render(width)...)
			}
		}
		return lines
	}

	return c.Container.Render(width)
}

// HandleMouse routes mouse events in the self-render shell into the
// self-render container (pi: handleMouse override; the leading blank line is
// not part of the self-render content).
func (c *ToolExecutionComponent) HandleMouse(event *tui.TuiMouseEvent) *tui.TuiMouseEventResult {
	c.mu.Lock()
	selfShell := c.hasRendererDefinition() && c.getRenderShell() == "self"
	selfRenderHeight := c.selfRenderHeight
	c.mu.Unlock()
	if !selfShell {
		return c.Container.HandleMouse(event)
	}
	if event.Y <= 0 || event.Y > selfRenderHeight {
		return nil
	}
	shifted := *event
	shifted.Y = event.Y - 1
	shifted.Height = selfRenderHeight
	return c.selfRenderContainer.HandleMouse(&shifted)
}

// updateDisplay rebuilds the child tree (pi: updateDisplay). The pig
// border-only shells color the frame by state instead of filling the box
// background (pi: bgFn pending/success/error → pig: muted/success/error).
func (c *ToolExecutionComponent) updateDisplay() {
	c.borderStateKey = "success"
	if c.isPartial {
		c.borderStateKey = "muted"
	} else if c.result != nil && c.result.IsError {
		c.borderStateKey = "error"
	}

	hasContent := false
	c.hideComponent = false
	if c.hasRendererDefinition() {
		selfShell := c.getRenderShell() == "self"
		if selfShell {
			c.selfRenderContainer.Clear()
		} else {
			c.contentBox.Clear()
		}
		// Box and Container have parallel AddChild surfaces (Box owns its
		// children directly); addChild targets the active shell variant. Each
		// rendered child is wrapped in a click-to-expand region (pi:
		// createResultRegion).
		addChild := func(component tui.Component) {
			wrapped := c.createResultRegion(component)
			if selfShell {
				c.selfRenderContainer.AddChild(wrapped)
			} else {
				c.contentBox.AddChild(wrapped)
			}
		}

		callRenderer := c.getCallRenderer()
		if callRenderer == nil {
			addChild(c.createCallFallback())
			hasContent = true
		} else {
			component, ok := safeCall(func() tui.Component {
				return callRenderer(c.args, theme.Current(), c.getRenderContext(c.callRendererComponent, c.resultIsError()))
			})
			if ok && component != nil {
				c.callRendererComponent = component
				addChild(component)
				hasContent = true
			} else {
				c.callRendererComponent = nil
				addChild(c.createCallFallback())
				hasContent = true
			}
		}

		if c.result != nil {
			resultRenderer := c.getResultRenderer()
			if resultRenderer == nil {
				if component := c.createResultFallback(); component != nil {
					addChild(component)
					hasContent = true
				}
			} else {
				resultValue := agent.AgentToolResult{Content: c.result.Content, Details: c.result.Details}
				options := tools.ToolRenderResultOptions{Expanded: c.expanded, IsPartial: c.isPartial}
				component, ok := safeCall(func() tui.Component {
					return resultRenderer(resultValue, options, theme.Current(), c.getRenderContext(c.resultRendererComponent, c.result.IsError))
				})
				if ok && component != nil {
					c.resultRendererComponent = component
					addChild(component)
					hasContent = true
				} else {
					c.resultRendererComponent = nil
					if component := c.createResultFallback(); component != nil {
						addChild(component)
						hasContent = true
					}
				}
			}
		}
	} else {
		c.contentText.SetText(c.formatToolExecution())
		hasContent = true
	}

	for _, img := range c.imageComponents {
		c.RemoveChild(img)
	}
	c.imageComponents = nil
	for _, spacer := range c.imageSpacers {
		c.RemoveChild(spacer)
	}
	c.imageSpacers = nil

	if c.result != nil {
		var imageBlocks []types.ImageContent
		for _, block := range c.result.Content {
			switch img := block.(type) {
			case types.ImageContent:
				imageBlocks = append(imageBlocks, img)
			case *types.ImageContent:
				// Builtin tools emit pointer blocks (read.go); values come
				// from the decoders
				// (docs/fix/2026-09-11-tool-execution-live-output-empty.md).
				if img != nil {
					imageBlocks = append(imageBlocks, *img)
				}
			}
		}
		caps := tui.GetCapabilities()
		for i, img := range imageBlocks {
			// pi: caps.images && this.showImages && img.data && img.mimeType.
			// The Image component and Kitty transcoding arrive with Phase 8;
			// until then caps.images is falsy and this stays unreachable.
			if caps.Images != nil && caps.Images != false && c.showImages && img.Data != "" && img.MimeType != "" {
				// pi v0.86.0 (#8577): a cached conversion only applies to the
				// same source image it was computed from.
				cached, hasConverted := c.convertedImages[i]
				var converted *imageConversion
				if hasConverted && cached.SourceData == img.Data && cached.SourceMimeType == img.MimeType {
					converted = &cached
				}
				imageData := img.Data
				imageMimeType := img.MimeType
				if converted != nil {
					imageData = converted.Data
					imageMimeType = converted.MimeType
				}
				_ = imageData
				_ = imageMimeType
				if caps.Images == "kitty" && imageMimeType != "image/png" {
					continue
				}
				spacer := tui.NewSpacer(1)
				c.AddChild(spacer)
				c.imageSpacers = append(c.imageSpacers, spacer)
				// Phase 8: new Image(imageData, imageMimeType,
				// { fallbackColor: s => theme.fg("toolOutput", s) },
				// { maxWidthCells: this.imageWidthCells }).
				var imageComponent tui.Component = &pendingImageComponent{}
				c.imageComponents = append(c.imageComponents, imageComponent)
				c.AddChild(imageComponent)
			}
		}
	}

	if c.hasRendererDefinition() && !hasContent && len(c.imageComponents) == 0 {
		c.hideComponent = true
	}
}

// resultIsError reports the current result error state (pi: this.result?
// .isError ?? false).
func (c *ToolExecutionComponent) resultIsError() bool {
	if c.result != nil {
		return c.result.IsError
	}
	return false
}

// safeCall recovers renderer panics (pi: try/catch around renderCall and
// renderResult).
func safeCall(fn func() tui.Component) (component tui.Component, ok bool) {
	defer func() {
		if recover() != nil {
			component = nil
			ok = false
		}
	}()
	return fn(), true
}

// pendingImageComponent is the Phase 8 placeholder for the pi-tui Image
// component (unreachable while getCapabilities().images is falsy). It
// renders nothing and reports why via the comment contract above.
type pendingImageComponent struct{}

func (p *pendingImageComponent) Render(width int) []string { return nil }
func (p *pendingImageComponent) Invalidate()               {}

// getTextOutput resolves the displayed output (pi: getTextOutput).
func (c *ToolExecutionComponent) getTextOutput() string {
	if c.result == nil {
		return tools.GetTextOutput(nil, c.showImages)
	}
	return tools.GetTextOutput(&agent.AgentToolResult{Content: c.result.Content, Details: c.result.Details}, c.showImages)
}

// formatToolExecution renders the generic no-definition view (pi:
// formatToolExecution).
func (c *ToolExecutionComponent) formatToolExecution() string {
	text := theme.Fg("toolTitle", theme.Bold(c.toolName))
	if c.args != nil {
		if content, err := json.MarshalIndent(c.args, "", "  "); err == nil && len(content) > 0 {
			text += "\n\n" + string(content)
		}
	}
	if output := c.getTextOutput(); output != "" {
		text += "\n" + output
	}
	return text
}
