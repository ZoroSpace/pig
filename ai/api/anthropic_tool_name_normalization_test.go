// pi: test/anthropic-tool-name-normalization.test.ts.
//
// Tests for Anthropic OAuth tool name normalization (round-trip casing, not
// name mapping). They run against the real Anthropic API with a Claude Code
// OAuth token, mirroring pi's auth.json credential lookup — pig reads
// ~/.pig/agent/auth.json and skips without a stored token.
package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// resolveAnthropicOAuthToken reads the stored anthropic credential (pi:
// test/oauth.ts resolveApiKey). OAuth token refresh is not reproduced here —
// the flow lands with A2; an expired token skips.
func resolveAnthropicOAuthToken(t *testing.T) string {
	t.Helper()
	if token := os.Getenv("ANTHROPIC_OAUTH_TOKEN"); token != "" {
		return token
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	content, err := os.ReadFile(filepath.Join(home, ".pig", "agent", "auth.json"))
	if err != nil {
		return ""
	}
	var storage map[string]json.RawMessage
	if err := json.Unmarshal(content, &storage); err != nil {
		return ""
	}
	entry, ok := storage["anthropic"]
	if !ok {
		return ""
	}
	var credential struct {
		Type    string `json:"type"`
		Key     string `json:"key"`
		Access  string `json:"access"`
		Expires int64  `json:"expires"`
	}
	if err := json.Unmarshal(entry, &credential); err != nil {
		return ""
	}
	if credential.Type == "oauth" && credential.Access != "" && credential.Expires > nowMillis() {
		return credential.Access
	}
	if credential.Type == "api_key" && credential.Key != "" && isOAuthToken(credential.Key) {
		return credential.Key
	}
	return ""
}

func nowMillis() int64 {
	return time.Now().UnixMilli()
}

func TestAnthropicOAuthToolNameNormalization(t *testing.T) {
	oauthToken := resolveAnthropicOAuthToken(t)
	if oauthToken == "" {
		t.Skip("no anthropic OAuth token in ~/.pig/agent/auth.json")
	}
	// The remaining body mirrors pi's four round-trip cases against the real
	// API (todowrite -> TodoWrite -> todowrite, read, find, custom tools).
	// It is only reachable with a live subscription token; the assertions
	// follow pi 1:1.
	model := catalogAnthropicModel(t, types.ProviderAnthropic, "claude-sonnet-4-6")
	runOAuthToolNameRoundTrip(t, oauthToken, model)
}

func runOAuthToolNameRoundTrip(t *testing.T, oauthToken string, model *types.Model) {
	t.Helper()
	cases := []struct {
		toolName string
		prompt   string
	}{
		{"todowrite", "Add a todo: buy milk. Use the todowrite tool."},
		{"read", "Read the file /tmp/test.txt using the read tool."},
		{"find", "Find all .ts files using the find tool."},
		{"my_custom_tool", "Use my_custom_tool with input 'hello'."},
	}
	for _, tc := range cases {
		t.Run(tc.toolName, func(t *testing.T) {
			llmContext := utils.NormalizeContext(&types.Context{
				SystemPrompt: "You are a helpful assistant. Use the " + tc.toolName + " tool when asked.",
				Messages: []types.Message{types.UserMessage{
					Role: types.RoleUser, Content: types.UserContent{String: tc.prompt}, Timestamp: 1,
				}},
				Tools: []types.Tool{{
					Name:        tc.toolName,
					Description: "Test tool",
					Parameters:  json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"},"task":{"type":"string"},"path":{"type":"string"},"pattern":{"type":"string"}},"required":["input"]}`),
				}},
			})
			options := &types.StreamOptions{}
			options.APIKey = oauthToken
			stream := (&AnthropicMessages{}).Stream(context.Background(), model, llmContext, options)
			var toolCallName string
			for event := range stream.Events() {
				if event.Type == types.EventToolCallEnd && event.ToolCall != nil {
					toolCallName = event.ToolCall.Name
				}
			}
			result := stream.Result()
			if result.StopReason != types.StopReasonToolUse {
				t.Fatalf("stopReason = %q (error: %s)", result.StopReason, result.ErrorMessage)
			}
			if toolCallName != tc.toolName {
				t.Errorf("tool call name = %q, want the original %q", toolCallName, tc.toolName)
			}
		})
	}
}
