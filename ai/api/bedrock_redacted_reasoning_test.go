// pi: test/bedrock-redacted-reasoning.test.ts.
//
// OpenAI models served through Bedrock Converse (e.g.
// `global.openai.gpt-5.6-terra`) return encrypted reasoning as the opaque
// `redactedContent` member of `reasoningContent`, not as `reasoningText`. The
// AWS SDK decodes the wire blob to bytes.
// @see https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ReasoningContentBlockDelta.html
package api

import (
	"context"
	"encoding/base64"
	"reflect"
	"testing"

	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/ZoroSpace/pig/ai/types"
)

const bedrockRedactedBase64 = "cnNuXzVaVnJpZjRKMGJYSXFtV2RsZWRqN1FJRmVOaWtSUWJF"

func bedrockRedactedBytes(t *testing.T) []byte {
	t.Helper()
	bytes, err := base64.StdEncoding.DecodeString(bedrockRedactedBase64)
	if err != nil {
		t.Fatal(err)
	}
	return bytes
}

// bedrockGPTModel is the GPT-5.6 Terra fixture (non-Claude reasoning model).
func bedrockGPTModel() *types.Model {
	return &types.Model{
		ID:        "global.openai.gpt-5.6-terra",
		Name:      "GPT-5.6 Terra (Global)",
		API:       "bedrock-converse-stream",
		Provider:  "amazon-bedrock",
		BaseURL:   "https://bedrock-runtime.ap-northeast-1.amazonaws.com",
		Reasoning: true,
		Input:     []types.ModelInput{types.ModelInputText},
		Cost: types.ModelCost{ModelCostRates: types.ModelCostRates{
			Input: 1.25, Output: 10, CacheRead: 0.125,
		}},
		ContextWindow: 400000,
		MaxTokens:     128000,
	}
}

// bedrockRedactedReasoningEvents mirrors the ConverseStream frames GPT-5.6
// emits: encrypted reasoning, then text.
func bedrockRedactedReasoningEvents(t *testing.T) []brtypes.ConverseStreamOutput {
	return []brtypes.ConverseStreamOutput{
		&brtypes.ConverseStreamOutputMemberMessageStart{Value: brtypes.MessageStartEvent{Role: brtypes.ConversationRoleAssistant}},
		&brtypes.ConverseStreamOutputMemberContentBlockDelta{Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: int32Ptr(0),
			Delta: &brtypes.ContentBlockDeltaMemberReasoningContent{
				Value: &brtypes.ReasoningContentBlockDeltaMemberRedactedContent{Value: bedrockRedactedBytes(t)},
			},
		}},
		&brtypes.ConverseStreamOutputMemberContentBlockStop{Value: brtypes.ContentBlockStopEvent{ContentBlockIndex: int32Ptr(0)}},
		&brtypes.ConverseStreamOutputMemberContentBlockDelta{Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: int32Ptr(1),
			Delta:             &brtypes.ContentBlockDeltaMemberText{Value: "done"},
		}},
		&brtypes.ConverseStreamOutputMemberContentBlockStop{Value: brtypes.ContentBlockStopEvent{ContentBlockIndex: int32Ptr(1)}},
		&brtypes.ConverseStreamOutputMemberMessageStop{Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonEndTurn}},
	}
}

func TestBedrockRedactedReasoning(t *testing.T) {
	t.Run("does not fail the stream when reasoning arrives as redactedContent", func(t *testing.T) {
		mock := &bedrockMockClient{}
		mock.install(t)
		mock.events = bedrockRedactedReasoningEvents(t)

		s := streamBedrockConverse(context.Background(), bedrockGPTModel(), bedrockTestContext(), &BedrockOptions{})
		message := s.Result()

		if message.StopReason == types.StopReasonError {
			t.Fatalf("unexpected error: %s", message.ErrorMessage)
		}
		// Reasoning precedes the answer, matching the order Bedrock streamed it.
		kinds := make([]types.ContentKind, 0, len(message.Content))
		for _, block := range message.Content {
			kinds = append(kinds, block.ContentKind())
		}
		if !reflect.DeepEqual(kinds, []types.ContentKind{types.KindThinking, types.KindText}) {
			t.Fatalf("content kinds = %v", kinds)
		}
		if text, ok := message.Content[1].(types.TextContent); !ok || text.Text != "done" {
			t.Fatalf("text block = %+v", message.Content[1])
		}
	})

	t.Run("preserves the encrypted reasoning payload on the assistant message", func(t *testing.T) {
		mock := &bedrockMockClient{}
		mock.install(t)
		mock.events = bedrockRedactedReasoningEvents(t)

		s := streamBedrockConverse(context.Background(), bedrockGPTModel(), bedrockTestContext(), &BedrockOptions{})
		message := s.Result()

		thinking := bedrockFindThinking(message)
		if thinking == nil {
			t.Fatal("expected a thinking block")
		}
		// Same representation Anthropic redacted thinking already uses: the
		// opaque payload rides in thinkingSignature with redacted: true.
		if !thinking.Redacted {
			t.Fatal("expected redacted to be true")
		}
		if thinking.ThinkingSignature != bedrockRedactedBase64 {
			t.Fatalf("thinkingSignature = %q, want %q", thinking.ThinkingSignature, bedrockRedactedBase64)
		}
	})

	t.Run("encodes the payload when the stream never sends contentBlockStop", func(t *testing.T) {
		mock := &bedrockMockClient{}
		mock.install(t)
		mock.events = []brtypes.ConverseStreamOutput{
			&brtypes.ConverseStreamOutputMemberMessageStart{Value: brtypes.MessageStartEvent{Role: brtypes.ConversationRoleAssistant}},
			&brtypes.ConverseStreamOutputMemberContentBlockDelta{Value: brtypes.ContentBlockDeltaEvent{
				ContentBlockIndex: int32Ptr(0),
				Delta: &brtypes.ContentBlockDeltaMemberReasoningContent{
					Value: &brtypes.ReasoningContentBlockDeltaMemberRedactedContent{Value: bedrockRedactedBytes(t)},
				},
			}},
			&brtypes.ConverseStreamOutputMemberMessageStop{Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonEndTurn}},
		}

		s := streamBedrockConverse(context.Background(), bedrockGPTModel(), bedrockTestContext(), &BedrockOptions{})
		message := s.Result()

		thinking := bedrockFindThinking(message)
		if thinking == nil {
			t.Fatal("expected a thinking block")
		}
		if thinking.ThinkingSignature != bedrockRedactedBase64 {
			t.Fatalf("thinkingSignature = %q", thinking.ThinkingSignature)
		}
	})

	t.Run("joins encrypted reasoning split across deltas", func(t *testing.T) {
		full := bedrockRedactedBytes(t)
		mock := &bedrockMockClient{}
		mock.install(t)
		mock.events = []brtypes.ConverseStreamOutput{
			&brtypes.ConverseStreamOutputMemberMessageStart{Value: brtypes.MessageStartEvent{Role: brtypes.ConversationRoleAssistant}},
			&brtypes.ConverseStreamOutputMemberContentBlockDelta{Value: brtypes.ContentBlockDeltaEvent{
				ContentBlockIndex: int32Ptr(0),
				Delta: &brtypes.ContentBlockDeltaMemberReasoningContent{
					Value: &brtypes.ReasoningContentBlockDeltaMemberRedactedContent{Value: full[:7]},
				},
			}},
			&brtypes.ConverseStreamOutputMemberContentBlockDelta{Value: brtypes.ContentBlockDeltaEvent{
				ContentBlockIndex: int32Ptr(0),
				Delta: &brtypes.ContentBlockDeltaMemberReasoningContent{
					Value: &brtypes.ReasoningContentBlockDeltaMemberRedactedContent{Value: full[7:]},
				},
			}},
			&brtypes.ConverseStreamOutputMemberContentBlockStop{Value: brtypes.ContentBlockStopEvent{ContentBlockIndex: int32Ptr(0)}},
			&brtypes.ConverseStreamOutputMemberMessageStop{Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonEndTurn}},
		}

		s := streamBedrockConverse(context.Background(), bedrockGPTModel(), bedrockTestContext(), &BedrockOptions{})
		message := s.Result()

		thinking := bedrockFindThinking(message)
		if thinking == nil {
			t.Fatal("expected a thinking block")
		}
		if thinking.ThinkingSignature != bedrockRedactedBase64 {
			t.Fatalf("thinkingSignature = %q", thinking.ThinkingSignature)
		}
		// The placeholder marks the block once, not once per delta.
		if thinking.Thinking != bedrockRedactedThinkingPlaceholder {
			t.Fatalf("thinking = %q", thinking.Thinking)
		}
	})

	t.Run("replays redacted reasoning as reasoningContent.redactedContent", func(t *testing.T) {
		context := &types.Context{Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hello"}, Timestamp: 1},
			types.AssistantMessage{
				Role: types.RoleAssistant,
				Content: []types.Content{
					types.ThinkingContent{Kind: types.KindThinking, ThinkingSignature: bedrockRedactedBase64, Redacted: true},
					types.NewTextContent("done"),
				},
				API:        "bedrock-converse-stream",
				Provider:   "amazon-bedrock",
				Model:      bedrockGPTModel().ID,
				StopReason: types.StopReasonStop,
				Timestamp:  1,
			},
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "continue"}, Timestamp: 1},
		}}

		payload := captureBedrockPayload(t, context, bedrockGPTModel())

		var assistant *bedrockMessage
		for i := range payload.Messages {
			if payload.Messages[i].Role == "assistant" {
				assistant = &payload.Messages[i]
				break
			}
		}
		if assistant == nil {
			t.Fatal("expected an assistant message")
		}
		if len(assistant.Content) != 2 {
			t.Fatalf("content blocks = %d, want 2", len(assistant.Content))
		}
		first := assistant.Content[0]
		if first.ReasoningContent == nil || string(first.ReasoningContent.RedactedContent) != string(bedrockRedactedBytes(t)) {
			t.Fatalf("first block = %+v, want redactedContent", first)
		}
		if assistant.Content[1].Text == nil || *assistant.Content[1].Text != "done" {
			t.Fatalf("second block = %+v, want text done", assistant.Content[1])
		}
	})

	t.Run("replays redacted reasoning before the toolUse block it belongs to", func(t *testing.T) {
		// Bedrock rejects a tool continuation whose reasoning block is missing
		// or reordered, so the opaque payload must land ahead of the matching
		// toolUse.
		context := &types.Context{Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "read the file"}, Timestamp: 1},
			types.AssistantMessage{
				Role: types.RoleAssistant,
				Content: []types.Content{
					types.ThinkingContent{Kind: types.KindThinking, ThinkingSignature: bedrockRedactedBase64, Redacted: true},
					types.ToolCall{Kind: types.KindToolCall, ID: "tool-1", Name: "read", Arguments: map[string]any{"path": "/tmp/a.txt"}},
				},
				API:        "bedrock-converse-stream",
				Provider:   "amazon-bedrock",
				Model:      bedrockGPTModel().ID,
				StopReason: types.StopReasonToolUse,
				Timestamp:  1,
			},
			types.ToolResultMessage{
				Role:       types.RoleToolResult,
				ToolCallID: "tool-1",
				ToolName:   "read",
				Content:    []types.Content{types.NewTextContent("file body")},
				Timestamp:  1,
			},
		}}

		payload := captureBedrockPayload(t, context, bedrockGPTModel())

		var assistant *bedrockMessage
		for i := range payload.Messages {
			if payload.Messages[i].Role == "assistant" {
				assistant = &payload.Messages[i]
				break
			}
		}
		if assistant == nil {
			t.Fatal("expected an assistant message")
		}
		if len(assistant.Content) != 2 {
			t.Fatalf("content blocks = %d, want 2", len(assistant.Content))
		}
		first := assistant.Content[0]
		if first.ReasoningContent == nil || string(first.ReasoningContent.RedactedContent) != string(bedrockRedactedBytes(t)) {
			t.Fatalf("first block = %+v, want redactedContent", first)
		}
		toolUse := assistant.Content[1].ToolUse
		if toolUse == nil || toolUse.ToolUseID != "tool-1" || toolUse.Name != "read" {
			t.Fatalf("second block = %+v, want toolUse", assistant.Content[1])
		}
	})
}

func bedrockFindThinking(message types.AssistantMessage) *types.ThinkingContent {
	for _, block := range message.Content {
		if thinking, ok := block.(types.ThinkingContent); ok {
			return &thinking
		}
	}
	return nil
}
