// Package components (pi: src/modes/interactive/components/) — the
// transcript/UI components for interactive mode.
//
// Ported in T5a-1 (this batch): markdown-transform, keybinding-hints,
// dynamic-border, visual-truncate, countdown-timer, bordered-loader,
// status-indicator, user-message, assistant-message,
// branch-summary-message, compaction-summary-message,
// skill-invocation-message, custom-message, custom-entry.
//
// Deferred to T5a-2 (they drag the per-tool renderer layer, pi-side ≈2k
// lines): tool-execution.ts, bash-execution.ts, diff.ts (needs the
// jsdiff diffWords port for renderIntraLineDiff), and the per-tool
// renderCall/renderResult implementations in core/tools.
//
// pi's components/index.ts re-exports are not ported (Go has no
// re-export; consumers import the defining package directly).
package components

import (
	"github.com/ZoroSpace/pig/codingagent/core"
)

// createMarkdownTransform returns a Markdown transform function applying
// the registered transformers in order (pi: createMarkdownTransform).
func CreateMarkdownTransform(
	messageType string,
	isStreaming bool,
	transformers []core.MarkdownTransformer,
) func(markdown string, availableWidth int) string {
	return func(markdown string, availableWidth int) string {
		return applyMarkdownTransformers(markdown, core.MarkdownTransformContext{
			MessageType:    messageType,
			IsStreaming:    isStreaming,
			AvailableWidth: availableWidth,
		}, transformers)
	}
}

// applyMarkdownTransformers runs each transformer, keeping the current
// markdown on nil returns and recovering transformer panics (pi:
// try/catch — "Keep the current Markdown and continue with the next
// transformer").
func applyMarkdownTransformers(markdown string, context core.MarkdownTransformContext, transformers []core.MarkdownTransformer) string {
	transformedMarkdown := markdown
	for _, transformer := range transformers {
		func() {
			defer func() {
				// Keep the current Markdown and continue with the next
				// transformer.
				recover()
			}()
			transformedMarkdown = transformer(transformedMarkdown, context)
		}()
	}
	return transformedMarkdown
}
