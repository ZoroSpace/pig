package types

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/samber/lo"
)

func roundtripMessage(t *testing.T, in Message) Message {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := DecodeMessage(data)
	if err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("roundtrip mismatch:\nin  = %#v\nout = %#v\njson = %s", in, out, data)
	}
	return out
}

func TestUserContentStringForm(t *testing.T) {
	data := []byte(`{"role":"user","content":"hello","timestamp":1}`)
	m, err := DecodeMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	u, ok := m.(UserMessage)
	if !ok {
		t.Fatalf("want UserMessage, got %T", m)
	}
	if u.Content.String != "hello" || u.Content.Parts != nil {
		t.Fatalf("want string form, got %+v", u.Content)
	}
	roundtripMessage(t, u)
}

func TestUserContentArrayForm(t *testing.T) {
	data := []byte(`{"role":"user","content":[{"type":"text","text":"see "},{"type":"image","data":"Zm9v","mimeType":"image/png"}],"timestamp":2}`)
	m, err := DecodeMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	u := m.(UserMessage)
	if u.Content.String != "" || len(u.Content.Parts) != 2 {
		t.Fatalf("want array form, got %+v", u.Content)
	}
	if _, ok := u.Content.Parts[1].(ImageContent); !ok {
		t.Fatalf("want ImageContent, got %T", u.Content.Parts[1])
	}
	roundtripMessage(t, u)
}

func TestAssistantMessageRoundtrip(t *testing.T) {
	text := NewTextContent("partial answer")
	thinking := NewThinkingContent("hmm")
	think := ThinkingContent{Kind: KindThinking, Thinking: "s", ThinkingSignature: "sig", Redacted: true}
	call := NewToolCall("call-1", "read", map[string]any{"path": "/tmp/x"})
	in := AssistantMessage{
		Role:       RoleAssistant,
		Content:    []Content{text, thinking, think, call},
		API:        ApiOpenAICompletions,
		Provider:   "openrouter",
		Model:      "acme/latest",
		Usage:      Usage{Input: 10, Output: 5, TotalTokens: 15},
		StopReason: StopReasonToolUse,
		Timestamp:  3,
	}
	out := roundtripMessage(t, in).(AssistantMessage)
	if len(out.ToolCalls()) != 1 || out.ToolCalls()[0].Name != "read" {
		t.Fatalf("tool calls: %+v", out.ToolCalls())
	}
	if out.Text() != "partial answer" {
		t.Fatalf("text: %q", out.Text())
	}
}

func TestToolResultMessageRoundtrip(t *testing.T) {
	in := ToolResultMessage{
		Role:       RoleToolResult,
		ToolCallID: "call-1",
		ToolName:   "read",
		Content:    []Content{NewTextContent("file body")},
		IsError:    true,
		Timestamp:  4,
	}
	out := roundtripMessage(t, in).(ToolResultMessage)
	if !out.IsError || out.ToolCallID != "call-1" {
		t.Fatalf("%+v", out)
	}
}

func TestDecodeMessageRejectsUnknownRole(t *testing.T) {
	if _, err := DecodeMessage([]byte(`{"role":"bogus"}`)); err == nil {
		t.Fatal("want error for unknown role")
	}
}

// pi v0.86.0 message-types.test.ts, runtime-relevant half: system is a
// transcript role and round-trips with its content/sections/tool fields.
// (The compile-time detail-type assertions have no Go equivalent: pig's
// ToolResultMessage.Details is json.RawMessage.)
func TestDecodeMessageDispatchesSystemRole(t *testing.T) {
	m, err := DecodeMessage([]byte(`{"role":"system","content":"hi","timestamp":1}`))
	if err != nil {
		t.Fatal(err)
	}
	sm, ok := m.(SystemMessage)
	if !ok || sm.Content.String != "hi" || sm.Timestamp != 1 {
		t.Fatalf("%T %+v", m, m)
	}
}

func TestSystemMessageJSONRoundtrip(t *testing.T) {
	in := SystemMessage{
		Role:         RoleSystem,
		Content:      SystemContent{String: "base"},
		Sections:     &SystemSections{Keys: []string{"a", "b"}, Values: map[string]*string{"a": lo.ToPtr("<a>1</a>"), "b": nil}},
		ToolsAdded:   []Tool{{Name: "t", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}},
		ToolsRemoved: []ToolReference{{Name: "old"}},
		Timestamp:    7,
	}
	data, err := json.Marshal(&in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodeMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	sm, ok := out.(SystemMessage)
	if !ok {
		t.Fatalf("%T", out)
	}
	if sm.Content.String != "base" || sm.Timestamp != 7 {
		t.Fatalf("%+v", sm)
	}
	if sm.Sections == nil || len(sm.Sections.Keys) != 2 || sm.Sections.Keys[0] != "a" || sm.Sections.Keys[1] != "b" {
		t.Fatalf("sections = %+v", sm.Sections)
	}
	if *sm.Sections.Values["a"] != "<a>1</a>" || sm.Sections.Values["b"] != nil {
		t.Fatalf("section values = %+v", sm.Sections.Values)
	}
	if len(sm.ToolsAdded) != 1 || sm.ToolsAdded[0].Name != "t" {
		t.Fatalf("toolsAdded = %+v", sm.ToolsAdded)
	}
	if len(sm.ToolsRemoved) != 1 || sm.ToolsRemoved[0].Name != "old" {
		t.Fatalf("toolsRemoved = %+v", sm.ToolsRemoved)
	}

	// Key order survives the marshal/unmarshal cycle (prompt cache
	// invariant; see SystemSections).
	re, _ := json.Marshal(&sm)
	first := bytes.Index(re, []byte(`"a"`))
	second := bytes.Index(re, []byte(`"b"`))
	if first == -1 || second == -1 || first > second {
		t.Fatalf("section key order lost: %s", re)
	}
}

func TestEventJSON(t *testing.T) {
	msg := &AssistantMessage{
		Role:       RoleAssistant,
		Content:    []Content{NewTextContent("done text")},
		API:        ApiOpenAICompletions,
		Provider:   "openai",
		Model:      "gpt-x",
		StopReason: StopReasonStop,
		Timestamp:  5,
	}
	done := AssistantMessageEvent{Type: EventDone, Reason: StopReasonStop, Message: msg}
	data, err := json.Marshal(done)
	if err != nil {
		t.Fatal(err)
	}
	var back AssistantMessageEvent
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Type != EventDone || back.Reason != StopReasonStop || back.Message.StopReason != StopReasonStop {
		t.Fatalf("%+v", back)
	}

	delta := AssistantMessageEvent{Type: EventTextDelta, ContentIndex: 0, Delta: "he", Partial: msg}
	data, _ = json.Marshal(delta)
	var backDelta AssistantMessageEvent
	if err := json.Unmarshal(data, &backDelta); err != nil {
		t.Fatal(err)
	}
	if backDelta.Type != EventTextDelta || backDelta.Delta != "he" || backDelta.ContentIndex != 0 {
		t.Fatalf("%+v", backDelta)
	}
}

func TestChatTemplateKwargValue(t *testing.T) {
	data := []byte(`{
		"enable_thinking": true,
		"thinking_budget": 1024,
		"label": "auto",
		"effort": {"$var": "thinking.effort", "omitWhenOff": true},
		"enabled": {"$var": "thinking.enabled"},
		"empty": null
	}`)
	var kwargs map[string]ChatTemplateKwargValue
	if err := json.Unmarshal(data, &kwargs); err != nil {
		t.Fatal(err)
	}
	if b, ok := kwargs["enable_thinking"].Value.(bool); !ok || !b {
		t.Fatalf("enable_thinking: %#v", kwargs["enable_thinking"].Value)
	}
	if n, ok := kwargs["thinking_budget"].Value.(float64); !ok || n != 1024 {
		t.Fatalf("thinking_budget: %#v", kwargs["thinking_budget"].Value)
	}
	if s, ok := kwargs["label"].Value.(string); !ok || s != "auto" {
		t.Fatalf("label: %#v", kwargs["label"].Value)
	}
	if kwargs["empty"].Value != nil {
		t.Fatalf("empty: %#v", kwargs["empty"].Value)
	}
	effort, ok := kwargs["effort"].Value.(ChatTemplateKwargVar)
	if !ok || effort.Var != ChatTemplateVarThinkingEffort || !effort.OmitWhenOff {
		t.Fatalf("effort: %#v", kwargs["effort"].Value)
	}
	enabled, ok := kwargs["enabled"].Value.(ChatTemplateKwargVar)
	if !ok || enabled.Var != ChatTemplateVarThinkingEnabled || enabled.OmitWhenOff {
		t.Fatalf("enabled: %#v", kwargs["enabled"].Value)
	}
	out, err := json.Marshal(kwargs)
	if err != nil {
		t.Fatal(err)
	}
	var again map[string]ChatTemplateKwargValue
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kwargs, again) {
		t.Fatalf("kwargs roundtrip mismatch:\n%s", out)
	}
}

func TestModelCompatAnthropicDispatch(t *testing.T) {
	data := []byte(`{
		"id": "claude-sonnet-4-5",
		"name": "Claude Sonnet 4.5",
		"api": "anthropic-messages",
		"provider": "anthropic",
		"baseUrl": "https://api.anthropic.com",
		"reasoning": true,
		"input": ["text", "image"],
		"cost": {"input": 3, "output": 15, "cacheRead": 0.3, "cacheWrite": 3.75},
		"contextWindow": 200000,
		"maxTokens": 64000,
		"compat": {
			"supportsStrictTools": true,
			"allowedFallbackModels": [
				{
					"provider": "anthropic",
					"model": "claude-haiku-4-5",
					"cost": {"input": 1, "output": 5, "cacheRead": 0.1, "cacheWrite": 1.25}
				}
			]
		}
	}`)
	var m Model
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	c, ok := m.Compat.(*AnthropicMessagesCompat)
	if !ok {
		t.Fatalf("want *AnthropicMessagesCompat, got %T", m.Compat)
	}
	if c.SupportsStrictTools == nil || *c.SupportsStrictTools != true {
		t.Fatalf("strict tools: %+v", c)
	}
	if len(c.AllowedFallbackModels) != 1 || c.AllowedFallbackModels[0].Provider != ProviderAnthropic {
		t.Fatalf("fallbacks: %+v", c.AllowedFallbackModels)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var again Model
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, again) {
		t.Fatalf("model roundtrip mismatch:\n%s", out)
	}
}

func TestImagesRoundtrip(t *testing.T) {
	data := []byte(`{
		"api": "openrouter-images",
		"provider": "openrouter",
		"model": "google/gemini-2.5-flash-image",
		"output": [{"type": "image", "data": "Zm9v", "mimeType": "image/png"}],
		"stopReason": "stop",
		"timestamp": 6
	}`)
	var in AssistantImages
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	if in.API != ImageApiOpenRouter || in.Provider != "openrouter" {
		t.Fatalf("%+v", in)
	}
	img, ok := in.Output[0].(ImageContent)
	if !ok || img.MimeType != "image/png" {
		t.Fatalf("output: %#v", in.Output[0])
	}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var again AssistantImages
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, again) {
		t.Fatalf("images roundtrip mismatch:\n%s", out)
	}
}

func TestModelCompatRoundtrip(t *testing.T) {
	data := []byte(`{
		"id": "deepseek-chat",
		"name": "DeepSeek",
		"api": "openai-completions",
		"provider": "deepseek",
		"baseUrl": "https://api.deepseek.com/v1",
		"reasoning": true,
		"input": ["text"],
		"cost": {"input": 0.5, "output": 1.5, "cacheRead": 0.1, "cacheWrite": 0.2},
		"contextWindow": 64000,
		"maxTokens": 8000,
		"compat": {
			"supportsStore": false,
			"thinkingFormat": "deepseek",
			"maxTokensField": "max_tokens"
		}
	}`)
	var m Model
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.API != ApiOpenAICompletions {
		t.Fatalf("api: %v", m.API)
	}
	c, ok := m.Compat.(*OpenAICompletionsCompat)
	if !ok {
		t.Fatalf("want *OpenAICompletionsCompat, got %T", m.Compat)
	}
	if c.SupportsStore == nil || *c.SupportsStore != false {
		t.Fatalf("explicit false must survive: %+v", c)
	}
	if c.ThinkingFormat == nil || *c.ThinkingFormat != ThinkingFormatDeepSeek {
		t.Fatalf("thinkingFormat: %+v", c)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var again Model
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, again) {
		t.Fatalf("model roundtrip mismatch:\n%s", out)
	}
}
