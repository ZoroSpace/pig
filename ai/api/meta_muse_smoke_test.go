// Meta Muse smoke (pi: test/stream.test.ts "Meta Provider (muse-spark-1.3
// via OpenAI Responses)" e2e describe — pig's real-API verification channel
// follows the other smoke files; see docs/handoffs/verify-matrix.md). Run
// with:
//
//	META_API_KEY=... go test -tags=smoke ./ai/api/ -run TestMetaMuseSmoke -v
//
// The key is read from the environment and never written to disk.
//go:build smoke

package api

import (
	"context"
	"os"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

func metaMuseSmokeModel(t *testing.T) *types.Model {
	t.Helper()
	model, ok := ai.MODELS[ai.ProviderMeta]["muse-spark-1.3"]
	if !ok {
		t.Fatal("meta muse-spark-1.3 missing from the builtin catalog")
	}
	return &model
}

func TestMetaMuseSmoke(t *testing.T) {
	if key := os.Getenv("META_API_KEY"); key == "" || key == "xxx" {
		t.Skip("META_API_KEY not set")
	}
	model := metaMuseSmokeModel(t)

	options := &OpenAIResponsesOptions{}
	options.APIKey = os.Getenv("META_API_KEY")
	options.MaxTokens = lo.ToPtr(int64(32))
	llmContext := utils.NormalizeContext(&types.Context{
		SystemPrompt: "You are a helpful assistant. Answer with one word.",
		Messages:     []types.Message{responsesUserMessage("Say pong.")},
	})

	stream := streamOpenAIResponses(context.Background(), model, llmContext, options)
	_, result := collectResponsesEvents(t, stream)
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %v (%s), want stop", result.StopReason, result.ErrorMessage)
	}
	if len(result.Content) == 0 {
		t.Fatal("empty response content")
	}
	t.Logf("reply: %s | usage: %+v", utils.ContentText(result.Content, ""), result.Usage)
}
