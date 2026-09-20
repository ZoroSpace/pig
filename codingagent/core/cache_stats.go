// cache_stats.go (pi: src/core/cache-stats.ts).
package core

import (
	"github.com/ZoroSpace/pig/ai"
)

// CacheTtlMs is the prompt-cache TTL: idle gaps longer than this are worth
// mentioning as the likely cause of a miss. Anthropic's default cache TTL
// is 5 minutes.
const CacheTtlMs = 5 * 60 * 1000

// noiseFloorTokens: per-turn misses at or below this are cache breakpoint
// granularity noise.
const noiseFloorTokens = 1024

// CacheMiss is a counted cache miss on a single assistant message.
type CacheMiss struct {
	// MissedTokens: prompt tokens that were in the previous turn's prompt
	// but not read from cache.
	MissedTokens int64
	// MissedCost: extra dollars paid vs. a full cache hit; 0 when pricing
	// is unknown.
	MissedCost float64
	// IdleMs: milliseconds since the previous request (which last refreshed
	// the cache).
	IdleMs int64
	// ModelChanged is true when the model changed relative to the previous
	// request.
	ModelChanged bool
}

// CacheWasteTotals is the cumulative cache waste across a session.
type CacheWasteTotals struct {
	MissedTokens int64
	MissedCost   float64
	// MissCount: number of counted misses (turns above the noise floor).
	MissCount int
}

// ModelPriceSource is the minimal pricing lookup, satisfied by ModelRuntime
// (pi: ModelPriceSource). Cost is $/million tokens.
type ModelPriceSource interface {
	GetModel(provider string, modelID string) *ai.Model
}

// previousRequest is the last request seen by the scan; everything in its
// prompt should be cached.
type previousRequest struct {
	promptTokens int64
	modelKey     string
	timestamp    int64
	// reportedCache is sticky: some earlier request in this scan segment
	// reported cache activity. Distinguishes a total miss on a
	// cache-read-only provider (OpenAI-style, writes unreported) from a
	// provider that never reports caching at all.
	reportedCache bool
}

// detectMiss computes the cache miss for one assistant message relative to
// the previous request. Returns nil when nothing is counted: first turn,
// after a reset, no cache activity ever reported (provider without cache
// support), or miss below the noise floor.
func detectMiss(prev *previousRequest, message *ai.AssistantMessage, models ModelPriceSource) *CacheMiss {
	usage := message.Usage
	promptTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	// A zero-cache turn only counts when cache activity was reported before:
	// on cache-read-only providers that is a total miss, while on providers
	// that never report caching it means nothing.
	if prev == nil || promptTokens <= 0 || (usage.CacheRead+usage.CacheWrite == 0 && !prev.reportedCache) {
		return nil
	}

	missedTokens := cacheMin64(prev.promptTokens, promptTokens) - usage.CacheRead
	if missedTokens <= noiseFloorTokens {
		return nil
	}

	// Extra cost = missed tokens billed at the actual paid rate
	// (input/cacheWrite, incl. write premium) instead of the cache-read
	// rate. Missed tokens can only land in the input or cacheWrite buckets,
	// so the paid rate comes straight from this message's own cost
	// breakdown.
	paidTokens := usage.Input + usage.CacheWrite
	var paidPerToken float64
	if paidTokens > 0 {
		paidPerToken = (usage.Cost.Input + usage.Cost.CacheWrite) / float64(paidTokens)
	}
	var readPerToken float64
	if usage.CacheRead > 0 {
		readPerToken = usage.Cost.CacheRead / float64(usage.CacheRead)
	} else if model := models.GetModel(string(message.Provider), message.Model); model != nil {
		readPerToken = model.Cost.CacheRead / 1_000_000
	}

	return &CacheMiss{
		MissedTokens: missedTokens,
		MissedCost:   float64(missedTokens) * cacheMaxFloat64(0, paidPerToken-readPerToken),
		IdleMs:       cacheMaxInt64(0, message.Timestamp-prev.timestamp),
		ModelChanged: string(message.Provider)+"/"+message.Model != prev.modelKey,
	}
}

func asPreviousRequest(message *ai.AssistantMessage, reportedCache bool) *previousRequest {
	usage := message.Usage
	promptTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	if promptTokens <= 0 {
		return nil
	}
	return &previousRequest{
		promptTokens:  promptTokens,
		modelKey:      string(message.Provider) + "/" + message.Model,
		timestamp:     message.Timestamp,
		reportedCache: reportedCache || usage.CacheRead+usage.CacheWrite > 0,
	}
}

type cacheMissScanResult struct {
	prev    *previousRequest
	totals  CacheWasteTotals
	misses  map[*ai.AssistantMessage]*CacheMiss
	missSeq []*CacheMiss
}

// scan walks entries; compaction/branch_summary reset the scan segment (the
// context legitimately changed; the next turn's prompt is new content, not
// re-billed content). Model switches are NOT exempt: they re-bill the full
// prompt and should be counted.
func scanCacheMisses(entries []SessionEntry, models ModelPriceSource) cacheMissScanResult {
	var prev *previousRequest
	totals := CacheWasteTotals{}
	misses := map[*ai.AssistantMessage]*CacheMiss{}
	var missSeq []*CacheMiss

	for _, entry := range entries {
		switch e := entry.(type) {
		case *CompactionEntry, *BranchSummaryEntry:
			prev = nil
			continue
		case *UsageEntry:
			// pi: a cache_warm usage entry reports the prompt size of the
			// warm request, keeping the miss detection baseline current
			// across idle warming refreshes.
			if e.Kind != "cache_warm" || e.Usage == nil {
				continue
			}
			usage := e.Usage
			promptTokens := usage.Input + usage.CacheRead + usage.CacheWrite
			if promptTokens > 0 {
				prev = &previousRequest{
					promptTokens:  promptTokens,
					modelKey:      string(e.Provider) + "/" + e.Model,
					timestamp:     isoToMillis(e.Timestamp),
					reportedCache: true,
				}
			}
			continue
		case *SessionMessageEntry:
			if e.Message.MessageRole() != ai.RoleAssistant {
				continue
			}
			assistant, ok := e.Message.(*ai.AssistantMessage)
			if !ok {
				continue
			}
			miss := detectMiss(prev, assistant, models)
			if miss != nil {
				totals.MissedTokens += miss.MissedTokens
				totals.MissedCost += miss.MissedCost
				totals.MissCount++
				misses[assistant] = miss
				missSeq = append(missSeq, miss)
			}
			reported := prev != nil && prev.reportedCache
			if next := asPreviousRequest(assistant, reported); next != nil {
				prev = next
			}
		}
	}
	return cacheMissScanResult{prev: prev, totals: totals, misses: misses, missSeq: missSeq}
}

// ComputeCacheWaste returns cumulative cache waste across a session:
// prompt tokens that should have been cache reads (they were in the
// previous turn's prompt) but were re-billed.
func ComputeCacheWaste(entries []SessionEntry, models ModelPriceSource) CacheWasteTotals {
	return scanCacheMisses(entries, models).totals
}

// CollectCacheMisses returns all counted cache misses across a session,
// keyed by the assistant message (by pointer) that paid for them. Used to
// re-derive transcript notices when rebuilding the chat from entries
// (resume, post-compaction rebuild). Iteration order is entry order
// (pig: the misses map plus missSeq preserve it).
func CollectCacheMisses(entries []SessionEntry, models ModelPriceSource) (map[*ai.AssistantMessage]*CacheMiss, []*CacheMiss) {
	result := scanCacheMisses(entries, models)
	return result.misses, result.missSeq
}

// DetectCacheMiss detects a cache miss on a just-completed assistant
// message. `entries` must not yet contain `message` (message_end fires
// before persistence).
func DetectCacheMiss(entries []SessionEntry, message *ai.AssistantMessage, models ModelPriceSource) *CacheMiss {
	return detectMiss(scanCacheMisses(entries, models).prev, message, models)
}

func cacheMin64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func cacheMaxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func cacheMaxFloat64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// ModelPriceSourceFunc adapts a function to ModelPriceSource (pig seam for
// ModelRuntime).
type ModelPriceSourceFunc func(provider string, modelID string) *ai.Model

func (f ModelPriceSourceFunc) GetModel(provider string, modelID string) *ai.Model {
	return f(provider, modelID)
}
