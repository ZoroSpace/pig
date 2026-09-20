// pi: test/google-thinking-disable.test.ts.
//
// Live-API probe; every describe block skips without its key (pi:
// describe.skipIf). pi's { retry: 2, timeout: 30000 } per-test options have
// no Go equivalent — the probes run once.
package api

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

type googleDisableRun struct {
	thinkingEventCount int
	thinkingCharCount  int
	text               string
	outputTokens       int64
	contentTypes       []string
}

func googleDisableContext() *types.TranscriptContext {
	return utils.NormalizeContext(&types.Context{
		SystemPrompt: "You are a precise assistant. Follow the requested output format exactly.",
		Messages: []types.Message{
			types.UserMessage{
				Role: types.RoleUser,
				Content: types.UserContent{String: "Before replying, carefully solve 36863 * 5279 internally. " +
					"Then reply with the word pong repeated exactly 40 times, separated by single spaces. Do not add any other text."},
				Timestamp: time.Now().UnixMilli(),
			},
		},
	})
}

func googleCountPongs(text string) int {
	return len(regexp.MustCompile(`\bpong\b`).FindAllString(text, -1))
}

func googleRunWithoutReasoning(t *testing.T, model *types.Model, maxTokens int64, extra func(*types.SimpleStreamOptions)) googleDisableRun {
	t.Helper()
	options := &types.SimpleStreamOptions{}
	options.MaxTokens = &maxTokens
	zero := 0.0
	options.Temperature = &zero
	if extra != nil {
		extra(options)
	}

	s := ai.StreamSimple(context.Background(), model, &ai.Context{Messages: googleDisableContext().Messages}, options)

	var run googleDisableRun
	for event := range s.Events() {
		switch event.Type {
		case types.EventThinkingStart, types.EventThinkingEnd:
			run.thinkingEventCount++
		case types.EventThinkingDelta:
			run.thinkingEventCount++
			run.thinkingCharCount += len(event.Delta)
		}
	}
	response := s.Result()
	if response.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %s (%s), want stop", response.StopReason, response.ErrorMessage)
	}
	var texts []string
	for _, block := range response.Content {
		switch b := block.(type) {
		case types.TextContent:
			texts = append(texts, b.Text)
		case *types.TextContent:
			texts = append(texts, b.Text)
		}
	}
	run.text = strings.TrimSpace(strings.Join(texts, ""))
	run.outputTokens = response.Usage.Output
	for _, block := range response.Content {
		run.contentTypes = append(run.contentTypes, string(block.ContentKind()))
	}
	return run
}

func googleExpectThinkingDisabled(t *testing.T, model *types.Model, maxTokens int64, minPongs int64, extra func(*types.SimpleStreamOptions)) {
	t.Helper()
	result := googleRunWithoutReasoning(t, model, maxTokens, extra)

	if result.thinkingEventCount != 0 {
		t.Errorf("thinkingEventCount = %d, want 0", result.thinkingEventCount)
	}
	if result.thinkingCharCount != 0 {
		t.Errorf("thinkingCharCount = %d, want 0", result.thinkingCharCount)
	}
	for _, contentType := range result.contentTypes {
		if contentType == "thinking" {
			t.Error("content includes a thinking block")
		}
	}
	if pongs := int64(googleCountPongs(result.text)); pongs < minPongs {
		t.Errorf("pongs = %d, want >= %d (text %q)", pongs, minPongs, result.text)
	}
}

func TestGoogleThinkingDisableAnthropicBudgetBased(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	model := googleCatalogModel(ai.ProviderAnthropic, "claude-sonnet-4-5")
	googleExpectThinkingDisabled(t, model, 320, 35, nil)
}

func TestGoogleThinkingDisableAnthropicAdaptive(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	model := googleCatalogModel(ai.ProviderAnthropic, "claude-sonnet-4-6")
	googleExpectThinkingDisabled(t, model, 320, 35, nil)
}

func TestGoogleThinkingDisableGemini25(t *testing.T) {
	if os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	model := googleCatalogModel(ai.ProviderGoogle, "gemini-2.5-flash")
	googleExpectThinkingDisabled(t, model, 160, 35, nil)
}

func TestGoogleThinkingDisableGemini3x(t *testing.T) {
	if os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	model := googleCatalogModel(ai.ProviderGoogle, "gemini-3-flash-preview")
	googleExpectThinkingDisabled(t, model, 160, 35, nil)
}

func TestGoogleThinkingDisableGemini31Pro(t *testing.T) {
	if os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	model := googleCatalogModel(ai.ProviderGoogle, "gemini-3.1-pro-preview")
	googleExpectThinkingDisabled(t, model, 512, 20, nil)
}

func TestGoogleThinkingDisableOpenAIResponses(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	model := googleCatalogModel(ai.ProviderOpenAI, "gpt-5.4-mini")
	googleExpectThinkingDisabled(t, model, 160, 35, nil)
}

func TestGoogleThinkingDisableOpenRouterQwen(t *testing.T) {
	if os.Getenv("OPENROUTER_API_KEY") == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	model := googleCatalogModel(ai.ProviderOpenRouter, "qwen/qwen3.5-plus-02-15")
	if model == nil {
		t.Skip("qwen/qwen3.5-plus-02-15 not in catalog")
	}
	googleExpectThinkingDisabled(t, model, 100, 35, nil)
}

// vertexE2EOptions resolves pi's vertexOptions: GOOGLE_CLOUD_API_KEY wins,
// then GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT + GOOGLE_CLOUD_LOCATION; nil means
// no Vertex credentials (pi: describe.skipIf).
func vertexE2EOptions(t *testing.T) func(*types.SimpleStreamOptions) {
	t.Helper()
	if key := os.Getenv("GOOGLE_CLOUD_API_KEY"); key != "" {
		return func(options *types.SimpleStreamOptions) { options.APIKey = key }
	}
	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if project == "" {
		project = os.Getenv("GCLOUD_PROJECT")
	}
	location := os.Getenv("GOOGLE_CLOUD_LOCATION")
	if project == "" || location == "" {
		return nil
	}
	return func(options *types.SimpleStreamOptions) {
		options.Env = types.ProviderEnv{"GOOGLE_CLOUD_PROJECT": project, "GOOGLE_CLOUD_LOCATION": location}
	}
}

func TestGoogleVertexThinkingDisableGemini25(t *testing.T) {
	extra := vertexE2EOptions(t)
	if extra == nil {
		t.Skip("no Vertex credentials (GOOGLE_CLOUD_API_KEY or GOOGLE_CLOUD_PROJECT+GOOGLE_CLOUD_LOCATION)")
	}
	model := googleCatalogModel(ai.ProviderGoogleVertex, "gemini-2.5-flash")
	googleExpectThinkingDisabled(t, model, 160, 35, extra)
}

func TestGoogleVertexThinkingDisableGemini3x(t *testing.T) {
	extra := vertexE2EOptions(t)
	if extra == nil {
		t.Skip("no Vertex credentials (GOOGLE_CLOUD_API_KEY or GOOGLE_CLOUD_PROJECT+GOOGLE_CLOUD_LOCATION)")
	}
	model := googleCatalogModel(ai.ProviderGoogleVertex, "gemini-3-flash-preview")
	googleExpectThinkingDisabled(t, model, 160, 35, extra)
}
