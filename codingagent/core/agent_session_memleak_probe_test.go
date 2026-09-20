// Memory-leak probe for the AgentSession per-turn path (pig-only diagnostic,
// not a pi test port). Drives N mock prompts against an in-memory session and
// samples live heap + goroutines after a forced GC each turn, so retained
// allocations (a real leak) are distinguishable from uncollected garbage.
//
// Run: go test ./codingagent/core/ -run TestMemLeakProbeTurns -v -timeout 120s
package core

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

// memProbeStream returns a small but realistic assistant reply each turn
// (several paragraphs of markdown-ish text plus usage), similar in shape to a
// real streaming turn.
func memProbeStream(replyBytes int) agent.StreamFn {
	return func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		stream := types.NewAssistantMessageEventStream()
		go func() {
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart})
			// incremental partial message, mirrors the real adapter path:
			// one Partial event per chunk with a growing text content.
			partial := &types.AssistantMessage{
				Role: types.RoleAssistant, Content: []types.Content{},
				API: "openai-completions", Provider: model.Provider, Model: model.ID,
			}
			chunk := strings.Repeat("x", 512)
			for i := 0; i < replyBytes/512; i++ {
				text := string(make([]byte, 0, len(chunk)*(i+1)))
				text += chunk
				partial.Content = []types.Content{types.NewTextContent(strings.Repeat(chunk, i+1))}
				_ = text
				stream.Push(types.AssistantMessageEvent{Type: types.EventTextDelta, ContentIndex: 0, Delta: chunk, Partial: partial})
			}
			message := testAssistantMessage(strings.Repeat(chunk, replyBytes/512))
			message.Usage = types.Usage{Input: 1000, Output: 500, Cost: types.Cost{Total: 0.001}}
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &message})
			stream.End()
		}()
		return stream
	}
}

// snapshot mem stats after a full GC (two passes so finalizers/drops settle).
func memSnapshot() (heapAlloc uint64, heapObjects uint64, goroutines int) {
	runtime.GC()
	runtime.GC()
	debug.FreeOSMemory()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc, m.HeapObjects, runtime.NumGoroutine()
}

func TestMemLeakProbeTurns(t *testing.T) {
	if testing.Short() {
		t.Skip("probe; skip in short mode")
	}
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		StreamFn:  memProbeStream(4096),
		GetAPIKey: true,
	})
	ctx := context.Background()

	const turns = 40
	for i := 0; i < turns; i++ {
		if err := session.Prompt(ctx, fmt.Sprintf("probe question %d with some padding text to make the user message realistic", i), nil); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
		waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := session.WaitForIdle(waitCtx); err != nil {
			t.Fatalf("turn %d wait: %v", i, err)
		}
		cancel()

		// sample at 1, 2, 5, 10, 20, 40
		if i == 0 || i == 1 || i == 4 || i == 9 || i == 19 || i == turns-1 {
			heap, objs, gs := memSnapshot()
			t.Logf("turn %2d: heapAlloc=%7d (%5.1f KiB) heapObjects=%7d goroutines=%d",
				i+1, heap, float64(heap)/1024, objs, gs)
		}
	}
}
