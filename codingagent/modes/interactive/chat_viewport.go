// chat_viewport.go (pi: modes/interactive/chat-viewport.ts, v0.85.1) —
// shared fullscreen transcript and fixed input-dock layout.
package interactive

import (
	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// chatViewportOptions carries the fixed dock slots (pi:
// ChatViewportOptions; widget slots nil = omitted).
type chatViewportOptions struct {
	document        tui.Component
	pendingMessages tui.Component
	status          tui.Component
	editor          tui.Component
	footer          tui.Component
	widgetsAbove    tui.Component
	widgetsBelow    tui.Component
	scrollbar       *core.ScrollViewScrollbar
}

// chatViewport is the assembled fullscreen layout (pi: ChatViewport).
type chatViewport struct {
	transcript *tui.ScrollView
	root       tui.Component
}

// createChatViewport assembles the transcript scroll view and the fixed
// input dock (pi: createChatViewport).
func createChatViewport(options chatViewportOptions) *chatViewport {
	scrollviewOptions := &tui.ScrollViewOptions{
		Follow:     "end",
		Primary:    true,
		Overscroll: "chain",
	}
	if options.scrollbar != nil {
		scrollviewOptions.Scrollbar = *options.scrollbar
	} else {
		scrollviewOptions.Scrollbar = "auto"
	}
	// Styles resolve the global theme lazily at call time: the layout is
	// built before theme.InitTheme runs (pi's `theme` import is a live
	// binding; pig's global is initialized later in Init).
	scrollviewOptions.ScrollbarTrackStyle = func(text string) string {
		return theme.Fg("scrollbarTrack", text)
	}
	scrollviewOptions.ScrollbarThumbStyle = func(text string) string {
		return theme.Fg("scrollbarThumb", text)
	}
	transcript := tui.NewScrollView(options.document, scrollviewOptions)

	dockChildren := []tui.StackChild{
		tui.StackEntryOf(options.pendingMessages, &tui.StackEntryOptions{Shrink: lo.ToPtr(1), MinSize: 0}),
		tui.StackEntryOf(options.status, &tui.StackEntryOptions{Shrink: lo.ToPtr(1), MinSize: 0}),
	}
	if options.widgetsAbove != nil {
		dockChildren = append(dockChildren, tui.StackEntryOf(options.widgetsAbove, &tui.StackEntryOptions{Shrink: lo.ToPtr(1), MinSize: 0}))
	}
	dockChildren = append(dockChildren, tui.StackEntryOf(options.editor, &tui.StackEntryOptions{Shrink: lo.ToPtr(1), MinSize: 3}))
	if options.widgetsBelow != nil {
		dockChildren = append(dockChildren, tui.StackEntryOf(options.widgetsBelow, &tui.StackEntryOptions{Shrink: lo.ToPtr(1), MinSize: 0}))
	}
	dockChildren = append(dockChildren, tui.StackEntryOf(options.footer, &tui.StackEntryOptions{Shrink: lo.ToPtr(1), MinSize: 0}))
	dock := tui.NewVStack(dockChildren, nil)

	root := tui.NewVStack([]tui.StackChild{
		tui.StackEntryOf(transcript, &tui.StackEntryOptions{Basis: 0, Grow: 1, Shrink: lo.ToPtr(1), MinSize: 1}),
		tui.StackEntryOf(dock, &tui.StackEntryOptions{Basis: "auto", Grow: 0, Shrink: lo.ToPtr(1), MinSize: 1}),
	}, nil)
	return &chatViewport{transcript: transcript, root: root}
}
