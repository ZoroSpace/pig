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
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
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

// pi v0.86.0: "a forced prompt is sent as the leading prompt for the run and
// never recorded" — a before_agent_start systemPrompt override collapses the
// run to one leading system message; the sections patch made alongside it is
// recorded instead of the forced text, and the next unforced turn replays
// the head plus both patches.
func TestSystemPromptForcedPromptLeadingAndNeverRecorded(t *testing.T) {
	turn := 0
	mu := &sync.Mutex{}
	var requests []*types.TranscriptContext
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			mu.Lock()
			requests = append(requests, llmContext)
			mu.Unlock()
			stream := newMockAssistantStream()
			msg := testAssistantMessage("ok", withModelRef(model))
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
		ExtensionsResult: []*LoadExtensionsResult{{
			Extensions: []*Extension{{
				Path:     "forced-prompt-stub",
				Handlers: map[string][]ExtensionHandler{},
			}},
			Runtime: CreateExtensionRuntime(),
		}},
	})
	defer session.Dispose()
	session.extensionRunner.extensions[0].Handlers["before_agent_start"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			beforeAgentStart, ok := event.(*BeforeAgentStartEvent)
			if !ok {
				return nil, nil
			}
			mu.Lock()
			turn++
			current := turn
			mu.Unlock()
			if current == 3 {
				beforeAgentStart.SystemPromptOptions.Sections.Keys = append(beforeAgentStart.SystemPromptOptions.Sections.Keys, "plan_mode")
				beforeAgentStart.SystemPromptOptions.Sections.Values["plan_mode"] = lo.ToPtr("Plan only.")
			}
			if current == 2 || current == 3 {
				return &BeforeAgentStartEventResult{SystemPrompt: lo.ToPtr("Exact prompt.")}, nil
			}
			return nil, nil
		},
	}

	for _, text := range []string{"one", "two", "three", "four"} {
		if err := session.Prompt(context.Background(), text, nil); err != nil {
			t.Fatal(err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(requests))
	}
	systems := make([][]*types.SystemMessage, 0, len(requests))
	for _, request := range requests {
		var systemMessages []*types.SystemMessage
		for _, message := range request.Messages {
			if system, ok := message.(types.SystemMessage); ok {
				systemMessages = append(systemMessages, &system)
			} else if system, ok := message.(*types.SystemMessage); ok {
				systemMessages = append(systemMessages, system)
			}
		}
		systems = append(systems, systemMessages)
	}
	wantCounts := []int{1, 1, 1, 3}
	for i, want := range wantCounts {
		if len(systems[i]) != want {
			t.Fatalf("request %d system messages = %d, want %d", i+1, len(systems[i]), want)
		}
	}

	// The forced turns collapse to one leading message carrying the forced
	// text with the head's tool declarations and timestamp.
	forced := systems[1][0]
	if forced.Content.String != "Exact prompt." {
		t.Fatalf("forced content = %q", forced.Content.String)
	}
	if len(forced.ToolsAdded) != len(systems[0][0].ToolsAdded) || forced.Timestamp != systems[0][0].Timestamp {
		t.Fatalf("forced message lost the head's declaration/timestamp: %+v", forced)
	}
	// The second forced turn reuses the same forced message unchanged.
	if !reflect.DeepEqual(systems[1][0], systems[2][0]) {
		t.Fatalf("second forced turn differs:\n%+v\n%+v", systems[1][0], systems[2][0])
	}

	// The unforced fourth turn replays the recorded head and both patches.
	head := systems[3][0]
	planText := systems[3][1]
	planNull := systems[3][2]
	if head.Content.String != "" || head.Sections == nil {
		t.Fatalf("head = %+v", head)
	}
	if planText.Sections == nil || planText.Sections.Values["plan_mode"] == nil ||
		*planText.Sections.Values["plan_mode"] != "<plan_mode>\nPlan only.\n</plan_mode>" {
		t.Fatalf("plan_mode patch missing: %+v", planText.Sections)
	}
	if planNull.Sections == nil {
		t.Fatal("plan_mode removal missing: nil sections")
	}
	if v, ok := planNull.Sections.Values["plan_mode"]; !ok || v != nil {
		t.Fatalf("plan_mode removal missing: %v %v", ok, v)
	}

	// The transcript records only the structured sections, never the forced
	// text.
	recorded := [][2]any{}
	for _, message := range session.Messages() {
		if system, ok := message.(types.SystemMessage); ok {
			recorded = append(recorded, [2]any{system.Content.String, system.Sections})
		}
	}
	if len(recorded) != 3 {
		t.Fatalf("recorded system messages = %d, want 3", len(recorded))
	}
	if recorded[0][0] != "" || recorded[1][0] != "" || recorded[2][0] != "" {
		t.Fatalf("forced text leaked into the transcript: %v", recorded)
	}
	firstSections := recorded[0][1].(*types.SystemSections)
	secondSections := recorded[1][1].(*types.SystemSections)
	thirdSections := recorded[2][1].(*types.SystemSections)
	if !reflect.DeepEqual(secondSections.Values["plan_mode"], lo.ToPtr("<plan_mode>\nPlan only.\n</plan_mode>")) {
		t.Fatalf("recorded plan_mode = %+v", secondSections.Values["plan_mode"])
	}
	if v, ok := thirdSections.Values["plan_mode"]; !ok || v != nil {
		t.Fatalf("recorded plan_mode removal = %+v", thirdSections.Values["plan_mode"])
	}
	if _, ok := firstSections.Values["plan_mode"]; ok {
		t.Fatalf("initial head unexpectedly declares plan_mode")
	}
	if aiutils.GetCurrentSystemPrompt(session.Messages()) == "" {
		t.Fatal("current system prompt should render from the recorded state")
	}
}

// pi v0.86.0: "keeps tool declarations stable across a session JSON
// round-trip" — simulating a resume (messages re-decoded from their persisted
// JSON) must not re-declare the system prompt; the declaration also carries
// no runtime fields (constrainedSampling/execute never serialize).
func TestSystemPromptToolDeclarationsStableAcrossJSONRoundTrip(t *testing.T) {
	// pi registers the tool WITHOUT constrainedSampling — the declaration
	// must omit the key entirely.
	plainTool := &coretools.ToolDefinition{
		Name:        "plain",
		Label:       "Plain",
		Description: "Plain tool",
		Parameters:  rawJSON(`{"type":"object","properties":{}}`),
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx coretools.ToolExecuteContext) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{Content: []types.Content{}, Details: map[string]any{}}, nil
		},
	}
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			msg := testAssistantMessage("ok", withModelRef(model))
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
		ExtensionsResult: []*LoadExtensionsResult{{
			Extensions: []*Extension{{
				Path: "plain-tool-stub",
				Tools: map[string]RegisteredTool{
					"plain": {Definition: plainTool},
				},
				Handlers: map[string][]ExtensionHandler{},
			}},
			Runtime: CreateExtensionRuntime(),
		}},
	})
	defer session.Dispose()
	session.SetActiveToolsByName([]string{"plain"})

	if err := session.Prompt(context.Background(), "one", nil); err != nil {
		t.Fatal(err)
	}
	head, ok := session.Messages()[0].(types.SystemMessage)
	if !ok {
		t.Fatalf("head = %T, want system message", session.Messages()[0])
	}
	if len(head.ToolsAdded) != 1 {
		t.Fatalf("toolsAdded = %+v", head.ToolsAdded)
	}
	// pi: Object.hasOwn(declaration, "constrainedSampling") === false — the
	// declaration must serialize without runtime-only fields.
	declaration, err := json.Marshal(head.ToolsAdded[0])
	if err != nil {
		t.Fatal(err)
	}
	var declared map[string]any
	if err := json.Unmarshal(declaration, &declared); err != nil {
		t.Fatal(err)
	}
	if _, ok := declared["constrainedSampling"]; ok {
		t.Fatalf("declaration leaks constrainedSampling: %s", declaration)
	}

	// Simulate a resume: the persisted JSON must replay to the same
	// declarations.
	// The persisted JSON replays through the message decoder (pi:
	// JSON.parse(JSON.stringify(...)) — pig's union decode lives in
	// types.DecodeMessageList).
	encoded, err := json.Marshal(session.Messages())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := types.DecodeMessageList(encoded)
	if err != nil {
		t.Fatal(err)
	}
	replayed := []agent.AgentMessage{}
	for _, message := range decoded {
		replayed = append(replayed, message)
	}
	session.Agent.SetMessages(replayed)

	if err := session.Prompt(context.Background(), "two", nil); err != nil {
		t.Fatal(err)
	}
	systemCount := 0
	for _, message := range session.Messages() {
		if message.MessageRole() == types.RoleSystem {
			systemCount++
		}
	}
	if systemCount != 1 {
		t.Fatalf("system messages after resume = %d, want 1", systemCount)
	}
}
