// pi: test/extensions-runner.test.ts "boundary chaining" describe (pi
// v0.87.0) — EmitBoundary's shared draft list, preview rebuilds, and error
// containment at the runner level.
package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

var (
	errEntryMissing   = errors.New("Entry missing not found")
	errBoundaryFailed = errors.New("boundary failed")
)

func errUnexpectedEvent(event ExtensionEvent) error {
	return fmt.Errorf("unexpected event %T", event)
}

func newBoundaryTestRunner(t *testing.T, extensions []*Extension) *ExtensionRunner {
	t.Helper()
	runner := NewExtensionRunner(extensions, CreateExtensionRuntime(), "/tmp/project", &SessionManager{}, &ModelRegistry{})
	t.Cleanup(func() { runner.Invalidate("test done") })
	return runner
}

// boundaryPreviewBuilder fakes pi's test buildContext: the preview's
// contextEntries mirror the proposed drafts one-to-one; the optional failing
// predicate lets a test mark specific proposals invalid.
func boundaryPreviewBuilder(failOn func(entries []SessionBoundaryDraft) error) BoundaryContextBuilder {
	return func(entries []SessionBoundaryDraft) (BoundaryContextPreview, error) {
		if failOn != nil {
			if err := failOn(entries); err != nil {
				return BoundaryContextPreview{}, err
			}
		}
		previewEntries := make([]ProjectedSessionEntry, 0, len(entries))
		for i, entry := range entries {
			previewEntries = append(previewEntries, ProjectedSessionEntry{
				SourceEntry: &CustomEntry{
					EntryBase:  EntryBase{Type: "custom", ID: draftPreviewID(i)},
					CustomType: entry.draftType(),
				},
			})
		}
		return BoundaryContextPreview{
			ContextEntries:  previewEntries,
			ContextMessages: []agent.AgentMessage{},
			LLMMessages:     []types.Message{},
			PendingMessages: []agent.AgentMessage{},
			CanContinue:     false,
		}, nil
	}
}

func draftPreviewID(index int) string {
	return "draft-" + string(rune('0'+index))
}

func TestEmitBoundaryChainsSharedDraftsAndPreservesOmittedResultFields(t *testing.T) {
	type observation struct {
		entries      int
		continuation bool
		preview      int
	}
	var observations []observation
	first := &Extension{
		Path: "<inline:first>",
		Handlers: map[string][]ExtensionHandler{"agent_before_settle": {func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			beforeSettle, ok := event.(*AgentBeforeSettleEvent)
			if !ok {
				return nil, errUnexpectedEvent(event)
			}
			observations = append(observations, observation{
				entries:      len(beforeSettle.Entries),
				continuation: beforeSettle.Continue,
				preview:      len(beforeSettle.Context.ContextEntries),
			})
			beforeSettle.Entries = append(beforeSettle.Entries, &CustomEntryDraft{Type: "custom", CustomType: "first", Data: 1})
			return &BoundaryResult{Continue: lo.ToPtr(true)}, nil
		}}},
	}
	second := &Extension{
		Path: "<inline:second>",
		Handlers: map[string][]ExtensionHandler{"agent_before_settle": {func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			beforeSettle, ok := event.(*AgentBeforeSettleEvent)
			if !ok {
				return nil, errUnexpectedEvent(event)
			}
			observations = append(observations, observation{
				entries:      len(beforeSettle.Entries),
				continuation: beforeSettle.Continue,
				preview:      len(beforeSettle.Context.ContextEntries),
			})
			return &BoundaryResult{Entries: []SessionBoundaryDraft{}}, nil
		}}},
	}
	runner := newBoundaryTestRunner(t, []*Extension{first, second})

	result, err := runner.EmitBoundary(context.Background(),
		&AgentBeforeSettleEvent{Type: "agent_before_settle", Outcome: AgentActivityCompleted},
		boundaryPreviewBuilder(nil))
	if err != nil {
		t.Fatal(err)
	}

	if len(observations) != 2 {
		t.Fatalf("observations = %d, want 2", len(observations))
	}
	if observations[0].entries != 0 || observations[0].continuation || observations[0].preview != 0 {
		t.Fatalf("first observation = %+v", observations[0])
	}
	if observations[1].entries != 1 || !observations[1].continuation || observations[1].preview != 1 {
		t.Fatalf("second observation = %+v", observations[1])
	}
	if len(result.Entries) != 0 {
		t.Fatalf("result entries = %d, want 0 (the second handler cleared them)", len(result.Entries))
	}
	if !result.Continue {
		t.Fatal("result continuation must survive the entries override")
	}
	if !result.Valid {
		t.Fatal("result must be valid")
	}
}

func TestEmitBoundaryReportsInvalidPreviewsAndLetsLaterHandlersRepair(t *testing.T) {
	secondRan := false
	first := &Extension{
		Path: "<inline:invalid>",
		Handlers: map[string][]ExtensionHandler{"agent_before_settle": {func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			return &BoundaryResult{Entries: []SessionBoundaryDraft{
				&ContextEditEntryDraft{Type: "context_edit", TargetID: "missing"},
			}}, nil
		}}},
	}
	second := &Extension{
		Path: "<inline:repair>",
		Handlers: map[string][]ExtensionHandler{"agent_before_settle": {func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			beforeSettle, ok := event.(*AgentBeforeSettleEvent)
			if !ok {
				return nil, errUnexpectedEvent(event)
			}
			secondRan = true
			if len(beforeSettle.Entries) != 1 {
				t.Fatalf("second handler entries = %d, want 1", len(beforeSettle.Entries))
			}
			return &BoundaryResult{Entries: []SessionBoundaryDraft{}}, nil
		}}},
	}
	runner := newBoundaryTestRunner(t, []*Extension{first, second})
	var errors []string
	runner.OnError(func(err ExtensionError) { errors = append(errors, err.Error) })

	failOnContextEdit := func(entries []SessionBoundaryDraft) error {
		for _, entry := range entries {
			if entry.draftType() == "context_edit" {
				return errEntryMissing
			}
		}
		return nil
	}
	result, err := runner.EmitBoundary(context.Background(),
		&AgentBeforeSettleEvent{Type: "agent_before_settle", Outcome: AgentActivityCompleted},
		boundaryPreviewBuilder(failOnContextEdit))
	if err != nil {
		t.Fatal(err)
	}

	if !secondRan {
		t.Fatal("the second handler must run after an invalid preview")
	}
	found := false
	for _, message := range errors {
		if strings.Contains(message, "Invalid boundary entries: ") && strings.Contains(message, "Entry missing not found") {
			found = true
		}
	}
	if !found {
		t.Fatalf("errors = %v", errors)
	}
	if len(result.Entries) != 0 {
		t.Fatalf("result entries = %d, want 0", len(result.Entries))
	}
	if !result.Valid {
		t.Fatal("the repaired proposal must end valid")
	}
}

func TestEmitBoundaryKeepsSharedMutationsMadeBeforeHandlerThrows(t *testing.T) {
	extension := &Extension{
		Path: "<inline:throwing>",
		Handlers: map[string][]ExtensionHandler{"agent_before_settle": {func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			beforeSettle, ok := event.(*AgentBeforeSettleEvent)
			if !ok {
				return nil, errUnexpectedEvent(event)
			}
			beforeSettle.Entries = append(beforeSettle.Entries, &CustomEntryDraft{Type: "custom", CustomType: "kept"})
			return nil, errBoundaryFailed
		}}},
	}
	runner := newBoundaryTestRunner(t, []*Extension{extension})
	var errors []string
	runner.OnError(func(err ExtensionError) { errors = append(errors, err.Error) })

	result, err := runner.EmitBoundary(context.Background(),
		&AgentBeforeSettleEvent{Type: "agent_before_settle", Outcome: AgentActivityCompleted},
		boundaryPreviewBuilder(nil))
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Entries) != 1 || result.Entries[0].draftType() != "custom" {
		t.Fatalf("result entries = %+v, want the kept draft", result.Entries)
	}
	if len(errors) != 1 || errors[0] != "boundary failed" {
		t.Fatalf("errors = %v", errors)
	}
	if !result.Valid {
		t.Fatal("the preview rebuilt fine after the handler error")
	}
}
