// pi: test/anthropic-error-tool-result-images.test.ts.
package api

import (
	"reflect"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

func anthropicImageBlock(data string) map[string]any {
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type":       "base64",
			"media_type": "image/png",
			"data":       data,
		},
	}
}

func errorImageOptions() *AnthropicOptions {
	options := &AnthropicOptions{}
	options.CacheRetention = types.CacheRetentionNone
	return options
}

// TestAnthropicErrorToolResultImages is the regression for #10794: Anthropic
// rejects error tool results that contain images.
func TestAnthropicErrorToolResultImages(t *testing.T) {
	model := anthropicTestModel("claude-test", types.ProviderAnthropic, nil)
	model.Input = []types.ModelInput{types.ModelInputText, types.ModelInputImage}
	model.BaseURL = unreachableAnthropicBaseURL

	assistant := types.AssistantMessage{
		Role: types.RoleAssistant,
		Content: []types.Content{
			types.NewToolCall("call-1", "codemode", map[string]any{}),
			types.NewToolCall("call-2", "read", map[string]any{}),
		},
		API: types.ApiAnthropicMessages, Provider: types.ProviderAnthropic, Model: "claude-test",
		Usage: types.Usage{}, StopReason: types.StopReasonToolUse, Timestamp: 1,
	}
	user := func(text string, ts int64) types.UserMessage {
		return types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: text}, Timestamp: ts}
	}

	t.Run("moves the images after the tool results of the turn", func(t *testing.T) {
		llmContext := utils.NormalizeContext(&types.Context{Messages: []types.Message{
			user("Show the screenshots", 1),
			assistant,
			types.ToolResultMessage{
				Role: types.RoleToolResult, ToolCallID: "call-1", ToolName: "codemode",
				Content: []types.Content{
					types.NewTextContent("Script failed"),
					types.NewImageContent("AAAA", "image/png"),
					types.NewImageContent("CCCC", "image/png"),
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

		payload := captureAnthropicPayload(t, model, llmContext, errorImageOptions(), nil)
		messages, ok := payload["messages"].([]map[string]any)
		if !ok || len(messages) < 3 {
			t.Fatalf("messages = %#v", payload["messages"])
		}
		got := messages[2]
		want := map[string]any{
			"role": "user",
			"content": []map[string]any{
				map[string]any{
					"type": "tool_result", "tool_use_id": "call-1",
					"content":  "Script failed\n(image attached after the tool results)\nScript error",
					"is_error": true,
				},
				map[string]any{
					"type": "tool_result", "tool_use_id": "call-2",
					"content":  []map[string]any{{"type": "text", "text": "(see attached image)"}, anthropicImageBlock("BBBB")},
					"is_error": false,
				},
				map[string]any{"type": "text", "text": "Images from the error result of tool call call-1 (codemode):"},
				anthropicImageBlock("AAAA"),
				anthropicImageBlock("CCCC"),
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("messages[2] =\n%#v\nwant\n%#v", got, want)
		}
	})

	t.Run("keeps error tool results without images unchanged", func(t *testing.T) {
		llmContext := utils.NormalizeContext(&types.Context{Messages: []types.Message{
			user("Run it", 1),
			func() types.AssistantMessage {
				copy := assistant
				copy.Content = []types.Content{assistant.Content[0]}
				return copy
			}(),
			types.ToolResultMessage{
				Role: types.RoleToolResult, ToolCallID: "call-1", ToolName: "codemode",
				Content: []types.Content{types.NewTextContent("Script failed")},
				IsError: true, Timestamp: 2,
			},
		}})

		payload := captureAnthropicPayload(t, model, llmContext, errorImageOptions(), nil)
		messages := payload["messages"].([]map[string]any)
		got := messages[2]
		want := map[string]any{
			"role": "user",
			"content": []map[string]any{
				map[string]any{
					"type": "tool_result", "tool_use_id": "call-1",
					"content":  "Script failed",
					"is_error": true,
				},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("messages[2] =\n%#v\nwant\n%#v", got, want)
		}
	})
}
