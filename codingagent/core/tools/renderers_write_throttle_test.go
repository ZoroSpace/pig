// renderers_write_throttle_test.go — regression tests for the pig-only
// streaming-preview throttle (docs/fix/2026-09-30-write-preview-streaming-flicker.md).
//
// Symptom: while tool args stream fast, every args delta re-rendered the
// write preview (growing tail line + "N more lines" counter); on terminals
// without DEC 2026 synchronized output (tmux) the differential renderer's
// per-line erase+rewrite was visible as strobing of exactly the write block.
// Fix: writeRenderCall time-throttles partial updates; ArgsComplete, first
// render and expand toggles bypass the throttle.
package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

type writeThrottleEnv struct {
	interval time.Duration
	now      time.Time
}

func setupWriteThrottle(t *testing.T) *writeThrottleEnv {
	t.Helper()
	env := &writeThrottleEnv{interval: 200 * time.Millisecond, now: time.Unix(1_000_000, 0)}
	oldInterval, oldClock := writePreviewThrottleInterval, writePreviewClock
	writePreviewThrottleInterval = env.interval
	writePreviewClock = func() time.Time { return env.now }
	t.Cleanup(func() {
		writePreviewThrottleInterval = oldInterval
		writePreviewClock = oldClock
	})
	theme.InitTheme("dark", false)
	return env
}

func (e *writeThrottleEnv) advance(d time.Duration) { e.now = e.now.Add(d) }

func writeRender(args map[string]any, last tui.Component, argsComplete bool, expanded bool) *WriteCallRenderComponent {
	component := writeRenderCall(args, theme.Current(), ToolRenderContext{
		Args:          args,
		LastComponent: last,
		ArgsComplete:  argsComplete,
		Expanded:      expanded,
		Cwd:           "/tmp",
	})
	return component.(*WriteCallRenderComponent)
}

func writePreviewBody(component *WriteCallRenderComponent) string {
	return strings.Join(component.Render(200), "\n")
}

func writePartialArgs(content string) map[string]any {
	return map[string]any{"path": "/tmp/pig-write-throttle.txt", "content": content}
}

// Throttled partial updates leave the preview text (and thus the terminal
// writes for the region) untouched until the interval elapses.
func TestWritePreviewPartialUpdatesThrottled(t *testing.T) {
	env := setupWriteThrottle(t)

	first := writeRender(writePartialArgs("line1\nline2"), nil, false, false)
	if !strings.Contains(writePreviewBody(first), "line2") {
		t.Fatalf("first render missing preview content: %q", writePreviewBody(first))
	}
	firstFrame := writePreviewBody(first)

	// +50ms: inside the throttle window — preview must stay stale.
	env.advance(50 * time.Millisecond)
	stale := writeRender(writePartialArgs("line1\nline2\nline3"), first, false, false)
	if got := writePreviewBody(stale); got != firstFrame {
		t.Fatalf("throttled update changed the preview:\n got %q\nwant %q", got, firstFrame)
	}

	// +250ms past the last applied update: the next delta applies.
	env.advance(250 * time.Millisecond)
	applied := writeRender(writePartialArgs("line1\nline2\nline3\nline4"), stale, false, false)
	if got := writePreviewBody(applied); !strings.Contains(got, "line4") {
		t.Fatalf("post-throttle update did not apply new content: %q", got)
	}
}

// ArgsComplete always bypasses the throttle so the final preview (full
// re-highlight) appears immediately when the args finish streaming.
func TestWritePreviewArgsCompleteBypassesThrottle(t *testing.T) {
	env := setupWriteThrottle(t)

	first := writeRender(writePartialArgs("line1\nline2"), nil, false, false)
	firstFrame := writePreviewBody(first)

	env.advance(10 * time.Millisecond)
	final := writeRender(writePartialArgs("line1\nline2\nline3"), first, true, false)
	if got := writePreviewBody(final); !strings.Contains(got, "line3") {
		t.Fatalf("ArgsComplete update was throttled away: %q (previous %q)", got, firstFrame)
	}
}

// Toggling expand inside the throttle window applies immediately, otherwise
// ctrl+o would feel dead for up to one interval.
func TestWritePreviewExpandBypassesThrottle(t *testing.T) {
	env := setupWriteThrottle(t)

	content := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12"
	first := writeRender(writePartialArgs(content), nil, false, false)
	if !strings.Contains(writePreviewBody(first), "more lines") {
		t.Fatalf("collapsed preview should carry the counter line: %q", writePreviewBody(first))
	}

	env.advance(50 * time.Millisecond)
	expanded := writeRender(writePartialArgs(content), first, false, true)
	body := writePreviewBody(expanded)
	if strings.Contains(body, "more lines") {
		t.Fatalf("expand toggle was throttled away: %q", body)
	}
	if !strings.Contains(body, "line12") {
		t.Fatalf("expanded preview missing tail content: %q", body)
	}
}
