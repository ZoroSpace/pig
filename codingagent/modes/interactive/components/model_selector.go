// model_selector.go (pi: components/model-selector.ts) — the /model
// selector: searchable model list with all/scoped scope toggle, current +
// default badges, background catalog refresh, and Ctrl+S set-as-default.
//
// Declared deviations (pig import direction — components cannot import the
// interactive package where pi's refreshModelCatalogs lives):
//   - ModelSelectorRuntime is the slice of ModelRuntime the component reads.
//   - The catalog refresh is injected as RefreshCatalogs (bound to
//     interactive.RefreshModelCatalogs + the runtime at the callsite).
//   - Multi-error refresh messages join provider ids in sorted order (pi
//     iterates Map insertion order; pig map iteration is unordered).
package components

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/codingagent/core"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// ModelItem is one selectable model entry (pi: ModelItem).
type ModelItem struct {
	provider string
	id       string
	model    ai.Model
}

// ModelSelectorDefaultReference identifies the configured default model (pi:
// DefaultModelReference).
type ModelSelectorDefaultReference struct {
	Provider string
	ID       string
}

// ModelSelectorRuntime is the runtime surface the selector reads (pig
// addition; *core.ModelRuntime satisfies it).
type ModelSelectorRuntime interface {
	GetAvailableSnapshot() []ai.Model
	GetModel(providerID, modelID string) (ai.Model, bool)
	GetError() string
}

// ModelSelectorTUI is the TUI surface the selector needs (pig addition —
// pi takes the full TUI but only calls requestRender; the narrow interface
// keeps the component testable).
type ModelSelectorTUI interface {
	RequestRender(force bool)
}

// modelScope is the model listing scope (pi: ModelScope).
type modelScope string

const (
	modelScopeAll    modelScope = "all"
	modelScopeScoped modelScope = "scoped"
)

// ModelSelectorComponent renders a model selector with search (pi:
// ModelSelectorComponent extends Container implements Focusable).
type ModelSelectorComponent struct {
	tui.Container

	searchInput   *tui.Input
	listContainer *tui.Container

	allModels            []ModelItem
	scopedModelItems     []ModelItem
	activeModels         []ModelItem
	filteredModels       []ModelItem
	selectedIndex        int
	currentModel         *ai.Model
	modelRuntime         ModelSelectorRuntime
	onSelect             func(model ai.Model)
	onSelectAsDefault    func(model ai.Model)
	onCancel             func()
	errorMessage         string
	refreshStatusMessage string
	refreshStatusSuccess bool
	tui                  ModelSelectorTUI
	scopedModels         []core.ScopedModel
	defaultModel         *ModelSelectorDefaultReference
	scope                modelScope
	scopeText            *tui.Text
	scopeHintText        *tui.Text
	refreshCtx           context.Context
	refreshCancel        context.CancelFunc
	refreshDone          chan struct{}
	closed               bool

	// RefreshCatalogs performs the all-catalog refresh (pig addition — see
	// file header).
	RefreshCatalogs func(ctx context.Context) (ai.ModelsRefreshResult, error)

	// mutator serializes background-goroutine component mutations (pig
	// concurrency note; nil = run inline, tests). Set at construction — the
	// refresh goroutine starts there.
	mutator func(fn func())
}

// NewModelSelectorComponent builds the selector (pi: constructor;
// initialSearchInput "" = none, onSelectAsDefault nil = hidden, defaultModel
// nil = none).
func NewModelSelectorComponent(
	tuiImpl ModelSelectorTUI,
	currentModel *ai.Model,
	modelRuntime ModelSelectorRuntime,
	scopedModels []core.ScopedModel,
	onSelect func(model ai.Model),
	onCancel func(),
	initialSearchInput string,
	onSelectAsDefault func(model ai.Model),
	defaultModel *ModelSelectorDefaultReference,
	refreshCatalogs func(ctx context.Context) (ai.ModelsRefreshResult, error),
	mutator func(func()),
) *ModelSelectorComponent {
	s := &ModelSelectorComponent{
		Container:            tui.Container{},
		currentModel:         currentModel,
		modelRuntime:         modelRuntime,
		scopedModels:         scopedModels,
		defaultModel:         defaultModel,
		scope:                modelScopeAll,
		onSelect:             onSelect,
		onSelectAsDefault:    onSelectAsDefault,
		onCancel:             onCancel,
		tui:                  tuiImpl,
		refreshStatusMessage: "Refreshing model catalogs…",
		RefreshCatalogs:      refreshCatalogs,
		mutator:              mutator,
	}
	if len(scopedModels) > 0 {
		s.scope = modelScopeScoped
	}
	s.refreshCtx, s.refreshCancel = context.WithCancel(context.Background())
	s.refreshDone = make(chan struct{})

	// Add top border
	s.AddChild(NewDynamicBorder(nil))
	s.AddChild(tui.NewSpacer(1))

	// Add hint about model filtering
	if len(scopedModels) > 0 {
		s.scopeText = tui.NewText(s.getScopeText(), 0, 0)
		s.AddChild(s.scopeText)
		s.scopeHintText = tui.NewText(s.getScopeHintText(), 0, 0)
		s.AddChild(s.scopeHintText)
	} else {
		hintText := "Only showing models from configured providers. Use /login to add providers."
		s.AddChild(tui.NewText(theme.Fg("warning", hintText), 0, 0))
	}
	s.AddChild(tui.NewSpacer(1))

	// Create search input
	s.searchInput = tui.NewInput()
	if initialSearchInput != "" {
		s.searchInput.SetValue(initialSearchInput)
	}
	s.searchInput.OnSubmit = func(_ string) {
		// Enter on search input selects the first filtered item
		if s.selectedIndex < len(s.filteredModels) {
			s.handleSelect(s.filteredModels[s.selectedIndex].model)
		}
	}
	s.AddChild(s.searchInput)

	s.AddChild(tui.NewSpacer(1))

	// Create list container
	s.listContainer = &tui.Container{}
	s.AddChild(s.listContainer)

	s.AddChild(tui.NewSpacer(1))

	// Hint
	if s.onSelectAsDefault != nil {
		s.AddChild(tui.NewText(theme.Fg("dim", "  Enter to select · Ctrl+S to set as default · Esc to cancel"), 0, 0))
	}

	// Add bottom border
	s.AddChild(NewDynamicBorder(nil))

	// Render the current snapshot immediately, then refresh in the background.
	s.loadModelsFromSnapshot()
	if initialSearchInput != "" {
		s.filterModels(initialSearchInput)
	} else {
		s.updateList()
	}
	s.tui.RequestRender(false)
	s.refreshModels()
	return s
}

func (s *ModelSelectorComponent) mutate(fn func()) {
	if s.mutator != nil {
		s.mutator(fn)
		return
	}
	fn()
}

func (s *ModelSelectorComponent) loadModelsFromSnapshot() {
	models := make([]ModelItem, 0)
	for _, model := range s.modelRuntime.GetAvailableSnapshot() {
		models = append(models, ModelItem{provider: string(model.Provider), id: model.ID, model: model})
	}
	s.allModels = s.sortModels(models)
	scoped := make([]core.ScopedModel, 0, len(s.scopedModels))
	for _, scopedItem := range s.scopedModels {
		if refreshed, ok := s.modelRuntime.GetModel(string(scopedItem.Model.Provider), scopedItem.Model.ID); ok {
			scopedItem.Model = refreshed
		}
		scoped = append(scoped, scopedItem)
	}
	s.scopedModels = scoped
	s.scopedModelItems = make([]ModelItem, 0, len(s.scopedModels))
	for _, scopedItem := range s.scopedModels {
		s.scopedModelItems = append(s.scopedModelItems, ModelItem{
			provider: string(scopedItem.Model.Provider), id: scopedItem.Model.ID, model: scopedItem.Model,
		})
	}
	if s.scope == modelScopeScoped {
		s.activeModels = s.scopedModelItems
	} else {
		s.activeModels = s.allModels
	}
	s.filteredModels = s.activeModels
	currentIndex := -1
	for i := range s.filteredModels {
		if ai.ModelsAreEqual(s.currentModel, &s.filteredModels[i].model) {
			currentIndex = i
			break
		}
	}
	if currentIndex >= 0 {
		s.selectedIndex = currentIndex
	} else {
		s.selectedIndex = minInt(s.selectedIndex, maxInt(0, len(s.filteredModels)-1))
	}
}

func (s *ModelSelectorComponent) refreshModels() {
	go func() {
		defer close(s.refreshDone)
		timeoutCtx, cancel := context.WithTimeout(s.refreshCtx, 15*time.Second)
		defer cancel()
		result, err := s.RefreshCatalogs(timeoutCtx)
		s.mutate(func() {
			if s.closed {
				return
			}
			if err != nil {
				s.refreshStatusMessage = ""
				if s.refreshCtx.Err() != nil && timeoutCtx.Err() == context.Canceled {
					// disposed — drop silently (pi: catch with aborted signal)
					return
				}
				if timeoutCtx.Err() == context.DeadlineExceeded {
					s.errorMessage = "Model refresh timed out; showing cached models."
				} else {
					s.errorMessage = fmt.Sprintf("Could not refresh model catalogs: %s", errorMessageOf(err))
				}
				s.updateList()
				s.tui.RequestRender(false)
				return
			}
			s.refreshStatusMessage = ""
			if result.Aborted {
				s.errorMessage = "Model refresh timed out; showing cached models."
			} else if len(result.Errors) == 1 {
				for provider := range result.Errors {
					s.errorMessage = fmt.Sprintf("Could not refresh %s; showing cached models.", provider)
					break
				}
			} else if len(result.Errors) > 1 {
				providers := make([]string, 0, len(result.Errors))
				for provider := range result.Errors {
					providers = append(providers, provider)
				}
				sort.Strings(providers)
				s.errorMessage = fmt.Sprintf("Could not refresh %d model catalogs (%s); showing cached models.",
					len(result.Errors), strings.Join(providers, ", "))
			} else {
				s.errorMessage = s.modelRuntime.GetError()
				if s.errorMessage == "" {
					s.refreshStatusMessage = "Model catalogs refreshed."
					s.refreshStatusSuccess = true
				}
			}
			s.loadModelsFromSnapshot()
			s.filterModels(s.searchInput.GetValue())
			s.tui.RequestRender(false)
		})
	}()
}

// Dispose cancels the background refresh (pi: dispose).
func (s *ModelSelectorComponent) Dispose() {
	if s.closed {
		return
	}
	s.closed = true
	s.refreshCancel()
}

func (s *ModelSelectorComponent) sortModels(models []ModelItem) []ModelItem {
	sorted := append([]ModelItem{}, models...)
	// Sort: current model first, default model second, then by provider.
	sort.SliceStable(sorted, func(a, b int) bool {
		aIsCurrent := ai.ModelsAreEqual(s.currentModel, &sorted[a].model)
		bIsCurrent := ai.ModelsAreEqual(s.currentModel, &sorted[b].model)
		if aIsCurrent != bIsCurrent {
			return aIsCurrent
		}
		aIsDefault := s.isDefaultModel(sorted[a].model)
		bIsDefault := s.isDefaultModel(sorted[b].model)
		if aIsDefault != bIsDefault {
			return aIsDefault
		}
		return sorted[a].provider < sorted[b].provider
	})
	return sorted
}

func (s *ModelSelectorComponent) getScopeText() string {
	allColor := "muted"
	if s.scope == modelScopeAll {
		allColor = "accent"
	}
	scopedColor := "muted"
	if s.scope == modelScopeScoped {
		scopedColor = "accent"
	}
	return theme.Fg("muted", "Scope: ") + theme.Fg(allColor, "all") +
		theme.Fg("muted", " | ") + theme.Fg(scopedColor, "scoped")
}

func (s *ModelSelectorComponent) getScopeHintText() string {
	return coretools.KeyHint("tui.input.tab", "scope") + theme.Fg("muted", " (all/scoped)")
}

func (s *ModelSelectorComponent) isDefaultModel(model ai.Model) bool {
	return s.defaultModel != nil &&
		s.defaultModel.Provider == string(model.Provider) && s.defaultModel.ID == model.ID
}

func (s *ModelSelectorComponent) isDefaultSearch(query string) bool {
	normalized := strings.ToLower(strings.TrimSpace(query))
	return len(normalized) > 0 && strings.HasPrefix("default", normalized)
}

func (s *ModelSelectorComponent) setScope(scope modelScope) {
	if s.scope == scope {
		return
	}
	s.scope = scope
	if s.scope == modelScopeScoped {
		s.activeModels = s.scopedModelItems
	} else {
		s.activeModels = s.allModels
	}
	currentIndex := -1
	for i := range s.activeModels {
		if ai.ModelsAreEqual(s.currentModel, &s.activeModels[i].model) {
			currentIndex = i
			break
		}
	}
	if currentIndex >= 0 {
		s.selectedIndex = currentIndex
	} else {
		s.selectedIndex = 0
	}
	s.filterModels(s.searchInput.GetValue())
	if s.scopeText != nil {
		s.scopeText.SetText(s.getScopeText())
	}
}

func (s *ModelSelectorComponent) filterModels(query string) {
	if query != "" {
		filtered := tui.FuzzyFilter(s.activeModels, query, func(item ModelItem) string {
			defaultText := ""
			if s.isDefaultModel(item.model) {
				defaultText = " default"
			}
			return GetModelSelectorSearchText(ModelSearchItem{ID: item.id, Provider: item.provider, Name: item.model.Name}) + defaultText
		})
		if s.isDefaultSearch(query) {
			var defaultItems []ModelItem
			defaultKeys := map[string]bool{}
			for _, item := range s.activeModels {
				if s.isDefaultModel(item.model) {
					defaultItems = append(defaultItems, item)
					defaultKeys[item.provider+"\x00"+item.id] = true
				}
			}
			s.filteredModels = append(append([]ModelItem{}, defaultItems...), filtered...)
			// pi spreads default items first, then filtered items not already
			// in the default set — dedupe here.
			out := make([]ModelItem, 0, len(s.filteredModels))
			seen := map[string]bool{}
			for _, item := range s.filteredModels {
				key := item.provider + "\x00" + item.id
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, item)
			}
			s.filteredModels = out
		} else {
			s.filteredModels = filtered
		}
	} else {
		s.filteredModels = s.activeModels
	}
	// When filtering by a query, move the selector to the top row so the best
	// match is highlighted. When the query is cleared, keep the current position
	// clamped to the (restored) list length.
	if query != "" {
		s.selectedIndex = 0
	} else {
		s.selectedIndex = minInt(s.selectedIndex, maxInt(0, len(s.filteredModels)-1))
	}
	s.updateList()
}

func (s *ModelSelectorComponent) updateList() {
	s.listContainer.Clear()

	maxVisible := 10
	startIndex := maxInt(0, minInt(
		s.selectedIndex-maxVisible/2,
		len(s.filteredModels)-maxVisible,
	))
	endIndex := minInt(startIndex+maxVisible, len(s.filteredModels))

	// Show visible slice of filtered models
	for i := startIndex; i < endIndex; i++ {
		item := s.filteredModels[i]

		isSelected := i == s.selectedIndex
		isCurrent := ai.ModelsAreEqual(s.currentModel, &item.model)
		isDefault := s.isDefaultModel(item.model)
		defaultBadge := ""
		if isDefault {
			defaultBadge = theme.Fg("muted", " · default")
		}

		var line string
		if isSelected {
			prefix := theme.Fg("accent", "→ ")
			modelText := item.id
			providerBadge := theme.Fg("muted", fmt.Sprintf("[%s]", item.provider))
			checkmark := ""
			if isCurrent {
				checkmark = theme.Fg("success", " ✓")
			}
			line = prefix + theme.Fg("accent", modelText) + " " + providerBadge + defaultBadge + checkmark
		} else {
			modelText := "  " + item.id
			providerBadge := theme.Fg("muted", fmt.Sprintf("[%s]", item.provider))
			checkmark := ""
			if isCurrent {
				checkmark = theme.Fg("success", " ✓")
			}
			line = modelText + " " + providerBadge + defaultBadge + checkmark
		}

		s.listContainer.AddChild(tui.NewText(line, 0, 0))
	}

	// Add scroll indicator if needed
	if startIndex > 0 || endIndex < len(s.filteredModels) {
		scrollInfo := theme.Fg("muted", fmt.Sprintf("  (%d/%d)", s.selectedIndex+1, len(s.filteredModels)))
		s.listContainer.AddChild(tui.NewText(scrollInfo, 0, 0))
	}

	// Show error message or "no results" if empty
	if s.errorMessage != "" {
		// Show error in red
		for _, line := range strings.Split(s.errorMessage, "\n") {
			s.listContainer.AddChild(tui.NewText(theme.Fg("error", line), 0, 0))
		}
	} else if len(s.filteredModels) == 0 {
		s.listContainer.AddChild(tui.NewText(theme.Fg("muted", "  No matching models"), 0, 0))
	} else {
		selected := s.filteredModels[s.selectedIndex]
		s.listContainer.AddChild(tui.NewSpacer(1))
		s.listContainer.AddChild(tui.NewText(theme.Fg("muted", fmt.Sprintf("  Model Name: %s", selected.model.Name)), 0, 0))
	}
	if s.refreshStatusMessage != "" {
		s.listContainer.AddChild(tui.NewSpacer(1))
		color := "muted"
		if s.refreshStatusSuccess {
			color = "success"
		}
		s.listContainer.AddChild(tui.NewText(theme.Fg(color, fmt.Sprintf("  %s", s.refreshStatusMessage)), 0, 0))
	}
}

// HandleInput implements InputHandler.
func (s *ModelSelectorComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	if kb.Matches(keyData, "tui.input.tab") {
		if len(s.scopedModelItems) > 0 {
			nextScope := modelScopeAll
			if s.scope == modelScopeAll {
				nextScope = modelScopeScoped
			}
			s.setScope(nextScope)
			if s.scopeHintText != nil {
				s.scopeHintText.SetText(s.getScopeHintText())
			}
		}
		return
	}
	switch {
	case kb.Matches(keyData, "tui.select.up"):
		// Up arrow - wrap to bottom when at top
		if len(s.filteredModels) == 0 {
			return
		}
		if s.selectedIndex == 0 {
			s.selectedIndex = len(s.filteredModels) - 1
		} else {
			s.selectedIndex--
		}
		s.updateList()
	case kb.Matches(keyData, "tui.select.down"):
		// Down arrow - wrap to top when at bottom
		if len(s.filteredModels) == 0 {
			return
		}
		if s.selectedIndex == len(s.filteredModels)-1 {
			s.selectedIndex = 0
		} else {
			s.selectedIndex++
		}
		s.updateList()
	case kb.Matches(keyData, "tui.select.confirm"):
		// Enter
		if s.selectedIndex < len(s.filteredModels) {
			s.handleSelect(s.filteredModels[s.selectedIndex].model)
		}
	case kb.Matches(keyData, "tui.select.cancel"):
		// Escape or Ctrl+C
		s.Dispose()
		s.onCancel()
	case tui.MatchesKey(keyData, tui.KeyCtrl("s")) && s.onSelectAsDefault != nil:
		// Ctrl+S — select and save as default
		if s.selectedIndex < len(s.filteredModels) {
			s.Dispose()
			s.onSelectAsDefault(s.filteredModels[s.selectedIndex].model)
		}
	default:
		// Pass everything else to search input
		s.searchInput.HandleInput(keyData)
		s.filterModels(s.searchInput.GetValue())
	}
}

func (s *ModelSelectorComponent) handleSelect(model ai.Model) {
	s.Dispose()
	s.onSelect(model)
}

// GetSearchInput exposes the search input (pi: getSearchInput).
func (s *ModelSelectorComponent) GetSearchInput() *tui.Input { return s.searchInput }
