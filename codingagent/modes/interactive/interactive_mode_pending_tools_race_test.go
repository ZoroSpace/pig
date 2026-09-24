package interactive

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
)

// TestHandleEventPendingToolsConcurrentAccess locks the pig-only
// pendingToolsMu guard against the goroutine split pi never had.
//
// pi processes every session event on the single-threaded event loop, so
// handleEvent's m.pendingTools accesses were race-free by construction. pig
// runs the parallel tool batch's run closures (and every settled tool's
// onUpdate, see agent_loop.go executePreparedToolCall) on their own
// goroutines: while the main loop emits tool_execution_end (map delete) for
// one tool, another tool's goroutine can still emit tool_execution_update
// (map read) — a real "fatal error: concurrent map read and map write"
// observed in dogfooding 2026-09-23 (parallel bash tools).
//
// Before the fix this test fails under -race with a DATA RACE on
// m.pendingTools (and intermittently fatally without it).
func TestHandleEventPendingToolsConcurrentAccess(t *testing.T) {
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)
	mode.isInitialized = true
	mode.footer = components.NewFooterComponent(session, nil)
	mode.pendingTools = map[string]*components.ToolExecutionComponent{}

	const rounds = 300
	startEvent := func(id string) *core.AgentSessionEvent {
		return &core.AgentSessionEvent{AgentEvent: agent.AgentEvent{
			Type: agent.AgentEventToolExecutionStart, ToolCallID: id, ToolName: "bash",
			Args: map[string]any{"command": "true"},
		}}
	}
	endEvent := func(id string) *core.AgentSessionEvent {
		return &core.AgentSessionEvent{AgentEvent: agent.AgentEvent{
			Type: agent.AgentEventToolExecutionEnd, ToolCallID: id, ToolName: "bash",
			Result: probeAgentToolResult(),
		}}
	}
	updateEvent := func(id string) *core.AgentSessionEvent {
		return &core.AgentSessionEvent{AgentEvent: agent.AgentEvent{
			Type: agent.AgentEventToolExecutionUpdate, ToolCallID: id, ToolName: "bash",
			Args:          map[string]any{"command": "true"},
			PartialResult: probeAgentToolResult(),
		}}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	// Main-loop stand-in: emits start→end pairs for every even tool (the
	// agent loop emits all starts and collects ends on the loop goroutine).
	// Even/odd id split keeps the two goroutines off the same component —
	// in the live flow a tool's goroutine only starts after the synchronous
	// emit of its own tool_execution_start returns, so same-component
	// concurrency never happens and the components are not goroutine-safe
	// by design (pi: single event loop). The map itself is shared, which is
	// the real race.
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			id := fmt.Sprintf("toolu_race_%d", 2*i)
			mode.handleEvent(context.Background(), startEvent(id))
			mode.handleEvent(context.Background(), endEvent(id))
		}
	}()
	// Settled-tool goroutine stand-in: streams updates for every odd tool
	// from "another" goroutine, overlapping the main loop's start/end map
	// writes. (Odd ids are never started, so the lookup finds no component —
	// the map access itself is the race under test.)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			id := fmt.Sprintf("toolu_race_%d", 2*i+1)
			mode.handleEvent(context.Background(), updateEvent(id))
		}
	}()
	wg.Wait()
}
