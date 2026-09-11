// pi: test/anthropic-empty-thinking-signature-compat.test.ts.
package api

import (
	"context"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
)

func emptySignatureTestModel(allowEmptySignature *bool) *types.Model {
	model := anthropicTestModel("mimo-v2.5-pro", types.ProviderID("xiaomi-token-plan-ams"), nil)
	model.BaseURL = unreachableAnthropicBaseURL
	model.Reasoning = true
	model.ContextWindow = 1048576
	model.MaxTokens = 1024
	if allowEmptySignature != nil {
		model.Compat = &types.AnthropicMessagesCompat{AllowEmptySignature: allowEmptySignature}
	}
	return model
}

func emptySignatureTestContext(thinkingSignature string, thinking string, provider types.ProviderID, modelID string) *types.Context {
	assistant := types.AssistantMessage{
		Role: types.RoleAssistant,
		Content: []types.Content{
			types.ThinkingContent{Kind: types.KindThinking, Thinking: thinking, ThinkingSignature: thinkingSignature},
		},
		API:        types.ApiAnthropicMessages,
		Provider:   provider,
		Model:      modelID,
		Usage:      types.Usage{},
		StopReason: types.StopReasonStop,
		Timestamp:  1,
	}
	return &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "first"}, Timestamp: 1},
			assistant,
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "second"}, Timestamp: 2},
		},
	}
}

// captureSimplePayload drives the real StreamSimple entry point with OnPayload
// capture (pi: streamSimple(model, context, { apiKey, onPayload })).
func captureSimplePayload(t *testing.T, model *types.Model, llmContext *types.Context, options *types.SimpleStreamOptions) map[string]any {
	t.Helper()
	var captured map[string]any
	if options == nil {
		options = &types.SimpleStreamOptions{}
	}
	options.APIKey = "fake-key"
	options.OnPayload = func(payload any, model *types.Model) any {
		if m, ok := payload.(map[string]any); ok {
			captured = m
		}
		return nil
	}
	stream := (&AnthropicMessages{}).StreamSimple(context.Background(), model, llmContext, options)
	stream.Result()
	if captured == nil {
		t.Fatal("Expected payload capture before request")
	}
	return captured
}

// normalizeAny converts the wire map shapes the adapter builds
// ([]map[string]any and []any) into a uniform []any view for assertions.
func normalizeAny(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case []map[string]any:
		out := make([]any, 0, len(v))
		for _, entry := range v {
			out = append(out, entry)
		}
		return out
	}
	return nil
}

func findAssistantMessages(payload map[string]any) []any {
	var out []any
	for _, message := range normalizeAny(payload["messages"]) {
		if m, ok := message.(map[string]any); ok && m["role"] == "assistant" {
			out = append(out, m)
		}
	}
	return out
}

func TestAnthropicEmptySignatureConvertsToTextByDefault(t *testing.T) {
	model := emptySignatureTestModel(nil)
	payload := captureSimplePayload(t, model, emptySignatureTestContext("", "internal reasoning", types.ProviderID("xiaomi-token-plan-ams"), "mimo-v2.5-pro"), nil)
	assistantMessages := findAssistantMessages(payload)
	if len(assistantMessages) != 1 {
		t.Fatalf("assistant messages = %#v, want one", assistantMessages)
	}
	content := normalizeAny(assistantMessages[0].(map[string]any)["content"])
	if len(content) != 1 {
		t.Fatalf("assistant content = %#v, want one block", content)
	}
	block, _ := content[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "internal reasoning" {
		t.Errorf("assistant block = %#v, want text \"internal reasoning\"", block)
	}
}

func TestAnthropicEmptySignaturePreservesEmptyThinkingWithSignature(t *testing.T) {
	model := emptySignatureTestModel(nil)
	payload := captureSimplePayload(t, model, emptySignatureTestContext("signed-thinking", "", types.ProviderID("xiaomi-token-plan-ams"), "mimo-v2.5-pro"), nil)
	assistantMessages := findAssistantMessages(payload)
	if len(assistantMessages) != 1 {
		t.Fatalf("assistant messages = %#v, want one", assistantMessages)
	}
	content := normalizeAny(assistantMessages[0].(map[string]any)["content"])
	if len(content) != 1 {
		t.Fatalf("assistant content = %#v, want one block", content)
	}
	block, _ := content[0].(map[string]any)
	if block["type"] != "thinking" || block["thinking"] != "" || block["signature"] != "signed-thinking" {
		t.Errorf("assistant block = %#v, want thinking with signature", block)
	}
}

func TestAnthropicEmptySignaturePreservedWhenAllowEmptySignatureEnabled(t *testing.T) {
	model := emptySignatureTestModel(lo.ToPtr(true))
	payload := captureSimplePayload(t, model, emptySignatureTestContext(" ", "internal reasoning", types.ProviderID("xiaomi-token-plan-ams"), "mimo-v2.5-pro"), nil)
	assistantMessages := findAssistantMessages(payload)
	if len(assistantMessages) != 1 {
		t.Fatalf("assistant messages = %#v, want one", assistantMessages)
	}
	content := normalizeAny(assistantMessages[0].(map[string]any)["content"])
	block, _ := content[0].(map[string]any)
	if block["type"] != "thinking" || block["thinking"] != "internal reasoning" || block["signature"] != "" {
		t.Errorf("assistant block = %#v, want thinking with empty signature", block)
	}
}

func TestAnthropicEmptySignatureAllowsForKimiCoding(t *testing.T) {
	for _, modelID := range []string{"k3"} {
		t.Run(modelID, func(t *testing.T) {
			model := catalogAnthropicModel(t, types.ProviderKimiCoding, modelID)
			if model.Provider != types.ProviderKimiCoding {
				t.Fatalf("provider = %q, want kimi-coding", model.Provider)
			}
			compat, ok := model.Compat.(*types.AnthropicMessagesCompat)
			if !ok || compat.AllowEmptySignature == nil || !*compat.AllowEmptySignature {
				t.Fatal("expected catalog compat allowEmptySignature: true")
			}
			model.BaseURL = unreachableAnthropicBaseURL
			payload := captureSimplePayload(t, model, emptySignatureTestContext(" ", "internal reasoning", types.ProviderKimiCoding, modelID), nil)
			assistantMessages := findAssistantMessages(payload)
			if len(assistantMessages) != 1 {
				t.Fatalf("assistant messages = %#v, want one", assistantMessages)
			}
			content := normalizeAny(assistantMessages[0].(map[string]any)["content"])
			block, _ := content[0].(map[string]any)
			if block["type"] != "thinking" || block["thinking"] != "internal reasoning" || block["signature"] != "" {
				t.Errorf("assistant block = %#v, want thinking with empty signature", block)
			}
		})
	}
}
