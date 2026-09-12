// pi: test/openai-codex-cache-affinity-e2e.test.ts. Live-API probe; skips
// without a stored openai-codex OAuth credential (pi: resolveApiKey reads
// ~/.pi/agent/auth.json and refreshes expired tokens — pig reads the PIG
// auth file and skips when absent/expired rather than refreshing here).
package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

// codexE2EAccessToken resolves the stored openai-codex OAuth access token
// (pi: resolveApiKey("openai-codex") — the OAuth credential's access token).
func codexE2EAccessToken(t *testing.T) string {
	t.Helper()
	if token := os.Getenv("OPENAI_CODEX_API_KEY"); token != "" {
		return token
	}
	dir := os.Getenv("PIG_CODING_AGENT_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("cannot resolve home dir")
		}
		dir = filepath.Join(home, ".pig", "agent")
	}
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Skip("no PIG auth.json with an openai-codex credential")
	}
	var storage map[string]struct {
		Type    string `json:"type"`
		Access  string `json:"access"`
		Expires int64  `json:"expires"`
	}
	if json.Unmarshal(data, &storage) != nil {
		t.Skip("auth.json is not parseable")
	}
	entry, ok := storage["openai-codex"]
	if !ok || entry.Access == "" {
		t.Skip("openai-codex credential not stored")
	}
	if entry.Expires > 0 && entry.Expires < time.Now().UnixMilli() {
		t.Skip("openai-codex access token expired; pi would refresh it here")
	}
	return entry.Access
}

func TestOpenAICodexCacheAffinityE2E(t *testing.T) {
	codexToken := codexE2EAccessToken(t)

	t.Run("handles SSE requests with aligned cache-affinity identifiers", func(t *testing.T) {
		base, ok := ai.MODELS[ai.ProviderOpenAICodex]["gpt-5.5"]
		if !ok {
			t.Fatal("gpt-5.5 not in catalog")
		}
		model := &base
		sessionID := "0195d6e4-4cf9-7f44-a2d8-f8f7f49ee9d3"

		llmContext := &types.Context{
			SystemPrompt: "You are a helpful assistant. Reply exactly as requested.",
			Messages:     []types.Message{responsesUserMessage("Reply with exactly: cache affinity e2e success")},
		}
		options := &OpenAICodexResponsesOptions{}
		options.APIKey = codexToken
		options.SessionID = sessionID
		options.Transport = types.TransportSSE

		stream := streamOpenAICodexResponses(context.Background(), model, llmContext, options)
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
		if !strings.Contains(text.String(), "cache affinity e2e success") {
			t.Fatalf("response text = %q", text.String())
		}
	})
}
