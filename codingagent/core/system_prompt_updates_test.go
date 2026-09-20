// system_prompt_updates_test.go — pi: test/system-prompt-updates.test.ts
// (new in v0.86.0). The pure-function sections tests port 1:1; the e2e
// suites run against pig's createAgentTestSession harness (mock streamFn +
// in-memory session manager) instead of pi's createHarness/extension
// factories — the forced-prompt-through-extension-factory and
// setActiveTools-sections cases need the Phase 7 extension surface and stay
// with S6-3b (declared).
package core

import (
	"context"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
	"github.com/samber/lo"
)

func TestDiffSystemPromptSectionsPatch(t *testing.T) {
	previous := BuildSystemPromptSections(BuildSystemPromptOptions{
		Cwd:      "/tmp",
		Sections: systemSections(map[string]string{"plan_mode": "Plan only."}),
	})
	current := BuildSystemPromptSections(BuildSystemPromptOptions{
		Cwd:      "/tmp",
		Sections: systemSections(map[string]string{"plan_mode": "Implementation allowed."}),
	})
	patch := DiffSystemPromptSections(previous, current)
	if patch == nil || len(patch.Keys) != 1 || patch.Keys[0] != "plan_mode" {
		t.Fatalf("patch = %+v", patch)
	}
	if got := *patch.Values["plan_mode"]; got != "<plan_mode>\nImplementation allowed.\n</plan_mode>" {
		t.Fatalf("plan_mode patch = %q", got)
	}
	if DiffSystemPromptSections(previous, previous) != nil {
		t.Fatal("equal sections should diff to nil")
	}
	removed := DiffSystemPromptSections(previous, BuildSystemPromptSections(BuildSystemPromptOptions{Cwd: "/tmp"}))
	if removed == nil || len(removed.Keys) != 1 || removed.Keys[0] != "plan_mode" || removed.Values["plan_mode"] != nil {
		t.Fatalf("removal patch = %+v", removed)
	}
}

func TestDiffSystemPromptSectionsPreambleUntagged(t *testing.T) {
	previous := BuildSystemPromptSections(BuildSystemPromptOptions{CustomPrompt: "You are A.", Cwd: "/tmp"})
	current := BuildSystemPromptSections(BuildSystemPromptOptions{CustomPrompt: "You are B.", Cwd: "/tmp"})
	if got := *previous.Values["preamble"]; got != "You are A." {
		t.Fatalf("preamble = %q", got)
	}
	patch := DiffSystemPromptSections(previous, current)
	if patch == nil || len(patch.Keys) != 1 || patch.Keys[0] != "preamble" || *patch.Values["preamble"] != "You are B." {
		t.Fatalf("patch = %+v", patch)
	}

	state := BuildSystemPromptState(BuildSystemPromptOptions{ForceSystemPrompt: lo.ToPtr("Exact prompt."), Cwd: "/tmp"})
	if state.content != "Exact prompt." || state.sections != nil {
		t.Fatalf("forced state = %+v", state)
	}
	unforced := BuildSystemPromptState(BuildSystemPromptOptions{Cwd: "/tmp"})
	if unforced.content != "" || unforced.sections == nil {
		t.Fatalf("unforced state = %+v", unforced)
	}
	if !sectionsEqual(unforced.sections, BuildSystemPromptSections(BuildSystemPromptOptions{Cwd: "/tmp"})) {
		t.Fatal("unforced sections mismatch BuildSystemPromptSections output")
	}

	defer func() {
		err := recover()
		if err == nil {
			t.Fatal("expected invalid section name to throw")
		}
		if errMsg, ok := err.(string); !ok || !strings.Contains(errMsg, "Invalid system prompt section name") {
			t.Fatalf("error = %v", err)
		}
	}()
	BuildSystemPromptSections(BuildSystemPromptOptions{Cwd: "/tmp", Sections: systemSections(map[string]string{"preamble": "x"})})
}

func TestSystemPromptDeclaredOnceAndReusedAcrossResume(t *testing.T) {
	responses := []string{"first", "second"}
	turn := 0
	session, sessionManager, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			text := responses[len(responses)-1]
			if turn < len(responses) {
				text = responses[turn]
			}
			turn++
			msg := testAssistantMessage(text)
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
	})
	defer session.Dispose()

	if err := session.Prompt(context.Background(), "one", nil); err != nil {
		t.Fatal(err)
	}
	if err := session.Prompt(context.Background(), "two", nil); err != nil {
		t.Fatal(err)
	}

	systemEntries := 0
	for _, entry := range sessionManager.GetEntries() {
		messageEntry, ok := entry.(*SessionMessageEntry)
		if !ok || messageEntry.Message == nil {
			continue
		}
		if messageEntry.Message.MessageRole() == types.RoleSystem {
			systemEntries++
		}
	}
	if systemEntries != 1 {
		t.Fatalf("system entries = %d, want 1", systemEntries)
	}

	roles := []types.Role{}
	for _, message := range session.Messages() {
		roles = append(roles, message.MessageRole())
	}
	wantRoles := []types.Role{types.RoleSystem, types.RoleUser, types.RoleAssistant, types.RoleUser, types.RoleAssistant}
	if len(roles) != len(wantRoles) {
		t.Fatalf("roles = %v", roles)
	}
	for i := range wantRoles {
		if roles[i] != wantRoles[i] {
			t.Fatalf("roles = %v, want %v", roles, wantRoles)
		}
	}

	head := session.Messages()[0]
	systemMessage, ok := head.(types.SystemMessage)
	if !ok {
		t.Fatalf("head = %T, want system message", head)
	}
	if systemMessage.Content.String != "" {
		t.Fatalf("head content = %q, want empty", systemMessage.Content.String)
	}
	wantKeys := []string{"preamble", "tools", "rules", "docs", "cwd"}
	if len(systemMessage.Sections.Keys) != len(wantKeys) {
		t.Fatalf("section keys = %v, want %v", systemMessage.Sections.Keys, wantKeys)
	}
	for i, key := range wantKeys {
		if systemMessage.Sections.Keys[i] != key {
			t.Fatalf("section keys = %v, want %v", systemMessage.Sections.Keys, wantKeys)
		}
	}
	if len(systemMessage.ToolsAdded) == 0 {
		t.Fatal("expected toolsAdded declarations")
	}
	if got := aiutils.GetSystemMessageText(&systemMessage); got != session.SystemPrompt() {
		t.Fatalf("getSystemMessageText(head) = %q, want systemPrompt %q", got, session.SystemPrompt())
	}
}

// systemSections builds a SystemSections from a plain map (pig: the
// order-preserving shape; single-key maps keep declaration unambiguous).
func systemSections(values map[string]string) *types.SystemSections {
	sections := &types.SystemSections{Keys: []string{}, Values: map[string]*string{}}
	for name, text := range values {
		sections.Keys = append(sections.Keys, name)
		sections.Values[name] = lo.ToPtr(text)
	}
	return sections
}

// sectionsEqual compares two normalized section collections by keys and
// values.
func sectionsEqual(a *types.SystemSections, b *types.SystemSections) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.Keys) != len(b.Keys) {
		return false
	}
	for i, key := range a.Keys {
		if b.Keys[i] != key {
			return false
		}
		av, aok := a.Values[key]
		bv, bok := b.Values[key]
		if aok != bok {
			return false
		}
		if av == nil || bv == nil {
			if av != bv {
				return false
			}
			continue
		}
		if *av != *bv {
			return false
		}
	}
	return true
}
