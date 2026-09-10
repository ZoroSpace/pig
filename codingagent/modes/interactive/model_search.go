// model_search.go (pi: modes/interactive/model-search.ts) — model search
// text ranking helpers shared by the /model autocomplete and selector.
//
// pig placement note: the canonical implementation lives in components (the
// /model selector consumes it there); this file re-exports for the
// interactive package (pi: same directory import surface).
package interactive

import "github.com/ZoroSpace/pig/codingagent/modes/interactive/components"

// modelSearchItem is pi's ModelSearchItem (modes/interactive/model-search.ts).
type modelSearchItem = components.ModelSearchItem

// GetModelSearchText builds the fuzzy search text for a model (pi:
// getModelSearchText).
func GetModelSearchText(item modelSearchItem) string {
	return components.GetModelSearchText(item)
}

// GetModelSelectorSearchText ranks exact provider-prefixed queries before
// proxy-provider IDs like openrouter/openai/gpt-5 (pi:
// getModelSelectorSearchText).
func GetModelSelectorSearchText(item modelSearchItem) string {
	return components.GetModelSelectorSearchText(item)
}
