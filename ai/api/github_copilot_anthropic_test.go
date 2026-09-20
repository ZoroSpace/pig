// pi: test/github-copilot-anthropic.test.ts. The catalog half (thinking
// level maps, context windows) is asserted in
// TestGitHubCopilotCatalogAnthropicMapping below, now that the
// github-copilot catalog entries have landed.
package api

import (
	gocontext "context"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

func githubCopilotTestModel(t *testing.T) *types.Model {
	t.Helper()
	model := anthropicTestModel("claude-opus-4-8", types.ProviderID("github-copilot"), &types.AnthropicMessagesCompat{
		ForceAdaptiveThinking: lo.ToPtr(true),
	})
	model.BaseURL = "https://api.individual.githubcopilot.com"
	return model
}

func TestGitHubCopilotAnthropicRequestShaping(t *testing.T) {
	model := githubCopilotTestModel(t)
	llmContext := utils.NormalizeContext(&types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}},
	})
	var captured capturedAnthropicRequest
	options := &AnthropicOptions{}
	options.APIKey = "copilot-token"
	options.Fetch = fakeAnthropicFetch(minimalAnthropicEvents, &captured)
	result := streamAnthropicMessages(gocontext.Background(), model, llmContext, options).Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}

	// Copilot uses Bearer auth, never x-api-key.
	if got := captured.req.Header.Get("Authorization"); got != "Bearer copilot-token" {
		t.Errorf("Authorization = %q, want Bearer copilot-token", got)
	}
	if captured.req.Header.Get("x-api-key") != "" {
		t.Errorf("x-api-key = %q, want absent", captured.req.Header.Get("x-api-key"))
	}
	// Dynamic Copilot headers: user-initiated, conversation-edits intent.
	if got := captured.req.Header.Get("X-Initiator"); got != "user" {
		t.Errorf("X-Initiator = %q, want user", got)
	}
	if got := captured.req.Header.Get("Openai-Intent"); got != "conversation-edits" {
		t.Errorf("Openai-Intent = %q, want conversation-edits", got)
	}
}

func TestGitHubCopilotAnthropicAgentInitiator(t *testing.T) {
	// A trailing assistant message marks an agent-initiated follow-up.
	model := githubCopilotTestModel(t)
	llmContext := utils.NormalizeContext(&types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1},
			types.AssistantMessage{Role: types.RoleAssistant, Content: []types.Content{types.NewTextContent("Hi")}, Timestamp: 2},
		},
	})
	var captured capturedAnthropicRequest
	options := anthropicTestOptions(minimalAnthropicEvents, &captured)
	result := streamAnthropicMessages(gocontext.Background(), model, llmContext, options).Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}
	if got := captured.req.Header.Get("X-Initiator"); got != "agent" {
		t.Errorf("X-Initiator = %q, want agent", got)
	}
}

// TestGitHubCopilotCatalogAnthropicMapping ports the catalog half of
// github-copilot-anthropic.test.ts (deferred from the A1 batch until the
// github-copilot catalog entries landed): thinking-level overrides, the 1M
// extended context, and getSupportedThinkingLevels membership.
func TestGitHubCopilotCatalogAnthropicMapping(t *testing.T) {
	catalog := catalogModelsSorted(t)[types.ProviderID("github-copilot")]
	if len(catalog) == 0 {
		t.Fatal("github-copilot catalog not populated")
	}
	catalogByID := map[string]types.Model{}
	for _, model := range catalog {
		catalogByID[model.ID] = model
	}

	opus47 := catalogByID["claude-opus-4.7"]
	if opus47.ThinkingLevelMap[types.ModelThinkingMinimal] == nil || *opus47.ThinkingLevelMap[types.ModelThinkingMinimal] != "low" {
		t.Errorf("opus-4.7 minimal = %v, want low", opus47.ThinkingLevelMap[types.ModelThinkingMinimal])
	}
	assertCopilotLevel(t, opus47, "xhigh", true)
	assertCopilotLevel(t, opus47, "max", true)

	opus5 := catalogByID["claude-opus-5"]
	if opus5.API != types.ApiAnthropicMessages {
		t.Errorf("opus-5 api = %s, want anthropic-messages", opus5.API)
	}
	if opus5.ContextWindow != 1000000 {
		t.Errorf("opus-5 contextWindow = %d, want 1000000 (extended context)", opus5.ContextWindow)
	}
	if opus5.ThinkingLevelMap[types.ModelThinkingMinimal] == nil || *opus5.ThinkingLevelMap[types.ModelThinkingMinimal] != "low" {
		t.Errorf("opus-5 minimal = %v, want low", opus5.ThinkingLevelMap[types.ModelThinkingMinimal])
	}
	assertCopilotLevel(t, opus5, "xhigh", true)
	assertCopilotLevel(t, opus5, "max", true)

	sonnet46 := catalogByID["claude-sonnet-4.6"]
	if sonnet46.ThinkingLevelMap[types.ModelThinkingMinimal] == nil || *sonnet46.ThinkingLevelMap[types.ModelThinkingMinimal] != "low" {
		t.Errorf("sonnet-4.6 minimal = %v, want low", sonnet46.ThinkingLevelMap[types.ModelThinkingMinimal])
	}
	assertCopilotLevel(t, sonnet46, "max", true)
	assertCopilotLevel(t, sonnet46, "xhigh", false)
}

// assertCopilotLevel asserts getSupportedThinkingLevels membership.
func assertCopilotLevel(t *testing.T, model types.Model, level types.ModelThinkingLevel, want bool) {
	t.Helper()
	supported := false
	for _, candidate := range ai.GetSupportedThinkingLevels(&model) {
		if candidate == level {
			supported = true
		}
	}
	if supported != want {
		t.Errorf("%s supports %s = %v, want %v", model.ID, level, supported, want)
	}
}
