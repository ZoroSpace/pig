// pi: test/harness/values.test.ts — bound value addresses and built-in
// durable addresses. Type-level assertions (expectTypeOf) have no Go
// counterpart (phantom types dropped; see values.go) and are skipped.
package session

import (
	"context"
	"testing"
)

func TestBindsImmutableScalarAndListAddressesWithValidatedComponents(t *testing.T) {
	scalar, err := NewValue("app.state", "")
	if err != nil {
		t.Fatal(err)
	}
	if scalar.Kind() != ValueKindValue || scalar.Namespace != "app.state" || scalar.Key != "" {
		t.Fatalf("unexpected scalar: %+v", scalar)
	}
	events, err := NewList("app.events", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	if events.Kind() != ValueKindList || events.Namespace != "app.events" || events.Key != "workspace" {
		t.Fatalf("unexpected list: %+v", events)
	}
	if _, err := NewValue("", ""); err == nil || !contains(err.Error(), "must not be empty") {
		t.Fatalf("expected empty-namespace error, got %v", err)
	}
	if _, err := NewValue("app\x00state", ""); err == nil || !contains(err.Error(), "must not contain") {
		t.Fatalf("expected null-byte namespace error, got %v", err)
	}
	if _, err := NewList("app.events", "bad\x00key"); err == nil || !contains(err.Error(), "must not contain") {
		t.Fatalf("expected null-byte key error, got %v", err)
	}
	application, err := NewValue("pi.application", "")
	if err != nil {
		t.Fatal(err)
	}
	if application.Namespace != "pi.application" || application.Key != "" {
		t.Fatalf("unexpected application address: %+v", application)
	}
}

func TestUsesSeparatelyConstructedEqualAddressesForOneDurableLocation(t *testing.T) {
	ctx := context.Background()
	storage := NewMemoryStorage(MemoryStorageOptions{Now: func() int64 { return 1 }})
	defer func() { _ = storage.Close(ctx) }()
	first := MustValue("app.state", "workspace")
	second := MustValue("app.state", "workspace")
	if _, err := storage.Commit([]Write{SetValue(first, map[string]any{"ready": true})}, ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := storage.GetValue(second, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.Seq != 1 {
		t.Fatalf("unexpected stored: %+v", stored)
	}
	if stored.Address.Namespace != first.Namespace || stored.Address.Key != first.Key {
		t.Fatalf("unexpected stored address: %+v", stored.Address)
	}
	value, ok := stored.Value.(map[string]any)
	if !ok || value["ready"] != true {
		t.Fatalf("unexpected stored value: %+v", stored.Value)
	}
}

func TestUsesTheExactReservedNamespacesKeysKindsAndValueTypes(t *testing.T) {
	type addr struct {
		kind      ValueKind
		namespace string
		key       string
	}
	toAddr := func(v Value) addr { return addr{v.Kind(), v.Namespace, v.Key} }
	toListAddr := func(v ValueList) addr { return addr{v.Kind(), v.Namespace, v.Key} }

	got := []addr{
		toAddr(BranchTip("review")),
		toAddr(LaneConfig("review")),
		toAddr(LaneStateAddress("review")),
		toAddr(OperationResult("operation")),
		toAddr(OperationMetaAddress("operation")),
		toAddr(OperationStateAddress("operation")),
		toAddr(OperationToolArgs("operation", "step", 2)),
		toAddr(OperationToolMemo("operation", "invocation", "name")),
		toAddr(OperationPreparation("operation", "task")),
		toAddr(PendingEntryAddress("entry")),
		toAddr(PendingToolOutput("operation", "invocation")),
		toListAddr(PendingAssistantFrames("operation", "response")),
		toAddr(SessionNameAddress),
		toAddr(EntryLabel("entry")),
	}
	want := []addr{
		{ValueKindValue, "pi.branch.tip", "review"},
		{ValueKindValue, "pi.lane.config", "review"},
		{ValueKindValue, "pi.lane.state", "review"},
		{ValueKindValue, "pi.result", "operation"},
		{ValueKindValue, "pi.op.meta", "operation"},
		{ValueKindValue, "pi.op.state", "operation"},
		{ValueKindValue, "pi.op.tool_args", "operation:step:2"},
		{ValueKindValue, "pi.op.tool_memo", "operation:invocation:name"},
		{ValueKindValue, "pi.op.preparation", "operation:task"},
		{ValueKindValue, "pi.pending.entry", "entry"},
		{ValueKindValue, "pi.pending.tool_output", "operation:invocation"},
		{ValueKindList, "pi.pending.assistant_frame", "operation:response"},
		{ValueKindValue, "pi.session.name", ""},
		{ValueKindValue, "pi.entry.label", "entry"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("address %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestExportsExactlyTheFiveDocumentedScanPrefixConstructors(t *testing.T) {
	type addr struct {
		kind      ValueKind
		namespace string
		key       string
	}
	toAddr := func(v Value) addr { return addr{v.Kind(), v.Namespace, v.Key} }

	prefixes := []addr{
		toAddr(BranchTipPrefix()),
		toAddr(OperationToolArgsPrefix("operation", "")),
		toAddr(OperationToolMemoPrefix("operation", "invocation")),
		toAddr(OperationPreparationPrefix("operation")),
		toAddr(PendingToolOutputPrefix("operation")),
	}
	want := []addr{
		{ValueKindValue, "pi.branch.tip", ""},
		{ValueKindValue, "pi.op.tool_args", "operation:"},
		{ValueKindValue, "pi.op.tool_memo", "operation:invocation:"},
		{ValueKindValue, "pi.op.preparation", "operation:"},
		{ValueKindValue, "pi.pending.tool_output", "operation:"},
	}
	if len(prefixes) != 5 {
		t.Fatalf("expected 5 prefixes, got %d", len(prefixes))
	}
	for i := range want {
		if prefixes[i] != want[i] {
			t.Fatalf("prefix %d: got %+v, want %+v", i, prefixes[i], want[i])
		}
	}
	if got := OperationToolArgsPrefix("operation", "step").Key; got != "operation:step:" {
		t.Fatalf("tool args step prefix = %q", got)
	}
	if got := OperationToolMemoPrefix("operation", "").Key; got != "operation:" {
		t.Fatalf("tool memo prefix = %q", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
