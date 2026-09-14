// Streaming-path memory probe: does the per-chunk UpdateContent chain
// (real interactive path) retain more per turn than the one-shot component
// construction measured in TestMemLeakProbeComponents?
//
// Run: go test ./codingagent/modes/interactive/ -run TestMemLeakProbeStreaming -v -timeout 120s
package interactive

import (
	"runtime"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/tui"
)

func TestMemLeakProbeStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("probe; skip in short mode")
	}
	initProbeTheme(t)

	text := probeMessageText()
	const turns = 200
	const chunks = 9 // mock stream pushes 9 chunks per turn

	container := &tui.Container{}
	before := memSnapshotHeap(t)
	for i := 0; i < turns; i++ {
		user := components.NewUserMessageComponent("probe question "+strings.Repeat("p", 100), &mdThemeProbe, 1, nil)
		_ = user.Render(120)
		container.AddChild(user)
		container.AddChild(tui.NewSpacer(1))

		assistant := components.NewAssistantMessageComponent(nil, &components.AssistantMessageOptions{
			MarkdownTheme: &mdThemeProbe,
		})
		// streaming frames: growing partial, one UpdateContent per chunk
		for c := 1; c <= chunks-1; c++ {
			partial := &types.AssistantMessage{
				Role:    types.RoleAssistant,
				Content: []types.Content{types.NewTextContent(text[:len(text)*c/chunks])},
				API:     "openai-completions", Provider: "memprobe", Model: "mock-model",
			}
			assistant.UpdateContent(partial, true)
			_ = assistant.Render(120)
		}
		final := testAssistantMessageForProbe(text)
		assistant.UpdateContent(final, false)
		_ = assistant.Render(120)
		container.AddChild(assistant)
	}
	_ = container.Render(120)
	after := memSnapshotHeap(t)

	delta := int64(after) - int64(before)
	t.Logf("streaming retained: %+8.1f KiB total, %+7.2f KiB/turn", float64(delta)/1024, float64(delta)/1024/turns)
	t.Logf("children: %d", len(container.Children))
	_ = runtime.NumGoroutine()
}
