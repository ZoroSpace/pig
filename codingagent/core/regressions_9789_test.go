// pi: test/suite/regressions/9789-context-handler-system-messages.test.ts (pi
// v0.87.0, aef5fc429) — context handlers only see the conversation; Pi
// restores the prompt and tool state after each, and context_with_system
// handlers own the full transcript.
package core

import (
	"context"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
)

// agentMessage is a local alias keeping the handler signatures readable.
type agentMessage = agent.AgentMessage

// agentHarnessRoleCompactionSummary is the compaction-summary role (pi:
// "compactionSummary"; harness.RoleCompactionSummary).
const agentHarnessRoleCompactionSummary = types.Role("compactionSummary")

// compactViaHook supplies compaction through the extension hook, as in the
// reported sessions (pi: compactViaHook).
func compactViaHookExtension() *Extension {
	return &Extension{
		Path: "compact-hook",
		Handlers: map[string][]ExtensionHandler{"session_before_compact": {func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			beforeCompact, ok := event.(*SessionBeforeCompactEvent)
			if !ok {
				return nil, errUnexpectedEvent(event)
			}
			return &SessionBeforeCompactResult{Compaction: &CompactionResult{
				Summary:          "extension summary",
				FirstKeptEntryID: beforeCompact.Preparation.FirstKeptEntryID,
				TokensBefore:     beforeCompact.Preparation.TokensBefore,
				Details:          map[string]any{"source": "test"},
			}}, nil
		}}},
	}
}

// captureFauxRequest queues a capture-and-respond step and returns the getter
// for the captured provider request (pi: captureRequest).
func captureFauxRequest(h *fauxHarness, text string) func() *types.TranscriptContext {
	var request *types.TranscriptContext
	h.setResponses(func(llmContext *types.TranscriptContext) *types.AssistantMessage {
		request = llmContext
		return fauxAssistantMessage(text)(llmContext)
	})
	return func() *types.TranscriptContext {
		if request == nil {
			panic("expected a provider request")
		}
		return request
	}
}

func fauxToolNames(context *types.TranscriptContext) []string {
	names := make([]string, 0)
	for _, tool := range aiutils.GetCurrentTools(context.Messages) {
		names = append(names, tool.Name)
	}
	return names
}

// fauxCompactSession compacts through the hook and asserts the canonical
// post-compaction shape (pi: compactSession).
func fauxCompactSession(t *testing.T, h *fauxHarness) {
	t.Helper()
	h.settingsManager.ApplyOverrides(Settings{"compaction": map[string]any{"keepRecentTokens": float64(1)}})
	h.setResponses(fauxAssistantMessage("one"), fauxAssistantMessage("two"))
	if err := h.session.Prompt(context.Background(), "first", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.session.Prompt(context.Background(), "second", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.session.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	messages := h.session.Messages()
	if len(messages) < 2 {
		t.Fatalf("messages = %d", len(messages))
	}
	if messages[0].MessageRole() != types.RoleSystem || messages[1].MessageRole() != agentHarnessRoleCompactionSummary {
		t.Fatalf("first two roles = %s, %s; want system, compactionSummary", messages[0].MessageRole(), messages[1].MessageRole())
	}
}

func Test9789KeepsPromptAndToolsWhenHandlerSlicesFromSummary(t *testing.T) {
	var seen [][]agentMessage
	contextHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		contextEvent, ok := event.(*ContextEvent)
		if !ok {
			return nil, errUnexpectedEvent(event)
		}
		seen = append(seen, contextEvent.Messages)
		summary := -1
		for i, message := range contextEvent.Messages {
			if message.MessageRole() == agentHarnessRoleCompactionSummary {
				summary = i
				break
			}
		}
		if summary < 0 {
			return &ContextEventResult{Messages: contextEvent.Messages}, nil
		}
		return &ContextEventResult{Messages: contextEvent.Messages[summary:]}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{
			compactViaHookExtension(),
			{Path: "context-ext", Handlers: map[string][]ExtensionHandler{"context": {contextHandler}}},
		},
	})
	fauxCompactSession(t, harness)
	getRequest := captureFauxRequest(harness, "after compaction")

	if err := harness.session.Prompt(context.Background(), "third", nil); err != nil {
		t.Fatal(err)
	}

	if len(seen) == 0 {
		t.Fatal("context handler never ran")
	}
	for _, message := range seen[len(seen)-1] {
		if message.MessageRole() == types.RoleSystem {
			t.Fatal("the context handler must not see system messages")
		}
	}
	request := getRequest()
	if len(request.Messages) == 0 || request.Messages[0].MessageRole() != types.RoleSystem {
		t.Fatal("the request must start with the replayed system message")
	}
	if !equalStringSlices(fauxToolNames(request), harness.session.GetActiveToolNames()) {
		t.Fatalf("tools = %v, want %v", fauxToolNames(request), harness.session.GetActiveToolNames())
	}
	if got := aiutils.GetCurrentSystemPrompt(request.Messages); got != harness.session.SystemPrompt() {
		t.Fatalf("system prompt = %q, want %q", got, harness.session.SystemPrompt())
	}
	systemCount := 0
	for _, message := range request.Messages {
		if message.MessageRole() == types.RoleSystem {
			systemCount++
		}
	}
	if systemCount != 1 {
		t.Fatalf("system messages = %d, want 1", systemCount)
	}
}

func Test9789KeepsMidConversationSystemMessagesWhenUnchanged(t *testing.T) {
	turn := 0
	beforeAgentStart := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		beforeStart, ok := event.(*BeforeAgentStartEvent)
		if !ok {
			return nil, errUnexpectedEvent(event)
		}
		turn++
		if turn == 2 {
			planMode := "<plan_mode>\nPlan only.\n</plan_mode>"
			beforeStart.SystemPromptOptions.Sections = &types.SystemSections{
				Keys:   []string{"plan_mode"},
				Values: map[string]*string{"plan_mode": &planMode},
			}
		}
		return nil, nil
	}
	contextHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		contextEvent, ok := event.(*ContextEvent)
		if !ok {
			return nil, errUnexpectedEvent(event)
		}
		return &ContextEventResult{Messages: contextEvent.Messages}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "context-ext", Handlers: map[string][]ExtensionHandler{
			"before_agent_start": {beforeAgentStart},
			"context":            {contextHandler},
		}}},
	})
	harness.setResponses(fauxAssistantMessage("one"))
	if err := harness.session.Prompt(context.Background(), "first", nil); err != nil {
		t.Fatal(err)
	}
	getRequest := captureFauxRequest(harness, "two")

	if err := harness.session.Prompt(context.Background(), "second", nil); err != nil {
		t.Fatal(err)
	}

	var systemMessages []types.SystemMessage
	for _, message := range getRequest().Messages {
		if system, ok := message.(types.SystemMessage); ok {
			systemMessages = append(systemMessages, system)
		}
	}
	if len(systemMessages) != 2 {
		t.Fatalf("system messages = %d, want 2", len(systemMessages))
	}
	sections := systemMessages[1].Sections
	if sections == nil || len(sections.Keys) != 1 || sections.Keys[0] != "plan_mode" ||
		sections.Values["plan_mode"] == nil || *sections.Values["plan_mode"] != "<plan_mode>\nPlan only.\n</plan_mode>" {
		t.Fatalf("sections = %+v", sections)
	}
}

func Test9789AppliesInPlaceEditsWithoutReturnValue(t *testing.T) {
	contextHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		contextEvent, ok := event.(*ContextEvent)
		if !ok {
			return nil, errUnexpectedEvent(event)
		}
		injected := types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{Parts: []types.Content{types.NewTextContent("injected")}},
			Timestamp: 0,
		}
		contextEvent.Messages = append([]agent.AgentMessage{injected}, contextEvent.Messages...)
		return nil, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "context-ext", Handlers: map[string][]ExtensionHandler{"context": {contextHandler}}}},
	})
	getRequest := captureFauxRequest(harness, "done")

	if err := harness.session.Prompt(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}

	request := getRequest()
	roles := make([]string, 0, len(request.Messages))
	for _, message := range request.Messages {
		roles = append(roles, string(message.MessageRole()))
	}
	if strings.Join(roles, ",") != "system,user,user" {
		t.Fatalf("roles = %v", roles)
	}
	if !equalStringSlices(fauxToolNames(request), harness.session.GetActiveToolNames()) {
		t.Fatalf("tools = %v, want %v", fauxToolNames(request), harness.session.GetActiveToolNames())
	}
}

func Test9789KeepsHandlerAddedSystemMessagesAfterReplayedHead(t *testing.T) {
	contextHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		contextEvent, ok := event.(*ContextEvent)
		if !ok {
			return nil, errUnexpectedEvent(event)
		}
		ephemeral := types.SystemMessage{
			Role:      types.RoleSystem,
			Content:   types.SystemContent{String: "ephemeral reminder"},
			Timestamp: 0,
		}
		returned := append([]agent.AgentMessage{ephemeral}, contextEvent.Messages...)
		return &ContextEventResult{Messages: returned}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "context-ext", Handlers: map[string][]ExtensionHandler{"context": {contextHandler}}}},
	})
	getRequest := captureFauxRequest(harness, "done")

	if err := harness.session.Prompt(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}

	request := getRequest()
	roles := make([]string, 0, len(request.Messages))
	for _, message := range request.Messages {
		roles = append(roles, string(message.MessageRole()))
	}
	if strings.Join(roles, ",") != "system,system,user" {
		t.Fatalf("roles = %v", roles)
	}
	if !equalStringSlices(fauxToolNames(request), harness.session.GetActiveToolNames()) {
		t.Fatalf("tools = %v, want %v", fauxToolNames(request), harness.session.GetActiveToolNames())
	}
	prompt := aiutils.GetCurrentSystemPrompt(request.Messages)
	if !strings.Contains(prompt, harness.session.SystemPrompt()) {
		t.Fatal("the replayed prompt must survive")
	}
	if !strings.Contains(prompt, "ephemeral reminder") {
		t.Fatal("the handler-added system message must survive")
	}
}

func Test9789ContextWithSystemRunsAfterContextAndSendsVerbatim(t *testing.T) {
	var seen [][]agentMessage
	contextWithSystemHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		withSystem, ok := event.(*ContextWithSystemEvent)
		if !ok {
			return nil, errUnexpectedEvent(event)
		}
		seen = append(seen, withSystem.Messages)
		returned := make([]agent.AgentMessage, len(withSystem.Messages))
		for i, message := range withSystem.Messages {
			if system, isSystem := message.(types.SystemMessage); ok && isSystem && len(system.ToolsAdded) > 0 {
				filtered := make([]types.Tool, 0, len(system.ToolsAdded))
				for _, tool := range system.ToolsAdded {
					if tool.Name != "bash" {
						filtered = append(filtered, tool)
					}
				}
				system.ToolsAdded = filtered
				returned[i] = system
				continue
			}
			returned[i] = message
		}
		return &ContextEventResult{Messages: returned}, nil
	}
	contextHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		contextEvent, ok := event.(*ContextEvent)
		if !ok {
			return nil, errUnexpectedEvent(event)
		}
		summary := -1
		for i, message := range contextEvent.Messages {
			if message.MessageRole() == agentHarnessRoleCompactionSummary {
				summary = i
				break
			}
		}
		if summary < 0 {
			return &ContextEventResult{Messages: contextEvent.Messages}, nil
		}
		return &ContextEventResult{Messages: contextEvent.Messages[summary:]}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{
			compactViaHookExtension(),
			{Path: "context-ext", Handlers: map[string][]ExtensionHandler{
				// Registered before context, but runs after: the phases are ordered.
				"context_with_system": {contextWithSystemHandler},
				"context":             {contextHandler},
			}},
		},
	})
	fauxCompactSession(t, harness)
	getRequest := captureFauxRequest(harness, "after compaction")

	if err := harness.session.Prompt(context.Background(), "third", nil); err != nil {
		t.Fatal(err)
	}

	if len(seen) == 0 {
		t.Fatal("context_with_system handler never ran")
	}
	input := seen[len(seen)-1]
	if len(input) < 2 || input[0].MessageRole() != types.RoleSystem || input[1].MessageRole() != agentHarnessRoleCompactionSummary {
		t.Fatalf("context_with_system input roles = %v", input)
	}
	activeTools := harness.session.GetActiveToolNames()
	hasBash := false
	for _, name := range activeTools {
		if name == "bash" {
			hasBash = true
		}
	}
	if !hasBash {
		t.Fatal("the active tool set must still contain bash")
	}
	wantTools := make([]string, 0, len(activeTools))
	for _, name := range activeTools {
		if name != "bash" {
			wantTools = append(wantTools, name)
		}
	}
	if !equalStringSlices(fauxToolNames(getRequest()), wantTools) {
		t.Fatalf("tools = %v, want %v", fauxToolNames(getRequest()), wantTools)
	}
}

func Test9789ReportsDroppedLeadingSystemButHonorsOutput(t *testing.T) {
	contextWithSystemHandler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		withSystem, ok := event.(*ContextWithSystemEvent)
		if !ok {
			return nil, errUnexpectedEvent(event)
		}
		returned := make([]agent.AgentMessage, 0, len(withSystem.Messages))
		for _, message := range withSystem.Messages {
			if message.MessageRole() != types.RoleSystem {
				returned = append(returned, message)
			}
		}
		return &ContextEventResult{Messages: returned}, nil
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		extensions: []*Extension{{Path: "context-ext", Handlers: map[string][]ExtensionHandler{
			"context_with_system": {contextWithSystemHandler},
		}}},
	})
	var errors []ExtensionError
	harness.session.extensionRunner.OnError(func(err ExtensionError) { errors = append(errors, err) })
	getRequest := captureFauxRequest(harness, "done")

	if err := harness.session.Prompt(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}

	request := getRequest()
	roles := make([]string, 0, len(request.Messages))
	for _, message := range request.Messages {
		roles = append(roles, string(message.MessageRole()))
	}
	if strings.Join(roles, ",") != "user" {
		t.Fatalf("roles = %v", roles)
	}
	if len(errors) != 1 || errors[0].Event != "context_with_system" ||
		!strings.HasPrefix(errors[0].Error, "Handler removed the leading system message") {
		t.Fatalf("errors = %+v", errors)
	}
}

// equalStringSlices compares two string slices order-sensitively.
func equalStringSlices(a, b []string) bool {
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
