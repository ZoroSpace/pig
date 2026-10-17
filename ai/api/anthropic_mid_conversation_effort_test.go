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
	return captureMidConvoOpts(t, model, llmContext, effort, lo.ToPtr(true))
}

// captureMidConvoOpts is captureMidConvo with an explicit thinkingEnabled
// (pi: capture(model, context, { effort, thinkingEnabled })).
func captureMidConvoOpts(t *testing.T, model *types.Model, llmContext *types.TranscriptContext, effort AnthropicEffort, thinkingEnabled *bool) (map[string]any, types.AssistantMessage) {
	t.Helper()
	options := &AnthropicOptions{}
	options.APIKey = "test-key"
	options.CacheRetention = types.CacheRetentionNone
	options.ThinkingEnabled = thinkingEnabled
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

func TestAnthropicMidConversationEffortOffThinkingDisabled(t *testing.T) {
	model := managedModel(t)
	model.ThinkingLevelMap = types.ThinkingLevelMap{
		types.ModelThinkingMinimal: nil,
		types.ModelThinkingLow:     lo.ToPtr("low"),
		types.ModelThinkingMedium:  lo.ToPtr("medium"),
		types.ModelThinkingHigh:    lo.ToPtr("high"),
		types.ModelThinkingMax:     lo.ToPtr("max"),
	}
	// Anthropic rejects block_binding with disabled thinking, and rejects
	// effort markers that change level (here low, then medium) while thinking
	// is disabled.
	payload, message := captureMidConvoOpts(t, model, utils.NormalizeContext(&types.Context{Messages: []types.Message{
		midConvoUser("one", 1),
		midConvoAssistant(t, model, "low"),
		midConvoUser("two", 2),
		midConvoAssistant(t, model, "medium"),
		midConvoUser("three", 3),
	}}), "", lo.ToPtr(false))
	if !stringMapEqual(mapOf(t, payload["thinking"]), map[string]any{"type": "disabled"}) {
		t.Errorf("thinking = %#v, want disabled", payload["thinking"])
	}
	if payload["output_config"] != nil {
		t.Errorf("output_config = %#v, want undefined", payload["output_config"])
	}
	if markers := effortMarkers(t, payload); len(markers) != 0 {
		t.Errorf("markers = %#v, want none", markers)
	}
	if message.ProviderThinkingLevel != "" {
		t.Errorf("providerThinkingLevel = %q, want unset", message.ProviderThinkingLevel)
	}
}

func TestAnthropicMidConversationEffortOffUnsupportedKeepsAdaptive(t *testing.T) {
	payload, message := captureMidConvoOpts(t, managedModel(t), utils.NormalizeContext(&types.Context{Messages: []types.Message{midConvoUser("one", 1)}}), "", lo.ToPtr(false))
	thinking := mapOf(t, payload["thinking"])
	if thinking["type"] != "adaptive" {
		t.Errorf("thinking = %#v, want adaptive", thinking)
	}
	markers := effortMarkers(t, payload)
	if len(markers) != 1 || !stringMapEqual(markers[0], map[string]any{"role": "system", "content": []any{}, "output_config": map[string]any{"effort": "high"}}) {
		t.Errorf("markers = %#v", markers)
	}
	if message.ProviderThinkingLevel != "high" {
		t.Errorf("providerThinkingLevel = %q, want high", message.ProviderThinkingLevel)
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

// TestAnthropicDisabledThinkingCatalogGates ports pi's "supports off only on
// managed models that accept disabled thinking" (dc939913f): Opus 5 and Haiku
// 5.5 keep off valid (no null mapping), while Opus 5.5 / Sonnet 5.5 / Fable
// 5.1 pin off to null.
func TestAnthropicDisabledThinkingCatalogGates(t *testing.T) {
	for _, id := range []string{"claude-opus-5", "claude-haiku-5-5"} {
		model := catalogAnthropicModel(t, types.ProviderAnthropic, id)
		compat, ok := model.Compat.(*types.AnthropicMessagesCompat)
		if !ok || compat.SupportsMidConvoEffort == nil || !*compat.SupportsMidConvoEffort {
			t.Errorf("%s must be mid-convo-effort managed", id)
		}
		if off, present := model.ThinkingLevelMap[types.ModelThinkingOff]; present && off == nil {
			t.Errorf("%s thinkingLevelMap.off = null, want a valid level", id)
		}
	}
	for _, id := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-fable-5-1"} {
		model := catalogAnthropicModel(t, types.ProviderAnthropic, id)
		if off, present := model.ThinkingLevelMap[types.ModelThinkingOff]; !present || off != nil {
			t.Errorf("%s thinkingLevelMap.off = %#v, want null", id, off)
		}
	}
}
