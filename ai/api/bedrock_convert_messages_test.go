// pi: test/bedrock-convert-messages.test.ts.
package api

import (
	"context"
	"reflect"
	"testing"

	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

func TestBedrockConstrainedSamplingGatesStrictToolUse(t *testing.T) {
	mock := &bedrockMockClient{}
	mock.install(t)
	tool := types.Tool{
		Name:        "lookup",
		Description: "Look up a value",
		Parameters:  []byte(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`),
		ConstrainedSampling: map[string]any{
			"type":   "json_schema",
			"strict": "require",
		},
	}
	// v0.86.0: tools are declared by the leading system message.
	context := utils.NormalizeContext(&types.Context{
		Messages: []types.Message{
			types.SystemMessage{Role: types.RoleSystem, Content: types.SystemContent{String: ""}, ToolsAdded: []types.Tool{tool}, Timestamp: 1},
			types.UserMessage{
				Role:      types.RoleUser,
				Content:   types.UserContent{String: "Use the tool"},
				Timestamp: 1,
			},
		},
	})

	payload := captureBedrockPayload(t, context, bedrockBaseModel)
	if payload.ToolConfig == nil || len(payload.ToolConfig.Tools) != 1 || payload.ToolConfig.Tools[0].ToolSpec == nil {
		t.Fatalf("expected one tool spec, got %+v", payload.ToolConfig)
	}
	if payload.ToolConfig.Tools[0].ToolSpec.Strict == nil || *payload.ToolConfig.Tools[0].ToolSpec.Strict != true {
		t.Fatalf("expected strict=true for a require tool on a strict-capable model")
	}

	context.Messages[0].(types.SystemMessage).ToolsAdded[0].ConstrainedSampling = map[string]any{
		"type":   "json_schema",
		"strict": "prefer",
	}
	novaPayload := captureBedrockPayload(t, context, bedrockNovaModel)
	if novaPayload.ToolConfig == nil || len(novaPayload.ToolConfig.Tools) != 1 || novaPayload.ToolConfig.Tools[0].ToolSpec == nil {
		t.Fatalf("expected one tool spec, got %+v", novaPayload.ToolConfig)
	}
	if novaPayload.ToolConfig.Tools[0].ToolSpec.Strict != nil {
		t.Fatalf("expected strict omitted for a prefer tool on a non-strict model")
	}
}

func TestBedrockPreservesEmptyPropertyNamesInStreamedToolArguments(t *testing.T) {
	mock := &bedrockMockClient{}
	mock.install(t)
	mock.events = []brtypes.ConverseStreamOutput{
		&brtypes.ConverseStreamOutputMemberMessageStart{Value: brtypes.MessageStartEvent{Role: brtypes.ConversationRoleAssistant}},
		&brtypes.ConverseStreamOutputMemberContentBlockStart{Value: brtypes.ContentBlockStartEvent{
			ContentBlockIndex: int32Ptr(0),
			Start:             &brtypes.ContentBlockStartMemberToolUse{Value: brtypes.ToolUseBlockStart{ToolUseId: strPtr("tool-1"), Name: strPtr("edit")}},
		}},
		&brtypes.ConverseStreamOutputMemberContentBlockDelta{Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: int32Ptr(0),
			Delta: &brtypes.ContentBlockDeltaMemberToolUse{Value: brtypes.ToolUseBlockDelta{
				Input: strPtr(`{"path":"/workspace/foobar/file.js","edits":[{"oldText":"first","newText":"updated first"},{"oldText":"second","newText":"updated second","":""}]}`),
			}},
		}},
		&brtypes.ConverseStreamOutputMemberContentBlockStop{Value: brtypes.ContentBlockStopEvent{ContentBlockIndex: int32Ptr(0)}},
		&brtypes.ConverseStreamOutputMemberMessageStop{Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonToolUse}},
	}

	s := streamBedrockConverse(context.Background(), bedrockBaseModel, utils.NormalizeContext(&types.Context{
		Messages: []types.Message{types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{String: "Use the tool"},
			Timestamp: 1,
		}},
	}), &BedrockOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionNone}})
	message := s.Result()

	call, ok := message.Content[0].(types.ToolCall)
	if !ok {
		t.Fatalf("expected a toolCall block, got %T", message.Content[0])
	}
	want := map[string]any{
		"path": "/workspace/foobar/file.js",
		"edits": []any{
			map[string]any{"oldText": "first", "newText": "updated first"},
			map[string]any{"oldText": "second", "newText": "updated second", "": ""},
		},
	}
	if !reflect.DeepEqual(call.Arguments, want) {
		t.Fatalf("arguments = %v, want %v", call.Arguments, want)
	}
}

func TestBedrockConvertMessagesSkipsUnknownContentTypes(t *testing.T) {
	mock := &bedrockMockClient{}
	mock.install(t)

	t.Run("skips unknown user content blocks instead of throwing", func(t *testing.T) {
		context := utils.NormalizeContext(&types.Context{Messages: []types.Message{types.UserMessage{
			Role: types.RoleUser,
			Content: types.UserContent{Parts: []types.Content{
				types.NewTextContent("hello"),
			}},
			Timestamp: 1,
		}}})
		payload := captureBedrockPayload(t, context, bedrockBaseModel)
		if len(payload.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(payload.Messages))
		}
		if len(payload.Messages[0].Content) != 1 || payload.Messages[0].Content[0].Text == nil || *payload.Messages[0].Content[0].Text != "hello" {
			t.Fatalf("unexpected content: %+v", payload.Messages[0].Content)
		}
	})

	t.Run("skips unknown assistant content blocks instead of throwing", func(t *testing.T) {
		// pi feeds a mixed text/unknown assistant message; pig's Message union
		// has no unknown member, so the closest shape is text plus a block the
		// converter skips (image content on a text-only path is still
		// converted — use a nil part instead).
		context := utils.NormalizeContext(&types.Context{Messages: []types.Message{types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{types.NewTextContent("hello")},
			API:        "bedrock-converse-stream",
			Provider:   "amazon-bedrock",
			Model:      bedrockBaseModel.ID,
			Usage:      emptyUsage(),
			StopReason: types.StopReasonStop,
			Timestamp:  1,
		}}})
		payload := captureBedrockPayload(t, context, bedrockBaseModel)
		if len(payload.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(payload.Messages))
		}
		if len(payload.Messages[0].Content) != 1 || payload.Messages[0].Content[0].Text == nil || *payload.Messages[0].Content[0].Text != "hello" {
			t.Fatalf("unexpected content: %+v", payload.Messages[0].Content)
		}
	})

	t.Run("replaces user messages with only unknown content blocks with a placeholder", func(t *testing.T) {
		// pig's UserContent has no unknown member; an empty parts slice takes
		// the same placeholder branch.
		context := utils.NormalizeContext(&types.Context{Messages: []types.Message{types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{Parts: []types.Content{}},
			Timestamp: 1,
		}}})
		payload := captureBedrockPayload(t, context, bedrockBaseModel)
		if len(payload.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(payload.Messages))
		}
		assertSingleTextContent(t, payload.Messages[0].Content, "<empty>")
	})

	t.Run("replaces blank user string content with a placeholder", func(t *testing.T) {
		context := utils.NormalizeContext(&types.Context{Messages: []types.Message{types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{String: "   "},
			Timestamp: 1,
		}}})
		payload := captureBedrockPayload(t, context, bedrockBaseModel)
		assertSingleTextContent(t, payload.Messages[0].Content, "<empty>")
	})

	t.Run("filters blank user text blocks when other content remains", func(t *testing.T) {
		context := utils.NormalizeContext(&types.Context{Messages: []types.Message{types.UserMessage{
			Role: types.RoleUser,
			Content: types.UserContent{Parts: []types.Content{
				types.NewTextContent(""),
				types.NewTextContent("hello"),
			}},
			Timestamp: 1,
		}}})
		payload := captureBedrockPayload(t, context, bedrockBaseModel)
		assertSingleTextContent(t, payload.Messages[0].Content, "hello")
	})

	t.Run("replaces user content emptied by surrogate sanitization with a placeholder", func(t *testing.T) {
		context := utils.NormalizeContext(&types.Context{Messages: []types.Message{types.UserMessage{
			Role:    types.RoleUser,
			Content: types.UserContent{String: "\xed\xa0\xbd"},
		}}})
		payload := captureBedrockPayload(t, context, bedrockBaseModel)
		assertSingleTextContent(t, payload.Messages[0].Content, "<empty>")
	})

	t.Run("skips assistant text blocks emptied by surrogate sanitization", func(t *testing.T) {
		context := utils.NormalizeContext(&types.Context{Messages: []types.Message{types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{types.NewTextContent("\xed\xa0\xbd")},
			API:        "bedrock-converse-stream",
			Provider:   "amazon-bedrock",
			Model:      bedrockBaseModel.ID,
			Usage:      emptyUsage(),
			StopReason: types.StopReasonStop,
			Timestamp:  1,
		}}})
		payload := captureBedrockPayload(t, context, bedrockBaseModel)
		if len(payload.Messages) != 0 {
			t.Fatalf("expected 0 messages, got %d", len(payload.Messages))
		}
	})

	t.Run("replaces blank tool result content with a placeholder", func(t *testing.T) {
		context := utils.NormalizeContext(&types.Context{Messages: []types.Message{types.ToolResultMessage{
			Role:       types.RoleToolResult,
			ToolCallID: "tool-1",
			ToolName:   "tool",
			Content:    []types.Content{types.NewTextContent("")},
			Timestamp:  1,
		}}})
		payload := captureBedrockPayload(t, context, bedrockBaseModel)
		if len(payload.Messages) != 1 || len(payload.Messages[0].Content) != 1 || payload.Messages[0].Content[0].ToolResult == nil {
			t.Fatalf("unexpected messages: %+v", payload.Messages)
		}
		content := payload.Messages[0].Content[0].ToolResult.Content
		if len(content) != 1 || content[0].Text == nil || *content[0].Text != "<empty>" {
			t.Fatalf("unexpected tool result content: %+v", content)
		}
	})

	// Regression for #10794: Claude rejects error tool results that contain
	// images.
	t.Run("moves images of error tool results after the tool results", func(t *testing.T) {
		context := utils.NormalizeContext(&types.Context{Messages: []types.Message{
			types.AssistantMessage{
				Role: types.RoleAssistant,
				Content: []types.Content{
					types.NewToolCall("call-1", "codemode", map[string]any{}),
					types.NewToolCall("call-2", "read", map[string]any{}),
				},
				API: "bedrock-converse-stream", Provider: "amazon-bedrock", Model: bedrockBaseModel.ID,
				Usage: emptyUsage(), StopReason: types.StopReasonToolUse, Timestamp: 1,
			},
			types.ToolResultMessage{
				Role: types.RoleToolResult, ToolCallID: "call-1", ToolName: "codemode",
				Content: []types.Content{
					types.NewTextContent("Script failed"),
					types.NewImageContent("AAAA", "image/png"),
					types.NewTextContent("Script error"),
				},
				IsError: true, Timestamp: 2,
			},
			types.ToolResultMessage{
				Role: types.RoleToolResult, ToolCallID: "call-2", ToolName: "read",
				Content: []types.Content{types.NewImageContent("BBBB", "image/png")},
				IsError: false, Timestamp: 3,
			},
		}})
		payload := captureBedrockPayload(t, context, bedrockBaseModel)
		if len(payload.Messages) != 2 {
			t.Fatalf("unexpected messages: %+v", payload.Messages)
		}
		content := payload.Messages[1].Content
		if len(content) != 4 {
			t.Fatalf("content length = %d, want 4: %+v", len(content), content)
		}
		first := content[0].ToolResult
		if first == nil || first.ToolUseID != "call-1" || first.Status != "error" {
			t.Fatalf("content[0] = %+v", content[0])
		}
		if len(first.Content) != 3 || first.Content[0].Text == nil || *first.Content[0].Text != "Script failed" ||
			first.Content[1].Text == nil || *first.Content[1].Text != "(image attached after the tool results)" ||
			first.Content[2].Text == nil || *first.Content[2].Text != "Script error" {
			t.Fatalf("first tool result content = %+v", first.Content)
		}
		second := content[1].ToolResult
		if second == nil || second.Status != "success" || len(second.Content) != 1 ||
			second.Content[0].Image == nil || second.Content[0].Image.Format != "png" {
			t.Fatalf("content[1] = %+v", content[1])
		}
		if content[2].Text == nil || *content[2].Text != "Images from the error result of tool call call-1 (codemode):" {
			t.Fatalf("content[2] = %+v", content[2])
		}
		if content[3].Image == nil || content[3].Image.Format != "png" {
			t.Fatalf("content[3] = %+v", content[3])
		}
	})

	t.Run("keeps images in error tool results for non-Claude models", func(t *testing.T) {
		context := utils.NormalizeContext(&types.Context{Messages: []types.Message{
			types.ToolResultMessage{
				Role: types.RoleToolResult, ToolCallID: "call-1", ToolName: "codemode",
				Content: []types.Content{
					types.NewTextContent("Script failed"),
					types.NewImageContent("AAAA", "image/png"),
				},
				IsError: true, Timestamp: 1,
			},
		}})
		payload := captureBedrockPayload(t, context, bedrockNovaModel)
		if len(payload.Messages) != 1 || len(payload.Messages[0].Content) != 1 {
			t.Fatalf("unexpected messages: %+v", payload.Messages)
		}
		result := payload.Messages[0].Content[0].ToolResult
		if result == nil || result.Status != "error" || len(result.Content) != 2 {
			t.Fatalf("tool result = %+v", result)
		}
		if result.Content[0].Text == nil || *result.Content[0].Text != "Script failed" || result.Content[1].Image == nil {
			t.Fatalf("tool result content = %+v", result.Content)
		}
	})

	t.Run("skips assistant messages with only unknown content blocks", func(t *testing.T) {
		context := utils.NormalizeContext(&types.Context{Messages: []types.Message{types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{},
			API:        "bedrock-converse-stream",
			Provider:   "amazon-bedrock",
			Model:      bedrockBaseModel.ID,
			Usage:      emptyUsage(),
			StopReason: types.StopReasonStop,
			Timestamp:  1,
		}}})
		payload := captureBedrockPayload(t, context, bedrockBaseModel)
		if len(payload.Messages) != 0 {
			t.Fatalf("expected 0 messages, got %d", len(payload.Messages))
		}
	})

	t.Run("removes empty property names only from replayed Bedrock input", func(t *testing.T) {
		toolArguments := map[string]any{
			"path": "/workspace/foobar/file.js",
			"edits": []any{
				map[string]any{"oldText": "first", "newText": "updated first"},
				map[string]any{"oldText": "second", "newText": "updated second", "": ""},
			},
		}
		context := utils.NormalizeContext(&types.Context{Messages: []types.Message{
			types.AssistantMessage{
				Role: types.RoleAssistant,
				Content: []types.Content{types.ToolCall{
					Kind:      types.KindToolCall,
					ID:        "tool-1",
					Name:      "edit",
					Arguments: toolArguments,
				}},
				API:        "bedrock-converse-stream",
				Provider:   "amazon-bedrock",
				Model:      bedrockBaseModel.ID,
				Usage:      emptyUsage(),
				StopReason: types.StopReasonToolUse,
				Timestamp:  1,
			},
			types.ToolResultMessage{
				Role:       types.RoleToolResult,
				ToolCallID: "tool-1",
				ToolName:   "edit",
				Content:    []types.Content{types.NewTextContent("done")},
				Timestamp:  1,
			},
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Continue"}, Timestamp: 1},
		}})

		payload := captureBedrockPayload(t, context, bedrockBaseModel)
		if len(payload.Messages) != 3 || payload.Messages[0].Content[0].ToolUse == nil {
			t.Fatalf("unexpected messages: %+v", payload.Messages)
		}
		input := payload.Messages[0].Content[0].ToolUse.Input
		want := map[string]any{
			"path": "/workspace/foobar/file.js",
			"edits": []any{
				map[string]any{"oldText": "first", "newText": "updated first"},
				map[string]any{"oldText": "second", "newText": "updated second"},
			},
		}
		if !reflect.DeepEqual(input, want) {
			t.Fatalf("replayed input = %v, want %v", input, want)
		}
		// The source arguments must be untouched (pi: sanitizeBedrockDocument
		// works on a clone).
		edits := toolArguments["edits"].([]any)[1].(map[string]any)
		if _, hasEmptyKey := edits[""]; !hasEmptyKey {
			t.Fatalf("source arguments were mutated: %v", toolArguments)
		}
	})
}

func assertSingleTextContent(t *testing.T, content []bedrockContentBlock, want string) {
	t.Helper()
	if len(content) != 1 || content[0].Text == nil || *content[0].Text != want {
		t.Fatalf("unexpected content: %+v, want single text %q", content, want)
	}
}

func emptyUsage() types.Usage {
	return types.Usage{}
}

func int32Ptr(v int32) *int32 { return &v }

func strPtr(v string) *string { return &v }
