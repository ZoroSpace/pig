// model_search.go (pi: modes/interactive/model-search.ts) — model search
// text ranking helpers shared by the /model autocomplete and selector.
//
// pig placement note: pi keeps this in modes/interactive; pig's /model
// selector lives in components and cannot import the interactive package,
// so the canonical implementation lives here and interactive re-exports it
// (declared deviation).
package components

// ModelSearchItem is pi's ModelSearchItem (modes/interactive/model-search.ts).
type ModelSearchItem struct {
	ID       string
	Provider string
	Name     string
}

// GetModelSearchText builds the fuzzy search text for a model (pi:
// getModelSearchText).
func GetModelSearchText(item ModelSearchItem) string {
	name := ""
	if item.Name != "" {
		name = " " + item.Name
	}
	return item.ID + " " + item.Provider + " " + item.Provider + "/" + item.ID + " " + item.Provider + " " + item.ID + name
}

// GetModelSelectorSearchText ranks exact provider-prefixed queries before
// proxy-provider IDs like openrouter/openai/gpt-5 (pi:
// getModelSelectorSearchText).
func GetModelSelectorSearchText(item ModelSearchItem) string {
	name := ""
	if item.Name != "" {
		name = " " + item.Name
	}
	return item.Provider + " " + item.Provider + "/" + item.ID + " " + item.Provider + " " + item.ID + name
}
