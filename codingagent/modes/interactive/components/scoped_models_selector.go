// scoped_models_selector.go (pi: components/scoped-models-selector.ts) —
// the /scoped-models enable/disable multi-select for model cycling with
// reorder, provider-group toggle, filtered enable/clear, and Ctrl+S persist.
//
// Declared deviation: updateModels/setRefreshStatus are called from the
// interactive-mode refresh goroutine; the callsite must wrap them in its
// mutateUI (pig concurrency note) — pi runs them on the event loop.
package components

import (
	"fmt"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/codingagent/core"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// scopedModelItem is one list entry (pi: ModelItem in
// scoped-models-selector.ts).
type scopedModelItem struct {
	fullID  string
	model   *ai.Model
	enabled bool
}

// isEnabled reports whether an id is enabled (nil = all enabled).
func isEnabled(enabledIds []string, id string) bool {
	return enabledIds == nil || containsString(enabledIds, id)
}

// toggleEnabled toggles one id (pi: toggle).
func toggleEnabled(enabledIds []string, id string) []string {
	if enabledIds == nil {
		// First toggle: start with only this one
		return []string{id}
	}
	index := indexOfString(enabledIds, id)
	if index >= 0 {
		return append(append([]string{}, enabledIds[:index]...), enabledIds[index+1:]...)
	}
	return append(append([]string{}, enabledIds...), id)
}

// enableAllIds enables targets (pi: enableAll). Result nil = all enabled.
func enableAllIds(enabledIds []string, allIds []string, targetIds []string) []string {
	if enabledIds == nil {
		// Already all enabled
		return nil
	}
	targets := targetIds
	if targets == nil {
		targets = allIds
	}
	result := append([]string{}, enabledIds...)
	for _, id := range targets {
		if !containsString(result, id) {
			result = append(result, id)
		}
	}
	if len(result) == len(allIds) && everyIn(result, allIds) {
		return nil
	}
	return result
}

// clearAllIds disables targets (pi: clearAll).
func clearAllIds(enabledIds []string, allIds []string, targetIds []string) []string {
	if enabledIds == nil {
		if targetIds != nil {
			out := []string{}
			for _, id := range allIds {
				if !containsString(targetIds, id) {
					out = append(out, id)
				}
			}
			return out
		}
		return []string{}
	}
	var targets map[string]bool
	if targetIds != nil {
		targets = map[string]bool{}
		for _, id := range targetIds {
			targets[id] = true
		}
	} else {
		targets = map[string]bool{}
		for _, id := range enabledIds {
			targets[id] = true
		}
	}
	out := []string{}
	for _, id := range enabledIds {
		if !targets[id] {
			out = append(out, id)
		}
	}
	return out
}

// moveEnabled moves an id by delta within the enabled list (pi: move).
func moveEnabled(enabledIds []string, id string, delta int) []string {
	if enabledIds == nil {
		return nil
	}
	list := append([]string{}, enabledIds...)
	index := indexOfString(list, id)
	if index < 0 {
		return list
	}
	newIndex := index + delta
	if newIndex < 0 || newIndex >= len(list) {
		return list
	}
	list[index], list[newIndex] = list[newIndex], list[index]
	return list
}

// getSortedIds orders all ids enabled-first (pi: getSortedIds).
func getSortedIds(enabledIds []string, allIds []string) []string {
	if enabledIds == nil {
		return allIds
	}
	enabledSet := map[string]bool{}
	for _, id := range enabledIds {
		enabledSet[id] = true
	}
	out := append([]string{}, enabledIds...)
	for _, id := range allIds {
		if !enabledSet[id] {
			out = append(out, id)
		}
	}
	return out
}

func indexOfString(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return -1
}

func everyIn(subset, superset []string) bool {
	set := map[string]bool{}
	for _, s := range superset {
		set[s] = true
	}
	for _, s := range subset {
		if !set[s] {
			return false
		}
	}
	return true
}

// ScopedModelsConfig is the constructor config (pi: ModelsConfig).
type ScopedModelsConfig struct {
	AllModels       []ai.Model
	EnabledModelIDs []string // nil = all enabled
	RefreshStatus   string
}

// ScopedModelsCallbacks carries the component callbacks (pi:
// ModelsCallbacks).
type ScopedModelsCallbacks struct {
	// OnChange fires whenever the enabled model set or order changes
	// (session-only, no persist; pi: onChange — async half dropped, pig
	// callback is sync).
	OnChange func(enabledModelIDs []string)
	// OnPersist fires when the user wants to persist the current selection
	// to settings (pi: onPersist).
	OnPersist func(enabledModelIDs []string)
	// OnCancel fires on escape / Ctrl+C with an empty search (pi: onCancel).
	OnCancel func()
}

// ScopedModelsSelectorComponent is the model configuration component (pi:
// ScopedModelsSelectorComponent extends Container implements Focusable).
type ScopedModelsSelectorComponent struct {
	tui.Container

	modelsById        map[string]ai.Model
	allIds            []string
	enabledIds        []string // nil = all enabled
	filteredItems     []*scopedModelItem
	selectedIndex     int
	searchInput       *tui.Input
	listContainer     *tui.Container
	footerText        *tui.Text
	callbacks         ScopedModelsCallbacks
	maxVisible        int
	isDirty           bool
	refreshStatusText *tui.Text

	focused bool
}

// NewScopedModelsSelectorComponent builds the selector (pi: constructor).
func NewScopedModelsSelectorComponent(config ScopedModelsConfig, callbacks ScopedModelsCallbacks) *ScopedModelsSelectorComponent {
	s := &ScopedModelsSelectorComponent{
		Container:  tui.Container{},
		modelsById: map[string]ai.Model{},
		callbacks:  callbacks,
		maxVisible: 8,
	}

	for _, model := range config.AllModels {
		fullID := fmt.Sprintf("%s/%s", model.Provider, model.ID)
		s.modelsById[fullID] = model
		s.allIds = append(s.allIds, fullID)
	}

	if config.EnabledModelIDs != nil {
		s.enabledIds = append([]string{}, config.EnabledModelIDs...)
	}
	s.filteredItems = s.buildItems()

	// Header
	s.AddChild(NewDynamicBorder(nil))
	s.AddChild(tui.NewSpacer(1))
	s.AddChild(tui.NewText(theme.Fg("accent", theme.Bold("Model Configuration")), 0, 0))
	s.AddChild(tui.NewText(theme.Fg("muted", fmt.Sprintf("Session-only. %s to save to settings.",
		coretools.KeyText(core.AppKeybindingModelsSave))), 0, 0))
	s.AddChild(tui.NewSpacer(1))

	// Search input
	s.searchInput = tui.NewInput()
	s.AddChild(s.searchInput)
	s.AddChild(tui.NewSpacer(1))

	// List container
	s.listContainer = &tui.Container{}
	s.AddChild(s.listContainer)

	// Footer hint
	s.AddChild(tui.NewSpacer(1))
	if config.RefreshStatus != "" {
		s.refreshStatusText = tui.NewText(theme.Fg("muted", fmt.Sprintf("  %s", config.RefreshStatus)), 0, 0)
		s.AddChild(s.refreshStatusText)
	}
	s.footerText = tui.NewText(s.getFooterText(), 0, 0)
	s.AddChild(s.footerText)

	s.AddChild(NewDynamicBorder(nil))
	s.updateList()
	return s
}

// GetFocused/SetFocused implement Focusable - propagate to searchInput for
// IME cursor positioning.
func (s *ScopedModelsSelectorComponent) GetFocused() bool { return s.focused }
func (s *ScopedModelsSelectorComponent) SetFocused(value bool) {
	s.focused = value
	s.searchInput.SetFocused(value)
}

// UpdateModels refreshes the model set (pi: updateModels; enabledModelIDs
// nil + unset flag → pi uses undefined; pig passes applyEnabled=false).
func (s *ScopedModelsSelectorComponent) UpdateModels(models []ai.Model, enabledModelIDs []string, applyEnabled bool) {
	selectedID := ""
	if s.selectedIndex < len(s.filteredItems) {
		selectedID = s.filteredItems[s.selectedIndex].fullID
	}
	if applyEnabled {
		if enabledModelIDs == nil {
			s.enabledIds = nil
		} else {
			s.enabledIds = append([]string{}, enabledModelIDs...)
		}
	}
	s.modelsById = map[string]ai.Model{}
	s.allIds = nil
	for _, model := range models {
		fullID := fmt.Sprintf("%s/%s", model.Provider, model.ID)
		s.modelsById[fullID] = model
		s.allIds = append(s.allIds, fullID)
	}
	s.refresh()
	refreshedIndex := -1
	if selectedID != "" {
		for i, item := range s.filteredItems {
			if item.fullID == selectedID {
				refreshedIndex = i
				break
			}
		}
	}
	if refreshedIndex >= 0 {
		s.selectedIndex = refreshedIndex
		s.updateList()
	}
}

// SetRefreshStatus updates the refresh status line (pi: setRefreshStatus).
func (s *ScopedModelsSelectorComponent) SetRefreshStatus(message string, kind string) {
	if s.refreshStatusText != nil {
		s.refreshStatusText.SetText(theme.Fg(kind, fmt.Sprintf("  %s", message)))
	}
}

func (s *ScopedModelsSelectorComponent) buildItems() []*scopedModelItem {
	items := []*scopedModelItem{}
	for _, id := range getSortedIds(s.enabledIds, s.allIds) {
		var model *ai.Model
		if m, ok := s.modelsById[id]; ok {
			model = &m
		}
		items = append(items, &scopedModelItem{
			fullID:  id,
			model:   model,
			enabled: isEnabled(s.enabledIds, id),
		})
	}
	return items
}

func (s *ScopedModelsSelectorComponent) getFooterText() string {
	enabledCount := len(s.allIds)
	unavailableCount := 0
	if s.enabledIds != nil {
		enabledCount = 0
		unavailableCount = 0
		for _, id := range s.enabledIds {
			if _, ok := s.modelsById[id]; ok {
				enabledCount++
			} else {
				unavailableCount++
			}
		}
	}
	allEnabled := s.enabledIds == nil
	countText := "all enabled"
	if !allEnabled {
		countText = fmt.Sprintf("%d/%d enabled", enabledCount, len(s.allIds))
		if unavailableCount > 0 {
			countText += fmt.Sprintf(" · %d unavailable", unavailableCount)
		}
	}
	parts := []string{
		fmt.Sprintf("%s toggle", coretools.KeyText("tui.select.confirm")),
		fmt.Sprintf("%s all", coretools.KeyText(core.AppKeybindingModelsEnableAll)),
		fmt.Sprintf("%s clear", coretools.KeyText(core.AppKeybindingModelsClearAll)),
		fmt.Sprintf("%s provider", coretools.KeyText(core.AppKeybindingModelsToggleProvider)),
		fmt.Sprintf("%s/%s reorder", coretools.KeyText(core.AppKeybindingModelsReorderUp), coretools.KeyText(core.AppKeybindingModelsReorderDown)),
		fmt.Sprintf("%s save", coretools.KeyText(core.AppKeybindingModelsSave)),
		countText,
	}
	if s.isDirty {
		return theme.Fg("dim", fmt.Sprintf("  %s ", joinSeparator(parts, " · "))) + theme.Fg("warning", "(unsaved)")
	}
	return theme.Fg("dim", fmt.Sprintf("  %s", joinSeparator(parts, " · ")))
}

func joinSeparator(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

func (s *ScopedModelsSelectorComponent) refresh() {
	query := s.searchInput.GetValue()
	items := s.buildItems()
	if query != "" {
		s.filteredItems = tui.FuzzyFilter(items, query, func(item *scopedModelItem) string {
			if item.model != nil {
				return GetModelSearchText(ModelSearchItem{ID: item.model.ID, Provider: string(item.model.Provider), Name: item.model.Name})
			}
			return item.fullID
		})
	} else {
		s.filteredItems = items
	}
	s.selectedIndex = minInt(s.selectedIndex, maxInt(0, len(s.filteredItems)-1))
	s.updateList()
	s.footerText.SetText(s.getFooterText())
}

func (s *ScopedModelsSelectorComponent) notifyChange() {
	if s.callbacks.OnChange == nil {
		return
	}
	if s.enabledIds == nil {
		s.callbacks.OnChange(nil)
		return
	}
	s.callbacks.OnChange(append([]string{}, s.enabledIds...))
}

func (s *ScopedModelsSelectorComponent) updateList() {
	s.listContainer.Clear()

	if len(s.filteredItems) == 0 {
		s.listContainer.AddChild(tui.NewText(theme.Fg("muted", "  No matching models"), 0, 0))
		return
	}

	startIndex := maxInt(0, minInt(
		s.selectedIndex-s.maxVisible/2,
		len(s.filteredItems)-s.maxVisible,
	))
	endIndex := minInt(startIndex+s.maxVisible, len(s.filteredItems))
	allEnabled := s.enabledIds == nil

	for i := startIndex; i < endIndex; i++ {
		item := s.filteredItems[i]
		isSelected := i == s.selectedIndex
		prefix := "  "
		if isSelected {
			prefix = theme.Fg("accent", "→ ")
		}
		id := item.fullID
		if item.model != nil {
			id = item.model.ID
		}
		modelText := id
		if isSelected {
			modelText = theme.Fg("accent", id)
		}
		providerBadgeText := " [unavailable]"
		if item.model != nil {
			providerBadgeText = fmt.Sprintf(" [%s]", item.model.Provider)
		}
		providerBadge := theme.Fg("muted", providerBadgeText)
		status := ""
		if item.model == nil {
			status = theme.Fg("dim", " ✗")
		} else if !allEnabled {
			if item.enabled {
				status = theme.Fg("success", " ✓")
			} else {
				status = theme.Fg("dim", " ✗")
			}
		}
		s.listContainer.AddChild(tui.NewText(prefix+modelText+providerBadge+status, 0, 0))
	}

	// Add scroll indicator if needed
	if startIndex > 0 || endIndex < len(s.filteredItems) {
		s.listContainer.AddChild(tui.NewText(
			theme.Fg("muted", fmt.Sprintf("  (%d/%d)", s.selectedIndex+1, len(s.filteredItems))), 0, 0))
	}

	if len(s.filteredItems) > 0 {
		selected := s.filteredItems[s.selectedIndex]
		s.listContainer.AddChild(tui.NewSpacer(1))
		name := "Model unavailable"
		if selected.model != nil {
			name = fmt.Sprintf("Model Name: %s", selected.model.Name)
		}
		s.listContainer.AddChild(tui.NewText(theme.Fg("muted", fmt.Sprintf("  %s", name)), 0, 0))
	}
}

// HandleInput implements InputHandler.
func (s *ScopedModelsSelectorComponent) HandleInput(data string) {
	kb := tui.GetKeybindings()

	// Navigation
	if kb.Matches(data, "tui.select.up") {
		if len(s.filteredItems) == 0 {
			return
		}
		if s.selectedIndex == 0 {
			s.selectedIndex = len(s.filteredItems) - 1
		} else {
			s.selectedIndex--
		}
		s.updateList()
		return
	}
	if kb.Matches(data, "tui.select.down") {
		if len(s.filteredItems) == 0 {
			return
		}
		if s.selectedIndex == len(s.filteredItems)-1 {
			s.selectedIndex = 0
		} else {
			s.selectedIndex++
		}
		s.updateList()
		return
	}

	// Reorder enabled models
	reorderUp := kb.Matches(data, core.AppKeybindingModelsReorderUp)
	reorderDown := kb.Matches(data, core.AppKeybindingModelsReorderDown)
	if reorderUp || reorderDown {
		if s.enabledIds == nil {
			return
		}
		if s.selectedIndex < len(s.filteredItems) {
			item := s.filteredItems[s.selectedIndex]
			if isEnabled(s.enabledIds, item.fullID) {
				delta := 1
				if reorderUp {
					delta = -1
				}
				currentIndex := indexOfString(s.enabledIds, item.fullID)
				newIndex := currentIndex + delta
				// Only move if within bounds
				if newIndex >= 0 && newIndex < len(s.enabledIds) {
					s.enabledIds = moveEnabled(s.enabledIds, item.fullID, delta)
					s.isDirty = true
					s.selectedIndex += delta
					s.refresh()
					s.notifyChange()
				}
			}
		}
		return
	}

	// Toggle on Enter
	if kb.Matches(data, "tui.select.confirm") {
		if s.selectedIndex < len(s.filteredItems) {
			item := s.filteredItems[s.selectedIndex]
			s.enabledIds = toggleEnabled(s.enabledIds, item.fullID)
			s.isDirty = true
			s.refresh()
			s.notifyChange()
		}
		return
	}

	// Enable all (filtered if search active, otherwise all)
	if kb.Matches(data, core.AppKeybindingModelsEnableAll) {
		var targetIds []string
		if s.searchInput.GetValue() != "" {
			for _, i := range s.filteredItems {
				targetIds = append(targetIds, i.fullID)
			}
		}
		s.enabledIds = enableAllIds(s.enabledIds, s.allIds, targetIds)
		s.isDirty = true
		s.refresh()
		s.notifyChange()
		return
	}

	// Clear all (filtered if search active, otherwise all)
	if kb.Matches(data, core.AppKeybindingModelsClearAll) {
		var targetIds []string
		if s.searchInput.GetValue() != "" {
			for _, i := range s.filteredItems {
				targetIds = append(targetIds, i.fullID)
			}
		}
		s.enabledIds = clearAllIds(s.enabledIds, s.allIds, targetIds)
		s.isDirty = true
		s.refresh()
		s.notifyChange()
		return
	}

	// Toggle provider of current item
	if kb.Matches(data, core.AppKeybindingModelsToggleProvider) {
		if s.selectedIndex < len(s.filteredItems) && s.filteredItems[s.selectedIndex].model != nil {
			item := s.filteredItems[s.selectedIndex]
			provider := string(item.model.Provider)
			providerIds := []string{}
			for _, id := range s.allIds {
				if m, ok := s.modelsById[id]; ok && string(m.Provider) == provider {
					providerIds = append(providerIds, id)
				}
			}
			allEnabled := true
			for _, id := range providerIds {
				if !isEnabled(s.enabledIds, id) {
					allEnabled = false
					break
				}
			}
			if allEnabled {
				s.enabledIds = clearAllIds(s.enabledIds, s.allIds, providerIds)
			} else {
				s.enabledIds = enableAllIds(s.enabledIds, s.allIds, providerIds)
			}
			s.isDirty = true
			s.refresh()
			s.notifyChange()
		}
		return
	}

	// Save/persist to settings
	if kb.Matches(data, core.AppKeybindingModelsSave) {
		if s.callbacks.OnPersist != nil {
			if s.enabledIds == nil {
				s.callbacks.OnPersist(nil)
			} else {
				s.callbacks.OnPersist(append([]string{}, s.enabledIds...))
			}
		}
		s.isDirty = false
		s.footerText.SetText(s.getFooterText())
		return
	}

	// Ctrl+C - clear search or cancel if empty
	if tui.MatchesKey(data, tui.KeyCtrl("c")) {
		if s.searchInput.GetValue() != "" {
			s.searchInput.SetValue("")
			s.refresh()
		} else if s.callbacks.OnCancel != nil {
			s.callbacks.OnCancel()
		}
		return
	}

	// Escape - cancel
	if tui.MatchesKey(data, tui.KeyEscape) {
		if s.callbacks.OnCancel != nil {
			s.callbacks.OnCancel()
		}
		return
	}

	// Pass everything else to search input
	s.searchInput.HandleInput(data)
	s.refresh()
}

// GetSearchInput exposes the search input (pi: getSearchInput).
func (s *ScopedModelsSelectorComponent) GetSearchInput() *tui.Input { return s.searchInput }
