// pi: test/model-search.test.ts — getDistinctModelName (#5916).
package components

import "testing"

func TestGetDistinctModelName(t *testing.T) {
	t.Run("hides names that restate the ID", func(t *testing.T) {
		cases := []ModelSearchItem{
			{ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5"},
			{ID: "openai/gpt-oss-120b", Name: "OpenAI: gpt-oss-120b"},
			{ID: "eu.anthropic.claude-sonnet-4-5-20250929-v1:0", Name: "Claude Sonnet 4.5 (EU)"},
			{ID: "gpt-5", Name: ""},
			{ID: "gpt-5"},
		}
		for _, item := range cases {
			if got := GetDistinctModelName(item); got != "" {
				t.Errorf("GetDistinctModelName(%+v) = %q, want empty", item, got)
			}
		}
	})

	t.Run("shows names with words the ID lacks", func(t *testing.T) {
		name := "MiniMax M3 [rasmus@openrouter, parasail]"
		if got := GetDistinctModelName(ModelSearchItem{ID: "minimax/minimax-m3", Name: name}); got != name {
			t.Fatalf("name = %q, want %q", got, name)
		}
		if got := GetDistinctModelName(ModelSearchItem{ID: "gemini-3.1-flash-lite-image", Name: "Nano Banana 2 Lite"}); got != "Nano Banana 2 Lite" {
			t.Fatalf("name = %q", got)
		}
	})

	t.Run("requires single-character words to match a whole ID token", func(t *testing.T) {
		if got := GetDistinctModelName(ModelSearchItem{ID: "mistral-small-2603", Name: "Mistral Small 4"}); got != "Mistral Small 4" {
			t.Fatalf("name = %q", got)
		}
	})
}
