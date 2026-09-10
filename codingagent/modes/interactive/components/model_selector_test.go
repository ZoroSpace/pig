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
