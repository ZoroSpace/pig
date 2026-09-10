// scoped_models_selector_test.go (pi: test/scoped-models-selector.test.ts,
// v0.85.1) — the enable-all / disable-one toggle round trip and the
// all-enabled collapse (normalizeEnabled).
//
// Test adaptation (declared): pi builds a coding-agent harness; the pig
// component consumes plain []ai.Model, so the tests drive it directly with
// fixture models.
package components

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

type scopedModelState struct {
	id      string
	enabled bool
}

func newScopedTestSelector(t *testing.T, states []scopedModelState) (*ScopedModelsSelectorComponent, *[]string) {
	t.Helper()
	initTestTheme(t)
	tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)

	provider := "test-provider"
	models := make([]ai.Model, 0, len(states))
	var initial []string
	for _, s := range states {
		models = append(models, ai.Model{ID: s.id, Name: s.id, Provider: types.ProviderID(provider), Reasoning: true})
		if s.enabled {
			initial = append(initial, provider+"/"+s.id)
		}
	}
	if len(initial) == len(states) {
		initial = nil // all enabled
	}

	// enabled tracks the latest onChange payload; nil = all enabled.
	enabled := &initial
	selector := NewScopedModelsSelectorComponent(
		ScopedModelsConfig{AllModels: models, EnabledModelIDs: initial},
		ScopedModelsCallbacks{
			OnChange:  func(ids []string) { *enabled = ids },
			OnPersist: func([]string) {},
			OnCancel:  func() {},
		},
	)
	return selector, enabled
}

func (s *scopedModelState) enabledIn(list *[]string, provider string) bool {
	if list == nil || *list == nil {
		return true
	}
	for _, id := range *list {
		if id == provider+"/"+s.id {
			return true
		}
	}
	return false
}

func getScopedMarkerStates(t *testing.T, selector *ScopedModelsSelectorComponent, states []scopedModelState) []bool {
	t.Helper()
	lines := strings.Split(utils.StripAnsi(strings.Join(selector.Render(120), "\n")), "\n")
	out := make([]bool, 0, len(states))
	for _, s := range states {
		found := false
		for _, line := range lines {
			if strings.Contains(line, s.id+" [") {
				// The marker is the 4th column: "→ ✓ id [...]" or "  ✓ id [...]".
				rest := strings.TrimPrefix(strings.TrimPrefix(line, "→ "), "  ")
				out = append(out, strings.HasPrefix(rest, "✓ "))
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected rendered row for %s:\n%s", s.id, strings.Join(lines, "\n"))
		}
	}
	return out
}

func TestScopedModelsSelectorMarksEveryModelAfterEnablingAll(t *testing.T) {
	states := []scopedModelState{{id: "model-a", enabled: true}, {id: "model-b", enabled: false}, {id: "model-c", enabled: false}}
	selector, enabled := newScopedTestSelector(t, states)

	selector.HandleInput("\x01") // ctrl+a — enable all

	if *enabled != nil {
		t.Fatalf("expected nil (all enabled), got %v", *enabled)
	}
	want := []bool{true, true, true}
	got := getScopedMarkerStates(t, selector, states)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("markers = %v, want %v", got, want)
		}
	}
	if output := utils.StripAnsi(strings.Join(selector.Render(120), "\n")); !strings.Contains(output, "all enabled") {
		t.Fatalf("missing 'all enabled' summary:\n%s", output)
	}
}

func TestScopedModelsSelectorDisablesOnlySelectedAfterEnablingAll(t *testing.T) {
	states := []scopedModelState{{id: "model-a", enabled: true}, {id: "model-b", enabled: false}, {id: "model-c", enabled: false}}
	selector, _ := newScopedTestSelector(t, states)

	selector.HandleInput("\x01") // ctrl+a — enable all
	selector.HandleInput("\r")   // toggle selected (model-a)

	got := getScopedMarkerStates(t, selector, states)
	want := []bool{false, true, true}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("markers = %v, want %v", got, want)
		}
	}
}

func TestScopedModelsSelectorEnablesOnlySelectedAfterClearingAll(t *testing.T) {
	states := []scopedModelState{{id: "model-a", enabled: true}, {id: "model-b", enabled: true}, {id: "model-c", enabled: true}}
	selector, enabled := newScopedTestSelector(t, states)

	selector.HandleInput("\x18") // ctrl+x — clear all
	if len(*enabled) != 0 {
		t.Fatalf("expected empty scope, got %v", *enabled)
	}
	got := getScopedMarkerStates(t, selector, states)
	want := []bool{false, false, false}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("markers = %v, want %v", got, want)
		}
	}

	selector.HandleInput("\r") // toggle selected (model-a)
	if len(*enabled) != 1 {
		t.Fatalf("expected single-model scope, got %v", *enabled)
	}
	got = getScopedMarkerStates(t, selector, states)
	want = []bool{true, false, false}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("markers = %v, want %v", got, want)
		}
	}
}

func TestScopedModelsSelectorRestoresAllEnabledAfterReenablingLastDisabled(t *testing.T) {
	states := []scopedModelState{{id: "model-a", enabled: true}, {id: "model-b", enabled: false}, {id: "model-c", enabled: false}}
	selector, enabled := newScopedTestSelector(t, states)

	selector.HandleInput("\x01") // ctrl+a — enable all (nil)
	selector.HandleInput("\r")   // disable model-a; enabled models re-sort first: [b, c, a]
	selector.HandleInput("\x1b[B")
	selector.HandleInput("\x1b[B") // move selection back to model-a
	selector.HandleInput("\r")     // re-enable model-a

	if *enabled != nil {
		t.Fatalf("expected nil (all enabled), got %v", *enabled)
	}
	got := getScopedMarkerStates(t, selector, states)
	want := []bool{true, true, true}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("markers = %v, want %v", got, want)
		}
	}
	if output := utils.StripAnsi(strings.Join(selector.Render(120), "\n")); !strings.Contains(output, "all enabled") {
		t.Fatalf("missing 'all enabled' summary:\n%s", output)
	}
}
