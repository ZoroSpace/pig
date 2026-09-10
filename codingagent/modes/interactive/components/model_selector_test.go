// model_selector_test.go (pi: test/model-selector.test.ts) — model selector
// catalog-refresh error reporting test.
//
// Test adaptation (declared): pi builds a full coding-agent harness and
// spies on ModelRuntime.refresh; pig injects a fake ModelSelectorRuntime +
// refresh function. pi asserts the Map insertion order ("openai,
// anthropic"); pig joins sorted keys ("anthropic, openai") — see the
// component's file header.
package components

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

type fakeModelSelectorRuntime struct {
	models []ai.Model
	err    string
}

func (f *fakeModelSelectorRuntime) GetAvailableSnapshot() []ai.Model { return f.models }
func (f *fakeModelSelectorRuntime) GetModel(providerID, modelID string) (ai.Model, bool) {
	for _, m := range f.models {
		if string(m.Provider) == providerID && m.ID == modelID {
			return m, true
		}
	}
	return ai.Model{}, false
}
func (f *fakeModelSelectorRuntime) GetError() string { return f.err }

// fakeSelectorTUI is the minimal TUI stub (pi: createFakeTui).
type fakeSelectorTUI struct{}

func (fakeSelectorTUI) RequestRender(bool) {}

func TestModelSelectorListsEveryCatalogThatFailedToRefresh(t *testing.T) {
	initTestTheme(t)

	runtime := &fakeModelSelectorRuntime{
		models: []ai.Model{{ID: "test-model", Provider: "test-provider"}},
	}
	refreshReturned := make(chan struct{})
	refresh := func(ctx context.Context) (ai.ModelsRefreshResult, error) {
		defer close(refreshReturned)
		return ai.ModelsRefreshResult{
			Errors: map[string]error{
				"anthropic": errors.New("unavailable"),
				"openai":    errors.New("unavailable"),
			},
		}, nil
	}

	// Serialize background mutations against test renders (the mutateUI
	// equivalent; pi is single-threaded).
	var mu sync.Mutex
	var render func(width int) []string

	selector := NewModelSelectorComponent(
		fakeSelectorTUI{},
		&runtime.models[0],
		runtime,
		nil,
		func(ai.Model) {},
		func() {},
		"",
		nil,
		nil,
		refresh,
		func(fn func()) {
			mu.Lock()
			defer mu.Unlock()
			fn()
		},
	)
	render = func(width int) []string {
		mu.Lock()
		defer mu.Unlock()
		return selector.Render(width)
	}

	<-refreshReturned

	// pi: vi.waitFor — pig polls render output until the refresh result is
	// reflected.
	deadline := time.Now().Add(2 * time.Second)
	for {
		rendered := stripVT(t, strings.Join(render(120), "\n"))
		if strings.Contains(rendered, "Could not refresh 2 model catalogs (anthropic, openai); showing cached models.") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("render missing refresh error message:\n%s", rendered)
		}
	}
}

// pi: "keeps the current model marked while browsing" (v0.85.1).
func TestModelSelectorKeepsCurrentModelMarkedWhileBrowsing(t *testing.T) {
	initTestTheme(t)

	runtime := &fakeModelSelectorRuntime{
		models: []ai.Model{
			{ID: "current-model", Name: "Current Model", Provider: "test-provider", Reasoning: true},
			{ID: "browsed-model", Name: "Browsed Model", Provider: "test-provider", Reasoning: true},
		},
	}
	current := &runtime.models[0]
	// Serialize test renders/input against the refresh goroutine's
	// mutations: the mutator and the test share one mutex (-race; pi is
	// single-threaded).
	var mu sync.Mutex
	mutator := func(fn func()) {
		mu.Lock()
		defer mu.Unlock()
		fn()
	}
	selector := NewModelSelectorComponent(
		fakeSelectorTUI{},
		current,
		runtime,
		nil,
		func(ai.Model) {},
		func() {},
		"",
		nil,
		nil,
		func(context.Context) (ai.ModelsRefreshResult, error) { return ai.ModelsRefreshResult{}, nil },
		mutator,
	)
	defer selector.Dispose()
	// Wait for the refresh goroutine to finish so Dispose cannot race it
	// (-race; pi awaits the refresh before rendering).
	<-selector.refreshDone

	getModelRow := func(id string) string {
		mu.Lock()
		defer mu.Unlock()
		renderedText := stripVT(t, strings.Join(selector.Render(120), "\n"))
		for _, line := range strings.Split(renderedText, "\n") {
			if strings.Contains(line, id+" [") {
				return strings.TrimRight(line, " ")
			}
		}
		return ""
	}

	if row := getModelRow("current-model"); row != "→ ✓ current-model [test-provider]" {
		t.Fatalf("unexpected row: %q", row)
	}
	mu.Lock()
	selector.HandleInput("\x1b[B")
	mu.Unlock()
	if row := getModelRow("current-model"); row != "  ✓ current-model [test-provider]" {
		t.Fatalf("unexpected row after browse: %q", row)
	}
	if row := getModelRow("browsed-model"); row != "→   browsed-model [test-provider]" {
		t.Fatalf("unexpected browsed row: %q", row)
	}
}

// pi: "uses the configured save binding" (v0.85.1).
func TestModelSelectorUsesConfiguredSaveBinding(t *testing.T) {
	initTestTheme(t)
	tui.SetKeybindings(core.NewKeybindingsManager(tui.KeybindingsConfig{
		"app.models.save": {"ctrl+r"},
	}, "").KeybindingsManager)

	runtime := &fakeModelSelectorRuntime{
		models: []ai.Model{{ID: "current-model", Name: "Current Model", Provider: "test-provider", Reasoning: true}},
	}
	current := &runtime.models[0]
	var saved []ai.Model
	// Serialize test renders/input against the refresh goroutine's
	// mutations (-race; pi is single-threaded).
	var mu sync.Mutex
	var selector *ModelSelectorComponent
	mutator := func(fn func()) {
		mu.Lock()
		defer mu.Unlock()
		fn()
	}
	lockedRender := func() string {
		mu.Lock()
		defer mu.Unlock()
		return stripVT(t, strings.Join(selector.Render(120), "\n"))
	}
	selector = NewModelSelectorComponent(
		fakeSelectorTUI{},
		current,
		runtime,
		nil,
		func(ai.Model) {},
		func() {},
		"",
		func(m ai.Model) { saved = append(saved, m) },
		nil,
		func(context.Context) (ai.ModelsRefreshResult, error) { return ai.ModelsRefreshResult{}, nil },
		mutator,
	)
	defer selector.Dispose()
	<-selector.refreshDone

	output := lockedRender()
	if !strings.Contains(output, "Ctrl+R to set as default") {
		t.Fatalf("expected configured binding in hints:\n%s", output)
	}
	mu.Lock()
	selector.HandleInput("\x13") // ctrl+s must not save
	mu.Unlock()
	if len(saved) != 0 {
		t.Fatal("ctrl+s should not trigger save")
	}
	selector.HandleInput("\x12") // ctrl+r
	if len(saved) != 1 || saved[0].ID != "current-model" {
		t.Fatalf("expected ctrl+r to save current-model, got %+v", saved)
	}
}
