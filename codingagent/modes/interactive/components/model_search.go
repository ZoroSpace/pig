// model_search.go (pi: modes/interactive/model-search.ts) — model search
// text ranking helpers shared by the /model autocomplete and selector.
//
// pig placement note: pi keeps this in modes/interactive; pig's /model
// selector lives in components and cannot import the interactive package,
// so the canonical implementation lives here and interactive re-exports it
// (declared deviation).
package components

import (
	"strings"
	"unicode"
)

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

// splitModelLabel lowercases value and splits it on every run of characters
// that are neither letters nor numbers (pi: splitModelLabel).
func splitModelLabel(value string) []string {
	return strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// GetDistinctModelName returns the model name when it has words the ID does
// not, e.g. a renamed override like "MiniMax M3 [rasmus@openrouter, parasail]"
// for "minimax/minimax-m3". Names that only restate the ID with different
// casing, separators, or a dropped namespace/version suffix ("Claude Sonnet
// 4.5 (EU)" for "eu.anthropic.claude-sonnet-4-5-20250929-v1:0") return "".
func GetDistinctModelName(item ModelSearchItem) string {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		return ""
	}
	idWords := splitModelLabel(item.ID)
	idTokens := make(map[string]bool, len(idWords))
	for _, word := range idWords {
		idTokens[word] = true
	}
	compactID := strings.Join(idWords, "")
	// Single-character words must match a whole ID token: "4" is not covered
	// by "mistral-small-2603".
	isCovered := func(word string) bool {
		return idTokens[word] || (len([]rune(word)) > 1 && strings.Contains(compactID, word))
	}
	for _, word := range splitModelLabel(name) {
		if !isCovered(word) {
			return name
		}
	}
	return ""
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
