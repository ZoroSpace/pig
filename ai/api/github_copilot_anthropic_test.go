// pi: test/github-copilot-anthropic.test.ts. The TS test also asserts the
// generated github-copilot catalog (thinking level maps, context windows) —
// that provider's catalog entries land with the B2 regeneration, so only the
// adapter's request-shaping surface is exercised here.
package api

import (
	gocontext "context"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
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
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}},
	}
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
	llmContext := &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1},
			types.AssistantMessage{Role: types.RoleAssistant, Content: []types.Content{types.NewTextContent("Hi")}, Timestamp: 2},
		},
	}
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
