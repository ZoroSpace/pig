// status_indicator.go (pi: components/status-indicator.ts) — the streaming
// status indicators.
package components

import (
	"strings"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// StatusIndicatorKind is pi's StatusIndicatorKind union.
type StatusIndicatorKind string

const (
	StatusKindWorking       StatusIndicatorKind = "working"
	StatusKindRetry         StatusIndicatorKind = "retry"
	StatusKindCompaction    StatusIndicatorKind = "compaction"
	StatusKindBranchSummary StatusIndicatorKind = "branchSummary"
)

// workingIndicatorToLoader converts the extension options (pi passes the
// WorkingIndicatorOptions straight through; the shapes are structurally
// identical).
func workingIndicatorToLoader(indicator *core.WorkingIndicatorOptions) *tui.LoaderIndicatorOptions {
	if indicator == nil {
		return nil
	}
	return &tui.LoaderIndicatorOptions{Frames: indicator.Frames, IntervalMs: indicator.IntervalMs}
}

// StatusIndicator is a Loader tagged with its kind (pi: class
// StatusIndicator extends Loader). The Loader is embedded by POINTER:
// NewLoader starts its animation goroutine in the constructor, so a by-value
// embed would leave the ticker mutating a discarded copy while the rendered
// instance froze on the first frame (the streaming-spinner freeze bug).
// pi's inheritance maps to the shared instance behind the pointer.
type StatusIndicator struct {
	*tui.Loader

	Kind StatusIndicatorKind
}

// NewStatusIndicator builds the indicator (pi: constructor).
func NewStatusIndicator(
	kind StatusIndicatorKind,
	ui tui.TUI,
	spinnerColorFn func(string) string,
	messageColorFn func(string) string,
	message string,
	indicator *core.WorkingIndicatorOptions,
) *StatusIndicator {
	return &StatusIndicator{
		Loader: tui.NewLoader(ui, spinnerColorFn, messageColorFn, message, workingIndicatorToLoader(indicator)),
		Kind:   kind,
	}
}

// Dispose stops the loader animation (pi: dispose).
func (s *StatusIndicator) Dispose() {
	s.Stop()
}

// StatusBorderRenderer is the editor-top-border embed surface (pi v0.86.0:
// renderInBorder/renderSpinnerInBorder moved from WorkingStatusIndicator to
// StatusIndicator, so every indicator subtype embeds — see the new
// interactive-tui/status-indicator tests). pig surfaces the method set as an
// interface because Go subtypes are not assignable to the base pointer type.
type StatusBorderRenderer interface {
	RenderInBorder(width int) string
	RenderSpinnerInBorder(width int) string
}

// RenderInBorder renders the indicator line for embedding into an editor
// border of the given width (pi: renderInBorder, v0.86.0 on the base class).
// Strips the loader's leading blank line and trailing padding.
func (s *StatusIndicator) RenderInBorder(width int) string {
	lines := s.Render(width + 2)
	line := ""
	if len(lines) > 1 {
		line = lines[1]
	}
	var trimmed string
	if strings.HasPrefix(line, " ") {
		trimmed = strings.TrimRight(line[1:], " ")
	} else {
		trimmed = strings.TrimRight(line, " ")
	}
	return tui.TruncateToWidth(trimmed, width, "", false)
}

// RenderSpinnerInBorder renders only the spinner frame, for the narrow
// fallback when the full message cannot fit (pi: renderSpinnerInBorder,
// v0.86.0 on the base class).
func (s *StatusIndicator) RenderSpinnerInBorder(width int) string {
	return tui.TruncateToWidth(s.GetRenderedIndicator(), width, "", false)
}

// WorkingStatusIndicator is the default streaming indicator (pi: class
// WorkingStatusIndicator). Value-embeds StatusIndicator: the Loader state
// lives behind the embedded pointer, so copies are safe (same reasoning as
// RetryStatusIndicator).
type WorkingStatusIndicator struct {
	StatusIndicator
}

// NewWorkingStatusIndicator builds the working indicator (pi: constructor;
// colorFn nil = default accent/muted coloring; when set it colors both the
// spinner and the message — used to match the editor border color when the
// indicator is embedded in the editor top border).
func NewWorkingStatusIndicator(ui tui.TUI, message string, indicator *core.WorkingIndicatorOptions, colorFn func(string) string) *WorkingStatusIndicator {
	spinnerColorFn := func(spinner string) string { return theme.Fg("accent", spinner) }
	messageColorFn := func(text string) string { return theme.Fg("muted", text) }
	if colorFn != nil {
		spinnerColorFn = colorFn
		messageColorFn = colorFn
	}
	return &WorkingStatusIndicator{
		StatusIndicator: *NewStatusIndicator(
			StatusKindWorking,
			ui,
			spinnerColorFn,
			messageColorFn,
			message,
			indicator,
		),
	}
}

// SetIndicator replaces the animation options (pi: setIndicator override on
// WorkingStatusIndicator; nil restores the default spinner). pi v0.86.0
// dropped this override and calls setIndicator on the base; pig keeps the
// WorkingIndicatorOptions → loader-options conversion here because only
// working indicators receive them.
func (w *WorkingStatusIndicator) SetIndicator(indicator *core.WorkingIndicatorOptions) {
	w.Loader.SetIndicator(workingIndicatorToLoader(indicator))
}

// RetryStatusIndicator counts down until the retry fires (pi: class
// RetryStatusIndicator).
type RetryStatusIndicator struct {
	StatusIndicator

	countdown *CountdownTimer
}

// NewRetryStatusIndicator builds the retry indicator (pi: constructor).
func NewRetryStatusIndicator(ui tui.TUI, attempt int, maxAttempts int, delayMs int) *RetryStatusIndicator {
	retryMessage := func(seconds int) string {
		return "Retrying (" + itoa(attempt) + "/" + itoa(maxAttempts) + ") in " + itoa(seconds) + "s... (" + tools.KeyText("app.interrupt") + " to cancel)"
	}
	r := &RetryStatusIndicator{
		StatusIndicator: *NewStatusIndicator(
			StatusKindRetry,
			ui,
			func(spinner string) string { return theme.Fg("warning", spinner) },
			func(text string) string { return theme.Fg("muted", text) },
			retryMessage(ceilDiv(delayMs, 1000)),
			nil,
		),
	}
	r.countdown = NewCountdownTimer(
		delayMs,
		ui,
		func(seconds int) {
			r.SetMessage(retryMessage(seconds))
		},
		func() {
			r.countdown = nil
		},
	)
	return r
}

// Dispose stops the countdown and the loader (pi: dispose override).
func (r *RetryStatusIndicator) Dispose() {
	if r.countdown != nil {
		r.countdown.Dispose()
		r.countdown = nil
	}
	r.StatusIndicator.Dispose()
}

// CompactionStatusReason is pi's CompactionStatusReason union.
type CompactionStatusReason string

const (
	CompactionReasonManual    CompactionStatusReason = "manual"
	CompactionReasonThreshold CompactionStatusReason = "threshold"
	CompactionReasonOverflow  CompactionStatusReason = "overflow"
)

// NewCompactionStatusIndicator builds the compaction indicator (pi: class
// CompactionStatusIndicator). A non-nil colorFn overrides both the spinner and
// text colors (used when the indicator is embedded in the editor border).
func NewCompactionStatusIndicator(ui tui.TUI, reason CompactionStatusReason, colorFn func(string) string) *StatusIndicator {
	cancelHint := "(" + tools.KeyText("app.interrupt") + " to cancel)"
	var label string
	if reason == CompactionReasonManual {
		label = "Compacting context... " + cancelHint
	} else {
		prefix := ""
		if reason == CompactionReasonOverflow {
			prefix = "Context overflow detected, "
		}
		label = prefix + "Auto-compacting... " + cancelHint
	}
	spinnerFg := colorFn
	if spinnerFg == nil {
		spinnerFg = func(spinner string) string { return theme.Fg("accent", spinner) }
	}
	textFg := colorFn
	if textFg == nil {
		textFg = func(text string) string { return theme.Fg("muted", text) }
	}
	return NewStatusIndicator(
		StatusKindCompaction,
		ui,
		spinnerFg,
		textFg,
		label,
		nil,
	)
}

// NewBranchSummaryStatusIndicator builds the branch-summary indicator (pi:
// class BranchSummaryStatusIndicator).
func NewBranchSummaryStatusIndicator(ui tui.TUI) *StatusIndicator {
	return NewStatusIndicator(
		StatusKindBranchSummary,
		ui,
		func(spinner string) string { return theme.Fg("accent", spinner) },
		func(text string) string { return theme.Fg("muted", text) },
		"Summarizing branch... ("+tools.KeyText("app.interrupt")+" to cancel)",
		nil,
	)
}

// IdleStatus renders two blank lines while idle (pi: class IdleStatus).
type IdleStatus struct{}

// Invalidate has no cached state (pi: invalidate).
func (i *IdleStatus) Invalidate() {}

// Render draws the blank filler lines (pi: render).
func (i *IdleStatus) Render(width int) []string {
	emptyLine := ""
	for j := 0; j < width; j++ {
		emptyLine += " "
	}
	return []string{emptyLine, emptyLine}
}

// itoa is strconv.Itoa shorthand.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// IndicatorKind exposes the indicator kind through the interface the
// interactive shell consumes (pig addition; pi reads the `kind` property).
func (s *StatusIndicator) IndicatorKind() StatusIndicatorKind { return s.Kind }

// SetIndicator replaces the animation options (pi: setIndicator override on
// WorkingStatusIndicator; nil restores the default spinner).
func (s *StatusIndicator) SetIndicator(indicator *core.WorkingIndicatorOptions) {
	s.Loader.SetIndicator(workingIndicatorToLoader(indicator))
}
