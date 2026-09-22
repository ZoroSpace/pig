// pi: test/session-context-edit.test.ts — the compaction-dependent cases of
// the session context-edit suite (S8-3 batch: EstimateProjectedContextTokens
// + the projection-based PrepareCompaction + the recovery-omission cut-point
// rule). The projection/wire-format cases live in
// session_manager_context_edit_test.go.
package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
)

// cpSettings builds { ...DEFAULT_COMPACTION_SETTINGS, keepRecentTokens }.
func cpSettings(keepRecentTokens int64) CompactionSettingsValues {
	settings := DefaultCompactionSettings
	settings.KeepRecentTokens = keepRecentTokens
	return settings
}

// cpSummaries maps projected messages to their compaction summaries (pi:
// messages.flatMap(m => "summary" in m ? [m.summary] : [])).
func cpSummaries(messages []agent.AgentMessage) []string {
	var summaries []string
	for _, message := range messages {
		if summary, ok := message.(harness.CompactionSummaryMessage); ok {
			summaries = append(summaries, summary.Summary)
		}
	}
	return summaries
}

// cpJSON renders a message list for substring assertions (pi:
// JSON.stringify(...)).
func cpJSON(messages []agent.AgentMessage) string {
	encoded, err := json.Marshal(messages)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// TestSessionContextEditUsesOnlyNewestSummaryForRepeatedCompaction: a
// repeated compaction whose retained range still contains the older
// compaction must seed the next summary from the newest one only (pi: "uses
// only the newest summary when a repeated compaction retains entries before
// the older compaction").
func TestSessionContextEditUsesOnlyNewestSummaryForRepeatedCompaction(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(userMsg("summarized first")); err != nil {
		t.Fatal(err)
	}
	retainedID, err := session.AppendMessage(userMsg("retained"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCompaction("first summary", lo.ToPtr(retainedID), 100, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(ceAssistant("after first compaction")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCompaction("second summary", lo.ToPtr(retainedID), 80, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(userMsg(strings.Repeat("new tail ", 100))); err != nil {
		t.Fatal(err)
	}

	projection := session.BuildSessionProjection()
	if got := cpSummaries(projection.Messages); !reflect.DeepEqual(got, []string{"second summary"}) {
		t.Fatalf("projected summaries = %v, want [second summary]", got)
	}
	preparation := PrepareCompaction(session.GetBranch(""), cpSettings(1))
	if preparation == nil {
		t.Fatal("preparation = nil")
	}
	if preparation.PreviousSummary != "second summary" {
		t.Fatalf("previousSummary = %q, want %q", preparation.PreviousSummary, "second summary")
	}
}

// TestSessionContextEditDoesNotTrustPreEditUsage: usage captured before a
// later context edit does not describe the projected context and falls back
// to per-message estimates (pi: "does not trust pre-edit assistant usage for
// projected context estimates").
func TestSessionContextEditDoesNotTrustPreEditUsage(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	largeUserID, err := session.AppendMessage(userMsg(strings.Repeat("discarded input ", 2000)))
	if err != nil {
		t.Fatal(err)
	}
	response := ceAssistant("small answer")
	response.Usage.Input = 10_000
	response.Usage.TotalTokens = 10_001
	assistantID, err := session.AppendMessage(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(largeUserID, nil); err != nil {
		t.Fatal(err)
	}

	editedEstimate := EstimateProjectedContextTokens(session.BuildSessionProjection(), session.GetBranch(""))
	if editedEstimate.UsageTokens != 0 {
		t.Fatalf("usageTokens = %d, want 0", editedEstimate.UsageTokens)
	}
	if editedEstimate.Tokens >= 100 {
		t.Fatalf("tokens = %d, want < 100", editedEstimate.Tokens)
	}

	if _, err := session.AppendContextEdit(assistantID, nil); err != nil {
		t.Fatal(err)
	}
	if got := EstimateProjectedContextTokens(session.BuildSessionProjection(), session.GetBranch("")).Tokens; got != 0 {
		t.Fatalf("tokens = %d, want 0 after omitting everything", got)
	}
}

// TestSessionContextEditUsesUsageAfterLatestEdit: usage captured after the
// latest context edit stays trustworthy (pi: "uses assistant usage captured
// after the latest context edit").
func TestSessionContextEditUsesUsageAfterLatestEdit(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := session.AppendMessage(userMsg("original"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(userID, ceTextReplacement("edited")); err != nil {
		t.Fatal(err)
	}
	response := ceAssistant("answer")
	response.Usage.Input = 4_000
	response.Usage.Output = 100
	response.Usage.TotalTokens = 4_100
	if _, err := session.AppendMessage(response); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(userMsg("next")); err != nil {
		t.Fatal(err)
	}

	estimate := EstimateProjectedContextTokens(session.BuildSessionProjection(), session.GetBranch(""))
	if estimate.UsageTokens != 4_100 {
		t.Fatalf("usageTokens = %d, want 4100", estimate.UsageTokens)
	}
	if estimate.TrailingTokens != 1 {
		t.Fatalf("trailingTokens = %d, want 1", estimate.TrailingTokens)
	}
	if estimate.Tokens != 4_101 {
		t.Fatalf("tokens = %d, want 4101", estimate.Tokens)
	}
}

// TestSessionContextEditDoesNotReusePostEditUsageAfterCompaction: a
// compaction after the usage capture invalidates it the same way an edit
// does (pi: "does not reuse post-edit assistant usage after a later
// compaction").
func TestSessionContextEditDoesNotReusePostEditUsageAfterCompaction(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := session.AppendMessage(userMsg("small input"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(userID, ceTextReplacement("edited input")); err != nil {
		t.Fatal(err)
	}
	response := ceAssistant("answer")
	response.Usage.Input = 50_000
	response.Usage.Output = 1
	response.Usage.TotalTokens = 50_001
	if _, err := session.AppendMessage(response); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCompaction("small summary", lo.ToPtr(userID), 50_001, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	estimate := EstimateProjectedContextTokens(session.BuildSessionProjection(), session.GetBranch(""))
	if estimate.UsageTokens != 0 {
		t.Fatalf("usageTokens = %d, want 0", estimate.UsageTokens)
	}
	if estimate.Tokens >= 100 {
		t.Fatalf("tokens = %d, want < 100", estimate.Tokens)
	}
}

// TestSessionContextEditIncludesSystemAndToolContext: the estimate counts
// the replayed system message (content + effective tool declarations) (pi:
// "includes effective system and tool context in edited estimates").
func TestSessionContextEditIncludesSystemAndToolContext(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(types.SystemMessage{
		Role:    types.RoleSystem,
		Content: types.SystemContent{String: strings.Repeat("system prompt ", 3000)},
		ToolsAdded: []types.Tool{{
			Name:        "example",
			Description: strings.Repeat("tool declaration ", 100),
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		}},
		Timestamp: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	userID, err := session.AppendMessage(userMsg("ask"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(ceAssistant("done")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(userID, ceTextReplacement("ask")); err != nil {
		t.Fatal(err)
	}

	if got := EstimateProjectedContextTokens(session.BuildSessionProjection(), session.GetBranch("")).Tokens; got <= 10_000 {
		t.Fatalf("tokens = %d, want > 10000", got)
	}
}

// TestSessionContextEditDoesNotAdvancePastBoundaryReplacement: a replacement
// edit of the candidate input is an external replacement that blocks the
// recovery-suffix advance (pi: "does not advance past a boundary replacement
// of the candidate input").
func TestSessionContextEditDoesNotAdvancePastBoundaryReplacement(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(userMsg("old request")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(ceAssistant("old answer")); err != nil {
		t.Fatal(err)
	}
	replacedUserID, err := session.AppendMessage(userMsg("original input"))
	if err != nil {
		t.Fatal(err)
	}
	assistantID, err := session.AppendMessage(ceAssistant("answered original input"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(replacedUserID, ceTextReplacement(strings.Repeat("NEW-INSTRUCTION ", 100))); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(assistantID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCustomEntry("bookkeeping", map[string]any{"source": "test"}); err != nil {
		t.Fatal(err)
	}

	preparation := PrepareCompaction(session.GetBranch(""), cpSettings(1))
	if preparation == nil {
		t.Fatal("preparation = nil")
	}
	if preparation.FirstKeptEntryID != replacedUserID {
		t.Fatalf("firstKeptEntryId = %q, want the replaced user entry %q", preparation.FirstKeptEntryID, replacedUserID)
	}
	if strings.Contains(cpJSON(preparation.MessagesToSummarize), "NEW-INSTRUCTION") {
		t.Fatalf("messagesToSummarize contains the replacement: %s", cpJSON(preparation.MessagesToSummarize))
	}
	if strings.Contains(cpJSON(preparation.TurnPrefixMessages), "NEW-INSTRUCTION") {
		t.Fatalf("turnPrefixMessages contains the replacement: %s", cpJSON(preparation.TurnPrefixMessages))
	}
}

// TestSessionContextEditMetadataDoesNotMoveCutPastUnsentInput: intrinsically
// context-invisible metadata must not move the cut past unsent input (pi:
// "does not let metadata move the cut past unsent boundary input").
func TestSessionContextEditMetadataDoesNotMoveCutPastUnsentInput(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(userMsg("old request")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(ceAssistant("old answer")); err != nil {
		t.Fatal(err)
	}
	instructionID, err := session.AppendCustomMessageEntry("next-work", strings.Repeat("UNSENT-INSTRUCTION ", 100), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCustomEntry("bookkeeping", map[string]any{"source": "test"}); err != nil {
		t.Fatal(err)
	}

	preparation := PrepareCompaction(session.GetBranch(""), cpSettings(1))
	if preparation == nil {
		t.Fatal("preparation = nil")
	}
	if preparation.FirstKeptEntryID != instructionID {
		t.Fatalf("firstKeptEntryId = %q, want the unsent custom message %q", preparation.FirstKeptEntryID, instructionID)
	}
	if strings.Contains(cpJSON(preparation.MessagesToSummarize), "UNSENT-INSTRUCTION") {
		t.Fatalf("messagesToSummarize contains the unsent input: %s", cpJSON(preparation.MessagesToSummarize))
	}
	if strings.Contains(cpJSON(preparation.TurnPrefixMessages), "UNSENT-INSTRUCTION") {
		t.Fatalf("turnPrefixMessages contains the unsent input: %s", cpJSON(preparation.TurnPrefixMessages))
	}
}

// TestSessionContextEditOmittedCustomMessageNotRecoveryAttempt: an omitted
// custom message is not an assistant recovery attempt, so no preparation
// exists (pi: "does not treat an omitted custom message as a recovery
// attempt").
func TestSessionContextEditOmittedCustomMessageNotRecoveryAttempt(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(userMsg(strings.Repeat("unanswered input ", 100))); err != nil {
		t.Fatal(err)
	}
	customID, err := session.AppendCustomMessageEntry("temporary", "temporary context", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(customID, nil); err != nil {
		t.Fatal(err)
	}

	if preparation := PrepareCompaction(session.GetBranch(""), cpSettings(1)); preparation != nil {
		t.Fatalf("preparation = %+v, want nil", preparation)
	}
}

// TestSessionContextEditAdvancesPastOmittedAssistantRecoverySuffix: a closed
// suffix with an omitted assistant attempt and metadata advances the kept
// boundary into the omitted attempt (pi: "advances past input for an omitted
// assistant recovery suffix with metadata").
func TestSessionContextEditAdvancesPastOmittedAssistantRecoverySuffix(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := session.AppendMessage(userMsg(strings.Repeat("recovery input ", 100)))
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := session.AppendMessage(ceAssistant("failed attempt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(attemptID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCustomEntry("bookkeeping", map[string]any{"source": "test"}); err != nil {
		t.Fatal(err)
	}

	preparation := PrepareCompaction(session.GetBranch(""), cpSettings(1))
	if preparation == nil {
		t.Fatal("preparation = nil")
	}
	if preparation.FirstKeptEntryID != attemptID {
		t.Fatalf("firstKeptEntryId = %q, want the omitted attempt %q", preparation.FirstKeptEntryID, attemptID)
	}
	if len(preparation.TurnPrefixMessages) != 1 {
		t.Fatalf("turnPrefixMessages = %d entries, want 1", len(preparation.TurnPrefixMessages))
	}
	prefixUser, ok := preparation.TurnPrefixMessages[0].(types.UserMessage)
	if !ok || !strings.Contains(ceText(prefixUser), "recovery input") {
		t.Fatalf("turnPrefixMessages[0] = %#v, want the recovery input user message", preparation.TurnPrefixMessages[0])
	}
	if strings.Contains(cpJSON(preparation.MessagesToSummarize), "recovery input") {
		t.Fatalf("messagesToSummarize contains the recovery input: %s", cpJSON(preparation.MessagesToSummarize))
	}
	if userID == attemptID {
		t.Fatal("fixture broken: user and attempt ids must differ")
	}
}

// TestSessionContextEditPreparesCompactionFromEditedContent: preparation is
// computed from edited model content — omitted entries appear in no
// summarized range (pi: "prepares compaction from edited model content").
func TestSessionContextEditPreparesCompactionFromEditedContent(t *testing.T) {
	session, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	omittedID, err := session.AppendMessage(userMsg(strings.Repeat("OMIT-ME ", 100)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(ceAssistant(strings.Repeat("old answer ", 100))); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendContextEdit(omittedID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(userMsg("keep")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(ceAssistant("suffix")); err != nil {
		t.Fatal(err)
	}

	preparation := PrepareCompaction(session.GetBranch(""), cpSettings(1))
	if preparation == nil {
		t.Fatal("preparation = nil")
	}
	if strings.Contains(cpJSON(preparation.MessagesToSummarize), "OMIT-ME") {
		t.Fatalf("messagesToSummarize contains the omitted input: %s", cpJSON(preparation.MessagesToSummarize))
	}
	if strings.Contains(cpJSON(preparation.TurnPrefixMessages), "OMIT-ME") {
		t.Fatalf("turnPrefixMessages contains the omitted input: %s", cpJSON(preparation.TurnPrefixMessages))
	}
}
