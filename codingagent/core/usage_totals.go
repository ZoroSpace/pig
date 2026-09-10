// pi: src/core/usage-totals.ts — usage accumulation and cost breakdown.
package core

import (
	"sort"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

// UsageTotals accumulates token and cost totals (pi: UsageTotals).
type UsageTotals struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Cost       float64
}

// CreateUsageTotals returns zeroed totals (pi: createUsageTotals).
func CreateUsageTotals() UsageTotals {
	return UsageTotals{}
}

// AddUsageToTotals adds one usage to the totals (pi: addUsageToTotals).
func AddUsageToTotals(totals *UsageTotals, usage types.Usage) {
	totals.Input += usage.Input
	totals.Output += usage.Output
	totals.CacheRead += usage.CacheRead
	totals.CacheWrite += usage.CacheWrite
	totals.Cost += usage.Cost.Total
}

// UsageCostBreakdownEntry is one bucket of the cost breakdown (pi:
// UsageCostBreakdownEntry).
type UsageCostBreakdownEntry struct {
	Key    string
	Cost   float64
	Tokens int64
}

// GetUsageCostBreakdown groups attributable assistant usage by model and all
// other usage into a separate bucket (pi: getUsageCostBreakdown). pi iterates
// the totals Map for output; pig keeps an insertion-ordered key list so the
// order is deterministic before sorting.
func GetUsageCostBreakdown(entries []SessionEntry) []UsageCostBreakdownEntry {
	totalsByKey := map[string]*UsageTotals{}
	var keys []string

	for _, entry := range entries {
		var key string
		var usage *types.Usage
		switch e := entry.(type) {
		case *SessionMessageEntry:
			// pi dispatches on message.role; pig's transcript holds both value
			// messages (runtime appends) and pointer messages (jsonl decode),
			// so both shapes are accepted.
			switch message := e.Message.(type) {
			case types.AssistantMessage:
				modelID := message.ResponseModel
				if modelID == "" {
					modelID = message.Model
				}
				key = string(message.Provider) + "/" + modelID
				usage = &message.Usage
			case *types.AssistantMessage:
				modelID := message.ResponseModel
				if modelID == "" {
					modelID = message.Model
				}
				key = string(message.Provider) + "/" + modelID
				usage = &message.Usage
			case types.ToolResultMessage:
				if message.Usage != nil {
					key = "Tools/summaries"
					usage = message.Usage
				}
			case *types.ToolResultMessage:
				if message.Usage != nil {
					key = "Tools/summaries"
					usage = message.Usage
				}
			}
		case *BranchSummaryEntry:
			key = "Tools/summaries"
			usage = e.Usage
		case *CompactionEntry:
			key = "Tools/summaries"
			usage = e.Usage
		}
		if key == "" || usage == nil {
			continue
		}
		totals, ok := totalsByKey[key]
		if !ok {
			fresh := CreateUsageTotals()
			totals = &fresh
			totalsByKey[key] = totals
			keys = append(keys, key)
		}
		AddUsageToTotals(totals, *usage)
	}

	out := make([]UsageCostBreakdownEntry, 0, len(keys))
	for _, key := range keys {
		totals := totalsByKey[key]
		tokens := totals.Input + totals.Output + totals.CacheRead + totals.CacheWrite
		if totals.Cost <= 0 && tokens <= 0 {
			continue
		}
		out = append(out, UsageCostBreakdownEntry{Key: key, Cost: totals.Cost, Tokens: tokens})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Cost > out[j].Cost })
	return out
}

// keep the agent import anchored: SessionMessageEntry.Message is the
// agent.AgentMessage alias.
var _ agent.AgentMessage = (*types.AssistantMessage)(nil)
