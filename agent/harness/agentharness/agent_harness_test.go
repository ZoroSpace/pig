// Port of pi's packages/agent/test/harness/agent-harness-scaffold.test.ts.
package agentharness

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	harness "github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/session"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

func createSession(t *testing.T, id string) session.Session {
	t.Helper()
	repo := session.NewMemorySessionRepo(session.MemorySessionRepoOptions{})
	sess, err := repo.Create(session.SessionCreateOptions{ID: lo.ToPtr(id)}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func catalogModel(t *testing.T, providerID ai.ProviderID, id string) *types.Model {
	t.Helper()
	entry, ok := ai.MODELS[providerID][id]
	if !ok {
		t.Fatalf("model %s/%s missing from catalog", providerID, id)
	}
	return &entry
}

func createHarness(t *testing.T, sess session.Session) *AgentHarness {
	t.Helper()
	harness, _, err := CreateAgentHarness(&AgentHarnessOptions{
		Session: sess,
		Models:  ai.NewModels(nil),
		Model:   catalogModel(t, "anthropic", "claude-sonnet-4-5"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return harness
}

func asNotImplemented(t *testing.T, err error, operation string) {
	t.Helper()
	var notImpl *HarnessNotImplemented
	if !errors.As(err, &notImpl) {
		t.Fatalf("err = %v, want HarnessNotImplemented", err)
	}
	if notImpl.Operation != operation {
		t.Fatalf("operation = %q, want %q", notImpl.Operation, operation)
	}
}

func TestOpensOnlyRecordFreeSessionsBeforeRestoreIsImplemented(t *testing.T) {
	sess := createSession(t, "session")
	h, suspended, err := CreateAgentHarness(&AgentHarnessOptions{
		Session: sess,
		Models:  ai.NewModels(nil),
		Model:   catalogModel(t, "anthropic", "claude-sonnet-4-5"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(suspended) != 0 {
		t.Fatalf("suspended = %v", suspended)
	}
	if h.Name() != "main" {
		t.Fatalf("name = %q", h.Name())
	}
	if h.Session() != sess {
		t.Fatal("harness session must be the provided session")
	}
	leaf, err := h.GetLeafID()
	if err != nil || leaf != nil {
		t.Fatalf("leaf = %v, %v", leaf, err)
	}
	sessionLeaf, err := h.Session().Branch("main", context.Background())
	if err != nil || sessionLeaf != nil {
		t.Fatalf("session leaf = %v, %v", sessionLeaf, err)
	}

	h.Close()

	// pi v0.84.3 appended an operation_started record; the v0.85.1 Session
	// has no record log, so the restore gate keys on an existing entry.
	recorded := createSession(t, "recorded")
	branch, err := recorded.CreateBranch("main", nil, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := branch.AppendMessage(&types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{&types.TextContent{Kind: types.KindText, Text: "hi"}}},
		Timestamp: 1,
	}, context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, err = CreateAgentHarness(&AgentHarnessOptions{
		Session: recorded,
		Models:  ai.NewModels(nil),
		Model:   catalogModel(t, "anthropic", "claude-sonnet-4-5"),
	})
	asNotImplemented(t, err, "create.restore")
}

func TestKeepsScaffoldSafeConfigurationAsDefensiveCopies(t *testing.T) {
	h := createHarness(t, createSession(t, "session"))

	model2 := catalogModel(t, "anthropic", "claude-sonnet-4-5")
	h.SetModel(model2)
	if h.GetModel() != model2 {
		t.Fatal("model identity lost")
	}

	h.SetThinkingLevel("high")
	if h.GetThinkingLevel() != "high" {
		t.Fatal("thinking level not kept")
	}

	activeTools := []string{"one"}
	h.SetActiveTools(activeTools)
	activeTools = append(activeTools, "mutated")
	if got := h.GetActiveTools(); !reflect.DeepEqual(got, []string{"one"}) {
		t.Fatalf("active tools = %v", got)
	}
	readActiveTools := h.GetActiveTools()
	readActiveTools = append(readActiveTools, "mutated")
	if got := h.GetActiveTools(); !reflect.DeepEqual(got, []string{"one"}) {
		t.Fatalf("active tools = %v", got)
	}

	tool := HarnessTool{AgentTool: agent.AgentTool{Tool: types.Tool{Name: "tool"}, Label: "Tool"}}
	tools := []HarnessTool{tool}
	h.SetTools(tools, nil)
	tools = append(tools, HarnessTool{AgentTool: agent.AgentTool{Tool: types.Tool{Name: "mutated"}, Label: "Mutated"}})
	if got := h.GetTools(); len(got) != 1 || got[0].Name != "tool" {
		t.Fatalf("tools = %v", got)
	}
	readTools := h.GetTools()
	readTools = append(readTools, HarnessTool{AgentTool: agent.AgentTool{Tool: types.Tool{Name: "mutated"}, Label: "Mutated"}})
	if got := h.GetTools(); len(got) != 1 || got[0].Name != "tool" {
		t.Fatalf("tools = %v", got)
	}

	resources := Resources{
		Skills:          []harness.Skill{{Name: "skill", Description: "desc", Content: "body", FilePath: "/tmp/SKILL.md"}},
		PromptTemplates: []harness.PromptTemplate{{Name: "template", Content: "body"}},
	}
	h.SetResources(resources)
	resources.Skills = append(resources.Skills, harness.Skill{Name: "mutated"})
	if got := h.GetResources().Skills; len(got) != 1 || got[0].Name != "skill" {
		t.Fatalf("skills = %v", got)
	}
	readResources := h.GetResources()
	readResources.Skills = append(readResources.Skills, harness.Skill{Name: "mutated"})
	if got := h.GetResources().Skills; len(got) != 1 || got[0].Name != "skill" {
		t.Fatalf("skills = %v", got)
	}

	streamOptions := StreamOptions{StreamOptions: types.StreamOptions{MaxTokens: lo.ToPtr(int64(10))}}
	h.SetStreamOptions(streamOptions)
	streamOptions.MaxTokens = lo.ToPtr(int64(20))
	if got := h.GetStreamOptions(); *got.MaxTokens != 10 {
		t.Fatalf("stream options = %+v", got)
	}
	readStreamOptions := h.GetStreamOptions()
	readStreamOptions.MaxTokens = lo.ToPtr(int64(30))
	if got := h.GetStreamOptions(); *got.MaxTokens != 10 {
		t.Fatalf("stream options = %+v", got)
	}

	retryPolicy := ai.RetryPolicy{Enabled: true, MaxRetries: 2, BaseDelayMs: 10}
	h.SetRetryPolicy(retryPolicy)
	retryPolicy.MaxRetries = 99
	if got := h.GetRetryPolicy(); got != (ai.RetryPolicy{Enabled: true, MaxRetries: 2, BaseDelayMs: 10}) {
		t.Fatalf("retry policy = %+v", got)
	}

	compactionSettings := harness.CompactionSettings{Enabled: false, ReserveTokens: 1, KeepRecentTokens: 2}
	h.SetCompactionSettings(compactionSettings)
	compactionSettings.ReserveTokens = 99
	if got := h.GetCompactionSettings(); got != (harness.CompactionSettings{Enabled: false, ReserveTokens: 1, KeepRecentTokens: 2}) {
		t.Fatalf("compaction settings = %+v", got)
	}

	h.SetSteeringMode(agent.QueueModeAll)
	if h.GetSteeringMode() != agent.QueueModeAll {
		t.Fatal("steering mode not kept")
	}
	h.SetFollowUpMode(agent.QueueModeAll)
	if h.GetFollowUpMode() != agent.QueueModeAll {
		t.Fatal("follow-up mode not kept")
	}
}

func TestRejectsEveryUnfinishedPublicOperationExplicitly(t *testing.T) {
	h := createHarness(t, createSession(t, "session"))
	callbackCalled := false
	userMessage := &types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{&types.TextContent{Kind: types.KindText, Text: "hello"}}},
		Timestamp: 1,
	}
	usage := &types.Usage{
		Input:  1,
		Output: 2,
		Cost:   types.Cost{},
	}

	type unfinishedCase struct {
		operation string
		invoke    func() error
	}
	unfinished := []unfinishedCase{
		{"prompt", func() error { _, err := h.Prompt(userMessage); return err }},
		{"skill", func() error { _, err := h.Skill("skill", ""); return err }},
		{"promptFromTemplate", func() error { _, err := h.PromptFromTemplate("template", nil); return err }},
		{"compact", func() error { _, err := h.Compact(""); return err }},
		{"navigateTree", func() error { _, err := h.NavigateTree(nil, nil); return err }},
		{"resume", func() error { _, err := h.Resume(); return err }},
		{"abort", func() error { _, err := h.Abort(); return err }},
		{"steer", func() error { _, err := h.Steer(userMessage); return err }},
		{"followUp", func() error { _, err := h.FollowUp(userMessage); return err }},
		{"nextRun", func() error { _, err := h.NextRun(userMessage); return err }},
		{"cancelQueued", func() error { _, err := h.CancelQueued("queued"); return err }},
		{"recordUsage", func() error { return h.RecordUsage(usage, nil) }},
		{"waitForIdle", func() error { return h.WaitForIdle() }},
		{"runWhenIdle", func() error {
			return h.RunWhenIdle(func() { callbackCalled = true })
		}},
		{"peekAction", func() error { _, err := h.PeekAction(); return err }},
		{"executeAction", func() error { _, err := h.ExecuteAction(); return err }},
		{"runToCompletion", func() error { return h.RunToCompletion() }},
		{"watch", func() error { _, err := h.Watch(); return err }},
		{"lane", func() error { _, _, err := h.Lane("main"); return err }},
		{"createLane", func() error { _, err := h.CreateLane("thread", nil); return err }},
		{"lanes", func() error { _, err := h.Lanes(); return err }},
		{"watchSession", func() error { _, err := h.WatchSession(); return err }},
	}
	for _, tc := range unfinished {
		asNotImplemented(t, tc.invoke(), tc.operation)
	}
	if callbackCalled {
		t.Fatal("runWhenIdle callback must not fire")
	}
	if _, err := h.Hooks().On("before_run", func(event any) any { return nil }, nil); err == nil {
		t.Fatal("hooks.on must throw")
	}
	if _, err := h.Events().OnEvent("event", func(event any) {}); err == nil {
		t.Fatal("events.on must throw")
	}
}

func TestReportsHarnessClosedAfterClose(t *testing.T) {
	h := createHarness(t, createSession(t, "session"))
	h.Close()

	userMessage := &types.UserMessage{Role: types.RoleUser, Timestamp: 1}
	var closedErr *HarnessClosed
	if _, err := h.Prompt(userMessage); !errors.As(err, &closedErr) {
		t.Fatalf("prompt err = %v", err)
	}
	if err := h.WaitForIdle(); !errors.As(err, &closedErr) {
		t.Fatalf("waitForIdle err = %v", err)
	}
	if _, err := h.Hooks().On("before_run", func(event any) any { return nil }, nil); !errors.As(err, &closedErr) {
		t.Fatalf("hooks.on err = %v", err)
	}
	if _, err := h.Events().OnEvent("event", func(event any) {}); !errors.As(err, &closedErr) {
		t.Fatalf("events.on err = %v", err)
	}
}
