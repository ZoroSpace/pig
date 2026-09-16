// Memory-leak probe for the transcript component layer (pig-only diagnostic).
// Drives N simulated turns of user+assistant components through the real
// chat-container path and samples the live heap after forced GC.
//
//   - Retained group: components are added to a shared Container (what the
//     interactive transcript does) — measures per-turn retained bytes.
//   - Dropped group: same construction, references discarded — if the heap
//     still grows, a package-level registry is retaining them (real leak).
//
// Run: go test ./codingagent/modes/interactive/ -run TestMemLeakProbeComponents -v -timeout 120s
package interactive

import (
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

func memSnapshotHeap(t *testing.T) uint64 {
	t.Helper()
	runtime.GC()
	runtime.GC()
	debug.FreeOSMemory()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

func probeMessageText() string {
	para := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 20)
	return para + "\n\n" + para
}

func buildTurnComponents(text string) (*components.UserMessageComponent, *components.AssistantMessageComponent) {
	user := components.NewUserMessageComponent("probe question with padding "+text[:20], &mdThemeProbe, 1, nil)
	assistant := components.NewAssistantMessageComponent(testAssistantMessageForProbe(text), &components.AssistantMessageOptions{
		MarkdownTheme: &mdThemeProbe,
	})
	return user, assistant
}

var mdThemeProbe tui.MarkdownTheme

func probeThemeColors() map[string]theme.ColorValue {
	return map[string]theme.ColorValue{
		"accent": "#ff0000", "dim": "#888888", "muted": "#666666", "text": "#d4d4d4",
		"userMessageBg": "#1a1a2e", "userMessageText": "#ffffff",
		"assistantMessageText": "#ffffff", "toolPendingBg": "#1a1a2e",
		"selectedBg": "#333333", "customMessageBg": "#1a1a2e",
		"searchMatchBg": "#333333", "searchMatchText": "#ffffff",
		"border": "#888888", "borderMuted": "#888888", "borderAccent": "#ff0000",
		"success": "#00ff00", "error": "#ff0000", "warning": "#ffff00",
		"thinkingText": "#888888", "toolPendingText": "#ffffff", "toolSuccessText": "#00ff00",
		"toolErrorText": "#ff0000", "customMessageText": "#ffffff",
		// Markdown inline-code tokens (needed since the clipboard placeholder
		// renders as inline code in the transcript).
		"mdCode": "#00ff00", "mdCodeBg": "#1a1a2e",
		// pig-only imagePlaceholder token (same value as dark.mdLink).
		"imagePlaceholder": "#81a2be",
	}
}

func initProbeTheme(t *testing.T) {
	t.Helper()
	colors := probeThemeColors()
	tl, err := theme.NewTheme(colors, colors, theme.ColorMode256, nil)
	if err != nil {
		t.Fatalf("NewTheme: %v", err)
	}
	theme.SetThemeInstance(tl)
	mdThemeProbe = theme.GetMarkdownTheme()
}

// memProbeGate skips the diagnostic probes unless explicitly requested.
// They are measurement harnesses (docs/fix/2026-09-14-tui-turn-memory-leak.md),
// not regression locks — real renders × hundreds of turns × forced GC make
// them the slowest tests in the suite (~127s under -race, vs ~7s for the
// rest of the package), so the default suite skips them:
//
//	PIG_MEMLEAK_PROBE=1 go test ./codingagent/modes/interactive/ -run 'TestMemLeakProbe' -race -v
func memProbeGate(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("probe; skip in short mode")
	}
	if os.Getenv("PIG_MEMLEAK_PROBE") == "" {
		t.Skip("probe; set PIG_MEMLEAK_PROBE=1 to run")
	}
}

func TestMemLeakProbeComponents(t *testing.T) {
	memProbeGate(t)
	initProbeTheme(t)
	text := probeMessageText()
	const turns = 200

	// --- dropped group: no external references ---
	before := memSnapshotHeap(t)
	for i := 0; i < turns; i++ {
		user, assistant := buildTurnComponents(text)
		_ = user.Render(120)
		_ = assistant.Render(120)
	}
	droppedAfter := memSnapshotHeap(t)

	// --- retained group: added to a live container (transcript semantics) ---
	container := &tui.Container{}
	beforeRetained := memSnapshotHeap(t)
	for i := 0; i < turns; i++ {
		user, assistant := buildTurnComponents(text)
		_ = user.Render(120)
		_ = assistant.Render(120)
		container.AddChild(user)
		container.AddChild(assistant)
		if i > 0 {
			container.AddChild(tui.NewSpacer(1))
		}
	}
	_ = container.Render(120)
	retainedAfter := memSnapshotHeap(t)

	droppedDelta := int64(droppedAfter) - int64(before)
	retainedDelta := int64(retainedAfter) - int64(beforeRetained)
	t.Logf("dropped:  %+8.1f KiB total, %+7.2f KiB/turn", float64(droppedDelta)/1024, float64(droppedDelta)/1024/turns)
	t.Logf("retained: %+8.1f KiB total, %+7.2f KiB/turn", float64(retainedDelta)/1024, float64(retainedDelta)/1024/turns)
	if droppedDelta > 4*1024*int64(turns) {
		t.Errorf("dropped group grew %d bytes over %d turns — package-level retention suspected", droppedDelta, turns)
	}
	t.Logf("container children: %d", len(container.Children))
}

// testAssistantMessageForProbe builds an assistant message without pulling the
// core test helpers into this package.
func testAssistantMessageForProbe(text string) *types.AssistantMessage {
	return &types.AssistantMessage{
		Role:    types.RoleAssistant,
		Content: []types.Content{types.NewTextContent(text)},
		API:     "openai-completions", Provider: "memprobe", Model: "mock-model",
		StopReason: types.StopReasonStop,
	}
}
