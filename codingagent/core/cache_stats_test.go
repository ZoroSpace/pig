// cache_stats_test.go (pi: test/cache-stats.test.ts).
package core

import (
	"math"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

// cacheStatsModels: $/million tokens; used as cache-read price fallback on
// full-miss turns.
var cacheStatsModels = ModelPriceSourceFunc(func(string, string) *ai.Model {
	return &ai.Model{Cost: types.ModelCost{ModelCostRates: types.ModelCostRates{CacheRead: 0.3}}}
})

func cacheStatsAssistant(options struct {
	input      int64
	cacheRead  int64
	cacheWrite int64
	cost       *types.Cost
	model      string
	timestamp  int64
}) *ai.AssistantMessage {
	cost := options.cost
	if cost == nil {
		cost = &types.Cost{}
	}
	model := options.model
	if model == "" {
		model = "test-model"
	}
	return &ai.AssistantMessage{
		Role:     types.RoleAssistant,
		Content:  []types.Content{},
		API:      types.ApiAnthropicMessages,
		Provider: "test",
		Model:    model,
		Usage: types.Usage{
			Input:       options.input,
			Output:      10,
			CacheRead:   options.cacheRead,
			CacheWrite:  options.cacheWrite,
			TotalTokens: 0,
			Cost:        *cost,
		},
		StopReason: types.StopReasonStop,
		Timestamp:  options.timestamp,
	}
}

func cacheStatsEntry(message *ai.AssistantMessage) SessionEntry {
	return &SessionMessageEntry{
		EntryBase: EntryBase{Type: "message", ID: "x"},
		Message:   message,
	}
}

func cacheStatsCompactionEntry() SessionEntry {
	return &CompactionEntry{EntryBase: EntryBase{Type: "compaction", ID: "c"}}
}

// Turn 1: fresh 100k cache write at $3.75/M
var cacheTurn1 = cacheStatsAssistant(struct {
	input      int64
	cacheRead  int64
	cacheWrite int64
	cost       *types.Cost
	model      string
	timestamp  int64
}{cacheWrite: 100_000, cost: &types.Cost{CacheWrite: 0.375}})

// Turn 2: healthy, everything read back at $0.30/M
var cacheTurn2 = cacheStatsAssistant(struct {
	input      int64
	cacheRead  int64
	cacheWrite int64
	cost       *types.Cost
	model      string
	timestamp  int64
}{cacheRead: 100_000, cacheWrite: 5_000, cost: &types.Cost{CacheRead: 0.03, CacheWrite: 0.019}, timestamp: 60_000})

func TestComputeCacheWaste(t *testing.T) {
	t.Run("accumulates missed tokens and cost across turns", func(t *testing.T) {
		// Turn 3: full miss, previous 105k prompt re-billed at $3.75/M write
		turn3 := cacheStatsAssistant(struct {
			input      int64
			cacheRead  int64
			cacheWrite int64
			cost       *types.Cost
			model      string
			timestamp  int64
		}{cacheWrite: 110_000, cost: &types.Cost{CacheWrite: 0.4125}, timestamp: 120_000})
		totals := ComputeCacheWaste([]SessionEntry{cacheStatsEntry(cacheTurn1), cacheStatsEntry(cacheTurn2), cacheStatsEntry(turn3)}, cacheStatsModels)
		if totals.MissedTokens != 105_000 {
			t.Fatalf("missedTokens = %d, want 105000", totals.MissedTokens)
		}
		// 105k at ($3.75 - $0.30)/M
		if math.Abs(totals.MissedCost-0.36225) > 1e-5 {
			t.Fatalf("missedCost = %v, want ~0.36225", totals.MissedCost)
		}
	})

	t.Run("counts nothing for healthy sessions", func(t *testing.T) {
		totals := ComputeCacheWaste([]SessionEntry{cacheStatsEntry(cacheTurn1), cacheStatsEntry(cacheTurn2)}, cacheStatsModels)
		if totals.MissedTokens != 0 || totals.MissedCost != 0 {
			t.Fatalf("totals = %+v, want zero", totals)
		}
	})

	t.Run("skips the turn after a compaction reset", func(t *testing.T) {
		afterReset := cacheStatsAssistant(struct {
			input      int64
			cacheRead  int64
			cacheWrite int64
			cost       *types.Cost
			model      string
			timestamp  int64
		}{cacheWrite: 20_000, cost: &types.Cost{CacheWrite: 0.075}})
		totals := ComputeCacheWaste([]SessionEntry{cacheStatsEntry(cacheTurn1), cacheStatsCompactionEntry(), cacheStatsEntry(afterReset)}, cacheStatsModels)
		if totals.MissedTokens != 0 {
			t.Fatalf("missedTokens = %d, want 0", totals.MissedTokens)
		}
	})

	t.Run("counts misses caused by model switches", func(t *testing.T) {
		otherModel := cacheStatsAssistant(struct {
			input      int64
			cacheRead  int64
			cacheWrite int64
			cost       *types.Cost
			model      string
			timestamp  int64
		}{cacheWrite: 100_000, cost: &types.Cost{CacheWrite: 0.375}, model: "other-model"})
		totals := ComputeCacheWaste([]SessionEntry{cacheStatsEntry(cacheTurn1), cacheStatsEntry(otherModel)}, cacheStatsModels)
		if totals.MissedTokens != 100_000 {
			t.Fatalf("missedTokens = %d, want 100000", totals.MissedTokens)
		}
		if totals.MissCount != 1 {
			t.Fatalf("missCount = %d, want 1", totals.MissCount)
		}
	})

	t.Run("skips providers that report no cache activity", func(t *testing.T) {
		a := cacheStatsAssistant(struct {
			input      int64
			cacheRead  int64
			cacheWrite int64
			cost       *types.Cost
			model      string
			timestamp  int64
		}{input: 100_000})
		b := cacheStatsAssistant(struct {
			input      int64
			cacheRead  int64
			cacheWrite int64
			cost       *types.Cost
			model      string
			timestamp  int64
		}{input: 110_000})
		totals := ComputeCacheWaste([]SessionEntry{cacheStatsEntry(a), cacheStatsEntry(b)}, cacheStatsModels)
		if totals.MissedTokens != 0 {
			t.Fatalf("missedTokens = %d, want 0", totals.MissedTokens)
		}
	})
}

func TestCollectCacheMisses(t *testing.T) {
	t.Run("maps counted misses to their assistant messages by reference", func(t *testing.T) {
		missTurn := cacheStatsAssistant(struct {
			input      int64
			cacheRead  int64
			cacheWrite int64
			cost       *types.Cost
			model      string
			timestamp  int64
		}{cacheWrite: 110_000, cost: &types.Cost{CacheWrite: 0.4125}, timestamp: 120_000})
		misses, _ := CollectCacheMisses([]SessionEntry{cacheStatsEntry(cacheTurn1), cacheStatsEntry(cacheTurn2), cacheStatsEntry(missTurn)}, cacheStatsModels)
		if len(misses) != 1 {
			t.Fatalf("misses size = %d, want 1", len(misses))
		}
		if miss, ok := misses[missTurn]; !ok || miss.MissedTokens != 105_000 {
			t.Fatalf("miss for missTurn = %+v, want missedTokens 105000", miss)
		}
	})
}

func TestDetectCacheMiss(t *testing.T) {
	t.Run("detects a miss on a just-completed message with idle time", func(t *testing.T) {
		missMessage := cacheStatsAssistant(struct {
			input      int64
			cacheRead  int64
			cacheWrite int64
			cost       *types.Cost
			model      string
			timestamp  int64
		}{cacheWrite: 110_000, cost: &types.Cost{CacheWrite: 0.4125}, timestamp: 600_000})
		miss := DetectCacheMiss([]SessionEntry{cacheStatsEntry(cacheTurn1), cacheStatsEntry(cacheTurn2)}, missMessage, cacheStatsModels)
		if miss == nil {
			t.Fatal("miss = nil, want defined")
		}
		if miss.MissedTokens != 105_000 {
			t.Fatalf("missedTokens = %d, want 105000", miss.MissedTokens)
		}
		if math.Abs(miss.MissedCost-0.36225) > 1e-5 {
			t.Fatalf("missedCost = %v, want ~0.36225", miss.MissedCost)
		}
		// 600s - 60s since the previous request
		if miss.IdleMs != 540_000 {
			t.Fatalf("idleMs = %d, want 540000", miss.IdleMs)
		}
		if miss.ModelChanged {
			t.Fatal("modelChanged = true, want false")
		}
	})

	t.Run("flags model switches on detected misses", func(t *testing.T) {
		otherModel := cacheStatsAssistant(struct {
			input      int64
			cacheRead  int64
			cacheWrite int64
			cost       *types.Cost
			model      string
			timestamp  int64
		}{cacheWrite: 110_000, cost: &types.Cost{CacheWrite: 0.4125}, model: "other-model", timestamp: 120_000})
		miss := DetectCacheMiss([]SessionEntry{cacheStatsEntry(cacheTurn1), cacheStatsEntry(cacheTurn2)}, otherModel, cacheStatsModels)
		if miss == nil {
			t.Fatal("miss = nil, want defined")
		}
		if miss.MissedTokens != 105_000 {
			t.Fatalf("missedTokens = %d, want 105000", miss.MissedTokens)
		}
		if !miss.ModelChanged {
			t.Fatal("modelChanged = false, want true")
		}
	})

	t.Run("returns undefined for healthy turns", func(t *testing.T) {
		healthy := cacheStatsAssistant(struct {
			input      int64
			cacheRead  int64
			cacheWrite int64
			cost       *types.Cost
			model      string
			timestamp  int64
		}{cacheRead: 105_000, cacheWrite: 2_000, cost: &types.Cost{CacheRead: 0.0315, CacheWrite: 0.0075}, timestamp: 120_000})
		if miss := DetectCacheMiss([]SessionEntry{cacheStatsEntry(cacheTurn1), cacheStatsEntry(cacheTurn2)}, healthy, cacheStatsModels); miss != nil {
			t.Fatalf("miss = %+v, want undefined", miss)
		}
	})

	t.Run("returns undefined for the first turn of a session", func(t *testing.T) {
		if miss := DetectCacheMiss(nil, cacheTurn1, cacheStatsModels); miss != nil {
			t.Fatalf("miss = %+v, want undefined", miss)
		}
	})
}
