// bash_execution.go (pi: src/modes/interactive/components/bash-execution.ts)
// — the component for user `!` bash command execution with streaming output.
package components

import (
	"strings"
	"sync"

	harnessutils "github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"

	"github.com/samber/lo"
)

// bashPreviewLinesComponent is the collapsed preview limit (pi:
// PREVIEW_LINES, matching tool execution behavior).
const bashPreviewLinesComponent = 20

// bashStatus is the execution status (pi: the "running" | "complete" |
// "cancelled" | "error" union).
type bashStatus = string

const (
	bashStatusRunning   bashStatus = "running"
	bashStatusComplete  bashStatus = "complete"
	bashStatusCancelled bashStatus = "cancelled"
	bashStatusError     bashStatus = "error"
)

// bashPreviewRenderer is the collapsed output renderer with width-aware
// caching (pi: the inline { render, invalidate } object literal).
type bashPreviewRenderer struct {
	mu          sync.Mutex
	styledInput string
	cachedWidth int
	cachedLines []string
	cached      bool
}

func (b *bashPreviewRenderer) Render(width int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.cached || b.cachedWidth != width {
		result := tools.TruncateToVisualLines(b.styledInput, bashPreviewLinesComponent, width, 1, tools.VisualTruncateKeepEnd)
		b.cachedLines = result.VisualLines
		b.cachedWidth = width
		b.cached = true
	}
	return b.cachedLines
}

func (b *bashPreviewRenderer) Invalidate() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cached = false
	b.cachedLines = nil
}

// BashExecutionComponent renders one user bash execution (pi: class
// BashExecutionComponent extends Container).
type BashExecutionComponent struct {
	tui.Container

	command          string
	outputLines      []string
	status           bashStatus
	exitCode         *int
	loader           *tui.Loader
	truncationResult *harnessutils.TruncationResult
	fullOutputPath   string
	expanded         bool
	contentContainer *tui.Container
	preview          *bashPreviewRenderer

	mu sync.Mutex
}

// NewBashExecutionComponent builds the component (pi: constructor;
// excludeFromContext = the `!!` prefix case).
func NewBashExecutionComponent(command string, ui tui.TUI, excludeFromContext bool) *BashExecutionComponent {
	c := &BashExecutionComponent{
		command: command,
		status:  bashStatusRunning,
		preview: &bashPreviewRenderer{},
	}
	c.contentContainer = &tui.Container{}

	// Use dim border for excluded-from-context commands (!! prefix)
	colorKey := "bashMode"
	if excludeFromContext {
		colorKey = "dim"
	}
	borderColor := func(str string) string { return theme.Fg(colorKey, str) }

	// Add spacer
	c.AddChild(tui.NewSpacer(1))

	// Top border
	c.AddChild(NewDynamicBorder(borderColor))

	// Content container (holds dynamic content between borders)
	c.AddChild(c.contentContainer)

	// Command header
	c.contentContainer.AddChild(tui.NewText(theme.Fg(colorKey, theme.Bold("$ "+command)), 1, 0))

	// Loader
	c.loader = tui.NewLoader(
		ui,
		func(spinner string) string { return theme.Fg(colorKey, spinner) },
		func(text string) string { return theme.Fg("muted", text) },
		"Running... ("+tools.KeyText("tui.select.cancel")+" to cancel)", // Plain text for loader
		nil,
	)
	c.contentContainer.AddChild(c.loader)

	// Bottom border
	c.AddChild(NewDynamicBorder(borderColor))
	return c
}

// SetExpanded toggles the full-output view (pi: setExpanded).
func (c *BashExecutionComponent) SetExpanded(expanded bool) {
	c.mu.Lock()
	c.expanded = expanded
	c.mu.Unlock()
	c.updateDisplay()
}

// Invalidate invalidates and rebuilds (pi: invalidate override).
func (c *BashExecutionComponent) Invalidate() {
	c.Container.Invalidate()
	c.updateDisplay()
}

// AppendOutput appends a streaming output chunk (pi: appendOutput; binary
// data is sanitized upstream in executeBashCommand).
func (c *BashExecutionComponent) AppendOutput(chunk string) {
	c.mu.Lock()
	// Strip ANSI codes and normalize line endings
	clean := strings.ReplaceAll(strings.ReplaceAll(utils.StripAnsi(chunk), "\r\n", "\n"), "\r", "\n")

	// Append to output lines
	newLines := strings.Split(clean, "\n")
	if len(c.outputLines) > 0 && len(newLines) > 0 {
		// Append first chunk to last line (incomplete line continuation)
		c.outputLines[len(c.outputLines)-1] += newLines[0]
		c.outputLines = append(c.outputLines, newLines[1:]...)
	} else {
		c.outputLines = append(c.outputLines, newLines...)
	}
	c.mu.Unlock()

	c.updateDisplay()
}

// SetComplete finalizes the run (pi: setComplete).
func (c *BashExecutionComponent) SetComplete(exitCode *int, cancelled bool, truncationResult *harnessutils.TruncationResult, fullOutputPath string) {
	c.mu.Lock()
	c.exitCode = exitCode
	if cancelled {
		c.status = bashStatusCancelled
	} else if exitCode != nil && *exitCode != 0 {
		c.status = bashStatusError
	} else {
		c.status = bashStatusComplete
	}
	c.truncationResult = truncationResult
	c.fullOutputPath = fullOutputPath
	c.mu.Unlock()

	// Stop loader
	c.loader.Stop()

	c.updateDisplay()
}

// updateDisplay rebuilds the content (pi: updateDisplay).
func (c *BashExecutionComponent) updateDisplay() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Apply truncation for LLM context limits (same limits as bash tool)
	fullOutput := strings.Join(c.outputLines, "\n")
	contextTruncation := harnessutils.TruncateTail(fullOutput, &harnessutils.TruncationOptions{
		MaxLines: lo.ToPtr(harnessutils.DefaultMaxLines),
		MaxBytes: lo.ToPtr(harnessutils.DefaultMaxBytes),
	})

	// Get the lines to potentially display (after context truncation)
	var availableLines []string
	if contextTruncation.Content != "" {
		availableLines = strings.Split(contextTruncation.Content, "\n")
	}

	// Apply preview truncation based on expanded state
	previewStart := len(availableLines) - bashPreviewLinesComponent
	if previewStart < 0 {
		previewStart = 0
	}
	previewLogicalLines := availableLines[previewStart:]
	hiddenLineCount := len(availableLines) - len(previewLogicalLines)

	// Rebuild content container
	c.contentContainer.Clear()

	// Command header
	c.contentContainer.AddChild(tui.NewText(theme.Fg("bashMode", theme.Bold("$ "+c.command)), 1, 0))

	// Output
	if len(availableLines) > 0 {
		if c.expanded {
			// Show all lines
			styled := make([]string, 0, len(availableLines))
			for _, line := range availableLines {
				styled = append(styled, theme.Fg("muted", line))
			}
			c.contentContainer.AddChild(tui.NewText("\n"+strings.Join(styled, "\n"), 1, 0))
		} else {
			// Use shared visual truncation utility with width-aware caching
			styled := make([]string, 0, len(previewLogicalLines))
			for _, line := range previewLogicalLines {
				styled = append(styled, theme.Fg("muted", line))
			}
			c.preview.styledInput = "\n" + strings.Join(styled, "\n")
			c.preview.Invalidate()
			c.contentContainer.AddChild(c.preview)
		}
	}

	// Loader or status
	if c.status == bashStatusRunning {
		c.contentContainer.AddChild(c.loader)
	} else {
		var statusParts []string

		// Show how many lines are hidden (collapsed preview)
		if hiddenLineCount > 0 {
			if c.expanded {
				statusParts = append(statusParts,
					theme.Fg("muted", "(")+tools.KeyHint("app.tools.expand", "to collapse")+theme.Fg("muted", ")"))
			} else {
				statusParts = append(statusParts,
					theme.Fg("muted", "... "+itoa(hiddenLineCount)+" more lines (")+tools.KeyHint("app.tools.expand", "to expand")+theme.Fg("muted", ")"))
			}
		}

		if c.status == bashStatusCancelled {
			statusParts = append(statusParts, theme.Fg("warning", "(cancelled)"))
		} else if c.status == bashStatusError {
			exit := 0
			if c.exitCode != nil {
				exit = *c.exitCode
			}
			statusParts = append(statusParts, theme.Fg("error", "(exit "+itoa(exit)+")"))
		}

		// Add truncation warning (context truncation, not preview truncation)
		wasTruncated := (c.truncationResult != nil && c.truncationResult.Truncated) || contextTruncation.Truncated
		if wasTruncated && c.fullOutputPath != "" {
			statusParts = append(statusParts, theme.Fg("warning", "Output truncated. Full output: "+c.fullOutputPath))
		}

		if len(statusParts) > 0 {
			c.contentContainer.AddChild(tui.NewText("\n"+strings.Join(statusParts, "\n"), 1, 0))
		}
	}
}

// GetOutput returns the raw output for BashExecutionMessage (pi:
// getOutput).
func (c *BashExecutionComponent) GetOutput() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.outputLines, "\n")
}

// GetCommand returns the executed command (pi: getCommand).
func (c *BashExecutionComponent) GetCommand() string {
	return c.command
}
