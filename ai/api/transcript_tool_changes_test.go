// Ported from pi's test/transcript-tool-changes.test.ts (#9548): end-to-end
// payload assertions across the anthropic-messages, openai-responses, and
// openai-completions adapters for mid-conversation system messages and tool
// state changes.
//
// Note: pi places this file in packages/ai/test and reaches the adapters via
// streamSimple dispatch; the S6-1 handoff suggested ai/utils, but pig's
// adapters live in ai/api and utils is a leaf (import cycle), so the port
// lives here against ai.StreamSimple dispatch instead.
package api

import (
	gocontext "context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

func transcriptTool(name string, description ...string) types.Tool {
	desc := name + " tool"
	if len(description) > 0 {
		desc = description[0]
	}
	return types.Tool{Name: name, Description: desc, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}
}

// captureTranscriptPayload streams via the api registry and captures the
// request payload through OnPayload; the request then fails against the
// unreachable base URL (pi: the PayloadCaptured throw in onPayload — pig's
// OnPayload cannot throw, so the 127.0.0.1:9 base URL fails the request
// after capture instead).
func captureTranscriptPayload(t *testing.T, model *types.Model, llmContext *types.Context) map[string]any {
	t.Helper()
	var captured map[string]any
	stream := ai.StreamSimple(gocontext.Background(), model, llmContext, &ai.SimpleStreamOptions{
		StreamOptions: types.StreamOptions{
			ProviderRequestOptions: types.ProviderRequestOptions{
				APIKey: "test-key",
				OnPayload: func(payload any, model *types.Model) any {
					if m, ok := payload.(map[string]any); ok {
						captured = m
					}
					return nil
				},
			},
		},
	})
	stream.Result()
	if captured == nil {
		t.Fatal("Expected payload capture")
	}
	// Normalize through JSON so nested slices are uniformly []any /
	// map[string]any regardless of the live params shape.
	normalized, err := json.Marshal(captured)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(normalized, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func transcriptModelBase(t *testing.T) *types.Model {
	t.Helper()
	return &types.Model{
		BaseURL:       "http://127.0.0.1:9",
		Reasoning:     true,
		Input:         []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 100000,
		MaxTokens:     1000,
	}
}

// transcriptContext mirrors the shared `context` fixture: a base prompt with
// two sections and one tool, then a later update that rewrites one section,
// removes another, and swaps the tool.
func transcriptContext() *types.Context {
	return &types.Context{Messages: []types.Message{
		types.SystemMessage{
			Role:       types.RoleSystem,
			Content:    types.SystemContent{String: "base prompt"},
			Sections:   &types.SystemSections{Keys: []string{"rules", "docs"}, Values: map[string]*string{"rules": strPtr("<rules>\nold rules\n</rules>"), "docs": strPtr("<docs>\nread docs\n</docs>")}},
			ToolsAdded: []types.Tool{transcriptTool("base_tool")},
			Timestamp:  0,
		},
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "before"}, Timestamp: 1},
		types.SystemMessage{
			Role:         types.RoleSystem,
			Content:      types.SystemContent{String: "updated guidance"},
			Sections:     &types.SystemSections{Keys: []string{"rules", "docs"}, Values: map[string]*string{"rules": strPtr("<rules>\nnew rules\n</rules>"), "docs": nil}},
			ToolsRemoved: []types.ToolReference{{Name: "base_tool"}},
			ToolsAdded:   []types.Tool{transcriptTool("late_tool")},
			Timestamp:    2,
		},
	}}
}

// transcriptAdditionContext is the addition-only variant (no removals, no
// section changes).
func transcriptAdditionContext() *types.Context {
	return &types.Context{Messages: []types.Message{
		types.SystemMessage{
			Role:       types.RoleSystem,
			Content:    types.SystemContent{String: "base prompt"},
			ToolsAdded: []types.Tool{transcriptTool("base_tool")},
			Timestamp:  0,
		},
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "before"}, Timestamp: 1},
		types.SystemMessage{
			Role:       types.RoleSystem,
			Content:    types.SystemContent{String: "updated guidance"},
			ToolsAdded: []types.Tool{transcriptTool("late_tool")},
			Timestamp:  2,
		},
	}}
}

// payloadStrings reads a payload key as a []string list; a missing key is
// an empty list.
func payloadStrings(t *testing.T, payload map[string]any, key string) []string {
	t.Helper()
	raw, ok := payload[key].([]any)
	if !ok {
		if _, present := payload[key]; present {
			t.Fatalf("payload[%q] is not a list: %#v", key, payload[key])
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("payload[%q] contains non-string: %#v", key, item)
		}
		out = append(out, s)
	}
	return out
}

// payloadMaps reads a payload key as a []map[string]any list.
func payloadMaps(t *testing.T, payload map[string]any, key string) []map[string]any {
	t.Helper()
	raw, ok := payload[key].([]any)
	if !ok {
		t.Fatalf("payload[%q] is not a list: %#v", key, payload[key])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("payload[%q] contains non-object: %#v", key, item)
		}
		out = append(out, m)
	}
	return out
}

// payloadMessageRoles lists message roles from an anthropic/completions
// payload.
func payloadMessageRoles(t *testing.T, payload map[string]any) []string {
	t.Helper()
	messages := payloadMaps(t, payload, "messages")
	out := make([]string, 0, len(messages))
	for _, m := range messages {
		role, _ := m["role"].(string)
		out = append(out, role)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTranscriptAnthropicNativeToolChanges(t *testing.T) {
	model := transcriptModelBase(t)
	model.ID = "claude-opus-5"
	model.Name = "Claude Opus 5"
	model.API = types.ApiAnthropicMessages
	model.Provider = types.ProviderAnthropic
	model.Compat = &types.AnthropicMessagesCompat{
		SupportsMidConvoSystemMessages: lo.ToPtr(true),
		SupportsMidConvoToolChanges:    lo.ToPtr(true),
	}

	payload := captureTranscriptPayload(t, model, transcriptContext())

	if !containsString(payloadStrings(t, payload, "betas"), "inline-tools-2026-09-15") {
		t.Fatalf("betas = %v", payload["betas"])
	}
	if containsString(payloadStrings(t, payload, "betas"), "mid-conversation-tool-changes-2026-07-01") {
		t.Fatalf("betas = %v", payload["betas"])
	}
	system := payloadMaps(t, payload, "system")
	if len(system) != 1 || system[0]["text"] != "base prompt\n\n<rules>\nold rules\n</rules>\n\n<docs>\nread docs\n</docs>" {
		t.Fatalf("system = %#v", payload["system"])
	}
	// The top-level list holds only the initial tools (with the cache
	// breakpoint) and the deferred placeholder; later tools never appear there.
	tools := payloadMaps(t, payload, "tools")
	if len(tools) != 2 {
		t.Fatalf("tools = %#v", payload["tools"])
	}
	if tools[0]["name"] != "base_tool" {
		t.Fatalf("tools[0] = %#v", tools[0])
	}
	if cacheControl, ok := tools[0]["cache_control"].(map[string]any); !ok || cacheControl["type"] != "ephemeral" {
		t.Fatalf("tools[0].cache_control = %#v", tools[0]["cache_control"])
	}
	if _, has := tools[0]["defer_loading"]; has {
		t.Fatalf("tools[0] must not be deferred: %#v", tools[0])
	}
	if tools[1]["name"] != "__pi_deferred_placeholder__" || tools[1]["defer_loading"] != true {
		t.Fatalf("tools[1] = %#v", tools[1])
	}
	if _, has := tools[1]["cache_control"]; has {
		t.Fatalf("tools[1] must not carry cache_control: %#v", tools[1])
	}

	messages := payloadMaps(t, payload, "messages")
	update := messages[len(messages)-1]
	if update["role"] != "system" {
		t.Fatalf("last message = %#v", update)
	}
	content := update["content"].([]any)
	if len(content) != 3 {
		t.Fatalf("update content = %#v", update["content"])
	}
	textBlock := content[0].(map[string]any)
	removalBlock := content[1].(map[string]any)
	additionBlock := content[2].(map[string]any)
	if textBlock["type"] != "text" {
		t.Fatalf("content[0] = %#v", content[0])
	}
	text, _ := textBlock["text"].(string)
	if !strings.Contains(text, "updated guidance") ||
		!strings.Contains(text, "<rules>\nnew rules\n</rules>") ||
		!strings.Contains(text, "Removed system prompt section \"docs\"") {
		t.Fatalf("update text = %q", text)
	}
	removalTool := removalBlock["tool"].(map[string]any)
	if removalBlock["type"] != "tool_removal" || removalTool["type"] != "tool_reference" || removalTool["name"] != "base_tool" {
		t.Fatalf("content[1] = %#v", content[1])
	}
	if additionBlock["type"] != "tool_addition" {
		t.Fatalf("content[2] = %#v", content[2])
	}
	additionTool := additionBlock["tool"].(map[string]any)
	if additionTool["type"] != "tool_definition" {
		t.Fatalf("content[2].tool = %#v", additionTool)
	}
	definition := additionTool["definition"].(map[string]any)
	if definition["name"] != "late_tool" || definition["description"] != "late_tool tool" {
		t.Fatalf("tool_addition definition = %#v", definition)
	}
	// Cache control goes on the block, never on the inline definition;
	// nothing is deferred.
	if cacheControl, ok := additionBlock["cache_control"].(map[string]any); !ok || cacheControl["type"] != "ephemeral" {
		t.Fatalf("content[2].cache_control = %#v", additionBlock["cache_control"])
	}
	if _, has := definition["cache_control"]; has {
		t.Fatalf("definition must not carry cache_control: %#v", definition)
	}
	if _, has := definition["defer_loading"]; has {
		t.Fatalf("definition must not be deferred: %#v", definition)
	}

	// The placeholder is declared before any change so its scaffolding is
	// cached from request one.
	initial := captureTranscriptPayload(t, model, &types.Context{Messages: transcriptContext().Messages[:2]})
	initialTools := payloadMaps(t, initial, "tools")
	names := make([]string, 0, len(initialTools))
	for _, tool := range initialTools {
		name, _ := tool["name"].(string)
		names = append(names, name)
	}
	if !equalStrings(names, []string{"base_tool", "__pi_deferred_placeholder__"}) {
		t.Fatalf("initial tools = %v", names)
	}
}

func TestTranscriptAnthropicRedefinesToolByValueUnderSameName(t *testing.T) {
	model := transcriptModelBase(t)
	model.ID = "claude-opus-5"
	model.Name = "Claude Opus 5"
	model.API = types.ApiAnthropicMessages
	model.Provider = types.ProviderAnthropic
	model.Compat = &types.AnthropicMessagesCompat{
		SupportsMidConvoSystemMessages: lo.ToPtr(true),
		SupportsMidConvoToolChanges:    lo.ToPtr(true),
	}

	redefinedTool := transcriptTool("base_tool", "changed")
	payload := captureTranscriptPayload(t, model, &types.Context{Messages: []types.Message{
		types.SystemMessage{Role: types.RoleSystem, Content: types.SystemContent{String: "base prompt"}, ToolsAdded: []types.Tool{transcriptTool("base_tool")}, Timestamp: 0},
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "before"}, Timestamp: 1},
		types.SystemMessage{Role: types.RoleSystem, ToolsRemoved: []types.ToolReference{{Name: "base_tool"}}, ToolsAdded: []types.Tool{redefinedTool}, Timestamp: 2},
	}})

	if !containsString(payloadStrings(t, payload, "betas"), "inline-tools-2026-09-15") {
		t.Fatalf("betas = %v", payload["betas"])
	}
	// The top-level declaration keeps the original definition, so the cached
	// prefix survives.
	tools := payloadMaps(t, payload, "tools")
	if len(tools) != 2 || tools[0]["name"] != "base_tool" || tools[0]["description"] != "base_tool tool" || tools[1]["name"] != "__pi_deferred_placeholder__" {
		t.Fatalf("tools = %#v", payload["tools"])
	}
	// The new definition replaces the old one in place; no removal block is
	// sent for it.
	messages := payloadMaps(t, payload, "messages")
	content := messages[len(messages)-1]["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("last message content = %#v", content)
	}
	additionBlock := content[0].(map[string]any)
	if additionBlock["type"] != "tool_addition" {
		t.Fatalf("content[0] = %#v", additionBlock)
	}
	additionTool := additionBlock["tool"].(map[string]any)
	if additionTool["type"] != "tool_definition" {
		t.Fatalf("content[0].tool = %#v", additionTool)
	}
	definition := additionTool["definition"].(map[string]any)
	if definition["name"] != "base_tool" || definition["description"] != "changed" {
		t.Fatalf("tool_addition definition = %#v", definition)
	}
}

func TestTranscriptAnthropicNoInitialToolFallsBackToCurrentToolList(t *testing.T) {
	model := transcriptModelBase(t)
	model.ID = "claude-opus-5"
	model.Name = "Claude Opus 5"
	model.API = types.ApiAnthropicMessages
	model.Provider = types.ProviderAnthropic
	model.Compat = &types.AnthropicMessagesCompat{
		SupportsMidConvoSystemMessages: lo.ToPtr(true),
		SupportsMidConvoToolChanges:    lo.ToPtr(true),
	}

	// Anthropic rejects a tool list where every tool (here: the placeholder)
	// is deferred.
	payload := captureTranscriptPayload(t, model, &types.Context{Messages: []types.Message{
		types.SystemMessage{Role: types.RoleSystem, Content: types.SystemContent{String: "base prompt"}, Timestamp: 0},
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "before"}, Timestamp: 1},
		types.SystemMessage{Role: types.RoleSystem, Content: types.SystemContent{String: "updated guidance"}, ToolsAdded: []types.Tool{transcriptTool("late_tool")}, Timestamp: 2},
	}})

	if containsString(payloadStrings(t, payload, "betas"), "inline-tools-2026-09-15") {
		t.Fatalf("betas = %v", payload["betas"])
	}
	tools := payloadMaps(t, payload, "tools")
	if len(tools) != 1 || tools[0]["name"] != "late_tool" {
		t.Fatalf("tools = %#v", payload["tools"])
	}
	if cacheControl, ok := tools[0]["cache_control"].(map[string]any); !ok || cacheControl["type"] != "ephemeral" {
		t.Fatalf("tools[0].cache_control = %#v", tools[0]["cache_control"])
	}
	if _, has := tools[0]["defer_loading"]; has {
		t.Fatalf("tools[0] must not be deferred: %#v", tools[0])
	}
	messages := payloadMaps(t, payload, "messages")
	content := messages[len(messages)-1]["content"].([]any)
	types2 := make([]string, 0, len(content))
	for _, block := range content {
		typeName, _ := block.(map[string]any)["type"].(string)
		types2 = append(types2, typeName)
	}
	if !equalStrings(types2, []string{"text"}) {
		t.Fatalf("last message content types = %v", types2)
	}
}

func TestTranscriptAnthropicFoldsWithoutNativeSupport(t *testing.T) {
	model := transcriptModelBase(t)
	model.ID = "claude-sonnet-4-5"
	model.Name = "Claude Sonnet 4.5"
	model.API = types.ApiAnthropicMessages
	model.Provider = types.ProviderAnthropic

	payload := captureTranscriptPayload(t, model, transcriptContext())

	if containsString(payloadStrings(t, payload, "betas"), "inline-tools-2026-09-15") {
		t.Fatalf("betas = %v", payload["betas"])
	}
	system := payloadMaps(t, payload, "system")
	if len(system) != 1 || system[0]["text"] != "base prompt\n\nupdated guidance\n\n<rules>\nnew rules\n</rules>" {
		t.Fatalf("system = %#v", payload["system"])
	}
	tools := payloadMaps(t, payload, "tools")
	if len(tools) != 1 || tools[0]["name"] != "late_tool" {
		t.Fatalf("tools = %#v", payload["tools"])
	}
	if !equalStrings(payloadMessageRoles(t, payload), []string{"user"}) {
		t.Fatalf("messages = %#v", payload["messages"])
	}
}

func TestTranscriptAnthropicRequiresBothCapabilitiesForNativeToolChanges(t *testing.T) {
	model := transcriptModelBase(t)
	model.ID = "claude-opus-5"
	model.Name = "Claude Opus 5"
	model.API = types.ApiAnthropicMessages
	model.Provider = types.ProviderAnthropic
	model.Compat = &types.AnthropicMessagesCompat{SupportsMidConvoToolChanges: lo.ToPtr(true)}

	payload := captureTranscriptPayload(t, model, transcriptContext())

	if containsString(payloadStrings(t, payload, "betas"), "inline-tools-2026-09-15") {
		t.Fatalf("betas = %v", payload["betas"])
	}
	tools := payloadMaps(t, payload, "tools")
	if len(tools) != 1 || tools[0]["name"] != "late_tool" {
		t.Fatalf("tools = %#v", payload["tools"])
	}
	if !equalStrings(payloadMessageRoles(t, payload), []string{"user"}) {
		t.Fatalf("messages = %#v", payload["messages"])
	}
}

func TestTranscriptOpenAIAdditionsAnchoredAtDeveloperMessage(t *testing.T) {
	model := transcriptModelBase(t)
	model.ID = "gpt-5.4"
	model.Name = "GPT-5.4"
	model.API = types.ApiOpenAIResponses
	model.Provider = "openai"
	model.Compat = &types.OpenAIResponsesCompat{
		SupportsMidConvoSystemMessages: lo.ToPtr(true),
		SupportsAdditionalTools:        lo.ToPtr(true),
	}

	payload := captureTranscriptPayload(t, model, transcriptAdditionContext())

	tools := payloadMaps(t, payload, "tools")
	toolNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		toolNames = append(toolNames, name)
	}
	if !equalStrings(toolNames, []string{"base_tool"}) {
		t.Fatalf("tools = %v", toolNames)
	}
	input := payloadMaps(t, payload, "input")
	var additionTools []string
	for _, item := range input {
		if item["type"] == "additional_tools" {
			nested := payloadMaps(t, item, "tools")
			for _, tool := range nested {
				name, _ := tool["name"].(string)
				additionTools = append(additionTools, name)
			}
		}
	}
	if !equalStrings(additionTools, []string{"late_tool"}) {
		t.Fatalf("additional_tools = %v", additionTools)
	}
	var developerContents []string
	for _, item := range input {
		role, _ := item["role"].(string)
		if role == "developer" {
			if _, hasType := item["type"]; hasType {
				continue
			}
			content, _ := item["content"].(string)
			developerContents = append(developerContents, content)
		}
	}
	if !equalStrings(developerContents, []string{"base prompt", "updated guidance"}) {
		t.Fatalf("developer contents = %v", developerContents)
	}
}

func TestTranscriptOpenAISyntheticToolSearch(t *testing.T) {
	model := transcriptModelBase(t)
	model.ID = "gpt-5.4"
	model.Name = "GPT-5.4"
	model.API = types.ApiOpenAIResponses
	model.Provider = "openai"
	model.Compat = &types.OpenAIResponsesCompat{
		SupportsMidConvoSystemMessages: lo.ToPtr(true),
		SupportsToolSearch:             lo.ToPtr(true),
	}

	payload := captureTranscriptPayload(t, model, transcriptAdditionContext())

	tools := payloadMaps(t, payload, "tools")
	if len(tools) != 1 || tools[0]["name"] != "base_tool" {
		t.Fatalf("tools = %#v", payload["tools"])
	}
	input := payloadMaps(t, payload, "input")
	hasSearchCall := false
	var searchOutputTools []string
	for _, item := range input {
		if item["type"] == "tool_search_call" {
			hasSearchCall = true
		}
		if item["type"] == "tool_search_output" {
			nested := payloadMaps(t, item, "tools")
			for _, tool := range nested {
				name, _ := tool["name"].(string)
				searchOutputTools = append(searchOutputTools, name)
			}
		}
	}
	if !hasSearchCall {
		t.Fatalf("no tool_search_call in input: %#v", payload["input"])
	}
	if !equalStrings(searchOutputTools, []string{"late_tool"}) {
		t.Fatalf("tool_search_output tools = %v", searchOutputTools)
	}
}

func TestTranscriptOpenAIFoldsWithoutNativeSupport(t *testing.T) {
	model := transcriptModelBase(t)
	model.ID = "gpt-4.1"
	model.Name = "GPT-4.1"
	model.API = types.ApiOpenAIResponses
	model.Provider = "openai"
	model.Compat = &types.OpenAIResponsesCompat{SupportsAdditionalTools: lo.ToPtr(true)}

	payload := captureTranscriptPayload(t, model, transcriptContext())

	tools := payloadMaps(t, payload, "tools")
	toolNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		toolNames = append(toolNames, name)
	}
	if !equalStrings(toolNames, []string{"late_tool"}) {
		t.Fatalf("tools = %v", toolNames)
	}
	input := payloadMaps(t, payload, "input")
	shape := make([]string, 0, len(input))
	for _, item := range input {
		if typeName, has := item["type"].(string); has && typeName != "" {
			shape = append(shape, typeName)
			continue
		}
		role, _ := item["role"].(string)
		shape = append(shape, role)
	}
	if !equalStrings(shape, []string{"developer", "user"}) {
		t.Fatalf("input shape = %v", shape)
	}
	firstContent, _ := input[0]["content"].(string)
	if firstContent != "base prompt\n\nupdated guidance\n\n<rules>\nnew rules\n</rules>" {
		t.Fatalf("input[0].content = %q", firstContent)
	}
}

func TestTranscriptOpenAIFallsBackToCurrentToolState(t *testing.T) {
	model := transcriptModelBase(t)
	model.ID = "gpt-5.4"
	model.Name = "GPT-5.4"
	model.API = types.ApiOpenAIResponses
	model.Provider = "openai"
	model.Compat = &types.OpenAIResponsesCompat{
		SupportsMidConvoSystemMessages: lo.ToPtr(true),
		SupportsAdditionalTools:        lo.ToPtr(true),
	}

	payload := captureTranscriptPayload(t, model, transcriptContext())

	tools := payloadMaps(t, payload, "tools")
	toolNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		toolNames = append(toolNames, name)
	}
	if !equalStrings(toolNames, []string{"late_tool"}) {
		t.Fatalf("tools = %v", toolNames)
	}
	input := payloadMaps(t, payload, "input")
	for _, item := range input {
		if item["type"] == "additional_tools" {
			t.Fatalf("unexpected additional_tools: %#v", item)
		}
	}
	developerCount := 0
	for _, item := range input {
		if role, _ := item["role"].(string); role == "developer" {
			developerCount++
		}
	}
	if developerCount != 2 {
		t.Fatalf("developer messages = %d (%#v)", developerCount, payload["input"])
	}
}

func TestTranscriptKimiAdditionsInToolBearingSystemMessages(t *testing.T) {
	model := transcriptModelBase(t)
	model.ID = "kimi-k3"
	model.Name = "Kimi K3"
	model.API = types.ApiOpenAICompletions
	model.Provider = "moonshotai"
	model.Compat = &types.OpenAICompletionsCompat{
		SupportsMidConvoSystemMessages: lo.ToPtr(true),
		SupportsMidConvoToolAdditions:  lo.ToPtr(true),
	}

	payload := captureTranscriptPayload(t, model, transcriptAdditionContext())

	tools := payloadMaps(t, payload, "tools")
	toolNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		function := tool["function"].(map[string]any)
		name, _ := function["name"].(string)
		toolNames = append(toolNames, name)
	}
	if !equalStrings(toolNames, []string{"base_tool"}) {
		t.Fatalf("tools = %v", toolNames)
	}
	messages := payloadMaps(t, payload, "messages")
	var toolBearer map[string]any
	var systemContents []*string
	for _, message := range messages {
		if role, _ := message["role"].(string); role == "system" {
			if content, has := message["content"]; has && content != nil {
				s := content.(string)
				systemContents = append(systemContents, &s)
			} else {
				systemContents = append(systemContents, nil)
			}
		}
		if _, has := message["tools"]; has {
			toolBearer = message
		}
	}
	if toolBearer == nil {
		t.Fatalf("no tool-bearing message: %#v", payload["messages"])
	}
	bearerTools := payloadMaps(t, toolBearer, "tools")
	bearerNames := make([]string, 0, len(bearerTools))
	for _, tool := range bearerTools {
		function := tool["function"].(map[string]any)
		name, _ := function["name"].(string)
		bearerNames = append(bearerNames, name)
	}
	if !equalStrings(bearerNames, []string{"late_tool"}) {
		t.Fatalf("tool-bearing tools = %v", bearerNames)
	}
	if len(systemContents) != 3 || systemContents[0] == nil || *systemContents[0] != "base prompt" ||
		systemContents[1] != nil || systemContents[2] == nil || *systemContents[2] != "updated guidance" {
		t.Fatalf("system contents = %#v", systemContents)
	}
}

// kimiCodingTranscriptModel fetches a generated kimi-coding model and points
// it at the unreachable base URL used by the capture helper.
func kimiCodingTranscriptModel(t *testing.T, id string) *types.Model {
	t.Helper()
	model, ok := ai.MODELS[types.ProviderKimiCoding][id]
	if !ok {
		t.Fatalf("model kimi-coding/%s not in catalog", id)
	}
	model.BaseURL = "http://127.0.0.1:9"
	return &model
}

// TestTranscriptKimiCodingAnchorAdditions is pi #10720: Kimi Coding uses Chat
// Completions so K3-style models keep added tools out of the top-level list.
func TestTranscriptKimiCodingAnchorAdditions(t *testing.T) {
	for _, modelID := range []string{"k3", "k3-256k", "kimi-for-coding"} {
		modelID := modelID
		t.Run("anchors additions for Kimi Coding "+modelID, func(t *testing.T) {
			payload := captureTranscriptPayload(t, kimiCodingTranscriptModel(t, modelID), transcriptAdditionContext())

			var toolNames []string
			for _, tool := range payloadMaps(t, payload, "tools") {
				function := tool["function"].(map[string]any)
				name, _ := function["name"].(string)
				toolNames = append(toolNames, name)
			}
			if !equalStrings(toolNames, []string{"base_tool"}) {
				t.Fatalf("tools = %v", toolNames)
			}
			var bearerNames []string
			for _, message := range payloadMaps(t, payload, "messages") {
				if _, has := message["tools"]; !has {
					continue
				}
				for _, tool := range payloadMaps(t, message, "tools") {
					function := tool["function"].(map[string]any)
					name, _ := function["name"].(string)
					bearerNames = append(bearerNames, name)
				}
			}
			if !equalStrings(bearerNames, []string{"late_tool"}) {
				t.Fatalf("tool-bearing tools = %v", bearerNames)
			}
		})
	}
}

// TestTranscriptKimiCodingHighspeedTopLevelTools is pi #10720: Kimi Code
// Highspeed rejects tool-bearing system messages and keeps added tools
// top-level.
func TestTranscriptKimiCodingHighspeedTopLevelTools(t *testing.T) {
	payload := captureTranscriptPayload(t, kimiCodingTranscriptModel(t, "kimi-for-coding-highspeed"), transcriptAdditionContext())

	var toolNames []string
	for _, tool := range payloadMaps(t, payload, "tools") {
		function := tool["function"].(map[string]any)
		name, _ := function["name"].(string)
		toolNames = append(toolNames, name)
	}
	if !equalStrings(toolNames, []string{"base_tool", "late_tool"}) {
		t.Fatalf("tools = %v", toolNames)
	}
	for _, message := range payloadMaps(t, payload, "messages") {
		if _, has := message["tools"]; has {
			t.Fatalf("unexpected dynamic tool message: %#v", message)
		}
	}
}

func TestTranscriptKimiK2SystemTextInline(t *testing.T) {
	model := transcriptModelBase(t)
	model.ID = "kimi-k2.7-code"
	model.Name = "Kimi K2.7 Code"
	model.API = types.ApiOpenAICompletions
	model.Provider = "moonshotai"
	model.Compat = &types.OpenAICompletionsCompat{SupportsMidConvoSystemMessages: lo.ToPtr(true)}

	payload := captureTranscriptPayload(t, model, transcriptAdditionContext())

	tools := payloadMaps(t, payload, "tools")
	toolNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		function := tool["function"].(map[string]any)
		name, _ := function["name"].(string)
		toolNames = append(toolNames, name)
	}
	if !equalStrings(toolNames, []string{"base_tool", "late_tool"}) {
		t.Fatalf("tools = %v", toolNames)
	}
	messages := payloadMaps(t, payload, "messages")
	for _, message := range messages {
		if _, has := message["tools"]; has {
			t.Fatalf("unexpected dynamic tool message: %#v", message)
		}
	}
	var systemContents []string
	for _, message := range messages {
		if role, _ := message["role"].(string); role == "system" {
			content, _ := message["content"].(string)
			systemContents = append(systemContents, content)
		}
	}
	if !equalStrings(systemContents, []string{"base prompt", "updated guidance"}) {
		t.Fatalf("system contents = %v", systemContents)
	}
}

func TestTranscriptCompletionsFoldsWithoutNativeSupport(t *testing.T) {
	model := transcriptModelBase(t)
	model.ID = "custom-model"
	model.Name = "Custom model"
	model.API = types.ApiOpenAICompletions
	model.Provider = "custom-provider"
	model.Reasoning = false

	payload := captureTranscriptPayload(t, model, transcriptContext())

	tools := payloadMaps(t, payload, "tools")
	toolNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		function := tool["function"].(map[string]any)
		name, _ := function["name"].(string)
		toolNames = append(toolNames, name)
	}
	if !equalStrings(toolNames, []string{"late_tool"}) {
		t.Fatalf("tools = %v", toolNames)
	}
	messages := payloadMaps(t, payload, "messages")
	if len(messages) != 2 || messages[0]["role"] != "system" || messages[1]["role"] != "user" {
		t.Fatalf("messages = %#v", payload["messages"])
	}
	content, _ := messages[0]["content"].(string)
	if content != "base prompt\n\nupdated guidance\n\n<rules>\nnew rules\n</rules>" {
		t.Fatalf("messages[0].content = %q", content)
	}
}
