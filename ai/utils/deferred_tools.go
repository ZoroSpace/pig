// pi: utils/deferred-tools.ts.
package utils

import "github.com/ZoroSpace/pig/ai/types"

// ToolNameNormalizer normalizes a tool name for deferred matching (pi:
// ToolNameNormalizer).
type ToolNameNormalizer func(name string) string

// DeferredToolPlacement splits current tools into prefix ("immediate") and
// transcript-loaded ("deferred") definitions (pi: the { immediate, deferred }
// return of splitDeferredTools). pi's deferred Map preserves first-appearance
// insertion order; Go maps have none, so Deferred is an ordered slice and
// DeferredNames carries the normalized names as a set.
type DeferredToolPlacement struct {
	Immediate []types.Tool
	Deferred  []types.Tool
	// DeferredNames is the set of normalized deferred tool names.
	DeferredNames map[string]struct{}
}

// SplitDeferredTools splits tools into prefix and transcript-loaded
// definitions (pi: splitDeferredTools). When enabled is false every unique
// tool is immediate. Deferred tools are those whose normalized name appears
// in a tool result's AddedToolNames without a preceding assistant tool call
// of the same name.
func SplitDeferredTools(context *types.Context, enabled bool, normalizeName ToolNameNormalizer) DeferredToolPlacement {
	if normalizeName == nil {
		normalizeName = func(name string) string { return name }
	}
	// uniqueTools dedupes by normalized name; a later duplicate replaces the
	// earlier value but keeps its position (pi: Map.set semantics).
	uniqueTools := map[string]types.Tool{}
	var uniqueOrder []string
	for _, tool := range context.Tools {
		name := normalizeName(tool.Name)
		if _, seen := uniqueTools[name]; !seen {
			uniqueOrder = append(uniqueOrder, name)
		}
		uniqueTools[name] = tool
	}
	placement := DeferredToolPlacement{DeferredNames: map[string]struct{}{}}
	if !enabled {
		for _, name := range uniqueOrder {
			placement.Immediate = append(placement.Immediate, uniqueTools[name])
		}
		return placement
	}

	deferredNamesSet := map[string]struct{}{}
	usedNames := map[string]struct{}{}
	for _, message := range context.Messages {
		switch m := message.(type) {
		case types.AssistantMessage:
			for _, block := range m.Content {
				if block.ContentKind() == types.KindToolCall {
					usedNames[normalizeName(block.(types.ToolCall).Name)] = struct{}{}
				}
			}
		case types.ToolResultMessage:
			for _, name := range m.AddedToolNames {
				normalizedName := normalizeName(name)
				if _, used := usedNames[normalizedName]; !used {
					deferredNamesSet[normalizedName] = struct{}{}
				}
			}
		}
	}

	for _, name := range uniqueOrder {
		tool := uniqueTools[name]
		if _, deferred := deferredNamesSet[name]; deferred {
			placement.Deferred = append(placement.Deferred, tool)
			placement.DeferredNames[name] = struct{}{}
		} else {
			placement.Immediate = append(placement.Immediate, tool)
		}
	}
	return placement
}
