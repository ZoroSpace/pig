// footer_usage_test.go — regression tests for the footer usage-stats fix
// (docs/fix/2026-09-11-footer-usage-stats-missing.md).
//
// The runtime appends VALUE-shaped messages into the transcript
// (SessionMessageEntry.Message holds types.AssistantMessage / value form;
// agent_session.go handleAgentEvent asserts the value shape and appends it
// verbatim), while other paths (print mode / json round-trips) produced
// pointer shapes. The footer used a pointer-only type assertion, so every
// live session dropped ALL usage stats (↑ ↓ R W CH $) from the stats line —
// only the context percent ("3.0%/1.0M (auto)") survived. These tests lock
// BOTH shapes, mirroring the dual-shape acceptance GetUsageCostBreakdown
// already documents.
package components

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/samber/lo"
)

// footerUsageView builds a footerSessionView over explicit entries with a
// reasoning model (so the right side shows "model • level").
func footerUsageView(entries []core.SessionEntry) footerSessionView {
	state := &agent.AgentState{
		Model: &types.Model{
			ID:            "glm-5-3-flash",
			Provider:      types.ProviderID("volcengine-plan"),
			ContextWindow: 1_000_000,
			Reasoning:     true,
		},
		ThinkingLevel: agent.ThinkingLevel("medium"),
	}
	percent := 3.0
	return footerSessionView{
		state:       func() *agent.AgentState { return state },
		entries:     func() []core.SessionEntry { return entries },
		sessionName: func() (string, bool) { return "", false },
		cwd:         func() string { return "/tmp/project" },
		contextUsage: func() *core.ContextUsage {
			return &core.ContextUsage{ContextWindow: 1_000_000, Percent: &percent}
		},
		isUsingSubscription: func(provider string) bool { return false },
	}
}

func footerAssistantEntryValue(usage types.Usage) core.SessionEntry {
	return &core.SessionMessageEntry{
		EntryBase: core.EntryBase{Type: "message", ID: "a"},
		Message: types.AssistantMessage{
			Role:  types.RoleAssistant,
			Usage: usage,
		},
	}
}

func footerAssistantEntryPointer(usage types.Usage) core.SessionEntry {
	return &core.SessionMessageEntry{
		EntryBase: core.EntryBase{Type: "message", ID: "a"},
		Message: &types.AssistantMessage{
			Role:  types.RoleAssistant,
			Usage: usage,
		},
	}
}

func footerToolResultEntryValue(usage types.Usage) core.SessionEntry {
	return &core.SessionMessageEntry{
		EntryBase: core.EntryBase{Type: "message", ID: "t"},
		Message: types.ToolResultMessage{
			Role:  types.RoleToolResult,
			Usage: lo.ToPtr(usage),
		},
	}
}

// footerAssertStats runs Render and asserts the stats line (line 2, ANSI
// stripped) contains every want fragment.
func footerAssertStats(t *testing.T, entries []core.SessionEntry, wants ...string) {
	t.Helper()
	footer := NewFooterComponentWithView(nil, footerUsageView(entries), createFooterData(1))
	statsLine := utils.StripAnsi(footer.Render(120)[1])
	for _, want := range wants {
		if !strings.Contains(statsLine, want) {
			t.Fatalf("stats line %q missing %q", statsLine, want)
		}
	}
	return
}

// TestFooterUsageStatsFromValueTypedMessages is THE regression for the
// reported bug: a live session's transcript holds value-shaped assistant
// messages; the footer must show ↑ ↓ R W CH and $ (previously all missing).
func TestFooterUsageStatsFromValueTypedMessages(t *testing.T) {
	initTestTheme(t)
	usage := footerUsageToTypes(footerAssistantUsage{
		input:      58_000,
		output:     2_900,
		cacheRead:  32_000,
		cacheWrite: 1_000,
		costTotal:  0.05,
	})
	// CH = 32000 / (58000 + 32000 + 1000) * 100 = 35.2%
	footerAssertStats(t, []core.SessionEntry{footerAssistantEntryValue(usage)},
		"↑58k", "↓2.9k", "R32k", "W1.0k", "CH35.2%", "$0.050",
		"3.0%/1.0M (auto)")
}

// TestFooterUsageStatsFromPointerTypedMessages guards the pre-existing
// pointer-shape path (print mode / jsonl round-trips) still renders stats.
func TestFooterUsageStatsFromPointerTypedMessages(t *testing.T) {
	initTestTheme(t)
	usage := footerUsageToTypes(footerAssistantUsage{
		input:      58_000,
		output:     2_900,
		cacheRead:  32_000,
		cacheWrite: 1_000,
		costTotal:  0.05,
	})
	footerAssertStats(t, []core.SessionEntry{footerAssistantEntryPointer(usage)},
		"↑58k", "↓2.9k", "R32k", "W1.0k", "CH35.2%", "$0.050")
}

// TestFooterUsageStatsMixedMessageShapes: value and pointer shapes may
// coexist in one transcript (resume + live turns); totals accumulate across
// both, including value-shaped toolResult usage.
func TestFooterUsageStatsMixedMessageShapes(t *testing.T) {
	initTestTheme(t)
	entries := []core.SessionEntry{
		footerAssistantEntryValue(footerUsageToTypes(footerAssistantUsage{
			input: 1_000, output: 100, costTotal: 0.01,
		})),
		footerToolResultEntryValue(footerUsageToTypes(footerAssistantUsage{
			input: 500, output: 50, costTotal: 0.02,
		})),
		footerAssistantEntryPointer(footerUsageToTypes(footerAssistantUsage{
			input: 100, output: 10, costTotal: 0.03,
		})),
	}
	// inputs 1000+500+100=1600 → "1.6k"; outputs 100+50+10=160 → "160";
	// cost 0.06 → "$0.060".
	footerAssertStats(t, entries, "↑1.6k", "↓160", "$0.060")
}

// TestFooterCacheHitRateClearedByZeroPromptAssistant locks pi's
// latestCacheHitRate semantics: the rate is re-derived from EVERY assistant
// message, and an assistant with zero prompt tokens (input+cacheRead+
// cacheWrite == 0) clears it ("undefined" in pi), hiding CH.
func TestFooterCacheHitRateClearedByZeroPromptAssistant(t *testing.T) {
	initTestTheme(t)
	entries := []core.SessionEntry{
		// First assistant: CH would be 500/2000 = 25.0%.
		footerAssistantEntryValue(footerUsageToTypes(footerAssistantUsage{
			input: 1_000, output: 10, cacheRead: 500, cacheWrite: 500,
		})),
		// Last assistant: prompt tokens 0 → rate cleared, no CH segment.
		footerAssistantEntryPointer(footerUsageToTypes(footerAssistantUsage{
			output: 10,
		})),
	}
	footer := NewFooterComponentWithView(nil, footerUsageView(entries), createFooterData(1))
	statsLine := utils.StripAnsi(footer.Render(120)[1])
	if strings.Contains(statsLine, "CH") {
		t.Fatalf("stats line %q should not contain CH after a zero-prompt assistant", statsLine)
	}
	// Cache totals from the first assistant still show.
	for _, want := range []string{"↑1.0k", "↓20", "R500", "W500"} {
		if !strings.Contains(statsLine, want) {
			t.Fatalf("stats line %q missing %q", statsLine, want)
		}
	}
}
