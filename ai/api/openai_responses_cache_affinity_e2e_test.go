// pi: test/openai-responses-cache-affinity-e2e.test.ts. Live-API probe; skips
// without OPENAI_API_KEY (pi: describe.skipIf).
package api

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

func TestOpenAIResponsesCacheAffinityE2E(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" || apiKey == "xxx" {
		t.Skip("OPENAI_API_KEY not set")
	}

	t.Run("handles direct OpenAI Responses requests with aligned cache-affinity identifiers", func(t *testing.T) {
		base, ok := ai.MODELS[ai.ProviderOpenAI]["gpt-5.4"]
		if !ok {
			t.Fatal("gpt-5.4 not in catalog")
		}
		model := &base
		sessionID := "0195d6e4-4cf9-7f44-a2d8-f8f7f49ee9d3"

		llmContext := &types.Context{
			SystemPrompt: "You are a helpful assistant. Reply exactly as requested.",
			Messages:     []types.Message{responsesUserMessage("Reply with exactly: openai cache affinity e2e success")},
		}
		options := &types.StreamOptions{}
		options.APIKey = apiKey
		options.SessionID = sessionID

		stream := streamOpenAIResponses(context.Background(), model, llmContext, &OpenAIResponsesOptions{StreamOptions: *options})
		_, result := collectResponsesEvents(t, stream)

		if result.StopReason == types.StopReasonError {
			t.Fatalf("response errored: %s", result.ErrorMessage)
		}
		if result.ErrorMessage != "" {
			t.Fatalf("errorMessage = %q", result.ErrorMessage)
		}
		var text strings.Builder
		for _, block := range result.Content {
			if tb, ok := block.(types.TextContent); ok {
				text.WriteString(tb.Text)
			}
		}
		if !strings.Contains(text.String(), "openai cache affinity e2e success") {
			t.Fatalf("response text = %q", text.String())
		}
	})
}
