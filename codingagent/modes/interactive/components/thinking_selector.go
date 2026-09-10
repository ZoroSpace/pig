// thinking_selector.go (pi: components/thinking-selector.ts) — the
// /thinking level picker with search filtering and Ctrl+S set-as-default.
package components

import (
	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// thinkingSelectListLayout is the fixed layout (pi:
// THINKING_SELECT_LIST_LAYOUT).
var thinkingSelectListLayout = &tui.SelectListLayoutOptions{
	MinPrimaryColumnWidth: 12,
	MaxPrimaryColumnWidth: 32,
}

// thinkingLevelDescriptions renders per-level descriptions (pi:
// LEVEL_DESCRIPTIONS).
var thinkingLevelDescriptions = map[agent.ThinkingLevel]string{
	types.ModelThinkingOff:     "No reasoning",
	types.ModelThinkingMinimal: "Very brief reasoning (~1k tokens)",
	types.ModelThinkingLow:     "Light reasoning (~2k tokens)",
	types.ModelThinkingMedium:  "Moderate reasoning (~8k tokens)",
	types.ModelThinkingHigh:    "Deep reasoning (~16k tokens)",
	types.ModelThinkingXHigh:   "Extra-high reasoning (~32k tokens)",
	types.ModelThinkingMax:     "Maximum reasoning",
}

// ThinkingSelectorComponent renders a thinking level selector with borders
// (pi: ThinkingSelectorComponent extends Container implements Focusable).
type ThinkingSelectorComponent struct {
	tui.Container

	searchInput          *tui.Input
	selectList           *tui.SelectList
	selectListChildIndex int
	allItems             []tui.SelectItem
	onSelect             func(level agent.ThinkingLevel)
	onCancel             func()
	onSelectAsDefault    func(level agent.ThinkingLevel)

	focused bool
}

// NewThinkingSelectorComponent builds the selector (pi: constructor;
// onSelectAsDefault/defaultThinkingLevel optional — nil/"").
func NewThinkingSelectorComponent(
	currentLevel agent.ThinkingLevel,
	availableLevels []agent.ThinkingLevel,
	onSelect func(level agent.ThinkingLevel),
	onCancel func(),
	onSelectAsDefault func(level agent.ThinkingLevel),
	defaultThinkingLevel agent.ThinkingLevel,
) *ThinkingSelectorComponent {
	s := &ThinkingSelectorComponent{
		Container:         tui.Container{},
		onSelect:          onSelect,
		onCancel:          onCancel,
		onSelectAsDefault: onSelectAsDefault,
	}

	s.allItems = make([]tui.SelectItem, 0, len(availableLevels))
	for _, level := range availableLevels {
		description := thinkingLevelDescriptions[level]
		if level == defaultThinkingLevel {
			description = description + " · default"
		}
		// Mark the current level in the label (v0.85.1) so it stays visible
		// while browsing.
		label := "  " + string(level)
		if level == currentLevel {
			label = "✓ " + string(level)
		}
		s.allItems = append(s.allItems, tui.SelectItem{
			Value:       string(level),
			Label:       label,
			Description: description,
		})
	}

	// Add top border
	s.AddChild(NewDynamicBorder(nil))
	s.AddChild(tui.NewSpacer(1))
	s.AddChild(tui.NewText("Thinking Level", 0, 0))
	s.AddChild(tui.NewSpacer(1))
	s.AddChild(tui.NewText(
		coretools.KeyDisplayText(core.AppKeybindingThinkingCycle)+" cycles thinking levels in-session", 0, 0))
	s.AddChild(tui.NewSpacer(1))

	s.searchInput = tui.NewInput()
	s.searchInput.OnSubmit = func(_ string) { s.selectList.HandleInput("\r") }
	s.AddChild(s.searchInput)
	s.AddChild(tui.NewSpacer(1))

	// Create selector
	s.selectList = s.buildSelectList(s.allItems, currentLevel)
	s.selectListChildIndex = len(s.Children)
	s.AddChild(s.selectList)
	s.AddChild(tui.NewSpacer(1))
	s.AddChild(tui.NewText(theme.Fg("dim",
		"  "+coretools.KeyDisplayText("tui.select.confirm")+" to select · "+
			coretools.KeyDisplayText(core.AppKeybindingThinkingSave)+" to set as default · "+
			coretools.KeyDisplayText("tui.select.cancel")+" to cancel"), 0, 0))

	// Add bottom border
	s.AddChild(NewDynamicBorder(nil))
	return s
}

// GetFocused/SetFocused implement Focusable - propagate to searchInput for
// IME cursor positioning.
func (s *ThinkingSelectorComponent) GetFocused() bool { return s.focused }
func (s *ThinkingSelectorComponent) SetFocused(value bool) {
	s.focused = value
	s.searchInput.SetFocused(value)
}

func (s *ThinkingSelectorComponent) buildSelectList(items []tui.SelectItem, preselect agent.ThinkingLevel) *tui.SelectList {
	list := tui.NewSelectList(items, maxInt(1, len(items)), theme.GetSelectListTheme(), thinkingSelectListLayout)
	for i, item := range items {
		if item.Value == string(preselect) {
			list.SetSelectedIndex(i)
			break
		}
	}
	list.OnSelect = func(item tui.SelectItem) { s.onSelect(agent.ThinkingLevel(item.Value)) }
	list.OnCancel = func() { s.onCancel() }
	return list
}

func (s *ThinkingSelectorComponent) applyFilter(query string) {
	filtered := s.allItems
	if query != "" {
		// v0.85.1 matches on the raw value instead of the label so the ✓
		// marker does not pollute fuzzy scoring.
		filtered = tui.FuzzyFilter(s.allItems, query, func(item tui.SelectItem) string {
			return item.Value + " " + item.Description
		})
	}
	selectedValue := agent.ThinkingLevel("")
	if selected := s.selectList.GetSelectedItem(); selected != nil {
		selectedValue = agent.ThinkingLevel(selected.Value)
	}
	newList := s.buildSelectList(filtered, selectedValue)
	s.Children[s.selectListChildIndex] = newList
	s.selectList = newList
}

// HandleInput implements InputHandler.
func (s *ThinkingSelectorComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	if kb.Matches(keyData, core.AppKeybindingThinkingSave) && s.onSelectAsDefault != nil {
		if item := s.selectList.GetSelectedItem(); item != nil {
			s.onSelectAsDefault(agent.ThinkingLevel(item.Value))
		}
		return
	}

	isNav := kb.Matches(keyData, "tui.select.up") ||
		kb.Matches(keyData, "tui.select.down") ||
		kb.Matches(keyData, "tui.select.confirm") ||
		kb.Matches(keyData, "tui.select.cancel")
	if isNav {
		s.selectList.HandleInput(keyData)
		return
	}

	s.searchInput.HandleInput(keyData)
	s.applyFilter(s.searchInput.GetValue())
}

// GetSelectList exposes the inner list (pi: getSelectList).
func (s *ThinkingSelectorComponent) GetSelectList() *tui.SelectList { return s.selectList }
