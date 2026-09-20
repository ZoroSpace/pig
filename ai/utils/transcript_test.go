// Ported from pi's test/system-message-replay.test.ts (#9548): system
// message replay, collapse, normalization, and tool state diffing over
// ai/utils/transcript.go + text.go helpers.
package utils

import (
	"encoding/json"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

func replayTool(name string, description ...string) types.Tool {
	desc := name + " tool"
	if len(description) > 0 {
		desc = description[0]
	}
	return types.Tool{Name: name, Description: desc, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}
}

func sectionsOf(pairs ...any) *types.SystemSections {
	s := &types.SystemSections{Values: map[string]*string{}}
	for i := 0; i < len(pairs); i += 2 {
		name := pairs[i].(string)
		s.Keys = append(s.Keys, name)
		if pairs[i+1] == nil {
			s.Values[name] = nil
		} else {
			s.Values[name] = lo.ToPtr(pairs[i+1].(string))
		}
	}
	return s
}

// replayTranscript mirrors the suite's shared `transcript` fixture.
func replayTranscript(t *testing.T) *types.TranscriptContext {
	t.Helper()
	return NormalizeContext(&types.Context{Messages: []types.Message{
		&types.SystemMessage{
			Role:       types.RoleSystem,
			Content:    types.SystemContent{String: "base"},
			Sections:   sectionsOf("a", "<a>1</a>", "b", "<b>1</b>"),
			ToolsAdded: []types.Tool{replayTool("first")},
			Timestamp:  10,
		},
		&types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hello"}, Timestamp: 11},
		&types.SystemMessage{Role: types.RoleSystem, Content: types.SystemContent{String: "also do this"}, Timestamp: 12},
		&types.AssistantMessage{
			Role:      types.RoleAssistant,
			Content:   []types.Content{types.NewTextContent("ok")},
			Timestamp: 13,
		},
		&types.SystemMessage{
			Role:         types.RoleSystem,
			Content:      types.SystemContent{String: ""},
			Sections:     sectionsOf("a", "<a>2</a>", "b", nil, "c", "<c>1</c>"),
			ToolsRemoved: []types.ToolReference{{Name: "first"}},
			ToolsAdded:   []types.Tool{replayTool("second")},
			Timestamp:    14,
		},
	}})
}

func systemMessagesOf(t *testing.T, messages []types.Message) []*types.SystemMessage {
	t.Helper()
	var out []*types.SystemMessage
	for _, m := range messages {
		if sm, ok := m.(*types.SystemMessage); ok {
			out = append(out, sm)
		} else if sm, ok := m.(types.SystemMessage); ok {
			out = append(out, &sm)
		}
	}
	return out
}

func TestGetCurrentSystemMessageReplaysContentSectionsTools(t *testing.T) {
	transcript := replayTranscript(t)
	current := GetCurrentSystemMessage(transcript.Messages)
	if current == nil {
		t.Fatal("expected replayed system message")
	}
	if current.Content.String != "base\n\nalso do this" {
		t.Fatalf("content = %q", current.Content.String)
	}
	if current.Sections == nil || len(current.Sections.Keys) != 2 ||
		current.Sections.Keys[0] != "a" || current.Sections.Keys[1] != "c" {
		t.Fatalf("sections keys = %+v", current.Sections)
	}
	if got := *current.Sections.Values["a"]; got != "<a>2</a>" {
		t.Fatalf("section a = %q", got)
	}
	if got := *current.Sections.Values["c"]; got != "<c>1</c>" {
		t.Fatalf("section c = %q", got)
	}
	if _, removed := current.Sections.Values["b"]; removed {
		t.Fatalf("section b should be gone")
	}
	if len(current.ToolsAdded) != 1 || current.ToolsAdded[0].Name != "second" {
		t.Fatalf("toolsAdded = %+v", current.ToolsAdded)
	}
	if current.Timestamp != 10 {
		t.Fatalf("timestamp = %d", current.Timestamp)
	}

	if got := GetCurrentSystemPrompt(transcript.Messages); got != "base\n\nalso do this\n\n<a>2</a>\n\n<c>1</c>" {
		t.Fatalf("prompt = %q", got)
	}
}

func TestCollapseSystemMessagesKeepsReplayedHeadOnly(t *testing.T) {
	transcript := replayTranscript(t)
	collapsed := CollapseSystemMessages(transcript)
	roles := make([]types.Role, 0, len(collapsed.Messages))
	for _, m := range collapsed.Messages {
		roles = append(roles, m.MessageRole())
	}
	want := []types.Role{types.RoleSystem, types.RoleUser, types.RoleAssistant}
	if len(roles) != len(want) {
		t.Fatalf("roles = %v", roles)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles = %v", roles)
		}
	}
	// Collapse is idempotent.
	again := CollapseSystemMessages(collapsed)
	a, _ := json.Marshal(again)
	b, _ := json.Marshal(collapsed)
	if string(a) != string(b) {
		t.Fatalf("collapse not idempotent:\n%s\n%s", a, b)
	}
}

func TestReplayWithoutSystemMessagesIsEmpty(t *testing.T) {
	context := NormalizeContext(&types.Context{Messages: []types.Message{
		&types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
	}})
	if GetCurrentSystemMessage(context.Messages) != nil {
		t.Fatal("expected no replayed system message")
	}
	if GetCurrentSystemPrompt(context.Messages) != "" {
		t.Fatal("expected empty prompt")
	}
	collapsed := CollapseSystemMessages(context)
	a, _ := json.Marshal(collapsed.Messages)
	b, _ := json.Marshal(context.Messages)
	if string(a) != string(b) {
		t.Fatalf("collapse changed a transcript without system messages")
	}
}

func TestLatePatchWithoutLeadingReplaysAsPrompt(t *testing.T) {
	context := NormalizeContext(&types.Context{Messages: []types.Message{
		&types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "old session"}, Timestamp: 1},
		&types.SystemMessage{
			Role:       types.RoleSystem,
			Content:    types.SystemContent{String: ""},
			Sections:   sectionsOf("preamble", "You are pi."),
			ToolsAdded: []types.Tool{replayTool("x")},
			Timestamp:  2,
		},
	}})
	if got := GetCurrentSystemPrompt(context.Messages); got != "You are pi." {
		t.Fatalf("prompt = %q", got)
	}
	collapsed := CollapseSystemMessages(context)
	messages := systemMessagesOf(t, collapsed.Messages)
	if len(messages) != 1 || len(messages[0].ToolsAdded) != 1 || messages[0].ToolsAdded[0].Name != "x" {
		b, _ := json.Marshal(collapsed.Messages)
		t.Fatalf("collapsed head = %s", b)
	}
}

func TestSystemMessageTextAndFramedUpdates(t *testing.T) {
	transcript := replayTranscript(t)
	messages := systemMessagesOf(t, transcript.Messages)
	if len(messages) != 3 {
		t.Fatalf("expected 3 system messages, got %d", len(messages))
	}
	leading, update := messages[0], messages[2]
	if got := GetSystemMessageText(leading); got != "base\n\n<a>1</a>\n\n<b>1</b>" {
		t.Fatalf("leading text = %q", got)
	}
	want := "Updated system prompt section \"a\":\n\n<a>2</a>\n\n" +
		"Removed system prompt section \"b\".\n\n" +
		"Updated system prompt section \"c\":\n\n<c>1</c>"
	if got := RenderSystemMessageUpdate(update); got != want {
		t.Fatalf("update = %q\nwant  %q", got, want)
	}
}

func TestNormalizeContextLegacyPromptAndTools(t *testing.T) {
	messages := []types.Message{&types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1}}

	bare := NormalizeContext(&types.Context{Messages: messages})
	if len(bare.Messages) != 1 {
		t.Fatalf("bare messages = %+v", bare.Messages)
	}
	empty := NormalizeContext(&types.Context{SystemPrompt: "", Tools: []types.Tool{}, Messages: messages})
	if len(empty.Messages) != 1 {
		t.Fatalf("empty prompt/tools should not create a message: %+v", empty.Messages)
	}
	withPrompt := NormalizeContext(&types.Context{
		SystemPrompt: "be brief",
		Tools:        []types.Tool{replayTool("a")},
		Messages:     messages,
	})
	if len(withPrompt.Messages) != 2 {
		t.Fatalf("messages = %+v", withPrompt.Messages)
	}
	head, ok := withPrompt.Messages[0].(types.SystemMessage)
	if !ok {
		// The leading message must be the value form: adapter message
		// switches match value types.
		t.Fatalf("leading message = %T", withPrompt.Messages[0])
	}
	if head.Content.String != "be brief" || head.Timestamp != 0 ||
		len(head.ToolsAdded) != 1 || head.ToolsAdded[0].Name != "a" {
		b, _ := json.Marshal(head)
		t.Fatalf("leading head = %s", b)
	}
}

func TestDeclarationsEqualIgnoresExecutableAndUndefinedFields(t *testing.T) {
	// In Go the "executable" and "undefined constrainedSampling" shapes both
	// collapse to the zero value, so the equality holds (pi: declarationsEqual
	// strips execute + undefined fields via the JSON round-trip).
	if !DeclarationsEqual(replayTool("a"), replayTool("a")) {
		t.Fatal("identical declarations should be equal")
	}
	if DeclarationsEqual(replayTool("a"), replayTool("a", "changed")) {
		t.Fatal("different descriptions should not be equal")
	}
	withSampling := replayTool("a")
	withSampling.ConstrainedSampling = false
	if DeclarationsEqual(replayTool("a"), withSampling) {
		t.Fatal("constrainedSampling false should not be equal to unset")
	}
}

func TestGetToolStateChangesTreatsChangedDefinitionsAsRemovePlusAdd(t *testing.T) {
	previous := []types.Tool{replayTool("a"), replayTool("b")}
	current := []types.Tool{replayTool("b", "changed"), replayTool("c")}
	changes := GetToolStateChanges(previous, current)

	added, _ := json.Marshal(changes.ToolsAdded)
	wantAdded, _ := json.Marshal([]types.Tool{replayTool("b", "changed"), replayTool("c")})
	if string(added) != string(wantAdded) {
		t.Fatalf("toolsAdded = %s, want %s", added, wantAdded)
	}
	removed, _ := json.Marshal(changes.ToolsRemoved)
	wantRemoved, _ := json.Marshal([]types.ToolReference{{Name: "a"}, {Name: "b"}})
	if string(removed) != string(wantRemoved) {
		t.Fatalf("toolsRemoved = %s, want %s", removed, wantRemoved)
	}

	same := GetToolStateChanges([]types.Tool{replayTool("a")}, []types.Tool{replayTool("a")})
	if len(same.ToolsAdded) != 0 || len(same.ToolsRemoved) != 0 {
		t.Fatalf("unchanged state should produce no changes: %+v", same)
	}
}

func TestHasNonAdditiveToolChangesAndRedefinitions(t *testing.T) {
	transcript := replayTranscript(t)
	if !HasNonAdditiveToolChanges(transcript.Messages) {
		t.Fatal("removal in transcript should be non-additive")
	}
	if HasToolRedefinitions(transcript.Messages) {
		t.Fatal("transcript should have no redefinitions")
	}

	additive := NormalizeContext(&types.Context{Messages: []types.Message{
		&types.SystemMessage{Role: types.RoleSystem, ToolsAdded: []types.Tool{replayTool("a")}, Timestamp: 1},
		&types.SystemMessage{Role: types.RoleSystem, ToolsAdded: []types.Tool{replayTool("b")}, Timestamp: 2},
	}})
	if HasNonAdditiveToolChanges(additive.Messages) {
		t.Fatal("pure additions should be additive")
	}

	redeclared := NormalizeContext(&types.Context{Messages: []types.Message{
		&types.SystemMessage{Role: types.RoleSystem, ToolsAdded: []types.Tool{replayTool("a")}, Timestamp: 1},
		&types.SystemMessage{Role: types.RoleSystem, ToolsAdded: []types.Tool{replayTool("a", "changed")}, Timestamp: 2},
	}})
	if !HasNonAdditiveToolChanges(redeclared.Messages) {
		t.Fatal("redeclaration should be non-additive")
	}
	if !HasToolRedefinitions(redeclared.Messages) {
		t.Fatal("redeclaration should be detected")
	}
}

func TestGetCurrentToolsPreservesFirstDeclarationOrder(t *testing.T) {
	transcript := replayTranscript(t)
	tools := GetCurrentTools(transcript.Messages)
	if len(tools) != 1 || tools[0].Name != "second" {
		t.Fatalf("tools = %+v", tools)
	}

	// Re-add after removal keeps the original position of the first
	// declaration (TS Map insertion order semantics).
	mixed := []types.Message{
		&types.SystemMessage{Role: types.RoleSystem, ToolsAdded: []types.Tool{replayTool("a"), replayTool("b")}, Timestamp: 1},
		&types.SystemMessage{Role: types.RoleSystem, ToolsRemoved: []types.ToolReference{{Name: "a"}}, Timestamp: 2},
		&types.SystemMessage{Role: types.RoleSystem, ToolsAdded: []types.Tool{replayTool("c")}, Timestamp: 3},
		&types.SystemMessage{Role: types.RoleSystem, ToolsAdded: []types.Tool{replayTool("a")}, Timestamp: 4},
	}
	got := GetCurrentTools(mixed)
	names := make([]string, 0, len(got))
	for _, tool := range got {
		names = append(names, tool.Name)
	}
	if len(names) != 3 || names[0] != "b" || names[1] != "c" || names[2] != "a" {
		t.Fatalf("names = %v, want [b c a]", names)
	}
}
