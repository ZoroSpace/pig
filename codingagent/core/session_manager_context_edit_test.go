// pi: test/session-context-edit.test.ts — append-only context edits and
// retain-none compactions in the session projection.
//
// Batch boundary (S8-2/S8-3): the projection and wire-format cases live
// here; the compaction-dependent cases of the pi file (the
// estimateProjectedContextTokens usage cases and the prepareCompaction
// cut-point cases) landed with S8-3 in compaction_projection_test.go.
package core

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
)

// ceAssistant builds the assistant fixture used by the context-edit tests
// (pi: assistant(text); usage.totalTokens 11).
func ceAssistant(text string) types.AssistantMessage {
	msg := assistantMsg(text)
	msg.Usage = types.Usage{
		Input:       10,
		Output:      1,
		CacheRead:   0,
		CacheWrite:  0,
		TotalTokens: 11,
		Cost:        types.Cost{},
	}
	msg.Timestamp = time.Now().UnixMilli()
	return msg
}

// ceToolResult builds the tool-result fixture (pi: the inline
// ToolResultMessage with toolCallId "call-1").
func ceToolResult(text string, isError bool) types.ToolResultMessage {
	return types.ToolResultMessage{
		Role:       types.RoleToolResult,
		ToolCallID: "call-1",
		ToolName:   "read",
		Content:    []types.Content{types.NewTextContent(text)},
		Details:    map[string]any{"path": "large.txt"},
		IsError:    isError,
		Timestamp:  time.Now().UnixMilli(),
	}
}

// ceTextParts extracts the text-block text of a content list (pi: text()'s
// array branch).
func ceTextParts(parts []types.Content) string {
	var texts []string
	for _, part := range parts {
		if tc, ok := part.(types.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	return strings.Join(texts, "")
}

// ceText extracts the projected text of a message (pi: text(message)).
func ceText(message agent.AgentMessage) string {
	switch m := message.(type) {
	case types.UserMessage:
		if m.Content.Parts == nil {
			return m.Content.String
		}
		return ceTextParts(m.Content.Parts)
	case types.AssistantMessage:
		return ceTextParts(m.Content)
	case types.ToolResultMessage:
		return ceTextParts(m.Content)
	case harness.CustomMessage:
		if s, ok := m.Content.(string); ok {
			return s
		}
		if parts, ok := m.Content.([]types.Content); ok {
			return ceTextParts(parts)
		}
	}
	return ""
}

// ceTextReplacement builds a single-text-block replacement (pi:
// { content: [{ type: "text", text }] }).
func ceTextReplacement(text string) *ContextEditReplacement {
	return &ContextEditReplacement{Content: []types.Content{types.NewTextContent(text)}}
}

// ceProjectedRoles maps projected messages to roles (pi:
// messages.map(m => m.role)).
func ceProjectedRoles(messages []agent.AgentMessage) []string {
	roles := make([]string, 0, len(messages))
	for _, message := range messages {
		roles = append(roles, string(message.MessageRole()))
	}
	return roles
}

// ceProjectedTexts maps projected messages to their visible text: the
// summary for compaction summaries, the content text otherwise (pi:
// messages.map(m => "summary" in m ? m.summary : text(m))).
func ceProjectedTexts(messages []agent.AgentMessage) []string {
	texts := make([]string, 0, len(messages))
	for _, message := range messages {
		if summary, ok := message.(harness.CompactionSummaryMessage); ok {
			texts = append(texts, summary.Summary)
			continue
		}
		texts = append(texts, ceText(message))
	}
	return texts
}

func TestSessionContextEditOmitsTargetOnlyFromModelProjection(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(userMsg("request")); err != nil {
		t.Fatal(err)
	}
	assistantID, err := session.AppendMessage(ceAssistant("partial"))
	if err != nil {
		t.Fatal(err)
	}
	result := ceToolResult("raw output", true)
	resultID, err := session.AppendMessage(result)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(assistantID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(resultID, nil); err != nil {
		t.Fatal(err)
	}

	var messageCount int
	for _, entry := range session.GetBranch("") {
		if _, ok := entry.(*SessionMessageEntry); ok {
			messageCount++
		}
	}
	if messageCount != 3 {
		t.Fatalf("branch message entries = %d, want 3", messageCount)
	}
	projection := session.BuildSessionProjection()
	if got := ceProjectedRoles(projection.Messages); !reflect.DeepEqual(got, []string{"user"}) {
		t.Fatalf("projected roles = %v, want [user]", got)
	}
	// The raw target entry is unchanged.
	target := session.GetEntry(resultID).(*SessionMessageEntry)
	if !reflect.DeepEqual(target.Message, agent.AgentMessage(result)) {
		t.Fatal("raw toolResult entry was modified")
	}
}

func TestSessionContextEditReplacesOnlyContentLatestWins(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	targetID, err := session.AppendMessage(ceAssistant("original"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(targetID, ceTextReplacement("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(targetID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(targetID, ceTextReplacement("restored")); err != nil {
		t.Fatal(err)
	}

	projected := session.BuildSessionProjection().Messages
	if len(projected) != 1 {
		t.Fatalf("projected = %d messages, want 1", len(projected))
	}
	if projected[0].MessageRole() != types.RoleAssistant {
		t.Fatalf("projected role = %q, want assistant", projected[0].MessageRole())
	}
	if text := ceText(projected[0]); text != "restored" {
		t.Fatalf("projected text = %q, want %q", text, "restored")
	}
	if assistant, ok := projected[0].(types.AssistantMessage); !ok || assistant.Usage.TotalTokens != 11 {
		t.Fatalf("projected message = %#v, want assistant with totalTokens 11", projected[0])
	}
	raw := session.GetEntry(targetID).(*SessionMessageEntry).Message.(types.AssistantMessage)
	if text := ceText(raw); text != "original" {
		t.Fatalf("raw text = %q, want %q", text, "original")
	}
}

func TestSessionContextEditNormalizesStringReplacementsForArrayOnlyRoles(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	assistantID, err := session.AppendMessage(ceAssistant("original"))
	if err != nil {
		t.Fatal(err)
	}
	resultID, err := session.AppendMessage(ceToolResult("original result", false))
	if err != nil {
		t.Fatal(err)
	}
	assistantEditID, err := session.AppendContextEdit(assistantID, &ContextEditReplacement{Content: "assistant replacement"})
	if err != nil {
		t.Fatal(err)
	}
	resultEditID, err := session.AppendContextEdit(resultID, &ContextEditReplacement{Content: "result replacement"})
	if err != nil {
		t.Fatal(err)
	}

	wantAssistant := []types.Content{types.NewTextContent("assistant replacement")}
	wantResult := []types.Content{types.NewTextContent("result replacement")}
	assistantEdit := session.GetEntry(assistantEditID).(*ContextEditEntry)
	if !reflect.DeepEqual(assistantEdit.Replacement.Content, ContextEditableContent(wantAssistant)) {
		t.Fatalf("assistant replacement = %#v, want %s", assistantEdit.Replacement.Content, wantAssistant)
	}
	resultEdit := session.GetEntry(resultEditID).(*ContextEditEntry)
	if !reflect.DeepEqual(resultEdit.Replacement.Content, ContextEditableContent(wantResult)) {
		t.Fatalf("result replacement = %#v, want %s", resultEdit.Replacement.Content, wantResult)
	}
	projected := session.BuildSessionProjection().Messages
	if len(projected) != 2 {
		t.Fatalf("projected = %d messages, want 2", len(projected))
	}
	if pa, ok := projected[0].(types.AssistantMessage); !ok || !reflect.DeepEqual(pa.Content, wantAssistant) {
		t.Fatalf("projected[0] = %#v, want assistant with %s", projected[0], wantAssistant)
	}
	if pr, ok := projected[1].(types.ToolResultMessage); !ok || !reflect.DeepEqual(pr.Content, wantResult) {
		t.Fatalf("projected[1] = %#v, want toolResult with %s", projected[1], wantResult)
	}
}

func TestSessionContextEditNormalizesImportedStringReplacements(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	assistantID, err := session.AppendMessage(ceAssistant("original"))
	if err != nil {
		t.Fatal(err)
	}
	editID, err := session.AppendContextEdit(assistantID, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a hand-edited/imported edit whose string replacement skipped
	// appendContextEdit's normalization (pi: edit.replacement = { content }).
	edit := session.GetEntry(editID).(*ContextEditEntry)
	edit.Replacement = &ContextEditReplacement{Content: "imported replacement"}

	projected := session.BuildSessionProjection().Messages
	if len(projected) != 1 {
		t.Fatalf("projected = %d messages, want 1", len(projected))
	}
	want := []types.Content{types.NewTextContent("imported replacement")}
	if pa, ok := projected[0].(types.AssistantMessage); !ok || !reflect.DeepEqual(pa.Content, want) {
		t.Fatalf("projected[0] = %#v, want assistant with %s", projected[0], want)
	}
}

func TestSessionContextEditKeepsEditsBranchRelative(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	targetID, err := session.AppendMessage(userMsg("original"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(targetID, &ContextEditReplacement{Content: "edited"}); err != nil {
		t.Fatal(err)
	}
	projection := session.BuildSessionProjection()
	if len(projection.Messages) != 1 || ceText(projection.Messages[0]) != "edited" {
		t.Fatalf("projected = %#v, want text %q", projection.Messages, "edited")
	}

	if err := session.Branch(targetID); err != nil {
		t.Fatal(err)
	}
	projection = session.BuildSessionProjection()
	if len(projection.Messages) != 1 || ceText(projection.Messages[0]) != "original" {
		t.Fatalf("projected after branch = %#v, want text %q", projection.Messages, "original")
	}
}

func TestSessionContextEditRetainNoneCompaction(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(userMsg("discarded")); err != nil {
		t.Fatal(err)
	}
	compactionID, err := session.AppendCompaction("exact handoff", nil, 100, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(userMsg("after")); err != nil {
		t.Fatal(err)
	}

	compaction := session.GetEntry(compactionID).(*CompactionEntry)
	if compaction.Base().Type != "compaction" || compaction.FirstKeptEntryID != compactionID {
		t.Fatalf("compaction entry = %#v, want firstKeptEntryId %q", compaction, compactionID)
	}
	projection := session.BuildSessionProjection()
	if got := ceProjectedRoles(projection.Messages); !reflect.DeepEqual(got, []string{"compactionSummary", "user"}) {
		t.Fatalf("projected roles = %v, want [compactionSummary user]", got)
	}
	if got := ceProjectedTexts(projection.Messages); !reflect.DeepEqual(got, []string{"exact handoff", "after"}) {
		t.Fatalf("projected texts = %v, want [exact handoff after]", got)
	}
}

func TestSessionContextEditAppliesPostCompactionEditsToRetainedEntries(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(userMsg("summarized")); err != nil {
		t.Fatal(err)
	}
	retainedID, err := session.AppendMessage(userMsg("original retained"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCompaction("summary", lo.ToPtr(retainedID), 100, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(retainedID, &ContextEditReplacement{Content: "edited retained"}); err != nil {
		t.Fatal(err)
	}

	projection := session.BuildSessionProjection()
	if got := ceProjectedTexts(projection.Messages); !reflect.DeepEqual(got, []string{"summary", "edited retained"}) {
		t.Fatalf("projected texts = %v, want [summary edited retained]", got)
	}
}

func TestSessionContextEditSupportsRepeatedRetainNoneCompactions(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(userMsg("discarded")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCompaction("first handoff", nil, 100, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(userMsg("also discarded")); err != nil {
		t.Fatal(err)
	}
	secondID, err := session.AppendCompaction("second handoff", nil, 50, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	second := session.GetEntry(secondID).(*CompactionEntry)
	if second.FirstKeptEntryID != secondID {
		t.Fatalf("second compaction firstKeptEntryId = %q, want %q", second.FirstKeptEntryID, secondID)
	}
	projection := session.BuildSessionProjection()
	if got := ceProjectedTexts(projection.Messages); !reflect.DeepEqual(got, []string{"second handoff"}) {
		t.Fatalf("projected texts = %v, want [second handoff]", got)
	}
}

// TestSessionContextEditWireFormatRoundTrip locks the context_edit JSONL
// wire shape: replacement null serializes as an explicit null, non-null
// content round-trips, and a reopened session projects the edit (pi:
// docs/session-format.md ContextEditEntry; the pi↔pig compatibility gate
// reruns at the S8 wrap-up batch).
func TestSessionContextEditWireFormatRoundTrip(t *testing.T) {
	dir := newTempDir(t)
	session, err := CreateSessionManager(dir, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := session.AppendMessage(userMsg("hello"))
	if err != nil {
		t.Fatal(err)
	}
	assistantID, err := session.AppendMessage(ceAssistant("answer"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(userID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(assistantID, ceTextReplacement("revised")); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(session.GetSessionFile())
	if err != nil {
		t.Fatal(err)
	}
	var editLines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, `"type":"context_edit"`) {
			editLines = append(editLines, line)
		}
	}
	if len(editLines) != 2 {
		t.Fatalf("context_edit lines = %d, want 2", len(editLines))
	}
	if !strings.Contains(editLines[0], `"replacement":null`) {
		t.Fatalf("null replacement line = %s, want explicit replacement null", editLines[0])
	}
	if !strings.Contains(editLines[1], `"replacement":{"content":[{"type":"text","text":"revised"}]}`) {
		t.Fatalf("content replacement line = %s", editLines[1])
	}

	reopened, err := OpenSession(session.GetSessionFile(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	projection := reopened.BuildSessionProjection()
	if got := ceProjectedTexts(projection.Messages); !reflect.DeepEqual(got, []string{"revised"}) {
		t.Fatalf("reopened projected texts = %v, want [revised]", got)
	}
}
