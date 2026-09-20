// pi: test/anthropic-mid-conversation-effort.test.ts.
package api

import (
	gocontext "context"
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// managedModel mirrors pi's managedModel (claude-fable-5-1 with
// supportsMidConvoEffort; the catalog entry lands with the B2 regeneration).
func managedModel(t *testing.T) *types.Model {
	t.Helper()
	model := anthropicTestModel("claude-fable-5-1", types.ProviderAnthropic, &types.AnthropicMessagesCompat{
		ForceAdaptiveThinking:  lo.ToPtr(true),
		SupportsMidConvoEffort: lo.ToPtr(true),
	})
	model.BaseURL = unreachableAnthropicBaseURL
	model.ThinkingLevelMap = types.ThinkingLevelMap{
		types.ModelThinkingOff:     nil,
		types.ModelThinkingMinimal: lo.ToPtr("low"),
		types.ModelThinkingLow:     lo.ToPtr("low"),
		types.ModelThinkingMedium:  lo.ToPtr("medium"),
		types.ModelThinkingHigh:    lo.ToPtr("high"),
		types.ModelThinkingMax:     lo.ToPtr("max"),
	}
	return model
}

func midConvoAssistant(t *testing.T, model *types.Model, level string) types.AssistantMessage {
	t.Helper()
	message := types.AssistantMessage{
		Role: types.RoleAssistant,
		Content: []types.Content{
			types.ThinkingContent{Kind: types.KindThinking, Thinking: "reasoning", ThinkingSignature: "signature"},
			types.NewTextContent("answer"),
		},
		API:        types.ApiAnthropicMessages,
		Provider:   model.Provider,
		Model:      model.ID,
		Usage:      types.Usage{},
		StopReason: types.StopReasonStop,
		Timestamp:  1,
	}
	if level != "" {
		message.ProviderThinkingLevel = level
	}
	return message
}

// captureMidConvo runs stream() with an OnPayload capture (pi: capture).
func captureMidConvo(t *testing.T, model *types.Model, llmContext *types.TranscriptContext, effort AnthropicEffort) (map[string]any, types.AssistantMessage) {
	t.Helper()
	options := &AnthropicOptions{}
	options.APIKey = "test-key"
	options.CacheRetention = types.CacheRetentionNone
	options.ThinkingEnabled = lo.ToPtr(true)
	if effort != "" {
		options.Effort = effort
	}
	var captured map[string]any
	options.OnPayload = func(payload any, model *types.Model) any {
		if m, ok := payload.(map[string]any); ok {
			captured = m
		}
		return nil
	}
	stream := streamAnthropicMessages(gocontext.Background(), model, llmContext, options)
	message := stream.Result()
	if captured == nil {
		t.Fatal("Expected payload capture")
	}
	return captured, message
}

func effortMarkers(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, message := range normalizeAny(payload["messages"]) {
		if m, ok := message.(map[string]any); ok && m["role"] == "system" {
			out = append(out, m)
		}
	}
	return out
}

func midConvoUser(text string, timestamp int64) types.UserMessage {
	return types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: text}, Timestamp: timestamp}
}

func mapOf(t *testing.T, value any) map[string]any {
	t.Helper()
	m, _ := value.(map[string]any)
	return m
}

func TestAnthropicMidConversationEffortMarkerPrefix(t *testing.T) {
	model := managedModel(t)
	firstPayload, firstMessage := captureMidConvo(t, model, utils.NormalizeContext(&types.Context{Messages: []types.Message{midConvoUser("one", 1)}}), AnthropicEffortLow)

	markers := effortMarkers(t, firstPayload)
	if len(markers) != 1 || !stringMapEqual(markers[0], map[string]any{"role": "system", "content": []any{}, "output_config": map[string]any{"effort": "low"}}) {
		t.Fatalf("first markers = %#v", markers)
	}
	if firstMessage.ProviderThinkingLevel != "low" {
		t.Errorf("providerThinkingLevel = %q, want low", firstMessage.ProviderThinkingLevel)
	}

	secondContext := utils.NormalizeContext(&types.Context{Messages: []types.Message{
		midConvoUser("one", 1),
		midConvoAssistant(t, model, "low"),
		midConvoUser("two", 2),
	}})
	secondPayload, secondMessage := captureMidConvo(t, model, secondContext, AnthropicEffortHigh)

	secondMarkers := effortMarkers(t, secondPayload)
	if len(secondMarkers) != 2 {
		t.Fatalf("second markers = %#v, want two", secondMarkers)
	}
	if !stringMapEqual(secondMarkers[0], markers[0]) {
		t.Errorf("historical marker = %#v, want %#v", secondMarkers[0], markers[0])
	}
	if !stringMapEqual(secondMarkers[1], map[string]any{"role": "system", "content": []any{}, "output_config": map[string]any{"effort": "high"}}) {
		t.Errorf("active marker = %#v", secondMarkers[1])
	}
	if !stringMapEqual(mapOf(t, firstPayload["output_config"]), map[string]any{"effort": "high"}) {
		t.Errorf("first output_config = %#v", firstPayload["output_config"])
	}
	thinking := mapOf(t, secondPayload["thinking"])
	if thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
		t.Errorf("second thinking = %#v", thinking)
	}
	if firstMessage.ProviderThinkingLevel != "low" || secondMessage.ProviderThinkingLevel != "high" {
		t.Errorf("levels = %q / %q", firstMessage.ProviderThinkingLevel, secondMessage.ProviderThinkingLevel)
	}
}

func TestAnthropicMidConversationEffortNativeLevels(t *testing.T) {
	for _, effort := range []AnthropicEffort{"low", "medium", "high", "xhigh", "max"} {
		t.Run(string(effort), func(t *testing.T) {
			model := managedModel(t)
			payload, message := captureMidConvo(t, model, utils.NormalizeContext(&types.Context{Messages: []types.Message{midConvoUser("one", 1)}}), effort)
			markers := effortMarkers(t, payload)
			if len(markers) != 1 || !stringMapEqual(markers[0], map[string]any{"role": "system", "content": []any{}, "output_config": map[string]any{"effort": string(effort)}}) {
				t.Fatalf("markers = %#v", markers)
			}
			if message.ProviderThinkingLevel != string(effort) {
				t.Errorf("providerThinkingLevel = %q", message.ProviderThinkingLevel)
			}
		})
	}
}

func TestAnthropicMidConversationEffortDefaultsToHigh(t *testing.T) {
	model := managedModel(t)
	payload, message := captureMidConvo(t, model, utils.NormalizeContext(&types.Context{Messages: []types.Message{midConvoUser("one", 1)}}), "")
	markers := effortMarkers(t, payload)
	if len(markers) != 1 || !stringMapEqual(markers[0], map[string]any{"role": "system", "content": []any{}, "output_config": map[string]any{"effort": "high"}}) {
		t.Fatalf("markers = %#v", markers)
	}
	thinking := mapOf(t, payload["thinking"])
	if thinking["block_binding"] == nil {
		t.Errorf("thinking = %#v, want block_binding drop_block", thinking)
	}
	if message.ProviderThinkingLevel != "high" {
		t.Errorf("providerThinkingLevel = %q", message.ProviderThinkingLevel)
	}
}

func TestAnthropicMidConversationEffortSkipsLegacyAndForeignAssistants(t *testing.T) {
	model := managedModel(t)
	legacy := midConvoAssistant(t, model, "") // no providerThinkingLevel
	otherProvider := midConvoAssistant(t, model, "low")
	otherProvider.Provider = types.ProviderID("other-provider")
	payload, _ := captureMidConvo(t, model, utils.NormalizeContext(&types.Context{Messages: []types.Message{
		midConvoUser("one", 1), legacy, midConvoUser("two", 2), otherProvider, midConvoUser("three", 3),
	}}), AnthropicEffortMedium)
	markers := effortMarkers(t, payload)
	if len(markers) != 1 {
		t.Fatalf("markers = %#v, want only the active one", markers)
	}
	if !stringMapEqual(markers[0], map[string]any{"role": "system", "content": []any{}, "output_config": map[string]any{"effort": "medium"}}) {
		t.Errorf("marker = %#v", markers[0])
	}
}

func TestAnthropicMidConversationEffortUnsupportedModelsUseTopLevelEffort(t *testing.T) {
	model := managedModel(t)
	model.Compat = &types.AnthropicMessagesCompat{ForceAdaptiveThinking: lo.ToPtr(true)}
	payload, message := captureMidConvo(t, model, utils.NormalizeContext(&types.Context{Messages: []types.Message{midConvoUser("one", 1)}}), AnthropicEffortLow)
	for _, message := range normalizeAny(payload["messages"]) {
		if m := message.(map[string]any); m["role"] == "system" {
			t.Errorf("unexpected system marker: %#v", m)
		}
	}
	if !stringMapEqual(mapOf(t, payload["output_config"]), map[string]any{"effort": "low"}) {
		t.Errorf("output_config = %#v", payload["output_config"])
	}
	thinking := mapOf(t, payload["thinking"])
	if thinking["type"] != "adaptive" || thinking["display"] != "summarized" {
		t.Errorf("thinking = %#v", thinking)
	}
	if message.ProviderThinkingLevel != "" {
		t.Errorf("providerThinkingLevel = %q, want unset", message.ProviderThinkingLevel)
	}
}

func TestAnthropicMidConversationEffortBetaHeaders(t *testing.T) {
	model := managedModel(t)
	model.BaseURL = "https://api.anthropic.com"
	var captured capturedAnthropicRequest
	options := &AnthropicOptions{}
	options.APIKey = "test-key"
	options.CacheRetention = types.CacheRetentionNone
	options.Fetch = fakeAnthropicFetch(minimalAnthropicEvents, &captured)
	result := streamAnthropicMessages(gocontext.Background(), model, utils.NormalizeContext(&types.Context{Messages: []types.Message{midConvoUser("one", 1)}}), options).Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}
	header := captured.req.Header.Get("anthropic-beta")
	if !strings.Contains(header, "mid-conversation-output-config-2026-07-01") || !strings.Contains(header, "thinking-binding-controls-2026-08-01") {
		t.Errorf("anthropic-beta = %q, want the effort and binding betas", header)
	}
}

func TestAnthropicMidConversationEffortCatalogGates(t *testing.T) {
	// pi: getModel("anthropic", "claude-fable-5-1") / openrouter fable-5.1 —
	// those catalog entries land with the B2 regeneration. The opus-4-8 gate
	// is assertable today.
	opus := catalogAnthropicModel(t, types.ProviderAnthropic, "claude-opus-4-8")
	compat, ok := opus.Compat.(*types.AnthropicMessagesCompat)
	if !ok {
		t.Fatal("expected anthropic compat")
	}
	if compat.SupportsMidConvoEffort != nil && *compat.SupportsMidConvoEffort {
		t.Error("opus-4-8 must not be mid-convo-effort managed")
	}
	if compat.AllowedFallbackModels != nil {
		t.Errorf("opus-4-8 fallbacks = %#v, want none", compat.AllowedFallbackModels)
	}
}
