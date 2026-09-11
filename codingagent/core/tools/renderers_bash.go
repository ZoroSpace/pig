// renderers_bash.go (pi: src/core/tools/renderers/bash.ts) — presentation
// for the shell tools.
//
// S5a note: pi moved the renderers out of bash.ts into core/tools/renderers/
// so a process that only displays tool output does not load the execution
// path. pig keeps them in package tools (files named renderers_*.go) because
// the renderers depend on package-tools values (render-utils, edit-diff,
// keybinding hints, detail types) while the tool files spread them back into
// their definitions — a Go subpackage would be an import cycle. Grep
// mapping: pi renderers/bash.ts ↔ renderers_bash.go.
//
// Renderers live apart from the implementation; bash.ts spreads these into
// the shell tool definition, so the tool's public shape is unchanged.
package tools

import (
	"fmt"
	"strings"
	"time"

	harnessutils "github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/tui"
	pigutils "github.com/ZoroSpace/pig/utils"
)

// bashPreviewLines is the collapsed shell-output preview height (pi:
// BASH_PREVIEW_LINES).
const bashPreviewLines = 5

// BashUpdateThrottleMs throttles streaming output updates (pi:
// BASH_UPDATE_THROTTLE_MS; declared in renderers/bash.ts in pi and imported
// by bash.ts).
const bashUpdateThrottleMs = 100

// BashResultRenderState is the result-row cache state (pi:
// BashResultRenderState).
type BashResultRenderState struct {
	CachedWidth   int
	CachedLines   []string
	CachedSkipped int
	// Cached is true once CachedLines holds a render for CachedWidth (pi:
	// undefined checks).
	Cached bool
}

// BashResultRenderComponent is the result render component (pi: class
// BashResultRenderComponent extends Container).
type BashResultRenderComponent struct {
	tui.Container
	state BashResultRenderState
}

// bashPreviewComponent is the inline collapsed-preview renderer (pi: the
// object literal { render, invalidate } with width-aware caching).
type bashPreviewComponent struct {
	theme         UITheme
	styledOutput  string
	cachedWidth   int
	cachedLines   []string
	cachedSkipped int
	cached        bool
	state         *BashResultRenderState
}

func (b *bashPreviewComponent) Render(width int) []string {
	if !b.cached || b.cachedWidth != width {
		preview := TruncateToVisualLines(b.styledOutput, bashPreviewLines, width, 0)
		b.cachedLines = preview.VisualLines
		b.cachedSkipped = preview.SkippedCount
		b.cachedWidth = width
		b.cached = true
		b.state.CachedWidth = width
		b.state.CachedLines = preview.VisualLines
		b.state.CachedSkipped = preview.SkippedCount
		b.state.Cached = true
	}
	if b.state.CachedSkipped > 0 {
		hint := b.theme.Fg("muted", fmt.Sprintf("... (%d earlier lines,", b.state.CachedSkipped)) +
			" " + KeyHint("app.tools.expand", "to expand") + b.theme.Fg("muted", ")")
		lines := []string{"", tui.TruncateToWidth(hint, width, "...", false)}
		return append(lines, b.state.CachedLines...)
	}
	return append([]string{""}, b.state.CachedLines...)
}

func (b *bashPreviewComponent) Invalidate() {
	b.cached = false
	b.state.Cached = false
	b.state.CachedLines = nil
	b.state.CachedSkipped = 0
}

// formatDuration renders seconds with one decimal (pi: formatDuration).
func formatDuration(ms float64) string {
	return fmt.Sprintf("%.1fs", ms/1000)
}

// argsToMap normalizes raw call arguments to the map shape the renderers
// consume (pi: args is the raw streamed object; pig carries whatever the
// caller decoded — JSON objects arrive as map[string]any).
func argsToMap(args any) map[string]any {
	if m, ok := args.(map[string]any); ok {
		return m
	}
	return nil
}

// formatShellCall renders the call row (pi: formatShellCall; theme comes
// from the renderer argument instead of the global).
func formatShellCall(theme UITheme, args map[string]any, prompt string) string {
	command, commandOk := Str(args["command"])
	timeout, _ := args["timeout"].(float64)
	timeoutSuffix := ""
	if timeout != 0 {
		timeoutSuffix = theme.Fg("muted", fmt.Sprintf(" (timeout %gs)", timeout))
	}
	commandDisplay := theme.Fg("toolOutput", "...")
	if !commandOk {
		commandDisplay = InvalidArgText(theme)
	} else if command != "" {
		commandDisplay = command
	}
	return theme.Fg("toolTitle", theme.Bold(prompt+" "+commandDisplay)) + timeoutSuffix
}

// startElapsedTicker drives the 1s elapsed-time refresh while a bash result
// is partial (pi: setInterval(() => context.invalidate(), 1000)).
func startElapsedTicker(invalidate func()) *time.Ticker {
	ticker := time.NewTicker(1 * time.Second)
	go func() {
		// pig panic containment: the render-state ticker must survive a
		// panic (see docs/feat/PANIC-RESILIENCE.md).
		defer pigutils.RecoverPanic("tools.renderBash.elapsed")
		for range ticker.C {
			invalidate()
		}
	}()
	return ticker
}

// rebuildBashResultRenderComponent repopulates the result component (pi:
// rebuildBashResultRenderComponent).
func rebuildBashResultRenderComponent(
	theme UITheme,
	component *BashResultRenderComponent,
	result ToolRenderResult,
	options ToolRenderResultOptions,
	showImages bool,
	startedAt int64,
	endedAt int64,
) {
	state := &component.state
	component.Clear()

	output := strings.Trim(GetTextOutput(&result, showImages), " \t\n\r")
	var details *BashToolDetails
	if result.Details != nil {
		details, _ = result.Details.(*BashToolDetails)
	}
	var truncation *harnessutils.TruncationResult
	fullOutputPath := ""
	if details != nil {
		truncation = details.Truncation
		fullOutputPath = details.FullOutputPath
	}
	if !options.IsPartial && truncation != nil && truncation.Truncated && fullOutputPath != "" && strings.HasSuffix(output, "]") {
		footerStart := strings.LastIndex(output, "\n\n[")
		if footerStart != -1 && strings.Contains(output[footerStart:], fullOutputPath) {
			output = strings.TrimRight(output[:footerStart], " \t\n\r")
		}
	}

	if output != "" {
		styledLines := strings.Split(output, "\n")
		for j, line := range styledLines {
			styledLines[j] = theme.Fg("toolOutput", line)
		}
		styledOutput := strings.Join(styledLines, "\n")

		if options.Expanded {
			component.AddChild(tui.NewText("\n"+styledOutput, 0, 0))
		} else {
			component.AddChild(&bashPreviewComponent{theme: theme, styledOutput: styledOutput, state: state})
		}
	}

	if (truncation != nil && truncation.Truncated) || fullOutputPath != "" {
		var warnings []string
		if fullOutputPath != "" {
			warnings = append(warnings, "Full output: "+fullOutputPath)
		}
		if truncation != nil && truncation.Truncated {
			if truncation.TruncatedBy == "lines" {
				warnings = append(warnings, fmt.Sprintf("Truncated: showing %d of %d lines", truncation.OutputLines, truncation.TotalLines))
			} else {
				maxBytes := int64(harnessutils.DefaultMaxBytes)
				if truncation.MaxBytes != 0 {
					maxBytes = int64(truncation.MaxBytes)
				}
				warnings = append(warnings, fmt.Sprintf("Truncated: %d lines shown (%s limit)", truncation.OutputLines, harnessutils.FormatSize(maxBytes)))
			}
		}
		component.AddChild(tui.NewText("\n"+theme.Fg("warning", "["+strings.Join(warnings, ". ")+"]"), 0, 0))
	}

	if startedAt != 0 {
		label := "Took"
		if options.IsPartial {
			label = "Elapsed"
		}
		endTime := endedAt
		if endTime == 0 {
			endTime = time.Now().UnixMilli()
		}
		component.AddChild(tui.NewText("\n"+theme.Fg("muted", fmt.Sprintf("%s %s", label, formatDuration(float64(endTime-startedAt)))), 0, 0))
	}
}

// CreateShellRenderers builds the shell renderers; they are shared by bash
// and powershell, which differ only in the prompt they display (pi:
// createShellRenderers).
func CreateShellRenderers(prompt string) ToolRenderers {
	return ToolRenderers{
		NewRenderState: func() any { return &BashRenderState{} },
		RenderCall: func(args any, theme UITheme, context ToolRenderContext) tui.Component {
			state := context.State.(*BashRenderState)
			if context.ExecutionStarted && state.StartedAt == 0 {
				state.StartedAt = time.Now().UnixMilli()
				state.EndedAt = 0
			}
			text, ok := context.LastComponent.(*tui.Text)
			if !ok {
				text = tui.NewText("", 0, 0)
			}
			text.SetText(formatShellCall(theme, argsToMap(args), prompt))
			return text
		},
		RenderResult: func(result ToolRenderResult, options ToolRenderResultOptions, theme UITheme, context ToolRenderContext) tui.Component {
			state := context.State.(*BashRenderState)
			if state.StartedAt != 0 && options.IsPartial && state.Interval == nil {
				state.Interval = startElapsedTicker(context.Invalidate)
			}
			if !options.IsPartial || context.IsError {
				if state.EndedAt == 0 {
					state.EndedAt = time.Now().UnixMilli()
				}
				if state.Interval != nil {
					state.Interval.Stop()
					state.Interval = nil
				}
			}
			var component *BashResultRenderComponent
			if c, ok := context.LastComponent.(*BashResultRenderComponent); ok {
				component = c
			} else {
				component = &BashResultRenderComponent{}
			}
			rebuildBashResultRenderComponent(theme, component, result, options, context.ShowImages, state.StartedAt, state.EndedAt)
			component.Invalidate()
			return component
		},
	}
}
