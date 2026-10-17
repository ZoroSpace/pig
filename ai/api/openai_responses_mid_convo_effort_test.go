// pi: test/openai-responses-mid-convo-effort.test.ts.
package api

import (
	gocontext "context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// midConvoEffortModel is the fixture model (pi: openAIModel).
func midConvoEffortModel(compat *types.OpenAIResponsesCompat) *types.Model {
	if compat == nil {
		compat = &types.OpenAIResponsesCompat{SupportsMidConvoEffort: lo.ToPtr(true)}
	}
	return &types.Model{
		ID: "gpt-6-luna", Name: "GPT-6 Luna", API: types.ApiOpenAIResponses,
		Provider: types.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
		Reasoning: true,
		ThinkingLevelMap: types.ThinkingLevelMap{
			types.ModelThinkingOff:     lo.ToPtr("none"),
			types.ModelThinkingMinimal: nil,
			types.ModelThinkingLow:     lo.ToPtr("low"),
			types.ModelThinkingMedium:  lo.ToPtr("medium"),
			types.ModelThinkingHigh:    lo.ToPtr("high"),
			types.ModelThinkingXHigh:   lo.ToPtr("xhigh"),
			types.ModelThinkingMax:     lo.ToPtr("max"),
		},
		Input:         []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 272000,
		MaxTokens:     128000,
		Compat:        compat,
	}
}

// midConvoToolResult is pi's toolResult fixture.
func midConvoToolResult(callID string) types.ToolResultMessage {
	return types.ToolResultMessage{
		Role: types.RoleToolResult, ToolCallID: callID, ToolName: "lookup",
		Content: []types.Content{types.NewTextContent("result")}, IsError: false, Timestamp: 0,
	}
}

// midConvoUpdate is pi's update helper.
func midConvoUpdate(effort string) map[string]any {
	return map[string]any{"type": "configuration_update", "reasoning": map[string]any{"effort": effort}}
}

// midConvoResponseEvents is pi's responseEvents.
func midConvoResponseEvents(responseID string, withToolCall bool) []any {
	var item map[string]any
	if withToolCall {
		item = map[string]any{
			"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "lookup",
			"arguments": "{}", "status": "completed",
		}
	} else {
		item = map[string]any{
			"type": "message", "id": "msg_" + responseID, "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "answer", "annotations": []any{}}},
		}
	}
	return []any{
		map[string]any{"type": "response.created", "response": map[string]any{"id": responseID}},
		map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item},
		map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item},
		map[string]any{"type": "response.completed", "response": map[string]any{"id": responseID, "status": "completed"}},
	}
}

// runMidConvoOpenAI runs the stream against a fake transport that captures the
// request payload (pi: runOpenAI).
func runMidConvoOpenAI(t *testing.T, messages []types.Message, effort types.ModelThinkingLevel, apiKey string, model *types.Model, withToolCall bool) (map[string]any, types.AssistantMessage) {
	t.Helper()
	var captured map[string]any
	fetch := func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(body, &captured)
		var b strings.Builder
		for _, event := range midConvoResponseEvents("resp_1", withToolCall) {
			raw, _ := json.Marshal(event)
			b.WriteString("data: " + string(raw) + "\n\n")
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(b.String())),
			Request:    req,
		}, nil
	}
	options := &OpenAIResponsesOptions{StreamOptions: types.StreamOptions{
		ProviderRequestOptions: types.ProviderRequestOptions{Fetch: fetch, APIKey: apiKey},
	}}
	options.ReasoningEffort = effort
	llmContext := utils.NormalizeContext(&types.Context{Messages: messages})
	stream := streamOpenAIResponses(gocontext.Background(), model, llmContext, options)
	_, message := collectResponsesEvents(t, stream)
	if captured == nil {
		t.Fatal("Request payload was not captured")
	}
	if message.StopReason == types.StopReasonError {
		t.Fatalf("response errored: %s", message.ErrorMessage)
	}
	return captured, message
}

// midConvoInput extracts the input array from a captured payload.
func midConvoInput(t *testing.T, payload map[string]any) []any {
	t.Helper()
	input, ok := payload["input"].([]any)
	if !ok {
		t.Fatalf("input = %#v", payload["input"])
	}
	return input
}

// midConvoEffortOf extracts reasoning.effort (pi: payload.reasoning?.effort).
func midConvoEffortOf(t *testing.T, payload map[string]any) string {
	t.Helper()
	if reasoning, ok := payload["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"].(string); ok {
			return effort
		}
		return ""
	}
	t.Fatalf("reasoning missing in %#v", payload)
	return ""
}

// midConvoConfigUpdates keeps only configuration_update items.
func midConvoConfigUpdates(input []any) []any {
	var updates []any
	for _, item := range input {
		if m, ok := item.(map[string]any); ok && m["type"] == "configuration_update" {
			updates = append(updates, m)
		}
	}
	return updates
}

func TestOpenAIResponsesMidConvoEffort(t *testing.T) {
	t.Run("keeps reasoning.effort constant and only appends input when the level changes", func(t *testing.T) {
		model := midConvoEffortModel(nil)
		history := []types.Message{responsesUserMessage("one")}
		firstPayload, first := runMidConvoOpenAI(t, history, types.ModelThinkingHigh, "sk-test", model, false)
		history = append(history, first, responsesUserMessage("two"))
		secondPayload, second := runMidConvoOpenAI(t, history, types.ModelThinkingHigh, "sk-test", model, false)
		history = append(history, second, responsesUserMessage("three"))
		thirdPayload, third := runMidConvoOpenAI(t, history, types.ModelThinkingLow, "sk-test", model, false)

		firstInput := midConvoInput(t, firstPayload)
		wantFirst := []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "one"}}},
			midConvoUpdate("high"),
		}
		if !reflect.DeepEqual(firstInput, wantFirst) {
			t.Fatalf("first input = %#v", firstInput)
		}
		for i, payload := range []map[string]any{firstPayload, secondPayload, thirdPayload} {
			if effort := midConvoEffortOf(t, payload); effort != "medium" {
				t.Fatalf("payload %d reasoning.effort = %q, want medium", i, effort)
			}
		}
		secondInput := midConvoInput(t, secondPayload)
		thirdInput := midConvoInput(t, thirdPayload)
		if !reflect.DeepEqual(secondInput[:len(firstInput)], firstInput) {
			t.Fatalf("second input prefix changed: %#v", secondInput[:len(firstInput)])
		}
		if !reflect.DeepEqual(thirdInput[:len(secondInput)], secondInput) {
			t.Fatalf("third input prefix changed: %#v", thirdInput[:len(secondInput)])
		}
		wantUpdates := []any{midConvoUpdate("high"), midConvoUpdate("high"), midConvoUpdate("low")}
		if got := midConvoConfigUpdates(thirdInput); !reflect.DeepEqual(got, wantUpdates) {
			t.Fatalf("configuration updates = %#v", got)
		}
		if last := thirdInput[len(thirdInput)-1]; !reflect.DeepEqual(last, midConvoUpdate("low")) {
			t.Fatalf("last input item = %#v", last)
		}
		levels := []string{first.ProviderThinkingLevel, second.ProviderThinkingLevel, third.ProviderThinkingLevel}
		if !reflect.DeepEqual(levels, []string{"high", "high", "low"}) {
			t.Fatalf("providerThinkingLevel = %#v", levels)
		}
	})

	t.Run("applies a level change made during a tool loop to the next reply", func(t *testing.T) {
		model := midConvoEffortModel(nil)
		history := []types.Message{responsesUserMessage("one")}
		toolPayload, toolCall := runMidConvoOpenAI(t, history, types.ModelThinkingHigh, "sk-test", model, true)
		history = append(history, toolCall, midConvoToolResult("call_1|fc_1"))
		afterPayload, after := runMidConvoOpenAI(t, history, types.ModelThinkingLow, "sk-test", model, false)

		toolInput := midConvoInput(t, toolPayload)
		afterInput := midConvoInput(t, afterPayload)
		if !reflect.DeepEqual(afterInput[:len(toolInput)], toolInput) {
			t.Fatalf("after-tool input prefix changed: %#v", afterInput[:len(toolInput)])
		}
		wantTail := []any{
			map[string]any{"type": "function_call_output", "call_id": "call_1"},
			midConvoUpdate("low"),
		}
		tail := afterInput[len(afterInput)-2:]
		for i, item := range tail {
			m, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("tail[%d] = %#v", i, item)
			}
			for key, value := range wantTail[i].(map[string]any) {
				if !reflect.DeepEqual(m[key], value) {
					t.Fatalf("tail[%d].%s = %#v, want %#v", i, key, m[key], value)
				}
			}
		}
		if after.ProviderThinkingLevel != "low" {
			t.Fatalf("providerThinkingLevel = %q, want low", after.ProviderThinkingLevel)
		}
	})

	t.Run("adds items only before replies recorded by this model", func(t *testing.T) {
		defaultModel := midConvoEffortModel(nil)
		unstamped := func() types.AssistantMessage {
			_, message := runMidConvoOpenAI(t, []types.Message{responsesUserMessage("one")}, types.ModelThinkingHigh, "sk-test", midConvoEffortModel(&types.OpenAIResponsesCompat{}), false)
			return message
		}()
		otherProvider := unstamped
		otherProvider.Provider = types.ProviderID("opencode")
		otherProvider.ProviderThinkingLevel = "high"
		otherModel := unstamped
		otherModel.Model = "gpt-6-sol"
		otherModel.ProviderThinkingLevel = "high"
		recorded := unstamped
		recorded.ProviderThinkingLevel = "high"

		payload, _ := runMidConvoOpenAI(t, []types.Message{
			responsesUserMessage("one"),
			unstamped,
			responsesUserMessage("two"),
			otherProvider,
			responsesUserMessage("three"),
			otherModel,
			responsesUserMessage("four"),
			recorded,
			responsesUserMessage("five"),
		}, types.ModelThinkingLow, "sk-test", defaultModel, false)

		input := midConvoInput(t, payload)
		var typesSeq []any
		for _, item := range input {
			m := item.(map[string]any)
			if itemType, ok := m["type"]; ok {
				typesSeq = append(typesSeq, itemType)
			} else {
				typesSeq = append(typesSeq, m["role"])
			}
		}
		wantSeq := []any{
			"user", "message", "user", "message", "user", "message", "user",
			"configuration_update", "message", "user", "configuration_update",
		}
		if !reflect.DeepEqual(typesSeq, wantSeq) {
			t.Fatalf("item sequence = %#v", typesSeq)
		}
		wantUpdates := []any{midConvoUpdate("high"), midConvoUpdate("low")}
		if got := midConvoConfigUpdates(input); !reflect.DeepEqual(got, wantUpdates) {
			t.Fatalf("configuration updates = %#v", got)
		}
	})

	for _, tc := range []struct {
		name   string
		apiKey string
		model  *types.Model
		level  types.ModelThinkingLevel
		effort string
	}{
		{
			name: "Sign in with ChatGPT", apiKey: "chatgpt-access-token",
			model: midConvoEffortModel(nil), level: types.ModelThinkingLow, effort: "low",
		},
		{
			name: "the compat flag turned off", apiKey: "sk-test",
			model: midConvoEffortModel(&types.OpenAIResponsesCompat{SupportsMidConvoEffort: lo.ToPtr(false)}),
			level: types.ModelThinkingLow, effort: "low",
		},
		{
			name: "thinking off", apiKey: "sk-test",
			model: midConvoEffortModel(nil), level: types.ModelThinkingLevel(""), effort: "none",
		},
	} {
		t.Run("sends the level as reasoning.effort with "+tc.name, func(t *testing.T) {
			first := func() types.AssistantMessage {
				_, message := runMidConvoOpenAI(t, []types.Message{responsesUserMessage("one")}, types.ModelThinkingHigh, tc.apiKey, tc.model, false)
				return message
			}()
			payload, message := runMidConvoOpenAI(t, []types.Message{responsesUserMessage("one"), first, responsesUserMessage("two")}, tc.level, tc.apiKey, tc.model, false)

			if effort := midConvoEffortOf(t, payload); effort != tc.effort {
				t.Fatalf("reasoning.effort = %q, want %q", effort, tc.effort)
			}
			if updates := midConvoConfigUpdates(midConvoInput(t, payload)); len(updates) != 0 {
				t.Fatalf("unexpected configuration updates: %#v", updates)
			}
			if message.ProviderThinkingLevel != "" {
				t.Fatalf("providerThinkingLevel = %q, want empty", message.ProviderThinkingLevel)
			}
		})
	}
}
