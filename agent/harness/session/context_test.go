// pi: test/harness/session/context.test.ts — buildSessionContext behaviors.
//
// Deviations:
//   - entry() helper: pig entries are built as typed structs with Seq and
//     Timestamp set to seq (pi's Omit-based helper has no counterpart).
package session_test

import (
	"github.com/samber/lo"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness/session"
	"github.com/ZoroSpace/pig/ai/types"
)

func contextUserMessage(text string) agent.AgentMessage {
	return types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(text)}},
		Timestamp: 1,
	}
}

func contextAssistantMessage(text string) types.AssistantMessage {
	return types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent(text)},
		API:        types.ApiAnthropicMessages,
		Provider:   "anthropic",
		Model:      "claude-sonnet-4-5",
		Usage:      types.Usage{Cost: types.Cost{}},
		StopReason: types.StopReasonStop,
		Timestamp:  1,
	}
}

func TestStartsAtTheLatestCompactionAndMaterializesItsRetainedTail(t *testing.T) {
	entries := []session.Entry{
		&session.MessageEntry{
			EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "old", Seq: 1, Timestamp: 1},
			Message:   contextUserMessage("old"),
		},
		&session.CompactionEntry{
			EntryBase:    session.EntryBase{Type: session.EntryTypeCompaction, ID: "compact", Seq: 2, Timestamp: 2, ParentID: lo.ToPtr("old")},
			Summary:      "summary",
			RetainedTail: []agent.AgentMessage{contextUserMessage("retained"), contextAssistantMessage("answer")},
			TokensBefore: 100,
		},
		&session.ModelChangeEntry{
			EntryBase: session.EntryBase{Type: session.EntryTypeModelChange, ID: "model", Seq: 3, Timestamp: 3, ParentID: lo.ToPtr("compact")},
			Provider:  "openai",
			ModelID:   "gpt-5",
		},
		&session.ThinkingLevelEntry{
			EntryBase:     session.EntryBase{Type: session.EntryTypeThinkingLevelChange, ID: "thinking", Seq: 4, Timestamp: 4, ParentID: lo.ToPtr("model")},
			ThinkingLevel: "high",
		},
		&session.MessageEntry{
			EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "tail", Seq: 5, Timestamp: 5, ParentID: lo.ToPtr("thinking")},
			Message:   contextUserMessage("tail"),
		},
	}

	context := session.BuildSessionContext(entries, nil)
	roles := make([]types.Role, 0, len(context.Messages))
	for _, message := range context.Messages {
		roles = append(roles, message.MessageRole())
	}
	assertRolesEqual(t, roles, []types.Role{
		"compactionSummary", "user", "assistant", "user",
	})
	if context.Model == nil || context.Model.Provider != "openai" || context.Model.ModelID != "gpt-5" {
		t.Fatalf("model = %+v", context.Model)
	}
	if context.ThinkingLevel != "high" {
		t.Fatalf("thinkingLevel = %s", context.ThinkingLevel)
	}
}

func TestAppliesCallerTransformsAfterTheCompactionBoundary(t *testing.T) {
	entries := []session.Entry{
		&session.MessageEntry{
			EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "old", Seq: 1, Timestamp: 1},
			Message:   contextUserMessage("old"),
		},
		&session.CompactionEntry{
			EntryBase:    session.EntryBase{Type: session.EntryTypeCompaction, ID: "compact", Seq: 2, Timestamp: 2, ParentID: lo.ToPtr("old")},
			Summary:      "summary",
			RetainedTail: []agent.AgentMessage{},
			TokensBefore: 100,
		},
		&session.BranchSummaryEntry{
			EntryBase: session.EntryBase{Type: session.EntryTypeBranchSummary, ID: "branch", Seq: 3, Timestamp: 3, ParentID: lo.ToPtr("compact")},
			FromID:    "abandoned",
			Summary:   "branch summary",
		},
		&session.MessageEntry{
			EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "tail", Seq: 4, Timestamp: 4, ParentID: lo.ToPtr("branch")},
			Message:   contextUserMessage("tail"),
		},
	}

	context := session.BuildSessionContext(entries, &session.SessionContextBuildOptions{
		EntryTransforms: []session.ContextEntryTransform{
			func(contextEntries []session.Entry) []session.Entry {
				filtered := make([]session.Entry, 0, len(contextEntries))
				for _, candidate := range contextEntries {
					if candidate.EntryType() != session.EntryTypeCompaction {
						filtered = append(filtered, candidate)
					}
				}
				return filtered
			},
		},
	})
	roles := make([]types.Role, 0, len(context.Messages))
	for _, message := range context.Messages {
		roles = append(roles, message.MessageRole())
	}
	assertRolesEqual(t, roles, []types.Role{"branchSummary", "user"})
}

func TestProjectsCustomEntriesAndOmitsDeferredAssistantHandles(t *testing.T) {
	deferred := contextAssistantMessage("")
	deferred.Content = []types.Content{}
	deferred.StopReason = types.StopReasonDeferred
	deferred.Deferred = &types.DeferredHandle{
		Provider: "openai",
		ModelID:  "gpt-5",
		API:      types.Api("openai-responses"), // literal: the constant arrives with its adapter (§3.4 scope rule)
		ID:       "response-1",
	}
	entries := []session.Entry{
		&session.MessageEntry{
			EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "user", Seq: 1, Timestamp: 1},
			Message:   contextUserMessage("hello"),
		},
		&session.MessageEntry{
			EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "deferred", Seq: 2, Timestamp: 2, ParentID: lo.ToPtr("user")},
			Message:   deferred,
		},
		&session.CustomEntry{
			EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "custom", Seq: 3, Timestamp: 3, ParentID: lo.ToPtr("deferred")},
			CustomType: "note",
			Data:       "project me",
		},
	}

	context := session.BuildSessionContext(entries, &session.SessionContextBuildOptions{
		EntryProjectors: map[string]session.CustomEntryContextMessageProjector{
			"note": func(custom *session.CustomEntry, index int, all []session.Entry) []agent.AgentMessage {
				return []agent.AgentMessage{contextUserMessage("note: " + custom.Data.(string))}
			},
		},
	})
	roles := make([]types.Role, 0, len(context.Messages))
	for _, message := range context.Messages {
		roles = append(roles, message.MessageRole())
	}
	assertRolesEqual(t, roles, []types.Role{"user", "user"})
	second, ok := context.Messages[1].(types.UserMessage)
	if !ok {
		t.Fatalf("message 1 = %T", context.Messages[1])
	}
	text, ok := second.Content.Parts[0].(types.TextContent)
	if !ok || text.Text != "note: project me" {
		t.Fatalf("projected content = %+v", second.Content.Parts)
	}
}

// assertRolesEqual compares role lists with helpful output.
func assertRolesEqual(t *testing.T, actual, expected []types.Role) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("roles = %v, want %v", actual, expected)
	}
	for i := range actual {
		if actual[i] != expected[i] {
			t.Fatalf("roles = %v, want %v", actual, expected)
		}
	}
}
