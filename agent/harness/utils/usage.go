// pi: harness/utils/usage.ts — usage aggregation helpers.
package utils

import "github.com/ZoroSpace/pig/ai/types"

// EmptyUsage returns the zero usage (pi: emptyUsage).
func EmptyUsage() types.Usage {
	return types.Usage{}
}

// AddUsage sums two usage records (pi: addUsage). Optional members
// (cacheWrite1h, reasoning) survive only when present on at least one side.
func AddUsage(left, right types.Usage) types.Usage {
	sum := types.Usage{
		Input:       left.Input + right.Input,
		Output:      left.Output + right.Output,
		CacheRead:   left.CacheRead + right.CacheRead,
		CacheWrite:  left.CacheWrite + right.CacheWrite,
		TotalTokens: left.TotalTokens + right.TotalTokens,
		Cost: types.Cost{
			Input:      left.Cost.Input + right.Cost.Input,
			Output:     left.Cost.Output + right.Cost.Output,
			CacheRead:  left.Cost.CacheRead + right.Cost.CacheRead,
			CacheWrite: left.Cost.CacheWrite + right.Cost.CacheWrite,
			Total:      left.Cost.Total + right.Cost.Total,
		},
	}
	if left.CacheWrite1h != nil || right.CacheWrite1h != nil {
		v := addOpt(left.CacheWrite1h, right.CacheWrite1h)
		sum.CacheWrite1h = &v
	}
	if left.Reasoning != nil || right.Reasoning != nil {
		v := addOpt(left.Reasoning, right.Reasoning)
		sum.Reasoning = &v
	}
	return sum
}

// addOpt sums two optional int64s treating nil as 0 (pi's `?? 0` addition).
func addOpt(a, b *int64) int64 {
	var sum int64
	if a != nil {
		sum += *a
	}
	if b != nil {
		sum += *b
	}
	return sum
}
