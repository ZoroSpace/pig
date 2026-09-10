// chat_viewport_test.go (pi: test/chat-viewport.test.ts, v0.85.1) — the
// shared fullscreen layout assembly: scrollbar defaults and overrides.
package interactive

import (
	"testing"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

func TestChatViewportDefaultsTheTranscriptScrollbarToAutoAndAcceptsOverrides(t *testing.T) {
	automatic := createChatViewport(chatViewportOptions{
		document:        &tui.Container{},
		pendingMessages: &tui.Container{},
		status:          &tui.Container{},
		editor:          &tui.Container{},
		footer:          &tui.Container{},
	})
	hidden := createChatViewport(chatViewportOptions{
		document:        &tui.Container{},
		pendingMessages: &tui.Container{},
		status:          &tui.Container{},
		editor:          &tui.Container{},
		footer:          &tui.Container{},
		scrollbar:       (*core.ScrollViewScrollbar)(scrollbarHiddenPtr()),
	})

	if got := automatic.transcript.Scrollbar(); got != "auto" {
		t.Fatalf("default scrollbar = %q, want auto", got)
	}
	if got := hidden.transcript.Scrollbar(); got != "hidden" {
		t.Fatalf("override scrollbar = %q, want hidden", got)
	}
}

func scrollbarHiddenPtr() *string {
	s := "hidden"
	return &s
}
